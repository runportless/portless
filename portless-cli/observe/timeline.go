package observe

import (
	"context"
	"fmt"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (c *Commands) timeline(ctx context.Context, limit int) error {
	if err := command.ValidLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
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
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Timeline) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No timeline events."))
		return nil
	}
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-18s %-24s %-20s %-10s %s", "WHEN", "TYPE", "SUBJECT", "SEVERITY", "SUMMARY")))
	for _, event := range response.Timeline {
		fmt.Fprintf(c.Out, "%-18s %-24s %-20s %-10s %s\n", relative(event.Timestamp), event.Type, command.EmptyAs(event.Subject, "—"), event.Severity, event.Summary)
	}
	return nil
}
