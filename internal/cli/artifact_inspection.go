package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) timeline(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	response, err := client.Timeline(ctx, environment.Project, environment.Name, limit)
	if err != nil {
		return err
	}
	if response.Timeline == nil {
		response.Timeline = []model.TimelineEvent{}
	}
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Timeline) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No timeline events."))
		return nil
	}
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-18s %-24s %-20s %-10s %s", "WHEN", "TYPE", "SUBJECT", "SEVERITY", "SUMMARY")))
	for _, event := range response.Timeline {
		fmt.Fprintf(c.Out, "%-18s %-24s %-20s %-10s %s\n", relative(event.Timestamp), event.Type, emptyAs(event.Subject, "—"), event.Severity, event.Summary)
	}
	return nil
}

func (c *CLI) showRecording(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	recording, err := client.Recording(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, recording)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Recording: "+recording.Name))
	fmt.Fprintf(c.Out, "\n  %-14s %s\n", "State:", c.state(c.Out, recording.Status))
	fmt.Fprintf(c.Out, "  %-14s %s → %s\n", "Scope:", emptyAs(recording.Source, "any"), emptyAs(recording.Target, "any"))
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

func (c *CLI) showFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	fault, err := client.Fault(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, fault)
	}
	state := "disabled"
	if fault.Enabled {
		state = "active"
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Fault: "+fault.Name))
	fmt.Fprintf(c.Out, "\n  %-14s %s\n", "State:", c.state(c.Out, state))
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
		fmt.Fprintf(c.Out, "  %-14s %s %s\n", "HTTP filter:", emptyAs(fault.Method, "any"), emptyAs(fault.Path, "any"))
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
