package mocks

import (
	"context"
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
	result, err := client.ListMocks(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if result.Mocks == nil {
		result.Mocks = []model.MockProfile{}
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	if len(result.Mocks) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No mock profiles."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.Heading(c.Out, "Mocks"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-24s %-20s %-8s %s", "PROFILE", "SERVICE", "ROUTES", "MODIFIED")))
	for _, profile := range result.Mocks {
		fmt.Fprintf(c.Out, "%-24s %-20s %-8d %s\n", profile.Name, profile.Service, len(profile.Routes), profile.ModifiedAt.Local().Format(time.RFC3339))
	}
	return nil
}

func (c *Commands) show(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	profile, err := client.Mock(ctx, environment.Project, environment.Name, name)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, profile)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, profile.Name))
	fmt.Fprintf(c.Out, "  %-13s %s\n", "Service:", profile.Service)
	if profile.Description != "" {
		fmt.Fprintf(c.Out, "  %-13s %s\n", "Description:", profile.Description)
	}
	fmt.Fprintf(c.Out, "  %-13s %d\n\n", "Routes:", len(profile.Routes))
	if len(profile.Routes) == 0 {
		fmt.Fprintln(c.Out, c.Muted(c.Out, "No routes."))
		return nil
	}
	fmt.Fprintln(c.Out, c.Muted(c.Out, fmt.Sprintf("%-22s %-8s %-32s %-7s %s", "ROUTE", "METHOD", "PATH", "STATUS", "STATE")))
	for _, route := range profile.Routes {
		state := "enabled"
		if !route.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(c.Out, "%-22s %-8s %-32s %-7d %s\n", route.Name, route.Method, route.Path, route.Status, c.State(c.Out, state))
	}
	return nil
}

func (c *Commands) create(ctx context.Context, name, service, description, fromRecording, fromOpenAPI string) error {
	var openAPIDocument string
	if fromOpenAPI != "" {
		content, err := os.ReadFile(fromOpenAPI)
		if err != nil {
			return err
		}
		if len(content) > 1<<20 {
			return command.UsageError("OpenAPI document must not exceed 1048576 bytes")
		}
		openAPIDocument = string(content)
	}
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	result, err := client.CreateMock(ctx, environment.Project, environment.Name, contract.CreateMockRequest{Name: name, Service: service, Description: description, FromRecording: fromRecording, OpenAPIDocument: openAPIDocument})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	c.PrintWarnings(result.Warnings)
	fmt.Fprintf(c.Out, "mock profile %s created for %s with %d routes\n", result.Mock.Name, result.Mock.Service, len(result.Mock.Routes))
	return nil
}

func (c *Commands) delete(ctx context.Context, name string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	if err := client.DeleteMock(ctx, environment.Project, environment.Name, name); err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "mock profile", name, "deleted")
	return nil
}

func (c *Commands) setRoute(ctx context.Context, profileName, routeName string, options routeOptions) error {
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
	route := model.MockRoute{Name: routeName, Method: options.method, Path: options.path, Query: query, Status: options.status, Headers: headers, Body: body, DelayMS: options.delay, Enabled: !options.disabled}
	updated, err := client.PutMockRoute(ctx, environment.Project, environment.Name, profileName, routeName, route)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, updated)
	}
	fmt.Fprintf(c.Out, "route %s updated in mock profile %s\n", routeName, updated.Name)
	return nil
}

func (c *Commands) deleteRoute(ctx context.Context, profileName, routeName string) error {
	client, environment, err := c.Current(ctx)
	if err != nil {
		return err
	}
	updated, err := client.DeleteMockRoute(ctx, environment.Project, environment.Name, profileName, routeName)
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, updated)
	}
	fmt.Fprintf(c.Out, "route %s deleted from mock profile %s\n", routeName, updated.Name)
	return nil
}

func (c *Commands) preview(ctx context.Context, profileName string, options previewOptions) error {
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
	preview, err := client.PreviewMock(ctx, environment.Project, environment.Name, profileName, model.MockRequest{Method: options.method, Path: options.path, Query: values, Headers: headers, Body: body})
	if err != nil {
		return err
	}
	if c.JSONOutput {
		return command.WriteJSON(c.Out, preview)
	}
	if !preview.Matched {
		fmt.Fprintf(c.Out, "no route matched; the mock would return %d\n", preview.Status)
		return nil
	}
	fmt.Fprintf(c.Out, "matched %s · %d", c.Accent(c.Out, preview.Route), preview.Status)
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
