package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/runtime/supervisor"
)

// RecoveryState classifies the evidence available for one persisted process run.
type RecoveryState string

const (
	// RecoveryLive means the authenticated supervisor is reachable and owns the expected run.
	RecoveryLive RecoveryState = "live"
	// RecoveryTerminal means the durable supervisor state proves that the run finished.
	RecoveryTerminal RecoveryState = "terminal"
	// RecoveryGone means the durable identity matches and neither the supervisor nor process group exists.
	RecoveryGone RecoveryState = "gone"
	// RecoveryUnverifiable means Portless cannot safely adopt, replace, or forget the run.
	RecoveryUnverifiable RecoveryState = "unverifiable"
)

// PersistedRun contains the private ownership evidence needed to inspect a supervised process.
type PersistedRun struct {
	Scope            string
	Service          string
	Generation       int64
	PID              int
	SupervisorPID    int
	SupervisorSocket string
	SupervisorState  string
	PrivateRunKey    string
	LaunchMode       model.LaunchMode
	Debugger         *model.DebuggerRuntime
}

// RecoveryInspection reports whether a persisted process is live, terminal, gone, or unverifiable.
type RecoveryInspection struct {
	State  RecoveryState
	Status supervisor.Status
	Err    error
}

type recoveryHooks struct {
	liveStatus        func(context.Context, string, string) (supervisor.Status, error)
	durableStatus     func(context.Context, string, string, string) (supervisor.Status, error)
	stop              func(context.Context, string, string, string) (supervisor.Status, error)
	processAlive      func(int) (bool, error)
	processGroupAlive func(int) (bool, error)
}

func defaultRecoveryHooks() recoveryHooks {
	return recoveryHooks{
		liveStatus:        supervisor.LiveStatus,
		durableStatus:     supervisor.StatusFor,
		stop:              supervisor.Stop,
		processAlive:      recordedProcessAlive,
		processGroupAlive: recordedProcessGroupAlive,
	}
}

// InspectPersistedRun authenticates a live or durable supervisor record and proves absence before reporting a run gone.
func (m *Manager) InspectPersistedRun(ctx context.Context, expected PersistedRun) RecoveryInspection {
	hooks := m.recovery
	if hooks.liveStatus == nil {
		hooks = defaultRecoveryHooks()
	}
	return inspectPersistedRun(ctx, expected, hooks)
}

// StopPersistedRun stops an authenticated live supervisor or accepts a terminal or provably gone run.
func (m *Manager) StopPersistedRun(ctx context.Context, expected PersistedRun, timeout time.Duration) (RecoveryInspection, error) {
	inspection := m.InspectPersistedRun(ctx, expected)
	switch inspection.State {
	case RecoveryTerminal, RecoveryGone:
		return inspection, nil
	case RecoveryUnverifiable:
		return inspection, inspectionError(inspection)
	case RecoveryLive:
		stopCtx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			stopCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		status, err := m.recovery.stop(stopCtx, expected.SupervisorSocket, expected.SupervisorState, expected.PrivateRunKey)
		if err != nil {
			return RecoveryInspection{State: RecoveryUnverifiable, Status: inspection.Status, Err: err}, err
		}
		if err := validateRecoveredStatus(status, expected); err != nil {
			return RecoveryInspection{State: RecoveryUnverifiable, Status: status, Err: err}, err
		}
		if !terminalSupervisorState(status.State) {
			err := fmt.Errorf("supervisor returned non-terminal state %s after stop", status.State)
			return RecoveryInspection{State: RecoveryUnverifiable, Status: status, Err: err}, err
		}
		return RecoveryInspection{State: RecoveryTerminal, Status: status}, nil
	default:
		err := errors.New("persisted process recovery returned an invalid state")
		return RecoveryInspection{State: RecoveryUnverifiable, Err: err}, err
	}
}

func inspectPersistedRun(ctx context.Context, expected PersistedRun, hooks recoveryHooks) RecoveryInspection {
	if expected.Scope == "" || expected.Service == "" || expected.Generation <= 0 || expected.SupervisorSocket == "" || expected.SupervisorState == "" || expected.PrivateRunKey == "" {
		return RecoveryInspection{State: RecoveryUnverifiable, Err: errors.New("persisted supervisor ownership record is incomplete")}
	}
	live, liveErr := hooks.liveStatus(ctx, expected.SupervisorSocket, expected.PrivateRunKey)
	if liveErr == nil {
		if err := validateRecoveredStatus(live, expected); err != nil {
			return RecoveryInspection{State: RecoveryUnverifiable, Status: live, Err: err}
		}
		if terminalSupervisorState(live.State) {
			return RecoveryInspection{State: RecoveryTerminal, Status: live}
		}
		return RecoveryInspection{State: RecoveryLive, Status: live}
	}

	persisted, persistedErr := hooks.durableStatus(ctx, expected.SupervisorSocket, expected.SupervisorState, expected.PrivateRunKey)
	if persistedErr != nil {
		return RecoveryInspection{State: RecoveryUnverifiable, Err: fmt.Errorf("supervisor is unavailable (%v) and durable state cannot be read: %w", liveErr, persistedErr)}
	}
	if err := validateRecoveredStatus(persisted, expected); err != nil {
		return RecoveryInspection{State: RecoveryUnverifiable, Status: persisted, Err: err}
	}
	if terminalSupervisorState(persisted.State) {
		return RecoveryInspection{State: RecoveryTerminal, Status: persisted}
	}

	supervisorAlive, err := hooks.processAlive(persisted.SupervisorPID)
	if err != nil {
		return RecoveryInspection{State: RecoveryUnverifiable, Status: persisted, Err: fmt.Errorf("inspect recorded supervisor PID %d: %w", persisted.SupervisorPID, err)}
	}
	groupAlive, err := hooks.processGroupAlive(persisted.PID)
	if err != nil {
		return RecoveryInspection{State: RecoveryUnverifiable, Status: persisted, Err: fmt.Errorf("inspect recorded process group %d: %w", persisted.PID, err)}
	}
	if !supervisorAlive && !groupAlive {
		return RecoveryInspection{State: RecoveryGone, Status: persisted}
	}
	return RecoveryInspection{
		State: RecoveryUnverifiable, Status: persisted,
		Err: fmt.Errorf("supervisor socket is unavailable while recorded supervisor PID %d or process group %d may still exist", persisted.SupervisorPID, persisted.PID),
	}
}

func validateRecoveredStatus(status supervisor.Status, expected PersistedRun) error {
	if status.Scope != expected.Scope || status.Service != expected.Service || status.Generation != expected.Generation {
		return errors.New("supervisor identity does not match persisted service run")
	}
	if status.PID <= 0 || status.SupervisorPID <= 0 {
		return errors.New("supervisor process identity is incomplete")
	}
	if expected.PID > 0 && status.PID != expected.PID {
		return errors.New("supervisor application PID does not match persisted service run")
	}
	if expected.SupervisorPID > 0 && status.SupervisorPID != expected.SupervisorPID {
		return errors.New("supervisor PID does not match persisted service run")
	}
	if status.LaunchMode != expected.LaunchMode || !recoveryDebuggersEqual(status.Debugger, expected.Debugger) {
		return errors.New("supervisor launch mode does not match persisted service run")
	}
	return nil
}

func recoveryDebuggersEqual(left, right *model.DebuggerRuntime) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Adapter == right.Adapter && left.Host == right.Host && left.Port == right.Port
}

func terminalSupervisorState(state string) bool {
	return state == "stopped" || state == "exited" || state == "failed"
}

func inspectionError(inspection RecoveryInspection) error {
	if inspection.Err != nil {
		return inspection.Err
	}
	return errors.New("persisted process state cannot be verified")
}

func recordedProcessAlive(pid int) (bool, error) {
	return signalTargetAlive(pid)
}

func recordedProcessGroupAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, errors.New("process group ID is missing")
	}
	return signalTargetAlive(-pid)
}

func signalTargetAlive(target int) (bool, error) {
	if target == 0 {
		return false, errors.New("process ID is missing")
	}
	err := syscall.Kill(target, syscall.Signal(0))
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, os.ErrProcessDone), errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
