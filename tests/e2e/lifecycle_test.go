//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func TestCLIZeroConfigurationLifecycle(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	upOutput, err := runCLIAt(binary, home, checkout, "up", "--name", "store-e2e", "--no-open", "--timeout", "2m")
	if err != nil {
		t.Fatalf("portless up failed: %v\n%s\ndaemon log:\n%s", err, upOutput, readDaemonLog(home))
	}
	for _, expected := range []string{
		"store-e2e/local", "healthy", "checkout", "inventory", "orders",
		"http://checkout.local.store-e2e.localhost",
	} {
		if !strings.Contains(upOutput, expected) {
			t.Fatalf("up output does not contain %q:\n%s", expected, upOutput)
		}
	}

	statusOutput, err := runCLIAt(binary, home, checkout, "--json", "status")
	if err != nil {
		t.Fatalf("JSON status failed: %v\n%s", err, statusOutput)
	}
	var environment model.Environment
	if err := json.Unmarshal([]byte(statusOutput), &environment); err != nil {
		t.Fatalf("decode JSON status: %v\n%s", err, statusOutput)
	}
	assertReadyStoreLite(t, environment)

	response := applicationRequest(t, home, "checkout.local.store-e2e.localhost", "/checkout?sku=coffee-mug&quantity=2", map[string]string{
		"Authorization": "Bearer e2e-secret",
		"X-E2E-Trace":   "visible",
	})
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"checkout":"accepted"`) || !strings.Contains(string(body), `"state":"created"`) {
		t.Fatalf("checkout response = %s\n%s", response.Status, body)
	}

	trafficOutput, err := runCLIAt(binary, home, checkout, "--json", "traffic", "list", "--edge", "external:checkout", "--limit", "20")
	if err != nil {
		t.Fatalf("list external traffic: %v\n%s", err, trafficOutput)
	}
	var traffic struct {
		Project     string               `json:"project"`
		Environment string               `json:"environment"`
		Traffic     []model.TrafficEvent `json:"traffic"`
	}
	if err := json.Unmarshal([]byte(trafficOutput), &traffic); err != nil {
		t.Fatalf("decode traffic: %v\n%s", err, trafficOutput)
	}
	if traffic.Project != "store-e2e" || traffic.Environment != "local" || len(traffic.Traffic) != 1 {
		t.Fatalf("unexpected external traffic: %#v", traffic)
	}
	eventOutput, err := runCLIAt(binary, home, checkout, "--json", "traffic", "show", fmt.Sprint(traffic.Traffic[0].Sequence))
	if err != nil {
		t.Fatalf("show traffic event: %v\n%s", err, eventOutput)
	}
	var event model.TrafficEvent
	if err := json.Unmarshal([]byte(eventOutput), &event); err != nil {
		t.Fatalf("decode traffic event: %v\n%s", err, eventOutput)
	}
	if event.Method != http.MethodGet || event.Path != "/checkout" || event.Status != http.StatusOK {
		t.Fatalf("unexpected traffic detail: %#v", event)
	}
	if event.RequestHeaders["Authorization"] != "[REDACTED]" || event.RequestHeaders["X-E2e-Trace"] != "visible" {
		t.Fatalf("traffic headers were not safely captured: %#v", event.RequestHeaders)
	}
	if !strings.Contains(event.ResponseBody, `"checkout":"accepted"`) {
		t.Fatalf("traffic response body was not captured: %q", event.ResponseBody)
	}

	for _, edge := range []string{"checkout:inventory", "checkout:orders"} {
		output, err := runCLIAt(binary, home, checkout, "--json", "traffic", "list", "--edge", edge, "--limit", "20")
		if err != nil {
			t.Fatalf("list %s traffic: %v\n%s", edge, err, output)
		}
		var result struct {
			Traffic []model.TrafficEvent `json:"traffic"`
		}
		if err := json.Unmarshal([]byte(output), &result); err != nil || len(result.Traffic) != 1 {
			t.Fatalf("expected one %s event: err=%v output=%s", edge, err, output)
		}
	}

	logsOutput, err := runCLIAt(binary, home, checkout, "logs", "checkout", "--limit", "50")
	if err != nil || !strings.Contains(logsOutput, "checkout requested sku=coffee-mug quantity=2") {
		t.Fatalf("checkout logs missing request: err=%v\n%s", err, logsOutput)
	}

	downOutput, err := runCLIAt(binary, home, checkout, "down", "--timeout", "2m")
	if err != nil {
		t.Fatalf("portless down failed: %v\n%s", err, downOutput)
	}
	if !strings.Contains(downOutput, "store-e2e/local") || !strings.Contains(downOutput, "stopped") {
		t.Fatalf("unexpected down output:\n%s", downOutput)
	}
	statusOutput, err = runCLIAt(binary, home, checkout, "--json", "status")
	if err != nil {
		t.Fatalf("status after down: %v\n%s", err, statusOutput)
	}
	if err := json.Unmarshal([]byte(statusOutput), &environment); err != nil {
		t.Fatalf("decode stopped status: %v\n%s", err, statusOutput)
	}
	if environment.Status != model.EnvironmentStopped {
		t.Fatalf("environment status after down = %s", environment.Status)
	}
	for _, service := range environment.Services {
		if service.Status != model.ServiceStopped {
			t.Fatalf("service %s status after down = %s", service.Name, service.Status)
		}
	}
}

func TestCLIFaultAndRecordingRoundTrip(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "experiments-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("portless up failed: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	if output, err := runCLIAt(binary, home, checkout, "record", "start", "orders-failure", "--edge", "checkout:orders", "--duration", "5m", "--max-events", "20"); err != nil {
		t.Fatalf("start recording: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "fault", "add", "orders-unavailable", "checkout:orders", "--status", "503"); err != nil {
		t.Fatalf("add fault: %v\n%s", err, output)
	}

	faulted := applicationRequest(t, home, "checkout.local.experiments-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	faultedBody, err := io.ReadAll(faulted.Body)
	faulted.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if faulted.StatusCode != http.StatusBadGateway || !strings.Contains(string(faultedBody), "orders: returned 503 Service Unavailable") {
		t.Fatalf("faulted checkout response = %s\n%s", faulted.Status, faultedBody)
	}

	faultOutput, err := runCLIAt(binary, home, checkout, "--json", "fault", "show", "orders-unavailable")
	if err != nil {
		t.Fatalf("show fault: %v\n%s", err, faultOutput)
	}
	var fault model.FaultRule
	if err := json.Unmarshal([]byte(faultOutput), &fault); err != nil {
		t.Fatalf("decode fault: %v\n%s", err, faultOutput)
	}
	if !fault.Enabled || fault.ExpiresAt != nil || fault.MatchCount != 1 || fault.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unexpected persistent fault: %#v", fault)
	}

	if output, err := runCLIAt(binary, home, checkout, "fault", "disable", fault.Name); err != nil {
		t.Fatalf("disable fault: %v\n%s", err, output)
	}
	recovered := applicationRequest(t, home, "checkout.local.experiments-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	recoveredBody, err := io.ReadAll(recovered.Body)
	recovered.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if recovered.StatusCode != http.StatusOK || !strings.Contains(string(recoveredBody), `"checkout":"accepted"`) {
		t.Fatalf("checkout did not recover after disabling fault: %s\n%s", recovered.Status, recoveredBody)
	}

	if output, err := runCLIAt(binary, home, checkout, "fault", "enable", fault.Name); err != nil {
		t.Fatalf("re-enable fault: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "fault", "disable", fault.Name); err != nil {
		t.Fatalf("disable re-enabled fault: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "fault", "delete", fault.Name, "--yes"); err != nil {
		t.Fatalf("delete fault: %v\n%s", err, output)
	}

	if output, err := runCLIAt(binary, home, checkout, "record", "stop", "orders-failure"); err != nil {
		t.Fatalf("stop recording: %v\n%s", err, output)
	}
	exportOutput, err := runCLIAt(binary, home, checkout, "record", "export", "orders-failure", "--output", "-")
	if err != nil {
		t.Fatalf("export recording: %v\n%s", err, exportOutput)
	}
	var exported struct {
		SchemaVersion int                  `json:"schemaVersion"`
		Project       string               `json:"project"`
		Environment   string               `json:"environment"`
		Recording     string               `json:"recording"`
		Traffic       []model.TrafficEvent `json:"traffic"`
	}
	if err := json.Unmarshal([]byte(exportOutput), &exported); err != nil {
		t.Fatalf("decode recording export: %v\n%s", err, exportOutput)
	}
	if exported.SchemaVersion != 1 || exported.Project != "experiments-e2e" || exported.Environment != "local" || exported.Recording != "orders-failure" || len(exported.Traffic) != 2 {
		t.Fatalf("unexpected recording export: %#v", exported)
	}
	if exported.Traffic[0].Source != "checkout" || exported.Traffic[0].Target != "orders" || exported.Traffic[0].Recording != "orders-failure" {
		t.Fatalf("recording contains the wrong edge: %#v", exported.Traffic)
	}
	if output, err := runCLIAt(binary, home, checkout, "record", "delete", "orders-failure", "--yes"); err != nil {
		t.Fatalf("delete recording: %v\n%s", err, output)
	}
}

func TestCLIDaemonRestartAdoptsRunningServices(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "restart-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("portless up failed: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	before := environmentStatus(t, binary, home, checkout)
	beforePIDs := servicePIDs(before)
	beforeDaemon := daemonStatus(t, binary, home, checkout)
	if !beforeDaemon.HandoffReady {
		t.Fatalf("daemon was not ready for handoff before restart: %#v", beforeDaemon)
	}

	restartOutput, err := runCLIAt(binary, home, checkout, "--json", "daemon", "restart", "--timeout", "30s")
	if err != nil {
		t.Fatalf("restart daemon: %v\n%s\ndaemon log:\n%s", err, restartOutput, readDaemonLog(home))
	}
	var restart struct {
		Daemon e2eDaemonStatus `json:"daemon"`
	}
	if err := json.Unmarshal([]byte(restartOutput), &restart); err != nil {
		t.Fatalf("decode daemon restart: %v\n%s", err, restartOutput)
	}
	if restart.Daemon.InstanceID == beforeDaemon.InstanceID || restart.Daemon.PID == beforeDaemon.PID {
		t.Fatalf("daemon identity did not change: before=%#v after=%#v", beforeDaemon, restart.Daemon)
	}
	if !restart.Daemon.Compatible || !restart.Daemon.CurrentBuild || restart.Daemon.RuntimeState != "ready" || len(restart.Daemon.RecoveryProblems) != 0 {
		t.Fatalf("replacement daemon was not healthy: %#v", restart.Daemon)
	}

	after := environmentStatus(t, binary, home, checkout)
	if after.Status != model.EnvironmentHealthy {
		t.Fatalf("environment after daemon restart = %s: %#v", after.Status, after)
	}
	afterPIDs := servicePIDs(after)
	if !maps.Equal(afterPIDs, beforePIDs) {
		t.Fatalf("service processes were replaced instead of adopted: before=%v after=%v", beforePIDs, afterPIDs)
	}

	response := applicationRequest(t, home, "checkout.local.restart-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"checkout":"accepted"`) {
		t.Fatalf("application route did not recover after daemon restart: %s\n%s", response.Status, body)
	}
}

type e2eDaemonStatus struct {
	State            string   `json:"state"`
	Compatible       bool     `json:"compatible"`
	CurrentBuild     bool     `json:"currentBuild"`
	PID              int      `json:"pid"`
	InstanceID       string   `json:"instanceId"`
	RuntimeState     string   `json:"runtimeState"`
	HandoffReady     bool     `json:"handoffReady"`
	RecoveryProblems []string `json:"recoveryProblems"`
}

func daemonStatus(t *testing.T, binary, home, checkout string) e2eDaemonStatus {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--json", "daemon", "status")
	if err != nil {
		t.Fatalf("daemon status: %v\n%s", err, output)
	}
	var result e2eDaemonStatus
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode daemon status: %v\n%s", err, output)
	}
	return result
}

func environmentStatus(t *testing.T, binary, home, checkout string) model.Environment {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--json", "status")
	if err != nil {
		t.Fatalf("environment status: %v\n%s", err, output)
	}
	var result model.Environment
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode environment status: %v\n%s", err, output)
	}
	return result
}

func servicePIDs(environment model.Environment) map[string]int {
	result := make(map[string]int, len(environment.Services))
	for _, service := range environment.Services {
		result[service.Name] = service.PID
	}
	return result
}

func assertReadyStoreLite(t *testing.T, environment model.Environment) {
	t.Helper()
	if environment.Project != "store-e2e" || environment.Name != "local" || environment.Status != model.EnvironmentHealthy {
		t.Fatalf("unexpected environment identity/status: %#v", environment)
	}
	if environment.PrimaryService != "checkout" {
		t.Fatalf("primary service = %q, want checkout", environment.PrimaryService)
	}
	if len(environment.Services) != 3 {
		t.Fatalf("services = %d, want 3: %#v", len(environment.Services), environment.Services)
	}
	serviceNames := make([]string, 0, len(environment.Services))
	for _, service := range environment.Services {
		serviceNames = append(serviceNames, service.Name)
		if service.Status != model.ServiceReady {
			t.Fatalf("service %s status = %s", service.Name, service.Status)
		}
	}
	sort.Strings(serviceNames)
	if strings.Join(serviceNames, ",") != "checkout,inventory,orders" {
		t.Fatalf("service names = %v", serviceNames)
	}
	edges := make([]string, 0, len(environment.Connections))
	for _, connection := range environment.Connections {
		edges = append(edges, connection.Source+":"+connection.Target)
	}
	sort.Strings(edges)
	if strings.Join(edges, ",") != "checkout:inventory,checkout:orders" {
		t.Fatalf("connections = %v", edges)
	}
}

func isolatedFixture(t *testing.T, fixture string) (string, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "portless-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	checkout := filepath.Join(root, fixture)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyDirectory(e2eRepositoryPath(t, "tests", "fixtures", fixture), checkout); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return home, checkout
}

func e2eRepositoryPath(t *testing.T, elements ...string) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate E2E test source")
	}
	parts := append([]string{filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))}, elements...)
	return filepath.Join(parts...)
}

func copyDirectory(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
}

func runCLIAt(binary, home, directory string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = directory
	command.Env = isolatedEnvironment(home)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func cleanupInstallation(t *testing.T, binary, home, directory string) {
	t.Helper()
	if output, err := runCLIAt(binary, home, directory, "reset", "--force", "--yes"); err != nil {
		t.Logf("E2E reset cleanup: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	if output, err := runCLIAt(binary, home, directory, "daemon", "stop", "--force"); err != nil && !strings.Contains(output, "not running") {
		t.Logf("E2E daemon cleanup: %v\n%s", err, output)
	}
}

func applicationRequest(t *testing.T, home, host, path string, headers map[string]string) *http.Response {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(home, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	var control struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(content, &control); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", control.Port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
