package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) listFaults(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
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
	response.Faults = truncate(response.Faults, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Faults) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No fault rules."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, "Fault rules"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-24s %-9s %-22s %s", "NAME", "STATE", "LIFETIME", "SCOPE")))
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
		fmt.Fprintf(c.Out, "%-24s %s %-22s %s\n", fault.Name, c.state(c.Out, fmt.Sprintf("%-9s", state)), lifetime, fault.ScopeSummary)
	}
	return nil
}

func (c *CLI) addFault(ctx context.Context, name, edge string, options faultOptions) error {
	source, target, err := parseEdge(edge)
	if err != nil || source == "" || target == "" {
		return usageError("edge must use source:target")
	}
	if options.duration < 0 {
		return usageError("--duration must be zero or greater")
	}
	if options.probability <= 0 || options.probability > 1 {
		return usageError("--probability must be greater than zero and no more than 1")
	}
	if options.latency < 0 || options.jitter < 0 || options.latency+options.jitter > 60_000 {
		return usageError("--latency plus --jitter must be between 0 and 60000 milliseconds")
	}
	if options.status != 0 && (options.status < 400 || options.status > 599) {
		return usageError("--status must be between 400 and 599")
	}
	if options.latency == 0 && options.jitter == 0 && options.status == 0 && !options.abort {
		return usageError("define at least one effect with --latency, --jitter, --status, or --abort")
	}
	client, environment, err := c.current(ctx)
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
	if c.jsonOutput {
		return writeJSON(c.Out, created)
	}
	lifetime := "until disabled"
	if created.ExpiresAt != nil {
		lifetime = "until " + created.ExpiresAt.Local().Format(time.RFC3339)
	}
	fmt.Fprintf(c.Out, "fault %s active %s: %s\n", created.Name, lifetime, created.ScopeSummary)
	return nil
}

func (c *CLI) disableFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if _, err := client.SetFaultEnabled(ctx, environment.Project, environment.Name, name, false); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "disable", Project: environment.Project, Environment: environment.Name, Name: name, Status: "disabled"})
	}
	fmt.Fprintln(c.Out, "fault", name, "disabled")
	return nil
}

func (c *CLI) enableFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	fault, err := client.SetFaultEnabled(ctx, environment.Project, environment.Name, name, true)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, fault)
	}
	fmt.Fprintln(c.Out, "fault", name, "enabled")
	return nil
}

func (c *CLI) deleteFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := client.DeleteFault(ctx, environment.Project, environment.Name, name); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "fault", name, "deleted")
	return nil
}

func (c *CLI) clearFaults(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	disabled, err := client.DisableAllFaults(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		result := map[string]any{"disabled": disabled.Disabled}
		result["project"] = environment.Project
		result["environment"] = environment.Name
		return writeJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, "all active faults disabled")
	return nil
}
