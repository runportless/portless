package observe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-daemon/model"
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

func (c *Commands) listServices(ctx context.Context, limit int) error {
	if err := command.ValidLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.Current(ctx)
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
	response.Services = command.Truncate(response.Services, limit)
	if c.JSONOutput {
		return command.WriteJSON(c.Out, response)
	}
	if len(response.Services) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No services."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, "Services"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-22s %-11s %-12s %-12s %-13s %-11s %-9s %s", "SERVICE", "PROVIDER", "MODE", "KIND", "STATE", "GENERATION", "RESTARTS", "ENDPOINT")))
	for _, service := range response.Services {
		fmt.Fprintf(c.Out, "%-22s %-11s %-12s %-12s %s %-11d %-9d %s\n", service.Name, providerFor(environment, service.Name), command.ServiceMode(environment, service), serviceType(service.ServiceDefinition), c.State(c.Out, fmt.Sprintf("%-13s", service.Status)), service.Generation, service.RestartCount, c.Accent(c.Out, command.StatusEndpoint(service)))
	}
	return nil
}

func (c *Commands) showService(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	service, err := client.Service(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, service)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, service.Name))
	fmt.Fprintf(c.Out, "\n  %-15s %s\n", "Provider:", providerFor(environment, service.Name))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Mode:", command.ServiceMode(environment, service))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Kind:", serviceType(service.ServiceDefinition))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "State:", c.State(c.Out, string(service.Status)))
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

func (c *Commands) showServiceConfiguration(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
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
	if c.JSONOutput {
		return command.WriteJSON(c.Out, configuration)
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, configuration.Service+" configuration"), environment.Project, environment.Name)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Command:", strings.Join(configuration.Command, " "))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Working directory:", command.EmptyAs(configuration.WorkingDirectory, "default"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Port variable:", command.EmptyAs(configuration.PortEnvironment, "PORT"))
	fmt.Fprintln(c.Out, "\n"+c.Muted(c.Out, fmt.Sprintf("%-28s %-28s %-14s %s", "KEY", "VALUE", "CLASS", "SOURCE")))
	for _, value := range configuration.Environment {
		fmt.Fprintf(c.Out, "%-28s %-28s %-14s %s\n", value.Key, value.Value, value.Classification, value.Source)
	}
	if len(configuration.Environment) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No environment values."))
	}
	return nil
}

func (c *Commands) serviceAction(ctx context.Context, action, name string, options serviceActionOptions) error {
	client, environment, err := c.Current(ctx)
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
		operation, err = c.WaitOperation(waitContext, client, operation, c.JSONOutput)
		if err != nil {
			return err
		}
		if operation.State != "succeeded" {
			return errors.New(operation.Error)
		}
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, operation)
	}
	if !options.wait {
		c.PrintOperation(operation)
		return nil
	}
	past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted", "debug": "debugging", "manage": "managed"}[action]
	fmt.Fprintf(c.Out, "%s %s\n", name, c.State(c.Out, past))
	return nil
}
