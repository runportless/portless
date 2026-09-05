package mocks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

func (c *Commands) list(ctx context.Context) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	result, err := client.ListMockScenarios(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if result.Scenarios == nil {
		result.Scenarios = []model.MockScenario{}
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	if len(result.Scenarios) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No mock scenarios."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, "Mocks"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-24s %-10s %-28s %-8s %s", "SCENARIO", "STATE", "SERVICES", "ROUTES", "MODIFIED")))
	for _, scenario := range result.Scenarios {
		services := strings.Join(scenario.Activation.TargetServices, ",")
		if services == "" {
			services = "—"
		}
		fmt.Fprintf(c.Out, "%-24s %-10s %-28s %-8d %s\n", scenario.Name, c.State(c.Out, string(scenario.Activation.State)), services, len(scenario.Routes), scenario.ModifiedAt.Local().Format(time.RFC3339))
	}
	return nil
}

func (c *Commands) show(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	scenario, err := client.MockScenario(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, scenario)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, scenario.Name))
	fmt.Fprintf(c.Out, "  %-13s %s\n", "State:", c.State(c.Out, string(scenario.Activation.State)))
	if scenario.Description != "" {
		fmt.Fprintf(c.Out, "  %-13s %s\n", "Description:", scenario.Description)
	}
	services := strings.Join(scenario.Activation.TargetServices, ", ")
	if services == "" {
		services = "none"
	}
	fmt.Fprintf(c.Out, "  %-13s %s\n", "Services:", services)
	fmt.Fprintf(c.Out, "  %-13s %d\n\n", "Routes:", len(scenario.Routes))
	if len(scenario.Routes) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No routes."))
		return nil
	}
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-22s %-20s %-8s %-32s %-7s %s", "ROUTE", "SERVICE", "METHOD", "PATH", "STATUS", "STATE")))
	for _, route := range scenario.Routes {
		state := "enabled"
		if !route.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(c.Out, "%-22s %-20s %-8s %-32s %-7d %s\n", route.Name, route.Service, route.Method, route.Path, route.Status, c.State(c.Out, state))
	}
	return nil
}

func (c *Commands) create(ctx context.Context, name, description string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	scenario, err := client.CreateMockScenario(ctx, environment.Project, environment.Name, contract.CreateMockRequest{Name: name, Description: description})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, scenario)
	}
	fmt.Fprintf(c.Out, "mock scenario %s created\n", scenario.Name)
	return nil
}

func (c *Commands) setEnabled(ctx context.Context, name string, enabled bool) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	idempotencyKey, err := command.InvocationKey("cli-set-mock-scenario")
	if err != nil {
		return err
	}
	operation, err := client.SetMockScenarioEnabled(ctx, environment.Project, environment.Name, name, contract.SetMockScenarioActivationRequest{Enabled: enabled}, idempotencyKey)
	if err != nil {
		return err
	}
	waitContext, cancel := context.WithTimeout(ctx, 10*time.Minute)
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
	action := "disabled"
	if enabled {
		action = "enabled"
	}
	fmt.Fprintf(c.Out, "mock scenario %s %s\n", name, action)
	return nil
}

func (c *Commands) delete(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := client.DeleteMockScenario(ctx, environment.Project, environment.Name, name); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "mock scenario", name, "deleted")
	return nil
}

func (c *Commands) setRoute(ctx context.Context, scenarioName, routeName string, options routeOptions) error {
	query, err := keyValueMap(options.query, "query")
	if err != nil {
		return err
	}
	headers, err := keyValueMap(options.header, "header")
	if err != nil {
		return err
	}
	body := options.body
	if options.bodyFile != "" {
		content, readErr := os.ReadFile(options.bodyFile)
		if readErr != nil {
			return readErr
		}
		body = string(content)
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	route := model.MockRoute{Name: routeName, Service: options.service, Method: options.method, Path: options.path, Query: query, Status: options.status, Headers: headers, Body: body, DelayMS: options.delay, Enabled: !options.disabled}
	updated, err := client.PutMockRoute(ctx, environment.Project, environment.Name, scenarioName, routeName, route)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, updated)
	}
	fmt.Fprintf(c.Out, "route %s updated for %s in mock scenario %s\n", routeName, route.Service, updated.Name)
	return nil
}

func (c *Commands) deleteRoute(ctx context.Context, scenarioName, routeName string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	updated, err := client.DeleteMockRoute(ctx, environment.Project, environment.Name, scenarioName, routeName)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, updated)
	}
	fmt.Fprintf(c.Out, "route %s deleted from mock scenario %s\n", routeName, updated.Name)
	return nil
}

func (c *Commands) preview(ctx context.Context, scenarioName string, options previewOptions) error {
	values, err := keyValueValues(options.query, "query")
	if err != nil {
		return err
	}
	headers, err := keyValueValues(options.header, "header")
	if err != nil {
		return err
	}
	body := options.body
	if options.bodyFile != "" {
		content, readErr := os.ReadFile(options.bodyFile)
		if readErr != nil {
			return readErr
		}
		body = string(content)
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	preview, err := client.PreviewMock(ctx, environment.Project, environment.Name, scenarioName, model.MockRequest{Service: options.service, Method: options.method, Path: options.path, Query: values, Headers: headers, Body: body})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, preview)
	}
	if !preview.Matched {
		fmt.Fprintf(c.Out, "no %s route matched; the mock would return %d\n", preview.Service, preview.Status)
		return nil
	}
	fmt.Fprintf(c.Out, "matched %s for %s · %d", c.Accent(c.Out, preview.Route), preview.Service, preview.Status)
	if preview.DelayMS > 0 {
		fmt.Fprintf(c.Out, " · %dms delay", preview.DelayMS)
	}
	fmt.Fprintln(c.Out)
	if preview.Body != "" {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, preview.Body)
	}
	return nil
}

func (c *Commands) importRecording(ctx context.Context, scenarioName, recording string, services []string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	result, err := client.ImportMockRecording(ctx, environment.Project, environment.Name, scenarioName, contract.ImportMockRecordingRequest{Recording: recording, Services: services})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	c.PrintWarnings(result.Warnings)
	fmt.Fprintf(c.Out, "imported recording %s into mock scenario %s (%d routes)\n", recording, result.Scenario.Name, len(result.Scenario.Routes))
	return nil
}

func (c *Commands) importOpenAPI(ctx context.Context, scenarioName, service, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(content) > 1<<20 {
		return command.UsageError("OpenAPI document must not exceed 1048576 bytes")
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	result, err := client.ImportMockOpenAPI(ctx, environment.Project, environment.Name, scenarioName, contract.ImportMockOpenAPIRequest{Service: service, Document: string(content)})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	c.PrintWarnings(result.Warnings)
	fmt.Fprintf(c.Out, "imported OpenAPI routes for %s into mock scenario %s (%d routes)\n", service, result.Scenario.Name, len(result.Scenario.Routes))
	return nil
}

func keyValueMap(values []string, label string) (map[string]string, error) {
	result := map[string]string{}
	for _, value := range values {
		name, item, found := strings.Cut(value, "=")
		if !found || strings.TrimSpace(name) == "" {
			return nil, command.UsageError("--%s must use name=value", label)
		}
		result[name] = item
	}
	return result, nil
}

func keyValueValues(values []string, label string) (map[string][]string, error) {
	parsed := map[string][]string{}
	for _, value := range values {
		name, item, found := strings.Cut(value, "=")
		if !found || strings.TrimSpace(name) == "" {
			return nil, command.UsageError("--%s must use name=value", label)
		}
		parsed[name] = append(parsed[name], item)
	}
	return parsed, nil
}
