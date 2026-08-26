// Package daemonlog owns bounded, read-only inspection of the Portless daemon log.
package daemonlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
)

const maxSnapshotBytes int64 = 256 << 10

// Snapshot is one bounded tail read of the daemon log.
type Snapshot struct {
	Content   string
	Truncated bool
}

// Reader reads one fixed, private daemon-log path and redacts known installation secrets.
type Reader struct {
	path       string
	limit      int64
	redactions []string
}

// NewReader constructs a reader for one fixed daemon-log path.
func NewReader(path string, secrets ...string) *Reader {
	return newReader(path, maxSnapshotBytes, secrets...)
}

func newReader(path string, limit int64, secrets ...string) *Reader {
	redactions := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, duplicate := seen[secret]; duplicate {
			continue
		}
		seen[secret] = struct{}{}
		redactions = append(redactions, secret)
	}
	sort.Slice(redactions, func(left, right int) bool { return len(redactions[left]) > len(redactions[right]) })
	return &Reader{path: path, limit: limit, redactions: redactions}
}

// Snapshot returns the latest bounded log content without following symlinks
// or reading files owned by another user.
func (r *Reader) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if r == nil || r.path == "" || r.limit <= 0 {
		return Snapshot{}, errors.New("daemon log reader is not configured")
	}
	info, err := os.Lstat(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect daemon log: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Snapshot{}, errors.New("daemon log is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Snapshot{}, fmt.Errorf("daemon log permissions %04o allow group or other access", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Snapshot{}, errors.New("daemon log ownership is unavailable")
	}
	if int(stat.Uid) != os.Geteuid() {
		return Snapshot{}, fmt.Errorf("daemon log belongs to UID %d, expected UID %d", stat.Uid, os.Geteuid())
	}
	file, err := os.Open(r.path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open daemon log: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect opened daemon log: %w", err)
	}
	if !os.SameFile(info, opened) {
		return Snapshot{}, errors.New("daemon log changed while it was being inspected")
	}
	start := opened.Size() - r.limit
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return Snapshot{}, fmt.Errorf("seek daemon log tail: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(file, r.limit))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read daemon log tail: %w", err)
	}
	truncated := start > 0
	if truncated {
		if newline := bytes.IndexByte(content, '\n'); newline >= 0 {
			content = content[newline+1:]
		}
	}
	value := strings.ToValidUTF8(string(content), "�")
	for _, secret := range r.redactions {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Content: value, Truncated: truncated}, nil
}
