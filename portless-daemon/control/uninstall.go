package control

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/portless-run/portless/portless-daemon/system/installation"
)

// removeInstallationState coordinates daemon shutdown and startup exclusion,
// then delegates the stopped-state filesystem operation to installation.
func (m *Manager) removeInstallationState(ctx context.Context, force bool) (installation.StateRemoval, error) {
	paths := m.layout
	status, err := installation.InspectState(paths)
	if err != nil {
		return installation.StateRemoval{}, err
	}
	result := installation.StateRemoval{Path: status.Path}
	if !status.Present {
		return result, nil
	}
	lock, err := os.OpenFile(paths.StartupLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return result, fmt.Errorf("open daemon startup lock: %w", err)
	}
	defer lock.Close()
	if err := m.acquireResetLock(ctx, lock); err != nil {
		return result, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	inspection, inspectErr := m.inspectDaemon(ctx)
	if inspectErr == nil {
		if len(inspection.Identity.ActiveEnvironments) > 0 && !force {
			return result, &ActiveEnvironmentsError{Environments: append([]string(nil), inspection.Identity.ActiveEnvironments...)}
		}
		if _, err := m.stopVerifiedDaemon(ctx, inspection, StopOptions{Force: force, Timeout: 15 * time.Second}, true, "uninstall Portless"); err != nil {
			return result, err
		}
	} else if err := m.prepareStoppedDaemonForReset(ctx, inspectErr); err != nil {
		return result, fmt.Errorf("prepare daemon for uninstall: %w", err)
	}
	if err := verifyDaemonInstanceStopped(paths); err != nil {
		return result, err
	}
	return installation.RemoveStoppedState(paths)
}
