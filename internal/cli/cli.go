package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/bootstrap"
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
	Project  model.Project `json:"project"`
	Warnings []string      `json:"warnings"`
}

func New(out, errOut io.Writer, dataDirectory string) (*CLI, error) {
	paths, err := bootstrap.ResolvePaths(dataDirectory)
	if err != nil {
		return nil, err
	}
	return &CLI{Out: out, Err: errOut, paths: paths}, nil
}

func (c *CLI) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		c.help()
		return 0
	}
	var err error
	switch args[0] {
	case "help", "--help", "-h":
		c.help()
		return 0
	case "version", "--version", "-v":
		fmt.Fprintln(c.Out, "portless "+Version)
		return 0
	case "up":
		return c.up(ctx, args[1:])
	case "down":
		err = c.down(ctx, args[1:])
	case "status":
		err = c.status(ctx, args[1:])
	case "ui":
		err = c.ui(ctx, args[1:])
	case "open":
		err = c.open(ctx, args[1:])
	case "logs":
		err = c.logs(ctx, args[1:])
	case "traffic":
		err = c.traffic(ctx, args[1:])
	case "record":
		err = c.record(ctx, args[1:])
	case "fault":
		err = c.fault(ctx, args[1:])
	case "project":
		err = c.project(ctx, args[1:])
	case "runtime":
		err = c.runtime(ctx, args[1:])
	default:
		fmt.Fprintf(c.Err, "unknown command %q\n\n", args[0])
		c.help()
		return 2
	}
	if err != nil {
		c.printError(err)
		return 1
	}
	return 0
}

func (c *CLI) help() {
	fmt.Fprintln(c.Out, `Portless runs and observes a local application environment without a required config file.

Usage:
  portless up [--open|--no-open] [--name NAME]
  portless down [--volumes --yes]
  portless status [--json]
  portless open [service]
  portless ui
  portless logs <service> [--follow]
  portless traffic [service|source:target] [--follow]
  portless record start|stop|list|export|delete ...
  portless fault add|list|disable|clear ...
  portless project rescan|export|rename|forget ...
  portless runtime status|start
  portless runtime use auto|docker|podman
  portless version`)
}

func (c *CLI) up(ctx context.Context, args []string) int {
	set := flag.NewFlagSet("up", flag.ContinueOnError)
	set.SetOutput(c.Err)
	name := set.String("name", "", "machine-local project name")
	jsonOutput := set.Bool("json", false, "emit JSON Lines")
	timeout := set.Duration("timeout", 10*time.Minute, "startup timeout")
	forceOpen := set.Bool("open", false, "open the dashboard")
	noOpen := set.Bool("no-open", false, "do not open a browser")
	forceWait := set.Bool("wait", false, "wait for readiness")
	noWait := set.Bool("no-wait", false, "return after the operation is accepted")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *forceOpen && *noOpen {
		fmt.Fprintln(c.Err, "--open and --no-open cannot be used together")
		return 2
	}
	if *forceWait && *noWait {
		fmt.Fprintln(c.Err, "--wait and --no-wait cannot be used together")
		return 2
	}
	openBrowser := !*noOpen
	wait := !*noWait
	if set.NArg() > 0 {
		fmt.Fprintln(c.Err, "partial service startup is not implemented in the first runnable slice; run `portless up` for the project")
		return 2
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		c.printError(err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		c.printError(err)
		return 1
	}
	var discovered discoverResponse
	if err := client.Do(ctx, http.MethodPost, "/api/v1/projects/discover", map[string]any{"path": cwd, "name": *name}, &discovered); err != nil {
		c.printError(err)
		return 1
	}
	for _, warning := range discovered.Warnings {
		fmt.Fprintln(c.Err, "warning:", warning)
	}
	operationContext, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	var operation model.Operation
	idempotency := fmt.Sprintf("cli-up-%s-%d", discovered.Project.Name, time.Now().UTC().Unix()/30)
	if err := client.DoWithHeaders(operationContext, http.MethodPost, "/api/v1/projects/"+bootstrap.EscapePath(discovered.Project.Name)+"/up", nil, &operation, map[string]string{"Idempotency-Key": idempotency}); err != nil {
		c.printError(err)
		return 1
	}
	if !wait {
		c.printOperation(operation, *jsonOutput)
		return 0
	}
	operation, err = c.waitOperation(operationContext, client, operation, *jsonOutput)
	if err != nil {
		c.printError(err)
		return 1
	}
	if operation.State != "succeeded" {
		c.printError(errors.New(operation.Error))
		return 1
	}
	var project model.Project
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects/"+bootstrap.EscapePath(discovered.Project.Name), nil, &project); err != nil {
		c.printError(err)
		return 1
	}
	c.printStatus(project)
	if openBrowser {
		if url, err := c.browserURL(ctx, client, "/projects/"+project.Name); err == nil {
			fmt.Fprintln(c.Out, "Dashboard:", project.DashboardURL)
			_ = launchBrowser(url)
		}
	}
	return 0
}

func (c *CLI) down(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("down", flag.ContinueOnError)
	set.SetOutput(c.Err)
	volumes := set.Bool("volumes", false, "remove managed data volumes")
	yes := set.Bool("yes", false, "confirm volume deletion")
	wait := set.Bool("wait", true, "wait for shutdown")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *volumes && !*yes {
		return errors.New("--volumes permanently deletes managed database/cache data; repeat with --yes")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	var operation model.Operation
	if err := client.Do(ctx, http.MethodPost, "/api/v1/projects/"+bootstrap.EscapePath(project.Name)+"/down", map[string]any{"removeVolumes": *volumes}, &operation); err != nil {
		return err
	}
	if *wait {
		operation, err = c.waitOperation(ctx, client, operation, false)
		if err != nil {
			return err
		}
		if operation.State != "succeeded" {
			return errors.New(operation.Error)
		}
	}
	fmt.Fprintf(c.Out, "%s  stopped\n", project.Name)
	return nil
}

func (c *CLI) status(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("status", flag.ContinueOnError)
	set.SetOutput(c.Err)
	jsonOutput := set.Bool("json", false, "emit JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	project, err := c.findCurrent(ctx, client)
	if err == nil {
		if *jsonOutput {
			return writeJSON(c.Out, project)
		}
		c.printStatus(project)
		return nil
	}
	var response struct {
		Projects []model.Project `json:"projects"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects", nil, &response); err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(c.Out, response)
	}
	if len(response.Projects) == 0 {
		fmt.Fprintln(c.Out, "No projects yet. Run `portless up` in a Spring Boot or NestJS repository.")
		return nil
	}
	for _, item := range response.Projects {
		fmt.Fprintf(c.Out, "%-24s %-16s %s\n", item.Name, item.Status, item.Path)
	}
	return nil
}

func (c *CLI) ui(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errors.New("usage: portless ui")
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	next := "/projects"
	if project, err := c.findCurrent(ctx, client); err == nil {
		next = "/projects/" + project.Name
	}
	url, err := c.browserURL(ctx, client, next)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.Out, url)
	if err := launchBrowser(url); err != nil {
		fmt.Fprintln(c.Err, "Could not open a browser; use the URL above:", err)
	}
	return nil
}

func (c *CLI) open(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return errors.New("usage: portless open [service]")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	url := project.DashboardURL
	serviceName := project.PrimaryService
	if len(args) == 1 {
		serviceName = args[0]
	}
	if serviceName != "" {
		found := false
		for _, service := range project.Services {
			if service.Name == serviceName {
				found = true
				if service.IngressURL != "" {
					url = service.IngressURL
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("service %s was not found in project %s", serviceName, project.Name)
		}
	}
	if strings.Contains(url, "/projects/") {
		url, err = c.browserURL(ctx, client, "/projects/"+project.Name)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(c.Out, url)
	if err := launchBrowser(url); err != nil {
		fmt.Fprintln(c.Err, "Could not open a browser; use the URL above:", err)
	}
	return nil
}

func (c *CLI) logs(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("logs", flag.ContinueOnError)
	set.SetOutput(c.Err)
	follow := set.Bool("follow", false, "continue polling for new lines")
	jsonOutput := set.Bool("json", false, "emit JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 1 {
		return errors.New("usage: portless logs <service> [--follow]")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	service := set.Arg(0)
	seen := 0
	for {
		var response struct {
			Lines []string `json:"lines"`
		}
		path := "/api/v1/projects/" + bootstrap.EscapePath(project.Name) + "/logs?service=" + bootstrap.EscapePath(service) + "&limit=2000"
		if err := client.Do(ctx, http.MethodGet, path, nil, &response); err != nil {
			return err
		}
		if seen > len(response.Lines) {
			seen = 0
		}
		for _, line := range response.Lines[seen:] {
			if *jsonOutput {
				_ = writeJSON(c.Out, map[string]any{"project": project.Name, "service": service, "line": line})
			} else {
				fmt.Fprintln(c.Out, line)
			}
		}
		seen = len(response.Lines)
		if !*follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func (c *CLI) traffic(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("traffic", flag.ContinueOnError)
	set.SetOutput(c.Err)
	follow := set.Bool("follow", false, "stream live traffic")
	jsonOutput := set.Bool("json", false, "emit JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	selector := ""
	if set.NArg() == 1 {
		selector = set.Arg(0)
	} else if set.NArg() > 1 {
		return errors.New("usage: portless traffic [service|source:target] [--follow]")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	var response struct {
		Traffic []model.TrafficEvent `json:"traffic"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects/"+bootstrap.EscapePath(project.Name)+"/traffic/http?limit=250", nil, &response); err != nil {
		return err
	}
	for index := len(response.Traffic) - 1; index >= 0; index-- {
		if matchesTraffic(response.Traffic[index], selector) {
			c.printTraffic(response.Traffic[index], *jsonOutput)
		}
	}
	if !*follow {
		return nil
	}
	return c.followTraffic(ctx, client, project.Name, selector, *jsonOutput)
}

func (c *CLI) record(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: portless record start|stop|list|export|delete ...")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + bootstrap.EscapePath(project.Name) + "/recordings"
	switch args[0] {
	case "list":
		var response struct {
			Recordings []model.Recording `json:"recordings"`
		}
		if err := client.Do(ctx, http.MethodGet, base, nil, &response); err != nil {
			return err
		}
		for _, item := range response.Recordings {
			fmt.Fprintf(c.Out, "%-24s %-10s %d events  %s → %s\n", item.Name, item.Status, item.EventCount, emptyAs(item.Source, "any"), emptyAs(item.Target, "any"))
		}
		return nil
	case "start":
		set := flag.NewFlagSet("record start", flag.ContinueOnError)
		set.SetOutput(c.Err)
		edge := set.String("edge", "", "source:target scope")
		duration := set.Duration("duration", 15*time.Minute, "automatic stop time")
		maxEvents := set.Int64("max-events", 10000, "maximum retained events")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 1 {
			return errors.New("usage: portless record start <name> [--edge source:target]")
		}
		source, target, err := parseEdge(*edge)
		if err != nil {
			return err
		}
		expires := time.Now().UTC().Add(*duration)
		input := model.Recording{Name: set.Arg(0), Source: source, Target: target, MaxEvents: *maxEvents, ExpiresAt: &expires}
		var created model.Recording
		if err := client.Do(ctx, http.MethodPost, base, input, &created); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "recording %s started; expires %s\n", created.Name, created.ExpiresAt.Format(time.RFC3339))
		return nil
	case "stop":
		name, err := recordingName(ctx, client, project.Name, args[1:])
		if err != nil {
			return err
		}
		if err := client.Do(ctx, http.MethodPost, base+"/"+bootstrap.EscapePath(name)+"/stop", nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.Out, "recording", name, "stopped")
		return nil
	case "export":
		if len(args) != 2 {
			return errors.New("usage: portless record export <name>")
		}
		var content []byte
		if err := client.Do(ctx, http.MethodGet, base+"/"+bootstrap.EscapePath(args[1])+"/export", nil, &content); err != nil {
			return err
		}
		_, err := c.Out.Write(content)
		return err
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: portless record delete <name>")
		}
		return client.Do(ctx, http.MethodDelete, base+"/"+bootstrap.EscapePath(args[1]), nil, nil)
	default:
		return fmt.Errorf("unknown record command %q", args[0])
	}
}

func (c *CLI) fault(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: portless fault add|list|disable|clear ...")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + bootstrap.EscapePath(project.Name) + "/faults"
	switch args[0] {
	case "list":
		var response struct {
			Faults []model.FaultRule `json:"faults"`
		}
		if err := client.Do(ctx, http.MethodGet, base, nil, &response); err != nil {
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
	case "add":
		set := flag.NewFlagSet("fault add", flag.ContinueOnError)
		set.SetOutput(c.Err)
		latency := set.Int64("latency", 0, "latency in milliseconds")
		jitter := set.Int64("jitter", 0, "maximum jitter in milliseconds")
		status := set.Int("status", 0, "synthetic HTTP status")
		abort := set.Bool("abort", false, "abort matching connections")
		probability := set.Float64("probability", 1, "match probability from 0 to 1")
		method := set.String("method", "", "HTTP method filter")
		path := set.String("path", "", "HTTP path glob")
		duration := set.Duration("duration", 10*time.Minute, "automatic expiry")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 2 {
			return errors.New("usage: portless fault add <name> <source:target> [effects]")
		}
		source, target, err := parseEdge(set.Arg(1))
		if err != nil || source == "" || target == "" {
			return errors.New("edge must be source:target")
		}
		expires := time.Now().UTC().Add(*duration)
		input := model.FaultRule{Name: set.Arg(0), Source: source, Target: target, LatencyMS: *latency, JitterMS: *jitter, StatusCode: *status, Abort: *abort, Probability: *probability, Method: *method, Path: *path, ExpiresAt: &expires}
		var created model.FaultRule
		if err := client.Do(ctx, http.MethodPost, base, input, &created); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "fault %s active: %s\n", created.Name, created.ScopeSummary)
		return nil
	case "disable":
		if len(args) != 2 {
			return errors.New("usage: portless fault disable <name>")
		}
		if err := client.Do(ctx, http.MethodDelete, base+"/"+bootstrap.EscapePath(args[1]), nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.Out, "fault", args[1], "disabled")
		return nil
	case "clear":
		if len(args) > 2 || len(args) == 2 && args[1] != "--all" {
			return errors.New("usage: portless fault clear [--all]")
		}
		var result map[string]any
		if err := client.Do(ctx, http.MethodPost, base+"/disable-all", nil, &result); err != nil {
			return err
		}
		fmt.Fprintln(c.Out, "all active faults disabled")
		return nil
	default:
		return fmt.Errorf("unknown fault command %q", args[0])
	}
}

func (c *CLI) project(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: portless project rescan|export|rename|forget")
	}
	client, project, err := c.current(ctx)
	if err != nil {
		return err
	}
	base := "/api/v1/projects/" + bootstrap.EscapePath(project.Name)
	switch args[0] {
	case "rescan":
		var response discoverResponse
		if err := client.Do(ctx, http.MethodPost, base+"/rescan", nil, &response); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "%s rescanned (revision %d)\n", response.Project.Name, response.Project.Revision)
		return nil
	case "export":
		set := flag.NewFlagSet("project export", flag.ContinueOnError)
		set.SetOutput(c.Err)
		output := set.String("output", "portless.project.json", "output path, or - for stdout")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		var content []byte
		if err := client.Do(ctx, http.MethodGet, base+"/declaration", nil, &content); err != nil {
			return err
		}
		if *output == "-" {
			_, err = c.Out.Write(content)
			return err
		}
		if err := os.WriteFile(*output, content, 0o600); err != nil {
			return err
		}
		fmt.Fprintln(c.Out, "wrote", *output)
		return nil
	case "rename":
		if len(args) != 2 {
			return errors.New("usage: portless project rename <new-name>")
		}
		var renamed model.Project
		if err := client.Do(ctx, http.MethodPatch, base, map[string]any{"name": args[1], "revision": project.Revision}, &renamed); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "%s renamed to %s\n", project.Name, renamed.Name)
		return nil
	case "forget":
		if len(args) != 2 || args[1] != "--yes" {
			return errors.New("forget removes Portless metadata; repeat as `portless project forget --yes`")
		}
		if err := client.Do(ctx, http.MethodDelete, base, nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.Out, "forgot", project.Name)
		return nil
	default:
		return fmt.Errorf("unknown project command %q", args[0])
	}
}

func (c *CLI) runtime(ctx context.Context, args []string) error {
	if len(args) == 0 || len(args) > 2 {
		return errors.New("usage: portless runtime status|start|use auto|docker|podman")
	}
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return err
	}
	var status map[string]any
	method, path, body := http.MethodGet, "/api/v1/runtime", any(nil)
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: portless runtime status")
		}
	case "start":
		if len(args) != 1 {
			return errors.New("usage: portless runtime start")
		}
		method, path = http.MethodPost, "/api/v1/runtime/start"
	case "use":
		if len(args) != 2 || (args[1] != "auto" && args[1] != "docker" && args[1] != "podman") {
			return errors.New("usage: portless runtime use auto|docker|podman")
		}
		method, body = http.MethodPut, map[string]string{"preference": args[1]}
	default:
		return errors.New("usage: portless runtime status|start|use auto|docker|podman")
	}
	if err := client.Do(ctx, method, path, body, &status); err != nil {
		return err
	}
	return writeJSON(c.Out, status)
}

func (c *CLI) current(ctx context.Context) (*bootstrap.Client, model.Project, error) {
	client, _, err := bootstrap.Connect(ctx, c.paths)
	if err != nil {
		return nil, model.Project{}, err
	}
	project, err := c.findCurrent(ctx, client)
	if err != nil {
		return nil, model.Project{}, err
	}
	return client, project, nil
}

func (c *CLI) findCurrent(ctx context.Context, client *bootstrap.Client) (model.Project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return model.Project{}, err
	}
	root, err := discovery.FindRoot(cwd)
	if err != nil {
		return model.Project{}, err
	}
	root, _ = filepath.EvalSymlinks(root)
	var response struct {
		Projects []model.Project `json:"projects"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects", nil, &response); err != nil {
		return model.Project{}, err
	}
	for _, item := range response.Projects {
		if item.Path == root {
			return item, nil
		}
	}
	return model.Project{}, errors.New("this checkout is not known to Portless; run `portless up`")
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

func (c *CLI) waitOperation(ctx context.Context, client *bootstrap.Client, operation model.Operation, jsonOutput bool) (model.Operation, error) {
	seen := 0
	for {
		if err := client.Do(ctx, http.MethodGet, "/api/v1/projects/"+bootstrap.EscapePath(operation.Project)+"/operations/"+strconv.FormatInt(operation.Number, 10), nil, &operation); err != nil {
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

func (c *CLI) followTraffic(ctx context.Context, client *bootstrap.Client, project, selector string, jsonOutput bool) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL+"/api/v1/projects/"+bootstrap.EscapePath(project)+"/stream?topic=traffic.http", nil)
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

func (c *CLI) printStatus(project model.Project) {
	ready := 0
	for _, service := range project.Services {
		if service.Status == model.ServiceReady {
			ready++
		}
	}
	fmt.Fprintf(c.Out, "%s  %s  %d/%d ready\n\n", project.Name, project.Status, ready, len(project.Services))
	fmt.Fprintln(c.Out, "SERVICE                 KIND        STATE          ENDPOINT")
	for _, service := range project.Services {
		kind := string(service.Kind)
		if service.Framework != "" {
			kind = service.Framework
		} else if service.Template != "" {
			kind = service.Template
		}
		fmt.Fprintf(c.Out, "%-23s %-11s %-14s %s\n", service.Name, kind, service.Status, service.IngressURL)
	}
	fmt.Fprintln(c.Out, "\nDashboard:", project.DashboardURL)
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
			if url, ok := remediation["url"].(string); ok && url != "" {
				fmt.Fprintln(c.Err, "inspect:", url)
			}
		}
		return
	}
	fmt.Fprintln(c.Err, "portless:", err)
}

func launchBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "linux":
		command = exec.Command("xdg-open", url)
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

func recordingName(ctx context.Context, client *bootstrap.Client, project string, arguments []string) (string, error) {
	if len(arguments) == 1 {
		return arguments[0], nil
	}
	if len(arguments) > 1 {
		return "", errors.New("usage: portless record stop [name]")
	}
	var response struct {
		Recordings []model.Recording `json:"recordings"`
	}
	if err := client.Do(ctx, http.MethodGet, "/api/v1/projects/"+bootstrap.EscapePath(project)+"/recordings", nil, &response); err != nil {
		return "", err
	}
	for _, item := range response.Recordings {
		if item.Status == "active" {
			return item.Name, nil
		}
	}
	return "", errors.New("no active recording")
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
