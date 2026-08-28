//go:build darwin || linux

package installation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type relayLifecycleLock struct {
	file *os.File
}

func withRelayLifecycleLock(ctx context.Context, details platformInstallation, operation func() error) (resultErr error) {
	if details.LifecycleLockPath == "" {
		return operation()
	}
	lock, err := acquireRelayLifecycleLock(ctx, details)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lock.close())
	}()
	return operation()
}

func acquireRelayLifecycleLock(ctx context.Context, details platformInstallation) (*relayLifecycleLock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := details.LifecycleLockPath
	if err := ensureArtifactDirectory(filepath.Dir(path), details.ArtifactUID, details.ArtifactGID); err != nil {
		return nil, fmt.Errorf("prepare relay lifecycle lock: %w", err)
	}
	descriptor, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open relay lifecycle lock: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	closeWithError := func(err error) (*relayLifecycleLock, error) {
		return nil, errors.Join(err, file.Close())
	}
	info, err := file.Stat()
	if err != nil {
		return closeWithError(fmt.Errorf("inspect relay lifecycle lock: %w", err))
	}
	uid, gid, ownerKnown := artifactOwner(info)
	if !info.Mode().IsRegular() || !ownerKnown || uid != details.ArtifactUID || gid != details.ArtifactGID || info.Mode().Perm() != 0o600 {
		return closeWithError(fmt.Errorf("relay lifecycle lock %s must be a mode-0600 regular file owned by UID %d and GID %d", path, details.ArtifactUID, details.ArtifactGID))
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return closeWithError(fmt.Errorf("sync relay lifecycle lock: %w", err))
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		err = unix.Flock(descriptor, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &relayLifecycleLock{file: file}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return closeWithError(fmt.Errorf("lock relay lifecycle: %w", err))
		}
		select {
		case <-ctx.Done():
			return closeWithError(fmt.Errorf("wait for another relay lifecycle operation: %w", ctx.Err()))
		case <-ticker.C:
		}
	}
}

func (lock *relayLifecycleLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	descriptor := int(lock.file.Fd())
	return errors.Join(unix.Flock(descriptor, unix.LOCK_UN), lock.file.Close())
}
