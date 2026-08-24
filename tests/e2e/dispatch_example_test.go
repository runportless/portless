//go:build e2e

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

const dispatchExampleE2EEnvironment = "PORTLESS_DISPATCH_EXAMPLE_E2E"

func TestDispatchExampleEndToEnd(t *testing.T) {
	if os.Getenv(dispatchExampleE2EEnvironment) != "1" {
		t.Skip(dispatchExampleE2EEnvironment + "=1 is required for the dependency-backed Dispatch example")
	}
	binary := e2eBinary(t)
	home, sources := dispatchExampleFixture(t)
	checkout := sources["console"]
	defer cleanupInstallation(t, binary, home, checkout)

	createOutput, err := runCLIAt(binary, home, checkout,
		"--json", "project", "create", "dispatch-example",
		"--source", "console="+sources["console"],
		"--source", "operations="+sources["operations"],
		"--source", "maps="+sources["maps"],
	)
	if err != nil {
		t.Fatalf("create Dispatch project: %v\n%s\ndaemon log:\n%s", err, createOutput, readDaemonLog(home))
	}
	var created struct {
		Project     model.Project     `json:"project"`
		Environment model.Environment `json:"environment"`
	}
	if err := json.Unmarshal([]byte(createOutput), &created); err != nil {
		t.Fatalf("decode Dispatch project creation: %v\n%s", err, createOutput)
	}
	if len(created.Project.Sources) != 3 || len(created.Environment.Services) != 7 {
		t.Fatalf("unexpected compiled Dispatch topology: project=%#v environment=%#v", created.Project, created.Environment)
	}
	assertSourcePaths(t, created.Environment, sources, "console", "operations", "maps")
	selectRequestedRuntime(t, binary, home, checkout)

	upOutput, err := runCLIAt(binary, home, checkout,
		"--env", "dispatch-example/local", "up", "--managed", "--no-open", "--timeout", "4m",
	)
	if err != nil {
		t.Fatalf("start Dispatch example: %v\n%s\ndaemon log:\n%s", err, upOutput, readDaemonLog(home))
	}
	environment := explicitEnvironmentStatus(t, binary, home, checkout, "dispatch-example/local")
	if environment.Status != model.EnvironmentHealthy || environment.PrimaryService != "console" || len(environment.Services) != 7 {
		t.Fatalf("Dispatch environment is not healthy: %#v", environment)
	}
	for _, service := range environment.Services {
		if service.Status != model.ServiceReady {
			t.Fatalf("Dispatch service %s is %s: %#v", service.Name, service.Status, service)
		}
	}

	host := "console.local.dispatch-example.localhost"
	traceHeaders := map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	waitForDispatchReady(t, home, host)
	assertDispatchJSON(t, home, host, http.MethodGet, "/dispatch/locations?query=depot", "", traceHeaders, http.StatusOK, `"central-depot"`)
	estimateBody := assertDispatchJSON(
		t, home, host, http.MethodGet,
		"/dispatch/estimates?pickup=central-depot&destination=harbor&size=medium&priority=standard",
		"", traceHeaders, http.StatusOK, `"strategy":"standard"`,
	)
	var estimate struct {
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal(estimateBody, &estimate); err != nil || estimate.Strategy != "standard" {
		t.Fatalf("decode standard route estimate: err=%v body=%s", err, estimateBody)
	}

	deliveryBody := assertDispatchJSON(
		t, home, host, http.MethodPost, "/dispatch/deliveries",
		`{"pickup":"central-depot","destination":"harbor","parcelSize":"medium","priority":"standard"}`,
		map[string]string{
			"content-type": "application/json",
			"traceparent":  traceHeaders["traceparent"],
		},
		http.StatusCreated, `"status":"scheduled"`,
	)
	var delivery struct {
		ID            string `json:"id"`
		RouteStrategy string `json:"routeStrategy"`
	}
	if err := json.Unmarshal(deliveryBody, &delivery); err != nil || !strings.HasPrefix(delivery.ID, "D-") || delivery.RouteStrategy != "standard" {
		t.Fatalf("decode scheduled delivery: err=%v delivery=%#v body=%s", err, delivery, deliveryBody)
	}
	waitForDispatchEvent(t, home, host, delivery.ID)

	trafficOutput, err := runCLIAt(binary, home, checkout,
		"--env", "dispatch-example/local", "--json", "traffic", "list", "--protocol", "http", "--limit", "200",
	)
	if err != nil {
		t.Fatalf("list Dispatch traffic: %v\n%s", err, trafficOutput)
	}
	var traffic struct {
		Exchanges []model.TrafficExchange `json:"exchanges"`
	}
	if err := json.Unmarshal([]byte(trafficOutput), &traffic); err != nil {
		t.Fatalf("decode Dispatch traffic: %v\n%s", err, trafficOutput)
	}
	edges := make(map[string]bool)
	for _, exchange := range traffic.Exchanges {
		edges[exchange.Source+":"+exchange.Target] = true
	}
	for _, edge := range []string{"console:api", "console:notifier", "api:geocoder", "api:routing", "routing:geocoder"} {
		if !edges[edge] {
			t.Fatalf("Dispatch traffic does not contain %s: %#v", edge, traffic.Exchanges)
		}
	}
}

func waitForDispatchReady(t *testing.T, home, host string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastStatus string
	var lastBody []byte
	for time.Now().Before(deadline) {
		response := applicationRequest(t, home, host, "/health", nil)
		encoded, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		lastStatus, lastBody = response.Status, encoded
		if response.StatusCode == http.StatusOK && strings.Contains(string(encoded), `"service":"console"`) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Dispatch console did not become HTTP-ready: %s\n%s", lastStatus, lastBody)
}

func dispatchExampleFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "portless-dispatch-example-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	templates := e2eRepositoryPath(t, "examples", "dispatch", "templates")
	sources := make(map[string]string, 3)
	for _, source := range []string{"console", "operations", "maps"} {
		path, err := filepath.EvalSymlinks(filepath.Join(templates, source))
		if err != nil {
			t.Fatal(err)
		}
		sources[source] = path
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return home, sources
}

func assertDispatchJSON(t *testing.T, home, host, method, path, body string, headers map[string]string, status int, contains string) []byte {
	t.Helper()
	response := applicationRequestWithMethod(t, home, host, method, path, body, headers)
	encoded, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || !strings.Contains(string(encoded), contains) {
		t.Fatalf("Dispatch %s %s = %s, want %d and %q\n%s", method, path, response.Status, status, contains, encoded)
	}
	return encoded
}

func waitForDispatchEvent(t *testing.T, home, host, deliveryID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		response := applicationRequest(t, home, host, "/dispatch/events", nil)
		encoded, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		last = encoded
		if response.StatusCode == http.StatusOK && strings.Contains(string(encoded), `"deliveryId":"`+deliveryID+`"`) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("notifier did not receive the NATS event for %s\n%s", deliveryID, last)
}
