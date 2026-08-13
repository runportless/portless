package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/container"
)

const resetInventoryLimit = 1000

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

type resetPlan struct {
	Projects              []model.Project
	Environments          []model.Environment
	ContainerEnvironments []model.Environment
	ActiveEnvironments    []string
}

type resetDaemonOutput struct {
	State      string    `json:"state"`
	PID        int       `json:"pid"`
	InstanceID string    `json:"instanceId"`
	StartedAt  time.Time `json:"startedAt"`
}

type resetOutput struct {
	Action                    string                  `json:"action"`
	Confirmed                 bool                    `json:"confirmed"`
	Changed                   bool                    `json:"changed"`
	Projects                  int                     `json:"projects"`
	Environments              int                     `json:"environments"`
	ManagedVolumeEnvironments int                     `json:"managedVolumeEnvironments"`
	ActiveEnvironments        []string                `json:"activeEnvironments"`
	WillRemove                []string                `json:"willRemove,omitempty"`
	Removed                   []string                `json:"removed,omitempty"`
	Preserved                 []string                `json:"preserved"`
	ProcessesStopped          int                     `json:"processesStopped"`
	RuntimeCleanup            []container.ResetResult `json:"runtimeCleanup,omitempty"`
	Daemon                    *resetDaemonOutput      `json:"daemon,omitempty"`
}

func (c *CLI) reset(ctx context.Context, options resetOptions) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	plan, err := loadResetPlan(ctx, client)
	if err != nil {
		return err
	}
	preview := resetOutput{
		Action: "reset", Confirmed: options.yes, Changed: false,
		Projects: len(plan.Projects), Environments: len(plan.Environments),
		ManagedVolumeEnvironments: len(plan.ContainerEnvironments),
		ActiveEnvironments:        append([]string{}, plan.ActiveEnvironments...),
		WillRemove:                append([]string(nil), resetRemovalCategories...),
		Preserved:                 append([]string(nil), resetPreservedCategories...),
	}
	if !options.yes {
		return c.printResetPreview(preview)
	}
	if len(plan.ActiveEnvironments) > 0 {
		return activeResetError(plan.ActiveEnvironments)
	}
	if !c.jsonOutput && len(plan.ContainerEnvironments) > 0 {
		fmt.Fprintln(c.Out, "Removing Portless-managed container resources...")
	}
	var prepared struct {
		Processes int                     `json:"processes"`
		Runtimes  []container.ResetResult `json:"runtimes"`
	}
	if err := client.Do(ctx, http.MethodPost, "/api/v1/runtime/reset", nil, &prepared); err != nil {
		return err
	}
	resetLifecycleComplete := false
	defer func() {
		if resetLifecycleComplete {
			return
		}
		cancelContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.Do(cancelContext, http.MethodPost, "/api/v1/runtime/reset/cancel", nil, nil)
	}()

	_, record, err := bootstrap.ResetDaemonApplicationState(ctx, c.paths)
	if err != nil {
		return err
	}
	resetLifecycleComplete = true
	if err := verifyEmptyReset(ctx, c.paths); err != nil {
		return err
	}
	result := preview
	result.Changed = true
	result.WillRemove = nil
	result.Removed = append([]string(nil), resetRemovalCategories...)
	result.ProcessesStopped = prepared.Processes
	result.RuntimeCleanup = append([]container.ResetResult(nil), prepared.Runtimes...)
	result.Daemon = &resetDaemonOutput{State: "running", PID: record.PID, InstanceID: record.InstanceID, StartedAt: record.StartedAt}
	if c.jsonOutput {
		return writeJSON(c.Out, result)
	}
	c.printResetComplete(result)
	return nil
}

func loadResetPlan(ctx context.Context, client *bootstrap.Client) (resetPlan, error) {
	var projectResponse struct {
		Projects []model.Project `json:"projects"`
		Total    int             `json:"total"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects?limit="+strconv.Itoa(resetInventoryLimit), nil, &projectResponse); err != nil {
		return resetPlan{}, err
	}
	var environmentResponse struct {
		Environments []model.Environment `json:"environments"`
		Total        int                 `json:"total"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/environments?limit="+strconv.Itoa(resetInventoryLimit), nil, &environmentResponse); err != nil {
		return resetPlan{}, err
	}
	if projectResponse.Total > len(projectResponse.Projects) || environmentResponse.Total > len(environmentResponse.Environments) {
		return resetPlan{}, fmt.Errorf("reset safety inventory exceeds %d projects or environments; Portless refused to erase ownership records without inventorying every environment", resetInventoryLimit)
	}
	plan := resetPlan{Projects: projectResponse.Projects, Environments: environmentResponse.Environments}
	for _, environment := range plan.Environments {
		selector := model.EnvironmentSelector(environment.Project, environment.Name)
		if environment.Status != model.EnvironmentStopped {
			plan.ActiveEnvironments = append(plan.ActiveEnvironments, selector)
		}
		if environmentHasContainers(environment) {
			plan.ContainerEnvironments = append(plan.ContainerEnvironments, environment)
		}
	}
	sort.Strings(plan.ActiveEnvironments)
	return plan, nil
}

func environmentHasContainers(environment model.Environment) bool {
	for _, service := range environment.Services {
		if service.Kind == model.ServiceContainer {
			return true
		}
	}
	return false
}

func verifyEmptyReset(ctx context.Context, paths bootstrap.Paths) error {
	client, _, err := bootstrap.ConnectExisting(ctx, paths)
	if err != nil {
		return fmt.Errorf("verify reset daemon: %w", err)
	}
	var response struct {
		Environments []model.Environment `json:"environments"`
		Total        int                 `json:"total"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/environments?limit=1", nil, &response); err != nil {
		return fmt.Errorf("verify reset database: %w", err)
	}
	if response.Total != 0 || len(response.Environments) != 0 {
		return errors.New("reset daemon started with non-empty environment state")
	}
	return nil
}

func activeResetError(environments []string) error {
	return fmt.Errorf("reset requires every environment to be stopped; active: %s; stop each with `portless --env project/environment down`, then retry", strings.Join(environments, ", "))
}

func (c *CLI) printResetPreview(result resetOutput) error {
	if c.jsonOutput {
		return writeJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Portless reset preview"))
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "This will permanently remove:")
	fmt.Fprintf(c.Out, "  %d %s\n", result.Projects, counted(result.Projects, "project"))
	fmt.Fprintf(c.Out, "  %d %s\n", result.Environments, counted(result.Environments, "environment"))
	fmt.Fprintln(c.Out, "  "+result.WillRemove[1])
	fmt.Fprintln(c.Out, "  "+result.WillRemove[2])
	fmt.Fprintln(c.Out, "  "+result.WillRemove[3])
	printResetPreserved(c, result.Preserved)
	if len(result.ActiveEnvironments) > 0 {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, c.warning(c.Out, "Reset is currently blocked by active environments:"))
		for _, environment := range result.ActiveEnvironments {
			fmt.Fprintln(c.Out, "  "+environment)
		}
	}
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "No changes were made. Run `portless reset --yes` to continue.")
	return nil
}

func (c *CLI) printResetComplete(result resetOutput) {
	fmt.Fprintln(c.Out, c.success(c.Out, "Portless reset complete."))
	fmt.Fprintf(c.Out, "Removed: %d %s and %d %s.\n", result.Projects, counted(result.Projects, "project"), result.Environments, counted(result.Environments, "environment"))
	for _, runtime := range result.RuntimeCleanup {
		fmt.Fprintf(c.Out, "%s cleanup: %d containers, %d volumes, %d networks.\n", runtime.Runtime, runtime.Containers, runtime.Volumes, runtime.Networks)
	}
	if result.ProcessesStopped > 0 {
		fmt.Fprintf(c.Out, "Stopped %d lingering supervised %s.\n", result.ProcessesStopped, counted(result.ProcessesStopped, "process"))
	}
	printResetPreserved(c, result.Preserved)
	if result.Daemon != nil {
		fmt.Fprintf(c.Out, "Daemon: %s (PID %d)\n", c.state(c.Out, result.Daemon.State), result.Daemon.PID)
	}
}

func printResetPreserved(c *CLI, preserved []string) {
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
