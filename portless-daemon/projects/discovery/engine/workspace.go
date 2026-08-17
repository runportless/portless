package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/portless-daemon/projects/discovery/spec"
)

// Limits bounds filesystem indexing and content reads during discovery.
type Limits struct {
	MaxFiles      int
	MaxDepth      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// DefaultLimits returns conservative bounds for local source discovery.
func DefaultLimits() Limits {
	return Limits{
		MaxFiles:      50_000,
		MaxDepth:      32,
		MaxFileBytes:  2 << 20,
		MaxTotalBytes: 32 << 20,
	}
}

func normalizeLimits(input Limits) Limits {
	defaults := DefaultLimits()
	if input.MaxFiles <= 0 {
		input.MaxFiles = defaults.MaxFiles
	}
	if input.MaxDepth <= 0 {
		input.MaxDepth = defaults.MaxDepth
	}
	if input.MaxFileBytes <= 0 {
		input.MaxFileBytes = defaults.MaxFileBytes
	}
	if input.MaxTotalBytes <= 0 {
		input.MaxTotalBytes = defaults.MaxTotalBytes
	}
	return input
}

type fileStamp struct {
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

type workspace struct {
	root        string
	rootHandle  *os.Root
	limits      Limits
	files       []string
	index       map[string]fileStamp
	directories map[string]struct{}

	mu        sync.Mutex
	cache     map[string][]byte
	totalRead int64
}

var ignoredDirectories = map[string]struct{}{
	".cache": {}, ".git": {}, ".gradle": {}, ".idea": {}, ".next": {}, ".turbo": {}, ".venv": {},
	"__pycache__": {}, "build": {}, "coverage": {}, "dist": {}, "node_modules": {}, "target": {}, "vendor": {}, "venv": {},
}

func openWorkspace(ctx context.Context, root string, limits Limits) (*workspace, error) {
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open discovery root: %w", err)
	}
	result := &workspace{
		root: root, rootHandle: rootHandle, limits: normalizeLimits(limits),
		index: make(map[string]fileStamp), directories: map[string]struct{}{".": {}}, cache: make(map[string][]byte),
	}
	if err := result.indexFiles(ctx); err != nil {
		_ = rootHandle.Close()
		return nil, err
	}
	return result, nil
}

// Close releases the workspace's root directory handle.
func (w *workspace) Close() error {
	return w.rootHandle.Close()
}

// Root returns the canonical absolute workspace root.
func (w *workspace) Root() string {
	return w.root
}

// Files returns a defensive copy of sorted indexed file paths.
func (w *workspace) Files() []string {
	return append([]string(nil), w.files...)
}

// Exists reports whether an indexed regular file exists at relativePath.
func (w *workspace) Exists(relativePath string) bool {
	cleaned, ok := spec.CleanRelative(relativePath)
	if !ok {
		return false
	}
	_, exists := w.index[cleaned]
	return exists
}

// IsDir reports whether an indexed directory exists at relativePath.
func (w *workspace) IsDir(relativePath string) bool {
	cleaned, ok := spec.CleanRelative(relativePath)
	if !ok {
		return false
	}
	_, exists := w.directories[cleaned]
	return exists
}

func (w *workspace) indexFiles(ctx context.Context) error {
	err := fs.WalkDir(w.rootHandle.FS(), ".", func(relativePath string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return fmt.Errorf("scan %s: %w", relativePath, walkErr)
		}
		if relativePath == "." {
			return nil
		}
		depth := strings.Count(relativePath, "/") + 1
		if depth > w.limits.MaxDepth {
			return fmt.Errorf("discovery depth limit of %d exceeded at %s", w.limits.MaxDepth, relativePath)
		}
		if entry.IsDir() {
			if _, ignored := ignoredDirectories[entry.Name()]; ignored {
				return fs.SkipDir
			}
			w.directories[path.Clean(relativePath)] = struct{}{}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", relativePath, err)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(w.files) >= w.limits.MaxFiles {
			return fmt.Errorf("discovery file limit of %d exceeded", w.limits.MaxFiles)
		}
		cleaned := path.Clean(relativePath)
		w.files = append(w.files, cleaned)
		w.index[cleaned] = fileStamp{size: info.Size(), mode: info.Mode(), modTime: info.ModTime()}
		return nil
	})
	if err != nil {
		return fmt.Errorf("index discovery workspace: %w", err)
	}
	sort.Strings(w.files)
	return nil
}

// ReadFile safely reads an unchanged indexed file within configured byte limits.
func (w *workspace) ReadFile(ctx context.Context, relativePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cleaned, ok := spec.CleanRelative(relativePath)
	if !ok || cleaned == "." {
		return nil, fmt.Errorf("invalid workspace file %q", relativePath)
	}
	stamp, exists := w.index[cleaned]
	if !exists {
		return nil, fmt.Errorf("workspace file %s is not a regular indexed file", cleaned)
	}

	w.mu.Lock()
	if cached, ok := w.cache[cleaned]; ok {
		result := append([]byte(nil), cached...)
		w.mu.Unlock()
		return result, nil
	}
	if stamp.size > w.limits.MaxFileBytes {
		w.mu.Unlock()
		return nil, fmt.Errorf("workspace file %s exceeds the %d-byte discovery limit", cleaned, w.limits.MaxFileBytes)
	}
	if w.totalRead+stamp.size > w.limits.MaxTotalBytes {
		w.mu.Unlock()
		return nil, fmt.Errorf("discovery read limit of %d bytes exceeded", w.limits.MaxTotalBytes)
	}
	w.mu.Unlock()

	info, err := w.rootHandle.Lstat(cleaned)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace file %s: %w", cleaned, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace file %s changed to a non-regular file during discovery", cleaned)
	}
	if !sameFileStamp(stamp, info) {
		return nil, fmt.Errorf("workspace file %s changed during discovery", cleaned)
	}
	file, err := w.rootHandle.Open(cleaned)
	if err != nil {
		return nil, fmt.Errorf("open workspace file %s: %w", cleaned, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect open workspace file %s: %w", cleaned, err)
	}
	if !openedInfo.Mode().IsRegular() || !sameFileStamp(stamp, openedInfo) {
		return nil, fmt.Errorf("workspace file %s changed during discovery", cleaned)
	}
	encoded, err := io.ReadAll(io.LimitReader(file, w.limits.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read workspace file %s: %w", cleaned, err)
	}
	if int64(len(encoded)) > w.limits.MaxFileBytes {
		return nil, fmt.Errorf("workspace file %s exceeds the %d-byte discovery limit", cleaned, w.limits.MaxFileBytes)
	}
	finalInfo, err := file.Stat()
	if err != nil || !sameFileStamp(openedInfoStamp(openedInfo), finalInfo) || int64(len(encoded)) != finalInfo.Size() {
		if err == nil {
			err = errors.New("metadata changed")
		}
		return nil, fmt.Errorf("workspace file %s changed while it was read: %w", cleaned, err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if cached, ok := w.cache[cleaned]; ok {
		return append([]byte(nil), cached...), nil
	}
	if w.totalRead+int64(len(encoded)) > w.limits.MaxTotalBytes {
		return nil, fmt.Errorf("discovery read limit of %d bytes exceeded", w.limits.MaxTotalBytes)
	}
	w.totalRead += int64(len(encoded))
	w.cache[cleaned] = append([]byte(nil), encoded...)
	return append([]byte(nil), encoded...), nil
}

func openedInfoStamp(info fs.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), mode: info.Mode(), modTime: info.ModTime()}
}

func sameFileStamp(expected fileStamp, actual fs.FileInfo) bool {
	return expected.size == actual.Size() && expected.mode == actual.Mode() && expected.modTime.Equal(actual.ModTime())
}
