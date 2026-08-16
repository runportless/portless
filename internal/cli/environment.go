package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) listEnvironments(ctx context.Context, project string, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, _, err := c.daemon.Connect(ctx)
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
	response.Environments = truncate(response.Environments, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Environments) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No environments."))
		return nil
	}
	c.printEnvironmentListHeader()
	for _, item := range response.Environments {
		fmt.Fprintf(c.Out, "%-32s %s %d services\n", model.EnvironmentSelector(item.Project, item.Name), c.state(c.Out, fmt.Sprintf("%-14s", item.Status)), len(item.Services))
	}
	return nil
}

func (c *CLI) cloneEnvironment(ctx context.Context, name, from string) error {
	client, current, err := c.current(ctx)
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
	if c.jsonOutput {
		return writeJSON(c.Out, created)
	}
	fmt.Fprintln(c.Out, "created", model.EnvironmentSelector(created.Project, created.Name))
	return nil
}

func (c *CLI) bindProvider(ctx context.Context, service string, options bindingOptions) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	binding := model.ComponentBinding{Service: service, Provider: options.provider, Source: options.source}
	remote := model.RemoteTarget{URL: options.remoteURL, Classification: options.classification, WritePolicy: options.writePolicy, HealthPath: options.healthPath}
	if options.provider == model.ProviderRemote {
		binding.Remote = &remote
	}
	updated, err := client.SetBinding(ctx, environment.Project, environment.Name, service, binding)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, updated)
	}
	detail := string(binding.Provider)
	if options.provider == model.ProviderLocal {
		detail += " source " + options.source
	} else if options.provider == model.ProviderRemote {
		detail += " " + remote.URL + " (" + string(remote.Classification) + ", " + string(remote.WritePolicy) + ")"
	}
	fmt.Fprintf(c.Out, "%s now uses %s for %s\n", model.EnvironmentSelector(environment.Project, environment.Name), detail, service)
	return nil
}

func (c *CLI) bindSource(ctx context.Context, source, pathValue string) error {
	sourcePath, err := absoluteSourcePath(pathValue)
	if err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	response, err := client.SetSource(ctx, environment.Project, environment.Name, source, sourcePath)
	if err != nil {
		return err
	}
	response.Warnings = nonNilStrings(response.Warnings)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	c.printWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "%s now uses %s for source %s\n", model.EnvironmentSelector(environment.Project, environment.Name), sourcePath, source)
	return nil
}

func (c *CLI) rescanEnvironment(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	response, err := client.RescanEnvironment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	response.Warnings = nonNilStrings(response.Warnings)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	c.printWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "%s rescanned (revision %d)\n", model.EnvironmentSelector(environment.Project, environment.Name), response.Environment.Revision)
	return nil
}

func (c *CLI) forgetEnvironment(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := client.ForgetEnvironment(ctx, environment.Project, environment.Name); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "forget", Project: environment.Project, Environment: environment.Name, Status: "forgotten"})
	}
	fmt.Fprintln(c.Out, "forgot", model.EnvironmentSelector(environment.Project, environment.Name))
	return nil
}

func (c *CLI) selectEnvironment(ctx context.Context, selector string) error {
	if c.environmentOverride != "" {
		return usageError("--env cannot be used with env select; pass the environment to env select directly")
	}
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	if _, err := c.loadEnvironment(ctx, client, selector); err != nil {
		return err
	}
	root, err := c.currentSourceRoot(ctx)
	if err != nil {
		return err
	}
	if err := client.SelectEnvironment(ctx, contract.SelectEnvironmentRequest{Path: root, Project: project, Environment: environment}); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "select", Project: project, Environment: environment, Path: root, Status: "selected"})
	}
	fmt.Fprintln(c.Out, "selected", selector, "for", root)
	return nil
}

func (c *CLI) showEnvironmentContext(ctx context.Context) error {
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	resolved, err := c.resolveEnvironmentContext(ctx, client)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, resolved)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Environment"))
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Effective:", c.accent(c.Out, resolved.Selector))
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Resolution:", environmentResolutionDescription(resolved.Resolution))
	fmt.Fprintf(c.Out, "  %-12s %s\n", "Checkout:", resolved.Path)
	fmt.Fprintf(c.Out, "  %-12s %s\n", "State:", c.state(c.Out, string(resolved.Environment.Status)))
	return nil
}

func (c *CLI) clearEnvironmentSelection(ctx context.Context) error {
	if c.environmentOverride != "" {
		return usageError("--env cannot be used with env clear; clear always applies to the current checkout")
	}
	root, err := c.currentSourceRoot(ctx)
	if err != nil {
		return err
	}
	client, _, err := c.daemon.Connect(ctx)
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
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "clear", Path: root, Status: status})
	}
	if result.Cleared {
		fmt.Fprintln(c.Out, "cleared the environment selection for", root)
	} else {
		fmt.Fprintln(c.Out, "no saved environment selection for", root)
	}
	return nil
}

func (c *CLI) current(ctx context.Context) (*apiclient.Client, model.Environment, error) {
	return c.currentOrNamed(ctx, "")
}

func (c *CLI) currentOrNamed(ctx context.Context, selector string) (*apiclient.Client, model.Environment, error) {
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return nil, model.Environment{}, err
	}
	environment, err := c.resolveEnvironment(ctx, client, selector)
	if err != nil {
		return nil, model.Environment{}, err
	}
	return client, environment, nil
}

func (c *CLI) findCurrent(ctx context.Context, client *apiclient.Client) (model.Environment, error) {
	resolved, err := c.resolveEnvironmentContext(ctx, client)
	if err != nil {
		return model.Environment{}, err
	}
	return resolved.Environment, nil
}

func (c *CLI) resolveEnvironment(ctx context.Context, client *apiclient.Client, selector string) (model.Environment, error) {
	effective, err := c.effectiveEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	if effective != "" {
		return c.loadEnvironment(ctx, client, effective)
	}
	return c.findCurrent(ctx, client)
}

func (c *CLI) effectiveEnvironmentSelector(selector string) (string, error) {
	if selector != "" && c.environmentOverride != "" {
		return "", usageError("an environment was provided twice; use only --env")
	}
	if selector != "" {
		return selector, nil
	}
	return c.environmentOverride, nil
}

func (c *CLI) resolveEnvironmentContext(ctx context.Context, client *apiclient.Client) (environmentContextOutput, error) {
	root, err := c.currentSourceRoot(ctx)
	if err != nil {
		return environmentContextOutput{}, err
	}
	if c.environmentOverride != "" {
		environment, err := c.loadEnvironment(ctx, client, c.environmentOverride)
		if err != nil {
			return environmentContextOutput{}, err
		}
		return environmentContextOutput{
			Path: root, Selector: model.EnvironmentSelector(environment.Project, environment.Name),
			Resolution: "flag", Environment: environment,
		}, nil
	}
	response, err := client.EnvironmentContext(ctx, root)
	if err != nil {
		return environmentContextOutput{}, err
	}
	if response.Environment == nil {
		if response.Resolution == "ambiguous" {
			return environmentContextOutput{}, ambiguousEnvironmentError(response.Candidates)
		}
		return environmentContextOutput{}, errors.New("this checkout is not part of a Portless environment; run `portless up` or `portless project create`")
	}
	environment := *response.Environment
	return environmentContextOutput{
		Path: root, Selector: model.EnvironmentSelector(environment.Project, environment.Name),
		Resolution: response.Resolution, Environment: environment,
	}, nil
}

func (c *CLI) environmentsForCurrentPath(ctx context.Context, client *apiclient.Client) ([]model.Environment, error) {
	root, err := c.currentSourceRoot(ctx)
	if err != nil {
		return nil, err
	}
	response, err := client.EnvironmentsForPath(ctx, root)
	if err != nil {
		return nil, err
	}
	return response.Environments, nil
}

func (c *CLI) currentSourceRoot(ctx context.Context) (string, error) {
	cwd, err := c.local.workingDirectory()
	if err != nil {
		return "", err
	}
	root, err := c.local.findProjectRoot(ctx, cwd)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		root = cwd
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	return root, nil
}

func currentWorkingDirectory() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(cwd); resolveErr == nil {
		cwd = resolved
	}
	return filepath.Abs(cwd)
}

func debugServiceForPath(environment model.Environment, path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	bestName, bestDirectory := "", ""
	for _, service := range environment.Services {
		if service.Kind != model.ServiceProcess || providerFor(environment, service.Name) != model.ProviderLocal {
			continue
		}
		for _, candidate := range serviceDirectories(environment, service) {
			directory, absErr := filepath.Abs(candidate)
			if absErr != nil {
				continue
			}
			relative, relErr := filepath.Rel(directory, path)
			if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			if len(directory) < len(bestDirectory) {
				continue
			}
			if len(directory) == len(bestDirectory) && bestName != "" && !strings.EqualFold(bestName, service.Name) {
				return "", fmt.Errorf("%s contains more than one service; choose one with `portless up --debug <service>`", path)
			}
			bestName, bestDirectory = service.Name, directory
		}
	}
	return bestName, nil
}

func serviceDirectories(environment model.Environment, service model.Service) []string {
	if service.ServiceDirectory != "" {
		return []string{service.ServiceDirectory}
	}
	var result []string
	binding := model.ComponentBinding{}
	for _, candidate := range environment.Bindings {
		if strings.EqualFold(candidate.Service, service.Name) {
			binding = candidate
			break
		}
	}
	var sourceRoot string
	for _, source := range environment.Sources {
		if strings.EqualFold(source.Name, binding.Source) {
			sourceRoot = source.Path
			break
		}
	}
	if sourceRoot != "" {
		for _, evidence := range service.Evidence {
			if evidence.File == "" {
				continue
			}
			result = append(result, filepath.Join(sourceRoot, filepath.Dir(filepath.FromSlash(evidence.File))))
		}
	}
	return result
}

func (c *CLI) loadEnvironment(ctx context.Context, client *apiclient.Client, selector string) (model.Environment, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	result, err := client.Environment(ctx, project, environment)
	if err != nil {
		return model.Environment{}, err
	}
	return result, nil
}

func ambiguousEnvironmentError(environments []model.Environment) error {
	selectors := make([]string, 0, len(environments))
	for _, environment := range environments {
		selectors = append(selectors, model.EnvironmentSelector(environment.Project, environment.Name))
	}
	return fmt.Errorf("this checkout belongs to multiple environments (%s); select one with `portless env select project/environment` or pass `--env project/environment`", strings.Join(selectors, ", "))
}

func environmentResolutionDescription(resolution string) string {
	switch resolution {
	case "flag":
		return "--env override for this invocation"
	case "selected":
		return "saved selection for this checkout"
	case "inferred":
		return "only environment using this checkout"
	default:
		return resolution
	}
}
