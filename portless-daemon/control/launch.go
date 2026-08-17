package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/portless-run/portless/portless-daemon/identity"
	"github.com/portless-run/portless/portless-daemon/system/installation"
)

func (m *Manager) ensureDaemon(ctx context.Context) (identity.Record, error) {
	paths := m.layout
	if err := installation.EnsurePrivateDirectory(paths.Root); err != nil {
		return identity.Record{}, err
	}
	if inspection, err := m.inspectDaemon(ctx); err == nil && inspection.Compatible {
		if inspection.CurrentBuild || len(inspection.Identity.ActiveEnvironments) > 0 && !inspection.Identity.HandoffReady {
			return inspection.Record, nil
		}
	}
	lock, err := os.OpenFile(paths.StartupLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return identity.Record{}, err
	}
	defer lock.Close()
	lockDeadline := m.hooks.Now().Add(65 * time.Second)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if inspection, inspectErr := m.inspectDaemon(ctx); inspectErr == nil && inspection.Compatible {
			if inspection.CurrentBuild || len(inspection.Identity.ActiveEnvironments) > 0 && !inspection.Identity.HandoffReady {
				return inspection.Record, nil
			}
		}
		if m.hooks.Now().After(lockDeadline) {
			return identity.Record{}, errors.New("timed out waiting for another Portless CLI to prepare the daemon")
		}
		if err := m.hooks.Wait(ctx, 100*time.Millisecond); err != nil {
			return identity.Record{}, err
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	inspection, inspectErr := m.inspectDaemon(ctx)
	if inspectErr == nil {
		if inspection.Compatible && inspection.CurrentBuild {
			return inspection.Record, nil
		}
		if inspection.Compatible && len(inspection.Identity.ActiveEnvironments) > 0 && !inspection.Identity.HandoffReady {
			return inspection.Record, nil
		}
		if _, err := m.stopVerifiedDaemon(ctx, inspection, StopOptions{Timeout: 15 * time.Second, Handoff: true}, false, "replace an outdated daemon"); err != nil {
			return identity.Record{}, err
		}
	} else {
		record, recordErr := identity.Read(paths)
		switch {
		case recordErr == nil:
			alive, aliveErr := m.hooks.ProcessAlive(record.PID)
			if aliveErr != nil {
				return identity.Record{}, fmt.Errorf("inspect recorded daemon process %d: %w", record.PID, aliveErr)
			}
			if alive {
				return identity.Record{}, unverifiedDaemonError(record, inspectErr)
			}
			identity.RemoveMatching(paths, record)
		case errors.Is(recordErr, os.ErrNotExist):
			// There is no daemon to replace.
		default:
			return identity.Record{}, fmt.Errorf("the daemon control record is invalid; refusing to start a second daemon: %w", recordErr)
		}
	}

	if err := m.hooks.StartDaemon(paths); err != nil {
		return identity.Record{}, err
	}
	// Reconciliation verifies each surviving process/container and restores its
	// dependency listeners before the daemon publishes readiness. A multi-service
	// environment can legitimately take longer than the old process-spawn-only
	// startup budget.
	startupDeadline := m.hooks.Now().Add(60 * time.Second)
	var lastError error
	for m.hooks.Now().Before(startupDeadline) {
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
	message := "daemon did not become ready; inspect " + paths.DaemonLog
	if lastError != nil {
		message += ": " + lastError.Error()
	} else if tail := readLogTail(paths.DaemonLog, 4096); tail != "" {
		message += ": " + tail
	}
	return identity.Record{}, errors.New(message)
}

func startDaemonProcess(paths installation.Layout) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.DaemonLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	command := exec.Command(executable, "__daemon", "--data-dir", paths.Root)
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Portless daemon: %w", err)
	}
	return command.Process.Release()
}

func readLogTail(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := info.Size() - limit
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	content, _ := io.ReadAll(io.LimitReader(file, limit))
	return strings.TrimSpace(string(content))
}
