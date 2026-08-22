//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestCLIMultipleSourcesAndMixedProviderEnvironment(t *testing.T) {
	binary := e2eBinary(t)
	home, sources := isolatedMultiSourceFixture(t)
	checkout := sources["checkout"]
	defer cleanupInstallation(t, binary, home, checkout)

	createOutput, err := runCLIAt(binary, home, checkout,
		"--json", "project", "create", "distributed-store",
		"--source", "checkout="+sources["checkout"],
		"--source", "inventory="+sources["inventory"],
		"--source", "orders="+sources["orders"],
	)
	if err != nil {
		t.Fatalf("create multi-source project: %v\n%s\ndaemon log:\n%s", err, createOutput, readDaemonLog(home))
	}
	var created struct {
		Project     model.Project     `json:"project"`
		Environment model.Environment `json:"environment"`
	}
	if err := json.Unmarshal([]byte(createOutput), &created); err != nil {
		t.Fatalf("decode project creation: %v\n%s", err, createOutput)
	}
	assertProjectSources(t, created.Project, "checkout", "inventory", "orders")
	assertSourcePaths(t, created.Environment, sources, "checkout", "inventory", "orders")

	if output, err := runCLIAt(binary, home, checkout, "env", "clone", "qa-assisted"); err != nil {
		t.Fatalf("clone environment: %v\n%s", err, output)
	}

	addOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/local", "--json", "project", "source", "add", "catalog", "--path", sources["catalog"],
	)
	if err != nil {
		t.Fatalf("add source to existing project: %v\n%s", err, addOutput)
	}
	var added struct {
		Project               model.Project     `json:"project"`
		Environment           model.Environment `json:"environment"`
		ConfigurationRequired []string          `json:"configurationRequired"`
	}
	if err := json.Unmarshal([]byte(addOutput), &added); err != nil {
		t.Fatalf("decode source addition: %v\n%s", err, addOutput)
	}
	assertProjectSources(t, added.Project, "catalog", "checkout", "inventory", "orders")
	assertSourcePaths(t, added.Environment, sources, "catalog", "checkout", "inventory", "orders")
	if strings.Join(added.ConfigurationRequired, ",") != "distributed-store/qa-assisted" {
		t.Fatalf("configuration required = %v, want distributed-store/qa-assisted", added.ConfigurationRequired)
	}

	qaBefore := explicitEnvironmentStatus(t, binary, home, checkout, "distributed-store/qa-assisted")
	if !hasConfigurationIssue(qaBefore.Issues, "MISSING_BINDING", "catalog") {
		t.Fatalf("cloned environment did not expose its missing catalog binding: %#v", qaBefore.Issues)
	}
	if output, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "env", "checkout", "set", "catalog", "--path", sources["catalog"],
	); err != nil {
		t.Fatalf("bind new source in cloned environment: %v\n%s", err, output)
	}

	var remoteInventoryRequests atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/health":
			_, _ = io.WriteString(writer, `{"service":"qa-inventory","ready":true}`)
		case strings.HasPrefix(request.URL.Path, "/inventory/"):
			remoteInventoryRequests.Add(1)
			_, _ = io.WriteString(writer, `{"available":true,"provider":"qa","sku":"coffee-mug"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer remote.Close()

	bindOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "env", "bind", "inventory",
		"--remote", remote.URL, "--classification", "qa", "--write-policy", "read-only", "--health-path", "/health",
	)
	if err != nil {
		t.Fatalf("bind QA inventory: %v\n%s", err, bindOutput)
	}
	if !strings.Contains(bindOutput, "remote "+remote.URL+" (qa, read-only)") {
		t.Fatalf("remote binding output was not human-readable:\n%s", bindOutput)
	}

	upOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "up", "--managed", "--no-open", "--timeout", "2m",
	)
	if err != nil {
		t.Fatalf("start mixed-provider environment: %v\n%s\ndaemon log:\n%s", err, upOutput, readDaemonLog(home))
	}
	qa := explicitEnvironmentStatus(t, binary, home, checkout, "distributed-store/qa-assisted")
	if qa.Status != model.EnvironmentHealthy || len(qa.Services) != 4 || len(qa.Issues) != 0 {
		t.Fatalf("mixed-provider environment was not healthy: %#v", qa)
	}
	assertSourcePaths(t, qa, sources, "catalog", "checkout", "inventory", "orders")
	inventoryBinding := bindingByService(qa.Bindings, "inventory")
	if inventoryBinding == nil || inventoryBinding.Provider != model.ProviderRemote || inventoryBinding.Remote == nil ||
		inventoryBinding.Remote.URL != remote.URL || inventoryBinding.Remote.Classification != model.RemoteQA || inventoryBinding.Remote.WritePolicy != model.WriteReadOnly {
		t.Fatalf("unexpected inventory provider binding: %#v", inventoryBinding)
	}

	response := applicationRequest(t, home, "checkout.qa-assisted.distributed-store.localhost", "/checkout?sku=coffee-mug&quantity=2", nil)
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"provider":"qa"`) || remoteInventoryRequests.Load() != 1 {
		t.Fatalf("checkout did not use the QA inventory provider: status=%s requests=%d\n%s", response.Status, remoteInventoryRequests.Load(), body)
	}

	trafficOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "--json", "traffic", "list", "--edge", "checkout:inventory", "--limit", "20",
	)
	if err != nil {
		t.Fatalf("list mixed-provider traffic: %v\n%s", err, trafficOutput)
	}
	var traffic struct {
		Exchanges []model.TrafficExchange `json:"exchanges"`
	}
	if err := json.Unmarshal([]byte(trafficOutput), &traffic); err != nil || len(traffic.Exchanges) != 1 {
		t.Fatalf("decode mixed-provider traffic: err=%v output=%s", err, trafficOutput)
	}
	if traffic.Exchanges[0].TargetProvider != model.ProviderRemote || traffic.Exchanges[0].RemoteClassification != model.RemoteQA {
		t.Fatalf("traffic lost remote provider metadata: %#v", traffic.Exchanges[0])
	}

	connectionOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "--json", "connection", "show", "checkout:inventory",
	)
	if err != nil {
		t.Fatalf("show remote connection: %v\n%s", err, connectionOutput)
	}
	var connection model.EffectiveConnection
	if err := json.Unmarshal([]byte(connectionOutput), &connection); err != nil || connection.Endpoint == nil {
		t.Fatalf("decode active remote connection: err=%v connection=%#v", err, connection)
	}
	blockedRequest, err := http.NewRequest(http.MethodPost, connection.Endpoint.URL+"/inventory/coffee-mug", strings.NewReader(`{"quantity":2}`))
	if err != nil {
		t.Fatal(err)
	}
	blockedResponse, err := (&http.Client{}).Do(blockedRequest)
	if err != nil {
		t.Fatalf("send write through read-only edge: %v", err)
	}
	blockedBody, err := io.ReadAll(blockedResponse.Body)
	blockedResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if blockedResponse.StatusCode != http.StatusForbidden || blockedResponse.Header.Get("X-Portless-Remote-Policy") != "read-only" ||
		!strings.Contains(string(blockedBody), "remote target is read-only") || remoteInventoryRequests.Load() != 1 {
		t.Fatalf("read-only remote policy was not enforced: status=%s headers=%v requests=%d body=%s", blockedResponse.Status, blockedResponse.Header, remoteInventoryRequests.Load(), blockedBody)
	}

	stableRuntime := map[string]model.Service{}
	for _, service := range qa.Services {
		if service.Name == "checkout" || service.Name == "orders" {
			stableRuntime[service.Name] = service
		}
	}
	localOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "env", "bind", "inventory", "--local", "inventory",
	)
	if err != nil {
		t.Fatalf("switch active inventory to local: %v\n%s", err, localOutput)
	}
	if !strings.Contains(localOutput, "Starting inventory from inventory") || !strings.Contains(localOutput, "now uses local source inventory") {
		t.Fatalf("active provider progress was not human-readable:\n%s", localOutput)
	}
	localEnvironment := explicitEnvironmentStatus(t, binary, home, checkout, "distributed-store/qa-assisted")
	if binding := bindingByService(localEnvironment.Bindings, "inventory"); binding == nil || binding.Provider != model.ProviderLocal || binding.Source != "inventory" {
		t.Fatalf("active local inventory binding = %#v", binding)
	}
	assertUnchangedServices(t, stableRuntime, localEnvironment, "checkout", "orders")

	remoteOutput, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "env", "bind", "inventory",
		"--remote", remote.URL, "--classification", "qa", "--write-policy", "read-only", "--health-path", "/health",
	)
	if err != nil {
		t.Fatalf("switch active inventory back to QA: %v\n%s", err, remoteOutput)
	}
	if !strings.Contains(remoteOutput, "Remote target passed preflight") || !strings.Contains(remoteOutput, "now uses remote "+remote.URL) {
		t.Fatalf("active remote provider progress was not human-readable:\n%s", remoteOutput)
	}
	restoredRemote := explicitEnvironmentStatus(t, binary, home, checkout, "distributed-store/qa-assisted")
	if binding := bindingByService(restoredRemote.Bindings, "inventory"); binding == nil || binding.Provider != model.ProviderRemote || binding.Remote == nil || binding.Remote.URL != remote.URL {
		t.Fatalf("restored remote inventory binding = %#v", binding)
	}
	assertUnchangedServices(t, stableRuntime, restoredRemote, "checkout", "orders")
	if output, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/qa-assisted", "down"); err != nil {
		t.Fatalf("stop mixed-provider environment before source deletion: %v\n%s", err, output)
	}
	checkoutListOutput, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/qa-assisted", "--json", "env", "checkout", "list")
	if err != nil {
		t.Fatalf("list environment checkouts: %v\n%s", err, checkoutListOutput)
	}
	var checkoutList struct {
		Project     string                `json:"project"`
		Environment string                `json:"environment"`
		Checkouts   []model.SourceBinding `json:"checkouts"`
	}
	if err := json.Unmarshal([]byte(checkoutListOutput), &checkoutList); err != nil || checkoutList.Project != "distributed-store" || checkoutList.Environment != "qa-assisted" || len(checkoutList.Checkouts) != 4 {
		t.Fatalf("environment checkout list: err=%v value=%#v output=%s", err, checkoutList, checkoutListOutput)
	}
	blockedRemove, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/qa-assisted", "env", "checkout", "remove", "catalog", "--yes")
	if err == nil || !strings.Contains(blockedRemove, "source checkout catalog is used by checkout providers for: catalog") || !strings.Contains(blockedRemove, "CHECKOUT_IN_USE") {
		t.Fatalf("checkout removal did not protect its local provider: err=%v\n%s", err, blockedRemove)
	}
	if output, err := runCLIAt(binary, home, checkout,
		"--env", "distributed-store/qa-assisted", "env", "bind", "catalog",
		"--remote", remote.URL, "--classification", "qa", "--write-policy", "read-only", "--health-path", "/health",
	); err != nil {
		t.Fatalf("switch catalog away from its checkout: %v\n%s", err, output)
	}
	removeOutput, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/qa-assisted", "--json", "env", "checkout", "remove", "catalog", "--yes")
	if err != nil {
		t.Fatalf("remove environment checkout: %v\n%s", err, removeOutput)
	}
	var removedCheckout struct {
		Environment model.Environment `json:"environment"`
		Warnings    []string          `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(removeOutput), &removedCheckout); err != nil {
		t.Fatalf("decode checkout removal: %v\n%s", err, removeOutput)
	}
	assertSourcePaths(t, removedCheckout.Environment, sources, "checkout", "inventory", "orders")
	setCompletion, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/qa-assisted", "__complete", "env", "checkout", "set", "cat")
	if err != nil || !strings.Contains(setCompletion, "catalog") {
		t.Fatalf("project source was not offered for checkout configuration: err=%v\n%s", err, setCompletion)
	}
	removeCompletion, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/qa-assisted", "__complete", "env", "checkout", "remove", "cat")
	if err != nil || strings.Contains(removeCompletion, "catalog") {
		t.Fatalf("removed checkout was still offered for checkout removal: err=%v\n%s", err, removeCompletion)
	}
	projectBeforeDeleteOutput, err := runCLIAt(binary, home, checkout, "--json", "project", "show", "distributed-store")
	if err != nil {
		t.Fatalf("show project after environment checkout removal: %v\n%s", err, projectBeforeDeleteOutput)
	}
	var projectBeforeDelete model.Project
	if err := json.Unmarshal([]byte(projectBeforeDeleteOutput), &projectBeforeDelete); err != nil {
		t.Fatalf("decode project after environment checkout removal: %v\n%s", err, projectBeforeDeleteOutput)
	}
	assertProjectSources(t, projectBeforeDelete, "catalog", "checkout", "inventory", "orders")

	deleteOutput, err := runCLIAt(binary, home, checkout, "--env", "distributed-store/local", "--json", "project", "source", "delete", "catalog", "--yes")
	if err != nil {
		t.Fatalf("delete source from existing project: %v\n%s", err, deleteOutput)
	}
	var deleted struct {
		Project         model.Project       `json:"project"`
		Environments    []model.Environment `json:"environments"`
		RemovedServices []string            `json:"removedServices"`
	}
	if err := json.Unmarshal([]byte(deleteOutput), &deleted); err != nil {
		t.Fatalf("decode source deletion: %v\n%s", err, deleteOutput)
	}
	assertProjectSources(t, deleted.Project, "checkout", "inventory", "orders")
	if strings.Join(deleted.RemovedServices, ",") != "catalog" || len(deleted.Environments) != 2 {
		t.Fatalf("source deletion = %#v", deleted)
	}

	projectOutput, err := runCLIAt(binary, home, checkout, "--json", "project", "show", "distributed-store")
	if err != nil {
		t.Fatalf("show final project: %v\n%s", err, projectOutput)
	}
	var project model.Project
	if err := json.Unmarshal([]byte(projectOutput), &project); err != nil {
		t.Fatalf("decode final project: %v\n%s", err, projectOutput)
	}
	assertProjectSources(t, project, "checkout", "inventory", "orders")
	if len(project.Environments) != 2 {
		t.Fatalf("project environments = %d, want 2: %#v", len(project.Environments), project.Environments)
	}
}

func assertUnchangedServices(t *testing.T, before map[string]model.Service, after model.Environment, names ...string) {
	t.Helper()
	for _, name := range names {
		var current model.Service
		for _, service := range after.Services {
			if service.Name == name {
				current = service
				break
			}
		}
		previous := before[name]
		if current.PID != previous.PID || current.Generation != previous.Generation || current.Status != model.ServiceReady {
			t.Fatalf("unrelated service %s changed: before=%#v after=%#v", name, previous, current)
		}
	}
}

func isolatedMultiSourceFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "portless-multi-source-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := e2eRepositoryPath(t, "tests", "fixtures", "store-lite", "apps")
	sources := make(map[string]string, 4)
	for _, name := range []string{"checkout", "inventory", "orders", "catalog"} {
		sourceService := name
		if name == "catalog" {
			sourceService = "inventory"
		}
		path := filepath.Join(root, name)
		if err := copyDirectory(filepath.Join(fixture, sourceService), path); err != nil {
			t.Fatal(err)
		}
		module := fmt.Sprintf("module example.com/%s\n\ngo 1.26\n", name)
		if err := os.WriteFile(filepath.Join(path, "go.mod"), []byte(module), 0o644); err != nil {
			t.Fatal(err)
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = canonical
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return home, sources
}

func explicitEnvironmentStatus(t *testing.T, binary, home, checkout, selector string) model.Environment {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--env", selector, "--json", "status")
	if err != nil {
		t.Fatalf("environment status for %s: %v\n%s", selector, err, output)
	}
	var environment model.Environment
	if err := json.Unmarshal([]byte(output), &environment); err != nil {
		t.Fatalf("decode environment status for %s: %v\n%s", selector, err, output)
	}
	return environment
}

func assertProjectSources(t *testing.T, project model.Project, expected ...string) {
	t.Helper()
	names := make([]string, 0, len(project.Sources))
	for _, source := range project.Sources {
		names = append(names, source.Name)
		if len(source.Services) != 1 || source.Services[0] != source.Name {
			t.Fatalf("source %s owns unexpected services: %v", source.Name, source.Services)
		}
	}
	sort.Strings(names)
	sort.Strings(expected)
	if strings.Join(names, ",") != strings.Join(expected, ",") {
		t.Fatalf("project sources = %v, want %v", names, expected)
	}
}

func assertSourcePaths(t *testing.T, environment model.Environment, expected map[string]string, names ...string) {
	t.Helper()
	actual := make(map[string]string, len(environment.Sources))
	for _, source := range environment.Sources {
		actual[source.Name] = source.Path
	}
	for _, name := range names {
		if actual[name] != expected[name] {
			t.Fatalf("%s source path = %q, want %q (all sources: %v)", name, actual[name], expected[name], actual)
		}
	}
	if len(actual) != len(names) {
		t.Fatalf("environment has %d source bindings, want %d: %v", len(actual), len(names), actual)
	}
}

func hasConfigurationIssue(issues []model.ConfigurationIssue, code, subject string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Subject == subject {
			return true
		}
	}
	return false
}

func bindingByService(bindings []model.ComponentBinding, service string) *model.ComponentBinding {
	for index := range bindings {
		if bindings[index].Service == service {
			return &bindings[index]
		}
	}
	return nil
}
