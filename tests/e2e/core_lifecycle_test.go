//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
)

func TestCLIDownAllWorksFromAmbiguousCheckout(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "down-all-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start local environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	if output, err := runCLIAt(binary, home, checkout, "env", "clone", "qa-local"); err != nil {
		t.Fatalf("clone environment: %v\n%s", err, output)
	}
	clone := explicitEnvironmentStatus(t, binary, home, checkout, "down-all-e2e/qa-local")
	if clone.Status != model.EnvironmentStopped {
		t.Fatalf("cloned environment status = %s, want stopped", clone.Status)
	}

	if output, err := runCLIAt(binary, home, checkout, "down", "--timeout", "2m"); err == nil {
		t.Fatalf("ordinary down unexpectedly selected one of two environments:\n%s", output)
	} else if !strings.Contains(output, "this checkout belongs to multiple environments") ||
		!strings.Contains(output, "down-all-e2e/local") || !strings.Contains(output, "down-all-e2e/qa-local") {
		t.Fatalf("ordinary down did not explain the ambiguity: %v\n%s", err, output)
	}

	output, err := runCLIAt(binary, home, checkout, "down", "--all", "--timeout", "2m")
	if err != nil {
		t.Fatalf("down --all failed from an ambiguous checkout: %v\n%s", err, output)
	}
	if !strings.Contains(output, "down-all-e2e/local") || !strings.Contains(output, "stopped") {
		t.Fatalf("down --all output did not include the active environment:\n%s", output)
	}
	for _, selector := range []string{"down-all-e2e/local", "down-all-e2e/qa-local"} {
		environment := explicitEnvironmentStatus(t, binary, home, checkout, selector)
		if environment.Status != model.EnvironmentStopped {
			t.Fatalf("%s status = %s, want stopped", selector, environment.Status)
		}
		for _, service := range environment.Services {
			if service.PID != 0 {
				t.Fatalf("%s service %s survived down --all: %#v", selector, service.Name, service)
			}
			if selector == "down-all-e2e/local" && service.Status != model.ServiceStopped {
				t.Fatalf("active service %s status after down --all = %s, want stopped", service.Name, service.Status)
			}
			if selector == "down-all-e2e/qa-local" && service.Status != model.ServicePlanned && service.Status != model.ServiceStopped {
				t.Fatalf("never-started service %s has active status after down --all: %#v", service.Name, service)
			}
		}
	}
}

func TestCLIServiceLifecycleOnlyMutatesTheSelectedService(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "service-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	before := environmentStatus(t, binary, home, checkout)
	checkoutBefore := requireService(t, before, "checkout")
	inventoryBefore := requireService(t, before, "inventory")
	ordersBefore := requireService(t, before, "orders")

	if output, err := runCLIAt(binary, home, checkout, "service", "stop", "orders", "--timeout", "2m"); err != nil {
		t.Fatalf("stop orders: %v\n%s", err, output)
	}
	stopped := environmentStatus(t, binary, home, checkout)
	assertSameServiceProcess(t, checkoutBefore, requireService(t, stopped, "checkout"))
	assertSameServiceProcess(t, inventoryBefore, requireService(t, stopped, "inventory"))
	ordersStopped := requireService(t, stopped, "orders")
	if ordersStopped.Status != model.ServiceStopped || ordersStopped.PID != 0 {
		t.Fatalf("orders after stop = %#v", ordersStopped)
	}

	failed := applicationRequest(t, home, "checkout.local.service-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	failedBody, readErr := io.ReadAll(failed.Body)
	failed.Body.Close()
	if readErr != nil || failed.StatusCode != http.StatusBadGateway || !strings.Contains(string(failedBody), "orders:") {
		t.Fatalf("checkout did not expose the stopped dependency: status=%s err=%v body=%s", failed.Status, readErr, failedBody)
	}

	if output, err := runCLIAt(binary, home, checkout, "service", "start", "orders", "--timeout", "2m"); err != nil {
		t.Fatalf("start orders: %v\n%s", err, output)
	}
	started := environmentStatus(t, binary, home, checkout)
	assertSameServiceProcess(t, checkoutBefore, requireService(t, started, "checkout"))
	assertSameServiceProcess(t, inventoryBefore, requireService(t, started, "inventory"))
	ordersStarted := requireService(t, started, "orders")
	if ordersStarted.Status != model.ServiceReady || ordersStarted.PID == 0 || ordersStarted.Generation <= ordersBefore.Generation {
		t.Fatalf("orders was not started as a new generation: before=%#v after=%#v", ordersBefore, ordersStarted)
	}

	if output, err := runCLIAt(binary, home, checkout, "service", "restart", "checkout", "--timeout", "2m"); err != nil {
		t.Fatalf("restart checkout: %v\n%s", err, output)
	}
	restarted := environmentStatus(t, binary, home, checkout)
	checkoutRestarted := requireService(t, restarted, "checkout")
	if checkoutRestarted.Status != model.ServiceReady || checkoutRestarted.PID == 0 || checkoutRestarted.Generation <= checkoutBefore.Generation {
		t.Fatalf("checkout was not restarted as a new generation: before=%#v after=%#v", checkoutBefore, checkoutRestarted)
	}
	assertSameServiceProcess(t, inventoryBefore, requireService(t, restarted, "inventory"))
	assertSameServiceProcess(t, ordersStarted, requireService(t, restarted, "orders"))

	recovered := applicationRequest(t, home, "checkout.local.service-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	recovered.Body.Close()
	if recovered.StatusCode != http.StatusOK {
		t.Fatalf("checkout did not recover after service lifecycle operations: %s", recovered.Status)
	}
}

func TestCLIMockProviderHotSwapKeepsPeerServicesRunning(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "mock-provider-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	before := environmentStatus(t, binary, home, checkout)
	checkoutBefore := requireService(t, before, "checkout")
	inventoryBefore := requireService(t, before, "inventory")
	ordersBefore := requireService(t, before, "orders")
	if len(before.Sources) != 1 {
		t.Fatalf("environment sources = %#v, want one local source", before.Sources)
	}

	if output, err := runCLIAt(binary, home, checkout, "mock", "create", "sold-out", "--service", "inventory", "--description", "Inventory has no available stock"); err != nil {
		t.Fatalf("create mock profile: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "mock", "route", "set", "sold-out", "lookup", "--method", "GET", "--path", "/inventory/{sku}", "--status", "200", "--header", "Content-Type=application/json", "--body", `{"available":false,"reason":"mocked sold out"}`); err != nil {
		t.Fatalf("create mock route: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "mock", "preview", "sold-out", "--path", "/inventory/coffee-mug", "--query", "quantity=1"); err != nil || !strings.Contains(output, "matched lookup") {
		t.Fatalf("preview mock route: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "env", "bind", "inventory", "--mock", "sold-out"); err != nil {
		t.Fatalf("bind mock provider: %v\n%s", err, output)
	}

	mocked := environmentStatus(t, binary, home, checkout)
	assertSameServiceProcess(t, checkoutBefore, requireService(t, mocked, "checkout"))
	assertSameServiceProcess(t, ordersBefore, requireService(t, mocked, "orders"))
	inventoryMocked := requireService(t, mocked, "inventory")
	if inventoryMocked.Status != model.ServiceReady || inventoryMocked.PID != 0 || inventoryMocked.Generation != inventoryBefore.Generation {
		t.Fatalf("inventory mock runtime = %#v, before=%#v", inventoryMocked, inventoryBefore)
	}
	var inventoryBinding *model.ComponentBinding
	for index := range mocked.Bindings {
		if mocked.Bindings[index].Service == "inventory" {
			inventoryBinding = &mocked.Bindings[index]
			break
		}
	}
	if inventoryBinding == nil || inventoryBinding.Provider != model.ProviderMock || inventoryBinding.Mock == nil || inventoryBinding.Mock.Profile != "sold-out" {
		t.Fatalf("inventory binding = %#v, want mock sold-out", inventoryBinding)
	}

	response := applicationRequest(t, home, "checkout.local.mock-provider-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil || response.StatusCode != http.StatusConflict || !strings.Contains(string(body), "mocked sold out") {
		t.Fatalf("checkout mock response: status=%s err=%v body=%s", response.Status, readErr, body)
	}
	trafficOutput, err := runCLIAt(binary, home, checkout, "--json", "traffic", "list", "--edge", "checkout:inventory", "--limit", "20")
	if err != nil {
		t.Fatalf("list mock traffic: %v\n%s", err, trafficOutput)
	}
	var traffic struct {
		Exchanges []model.TrafficExchange `json:"exchanges"`
	}
	if err := json.Unmarshal([]byte(trafficOutput), &traffic); err != nil {
		t.Fatalf("decode mock traffic: %v\n%s", err, trafficOutput)
	}
	if len(traffic.Exchanges) != 1 || traffic.Exchanges[0].TargetProvider != model.ProviderMock || traffic.Exchanges[0].MockProfile != "sold-out" || traffic.Exchanges[0].MockRoute != "lookup" {
		t.Fatalf("mock attribution = %#v", traffic.Exchanges)
	}
	ordersTraffic, err := runCLIAt(binary, home, checkout, "--json", "traffic", "list", "--edge", "checkout:orders", "--limit", "20")
	if err != nil {
		t.Fatalf("list orders traffic: %v\n%s", err, ordersTraffic)
	}
	traffic.Exchanges = nil
	if err := json.Unmarshal([]byte(ordersTraffic), &traffic); err != nil || len(traffic.Exchanges) != 0 {
		t.Fatalf("orders should not be called after inventory rejects checkout: err=%v output=%s", err, ordersTraffic)
	}

	if output, err := runCLIAt(binary, home, checkout, "env", "bind", "inventory", "--local", before.Sources[0].Name); err != nil {
		t.Fatalf("restore local inventory: %v\n%s", err, output)
	}
	restored := environmentStatus(t, binary, home, checkout)
	assertSameServiceProcess(t, checkoutBefore, requireService(t, restored, "checkout"))
	assertSameServiceProcess(t, ordersBefore, requireService(t, restored, "orders"))
	inventoryRestored := requireService(t, restored, "inventory")
	if inventoryRestored.Status != model.ServiceReady || inventoryRestored.PID == 0 || inventoryRestored.Generation != inventoryBefore.Generation+1 {
		t.Fatalf("restored inventory = %#v, before=%#v", inventoryRestored, inventoryBefore)
	}
	recovered := applicationRequest(t, home, "checkout.local.mock-provider-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	recovered.Body.Close()
	if recovered.StatusCode != http.StatusOK {
		t.Fatalf("checkout did not recover after restoring inventory: %s", recovered.Status)
	}
	if output, err := runCLIAt(binary, home, checkout, "mock", "delete", "sold-out", "--yes"); err != nil {
		t.Fatalf("delete mock profile: %v\n%s", err, output)
	}
}

func TestCLIDownAllStopsMultipleActiveWorktrees(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "worktrees-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start local worktree: %v\n%s", err, output)
	}
	local := environmentStatus(t, binary, home, checkout)
	if len(local.Sources) != 1 {
		t.Fatalf("local sources = %#v", local.Sources)
	}
	secondCheckout := filepath.Join(filepath.Dir(checkout), "store-lite-qa")
	if err := copyDirectory(checkout, secondCheckout); err != nil {
		t.Fatal(err)
	}
	if output, err := runCLIAt(binary, home, checkout, "env", "clone", "qa"); err != nil {
		t.Fatalf("clone qa environment: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "--env", "worktrees-e2e/qa", "env", "checkout", "set", local.Sources[0].Name, "--path", secondCheckout); err != nil {
		t.Fatalf("bind qa worktree: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, secondCheckout, "--env", "worktrees-e2e/qa", "up", "--managed", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start qa worktree: %v\n%s", err, output)
	}

	local = explicitEnvironmentStatus(t, binary, home, checkout, "worktrees-e2e/local")
	qa := explicitEnvironmentStatus(t, binary, home, checkout, "worktrees-e2e/qa")
	if local.Status != model.EnvironmentHealthy || qa.Status != model.EnvironmentHealthy {
		t.Fatalf("both worktrees were not active: local=%s qa=%s", local.Status, qa.Status)
	}
	for name, localPID := range servicePIDs(local) {
		if qaPID := servicePIDs(qa)[name]; localPID == 0 || qaPID == 0 || localPID == qaPID {
			t.Fatalf("service %s did not receive isolated processes: local=%d qa=%d", name, localPID, qaPID)
		}
	}

	output, err := runCLIAt(binary, home, checkout, "down", "--all", "--timeout", "2m")
	if err != nil {
		t.Fatalf("down --all with multiple active worktrees: %v\n%s", err, output)
	}
	for _, selector := range []string{"worktrees-e2e/local", "worktrees-e2e/qa"} {
		if !strings.Contains(output, selector) {
			t.Fatalf("down --all omitted %s:\n%s", selector, output)
		}
		environment := explicitEnvironmentStatus(t, binary, home, checkout, selector)
		if environment.Status != model.EnvironmentStopped {
			t.Fatalf("%s status after down --all = %s", selector, environment.Status)
		}
		for _, service := range environment.Services {
			if service.Status != model.ServiceStopped || service.PID != 0 {
				t.Fatalf("%s service survived down --all: %#v", selector, service)
			}
		}
	}
}

func TestCLIDaemonCrashRecoveryAdoptsSupervisors(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "crash-recovery-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start crash recovery environment: %v\n%s", err, output)
	}
	beforeEnvironment := environmentStatus(t, binary, home, checkout)
	beforePIDs := servicePIDs(beforeEnvironment)
	beforeDaemon := daemonStatus(t, binary, home, checkout)
	if err := syscall.Kill(beforeDaemon.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill isolated daemon PID %d: %v", beforeDaemon.PID, err)
	}
	waitForProcessExit(t, beforeDaemon.PID)

	afterEnvironment := environmentStatus(t, binary, home, checkout)
	afterDaemon := daemonStatus(t, binary, home, checkout)
	if afterDaemon.PID == beforeDaemon.PID || afterDaemon.InstanceID == beforeDaemon.InstanceID || afterDaemon.RuntimeState != "ready" || !afterDaemon.HandoffReady {
		t.Fatalf("daemon did not recover after crash: before=%#v after=%#v", beforeDaemon, afterDaemon)
	}
	if afterEnvironment.Status != model.EnvironmentHealthy || !maps.Equal(beforePIDs, servicePIDs(afterEnvironment)) {
		t.Fatalf("supervisors were not adopted after daemon crash: before=%v after=%#v", beforePIDs, afterEnvironment)
	}
	response := applicationRequest(t, home, "checkout.local.crash-recovery-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("application did not recover after daemon crash: %s", response.Status)
	}
}

func TestCLIExecutableReplacementAdoptsRunningServices(t *testing.T) {
	original := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	executable := filepath.Join(filepath.Dir(home), "portless-replace-e2e")
	copyExecutableForE2E(t, original, executable)
	defer cleanupInstallation(t, executable, home, checkout)

	if output, err := runCLIAt(executable, home, checkout, "up", "--name", "replace-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start environment with initial executable: %v\n%s", err, output)
	}
	beforeEnvironment := environmentStatus(t, executable, home, checkout)
	beforePIDs := servicePIDs(beforeEnvironment)
	beforeDaemon := daemonStatus(t, executable, home, checkout)
	replacement := executable + ".new"
	buildReplacementExecutable(t, replacement)
	if output, err := runCLIAt(replacement, home, checkout, "--version"); err != nil || !strings.Contains(output, "replacement-e2e") {
		t.Fatalf("replacement executable is not runnable: %v\n%s", err, output)
	}
	if err := os.Rename(replacement, executable); err != nil {
		t.Fatalf("atomically replace E2E executable: %v", err)
	}

	afterEnvironment := environmentStatus(t, executable, home, checkout)
	waitForProcessExit(t, beforeDaemon.PID)
	afterDaemon := daemonStatus(t, executable, home, checkout)
	if afterDaemon.PID == beforeDaemon.PID || afterDaemon.InstanceID == beforeDaemon.InstanceID || afterDaemon.BuildID == beforeDaemon.BuildID || !afterDaemon.CurrentBuild || afterDaemon.RuntimeState != "ready" {
		t.Fatalf("replacement daemon identity is invalid: before=%#v after=%#v", beforeDaemon, afterDaemon)
	}
	if afterEnvironment.Status != model.EnvironmentHealthy || !maps.Equal(beforePIDs, servicePIDs(afterEnvironment)) {
		t.Fatalf("services were replaced during executable handoff: before=%v after=%#v", beforePIDs, afterEnvironment)
	}
}

func TestCLIServiceCrashBecomesDegradedAndRecovers(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "service-crash-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start service crash environment: %v\n%s", err, output)
	}
	before := environmentStatus(t, binary, home, checkout)
	checkoutBefore := requireService(t, before, "checkout")
	inventoryBefore := requireService(t, before, "inventory")
	ordersBefore := requireService(t, before, "orders")
	if err := syscall.Kill(-ordersBefore.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill orders process group %d: %v", ordersBefore.PID, err)
	}
	failed := waitForFailedService(t, binary, home, checkout, "orders")
	if failed.Reason == "" {
		t.Fatalf("crashed service lacks failure detail: %#v", failed)
	}
	degraded := environmentStatus(t, binary, home, checkout)
	if degraded.Status != model.EnvironmentDegraded && degraded.Status != model.EnvironmentFailed {
		t.Fatalf("environment status after required service crash = %s", degraded.Status)
	}
	assertSameServiceProcess(t, checkoutBefore, requireService(t, degraded, "checkout"))
	assertSameServiceProcess(t, inventoryBefore, requireService(t, degraded, "inventory"))
	if logs, err := runCLIAt(binary, home, checkout, "logs", "orders", "--limit", "20"); err != nil || !strings.Contains(logs, "orders ready on") {
		t.Fatalf("crashed service logs were not retained: err=%v\n%s", err, logs)
	}

	if output, err := runCLIAt(binary, home, checkout, "service", "start", "orders", "--timeout", "2m"); err != nil {
		t.Fatalf("start crashed service: %v\n%s", err, output)
	}
	recovered := environmentStatus(t, binary, home, checkout)
	ordersRecovered := requireService(t, recovered, "orders")
	if recovered.Status != model.EnvironmentHealthy || ordersRecovered.Status != model.ServiceReady || ordersRecovered.PID == 0 || ordersRecovered.Generation <= ordersBefore.Generation {
		t.Fatalf("environment did not recover from service crash: %#v", recovered)
	}
	assertSameServiceProcess(t, checkoutBefore, requireService(t, recovered, "checkout"))
	assertSameServiceProcess(t, inventoryBefore, requireService(t, recovered, "inventory"))
	response := applicationRequest(t, home, "checkout.local.service-crash-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("application did not recover after service restart: %s", response.Status)
	}
}

func requireService(t *testing.T, environment model.Environment, name string) model.Service {
	t.Helper()
	for _, service := range environment.Services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service %s not found in %#v", name, environment.Services)
	return model.Service{}
}

func assertSameServiceProcess(t *testing.T, before, after model.Service) {
	t.Helper()
	if after.Status != model.ServiceReady || after.PID != before.PID || after.Generation != before.Generation {
		t.Fatalf("service %s was unexpectedly replaced: before=%#v after=%#v", before.Name, before, after)
	}
}

func waitForFailedService(t *testing.T, binary, home, checkout, name string) model.Service {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last model.Service
	for time.Now().Before(deadline) {
		environment := environmentStatus(t, binary, home, checkout)
		last = requireService(t, environment, name)
		if last.Status == model.ServiceExited || last.Status == model.ServiceFailed || last.Status == model.ServiceUnhealthy {
			return last
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("service %s did not report its crash: %#v", name, last)
	return model.Service{}
}

func copyExecutableForE2E(t *testing.T, source, destination string) {
	t.Helper()
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o755); err != nil {
		t.Fatal(err)
	}
}

func buildReplacementExecutable(t *testing.T, output string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-tags=e2e", "-trimpath", "-ldflags", "-X github.com/portless-run/portless/portless-cli.Version=replacement-e2e", "-o", output, "./portless-cli/cmd/portless")
	command.Dir = e2eRepositoryPath(t)
	command.Env = os.Environ()
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build replacement E2E executable: %v\n%s", err, result)
	}
}
