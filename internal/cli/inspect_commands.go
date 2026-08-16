package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/portless-run/portless/internal/model"
)

func validLimit(limit, maximum int) error {
	if limit <= 0 || limit > maximum {
		return usageError("--limit must be between 1 and %d", maximum)
	}
	return nil
}

func truncate[T any](items []T, limit int) []T {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func (c *CLI) listProjects(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListProjects(ctx, limit)
	if err != nil {
		return err
	}
	if response.Projects == nil {
		response.Projects = []model.Project{}
	}
	response.Projects = truncate(response.Projects, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Projects) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No projects."))
		return nil
	}
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-24s %-12s %-10s %-9s %s", "PROJECT", "ENVIRONMENTS", "SERVICES", "SOURCES", "DASHBOARD")))
	for _, project := range response.Projects {
		fmt.Fprintf(c.Out, "%-24s %-12d %-10d %-9d %s\n", project.Name, len(project.Environments), len(project.Services), len(project.Sources), c.accent(c.Out, project.DashboardURL))
	}
	return nil
}

func (c *CLI) showProject(ctx context.Context, requested string) error {
	if requested != "" && c.environmentOverride != "" {
		return usageError("an explicit project cannot be combined with --env")
	}
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	name := requested
	if name == "" {
		environment, err := c.resolveEnvironment(ctx, client, "")
		if err != nil {
			return err
		}
		name = environment.Project
	}
	project, err := client.Project(ctx, name)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, project)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, project.Name))
	fmt.Fprintf(c.Out, "\n  %-15s %s\n", "Dashboard:", c.accent(c.Out, project.DashboardURL))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Primary service:", emptyAs(project.PrimaryService, "not selected"))
	fmt.Fprintf(c.Out, "  %-15s %d\n", "Revision:", project.Revision)
	fmt.Fprintln(c.Out, "\n"+c.heading(c.Out, "Sources"))
	if len(project.Sources) == 0 {
		fmt.Fprintln(c.Out, "  none")
	}
	for _, source := range project.Sources {
		fmt.Fprintf(c.Out, "  %-20s %s\n", source.Name, strings.Join(source.Services, ", "))
	}
	fmt.Fprintln(c.Out, "\n"+c.heading(c.Out, "Environments"))
	if len(project.Environments) == 0 {
		fmt.Fprintln(c.Out, "  none")
	}
	for _, environment := range project.Environments {
		fmt.Fprintf(c.Out, "  %-20s %-12s %d/%d ready  %s\n", environment.Name, c.state(c.Out, string(environment.Status)), environment.ReadyCount, environment.ServiceCount, environment.DashboardURL)
	}
	fmt.Fprintln(c.Out, "\n"+c.heading(c.Out, "Services"))
	if len(project.Services) == 0 {
		fmt.Fprintln(c.Out, "  none")
	}
	for _, service := range project.Services {
		fmt.Fprintf(c.Out, "  %-20s %-10s %s\n", service.Name, service.Kind, serviceType(service))
	}
	fmt.Fprintln(c.Out, "\n"+c.heading(c.Out, "Connections"))
	if len(project.Connections) == 0 {
		fmt.Fprintln(c.Out, "  none")
	}
	for _, connection := range project.Connections {
		fmt.Fprintf(c.Out, "  %-28s %-10s env %s\n", connection.Source+":"+connection.Target, connection.Protocol, emptyAs(connection.Environment, "—"))
	}
	return nil
}
