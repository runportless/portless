package projects

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (c *Commands) createProject(ctx context.Context, name string, sourceValues []string) error {
	var sources []contract.SourceInput
	for _, value := range sourceValues {
		sourceName, sourcePath, found := strings.Cut(value, "=")
		if !found || sourceName == "" || sourcePath == "" {
			return command.UsageError("each --source must use name=path")
		}
		sourcePath, err := absoluteSourcePath(sourcePath)
		if err != nil {
			return fmt.Errorf("source %s: %w", sourceName, err)
		}
		sources = append(sources, contract.SourceInput{Name: sourceName, Path: sourcePath})
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	response, err := client.CreateProject(ctx, contract.CreateProjectRequest{Name: name, Sources: sources})
	if err != nil {
		return err
	}
	response.Warnings = command.NonNilStrings(response.Warnings)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	c.PrintWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "created %s with environment %s and %d sources\n", response.Project.Name, response.Environment.Name, len(sources))
	return nil
}

func (c *Commands) addProjectSource(ctx context.Context, source, pathValue string) error {
	sourcePath, err := absoluteSourcePath(pathValue)
	if err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	response, err := client.AddProjectSource(ctx, environment.Project, contract.AddProjectSourceRequest{Name: source, Path: sourcePath, Environment: environment.Name})
	if err != nil {
		return err
	}
	response.Warnings = command.NonNilStrings(response.Warnings)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	c.PrintWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "%s source %s to project %s\n", c.Success(c.Out, "added"), source, response.Project.Name)
	fmt.Fprintf(c.Out, "%s now uses %s for source %s\n", model.EnvironmentSelector(response.Environment.Project, response.Environment.Name), sourcePath, source)
	pending := command.NonNilStrings(response.ConfigurationRequired)
	if len(pending) > 0 {
		label := "environment requires"
		if len(pending) != 1 {
			label = "environments require"
		}
		fmt.Fprintf(c.Out, "%d other %s configuration: %s\n", len(pending), label, strings.Join(pending, ", "))
		for _, selector := range pending {
			fmt.Fprintln(c.Out, "  "+c.Accent(c.Out, "portless --env "+selector+" env source "+source+" --path <checkout>"))
		}
		fmt.Fprintln(c.Out, "Or bind the new services remotely with `portless env bind`.")
	}
	return nil
}

func (c *Commands) exportProject(ctx context.Context, output string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	content, err := client.ExportProject(ctx, environment.Project)
	if err != nil {
		return err
	}
	if output == "-" {
		_, err = c.Out.Write(content)
		return err
	}
	if err := os.WriteFile(output, content, 0o600); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "export", Project: environment.Project, Path: output, Status: "written"})
	}
	fmt.Fprintln(c.Out, "wrote", output)
	return nil
}

func (c *Commands) renameProject(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	project, err := client.Project(ctx, environment.Project)
	if err != nil {
		return err
	}
	renamed, err := client.RenameProject(ctx, environment.Project, contract.RenameProjectRequest{Name: name, Revision: project.Revision})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, renamed)
	}
	fmt.Fprintf(c.Out, "%s renamed to %s\n", project.Name, renamed.Name)
	return nil
}

func (c *Commands) forgetProject(ctx context.Context) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := client.ForgetProject(ctx, environment.Project); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "forget", Project: environment.Project, Status: "forgotten"})
	}
	fmt.Fprintln(c.Out, "forgot", environment.Project)
	return nil
}

func absoluteSourcePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}
