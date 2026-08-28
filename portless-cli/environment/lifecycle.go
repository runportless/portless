package environment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/runportless/portless/portless-cli/command"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

func (c *Commands) up(ctx context.Context, selector string, options upOptions) error {
	selector, err := c.EffectiveEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	if err := c.RequireIngress(ctx); err != nil {
		return err
	}
	var environment model.Environment
	warnings := []string{}
	if selector != "" {
		environment, err = c.LoadEnvironment(ctx, client, selector)
	} else {
		environments, resolveErr := c.EnvironmentsForCurrentPath(ctx, client)
		switch {
		case resolveErr != nil:
			err = resolveErr
		case len(environments) == 1:
			environment = environments[0]
		case len(environments) > 1:
			err = command.AmbiguousEnvironmentError(environments)
		default:
			cwd, cwdErr := c.Local.WorkingDirectory()
			if cwdErr != nil {
				err = cwdErr
				break
			}
			discovered, discoverErr := client.DiscoverProject(ctx, contract.DiscoverProjectRequest{Path: cwd, Name: options.name})
			if discoverErr != nil {
				err = discoverErr
				break
			}
			environment = discovered.Environment
			warnings = command.NonNilStrings(discovered.Warnings)
			c.PrintWarnings(warnings)
		}
	}
	if err != nil {
		return err
	}
	request := contract.UpRequest{Managed: options.managed}
	if !options.managed {
		debugService := strings.TrimSpace(options.debug)
		if debugService == "" {
			cwd, cwdErr := c.Local.WorkingDirectory()
			if cwdErr != nil {
				return cwdErr
			}
			debugService, err = command.DebugServiceForPath(environment, cwd)
			if err != nil {
				return err
			}
		}
		if debugService != "" {
			request.DebugServices = []string{debugService}
		}
	}
	operationContext, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()
	idempotency, err := command.InvocationKey("cli-up")
	if err != nil {
		return err
	}
	operation, err := client.UpEnvironment(operationContext, environment.Project, environment.Name, request, idempotency)
	if err != nil {
		return err
	}
	if !options.wait {
		if c.JSONOutput {
			return command.WriteJSON(c.Out, upOutput{Environment: environment, Operation: operation, Warnings: warnings})
		}
		c.PrintOperation(operation)
		return nil
	}
	operation, err = c.WaitOperation(operationContext, client, operation, c.JSONOutput)
	if err != nil {
		return err
	}
	if operation.State != "succeeded" {
		return errors.New(operation.Error)
	}
	environment, err = client.Environment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		if err := command.WriteJSON(c.Out, upOutput{Environment: environment, Operation: operation, Warnings: warnings}); err != nil {
			return err
		}
	} else {
		c.PrintStatus(environment)
		c.PrintDebugGuidance(environment)
	}
	if options.open {
		next := "/environments/" + apiclient.EscapePath(environment.Project, environment.Name)
		if browserURL, browserErr := c.BrowserURL(ctx, client, next); browserErr == nil {
			_ = c.Local.LaunchBrowser(browserURL)
		}
	}
	return nil
}

func (c *Commands) down(ctx context.Context, selector string, options downOptions) error {
	if options.all {
		if selector != "" || strings.TrimSpace(c.EnvironmentOverride) != "" {
			return command.UsageError("--all cannot be combined with --env or an environment selector")
		}
		client, _, err := c.Daemon.Connect(ctx)
		if err != nil {
			return err
		}
		return c.downAll(ctx, client, options)
	}
	client, environment, err := c.CurrentOrNamed(ctx, selector)
	if err != nil {
		return err
	}
	operation, err := c.startDown(ctx, client, environment, options.volumes)
	if err != nil {
		return err
	}
	if options.wait {
		operation, err = c.waitDown(ctx, client, operation, options.timeout, c.JSONOutput)
		if err != nil {
			return err
		}
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, operation)
	}
	if !options.wait {
		c.PrintOperation(operation)
		return nil
	}
	fmt.Fprintf(c.Out, "%s/%s  %s\n", environment.Project, environment.Name, c.State(c.Out, "stopped"))
	return nil
}

func (c *Commands) status(ctx context.Context, selector string, jsonOutput bool) error {
	selector, err := c.EffectiveEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	var environment model.Environment
	if selector != "" {
		environment, err = c.LoadEnvironment(ctx, client, selector)
	} else {
		environment, err = c.FindCurrent(ctx, client)
	}
	if err == nil {
		if jsonOutput {
			return command.WriteJSON(c.Out, environment)
		}
		c.PrintStatus(environment)
		return nil
	}
	if selector != "" {
		return err
	}
	response, requestErr := client.ListEnvironments(ctx, "", 100)
	if requestErr != nil {
		return requestErr
	}
	if response.Environments == nil {
		response.Environments = []model.Environment{}
	}
	if jsonOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Environments) == 0 {
		fmt.Fprintln(c.Out, "No environments yet. Run `portless up` in a supported repository or create a multi-source project.")
		return nil
	}
	c.PrintEnvironmentListHeader()
	for _, item := range response.Environments {
		fmt.Fprintf(c.Out, "%-32s %s %d services\n", model.EnvironmentSelector(item.Project, item.Name), c.State(c.Out, fmt.Sprintf("%-14s", item.Status)), len(item.Services))
	}
	return nil
}

func (c *Commands) ui(ctx context.Context) error {
	client, _, err := c.Daemon.Connect(ctx)
	if err != nil {
		return err
	}
	if err := c.RequireIngress(ctx); err != nil {
		return err
	}
	next := "/projects"
	if environment, findErr := c.ResolveEnvironment(ctx, client, ""); findErr == nil {
		next = "/environments/" + apiclient.EscapePath(environment.Project, environment.Name)
	}
	browserURL, err := c.BrowserURL(ctx, client, next)
	if err != nil {
		return err
	}
	launchErr := c.Local.LaunchBrowser(browserURL)
	if c.JSONOutput {
		result := command.BrowserOutput{URL: browserURL, Opened: launchErr == nil}
		if launchErr != nil {
			result.Error = launchErr.Error()
		}
		return command.WriteJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, "Portless control plane:", c.Accent(c.Out, browserURL))
	if launchErr != nil {
		fmt.Fprintln(c.Err, "Could not open a browser; use the URL above:", launchErr)
	}
	return nil
}

func (c *Commands) open(ctx context.Context, requestedService string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := c.RequireIngress(ctx); err != nil {
		return err
	}
	serviceName := environment.PrimaryService
	if requestedService != "" {
		serviceName = requestedService
	}
	if serviceName != "" {
		for _, service := range environment.Services {
			if strings.EqualFold(service.Name, serviceName) {
				if endpoint := command.ServiceEndpointForProtocol(service, model.ProtocolHTTP); endpoint != nil {
					launchErr := c.Local.LaunchBrowser(endpoint.URL)
					if c.JSONOutput {
						if err := command.WriteJSON(c.Out, command.BrowserOutput{URL: endpoint.URL, Service: service.Name, Opened: launchErr == nil, Error: command.ErrorString(launchErr)}); err != nil {
							return err
						}
					} else {
						fmt.Fprintf(c.Out, "%s: %s\n", service.Name, c.Accent(c.Out, endpoint.URL))
					}
					return launchErr
				}
				return fmt.Errorf("service %s does not expose an HTTP endpoint", serviceName)
			}
		}
		return fmt.Errorf("service %s was not found in %s/%s", serviceName, environment.Project, environment.Name)
	}
	next := "/environments/" + apiclient.EscapePath(environment.Project, environment.Name)
	browserURL, err := c.BrowserURL(ctx, client, next)
	if err != nil {
		return err
	}
	launchErr := c.Local.LaunchBrowser(browserURL)
	if c.JSONOutput {
		if err := command.WriteJSON(c.Out, command.BrowserOutput{URL: browserURL, Opened: launchErr == nil, Error: command.ErrorString(launchErr)}); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(c.Out, "Portless control plane:", c.Accent(c.Out, browserURL))
	}
	return launchErr
}
