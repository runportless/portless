package projects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (c *Commands) listEnvironments(ctx context.Context, project string, limit int) error {
	if err := command.ValidLimit(limit, 1000); err != nil {
		return err
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListEnvironments(ctx, project, limit)
	if err != nil {
		return err
	}
	if response.Environments == nil {
		response.Environments = []model.Environment{}
	}
	response.Environments = command.Truncate(response.Environments, limit)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Environments) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No environments."))
		return nil
	}
	c.PrintEnvironmentListHeader()
	for _, item := range response.Environments {
		fmt.Fprintf(c.Out, "%-32s %s %d services\n", model.EnvironmentSelector(item.Project, item.Name), c.State(c.Out, fmt.Sprintf("%-14s", item.Status)), len(item.Services))
	}
	return nil
}

func (c *Commands) cloneEnvironment(ctx context.Context, name, from string) error {
	client, current, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if from == "" {
		from = current.Name
	}
	created, err := client.CloneEnvironment(ctx, contract.CloneEnvironmentRequest{Project: current.Project, Name: name, From: from})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, created)
	}
	fmt.Fprintln(c.Out, "created", model.EnvironmentSelector(created.Project, created.Name))
	return nil
}

func (c *Commands) bindProvider(ctx context.Context, service string, options bindingOptions) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	binding := model.ComponentBinding{Service: service, Provider: options.provider, Source: options.source}
	remote := model.RemoteTarget{URL: options.remoteURL, Classification: options.classification, WritePolicy: options.writePolicy, HealthPath: options.healthPath}
	if options.provider == model.ProviderRemote {
		binding.Remote = &remote
	} else if options.provider == model.ProviderMock {
		binding.Mock = &model.MockTarget{Profile: options.mockProfile}
	}
	idempotencyKey, err := command.InvocationKey("cli-change-provider")
	if err != nil {
		return err
	}
	operation, err := client.ChangeBinding(ctx, environment.Project, environment.Name, service, binding, idempotencyKey)
	if err != nil {
		return err
	}
	waitContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	operation, err = c.WaitOperation(waitContext, client, operation, c.JSONOutput)
	if err != nil {
		return err
	}
	if operation.State != "succeeded" {
		return errors.New(operation.Error)
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, operation)
	}
	detail := string(binding.Provider)
	if options.provider == model.ProviderLocal {
		detail += " source " + options.source
	} else if options.provider == model.ProviderRemote {
		detail += " " + remote.URL + " (" + string(remote.Classification) + ", " + string(remote.WritePolicy) + ")"
	} else if options.provider == model.ProviderMock {
		detail += " profile " + options.mockProfile
	}
	fmt.Fprintf(c.Out, "%s now uses %s for %s\n", model.EnvironmentSelector(environment.Project, environment.Name), detail, service)
	return nil
}

func (c *Commands) bindSource(ctx context.Context, source, pathValue string) error {
	sourcePath, err := absoluteSourcePath(pathValue)
	if err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	response, err := client.SetSource(ctx, environment.Project, environment.Name, source, sourcePath)
	if err != nil {
		return err
	}
	response.Warnings = command.NonNilStrings(response.Warnings)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	c.PrintWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "%s now uses %s for source %s\n", model.EnvironmentSelector(environment.Project, environment.Name), sourcePath, source)
	return nil
}

func (c *Commands) rescanEnvironment(ctx context.Context) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	response, err := client.RescanEnvironment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	response.Warnings = command.NonNilStrings(response.Warnings)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	c.PrintWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "%s rescanned (revision %d)\n", model.EnvironmentSelector(environment.Project, environment.Name), response.Environment.Revision)
	return nil
}

func (c *Commands) forgetEnvironment(ctx context.Context) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := client.ForgetEnvironment(ctx, environment.Project, environment.Name); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "forget", Project: environment.Project, Environment: environment.Name, Status: "forgotten"})
	}
	fmt.Fprintln(c.Out, "forgot", model.EnvironmentSelector(environment.Project, environment.Name))
	return nil
}

func (c *Commands) selectEnvironment(ctx context.Context, selector string) error {
	if c.EnvironmentOverride != "" {
		return command.UsageError("--env cannot be used with env select; pass the environment to env select directly")
	}
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	if _, err := c.LoadEnvironment(ctx, client, selector); err != nil {
		return err
	}
	root, err := c.CurrentSourceRoot(ctx)
	if err != nil {
		return err
	}
	if err := client.SelectEnvironment(ctx, contract.SelectEnvironmentRequest{Path: root, Project: project, Environment: environment}); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "select", Project: project, Environment: environment, Path: root, Status: "selected"})
	}
	fmt.Fprintln(c.Out, "selected", selector, "for", root)
	return nil
}

func (c *Commands) showEnvironmentContext(ctx context.Context) error {
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	resolved, err := c.ResolveEnvironmentContext(ctx, client)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, resolved)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Environment"))
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Effective:", c.Accent(c.Out, resolved.Selector))
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Resolution:", command.EnvironmentResolutionDescription(resolved.Resolution))
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Checkout:", resolved.Path)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "State:", c.State(c.Out, string(resolved.Environment.Status)))
	return nil
}

func (c *Commands) clearEnvironmentSelection(ctx context.Context) error {
	if c.EnvironmentOverride != "" {
		return command.UsageError("--env cannot be used with env clear; clear always applies to the current checkout")
	}
	root, err := c.CurrentSourceRoot(ctx)
	if err != nil {
		return err
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	result, err := client.ClearEnvironmentSelection(ctx, root)
	if err != nil {
		return err
	}
	status := "already-clear"
	if result.Cleared {
		status = "cleared"
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "clear", Path: root, Status: status})
	}
	if result.Cleared {
		fmt.Fprintln(c.Out, "cleared the environment selection for", root)
	} else {
		fmt.Fprintln(c.Out, "no saved environment selection for", root)
	}
	return nil
}
