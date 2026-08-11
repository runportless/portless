package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery"
)

var Version = "dev"

type CLI struct {
	Out   io.Writer
	Err   io.Writer
	paths bootstrap.Paths
}

type discoverResponse struct {
	Project     model.Project     `json:"project"`
	Environment model.Environment `json:"environment"`
	Warnings    []string          `json:"warnings"`
}

func New(out, errOut io.Writer, dataDirectory string) (*CLI, error) {
	paths, err := bootstrap.ResolvePaths(dataDirectory)
	if err != nil {
		return nil, err
	}
	return &CLI{Out: out, Err: errOut, paths: paths}, nil
}

func (c *CLI) setup(ctx context.Context) error {
	if _, err := bootstrap.EnsureDaemon(ctx, c.paths); err != nil {
		return err
	}
	uid, gid := requestingUserIDs()
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if status.Installed && status.OwnerUID <= 0 {
		return errors.New("the existing clean-URL relay owner could not be determined; inspect `portless setup status`, then remove it with `portless setup uninstall --force`")
	}
	if status.Installed && status.OwnerUID != uid {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d; remove it with `portless setup uninstall --force` before installing it for this user", status.OwnerUID)
	}
	if status.Healthy && status.TargetSocket == c.paths.Ingress && status.ReceiptPresent {
		fmt.Fprintln(c.Out, "Clean localhost URLs are already configured.")
		fmt.Fprintln(c.Out, ingress.ControlOrigin)
		return nil
	}
	executable, err := resolvedExecutable()
	if err != nil {
		return err
	}
	if status.Installed {
		fmt.Fprintln(c.Out, "Repairing the Portless localhost port-80 relay requires administrator approval.")
	} else {
		fmt.Fprintln(c.Out, "Portless needs administrator approval once to install its localhost port-80 relay.")
	}
	if err := ingress.Install(ctx, ingress.SetupRequest{
		Executable: executable, TargetSocket: c.paths.Ingress,
		UID: uid, GID: gid, Stdin: os.Stdin, Stdout: c.Out, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := ingress.WaitUntilReady(ctx, 8*time.Second); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "Clean localhost URLs are ready.")
	fmt.Fprintln(c.Out, ingress.ControlOrigin)
	return nil
}

func (c *CLI) setupStatus(ctx context.Context, jsonOutput bool) error {
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(c.Out, status)
	}
	fmt.Fprintln(c.Out, "Clean URL relay:", status.State())
	fmt.Fprintln(c.Out, "Platform:", status.Platform)
	if !status.Installed {
		fmt.Fprintln(c.Out, "Run `portless setup` to install it.")
		return nil
	}
	fmt.Fprintln(c.Out, "Service:", status.Service)
	if status.OwnerUID > 0 {
		fmt.Fprintf(c.Out, "Owner: UID %d, GID %d\n", status.OwnerUID, status.OwnerGID)
	} else {
		fmt.Fprintln(c.Out, "Owner: unknown")
	}
	if status.TargetSocket != "" {
		fmt.Fprintln(c.Out, "Target socket:", status.TargetSocket)
	}
	fmt.Fprintln(c.Out, "Helper:", status.HelperPath)
	fmt.Fprintln(c.Out, "Configuration:", status.ConfigurationPath)
	fmt.Fprintln(c.Out, "Receipt:", status.ReceiptPath)
	if status.HealthError != "" {
		fmt.Fprintln(c.Out, "End-to-end check:", status.HealthError)
	}
	if status.Problem != "" {
		fmt.Fprintln(c.Out, "Problem:", status.Problem)
	}
	return nil
}

func (c *CLI) uninstallSetup(ctx context.Context, force bool) error {
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	uid, _ := requestingUserIDs()
	if err := ingress.ValidateUninstallOwnership(status, uid, force); err != nil {
		return err
	}
	executable, err := resolvedExecutable()
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "Removing the Portless clean-URL relay:")
	fmt.Fprintln(c.Out, "  service:", status.Service)
	fmt.Fprintln(c.Out, "  helper: ", status.HelperPath)
	fmt.Fprintln(c.Out, "Projects, containers, volumes, recordings, and Portless user data will not be removed.")
	fmt.Fprintln(c.Out, "Administrator approval is required to remove the system service.")
	removed, err := ingress.Uninstall(ctx, ingress.UninstallRequest{
		Executable: executable, UID: uid, Force: force, Stdin: os.Stdin, Stdout: c.Out, Stderr: c.Err,
	})
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	fmt.Fprintln(c.Out, "Clean-URL relay removed. Portless no longer owns 127.0.0.1:80.")
	fmt.Fprintln(c.Out, "Running environments were not stopped, but their clean localhost URLs are unavailable until `portless setup` is run again.")
	return nil
}

func resolvedExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(executable)
}

func requestingUserIDs() (int, int) {
	uid, gid := os.Getuid(), os.Getgid()
	if os.Geteuid() != 0 {
		return uid, gid
	}
	if sudoUID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && sudoUID > 0 {
		uid = sudoUID
	}
	if sudoGID, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil && sudoGID > 0 {
		gid = sudoGID
	}
	return uid, gid
}

func (c *CLI) up(ctx context.Context, selector string, options upOptions) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	if err := requireIngress(ctx); err != nil {
		return err
	}
	var environment model.Environment
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
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				err = cwdErr
				break
			}
			var discovered discoverResponse
			if discoverErr := client.Do(ctx, http.MethodPost, "/api/v1/projects/discover", map[string]any{"path": cwd, "name": options.name}, &discovered); discoverErr != nil {
				err = discoverErr
				break
			}
			environment = discovered.Environment
			for _, warning := range discovered.Warnings {
				fmt.Fprintln(c.Err, "warning:", warning)
			}
		}
	}
	if err != nil {
		return err
	}
	operationContext, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()
	var operation model.Operation
	idempotency := fmt.Sprintf("cli-up-%s-%s-%d", environment.Project, environment.Name, time.Now().UTC().Unix()/30)
	if err := client.DoWithHeaders(operationContext, http.MethodPost, environmentAPI(environment)+"/up", nil, &operation, map[string]string{"Idempotency-Key": idempotency}); err != nil {
		return err
	}
	if !options.wait {
		c.printOperation(operation, options.jsonOutput)
		return nil
	}
	operation, err = c.waitOperation(operationContext, client, operation, options.jsonOutput)
	if err != nil {
		return err
	}
	if operation.State != "succeeded" {
		return errors.New(operation.Error)
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment), nil, &environment); err != nil {
		return err
	}
	c.printStatus(environment)
	if options.open {
		next := "/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
		if browserURL, browserErr := c.browserURL(ctx, client, next); browserErr == nil {
			_ = launchBrowser(browserURL)
		}
	}
	return nil
}

func (c *CLI) down(ctx context.Context, selector string, options downOptions) error {
	client, environment, err := c.currentOrNamed(ctx, selector)
	if err != nil {
		return err
	}
	var operation model.Operation
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/down", map[string]any{"removeVolumes": options.volumes}, &operation); err != nil {
		return err
	}
	if options.wait {
		operation, err = c.waitOperation(ctx, client, operation, false)
		if err != nil {
			return err
		}
		if operation.State != "succeeded" {
			return errors.New(operation.Error)
		}
	}
	fmt.Fprintf(c.Out, "%s/%s  stopped\n", environment.Project, environment.Name)
	return nil
}

func (c *CLI) status(ctx context.Context, selector string, jsonOutput bool) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
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
	var response struct {
		Environments []model.Environment `json:"environments"`
	}
	if requestErr := client.Do(ctx, http.MethodGet, "/api/v1/environments", nil, &response); requestErr != nil {
		return requestErr
	}
	if jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Environments) == 0 {
		fmt.Fprintln(c.Out, "No environments yet. Run `portless up` in a supported repository or create a multi-source project.")
		return nil
	}
	for _, item := range response.Environments {
		fmt.Fprintf(c.Out, "%-32s %-14s %d services\n", model.EnvironmentSelector(item.Project, item.Name), item.Status, len(item.Services))
	}
	return nil
}

func (c *CLI) ui(ctx context.Context) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	if err := requireIngress(ctx); err != nil {
		return err
	}
	next := "/projects"
	if environment, findErr := c.findCurrent(ctx, client); findErr == nil {
		next = "/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
	}
	browserURL, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Out, browserURL)
	if err := launchBrowser(browserURL); err != nil {
		fmt.Fprintln(c.Err, "Could not open a browser; use the URL above:", err)
	}
	return nil
}

func (c *CLI) open(ctx context.Context, requestedService string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := requireIngress(ctx); err != nil {
		return err
	}
	serviceName := environment.PrimaryService
	if requestedService != "" {
		serviceName = requestedService
	}
	if serviceName != "" {
		for _, service := range environment.Services {
			if strings.EqualFold(service.Name, serviceName) {
				if service.IngressURL != "" {
					fmt.Fprintln(c.Out, service.IngressURL)
					return launchBrowser(service.IngressURL)
				}
				return fmt.Errorf("service %s does not expose an HTTP endpoint", serviceName)
			}
		}
		return fmt.Errorf("service %s was not found in %s/%s", serviceName, environment.Project, environment.Name)
	}
	next := "/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
	browserURL, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Out, browserURL)
	return launchBrowser(browserURL)
}

func (c *CLI) logs(ctx context.Context, service string, options streamOptions) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	seen := 0
	for {
		var response struct {
			Lines []string `json:"lines"`
		}
		path := environmentAPI(environment) + "/logs?service=" + url.QueryEscape(service) + "&limit=2000"
		if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if seen > len(response.Lines) {
			seen = 0
		}
		for _, line := range response.Lines[seen:] {
			if options.jsonOutput {
				_ = writeJSON(c.Out, map[string]any{"project": environment.Project, "environment": environment.Name, "service": service, "line": line})
			} else {
				fmt.Fprintln(c.Out, line)
			}
		}
		seen = len(response.Lines)
		if !options.follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func (c *CLI) traffic(ctx context.Context, selector string, options streamOptions) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Traffic []model.TrafficEvent `json:"traffic"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/traffic/http?limit=250", nil, &response); err != nil {
		return err
	}
	for index := len(response.Traffic) - 1; index >= 0; index-- {
		if matchesTraffic(response.Traffic[index], selector) {
			c.printTraffic(response.Traffic[index], options.jsonOutput)
		}
	}
	if !options.follow {
		return nil
	}
	return c.followTraffic(ctx, client, environment, selector, options.jsonOutput)
}

func (c *CLI) listRecordings(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Recordings []model.Recording `json:"recordings"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/recordings", nil, &response); err != nil {
		return err
	}
	for _, item := range response.Recordings {
		fmt.Fprintf(c.Out, "%-24s %-10s %d events  %s → %s\n", item.Name, item.Status, item.EventCount, emptyAs(item.Source, "any"), emptyAs(item.Target, "any"))
	}
	return nil
}

func (c *CLI) startRecording(ctx context.Context, name string, options recordingOptions) error {
	source, target, err := parseEdge(options.edge)
	if err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(options.duration)
	input := model.Recording{Name: name, Source: source, Target: target, MaxEvents: options.maxEvents, ExpiresAt: &expires}
	var created model.Recording
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/recordings", input, &created); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "recording %s started; expires %s\n", created.Name, created.ExpiresAt.Format(time.RFC3339))
	return nil
}

func (c *CLI) stopRecording(ctx context.Context, requestedName string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := environmentAPI(environment) + "/recordings"
	arguments := []string(nil)
	if requestedName != "" {
		arguments = []string{requestedName}
	}
	name, err := recordingName(ctx, client, base, arguments)
	if err != nil {
		return err
	}
	if err := client.Do(ctx, http.MethodPost, base+"/"+bootstrap.EscapePath(name)+"/stop", nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "recording", name, "stopped")
	return nil
}

func (c *CLI) exportRecording(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var content []byte
	path := environmentAPI(environment) + "/recordings/" + bootstrap.EscapePath(name) + "/export"
	if err := client.Do(ctx, http.MethodGet, path, nil, &content); err != nil {
		return err
	}
	_, err = c.Out.Write(content)
	return err
}

func (c *CLI) deleteRecording(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	path := environmentAPI(environment) + "/recordings/" + bootstrap.EscapePath(name)
	return client.Do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *CLI) listFaults(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Faults []model.FaultRule `json:"faults"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/faults", nil, &response); err != nil {
		return err
	}
	for _, fault := range response.Faults {
		state := "disabled"
		if fault.Enabled {
			state = "active"
		}
		fmt.Fprintf(c.Out, "%-24s %-9s %s\n", fault.Name, state, fault.ScopeSummary)
	}
	return nil
}

func (c *CLI) addFault(ctx context.Context, name, edge string, options faultOptions) error {
	source, target, err := parseEdge(edge)
	if err != nil || source == "" || target == "" {
		return errors.New("edge must be source:target")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(options.duration)
	input := model.FaultRule{Name: name, Source: source, Target: target, LatencyMS: options.latency, JitterMS: options.jitter, StatusCode: options.status, Abort: options.abort, Probability: options.probability, Method: options.method, Path: options.path, ExpiresAt: &expires}
	var created model.FaultRule
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/faults", input, &created); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "fault %s active: %s\n", created.Name, created.ScopeSummary)
	return nil
}

func (c *CLI) disableFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	path := environmentAPI(environment) + "/faults/" + bootstrap.EscapePath(name)
	if err := client.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "fault", name, "disabled")
	return nil
}

func (c *CLI) clearFaults(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var result map[string]any
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/faults/disable-all", nil, &result); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "all active faults disabled")
	return nil
}

func (c *CLI) createProject(ctx context.Context, name string, sourceValues []string) error {
	var sources []application.SourceInput
	for _, value := range sourceValues {
		sourceName, sourcePath, found := strings.Cut(value, "=")
		if !found || sourceName == "" || sourcePath == "" {
			return usageError("each --source must use name=path")
		}
		sourcePath, err := absoluteSourcePath(sourcePath)
		if err != nil {
			return fmt.Errorf("source %s: %w", sourceName, err)
		}
		sources = append(sources, application.SourceInput{Name: sourceName, Path: sourcePath})
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	var response discoverResponse
	if err := client.Do(ctx, http.MethodPost, "/api/v1/projects", map[string]any{"name": name, "sources": sources}, &response); err != nil {
		return err
	}
	for _, warning := range response.Warnings {
		fmt.Fprintln(c.Err, "warning:", warning)
	}
	fmt.Fprintf(c.Out, "created %s with environment %s and %d sources\n", response.Project.Name, response.Environment.Name, len(sources))
	return nil
}

func (c *CLI) exportProject(ctx context.Context, output string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + bootstrap.EscapePath(environment.Project)
	var content []byte
	if err := client.Do(ctx, http.MethodGet, base+"/declaration", nil, &content); err != nil {
		return err
	}
	if output == "-" {
		_, err = c.Out.Write(content)
		return err
	}
	if err := os.WriteFile(output, content, 0o600); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "wrote", output)
	return nil
}

func (c *CLI) renameProject(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + bootstrap.EscapePath(environment.Project)
	var project model.Project
	if err := client.Do(ctx, http.MethodGet, base, nil, &project); err != nil {
		return err
	}
	var renamed model.Project
	if err := client.Do(ctx, http.MethodPatch, base, map[string]any{"name": name, "revision": project.Revision}, &renamed); err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "%s renamed to %s\n", project.Name, renamed.Name)
	return nil
}

func (c *CLI) forgetProject(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + bootstrap.EscapePath(environment.Project)
	if err := client.Do(ctx, http.MethodDelete, base, nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "forgot", environment.Project)
	return nil
}

func (c *CLI) listEnvironments(ctx context.Context, project string) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	path := "/api/v1/environments"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	var response struct {
		Environments []model.Environment `json:"environments"`
	}
	if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return err
	}
	for _, item := range response.Environments {
		fmt.Fprintf(c.Out, "%-32s %-14s %d services\n", model.EnvironmentSelector(item.Project, item.Name), item.Status, len(item.Services))
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
	var created model.Environment
	if err := client.Do(ctx, http.MethodPost, "/api/v1/environments", map[string]any{"project": current.Project, "name": name, "from": from}, &created); err != nil {
		return err
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
	var updated model.Environment
	path := environmentAPI(environment) + "/bindings/" + bootstrap.EscapePath(service)
	if err := client.Do(ctx, http.MethodPut, path, binding, &updated); err != nil {
		return err
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
	var response struct {
		Environment model.Environment `json:"environment"`
		Warnings    []string          `json:"warnings"`
	}
	path := environmentAPI(environment) + "/sources/" + bootstrap.EscapePath(source)
	if err := client.Do(ctx, http.MethodPut, path, map[string]string{"path": sourcePath}, &response); err != nil {
		return err
	}
	for _, warning := range response.Warnings {
		fmt.Fprintln(c.Err, "warning:", warning)
	}
	fmt.Fprintf(c.Out, "%s now uses %s for source %s\n", model.EnvironmentSelector(environment.Project, environment.Name), sourcePath, source)
	return nil
}

func (c *CLI) rescanEnvironment(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Environment model.Environment `json:"environment"`
		Warnings    []string          `json:"warnings"`
	}
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/rescan", nil, &response); err != nil {
		return err
	}
	for _, warning := range response.Warnings {
		fmt.Fprintln(c.Err, "warning:", warning)
	}
	fmt.Fprintf(c.Out, "%s rescanned (revision %d)\n", model.EnvironmentSelector(environment.Project, environment.Name), response.Environment.Revision)
	return nil
}

func (c *CLI) forgetEnvironment(ctx context.Context) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if err := client.Do(ctx, http.MethodDelete, environmentAPI(environment), nil, nil); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "forgot", model.EnvironmentSelector(environment.Project, environment.Name))
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

func (c *CLI) useEnvironment(ctx context.Context, selector string) error {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	if _, err := c.loadEnvironment(ctx, client, selector); err != nil {
		return err
	}
	root, err := currentSourceRoot()
	if err != nil {
		return err
	}
	if err := client.Do(ctx, http.MethodPut, "/api/v1/environments/select", map[string]any{"path": root, "project": project, "environment": environment}, nil); err != nil {
		return err
	}
	fmt.Fprintln(c.Out, "using", selector, "in", root)
	return nil
}

func (c *CLI) runtimeStatus(ctx context.Context) error {
	return c.runtimeRequest(ctx, http.MethodGet, "/api/v1/runtime", nil)
}

func (c *CLI) startRuntime(ctx context.Context) error {
	return c.runtimeRequest(ctx, http.MethodPost, "/api/v1/runtime/start", nil)
}

func (c *CLI) useRuntime(ctx context.Context, preference string) error {
	return c.runtimeRequest(ctx, http.MethodPut, "/api/v1/runtime", map[string]string{"preference": preference})
}

func (c *CLI) runtimeRequest(ctx context.Context, method, path string, body any) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	var status map[string]any
	if err := client.Do(ctx, method, path, body, &status); err != nil {
		return err
	}
	return writeJSON(c.Out, status)
}

func (c *CLI) current(ctx context.Context) (*bootstrap.Client, model.Environment, error) {
	return c.currentOrNamed(ctx, "")
}

func (c *CLI) currentOrNamed(ctx context.Context, selector string) (*bootstrap.Client, model.Environment, error) {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return nil, model.Environment{}, err
	}
	var environment model.Environment
	if selector != "" {
		environment, err = c.loadEnvironment(ctx, client, selector)
	} else {
		environment, err = c.findCurrent(ctx, client)
	}
	if err != nil {
		return nil, model.Environment{}, err
	}
	return client, environment, nil
}

func (c *CLI) findCurrent(ctx context.Context, client *bootstrap.Client) (model.Environment, error) {
	environments, err := c.environmentsForCurrentPath(ctx, client)
	if err != nil {
		return model.Environment{}, err
	}
	switch len(environments) {
	case 0:
		return model.Environment{}, errors.New("this checkout is not part of a Portless environment; run `portless up` or `portless project create`")
	case 1:
		resolved := environments[0]
		return c.loadEnvironment(ctx, client, model.EnvironmentSelector(resolved.Project, resolved.Name))
	default:
		return model.Environment{}, ambiguousEnvironmentError(environments)
	}
}

func (c *CLI) environmentsForCurrentPath(ctx context.Context, client *bootstrap.Client) ([]model.Environment, error) {
	root, err := currentSourceRoot()
	if err != nil {
		return nil, err
	}
	var response struct {
		Environments []model.Environment `json:"environments"`
	}
	path := "/api/v1/environments/resolve?path=" + url.QueryEscape(root)
	if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return response.Environments, nil
}

func currentSourceRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := discovery.FindRoot(cwd)
	if err != nil {
		root = cwd
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	return root, nil
}

func (c *CLI) loadEnvironment(ctx context.Context, client *bootstrap.Client, selector string) (model.Environment, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	var result model.Environment
	if err := client.Do(ctx, http.MethodGet, "/api/v1/environments/"+bootstrap.EscapePath(project, environment), nil, &result); err != nil {
		return model.Environment{}, err
	}
	return result, nil
}

func ambiguousEnvironmentError(environments []model.Environment) error {
	selectors := make([]string, 0, len(environments))
	for _, environment := range environments {
		selectors = append(selectors, model.EnvironmentSelector(environment.Project, environment.Name))
	}
	return fmt.Errorf("this checkout belongs to multiple environments (%s); select one with `portless use project/environment`", strings.Join(selectors, ", "))
}

func environmentAPI(environment model.Environment) string {
	return "/api/v1/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
}

func (c *CLI) browserURL(ctx context.Context, client *bootstrap.Client, next string) (string, error) {
	var result struct {
		URL string `json:"url"`
	}
	if err := client.Do(ctx, http.MethodPost, "/api/v1/browser-claims", map[string]string{"next": next}, &result); err != nil {
		return "", err
	}
	return result.URL, nil
}

func requireIngress(ctx context.Context) error {
	if err := ingress.Check(ctx); err != nil {
		return fmt.Errorf("clean localhost URLs are not configured; run `portless setup` once, then retry: %w", err)
	}
	return nil
}

func (c *CLI) waitOperation(ctx context.Context, client *bootstrap.Client, operation model.Operation, jsonOutput bool) (model.Operation, error) {
	seen := 0
	for {
		path := "/api/v1/environments/" + bootstrap.EscapePath(operation.Project, operation.Environment) + "/operations/" + strconv.FormatInt(operation.Number, 10)
		if err := client.Do(ctx, http.MethodGet, path, nil, &operation); err != nil {
			return model.Operation{}, err
		}
		for _, event := range operation.Events[seen:] {
			if jsonOutput {
				_ = writeJSON(c.Out, event)
			} else {
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

func (c *CLI) followTraffic(ctx context.Context, client *bootstrap.Client, environment model.Environment, selector string, jsonOutput bool) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+environmentAPI(environment)+"/stream?topic=traffic.http", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Accept", "text/event-stream")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("traffic stream returned %s", response.Status)
	}
	scanner := bufio.NewScanner(response.Body)
	eventType := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") && eventType == "traffic.http" {
			var event model.TrafficEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && matchesTraffic(event, selector) {
				c.printTraffic(event, jsonOutput)
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return scanner.Err()
}

func (c *CLI) printStatus(environment model.Environment) {
	ready := 0
	for _, service := range environment.Services {
		if service.Status == model.ServiceReady {
			ready++
		}
	}
	fmt.Fprintf(c.Out, "%s/%s  %s  %d/%d ready\n\n", environment.Project, environment.Name, environment.Status, ready, len(environment.Services))
	fmt.Fprintln(c.Out, "SERVICE                 PROVIDER    KIND        STATE          ENDPOINT")
	for _, service := range environment.Services {
		kind := string(service.Kind)
		if service.Framework != "" {
			kind = service.Framework
		} else if service.Template != "" {
			kind = service.Template
		}
		provider := "local"
		for _, binding := range environment.Bindings {
			if strings.EqualFold(binding.Service, service.Name) {
				provider = string(binding.Provider)
				break
			}
		}
		fmt.Fprintf(c.Out, "%-23s %-11s %-11s %-14s %s\n", service.Name, provider, kind, service.Status, statusEndpoint(service))
	}
	fmt.Fprintln(c.Out, "\nDashboard:", environment.DashboardURL)
}

func statusEndpoint(service model.Service) string {
	if service.IngressURL != "" {
		return service.IngressURL
	}
	if service.UpstreamPort > 0 {
		return fmt.Sprintf("127.0.0.1:%d", service.UpstreamPort)
	}
	return ""
}

func (c *CLI) printOperation(operation model.Operation, jsonOutput bool) {
	if jsonOutput {
		_ = writeJSON(c.Out, operation)
		return
	}
	fmt.Fprintf(c.Out, "%s operation %d %s\n", operation.Type, operation.Number, operation.State)
}

func (c *CLI) printTraffic(event model.TrafficEvent, jsonOutput bool) {
	if jsonOutput {
		_ = writeJSON(c.Out, event)
		return
	}
	fault := ""
	if event.Fault != "" {
		fault = " fault=" + event.Fault
	}
	fmt.Fprintf(c.Out, "#%-5d %-7s %-18s %4d %5dms %s:%s%s\n", event.Sequence, event.Method, event.Path, event.Status, event.DurationMS, event.Source, event.Target, fault)
}

func (c *CLI) printError(err error) {
	var clientErr *bootstrap.ClientError
	if errors.As(err, &clientErr) {
		fmt.Fprintf(c.Err, "portless: %s\n", clientErr.Message)
		if clientErr.Code != "" {
			fmt.Fprintf(c.Err, "code: %s\n", clientErr.Code)
		}
		for _, remediation := range clientErr.Remediation {
			if command, ok := remediation["command"].(string); ok && command != "" {
				fmt.Fprintln(c.Err, "next:", command)
			}
			if targetURL, ok := remediation["url"].(string); ok && targetURL != "" {
				fmt.Fprintln(c.Err, "inspect:", targetURL)
			}
		}
		return
	}
	fmt.Fprintln(c.Err, "portless:", err)
}

func launchBrowser(targetURL string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", targetURL)
	case "linux":
		command = exec.Command("xdg-open", targetURL)
	default:
		return errors.New("automatic browser opening is unsupported on this platform")
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func parseEdge(value string) (string, string, error) {
	if value == "" {
		return "", "", nil
	}
	source, target, found := strings.Cut(value, ":")
	if !found || source == "" || target == "" || strings.Contains(target, ":") {
		return "", "", errors.New("edge must use source:target")
	}
	return source, target, nil
}

func matchesTraffic(event model.TrafficEvent, selector string) bool {
	if selector == "" {
		return true
	}
	if source, target, found := strings.Cut(selector, ":"); found {
		return event.Source == source && event.Target == target
	}
	return event.Source == selector || event.Target == selector
}

func recordingName(ctx context.Context, client *bootstrap.Client, base string, arguments []string) (string, error) {
	if len(arguments) == 1 {
		return arguments[0], nil
	}
	if len(arguments) > 1 {
		return "", errors.New("usage: portless record stop [name]")
	}
	var response struct {
		Recordings []model.Recording `json:"recordings"`
	}
	if err := client.Do(ctx, http.MethodGet, base, nil, &response); err != nil {
		return "", err
	}
	for _, item := range response.Recordings {
		if item.Status == "active" {
			return item.Name, nil
		}
	}
	return "", errors.New("no active recording")
}

func firstArg(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func emptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
