package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/lifecycle"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

// RestartOptions controls exceptional forced replacement. Normal authenticated
// restart always uses the fixed daemon restart SLA.
type RestartOptions struct {
	Force bool
}

// RestartResult describes one completed daemon replacement and its observed
// end-to-end duration.
type RestartResult struct {
	Restart    contract.DaemonRestart `json:"restart"`
	Daemon     identity.Record        `json:"-"`
	Inspection Inspection             `json:"-"`
	DurationMS int64                  `json:"durationMs"`
	Forced     bool                   `json:"forced"`
}

func (m *Manager) restartDaemon(ctx context.Context, options RestartOptions) (RestartResult, error) {
	startedAt := m.hooks.Now()
	if options.Force {
		stopped, err := m.stopDaemon(ctx, StopOptions{Force: true, Handoff: true, Timeout: 15 * time.Second})
		if err != nil {
			return RestartResult{}, err
		}
		record, err := m.ensureDaemon(ctx)
		result := RestartResult{
			Restart: contract.DaemonRestart{
				Reason: "forced", PreviousInstanceID: stopped.InstanceID, Handoff: false,
				ActiveEnvironments: append([]string(nil), stopped.ActiveEnvironments...),
			},
			Daemon: record, DurationMS: m.hooks.Now().Sub(startedAt).Milliseconds(), Forced: true,
		}
		if err == nil {
			result.Inspection, err = m.inspectDaemon(ctx)
		}
		return result, err
	}

	deadline := startedAt.Add(contract.DaemonRestartSLA)
	restartContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	inspection, err := m.inspectDaemon(restartContext)
	if errors.Is(err, os.ErrNotExist) {
		record, startErr := m.ensureDaemon(restartContext)
		result := RestartResult{Daemon: record, DurationMS: m.hooks.Now().Sub(startedAt).Milliseconds()}
		if startErr == nil {
			result.Inspection, startErr = m.inspectDaemon(restartContext)
		}
		return result, startErr
	}
	if err != nil {
		return RestartResult{}, err
	}
	return m.restartInspectedDaemon(restartContext, startedAt, deadline, inspection)
}

func (m *Manager) restartOutdatedDaemon(ctx context.Context, inspection Inspection) (identity.Record, error) {
	startedAt := m.hooks.Now()
	deadline := startedAt.Add(contract.DaemonRestartSLA)
	restartContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, err := m.restartInspectedDaemon(restartContext, startedAt, deadline, inspection)
	return result.Daemon, err
}

func (m *Manager) restartInspectedDaemon(ctx context.Context, startedAt, deadline time.Time, inspection Inspection) (RestartResult, error) {
	if inspection.Identity.ProtocolVersion != lifecycle.ProtocolVersion {
		return RestartResult{}, incompatibleDaemonError(inspection)
	}
	token, err := installation.ReadPrivateTextFile(inspection.Record.TokenPath)
	if err != nil {
		return RestartResult{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	client := apiclient.New(fmt.Sprintf("http://127.0.0.1:%d", inspection.Record.Port), token, m.hooks.HTTPClient(contract.DaemonRestartSLA)).WithClientKind(contract.ClientKindCLI)
	receipt, restartErr := client.RestartDaemon(ctx, inspection.Record.InstanceID)
	if restartErr != nil {
		var clientError *apiclient.ClientError
		if errors.As(restartErr, &clientError) && clientError.Code != "DAEMON_INSTANCE_CHANGED" {
			return RestartResult{}, restartErr
		}
		// Automatic executable replacement may close the old connection just
		// before the explicit request completes. Observe the replacement before
		// deciding that the restart failed.
		receipt = contract.DaemonRestart{
			Restarting: true, Reason: "cli", PreviousInstanceID: inspection.Record.InstanceID,
			AcceptedAt: startedAt.UTC(), DeadlineAt: deadline.UTC(), Handoff: true,
			ActiveEnvironments: append([]string(nil), inspection.Identity.ActiveEnvironments...),
		}
	} else if inspection.Identity.APIVersion == contract.APIVersion {
		if err := validateRestartReceipt(receipt, inspection.Record.InstanceID); err != nil {
			return RestartResult{}, err
		}
	}
	if receipt.AcceptedAt.IsZero() {
		receipt.AcceptedAt = startedAt.UTC()
	}
	if receipt.DeadlineAt.IsZero() || receipt.DeadlineAt.After(deadline) {
		receipt.DeadlineAt = deadline.UTC()
	}
	if receipt.Reason == "" {
		receipt.Reason = "cli"
	}
	ready, err := m.awaitReplacement(ctx, inspection.Record, receipt)
	if err != nil {
		if restartErr != nil {
			return RestartResult{}, fmt.Errorf("request daemon restart (%v); replacement failed: %w", restartErr, err)
		}
		return RestartResult{}, err
	}
	readyRecord := ready.Record
	readyRecord.State = ready.Identity.State
	readyRecord.RecoveryProblems = append([]string(nil), ready.Identity.RecoveryProblems...)
	return RestartResult{Restart: receipt, Daemon: readyRecord, Inspection: ready, DurationMS: m.hooks.Now().Sub(startedAt).Milliseconds()}, nil
}

func (m *Manager) awaitReplacement(ctx context.Context, previous identity.Record, receipt contract.DaemonRestart) (Inspection, error) {
	var lastError error
	for {
		current, err := identity.Read(m.layout)
		if err == nil && current.InstanceID != "" && current.InstanceID != previous.InstanceID {
			inspection, inspectErr := m.inspectDaemon(ctx)
			if inspectErr == nil && inspection.Compatible && inspection.CurrentBuild && inspection.Identity.State == "ready" {
				return inspection, nil
			}
			if inspectErr != nil {
				lastError = inspectErr
			} else {
				lastError = incompatibleDaemonError(inspection)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			lastError = err
		}
		if err := m.hooks.Wait(ctx, 100*time.Millisecond); err != nil {
			restart := receipt.RestartID
			if restart == "" {
				restart = "without a receipt"
			}
			message := fmt.Sprintf("daemon restart %s exceeded the %s readiness SLA; inspect %s", restart, contract.DaemonRestartSLA, m.layout.DaemonLog)
			if lastError != nil {
				message += ": " + lastError.Error()
			}
			return Inspection{}, errors.New(message)
		}
	}
}

func validateRestartReceipt(receipt contract.DaemonRestart, previousInstanceID string) error {
	if !receipt.Restarting || receipt.RestartID == "" || receipt.Reason == "" || receipt.PreviousInstanceID != previousInstanceID || receipt.TargetBuildID == "" || receipt.AcceptedAt.IsZero() || receipt.DeadlineAt.IsZero() || !receipt.DeadlineAt.After(receipt.AcceptedAt) || receipt.DeadlineAt.Sub(receipt.AcceptedAt) > contract.DaemonRestartSLA {
		return errors.New("daemon returned an invalid restart receipt")
	}
	if !receipt.Handoff {
		return errors.New("daemon restart receipt did not confirm runtime handoff")
	}
	return nil
}
