package traffic

import (
	"context"
	"fmt"
	"time"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (c *Commands) listRecordings(ctx context.Context, limit int) error {
	if err := command.ValidLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListRecordings(ctx, environment.Project, environment.Name, limit)
	if err != nil {
		return err
	}
	if response.Recordings == nil {
		response.Recordings = []model.Recording{}
	}
	response.Recordings = command.Truncate(response.Recordings, limit)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Recordings) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No recordings."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, "Recordings"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-24s %-10s %-8s %s", "NAME", "STATE", "EVENTS", "EDGE")))
	for _, item := range response.Recordings {
		fmt.Fprintf(c.Out, "%-24s %s %-8d %s → %s\n", item.Name, c.State(c.Out, fmt.Sprintf("%-10s", item.Status)), item.EventCount, command.EmptyAs(item.Source, "any"), command.EmptyAs(item.Target, "any"))
	}
	return nil
}

func (c *Commands) startRecording(ctx context.Context, name string, options recordingOptions) error {
	if options.duration <= 0 || options.duration > time.Hour {
		return command.UsageError("--duration must be greater than zero and no more than 1h")
	}
	if options.maxEvents < 1 || options.maxEvents > 100_000 {
		return command.UsageError("--max-events must be between 1 and 100000")
	}
	source, target, err := command.ParseEdge(options.edge)
	if err != nil {
		return command.UsageError("--edge must use source:target")
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(options.duration)
	input := model.Recording{Name: name, Source: source, Target: target, MaxEvents: options.maxEvents, ExpiresAt: &expires}
	created, err := client.StartRecording(ctx, environment.Project, environment.Name, input)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, created)
	}
	fmt.Fprintf(c.Out, "recording %s started; expires %s\n", created.Name, created.ExpiresAt.Format(time.RFC3339))
	return nil
}

func (c *Commands) stopRecording(ctx context.Context, requestedName string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	arguments := []string(nil)
	if requestedName != "" {
		arguments = []string{requestedName}
	}
	name, err := recordingName(ctx, client, environment.Project, environment.Name, arguments)
	if err != nil {
		return err
	}
	stopped, err := client.StopRecording(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, stopped)
	}
	fmt.Fprintln(c.Out, "recording", name, "stopped")
	return nil
}

func (c *Commands) exportRecording(ctx context.Context, name string, options exportOptions) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	content, err := client.ExportRecording(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if options.output == "-" {
		_, err = c.Out.Write(content)
		return err
	}
	if err := writePrivateFile(options.output, content, options.force); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "export", Project: environment.Project, Environment: environment.Name, Name: name, Path: options.output, Status: "written"})
	}
	fmt.Fprintln(c.Out, "wrote", options.output)
	return nil
}

func (c *Commands) deleteRecording(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := client.DeleteRecording(ctx, environment.Project, environment.Name, name); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "recording", name, "deleted")
	return nil
}
