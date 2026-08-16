package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	apiclient "github.com/portless-run/portless/internal/api/client"
	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/model"
)

func (c *CLI) up(ctx context.Context, selector string, options upOptions) error {
	selector, err := c.effectiveEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	if err := c.requireIngress(ctx); err != nil {
		return err
	}
	var environment model.Environment
	warnings := []string{}
	if selector != "" {
		environment, err = c.loadEnvironment(ctx, client, selector)
	} else {
		environments, resolveErr := c.environmentsForCurrentPath(ctx, client)
		switch {
		case resolveErr != nil:
			err = resolveErr
		case len(environments) == 1:
			environment = environments[0]
		case len(environments) > 1:
			err = ambiguousEnvironmentError(environments)
		default:
			cwd, cwdErr := c.local.workingDirectory()
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
			warnings = nonNilStrings(discovered.Warnings)
			c.printWarnings(warnings)
		}
	}
	if err != nil {
		return err
	}
	request := contract.UpRequest{Managed: options.managed}
	if !options.managed {
		debugService := strings.TrimSpace(options.debug)
		if debugService == "" {
			cwd, cwdErr := c.local.workingDirectory()
			if cwdErr != nil {
				return cwdErr
			}
			debugService, err = debugServiceForPath(environment, cwd)
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
	idempotency, err := invocationKey("cli-up")
	if err != nil {
		return err
	}
	operation, err := client.UpEnvironment(operationContext, environment.Project, environment.Name, request, idempotency)
	if err != nil {
		return err
	}
	if !options.wait {
		if c.jsonOutput {
			return writeJSON(c.Out, upOutput{Environment: environment, Operation: operation, Warnings: warnings})
		}
		c.printOperation(operation)
		return nil
	}
	operation, err = c.waitOperation(operationContext, client, operation, c.jsonOutput)
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
	if c.jsonOutput {
		if err := writeJSON(c.Out, upOutput{Environment: environment, Operation: operation, Warnings: warnings}); err != nil {
			return err
		}
	} else {
		c.printStatus(environment)
		c.printDebugGuidance(environment)
	}
	if options.open {
		next := "/environments/" + apiclient.EscapePath(environment.Project, environment.Name)
		if browserURL, browserErr := c.browserURL(ctx, client, next); browserErr == nil {
			_ = c.local.launchBrowser(browserURL)
		}
	}
	return nil
}

func invocationKey(prefix string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create operation idempotency key: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(random[:]), nil
}

func (c *CLI) down(ctx context.Context, selector string, options downOptions) error {
	if options.all {
		if selector != "" || strings.TrimSpace(c.environmentOverride) != "" {
			return usageError("--all cannot be combined with --env or an environment selector")
		}
		client, _, err := c.daemon.Connect(ctx)
		if err != nil {
			return err
		}
		return c.downAll(ctx, client, options)
	}
	client, environment, err := c.currentOrNamed(ctx, selector)
	if err != nil {
		return err
	}
	operation, err := c.startDown(ctx, client, environment, options.volumes)
	if err != nil {
		return err
	}
	if options.wait {
		operation, err = c.waitDown(ctx, client, operation, options.timeout, c.jsonOutput)
		if err != nil {
			return err
		}
	}
	if c.jsonOutput {
		return writeJSON(c.Out, operation)
	}
	if !options.wait {
		c.printOperation(operation)
		return nil
	}
	fmt.Fprintf(c.Out, "%s/%s  %s\n", environment.Project, environment.Name, c.state(c.Out, "stopped"))
	return nil
}

func (c *CLI) status(ctx context.Context, selector string, jsonOutput bool) error {
	selector, err := c.effectiveEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	var environment model.Environment
	if selector != "" {
		environment, err = c.loadEnvironment(ctx, client, selector)
	} else {
		environment, err = c.findCurrent(ctx, client)
	}
	if err == nil {
		if jsonOutput {
			return writeJSON(c.Out, environment)
		}
		c.printStatus(environment)
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
		return writeJSON(c.Out, response)
	}
	if len(response.Environments) == 0 {
		fmt.Fprintln(c.Out, "No environments yet. Run `portless up` in a supported repository or create a multi-source project.")
		return nil
	}
	c.printEnvironmentListHeader()
	for _, item := range response.Environments {
		fmt.Fprintf(c.Out, "%-32s %s %d services\n", model.EnvironmentSelector(item.Project, item.Name), c.state(c.Out, fmt.Sprintf("%-14s", item.Status)), len(item.Services))
	}
	return nil
}

func (c *CLI) ui(ctx context.Context) error {
	client, _, err := c.daemon.Connect(ctx)
	if err != nil {
		return err
	}
	if err := c.requireIngress(ctx); err != nil {
		return err
	}
	next := "/projects"
	if environment, findErr := c.resolveEnvironment(ctx, client, ""); findErr == nil {
		next = "/environments/" + apiclient.EscapePath(environment.Project, environment.Name)
	}
	browserURL, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	launchErr := c.local.launchBrowser(browserURL)
	if c.jsonOutput {
		result := browserOutput{URL: browserURL, Opened: launchErr == nil}
		if launchErr != nil {
			result.Error = launchErr.Error()
		}
		return writeJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, "Portless control plane:", c.accent(c.Out, browserURL))
	if launchErr != nil {
		fmt.Fprintln(c.Err, "Could not open a browser; use the URL above:", launchErr)
	}
	return nil
}

func (c *CLI) open(ctx context.Context, requestedService string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := c.requireIngress(ctx); err != nil {
		return err
	}
	serviceName := environment.PrimaryService
	if requestedService != "" {
		serviceName = requestedService
	}
	if serviceName != "" {
		for _, service := range environment.Services {
			if strings.EqualFold(service.Name, serviceName) {
				if endpoint := serviceEndpointForProtocol(service, model.ProtocolHTTP); endpoint != nil {
					launchErr := c.local.launchBrowser(endpoint.URL)
					if c.jsonOutput {
						if err := writeJSON(c.Out, browserOutput{URL: endpoint.URL, Service: service.Name, Opened: launchErr == nil, Error: errorString(launchErr)}); err != nil {
							return err
						}
					} else {
						fmt.Fprintf(c.Out, "%s: %s\n", service.Name, c.accent(c.Out, endpoint.URL))
					}
					return launchErr
				}
				return fmt.Errorf("service %s does not expose an HTTP endpoint", serviceName)
			}
		}
		return fmt.Errorf("service %s was not found in %s/%s", serviceName, environment.Project, environment.Name)
	}
	next := "/environments/" + apiclient.EscapePath(environment.Project, environment.Name)
	browserURL, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	launchErr := c.local.launchBrowser(browserURL)
	if c.jsonOutput {
		if err := writeJSON(c.Out, browserOutput{URL: browserURL, Opened: launchErr == nil, Error: errorString(launchErr)}); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(c.Out, "Portless control plane:", c.accent(c.Out, browserURL))
	}
	return launchErr
}

func (c *CLI) waitOperation(ctx context.Context, client *apiclient.Client, operation model.Operation, jsonOutput bool) (model.Operation, error) {
	seen := 0
	for {
		current, err := client.Operation(ctx, operation.Project, operation.Environment, operation.Number)
		if err != nil {
			return model.Operation{}, err
		}
		operation = current
		for _, event := range operation.Events[seen:] {
			if !jsonOutput {
				fmt.Fprintf(c.Out, "  %-12s %s\n", event.Subject, event.Message)
			}
		}
		seen = len(operation.Events)
		if operation.State != "running" {
			return operation, nil
		}
		select {
		case <-ctx.Done():
			return model.Operation{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
