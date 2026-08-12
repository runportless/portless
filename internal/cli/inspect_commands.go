package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/bootstrap"
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
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	var response struct {
		Projects []model.Project `json:"projects"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects?limit="+strconv.Itoa(limit), nil, &response); err != nil {
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
	client, _, err := bootstrap.Connect(ctx, c.paths)
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
	var project model.Project
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects/"+bootstrap.EscapePath(name), nil, &project); err != nil {
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

func serviceType(service model.ServiceDefinition) string {
	if service.Framework != "" {
		return service.Framework
	}
	if service.Template != "" {
		return service.Template
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
	var response struct {
		Services []model.Service `json:"services"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/services?limit="+strconv.Itoa(limit), nil, &response); err != nil {
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
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-22s %-11s %-12s %-13s %-11s %-9s %s", "SERVICE", "PROVIDER", "KIND", "STATE", "GENERATION", "RESTARTS", "ENDPOINT")))
	for _, service := range response.Services {
		fmt.Fprintf(c.Out, "%-22s %-11s %-12s %s %-11d %-9d %s\n", service.Name, providerFor(environment, service.Name), serviceType(service.ServiceDefinition), c.state(c.Out, fmt.Sprintf("%-13s", service.Status)), service.Generation, service.RestartCount, c.accent(c.Out, statusEndpoint(service)))
	}
	return nil
}

func (c *CLI) showService(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var service model.Service
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/services/"+bootstrap.EscapePath(name), nil, &service); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, service)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, service.Name))
	fmt.Fprintf(c.Out, "\n  %-15s %s\n", "Provider:", providerFor(environment, service.Name))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Kind:", serviceType(service.ServiceDefinition))
	fmt.Fprintf(c.Out, "  %-15s %s\n", "State:", c.state(c.Out, string(service.Status)))
	if service.Reason != "" {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Reason:", service.Reason)
	}
	fmt.Fprintf(c.Out, "  %-15s %d\n", "Generation:", service.Generation)
	fmt.Fprintf(c.Out, "  %-15s %d\n", "Restarts:", service.RestartCount)
	fmt.Fprintf(c.Out, "  %-15s %s\n", "Endpoint:", emptyAs(statusEndpoint(service), "none"))
	if service.PID != 0 {
		fmt.Fprintf(c.Out, "  %-15s %d\n", "PID:", service.PID)
	}
	if service.StartedAt != nil {
		fmt.Fprintf(c.Out, "  %-15s %s\n", "Started:", service.StartedAt.Local().Format(time.RFC3339))
	}
	return nil
}

func (c *CLI) showServiceConfiguration(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var configuration model.ServiceConfiguration
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/services/"+bootstrap.EscapePath(name)+"/configuration", nil, &configuration); err != nil {
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
	var operation model.Operation
	path := environmentAPI(environment) + "/services/" + bootstrap.EscapePath(name) + "/" + action
	if err := client.Do(ctx, http.MethodPost, path, nil, &operation); err != nil {
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
	past := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[action]
	fmt.Fprintf(c.Out, "%s %s\n", name, c.state(c.Out, past))
	return nil
}

func (c *CLI) listConnections(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Connections []model.EffectiveConnection `json:"connections"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/connections?limit="+strconv.Itoa(limit), nil, &response); err != nil {
		return err
	}
	if response.Connections == nil {
		response.Connections = []model.EffectiveConnection{}
	}
	response.Connections = truncate(response.Connections, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Connections) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No connections."))
		return nil
	}
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-30s %-10s %-11s %-12s %-22s %s", "CONNECTION", "PROTOCOL", "PROVIDER", "STATE", "PROXY", "INJECTED AS")))
	for _, connection := range response.Connections {
		fmt.Fprintf(c.Out, "%-30s %-10s %-11s %s %-22s %s\n", connection.Source+":"+connection.Target, connection.Protocol, connection.TargetProvider, c.state(c.Out, fmt.Sprintf("%-12s", connection.TargetStatus)), emptyAs(connection.ProxyAddress, "inactive"), emptyAs(connection.InjectedEnvVar, "—"))
	}
	return nil
}

func (c *CLI) showConnection(ctx context.Context, edge string) error {
	source, target, err := parseEdge(edge)
	if err != nil || source == "" || target == "" {
		return usageError("connection must use source:target")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var connection model.EffectiveConnection
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/connections/"+bootstrap.EscapePath(source, target), nil, &connection); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, connection)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Connection: "+source+":"+target))
	fmt.Fprintf(c.Out, "\n  %-18s %s\n", "Protocol:", connection.Protocol)
	fmt.Fprintf(c.Out, "  %-18s %t\n", "Required:", connection.Required)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Target provider:", connection.TargetProvider)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Target state:", c.state(c.Out, string(connection.TargetStatus)))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Proxy address:", emptyAs(connection.ProxyAddress, "inactive"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Injected as:", emptyAs(connection.InjectedEnvVar, "none"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Injected value:", emptyAs(connection.InjectedValue, "not active"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Effective target:", emptyAs(connection.TargetEndpoint, "not active"))
	return nil
}

func relative(value time.Time) string {
	duration := time.Since(value)
	if duration < 0 {
		return value.Local().Format(time.RFC3339)
	}
	if duration < time.Minute {
		return fmt.Sprintf("%ds ago", int(duration.Seconds()))
	}
	if duration < time.Hour {
		return fmt.Sprintf("%dm ago", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(duration.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(duration.Hours()/24))
}

func (c *CLI) timeline(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Timeline []model.TimelineEvent `json:"timeline"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/timeline?limit="+strconv.Itoa(limit), nil, &response); err != nil {
		return err
	}
	if response.Timeline == nil {
		response.Timeline = []model.TimelineEvent{}
	}
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Timeline) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No timeline events."))
		return nil
	}
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-18s %-24s %-20s %-10s %s", "WHEN", "TYPE", "SUBJECT", "SEVERITY", "SUMMARY")))
	for _, event := range response.Timeline {
		fmt.Fprintf(c.Out, "%-18s %-24s %-20s %-10s %s\n", relative(event.Timestamp), event.Type, emptyAs(event.Subject, "—"), event.Severity, event.Summary)
	}
	return nil
}

func (c *CLI) showRecording(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var recording model.Recording
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/recordings/"+bootstrap.EscapePath(name), nil, &recording); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, recording)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Recording: "+recording.Name))
	fmt.Fprintf(c.Out, "\n  %-14s %s\n", "State:", c.state(c.Out, recording.Status))
	fmt.Fprintf(c.Out, "  %-14s %s → %s\n", "Scope:", emptyAs(recording.Source, "any"), emptyAs(recording.Target, "any"))
	fmt.Fprintf(c.Out, "  %-14s %d / %d\n", "Events:", recording.EventCount, recording.MaxEvents)
	fmt.Fprintf(c.Out, "  %-14s %s\n", "Started:", recording.StartedAt.Local().Format(time.RFC3339))
	if recording.CompletedAt != nil {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Completed:", recording.CompletedAt.Local().Format(time.RFC3339))
	}
	if recording.ExpiresAt != nil {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Expires:", recording.ExpiresAt.Local().Format(time.RFC3339))
	}
	return nil
}

func (c *CLI) showFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var fault model.FaultRule
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/faults/"+bootstrap.EscapePath(name), nil, &fault); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, fault)
	}
	state := "disabled"
	if fault.Enabled {
		state = "active"
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Fault: "+fault.Name))
	fmt.Fprintf(c.Out, "\n  %-14s %s\n", "State:", c.state(c.Out, state))
	fmt.Fprintf(c.Out, "  %-14s %s → %s\n", "Scope:", fault.Source, fault.Target)
	fmt.Fprintf(c.Out, "  %-14s %.2f\n", "Probability:", fault.Probability)
	if fault.LatencyMS > 0 {
		fmt.Fprintf(c.Out, "  %-14s %dms (+%dms jitter)\n", "Latency:", fault.LatencyMS, fault.JitterMS)
	}
	if fault.StatusCode > 0 {
		fmt.Fprintf(c.Out, "  %-14s %d\n", "HTTP status:", fault.StatusCode)
	}
	if fault.Abort {
		fmt.Fprintf(c.Out, "  %-14s yes\n", "Abort:")
	}
	if fault.Method != "" || fault.Path != "" {
		fmt.Fprintf(c.Out, "  %-14s %s %s\n", "HTTP filter:", emptyAs(fault.Method, "any"), emptyAs(fault.Path, "any"))
	}
	fmt.Fprintf(c.Out, "  %-14s %d\n", "Matches:", fault.MatchCount)
	fmt.Fprintf(c.Out, "  %-14s %s\n", "Created:", fault.CreatedAt.Local().Format(time.RFC3339))
	if fault.ExpiresAt != nil {
		fmt.Fprintf(c.Out, "  %-14s %s\n", "Expires:", fault.ExpiresAt.Local().Format(time.RFC3339))
	}
	return nil
}

func (c *CLI) printURL(ctx context.Context, requested string) error {
	_, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	name := requested
	if name == "" {
		name = environment.PrimaryService
	}
	for _, service := range environment.Services {
		if strings.EqualFold(service.Name, name) {
			if service.IngressURL == "" {
				return fmt.Errorf("service %s does not expose an HTTP endpoint", service.Name)
			}
			if c.jsonOutput {
				return writeJSON(c.Out, map[string]string{"service": service.Name, "url": service.IngressURL})
			}
			fmt.Fprintln(c.Out, service.IngressURL)
			return nil
		}
	}
	if name == "" {
		return errors.New("the environment has no primary HTTP service")
	}
	return fmt.Errorf("service %s was not found in %s/%s", name, environment.Project, environment.Name)
}

func matchesTrafficOptions(event model.TrafficEvent, options trafficOptions) bool {
	if options.protocol == "http" && event.Protocol != model.ProtocolHTTP {
		return false
	}
	if options.protocol == "tcp" && event.Protocol == model.ProtocolHTTP {
		return false
	}
	if options.service != "" && event.Source != options.service && event.Target != options.service {
		return false
	}
	if options.edge != "" {
		source, target, _ := parseEdge(options.edge)
		return event.Source == source && event.Target == target
	}
	return true
}

func printHeaderMap(writer io.Writer, title string, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fmt.Fprintln(writer, "\n"+title+":")
	for _, key := range keys {
		fmt.Fprintf(writer, "  %-24s %s\n", key+":", headers[key])
	}
}

func writePrivateFile(path string, content []byte, force bool) error {
	if path == "" || path == "-" {
		return errors.New("an output path is required")
	}
	if _, err := os.Lstat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; use --force to overwrite it", path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".portless-export-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
