package traffic

import (
	"context"
	"fmt"
	"time"

	"github.com/portless-run/portless/portless-cli/command"
)

func (c *Commands) showRecording(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	recording, err := client.Recording(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, recording)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Recording: "+recording.Name))
	fmt.Fprintf(c.Out, "\n  %-14s %s\n", "State:", c.State(c.Out, recording.Status))
	fmt.Fprintf(c.Out, "  %-14s %s → %s\n", "Scope:", command.EmptyAs(recording.Source, "any"), command.EmptyAs(recording.Target, "any"))
	fmt.Fprintf(c.Out, "  %-14s %d / %d\n", "Events:", recording.EventCount, recording.MaxEvents)
	fmt.Fprintf(c.Out, "  %-14s %s\n", "Started:", recording.StartedAt.Local().Format(time.RFC3339))
	if recording.CompletedAt != nil {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Completed:", recording.CompletedAt.Local().Format(time.RFC3339))
	}
	if recording.ExpiresAt != nil {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Expires:", recording.ExpiresAt.Local().Format(time.RFC3339))
	}
	return nil
}

func (c *Commands) showFault(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	fault, err := client.Fault(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, fault)
	}
	state := "disabled"
	if fault.Enabled {
		state = "active"
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Fault: "+fault.Name))
	fmt.Fprintf(c.Out, "\n  %-14s %s\n", "State:", c.State(c.Out, state))
	fmt.Fprintf(c.Out, "  %-14s %s → %s\n", "Scope:", fault.Source, fault.Target)
	fmt.Fprintf(c.Out, "  %-14s %.2f\n", "Probability:", fault.Probability)
	if fault.LatencyMS > 0 {
		fmt.Fprintf(c.Out, "  %-14s %dms (+%dms jitter)\n", "Latency:", fault.LatencyMS, fault.JitterMS)
	}
	if fault.StatusCode > 0 {
		fmt.Fprintf(c.Out, "  %-14s %d\n", "HTTP status:", fault.StatusCode)
	}
	if fault.Abort {
		fmt.Fprintf(c.Out, "  %-14s yes\n", "Abort:")
	}
	if fault.Method != "" || fault.Path != "" {
		fmt.Fprintf(c.Out, "  %-14s %s %s\n", "HTTP filter:", command.EmptyAs(fault.Method, "any"), command.EmptyAs(fault.Path, "any"))
	}
	fmt.Fprintf(c.Out, "  %-14s %d\n", "Matches:", fault.MatchCount)
	fmt.Fprintf(c.Out, "  %-14s %s\n", "Created:", fault.CreatedAt.Local().Format(time.RFC3339))
	if fault.ExpiresAt != nil {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Expires:", fault.ExpiresAt.Local().Format(time.RFC3339))
	} else {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Lifetime:", "until disabled")
	}
	return nil
}
