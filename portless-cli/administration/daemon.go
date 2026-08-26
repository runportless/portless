package administration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/control"
)

type daemonStatusOutput struct {
	State              string    `json:"state"`
	Compatible         bool      `json:"compatible"`
	CurrentBuild       bool      `json:"currentBuild"`
	PID                int       `json:"pid,omitempty"`
	ProtocolVersion    string    `json:"protocolVersion,omitempty"`
	APIVersion         string    `json:"apiVersion,omitempty"`
	InstallationID     string    `json:"installationId,omitempty"`
	InstanceID         string    `json:"instanceId,omitempty"`
	BuildID            string    `json:"buildId,omitempty"`
	ExpectedBuildID    string    `json:"expectedBuildId,omitempty"`
	StartedAt          time.Time `json:"startedAt,omitempty"`
	RuntimeState       string    `json:"runtimeState,omitempty"`
	HandoffState       string    `json:"handoffState"`
	HandoffVerifiedAt  time.Time `json:"handoffVerifiedAt,omitempty"`
	HandoffProblems    []string  `json:"handoffProblems"`
	RecoveryProblems   []string  `json:"recoveryProblems"`
	ActiveEnvironments []string  `json:"activeEnvironments"`
	Problems           []string  `json:"problems"`
}

func (c *Commands) daemonStatus(ctx context.Context, jsonOutput bool) error {
	inspection, err := c.Daemon.Inspect(ctx)
	if errors.Is(err, os.ErrNotExist) {
		result := daemonStatusOutput{
			State: "stopped", HandoffState: "not-required", HandoffProblems: []string{},
			RecoveryProblems: []string{}, ActiveEnvironments: []string{}, Problems: []string{},
		}
		if jsonOutput {
			return command.WriteJSON(c.Out, result)
		}
		fmt.Fprintln(c.Out, "Portless daemon is", c.State(c.Out, "stopped")+".")
		return nil
	}
	if err != nil {
		if errors.Is(err, control.ErrLegacyDaemon) {
			return fmt.Errorf("daemon identity could not be verified: %w; replace it once with `portless daemon restart --force`", err)
		}
		return fmt.Errorf("daemon identity could not be verified: %w", err)
	}
	state := "running"
	if !inspection.Compatible {
		state = "incompatible"
	} else if !inspection.CurrentBuild {
		state = "outdated"
	}
	handoff := control.HandoffInspection{State: "unchecked", Problems: []string{}, ActiveEnvironments: append([]string(nil), inspection.Identity.ActiveEnvironments...)}
	if inspection.Compatible {
		handoff, err = c.Daemon.VerifyHandoff(ctx)
		if err != nil {
			return fmt.Errorf("verify daemon runtime handoff: %w", err)
		}
	}
	activeEnvironments := append([]string(nil), handoff.ActiveEnvironments...)
	result := daemonStatusOutput{
		State: state, Compatible: inspection.Compatible, CurrentBuild: inspection.CurrentBuild, PID: inspection.Identity.PID,
		ProtocolVersion: inspection.Identity.ProtocolVersion, APIVersion: inspection.Identity.APIVersion,
		InstallationID: inspection.Identity.InstallationID, InstanceID: inspection.Identity.InstanceID,
		BuildID: inspection.Identity.BuildID, ExpectedBuildID: inspection.ExpectedBuildID,
		StartedAt: inspection.Identity.StartedAt, ActiveEnvironments: activeEnvironments,
		RuntimeState: inspection.Identity.State, HandoffState: string(handoff.State), HandoffVerifiedAt: handoff.VerifiedAt,
		HandoffProblems:  append([]string(nil), handoff.Problems...),
		RecoveryProblems: append([]string(nil), inspection.Identity.RecoveryProblems...),
		Problems:         append([]string(nil), inspection.Problems...),
	}
	if result.ActiveEnvironments == nil {
		result.ActiveEnvironments = []string{}
	}
	if result.Problems == nil {
		result.Problems = []string{}
	}
	if result.RecoveryProblems == nil {
		result.RecoveryProblems = []string{}
	}
	if result.HandoffProblems == nil {
		result.HandoffProblems = []string{}
	}
	if jsonOutput {
		return command.WriteJSON(c.Out, result)
	}
	c.printDaemonStatus(result)
	return nil
}

func (c *Commands) printDaemonStatus(result daemonStatusOutput) {
	fmt.Fprintf(c.Out, "%s %s\n", c.Heading(c.Out, "Portless daemon:"), c.State(c.Out, result.State))
	fmt.Fprintf(c.Out, "PID: %d\n", result.PID)
	fmt.Fprintf(c.Out, "Started: %s\n", result.StartedAt.Local().Format(time.RFC3339))
	fmt.Fprintf(c.Out, "Instance: %s\n", shortFingerprint(result.InstanceID))
	fmt.Fprintf(c.Out, "Build: %s\n", shortFingerprint(result.BuildID))
	fmt.Fprintf(c.Out, "Protocol Version: %s\n", result.ProtocolVersion)
	fmt.Fprintf(c.Out, "API Version: %s\n", result.APIVersion)
	if result.RuntimeState != "" {
		fmt.Fprintf(c.Out, "Runtime state: %s\n", result.RuntimeState)
	}
	if result.HandoffState == "ready" {
		fmt.Fprintln(c.Out, "Runtime handoff:", c.Success(c.Out, "ready"))
	} else {
		fmt.Fprintln(c.Out, "Runtime handoff:", c.Warning(c.Out, result.HandoffState))
	}
	if len(result.ActiveEnvironments) == 0 {
		fmt.Fprintln(c.Out, "Active environments: none")
	} else {
		fmt.Fprintln(c.Out, "Active environments:")
		for _, environment := range result.ActiveEnvironments {
			fmt.Fprintln(c.Out, "  "+environment)
		}
	}
	for _, problem := range result.Problems {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "Problem:")+" "+problem)
	}
	for _, problem := range result.RecoveryProblems {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "Recovery:")+" "+problem)
	}
	for _, problem := range result.HandoffProblems {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "Handoff:")+" "+problem)
	}
}

func (c *Commands) stopDaemon(ctx context.Context, options control.StopOptions, jsonOutput bool) error {
	result, err := c.Daemon.Stop(ctx, options)
	if err != nil {
		return err
	}
	if jsonOutput {
		return command.WriteJSON(c.Out, result)
	}
	if !result.WasRunning {
		fmt.Fprintln(c.Out, "Portless daemon is already stopped.")
		return nil
	}
	fmt.Fprintf(c.Out, "%s Portless daemon (PID %d).\n", c.Success(c.Out, "Stopped"), result.PID)
	printForcedDaemonWarning(c, result)
	return nil
}

func (c *Commands) restartDaemon(ctx context.Context, options control.StopOptions, jsonOutput bool) error {
	options.Handoff = true
	stopped, err := c.Daemon.Stop(ctx, options)
	if err != nil {
		return err
	}
	record, err := c.Daemon.Ensure(ctx)
	if err != nil {
		return err
	}
	inspection, err := c.Daemon.Inspect(ctx)
	if err != nil {
		return err
	}
	handoff, err := c.Daemon.VerifyHandoff(ctx)
	if err != nil {
		return err
	}
	problems := append([]string(nil), inspection.Problems...)
	if problems == nil {
		problems = []string{}
	}
	if jsonOutput {
		return command.WriteJSON(c.Out, map[string]any{"stopped": stopped, "daemon": daemonStatusOutput{
			State: "running", Compatible: inspection.Compatible, CurrentBuild: inspection.CurrentBuild, PID: record.PID,
			ProtocolVersion: record.ProtocolVersion, APIVersion: record.APIVersion,
			InstallationID: record.InstallationID, InstanceID: record.InstanceID,
			BuildID: record.BuildID, ExpectedBuildID: inspection.ExpectedBuildID,
			StartedAt: record.StartedAt, ActiveEnvironments: append([]string{}, handoff.ActiveEnvironments...),
			RuntimeState: inspection.Identity.State, HandoffState: string(handoff.State), HandoffVerifiedAt: handoff.VerifiedAt,
			HandoffProblems:  append([]string{}, handoff.Problems...),
			RecoveryProblems: append([]string{}, inspection.Identity.RecoveryProblems...),
			Problems:         problems,
		}})
	}
	fmt.Fprintf(c.Out, "Portless daemon is %s (PID %d, build %s).\n", c.Success(c.Out, "running"), record.PID, shortFingerprint(record.BuildID))
	printForcedDaemonWarning(c, stopped)
	return nil
}

func printForcedDaemonWarning(c *Commands, result control.StopResult) {
	if !result.Forced || len(result.ActiveEnvironments) == 0 {
		return
	}
	fmt.Fprintln(c.Out, c.Warning(c.Out, "Warning:")+" runtime handoff safety was bypassed for these active environments:")
	for _, environment := range result.ActiveEnvironments {
		fmt.Fprintln(c.Out, "  "+environment)
	}
}

func shortFingerprint(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
