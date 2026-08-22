package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

// resetApplicationState serializes against daemon startup, verifies and
// stops the current daemon, removes application state, and starts an empty
// daemon. Callers must stop authenticated process supervisors and remove
// installation-owned container resources through the daemon API first.
func (m *Manager) resetApplicationState(ctx context.Context, force bool) (installation.ResetStateResult, identity.Record, error) {
	paths := m.layout
	if err := installation.EnsurePrivateDirectory(paths.Root); err != nil {
		return installation.ResetStateResult{}, identity.Record{}, err
	}
	lock, err := os.OpenFile(paths.StartupLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return installation.ResetStateResult{}, identity.Record{}, err
	}
	defer lock.Close()
	if err := m.acquireResetLock(ctx, lock); err != nil {
		return installation.ResetStateResult{}, identity.Record{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	inspection, inspectErr := m.inspectDaemon(ctx)
	if inspectErr == nil {
		if len(inspection.Identity.ActiveEnvironments) > 0 && !force {
			return installation.ResetStateResult{}, identity.Record{}, &ActiveEnvironmentsError{Environments: append([]string(nil), inspection.Identity.ActiveEnvironments...)}
		}
		if _, err := m.stopVerifiedDaemon(ctx, inspection, StopOptions{Force: force, Timeout: 15 * time.Second}, true, "reset application state"); err != nil {
			return installation.ResetStateResult{}, identity.Record{}, err
		}
	} else if err := m.prepareStoppedDaemonForReset(ctx, inspectErr); err != nil {
		return installation.ResetStateResult{}, identity.Record{}, err
	}

	result, err := installation.ResetApplicationState(paths)
	if err != nil {
		return result, identity.Record{}, err
	}
	if err := m.hooks.StartDaemon(paths); err != nil {
		return result, identity.Record{}, fmt.Errorf("application state was erased, but the daemon could not restart: %w", err)
	}
	record, err := m.waitForResetDaemon(ctx)
	if err != nil {
		return result, identity.Record{}, fmt.Errorf("application state was erased, but the replacement daemon is not ready: %w", err)
	}
	return result, record, err
}

func (m *Manager) acquireResetLock(ctx context.Context, lock *os.File) error {
	deadline := m.hooks.Now().Add(65 * time.Second)
	for {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return nil
		}
		if m.hooks.Now().After(deadline) {
			return errors.New("timed out waiting for another Portless CLI to release the daemon startup lock")
		}
		if err := m.hooks.Wait(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

func (m *Manager) prepareStoppedDaemonForReset(ctx context.Context, inspectErr error) error {
	paths := m.layout
	record, recordErr := identity.Read(paths)
	if errors.Is(recordErr, os.ErrNotExist) {
		return verifyDaemonInstanceStopped(paths)
	}
	if recordErr != nil {
		return fmt.Errorf("inspect daemon before reset: %w", inspectErr)
	}
	alive, err := m.hooks.ProcessAlive(record.PID)
	if err != nil {
		return err
	}
	if alive {
		return unverifiedDaemonError(record, inspectErr)
	}
	identity.RemoveMatching(paths, record)
	return verifyDaemonInstanceStopped(paths)
}

func verifyDaemonInstanceStopped(paths installation.Layout) error {
	lock, err := os.OpenFile(paths.InstanceLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon instance lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("the daemon instance lock is still held; refusing to erase application state without an authenticated daemon shutdown")
	}
	return syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
}

func (m *Manager) waitForResetDaemon(ctx context.Context) (identity.Record, error) {
	paths := m.layout
	deadline := m.hooks.Now().Add(60 * time.Second)
	var lastError error
	for m.hooks.Now().Before(deadline) {
		inspection, err := m.inspectDaemon(ctx)
		if err == nil && inspection.Compatible && inspection.CurrentBuild {
			return inspection.Record, nil
		}
		if err != nil {
			lastError = err
		} else {
			lastError = incompatibleDaemonError(inspection)
		}
		if err := m.hooks.Wait(ctx, 100*time.Millisecond); err != nil {
			return identity.Record{}, err
		}
	}
	message := "daemon did not become ready after reset; inspect " + paths.DaemonLog
	if lastError != nil {
		message += ": " + lastError.Error()
	} else if tail := readLogTail(paths.DaemonLog, 4096); tail != "" {
		message += ": " + tail
	}
	return identity.Record{}, errors.New(message)
}
