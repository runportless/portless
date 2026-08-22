package traffic

import (
	"context"
	"fmt"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/model"
)

func (c *Commands) listFaults(ctx context.Context, limit int) error {
	if err := command.ValidLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListFaults(ctx, environment.Project, environment.Name, limit)
	if err != nil {
		return err
	}
	if response.Faults == nil {
		response.Faults = []model.FaultRule{}
	}
	response.Faults = command.Truncate(response.Faults, limit)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Faults) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No fault rules."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, "Fault rules"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-24s %-9s %-22s %s", "NAME", "STATE", "LIFETIME", "SCOPE")))
	for _, fault := range response.Faults {
		state := "disabled"
		lifetime := "—"
		if fault.Enabled {
			state = "active"
			lifetime = "until disabled"
			if fault.ExpiresAt != nil {
				lifetime = "until " + fault.ExpiresAt.Local().Format("2006-01-02 15:04")
			}
		}
		fmt.Fprintf(c.Out, "%-24s %s %-22s %s\n", fault.Name, c.State(c.Out, fmt.Sprintf("%-9s", state)), lifetime, fault.ScopeSummary)
	}
	return nil
}

func (c *Commands) addFault(ctx context.Context, name, edge string, options faultOptions) error {
	source, target, err := command.ParseEdge(edge)
	if err != nil || source == "" || target == "" {
		return command.UsageError("edge must use source:target")
	}
	if options.duration < 0 {
		return command.UsageError("--duration must be zero or greater")
	}
	if options.probability <= 0 || options.probability > 1 {
		return command.UsageError("--probability must be greater than zero and no more than 1")
	}
	if options.latency < 0 || options.jitter < 0 || options.latency+options.jitter > 60_000 {
		return command.UsageError("--latency plus --jitter must be between 0 and 60000 milliseconds")
	}
	if options.status != 0 && (options.status < 400 || options.status > 599) {
		return command.UsageError("--status must be between 400 and 599")
	}
	if options.latency == 0 && options.jitter == 0 && options.status == 0 && !options.abort {
		return command.UsageError("define at least one effect with --latency, --jitter, --status, or --abort")
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	input := model.FaultRule{Name: name, Source: source, Target: target, LatencyMS: options.latency, JitterMS: options.jitter, StatusCode: options.status, Abort: options.abort, Probability: options.probability, Method: options.method, Path: options.path}
	if options.duration > 0 {
		expires := time.Now().UTC().Add(options.duration)
		input.ExpiresAt = &expires
	}
	created, err := client.CreateFault(ctx, environment.Project, environment.Name, input)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, created)
	}
	lifetime := "until disabled"
	if created.ExpiresAt != nil {
		lifetime = "until " + created.ExpiresAt.Local().Format(time.RFC3339)
	}
	fmt.Fprintf(c.Out, "fault %s active %s: %s\n", created.Name, lifetime, created.ScopeSummary)
	return nil
}

func (c *Commands) disableFault(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if _, err := client.SetFaultEnabled(ctx, environment.Project, environment.Name, name, false); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "disable", Project: environment.Project, Environment: environment.Name, Name: name, Status: "disabled"})
	}
	fmt.Fprintln(c.Out, "fault", name, "disabled")
	return nil
}

func (c *Commands) enableFault(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	fault, err := client.SetFaultEnabled(ctx, environment.Project, environment.Name, name, true)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, fault)
	}
	fmt.Fprintln(c.Out, "fault", name, "enabled")
	return nil
}

func (c *Commands) deleteFault(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := client.DeleteFault(ctx, environment.Project, environment.Name, name); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "fault", name, "deleted")
	return nil
}

func (c *Commands) clearFaults(ctx context.Context) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	disabled, err := client.DisableAllFaults(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		result := map[string]any{"disabled": disabled.Disabled}
		result["project"] = environment.Project
		result["environment"] = environment.Name
		return command.WriteJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, "all active faults disabled")
	return nil
}
