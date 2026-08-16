package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/portless-run/portless/internal/runtime/container"
)

var Version = "dev"

type CLI struct {
	Out                 io.Writer
	Err                 io.Writer
	paths               bootstrap.Paths
	jsonOutput          bool
	noColor             bool
	completionOutput    bool
	colorPreference     colorPreference
	colorSource         string
	environmentOverride string
	completionCache     map[string][]string
}

type discoverResponse struct {
	Project     model.Project     `json:"project"`
	Environment model.Environment `json:"environment"`
	Warnings    []string          `json:"warnings"`
}

type projectSourceResponse struct {
	discoverResponse
	ConfigurationRequired []string `json:"configurationRequired"`
}

type upOutput struct {
	Environment model.Environment `json:"environment"`
	Operation   model.Operation   `json:"operation"`
	Warnings    []string          `json:"warnings"`
}

type browserOutput struct {
	URL     string `json:"url"`
	Service string `json:"service,omitempty"`
	Opened  bool   `json:"opened"`
	Error   string `json:"error,omitempty"`
}

type relayStatusOutput struct {
	State string `json:"state"`
	ingress.InstallationStatus
}

type relayActionOutput struct {
	Action string `json:"action"`
	relayStatusOutput
}

type actionOutput struct {
	Action      string `json:"action"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Name        string `json:"name,omitempty"`
	Path        string `json:"path,omitempty"`
	Status      string `json:"status,omitempty"`
}

type logsOutput struct {
	Project     string           `json:"project"`
	Environment string           `json:"environment"`
	Entries     []model.LogEntry `json:"entries"`
}

type environmentContextOutput struct {
	Path        string            `json:"path"`
	Selector    string            `json:"selector"`
	Resolution  string            `json:"resolution"`
	Environment model.Environment `json:"environment"`
}

type upRequest struct {
	DebugServices []string `json:"debugServices,omitempty"`
	Managed       bool     `json:"managed,omitempty"`
}

type errorOutput struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code        string           `json:"code"`
	Message     string           `json:"message"`
	Status      int              `json:"status,omitempty"`
	Subject     map[string]any   `json:"subject,omitempty"`
	Details     map[string]any   `json:"details,omitempty"`
	Remediation []map[string]any `json:"remediation,omitempty"`
}

func New(out, errOut io.Writer, dataDirectory string) (*CLI, error) {
	paths, err := bootstrap.ResolvePaths(dataDirectory)
	if err != nil {
		return nil, err
	}
	return &CLI{Out: out, Err: errOut, paths: paths}, nil
}

func (c *CLI) installRelay(ctx context.Context, jsonOutput bool) error {
	if _, err := bootstrap.EnsureDaemon(ctx, c.paths); err != nil {
		return err
	}
	uid, gid := requestingUserIDs()
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if status.Installed && status.OwnerUID <= 0 {
		return errors.New("the existing clean-URL relay owner could not be determined; inspect `portless relay status`, then remove it with `portless relay uninstall --force`")
	}
	if status.Installed && status.OwnerUID != uid {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d; remove it with `portless relay uninstall --force` before installing it for this user", status.OwnerUID)
	}
	if status.Healthy && status.TargetSocket == c.paths.Ingress && status.DNSTargetSocket == c.paths.DNS && status.ReceiptPresent && status.ResolverPresent {
		if jsonOutput {
			return writeRelayStatusJSON(c.Out, status)
		}
		fmt.Fprintln(c.Out, "Clean local endpoints are already configured.")
		fmt.Fprintln(c.Out, c.accent(c.Out, ingress.ControlOrigin))
		fmt.Fprintln(c.Out, c.accent(c.Out, "*.portless.test"))
		return nil
	}
	executable, err := resolvedExecutable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		if status.Installed {
			fmt.Fprintln(c.Out, "Repairing Portless HTTP ingress and TCP endpoint DNS requires administrator approval.")
		} else {
			fmt.Fprintln(c.Out, "Portless needs administrator approval once to install HTTP ingress and scoped TCP endpoint DNS.")
		}
	}
	installOutput := c.Out
	if jsonOutput {
		installOutput = c.Err
	}
	if err := ingress.Install(ctx, ingress.SetupRequest{
		Executable: executable, TargetSocket: c.paths.Ingress, DNSTargetSocket: c.paths.DNS,
		UID: uid, GID: gid, Stdin: os.Stdin, Stdout: installOutput, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := ingress.WaitUntilReady(ctx, 8*time.Second); err != nil {
		return err
	}
	if jsonOutput {
		ready, err := ingress.Inspect(ctx)
		if err != nil {
			return err
		}
		return writeRelayStatusJSON(c.Out, ready)
	}
	fmt.Fprintln(c.Out, "Clean local endpoints are", c.success(c.Out, "ready")+".")
	fmt.Fprintln(c.Out, c.accent(c.Out, ingress.ControlOrigin))
	fmt.Fprintln(c.Out, c.accent(c.Out, "*.portless.test"))
	return nil
}

func (c *CLI) relayStatus(ctx context.Context, jsonOutput bool) error {
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeRelayStatusJSON(c.Out, status)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Portless relay:"), c.state(c.Out, status.State()))
	fmt.Fprintln(c.Out, "Platform:", status.Platform)
	fmt.Fprintln(c.Out, "HTTP listener:", ingress.DefaultListenAddress)
	fmt.Fprintln(c.Out, "Control URL:", ingress.ControlOrigin)
	fmt.Fprintln(c.Out, "DNS domain:", "portless.test")
	fmt.Fprintln(c.Out, "DNS listener:", ingress.DefaultDNSAddress, "(UDP and TCP)")
	if !status.Installed {
		fmt.Fprintln(c.Out, "Run `portless relay install` or `portless setup` to install it.")
		return nil
	}
	fmt.Fprintln(c.Out, "Service:", status.Service)
	if status.OwnerUID > 0 {
		fmt.Fprintf(c.Out, "Owner: UID %d, GID %d\n", status.OwnerUID, status.OwnerGID)
	} else {
		fmt.Fprintln(c.Out, "Owner: unknown")
	}
	if status.TargetSocket != "" {
		fmt.Fprintln(c.Out, "HTTP forwards to:", status.TargetSocket)
	}
	if status.DNSTargetSocket != "" {
		fmt.Fprintln(c.Out, "DNS forwards to:", status.DNSTargetSocket)
	}
	if status.ResolverPath != "" {
		fmt.Fprintln(c.Out, "Resolver:", status.ResolverPath)
	}
	poolState := "not ready"
	if status.EndpointPoolReady {
		poolState = "ready"
	}
	fmt.Fprintln(c.Out, "TCP endpoint pool:", poolState)
	if status.EndpointPoolDetail != "" {
		fmt.Fprintln(c.Out, "  "+status.EndpointPoolDetail)
	}
	if status.InstalledAt != nil {
		fmt.Fprintln(c.Out, "Installed:", status.InstalledAt.Local().Format(time.RFC3339))
	}
	fmt.Fprintln(c.Out, "Helper:", status.HelperPath)
	fmt.Fprintln(c.Out, "Configuration:", status.ConfigurationPath)
	fmt.Fprintln(c.Out, "Receipt:", status.ReceiptPath)
	if status.HealthError != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "HTTP check:"), status.HealthError)
	}
	if status.DNSHealthError != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "DNS check:"), status.DNSHealthError)
	}
	if status.ResolverHealthError != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "Resolver check:"), status.ResolverHealthError)
	}
	if status.Problem != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "Problem:"), status.Problem)
	}
	return nil
}

func (c *CLI) restartRelay(ctx context.Context, jsonOutput bool) error {
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed; run `portless relay install`")
	}
	uid, _ := requestingUserIDs()
	if err := ingress.ValidateOwnership(status, uid); err != nil {
		return err
	}
	if status.TargetSocket != "" && status.TargetSocket != c.paths.Ingress {
		return fmt.Errorf("the relay targets %s, but this Portless installation uses %s; run `portless relay install` to repair it", status.TargetSocket, c.paths.Ingress)
	}
	if status.DNSTargetSocket != c.paths.DNS || !status.ResolverPresent {
		return errors.New("the relay DNS configuration is stale; run `portless relay install` to repair it")
	}
	if _, err := bootstrap.EnsureDaemon(ctx, c.paths); err != nil {
		return err
	}
	executable, err := resolvedExecutable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		fmt.Fprintln(c.Out, "Restarting the Portless localhost relay requires administrator approval.")
	}
	restartOutput := c.Out
	if jsonOutput {
		restartOutput = c.Err
	}
	if err := ingress.Restart(ctx, ingress.RestartRequest{
		Executable: executable, UID: uid, Stdin: os.Stdin, Stdout: restartOutput, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := ingress.WaitUntilReady(ctx, 8*time.Second); err != nil {
		return err
	}
	ready, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(c.Out, relayActionOutput{
			Action:            "restart",
			relayStatusOutput: relayStatusOutput{State: ready.State(), InstallationStatus: ready},
		})
	}
	fmt.Fprintln(c.Out, "Clean-URL relay restarted and", c.success(c.Out, "ready")+".")
	fmt.Fprintln(c.Out, c.accent(c.Out, ingress.ControlOrigin))
	return nil
}

func (c *CLI) uninstallRelay(ctx context.Context, force, jsonOutput bool) error {
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		if jsonOutput {
			return writeJSON(c.Out, actionOutput{Action: "uninstall", Status: "not-installed"})
		}
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
	if !jsonOutput {
		fmt.Fprintln(c.Out, "Removing the Portless clean-URL relay:")
		fmt.Fprintln(c.Out, "  service:", status.Service)
		fmt.Fprintln(c.Out, "  helper: ", status.HelperPath)
		fmt.Fprintln(c.Out, "Projects, containers, volumes, recordings, and Portless user data will not be removed.")
		fmt.Fprintln(c.Out, "Administrator approval is required to remove the system service.")
	}
	uninstallOutput := c.Out
	if jsonOutput {
		uninstallOutput = c.Err
	}
	removed, err := ingress.Uninstall(ctx, ingress.UninstallRequest{
		Executable: executable, UID: uid, Force: force, Stdin: os.Stdin, Stdout: uninstallOutput, Stderr: c.Err,
	})
	if err != nil {
		return err
	}
	if !removed {
		if jsonOutput {
			return writeJSON(c.Out, actionOutput{Action: "uninstall", Status: "not-installed"})
		}
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	if jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "uninstall", Name: status.Service, Status: "removed"})
	}
	fmt.Fprintln(c.Out, "Clean-URL relay removed. Portless no longer owns 127.0.0.1:80, "+ingress.DefaultDNSAddress+", its reserved loopback endpoint pool, or the portless.test resolver entry.")
	fmt.Fprintln(c.Out, "Running environments were not stopped, but their clean localhost URLs are unavailable until `portless relay install` or `portless setup` is run.")
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
	selector, err := c.effectiveEnvironmentSelector(selector)
	if err != nil {
		return err
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
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
			warnings = nonNilStrings(discovered.Warnings)
			c.printWarnings(warnings)
		}
	}
	if err != nil {
		return err
	}
	request := upRequest{Managed: options.managed}
	if !options.managed {
		debugService := strings.TrimSpace(options.debug)
		if debugService == "" {
			cwd, cwdErr := currentWorkingDirectory()
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
	var operation model.Operation
	idempotency, err := invocationKey("cli-up")
	if err != nil {
		return err
	}
	if err := client.DoWithHeaders(operationContext, http.MethodPost, environmentAPI(environment)+"/up", request, &operation, map[string]string{"Idempotency-Key": idempotency}); err != nil {
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
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment), nil, &environment); err != nil {
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
		next := "/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
		if browserURL, browserErr := c.browserURL(ctx, client, next); browserErr == nil {
			_ = launchBrowser(browserURL)
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
		client, _, err := bootstrap.Connect(ctx, c.paths)
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
	if selector != "" {
		return err
	}
	var response struct {
		Environments []model.Environment `json:"environments"`
	}
	if requestErr := client.Do(ctx, http.MethodGet, "/api/v1/environments", nil, &response); requestErr != nil {
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
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	if err := c.requireIngress(ctx); err != nil {
		return err
	}
	next := "/projects"
	if environment, findErr := c.resolveEnvironment(ctx, client, ""); findErr == nil {
		next = "/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
	}
	browserURL, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	launchErr := launchBrowser(browserURL)
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
					launchErr := launchBrowser(endpoint.URL)
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
	next := "/environments/" + bootstrap.EscapePath(environment.Project, environment.Name)
	browserURL, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	launchErr := launchBrowser(browserURL)
	if c.jsonOutput {
		if err := writeJSON(c.Out, browserOutput{URL: browserURL, Opened: launchErr == nil, Error: errorString(launchErr)}); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(c.Out, "Portless control plane:", c.accent(c.Out, browserURL))
	}
	return launchErr
}

func (c *CLI) logs(ctx context.Context, requestedService string, options logsOptions) error {
	if err := validLimit(options.limit, 10_000); err != nil {
		return err
	}
	if options.since < 0 {
		return usageError("--since cannot be negative")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	if _, err := logServiceNames(environment, requestedService); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	var cursor time.Time
	initial := true
	for {
		var response struct {
			Entries []model.LogEntry `json:"entries"`
		}
		query := url.Values{"limit": {strconv.Itoa(options.limit)}}
		if requestedService != "" {
			query.Set("service", requestedService)
		}
		if !cursor.IsZero() {
			query.Set("since", cursor.Format(time.RFC3339Nano))
		} else if options.since > 0 {
			query.Set("since", options.since.String())
		}
		path := environmentAPI(environment) + "/logs?" + query.Encode()
		if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if response.Entries == nil {
			response.Entries = []model.LogEntry{}
		}
		if !options.tail {
			if c.jsonOutput {
				return writeJSON(c.Out, logsOutput{Project: environment.Project, Environment: environment.Name, Entries: response.Entries})
			}
			c.printLogs(environment, response.Entries, requestedService != "", options.timestamps)
			return nil
		}
		for _, entry := range response.Entries {
			if entry.Timestamp.After(cursor) {
				cursor = entry.Timestamp
				seen = make(map[string]struct{})
			}
			key := logEntryKey(entry)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			if c.jsonOutput {
				if err := writeJSONLine(c.Out, entry); err != nil {
					return err
				}
			} else {
				c.printLogEntry(entry, requestedService == "", options.timestamps)
			}
		}
		if initial && len(response.Entries) == 0 && !c.jsonOutput {
			c.printLogs(environment, response.Entries, requestedService != "", options.timestamps)
		}
		initial = false
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func logServiceNames(environment model.Environment, requested string) ([]string, error) {
	if requested != "" {
		for _, service := range environment.Services {
			if strings.EqualFold(service.Name, requested) {
				return []string{service.Name}, nil
			}
		}
		return nil, fmt.Errorf("service %s was not found in %s/%s", requested, environment.Project, environment.Name)
	}
	services := make([]string, 0, len(environment.Services))
	for _, service := range environment.Services {
		services = append(services, service.Name)
	}
	return services, nil
}

func (c *CLI) printLogs(environment model.Environment, entries []model.LogEntry, singleService, timestamps bool) {
	for _, entry := range entries {
		c.printLogEntry(entry, !singleService, timestamps)
	}
	if len(entries) > 0 {
		return
	}
	if singleService {
		fmt.Fprintln(c.Out, "No logs for the selected service.")
		return
	}
	fmt.Fprintf(c.Out, "No logs for %s/%s.\n", environment.Project, environment.Name)
}

func (c *CLI) printLogEntry(entry model.LogEntry, includeService, timestamps bool) {
	prefix := ""
	if timestamps {
		prefix = entry.Timestamp.Local().Format("15:04:05.000") + " "
	}
	if includeService {
		fmt.Fprintf(c.Out, "%s%s %s\n", prefix, c.accent(c.Out, "["+entry.Service+"]"), entry.Message)
		return
	}
	fmt.Fprintln(c.Out, prefix+entry.Message)
}

func logEntryKey(entry model.LogEntry) string {
	return entry.Timestamp.Format(time.RFC3339Nano) + "\x00" + entry.Service + "\x00" + entry.Stream + "\x00" + strconv.FormatInt(entry.Generation, 10) + "\x00" + entry.Message
}

func (c *CLI) traffic(ctx context.Context, options trafficOptions) error {
	if options.protocol != "http" && options.protocol != "tcp" {
		return usageError("--protocol must be http or tcp")
	}
	if err := validLimit(options.limit, 1000); err != nil {
		return err
	}
	if options.edge != "" {
		if source, target, err := parseEdge(options.edge); err != nil || source == "" || target == "" {
			return usageError("--edge must use source:target")
		}
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Traffic []model.TrafficEvent `json:"traffic"`
	}
	query := trafficQuery(options, 0)
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/traffic?"+query.Encode(), nil, &response); err != nil {
		return err
	}
	traffic := make([]model.TrafficEvent, 0, len(response.Traffic))
	for index := len(response.Traffic) - 1; index >= 0; index-- {
		traffic = append(traffic, response.Traffic[index])
	}
	if !options.tail {
		if c.jsonOutput {
			return writeJSON(c.Out, map[string]any{"project": environment.Project, "environment": environment.Name, "traffic": traffic})
		}
		c.printTrafficList(environment, options.protocol, traffic)
		return nil
	}
	if c.jsonOutput {
		for _, event := range traffic {
			if err := writeJSONLine(c.Out, event); err != nil {
				return err
			}
		}
	} else {
		c.printTrafficList(environment, options.protocol, traffic)
	}
	seen := make(map[int64]struct{}, len(traffic))
	for _, event := range traffic {
		seen[event.Sequence] = struct{}{}
	}
	return c.followTraffic(ctx, client, environment, options, seen, c.jsonOutput)
}

func trafficQuery(options trafficOptions, after int64) url.Values {
	query := url.Values{"protocol": {options.protocol}, "limit": {strconv.Itoa(options.limit)}}
	if options.service != "" {
		query.Set("service", options.service)
	}
	if options.edge != "" {
		query.Set("edge", options.edge)
	}
	if after > 0 {
		query.Set("after", strconv.FormatInt(after, 10))
	}
	return query
}

func (c *CLI) showTraffic(ctx context.Context, sequenceValue string) error {
	sequence, err := strconv.ParseInt(sequenceValue, 10, 64)
	if err != nil || sequence <= 0 {
		return usageError("traffic sequence must be a positive integer")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var event model.TrafficEvent
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/traffic/"+strconv.FormatInt(sequence, 10), nil, &event); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, event)
	}
	c.printTrafficDetail(event)
	return nil
}

func (c *CLI) listRecordings(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Recordings []model.Recording `json:"recordings"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/recordings?limit="+strconv.Itoa(limit), nil, &response); err != nil {
		return err
	}
	if response.Recordings == nil {
		response.Recordings = []model.Recording{}
	}
	response.Recordings = truncate(response.Recordings, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Recordings) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No recordings."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, "Recordings"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-24s %-10s %-8s %s", "NAME", "STATE", "EVENTS", "EDGE")))
	for _, item := range response.Recordings {
		fmt.Fprintf(c.Out, "%-24s %s %-8d %s → %s\n", item.Name, c.state(c.Out, fmt.Sprintf("%-10s", item.Status)), item.EventCount, emptyAs(item.Source, "any"), emptyAs(item.Target, "any"))
	}
	return nil
}

func (c *CLI) startRecording(ctx context.Context, name string, options recordingOptions) error {
	if options.duration <= 0 || options.duration > time.Hour {
		return usageError("--duration must be greater than zero and no more than 1h")
	}
	if options.maxEvents < 1 || options.maxEvents > 100_000 {
		return usageError("--max-events must be between 1 and 100000")
	}
	source, target, err := parseEdge(options.edge)
	if err != nil {
		return usageError("--edge must use source:target")
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
	if c.jsonOutput {
		return writeJSON(c.Out, created)
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
	var stopped model.Recording
	if err := client.Do(ctx, http.MethodPost, base+"/"+bootstrap.EscapePath(name)+"/stop", nil, &stopped); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, stopped)
	}
	fmt.Fprintln(c.Out, "recording", name, "stopped")
	return nil
}

func (c *CLI) exportRecording(ctx context.Context, name string, options exportOptions) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var content []byte
	path := environmentAPI(environment) + "/recordings/" + bootstrap.EscapePath(name) + "/export"
	if err := client.Do(ctx, http.MethodGet, path, nil, &content); err != nil {
		return err
	}
	if options.output == "-" {
		_, err = c.Out.Write(content)
		return err
	}
	if err := writePrivateFile(options.output, content, options.force); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "export", Project: environment.Project, Environment: environment.Name, Name: name, Path: options.output, Status: "written"})
	}
	fmt.Fprintln(c.Out, "wrote", options.output)
	return nil
}

func (c *CLI) deleteRecording(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	path := environmentAPI(environment) + "/recordings/" + bootstrap.EscapePath(name)
	if err := client.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "recording", name, "deleted")
	return nil
}

func (c *CLI) listFaults(ctx context.Context, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Faults []model.FaultRule `json:"faults"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/faults?limit="+strconv.Itoa(limit), nil, &response); err != nil {
		return err
	}
	if response.Faults == nil {
		response.Faults = []model.FaultRule{}
	}
	response.Faults = truncate(response.Faults, limit)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Faults) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No fault rules."))
		return nil
	}
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, "Fault rules"), environment.Project, environment.Name)
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-24s %-9s %-22s %s", "NAME", "STATE", "LIFETIME", "SCOPE")))
	for _, fault := range response.Faults {
		state := "disabled"
		lifetime := "—"
		if fault.Enabled {
			state = "active"
			lifetime = "until disabled"
			if fault.ExpiresAt != nil {
				lifetime = "until " + fault.ExpiresAt.Local().Format("2006-01-02 15:04")
			}
		}
		fmt.Fprintf(c.Out, "%-24s %s %-22s %s\n", fault.Name, c.state(c.Out, fmt.Sprintf("%-9s", state)), lifetime, fault.ScopeSummary)
	}
	return nil
}

func (c *CLI) addFault(ctx context.Context, name, edge string, options faultOptions) error {
	source, target, err := parseEdge(edge)
	if err != nil || source == "" || target == "" {
		return usageError("edge must use source:target")
	}
	if options.duration < 0 {
		return usageError("--duration must be zero or greater")
	}
	if options.probability <= 0 || options.probability > 1 {
		return usageError("--probability must be greater than zero and no more than 1")
	}
	if options.latency < 0 || options.jitter < 0 || options.latency+options.jitter > 60_000 {
		return usageError("--latency plus --jitter must be between 0 and 60000 milliseconds")
	}
	if options.status != 0 && (options.status < 400 || options.status > 599) {
		return usageError("--status must be between 400 and 599")
	}
	if options.latency == 0 && options.jitter == 0 && options.status == 0 && !options.abort {
		return usageError("define at least one effect with --latency, --jitter, --status, or --abort")
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	input := model.FaultRule{Name: name, Source: source, Target: target, LatencyMS: options.latency, JitterMS: options.jitter, StatusCode: options.status, Abort: options.abort, Probability: options.probability, Method: options.method, Path: options.path}
	if options.duration > 0 {
		expires := time.Now().UTC().Add(options.duration)
		input.ExpiresAt = &expires
	}
	var created model.FaultRule
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/faults", input, &created); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, created)
	}
	lifetime := "until disabled"
	if created.ExpiresAt != nil {
		lifetime = "until " + created.ExpiresAt.Local().Format(time.RFC3339)
	}
	fmt.Fprintf(c.Out, "fault %s active %s: %s\n", created.Name, lifetime, created.ScopeSummary)
	return nil
}

func (c *CLI) disableFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	path := environmentAPI(environment) + "/faults/" + bootstrap.EscapePath(name) + "/disable"
	if err := client.Do(ctx, http.MethodPost, path, nil, nil); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "disable", Project: environment.Project, Environment: environment.Name, Name: name, Status: "disabled"})
	}
	fmt.Fprintln(c.Out, "fault", name, "disabled")
	return nil
}

func (c *CLI) enableFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	path := environmentAPI(environment) + "/faults/" + bootstrap.EscapePath(name) + "/enable"
	var fault model.FaultRule
	if err := client.Do(ctx, http.MethodPost, path, nil, &fault); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, fault)
	}
	fmt.Fprintln(c.Out, "fault", name, "enabled")
	return nil
}

func (c *CLI) deleteFault(ctx context.Context, name string) error {
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	path := environmentAPI(environment) + "/faults/" + bootstrap.EscapePath(name)
	if err := client.Do(ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "delete", Project: environment.Project, Environment: environment.Name, Name: name, Status: "deleted"})
	}
	fmt.Fprintln(c.Out, "fault", name, "deleted")
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
	if c.jsonOutput {
		if result == nil {
			result = map[string]any{}
		}
		result["project"] = environment.Project
		result["environment"] = environment.Name
		return writeJSON(c.Out, result)
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
	response.Warnings = nonNilStrings(response.Warnings)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	c.printWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "created %s with environment %s and %d sources\n", response.Project.Name, response.Environment.Name, len(sources))
	return nil
}

func (c *CLI) addProjectSource(ctx context.Context, source, pathValue string) error {
	sourcePath, err := absoluteSourcePath(pathValue)
	if err != nil {
		return err
	}
	client, environment, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response projectSourceResponse
	path := "/api/v1/projects/" + bootstrap.EscapePath(environment.Project) + "/sources"
	input := map[string]string{"name": source, "path": sourcePath, "environment": environment.Name}
	if err := client.Do(ctx, http.MethodPost, path, input, &response); err != nil {
		return err
	}
	response.Warnings = nonNilStrings(response.Warnings)
	if c.jsonOutput {
		return writeJSON(c.Out, response)
	}
	c.printWarnings(response.Warnings)
	fmt.Fprintf(c.Out, "%s source %s to project %s\n", c.success(c.Out, "added"), source, response.Project.Name)
	fmt.Fprintf(c.Out, "%s now uses %s for source %s\n", model.EnvironmentSelector(response.Environment.Project, response.Environment.Name), sourcePath, source)
	pending := nonNilStrings(response.ConfigurationRequired)
	if len(pending) > 0 {
		label := "environment requires"
		if len(pending) != 1 {
			label = "environments require"
		}
		fmt.Fprintf(c.Out, "%d other %s configuration: %s\n", len(pending), label, strings.Join(pending, ", "))
		for _, selector := range pending {
			fmt.Fprintln(c.Out, "  "+c.accent(c.Out, "portless --env "+selector+" env source "+source+" --path <checkout>"))
		}
		fmt.Fprintln(c.Out, "Or bind the new services remotely with `portless env bind`.")
	}
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
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "export", Project: environment.Project, Path: output, Status: "written"})
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
	if c.jsonOutput {
		return writeJSON(c.Out, renamed)
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
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "forget", Project: environment.Project, Status: "forgotten"})
	}
	fmt.Fprintln(c.Out, "forgot", environment.Project)
	return nil
}

func (c *CLI) listEnvironments(ctx context.Context, project string, limit int) error {
	if err := validLimit(limit, 1000); err != nil {
		return err
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if project != "" {
		query.Set("project", project)
	}
	path := "/api/v1/environments?" + query.Encode()
	var response struct {
		Environments []model.Environment `json:"environments"`
	}
	if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
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
	var created model.Environment
	if err := client.Do(ctx, http.MethodPost, "/api/v1/environments", map[string]any{"project": current.Project, "name": name, "from": from}, &created); err != nil {
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
	var updated model.Environment
	path := environmentAPI(environment) + "/bindings/" + bootstrap.EscapePath(service)
	if err := client.Do(ctx, http.MethodPut, path, binding, &updated); err != nil {
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
	var response struct {
		Environment model.Environment `json:"environment"`
		Warnings    []string          `json:"warnings"`
	}
	path := environmentAPI(environment) + "/sources/" + bootstrap.EscapePath(source)
	if err := client.Do(ctx, http.MethodPut, path, map[string]string{"path": sourcePath}, &response); err != nil {
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
	var response struct {
		Environment model.Environment `json:"environment"`
		Warnings    []string          `json:"warnings"`
	}
	if err := client.Do(ctx, http.MethodPost, environmentAPI(environment)+"/rescan", nil, &response); err != nil {
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
	if err := client.Do(ctx, http.MethodDelete, environmentAPI(environment), nil, nil); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "forget", Project: environment.Project, Environment: environment.Name, Status: "forgotten"})
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

func (c *CLI) selectEnvironment(ctx context.Context, selector string) error {
	if c.environmentOverride != "" {
		return usageError("--env cannot be used with env select; pass the environment to env select directly")
	}
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
	root, err := currentSourceRoot(ctx)
	if err != nil {
		return err
	}
	if err := client.Do(ctx, http.MethodPut, "/api/v1/environments/select", map[string]any{"path": root, "project": project, "environment": environment}, nil); err != nil {
		return err
	}
	if c.jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "select", Project: project, Environment: environment, Path: root, Status: "selected"})
	}
	fmt.Fprintln(c.Out, "selected", selector, "for", root)
	return nil
}

func (c *CLI) showEnvironmentContext(ctx context.Context) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
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
	root, err := currentSourceRoot(ctx)
	if err != nil {
		return err
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	var result struct {
		Cleared bool `json:"cleared"`
	}
	path := "/api/v1/environments/select?path=" + url.QueryEscape(root)
	if err := client.Do(ctx, http.MethodDelete, path, nil, &result); err != nil {
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

func (c *CLI) runtimeStatus(ctx context.Context, jsonOutput bool) error {
	return c.runtimeRequest(ctx, http.MethodGet, "/api/v1/runtime", nil, jsonOutput)
}

func (c *CLI) startRuntime(ctx context.Context, jsonOutput bool) error {
	return c.runtimeRequest(ctx, http.MethodPost, "/api/v1/runtime/start", nil, jsonOutput)
}

func (c *CLI) useRuntime(ctx context.Context, preference string, jsonOutput bool) error {
	return c.runtimeRequest(ctx, http.MethodPut, "/api/v1/runtime", map[string]string{"preference": preference}, jsonOutput)
}

func (c *CLI) runtimeRequest(ctx context.Context, method, path string, body any, jsonOutput bool) error {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	var status container.Status
	if err := client.Do(ctx, method, path, body, &status); err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(c.Out, status)
	}
	c.printRuntimeStatus(status)
	return nil
}

func (c *CLI) printRuntimeStatus(status container.Status) {
	state := status.State
	if state == "" {
		state = "unknown"
	}
	selected := "none"
	if status.Selected != "" {
		selected = string(status.Selected)
		if status.Version != "" {
			selected += " " + status.Version
		}
	}

	fmt.Fprintln(c.Out, c.heading(c.Out, "Container runtime"))
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %-11s %s\n", "Status:", c.state(c.Out, state))
	fmt.Fprintf(c.Out, "  %-11s %s\n", "Selected:", selected)
	fmt.Fprintf(c.Out, "  %-11s %s\n", "Preference:", status.Preference)
	if status.Reason != "" {
		fmt.Fprintf(c.Out, "  %-11s %s\n", "Reason:", status.Reason)
	}
	if len(status.Candidates) == 0 {
		return
	}

	ordered := make([]container.ProbeResult, 0, len(status.Candidates))
	for _, candidate := range status.Candidates {
		if candidate.Name == status.Selected {
			ordered = append(ordered, candidate)
		}
	}
	for _, candidate := range status.Candidates {
		if candidate.Name != status.Selected {
			ordered = append(ordered, candidate)
		}
	}

	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("  %-10s %-10s %-10s %s", "RUNTIME", "STATE", "VERSION", "DETAILS")))
	for _, candidate := range ordered {
		version := candidate.Version
		if version == "" {
			version = "—"
		}
		details := candidate.Reason
		if candidate.Name == status.Selected {
			if details == "" {
				details = "selected"
			} else {
				details = "selected · " + details
			}
		}
		fmt.Fprintf(c.Out, "  %-10s %s %-10s %s\n", candidate.Name, c.state(c.Out, fmt.Sprintf("%-10s", candidate.State)), version, details)
	}
}

func (c *CLI) current(ctx context.Context) (*bootstrap.Client, model.Environment, error) {
	return c.currentOrNamed(ctx, "")
}

func (c *CLI) currentOrNamed(ctx context.Context, selector string) (*bootstrap.Client, model.Environment, error) {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return nil, model.Environment{}, err
	}
	environment, err := c.resolveEnvironment(ctx, client, selector)
	if err != nil {
		return nil, model.Environment{}, err
	}
	return client, environment, nil
}

func (c *CLI) findCurrent(ctx context.Context, client *bootstrap.Client) (model.Environment, error) {
	resolved, err := c.resolveEnvironmentContext(ctx, client)
	if err != nil {
		return model.Environment{}, err
	}
	return resolved.Environment, nil
}

func (c *CLI) resolveEnvironment(ctx context.Context, client *bootstrap.Client, selector string) (model.Environment, error) {
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

func (c *CLI) resolveEnvironmentContext(ctx context.Context, client *bootstrap.Client) (environmentContextOutput, error) {
	root, err := currentSourceRoot(ctx)
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
	var response application.EnvironmentContext
	path := "/api/v1/environments/context?path=" + url.QueryEscape(root)
	if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
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

func (c *CLI) environmentsForCurrentPath(ctx context.Context, client *bootstrap.Client) ([]model.Environment, error) {
	root, err := currentSourceRoot(ctx)
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

func currentSourceRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, err := discovery.FindRoot(ctx, cwd)
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

func (c *CLI) requireIngress(ctx context.Context) error {
	if e2ePrivateIngress {
		if err := ingress.CheckSocket(ctx, c.paths.Ingress); err != nil {
			return fmt.Errorf("verify private Portless ingress: %w", err)
		}
		return nil
	}
	status, err := ingress.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("inspect local endpoint networking: %w", err)
	}
	uid, _ := requestingUserIDs()
	ready := status.Installed && status.Healthy && status.OwnerUID == uid && status.TargetSocket == c.paths.Ingress && status.DNSTargetSocket == c.paths.DNS
	if ready {
		return nil
	}
	detail := "HTTP ingress, TCP DNS, or the loopback endpoint pool is incomplete"
	switch {
	case !status.Installed:
		detail = "the Portless relay is not installed"
	case status.OwnerUID != uid:
		detail = fmt.Sprintf("the Portless relay belongs to user ID %d instead of %d", status.OwnerUID, uid)
	case status.TargetSocket != c.paths.Ingress:
		detail = fmt.Sprintf("the HTTP relay targets %s instead of %s", emptyAs(status.TargetSocket, "an unknown socket"), c.paths.Ingress)
	case status.DNSTargetSocket != c.paths.DNS:
		detail = fmt.Sprintf("the DNS relay targets %s instead of %s", emptyAs(status.DNSTargetSocket, "an unknown socket"), c.paths.DNS)
	case !status.EndpointPoolReady:
		detail = emptyAs(status.EndpointPoolDetail, "the loopback endpoint pool is not ready")
	case !status.ResolverPresent:
		detail = "the scoped portless.test resolver configuration is missing"
	case !status.ResolverHealthy:
		detail = emptyAs(status.ResolverHealthError, "the system resolver cannot resolve portless.test")
	case !status.DNSHealthy:
		detail = emptyAs(status.DNSHealthError, "the authoritative DNS relay is not healthy")
	case !status.HTTPHealthy:
		detail = emptyAs(status.HealthError, "the clean HTTP ingress is not healthy")
	case status.Problem != "":
		detail = status.Problem
	}
	return fmt.Errorf("clean local endpoints are not configured for this Portless installation; run `portless relay install` or `portless setup`, then retry: %s", detail)
}

func (c *CLI) waitOperation(ctx context.Context, client *bootstrap.Client, operation model.Operation, jsonOutput bool) (model.Operation, error) {
	seen := 0
	for {
		path := "/api/v1/environments/" + bootstrap.EscapePath(operation.Project, operation.Environment) + "/operations/" + strconv.FormatInt(operation.Number, 10)
		if err := client.Do(ctx, http.MethodGet, path, nil, &operation); err != nil {
			return model.Operation{}, err
		}
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

func (c *CLI) followTraffic(ctx context.Context, client *bootstrap.Client, environment model.Environment, options trafficOptions, seen map[int64]struct{}, jsonOutput bool) error {
	topic := "traffic.http"
	if options.protocol == "tcp" {
		topic = "traffic.tcp"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+environmentAPI(environment)+"/stream?topic="+url.QueryEscape(topic), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.Token)
	request.Header.Set("Accept", "text/event-stream")
	streamClient := *client.HTTP
	streamClient.Timeout = 0
	response, err := streamClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("traffic stream returned %s", response.Status)
	}
	last := int64(0)
	for sequence := range seen {
		if sequence > last {
			last = sequence
		}
	}
	var replay struct {
		Traffic []model.TrafficEvent `json:"traffic"`
	}
	if err := client.Do(ctx, http.MethodGet, environmentAPI(environment)+"/traffic?"+trafficQuery(options, last).Encode(), nil, &replay); err != nil {
		return err
	}
	for index := len(replay.Traffic) - 1; index >= 0; index-- {
		event := replay.Traffic[index]
		if _, exists := seen[event.Sequence]; exists {
			continue
		}
		seen[event.Sequence] = struct{}{}
		if jsonOutput {
			if err := writeJSONLine(c.Out, event); err != nil {
				return err
			}
		} else {
			c.printTraffic(event)
		}
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	eventType := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") && eventType == topic {
			var event model.TrafficEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && matchesTrafficOptions(event, options) {
				if _, exists := seen[event.Sequence]; exists {
					continue
				}
				seen[event.Sequence] = struct{}{}
				if jsonOutput {
					if err := writeJSONLine(c.Out, event); err != nil {
						return err
					}
				} else {
					c.printTraffic(event)
				}
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
	fmt.Fprintf(c.Out, "%s  %s  %d/%d ready\n\n", c.heading(c.Out, environment.Project+"/"+environment.Name), c.state(c.Out, string(environment.Status)), ready, len(environment.Services))
	fmt.Fprintln(c.Out, c.muted(c.Out, "SERVICE                 PROVIDER    MODE         KIND        STATE          ENDPOINT"))
	for _, service := range environment.Services {
		kind := string(service.Kind)
		if service.Framework != "" {
			kind = service.Framework
		} else if service.Resource != nil {
			kind = service.Resource.Type
		}
		provider := "local"
		for _, binding := range environment.Bindings {
			if strings.EqualFold(binding.Service, service.Name) {
				provider = string(binding.Provider)
				break
			}
		}
		fmt.Fprintf(c.Out, "%-23s %-11s %-12s %-11s %s %s\n", service.Name, provider, serviceMode(environment, service), kind, c.state(c.Out, fmt.Sprintf("%-14s", service.Status)), c.accent(c.Out, statusEndpoint(service)))
	}
	fmt.Fprintln(c.Out, "\nDashboard:", c.accent(c.Out, environment.DashboardURL))
}

func (c *CLI) printDebugGuidance(environment model.Environment) {
	var debugging []model.Service
	for _, service := range environment.Services {
		if service.LaunchMode == model.LaunchDebug && service.Debugger != nil {
			debugging = append(debugging, service)
		}
	}
	if len(debugging) == 0 {
		return
	}
	fmt.Fprintln(c.Out, "\n"+c.heading(c.Out, "Debuggers"))
	for _, service := range debugging {
		fmt.Fprintf(c.Out, "  %-18s %s at %s:%d\n", service.Name, service.Debugger.Adapter, service.Debugger.Host, service.Debugger.Port)
	}
	fmt.Fprintln(c.Out, "\nUse your IDE's Attach to Process action and choose the matching Node or JVM process. No run configuration or environment file is required.")
}

func serviceMode(environment model.Environment, service model.Service) string {
	if providerFor(environment, service.Name) != model.ProviderLocal || service.Kind != model.ServiceProcess {
		return "—"
	}
	if service.LaunchMode == "" {
		return string(model.LaunchManaged)
	}
	return string(service.LaunchMode)
}

func statusEndpoint(service model.Service) string {
	if endpoint := primaryServiceEndpoint(service); endpoint != nil {
		return endpoint.URL
	}
	return ""
}

func primaryServiceEndpoint(service model.Service) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic {
			return &service.Endpoints[index]
		}
	}
	return nil
}

func serviceEndpointForProtocol(service model.Service, protocol model.Protocol) *model.Endpoint {
	for index := range service.Endpoints {
		if service.Endpoints[index].Kind == model.EndpointPublic && service.Endpoints[index].Protocol == protocol {
			return &service.Endpoints[index]
		}
	}
	return nil
}

func (c *CLI) printOperation(operation model.Operation) {
	fmt.Fprintf(c.Out, "%s operation %d %s\n", operation.Type, operation.Number, c.state(c.Out, operation.State))
}

func (c *CLI) printTrafficList(environment model.Environment, protocol string, events []model.TrafficEvent) {
	title := strings.ToUpper(protocol) + " traffic"
	fmt.Fprintf(c.Out, "%s · %s/%s\n\n", c.heading(c.Out, title), environment.Project, environment.Name)
	if len(events) == 0 {
		fmt.Fprintln(c.Out, c.muted(c.Out, "No "+strings.ToUpper(protocol)+" traffic captured."))
		return
	}
	if protocol == "http" {
		fmt.Fprintln(c.Out, c.muted(c.Out, "SEQ    METHOD  PATH               CODE  TIME    EDGE"))
	} else {
		fmt.Fprintln(c.Out, c.muted(c.Out, "SEQ    PROTOCOL   TIME    EDGE                         RESULT"))
	}
	for _, event := range events {
		c.printTraffic(event)
	}
}

func (c *CLI) printTraffic(event model.TrafficEvent) {
	fault := ""
	if event.Fault != "" {
		fault = " fault=" + event.Fault
	}
	if event.Protocol != model.ProtocolHTTP {
		result := "ok"
		if event.Error != "" {
			result = c.failure(c.Out, event.Error)
		}
		if event.Fault != "" {
			result = c.warning(c.Out, "fault="+event.Fault)
		}
		fmt.Fprintf(c.Out, "#%-5d %-10s %5dms %-28s %s\n", event.Sequence, strings.ToUpper(string(event.Protocol)), event.DurationMS, event.Source+":"+event.Target, result)
		return
	}
	status := fmt.Sprintf("%4d", event.Status)
	switch {
	case event.Status >= 500:
		status = c.failure(c.Out, status)
	case event.Status >= 400:
		status = c.warning(c.Out, status)
	case event.Status >= 200 && event.Status < 400:
		status = c.success(c.Out, status)
	}
	if fault != "" {
		fault = c.warning(c.Out, fault)
	}
	method := event.Method
	path := event.Path
	if method == "" {
		method = strings.ToUpper(string(event.Protocol))
	}
	if path == "" {
		path = "session"
	}
	fmt.Fprintf(c.Out, "#%-5d %-7s %-18s %s %5dms %s:%s%s\n", event.Sequence, method, path, status, event.DurationMS, event.Source, event.Target, fault)
}

func (c *CLI) printTrafficDetail(event model.TrafficEvent) {
	fmt.Fprintf(c.Out, "%s #%d\n\n", c.heading(c.Out, strings.ToUpper(string(event.Protocol))+" traffic"), event.Sequence)
	fmt.Fprintf(c.Out, "  %-18s %s → %s\n", "Edge:", event.Source, event.Target)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Provider:", emptyAs(string(event.TargetProvider), "unknown"))
	if event.Method != "" {
		fmt.Fprintf(c.Out, "  %-18s %s %s\n", "Request:", event.Method, event.Path)
	}
	if event.Status != 0 {
		fmt.Fprintf(c.Out, "  %-18s %d\n", "Status:", event.Status)
	}
	fmt.Fprintf(c.Out, "  %-18s %dms\n", "Duration:", event.DurationMS)
	fmt.Fprintf(c.Out, "  %-18s %d / %d\n", "Bytes in / out:", event.RequestBytes, event.ResponseBytes)
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Fault:", emptyAs(event.Fault, "none"))
	fmt.Fprintf(c.Out, "  %-18s %s\n", "Recording:", emptyAs(event.Recording, "none"))
	if event.Error != "" {
		fmt.Fprintf(c.Out, "  %-18s %s\n", "Error:", c.failure(c.Out, event.Error))
	}
	printHeaderMap(c.Out, "Request headers", event.RequestHeaders)
	printHeaderMap(c.Out, "Response headers", event.ResponseHeaders)
}

func (c *CLI) printError(err error) {
	var clientErr *bootstrap.ClientError
	if c.jsonOutput {
		detail := errorDetail{Code: "COMMAND_FAILED", Message: err.Error()}
		var usage *commandUsageError
		if errors.As(err, &usage) || isCobraSyntaxError(err) {
			detail.Code = "USAGE_ERROR"
		}
		if errors.As(err, &clientErr) {
			detail = errorDetail{
				Code: clientErr.Code, Message: clientErr.Message, Status: clientErr.Status,
				Subject: clientErr.Subject, Details: clientErr.Details, Remediation: clientErr.Remediation,
			}
			if detail.Code == "" {
				detail.Code = "API_ERROR"
			}
		}
		_ = writeJSON(c.Err, errorOutput{Error: detail})
		return
	}
	if errors.As(err, &clientErr) {
		fmt.Fprintf(c.Err, "%s %s\n", c.failure(c.Err, "portless:"), clientErr.Message)
		if clientErr.Code != "" {
			fmt.Fprintf(c.Err, "%s %s\n", c.muted(c.Err, "code:"), clientErr.Code)
		}
		for _, remediation := range clientErr.Remediation {
			if command, ok := remediation["command"].(string); ok && command != "" {
				fmt.Fprintln(c.Err, c.accent(c.Err, "next:"), command)
			}
			if targetURL, ok := remediation["url"].(string); ok && targetURL != "" {
				fmt.Fprintln(c.Err, c.accent(c.Err, "inspect:"), targetURL)
			}
		}
		return
	}
	fmt.Fprintln(c.Err, c.failure(c.Err, "portless:"), err)
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
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSONLine(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeRelayStatusJSON(writer io.Writer, status ingress.InstallationStatus) error {
	return writeJSON(writer, relayStatusOutput{State: status.State(), InstallationStatus: status})
}

func (c *CLI) printEnvironmentListHeader() {
	fmt.Fprintln(c.Out, c.muted(c.Out, fmt.Sprintf("%-32s %-14s %s", "ENVIRONMENT", "STATE", "SERVICES")))
}

func (c *CLI) printWarnings(warnings []string) {
	if c.jsonOutput {
		return
	}
	for _, warning := range warnings {
		fmt.Fprintln(c.Err, c.warning(c.Err, "warning:"), warning)
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func emptyAs(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
