package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func serviceType(service model.ServiceDefinition) string {
	if service.Framework != "" {
		return service.Framework
	}
	if service.Resource != nil {
		return service.Resource.Type
	}
	return string(service.Kind)
}

func providerFor(environment model.Environment, service string) model.ProviderKind {
	for _, binding := range environment.Bindings {
		if strings.EqualFold(binding.Service, service) {
			return binding.Provider
		}
	}
	return model.ProviderLocal
}

func (c *CLI) listServices(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	response, err := client.ListServices(ctx, environment.Project, environment.Name, limit)
	if err != nil {
		return err
	}
	if response.Services == nil {
		response.Services = []model.Service{}
	}
	response.Services = truncate(response.Services, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Services) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No services."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, "Services"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-22s %-11s %-12s %-12s %-13s %-11s %-9s %s", "SERVICE", "PROVIDER", "MODE", "KIND", "STATE", "GENERATION", "RESTARTS", "ENDPOINT")))
	for _, service := range response.Services {
		fmt.Fprintf(c.Out, "%-22s %-11s %-12s %-12s %s %-11d %-9d %s\n", service.Name, providerFor(environment, service.Name), serviceMode(environment, service), serviceType(service.ServiceDefinition), c.state(c.Out, fmt.Sprintf("%-13s", service.Status)), service.Generation, service.RestartCount, c.accent(c.Out, statusEndpoint(service)))
	}
	return nil
}

func (c *CLI) showService(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	service, err := client.Service(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, service)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, service.Name))
	fmt.Fprintf(c.Out, "\n  %-15s %s\n", "Provider:", providerFor(environment, service.Name))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Mode:", serviceMode(environment, service))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Kind:", serviceType(service.ServiceDefinition))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "State:", c.state(c.Out, string(service.Status)))
	if service.Reason != "" {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Reason:", service.Reason)
	}
	fmt.Fprintf(c.Out, "  %-15s %d\n", "Generation:", service.Generation)
	fmt.Fprintf(c.Out, "  %-15s %d\n", "Restarts:", service.RestartCount)
	if len(service.Endpoints) == 0 {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Endpoint:", "none")
	} else {
		for index, endpoint := range service.Endpoints {
			label := "Endpoint:"
			if index > 0 {
				label = ""
			}
			fmt.Fprintf(c.Out, "  %-15s %s\n", label, endpoint.URL)
		}
	}
	if service.UpstreamPort > 0 {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Runtime target:", fmt.Sprintf("127.0.0.1:%d", service.UpstreamPort))
	}
	if service.PID != 0 {
		fmt.Fprintf(c.Out, "  %-15s %d\n", "PID:", service.PID)
	}
	if service.StartedAt != nil {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Started:", service.StartedAt.Local().Format(time.RFC3339))
	}
	if service.Debugger != nil {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Debugger:", service.Debugger.Adapter)
		fmt.Fprintf(c.Out, "  %-15s %s:%d (%s)\n", "Debug address:", service.Debugger.Host, service.Debugger.Port, service.Debugger.State)
	}
	return nil
}

func (c *CLI) showServiceConfiguration(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	configuration, err := client.ServiceConfiguration(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if configuration.Command == nil {
		configuration.Command = []string{}
	}
	if configuration.Environment == nil {
		configuration.Environment = []model.ConfigurationValue{}
	}
	if c.jsonOutput {
		return writeJSON(c.Out, configuration)
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, configuration.Service+" configuration"), environment.Project, environment.Name)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Command:", strings.Join(configuration.Command, " "))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Working directory:", emptyAs(configuration.WorkingDirectory, "default"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Port variable:", emptyAs(configuration.PortEnvironment, "PORT"))
	fmt.Fprintln(c.Out, "\n"+c.muted(c.Out, fmt.Sprintf("%-28s %-28s %-14s %s", "KEY", "VALUE", "CLASS", "SOURCE")))
	for _, value := range configuration.Environment {
		fmt.Fprintf(c.Out, "%-28s %-28s %-14s %s\n", value.Key, value.Value, value.Classification, value.Source)
	}
	if len(configuration.Environment) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No environment values."))
	}
	return nil
}

func (c *CLI) serviceAction(ctx context.Context, action, name string, options serviceActionOptions) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	operation, err := client.ServiceAction(ctx, environment.Project, environment.Name, name, action)
	if err != nil {
		return err
	}
	if options.wait {
		waitContext, cancel := context.WithTimeout(ctx, options.timeout)
		defer cancel()
		operation, err = c.waitOperation(waitContext, client, operation, c.jsonOutput)
		if err != nil {
			return err
		}
		if operation.State != "succeeded" {
			return errors.New(operation.Error)
		}
	}
	if c.jsonOutput {
		return writeJSON(c.Out, operation)
	}
	if !options.wait {
		c.printOperation(operation)
		return nil
	}
	past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted", "debug": "debugging", "manage": "managed"}[action]
	fmt.Fprintf(c.Out, "%s %s\n", name, c.state(c.Out, past))
	return nil
}
