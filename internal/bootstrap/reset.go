package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// ResetDaemonApplicationState serializes against daemon startup, verifies and
// stops the current daemon, removes application state, and starts an empty
// daemon. Callers must stop authenticated process supervisors and remove
// installation-owned container resources through the daemon API first.
func ResetDaemonApplicationState(ctx context.Context, paths Paths, force bool) (ResetStateResult, ControlRecord, error) {
	if err := ensurePrivateDirectory(paths.Root); err != nil {
		return ResetStateResult{}, ControlRecord{}, err
	}
	lock, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ResetStateResult{}, ControlRecord{}, err
	}
	defer lock.Close()
	if err := acquireResetLock(ctx, lock); err != nil {
		return ResetStateResult{}, ControlRecord{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	inspection, inspectErr := InspectDaemon(ctx, paths)
	if inspectErr == nil {
		if len(inspection.Identity.ActiveEnvironments) > 0 && !force {
			return ResetStateResult{}, ControlRecord{}, &ActiveEnvironmentsError{Environments: append([]string(nil), inspection.Identity.ActiveEnvironments...)}
		}
		if _, err := stopVerifiedDaemon(ctx, paths, inspection, StopOptions{Timeout: 15 * time.Second}, true, "reset application state"); err != nil {
			return ResetStateResult{}, ControlRecord{}, err
		}
	} else if err := prepareStoppedDaemonForReset(ctx, paths, inspectErr); err != nil {
		return ResetStateResult{}, ControlRecord{}, err
	}

	result, err := ResetApplicationState(paths)
	if err != nil {
		return result, ControlRecord{}, err
	}
	if err := startDaemon(paths); err != nil {
		return result, ControlRecord{}, fmt.Errorf("application state was erased, but the daemon could not restart: %w", err)
	}
	record, err := waitForResetDaemon(ctx, paths)
	if err != nil {
		return result, ControlRecord{}, fmt.Errorf("application state was erased, but the replacement daemon is not ready: %w", err)
	}
	return result, record, err
}

func acquireResetLock(ctx context.Context, lock *os.File) error {
	deadline := time.Now().Add(65 * time.Second)
	for {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for another Portless CLI to release the daemon startup lock")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func prepareStoppedDaemonForReset(ctx context.Context, paths Paths, inspectErr error) error {
	record, recordErr := ReadControl(paths)
	if errors.Is(recordErr, os.ErrNotExist) {
		return verifyDaemonInstanceStopped(paths)
	}
	if recordErr != nil {
		return fmt.Errorf("inspect daemon before reset: %w", inspectErr)
	}
	alive, err := processIsAlive(record.PID)
	if err != nil {
		return err
	}
	if alive {
		return unverifiedDaemonError(record, inspectErr)
	}
	removeMatchingControl(paths, record)
	return verifyDaemonInstanceStopped(paths)
}

func verifyDaemonInstanceStopped(paths Paths) error {
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

func waitForResetDaemon(ctx context.Context, paths Paths) (ControlRecord, error) {
	deadline := time.Now().Add(60 * time.Second)
	var lastError error
	for time.Now().Before(deadline) {
		inspection, err := InspectDaemon(ctx, paths)
		if err == nil && inspection.Compatible && inspection.CurrentBuild {
			return inspection.Record, nil
		}
		if err != nil {
			lastError = err
		} else {
			lastError = incompatibleDaemonError(inspection)
		}
		select {
		case <-ctx.Done():
			return ControlRecord{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	message := "daemon did not become ready after reset; inspect " + paths.DaemonLog
	if lastError != nil {
		message += ": " + lastError.Error()
	} else if tail := readLogTail(paths.DaemonLog, 4096); tail != "" {
		message += ": " + tail
	}
	return ControlRecord{}, errors.New(message)
}
