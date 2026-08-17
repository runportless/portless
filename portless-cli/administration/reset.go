package administration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-cli/command"
	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/control"
	"github.com/portless-run/portless/portless-daemon/system/installation"
)

var (
	resetRemovalCategories = []string{
		"projects and environment configuration",
		"traffic, recordings, faults, timelines, and service logs",
		"Portless-managed containers, networks, database volumes, and cache volumes in every previously used runtime",
		"local daemon application database and runtime history",
	}
	resetPreservedCategories = []string{
		"CLI preferences and container runtime selection",
		"Portless installation identity and CLI authentication",
		"localhost relay installation",
	}
)

type resetDaemonOutput struct {
	State      string    `json:"state"`
	PID        int       `json:"pid"`
	InstanceID string    `json:"instanceId"`
	StartedAt  time.Time `json:"startedAt"`
}

type resetOutput struct {
	Action                    string                        `json:"action"`
	Confirmed                 bool                          `json:"confirmed"`
	Forced                    bool                          `json:"forced"`
	Changed                   bool                          `json:"changed"`
	Projects                  int                           `json:"projects"`
	Environments              int                           `json:"environments"`
	ManagedVolumeEnvironments int                           `json:"managedVolumeEnvironments"`
	ActiveEnvironments        []string                      `json:"activeEnvironments"`
	WillRemove                []string                      `json:"willRemove,omitempty"`
	Removed                   []string                      `json:"removed,omitempty"`
	Preserved                 []string                      `json:"preserved"`
	ProcessesStopped          int                           `json:"processesStopped"`
	RuntimeCleanup            []contract.RuntimeResetResult `json:"runtimeCleanup,omitempty"`
	Daemon                    *resetDaemonOutput            `json:"daemon,omitempty"`
	TopologyIncompatible      bool                          `json:"topologyIncompatible"`
}

func (c *Commands) reset(ctx context.Context, options resetOptions) error {
	var (
		client *apiclient.Client
		err    error
	)
	if options.force && options.yes {
		client, err = c.connectCurrentDaemonForForcedReset(ctx)
	} else {
		client, _, err = c.Daemon.Connect(ctx)
	}
	if err != nil {
		return err
	}
	plan, err := loadResetPlan(ctx, client)
	if err != nil {
		return err
	}
	preview := resetOutput{
		Action: "reset", Confirmed: options.yes, Forced: options.force, Changed: false,
		Projects: plan.Projects, Environments: plan.Environments,
		ManagedVolumeEnvironments: plan.ManagedVolumeEnvironments,
		ActiveEnvironments:        append([]string{}, plan.ActiveEnvironments...),
		WillRemove:                append([]string(nil), resetRemovalCategories...),
		Preserved:                 append([]string(nil), resetPreservedCategories...),
		TopologyIncompatible:      plan.TopologyIncompatible,
	}
	if !options.yes {
		return c.printResetPreview(preview)
	}
	if len(plan.ActiveEnvironments) > 0 && plan.TopologyIncompatible && !options.force {
		return incompatibleActiveResetError(plan.ActiveEnvironments)
	}
	if len(plan.ActiveEnvironments) > 0 && !options.force {
		return activeResetError(plan.ActiveEnvironments)
	}
	if !c.JSONOutput && (plan.ManagedVolumeEnvironments > 0 || plan.TopologyIncompatible) {
		fmt.Fprintln(c.Out, "Removing Portless-managed container resources...")
	}
	prepared, err := client.PrepareReset(ctx, options.force)
	if err != nil {
		return err
	}
	resetLifecycleComplete := false
	defer func() {
		if resetLifecycleComplete {
			return
		}
		cancelContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.CancelReset(cancelContext)
	}()

	_, record, err := c.Daemon.ResetApplicationState(ctx, options.force)
	if err != nil {
		return err
	}
	resetLifecycleComplete = true
	if err := verifyEmptyReset(ctx, c.Paths); err != nil {
		return err
	}
	result := preview
	result.Changed = true
	result.WillRemove = nil
	result.Removed = append([]string(nil), resetRemovalCategories...)
	result.ProcessesStopped = prepared.Processes
	result.RuntimeCleanup = append([]contract.RuntimeResetResult(nil), prepared.Runtimes...)
	result.Daemon = &resetDaemonOutput{State: "running", PID: record.PID, InstanceID: record.InstanceID, StartedAt: record.StartedAt}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	c.printResetComplete(result)
	return nil
}

func (c *Commands) connectCurrentDaemonForForcedReset(ctx context.Context) (*apiclient.Client, error) {
	inspection, inspectErr := c.Daemon.Inspect(ctx)
	if inspectErr == nil && inspection.Compatible && inspection.CurrentBuild {
		client, _, err := c.Daemon.Connect(ctx)
		return client, err
	}
	if _, err := c.Daemon.Stop(ctx, control.StopOptions{Force: true, Timeout: 15 * time.Second}); err != nil {
		return nil, fmt.Errorf("replace daemon before forced reset: %w", err)
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("start current daemon before forced reset: %w", err)
	}
	return client, nil
}

func loadResetPlan(ctx context.Context, client *apiclient.Client) (contract.ResetPlan, error) {
	plan, err := client.ResetPlan(ctx)
	if err != nil {
		return contract.ResetPlan{}, err
	}
	if plan.ActiveEnvironments == nil {
		plan.ActiveEnvironments = []string{}
	}
	return plan, nil
}

func verifyEmptyReset(ctx context.Context, paths installation.Layout) error {
	client, _, err := control.New(paths).ConnectExisting(ctx)
	if err != nil {
		return fmt.Errorf("verify reset daemon: %w", err)
	}
	response, err := client.ListEnvironments(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("verify reset database: %w", err)
	}
	if response.Total != 0 || len(response.Environments) != 0 {
		return errors.New("reset daemon started with non-empty environment state")
	}
	return nil
}

func activeResetError(environments []string) error {
	return fmt.Errorf("reset requires every environment to be stopped; active: %s; run `portless down --all`, then retry", strings.Join(environments, ", "))
}

func incompatibleActiveResetError(environments []string) error {
	return fmt.Errorf("stored application topology is incompatible with this Portless build, so active environments cannot be shut down individually: %s; run `portless reset --force --yes` to stop verified runtimes and rediscover sources", strings.Join(environments, ", "))
}

func (c *Commands) printResetPreview(result resetOutput) error {
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Portless reset preview"))
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "This will permanently remove:")
	fmt.Fprintf(c.Out, "  %d %s\n", result.Projects, counted(result.Projects, "project"))
	fmt.Fprintf(c.Out, "  %d %s\n", result.Environments, counted(result.Environments, "environment"))
	fmt.Fprintln(c.Out, "  "+result.WillRemove[1])
	fmt.Fprintln(c.Out, "  "+result.WillRemove[2])
	fmt.Fprintln(c.Out, "  "+result.WillRemove[3])
	printResetPreserved(c, result.Preserved)
	if result.TopologyIncompatible {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, c.Warning(c.Out, "The stored project topology is incompatible with this build; reset will use format-independent runtime ownership records."))
	}
	if len(result.ActiveEnvironments) > 0 {
		fmt.Fprintln(c.Out)
		if result.Forced {
			fmt.Fprintln(c.Out, c.Warning(c.Out, "Force reset will terminate verified Portless runtimes in these environments:"))
		} else {
			fmt.Fprintln(c.Out, c.Warning(c.Out, "Reset is currently blocked by active environments:"))
		}
		for _, environment := range result.ActiveEnvironments {
			fmt.Fprintln(c.Out, "  "+environment)
		}
	}
	fmt.Fprintln(c.Out)
	command := "portless reset --yes"
	if result.Forced || result.TopologyIncompatible && len(result.ActiveEnvironments) > 0 {
		command = "portless reset --force --yes"
	}
	fmt.Fprintf(c.Out, "No changes were made. Run `%s` to continue.\n", command)
	return nil
}

func (c *Commands) printResetComplete(result resetOutput) {
	fmt.Fprintln(c.Out, c.Success(c.Out, "Portless reset complete."))
	fmt.Fprintf(c.Out, "Removed: %d %s and %d %s.\n", result.Projects, counted(result.Projects, "project"), result.Environments, counted(result.Environments, "environment"))
	for _, runtime := range result.RuntimeCleanup {
		fmt.Fprintf(c.Out, "%s cleanup: %d containers, %d volumes, %d networks.\n", runtime.Runtime, runtime.Containers, runtime.Volumes, runtime.Networks)
	}
	if result.ProcessesStopped > 0 {
		fmt.Fprintf(c.Out, "Stopped %d lingering supervised %s.\n", result.ProcessesStopped, counted(result.ProcessesStopped, "process"))
	}
	printResetPreserved(c, result.Preserved)
	if result.Daemon != nil {
		fmt.Fprintf(c.Out, "Daemon: %s (PID %d)\n", c.State(c.Out, result.Daemon.State), result.Daemon.PID)
	}
}

func printResetPreserved(c *Commands, preserved []string) {
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Preserved:")
	for _, item := range preserved {
		fmt.Fprintln(c.Out, "  "+item)
	}
}

func counted(count int, singular string) string {
	if count == 1 {
		return singular
	}
	return singular + "s"
}
