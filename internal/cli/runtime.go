package cli

import (
	"context"
	"fmt"

	"github.com/portless-run/portless/internal/api/contract"
)

func (c *CLI) runtimeStatus(ctx context.Context, jsonOutput bool) error {
	return c.runtimeRequest(ctx, "status", "", jsonOutput)
}

func (c *CLI) startRuntime(ctx context.Context, jsonOutput bool) error {
	return c.runtimeRequest(ctx, "start", "", jsonOutput)
}

func (c *CLI) useRuntime(ctx context.Context, preference string, jsonOutput bool) error {
	return c.runtimeRequest(ctx, "use", preference, jsonOutput)
}

func (c *CLI) runtimeRequest(ctx context.Context, action, preference string, jsonOutput bool) error {
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	var status contract.RuntimeStatus
	switch action {
	case "status":
		status, err = client.RuntimeStatus(ctx)
	case "start":
		status, err = client.StartRuntime(ctx)
	case "use":
		status, err = client.UseRuntime(ctx, preference)
	default:
		err = fmt.Errorf("unsupported runtime action %q", action)
	}
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(c.Out, status)
	}
	c.printRuntimeStatus(status)
	return nil
}

func (c *CLI) printRuntimeStatus(status contract.RuntimeStatus) {
	state := status.State
	if state == "" {
		state = "unknown"
	}
	selected := "none"
	if status.Selected != "" {
		selected = string(status.Selected)
		if status.Version != "" {
			selected += " " + status.Version
		}
	}

	fmt.Fprintln(c.Out, c.heading(c.Out, "Container runtime"))
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %-11s %s\n", "Status:", c.state(c.Out, state))
	fmt.Fprintf(c.Out, "  %-11s %s\n", "Selected:", selected)
	fmt.Fprintf(c.Out, "  %-11s %s\n", "Preference:", status.Preference)
	if status.Reason != "" {
		fmt.Fprintf(c.Out, "  %-11s %s\n", "Reason:", status.Reason)
	}
	if len(status.Candidates) == 0 {
		return
	}

	ordered := make([]contract.RuntimeProbe, 0, len(status.Candidates))
	for _, candidate := range status.Candidates {
		if candidate.Name == status.Selected {
			ordered = append(ordered, candidate)
		}
	}
	for _, candidate := range status.Candidates {
		if candidate.Name != status.Selected {
			ordered = append(ordered, candidate)
		}
	}

	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("  %-10s %-10s %-10s %s", "RUNTIME", "STATE", "VERSION", "DETAILS")))
	for _, candidate := range ordered {
		version := candidate.Version
		if version == "" {
			version = "—"
		}
		details := candidate.Reason
		if candidate.Name == status.Selected {
			if details == "" {
				details = "selected"
			} else {
				details = "selected · " + details
			}
		}
		fmt.Fprintf(c.Out, "  %-10s %s %-10s %s\n", candidate.Name, c.state(c.Out, fmt.Sprintf("%-10s", candidate.State)), version, details)
	}
}
