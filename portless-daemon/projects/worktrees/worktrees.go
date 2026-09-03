// Package worktrees prepares independent, local checkout snapshots for
// environment startup. It never runs discovery or application setup commands.
package worktrees

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Repository identifies the checkout and committed base of a local source.
type Repository struct {
	Root   string
	Commit string
}

// Inspect finds the containing Git checkout, including for a source nested in
// a monorepo or an existing linked worktree. A committed base is required.
func Inspect(ctx context.Context, source string) (Repository, error) {
	root, err := git(ctx, source, "rev-parse", "--show-toplevel")
	if err != nil {
		return Repository{}, fmt.Errorf("find source Git checkout: %w", err)
	}
	root, err = filepath.EvalSymlinks(strings.TrimSpace(root))
	if err != nil {
		return Repository{}, fmt.Errorf("resolve source Git checkout: %w", err)
	}
	commit, err := git(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Repository{}, errors.New("automatic checkout preparation requires a Git repository with at least one commit")
	}
	return Repository{Root: root, Commit: strings.TrimSpace(commit)}, nil
}

// Create registers a detached worktree below directory and snapshots the
// current files, including uncommitted changes and ignored local dependencies.
// Files are copied independently; Git history is shared. The returned checkout
// is durable and must not be deleted when its environment stops or is forgotten.
func Create(ctx context.Context, directory, prefix string, repository Repository) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if filepath.Base(prefix) != prefix || prefix == "." || prefix == ".." {
		return "", errors.New("invalid environment checkout name")
	}
	if within(repository.Root, directory) {
		return "", errors.New("environment checkouts directory must be outside the source repository")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create environment checkouts directory: %w", err)
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	if within(repository.Root, directory) {
		return "", errors.New("environment checkouts directory must be outside the source repository")
	}
	checkout, err := os.MkdirTemp(directory, prefix+"-")
	if err != nil {
		return "", fmt.Errorf("allocate environment checkout: %w", err)
	}
	// Only this invocation's unpublished directory may be rolled back. Never
	// remove a previously returned worktree, which may contain a user's edits.
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = git(cleanup, repository.Root, "worktree", "remove", "--force", checkout)
		_ = os.RemoveAll(checkout)
	}()
	if _, err := git(ctx, repository.Root, "worktree", "add", "--detach", "--no-checkout", checkout, repository.Commit); err != nil {
		return "", fmt.Errorf("create environment Git worktree: %w", err)
	}
	// Populate only the new index. Copying the live filesystem instead of
	// checking out HEAD preserves deleted, modified, untracked and ignored files
	// without touching the source's index, branch, hooks, or checkout filters.
	if _, err := git(ctx, checkout, "read-tree", repository.Commit); err != nil {
		return "", fmt.Errorf("prepare environment Git index: %w", err)
	}
	if err := snapshot(ctx, repository.Root, checkout); err != nil {
		return "", fmt.Errorf("copy environment checkout: %w", err)
	}
	complete = true
	return checkout, nil
}

const (
	maxSnapshotFiles = 1_000_000
	maxSnapshotBytes = int64(10 << 30)
)

func snapshot(ctx context.Context, source, destination string) error {
	input, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer output.Close()
	files, remaining := 0, maxSnapshotBytes
	return fs.WalkDir(input.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if entry.Name() == ".git" {
			if name != ".git" {
				return fmt.Errorf("nested Git repository at %s needs its own source checkout", filepath.Dir(name))
			}
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		files++
		if files > maxSnapshotFiles {
			return errors.New("automatic checkout exceeds one million files; use a smaller source checkout")
		}
		if entry.IsDir() {
			return output.Mkdir(name, 0o700)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := input.Readlink(name)
			if err != nil {
				return err
			}
			// Absolute links back into the source must point into the independent
			// copy. External links are retained, never traversed while copying.
			if filepath.IsAbs(target) && within(source, target) {
				relative, _ := filepath.Rel(source, target)
				target, err = filepath.Rel(filepath.Dir(filepath.FromSlash(name)), relative)
				if err != nil {
					return err
				}
			}
			return output.Symlink(target, name)
		}
		// A running checkout can contain sockets, FIFOs and device files. They
		// are runtime state, not files to reopen or recreate in another environment.
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyFile(ctx, input, output, name, &remaining)
	})
}

func copyFile(ctx context.Context, input, output *os.Root, name string, remaining *int64) error {
	source, err := input.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source file %s changed while preparing the checkout; retry startup", name)
	}
	if info.Size() > *remaining {
		return errors.New("automatic checkout exceeds 10 GiB; use a smaller source checkout")
	}
	destination, err := output.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(destination, io.LimitReader(contextReader{ctx, source}, *remaining+1))
	closeErr := destination.Close()
	*remaining -= n
	if *remaining < 0 {
		return errors.New("automatic checkout exceeds 10 GiB; use a smaller source checkout")
	}
	return errors.Join(copyErr, closeErr)
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

// Read checks the preparation deadline before reading another file chunk.
func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(buffer)
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func git(ctx context.Context, directory string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-C", directory}
	command := exec.CommandContext(ctx, "git", append(args, arguments...)...)
	command.WaitDelay = time.Second
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr limitedOutput
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", arguments[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

type limitedOutput struct{ buffer bytes.Buffer }

// String returns the bounded output retained from a Git command.
func (b *limitedOutput) String() string { return b.buffer.String() }

// Write drains Git output while retaining at most eight KiB.
func (b *limitedOutput) Write(value []byte) (int, error) {
	n := len(value)
	if remaining := 8192 - b.buffer.Len(); remaining > 0 {
		_, _ = b.buffer.Write(value[:min(remaining, n)])
	}
	return n, nil
}
