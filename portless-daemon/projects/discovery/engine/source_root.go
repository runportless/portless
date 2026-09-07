package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverWithin confines root selection and discovery reads to an authorized directory handle.
// Descendant symlinks cannot escape that handle, including if a path changes during discovery.
func (e *Engine) DiscoverWithin(ctx context.Context, start, allowedRoot string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, e.config.ScanTimeout)
	defer cancel()
	if !filepath.IsAbs(start) || !filepath.IsAbs(allowedRoot) {
		return Result{}, errors.New("confined discovery requires absolute source and root paths")
	}
	allowedRoot = filepath.Clean(allowedRoot)
	relative, err := filepath.Rel(allowedRoot, start)
	if err != nil || !filepath.IsLocal(relative) {
		return Result{}, errors.New("source is outside its authorized discovery root")
	}
	canonical, err := filepath.EvalSymlinks(allowedRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve authorized discovery root: %w", err)
	}
	if canonical != allowedRoot {
		return Result{}, errors.New("authorized discovery root changed; restart the MCP server with its current canonical path")
	}
	root, err := openCanonicalRoot(allowedRoot)
	if err != nil {
		return Result{}, fmt.Errorf("open authorized discovery root: %w", err)
	}
	defer root.Close()
	info, err := root.Stat(relative)
	if err != nil {
		return Result{}, fmt.Errorf("inspect confined discovery source: %w", err)
	}
	if !info.IsDir() {
		relative = filepath.Dir(relative)
	}
	for current := relative; ; current = filepath.Dir(current) {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		found := false
		for _, marker := range e.markers {
			_, err := root.Lstat(filepath.Join(current, marker))
			if err == nil {
				found = true
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return Result{}, fmt.Errorf("inspect confined root marker: %w", err)
			}
		}
		if found {
			handle, err := root.OpenRoot(current)
			if err != nil {
				return Result{}, fmt.Errorf("open confined project root: %w", err)
			}
			workspace, err := openWorkspaceHandle(ctx, filepath.Join(allowedRoot, current), handle, e.config.Limits)
			if err != nil {
				return Result{}, err
			}
			defer workspace.Close()
			return e.discoverWorkspace(ctx, workspace)
		}
		if current == "." {
			break
		}
	}
	return Result{}, errors.New("no project root found inside the authorized source root; an ancestor outside it cannot be scanned")
}

// Open each canonical component through a pinned parent and compare its identity
// after opening. No symlink (including a concurrent ancestor swap) can redirect
// the authorization root to a different directory.
func openCanonicalRoot(path string) (*os.Root, error) {
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		info, err := root.Lstat(component)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, errors.New("authorized discovery root changed or contains a symlink")
		}
		next, err := root.OpenRoot(component)
		root.Close()
		if err != nil {
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, errors.New("authorized discovery root changed while opening")
		}
		root = next
	}
	return root, nil
}
