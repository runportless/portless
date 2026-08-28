//go:build relay_e2e && (darwin || linux)

package relay_e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-cli/doctor"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/networking"
	relayhealth "github.com/runportless/portless/portless-relay/health"
	relayinstallation "github.com/runportless/portless/portless-relay/installation"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
	"golang.org/x/sys/unix"
)

const destructiveRelayOptIn = "PORTLESS_DESTRUCTIVE_RELAY_E2E"
const destructiveRelayResourceOptIn = "PORTLESS_DESTRUCTIVE_RELAY_RESOURCE_E2E"

var relayMachine *machineHarness

func TestMain(tests *testing.M) {
	if os.Getenv(destructiveRelayOptIn) != "1" {
		fmt.Fprintf(os.Stderr, "%s=1 is required; this suite replaces the machine-wide Portless relay\n", destructiveRelayOptIn)
		os.Exit(2)
	}
	harness, err := newMachineHarness()
	if err != nil {
		fmt.Fprintln(os.Stderr, "relay E2E preflight:", err)
		os.Exit(1)
	}
	relayMachine = harness
	code := tests.Run()
	if cleanupErr := harness.cleanup(); cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "relay E2E cleanup:", cleanupErr)
		code = 1
	}
	os.Exit(code)
}

func TestDestructiveRelayLifecycle(t *testing.T) {
	harness := relayMachine
	if err := harness.takeMachineOwnership(); err != nil {
		t.Fatal(err)
	}

	harness.testInstallAttempted = true
	install := mustRelayStatus(t, harness.runTest("--json", "relay", "install"))
	assertInstalledRelay(t, install, harness.home)

	// Installation is a repair operation and must be idempotent while already healthy.
	reinstalled := mustRelayStatus(t, harness.runTest("--json", "relay", "install"))
	assertInstalledRelay(t, reinstalled, harness.home)
	if install.InstalledAt == nil || reinstalled.InstalledAt == nil || !install.InstalledAt.Equal(*reinstalled.InstalledAt) {
		t.Fatalf("idempotent install replaced the healthy relay: before=%v after=%v", install.InstalledAt, reinstalled.InstalledAt)
	}

	doctorResult := harness.runTest("--json", "doctor", "relay")
	if doctorResult.Err != nil {
		t.Fatalf("doctor relay failed: %v\nstdout:\n%s\nstderr:\n%s", doctorResult.Err, doctorResult.Stdout, doctorResult.Stderr)
	}
	var report doctor.Report
	decodeJSON(t, doctorResult.Stdout, &report)
	if !report.Healthy || report.Summary.Failed != 0 || report.Summary.Warnings != 0 || report.Summary.Skipped != 0 {
		t.Fatalf("relay doctor was not healthy: %#v", report)
	}
	for _, required := range []string{"relay.installation", "relay.ownership", "relay.target", "relay.dns_target", "relay.endpoint_pool", "relay.portless_dns", "relay.service", "relay.port_80", "relay.end_to_end", "relay.dns_listener", "relay.dns_end_to_end"} {
		if !healthyDoctorCheck(report, required) {
			t.Errorf("doctor did not pass %s: %#v", required, report.Checks)
		}
	}

	if os.Getenv(destructiveRelayResourceOptIn) == "1" {
		appendRelayResourceMarker(t, harness.checkout)
		if preference := strings.TrimSpace(os.Getenv("PORTLESS_MANAGED_RESOURCE_RUNTIME")); preference != "" {
			if result := harness.runTest("runtime", "use", preference); result.Err != nil {
				t.Fatalf("select relay resource runtime %s: %v\n%s", preference, result.Err, result.Output())
			}
		}
	}
	upTimeout := "2m"
	if os.Getenv(destructiveRelayResourceOptIn) == "1" {
		upTimeout = "4m"
	}
	up := harness.runTest("up", "--name", "relay-e2e", "--no-open", "--timeout", upTimeout)
	if up.Err != nil {
		t.Fatalf("production portless up did not accept the installed relay: %v\nstdout:\n%s\nstderr:\n%s\ndaemon log:\n%s", up.Err, up.Stdout, up.Stderr, harness.daemonLog())
	}

	control := relayRequest(t, "portless.localhost", "/api/v1/health")
	controlBody := readResponse(t, control)
	if control.StatusCode != http.StatusOK || !strings.Contains(controlBody, `"ready":true`) {
		t.Fatalf("clean control URL returned %s: %s", control.Status, controlBody)
	}
	ui := relayRequest(t, "portless.localhost", "/")
	uiBody := readResponse(t, ui)
	if ui.StatusCode != http.StatusOK || !strings.Contains(uiBody, `id="root"`) {
		t.Fatalf("embedded UI did not load through the clean control URL: %s: %s", ui.Status, uiBody)
	}
	assertProductionBrowserSession(t, harness)
	application := relayRequest(t, "checkout.local.relay-e2e.localhost", "/checkout?sku=coffee-mug&quantity=2")
	applicationBody := readResponse(t, application)
	if application.StatusCode != http.StatusOK || !strings.Contains(applicationBody, `"checkout":"accepted"`) {
		t.Fatalf("clean application URL returned %s: %s", application.Status, applicationBody)
	}
	unknown := relayRequest(t, "not-portless.example.test", "/api/v1/health")
	unknownBody := readResponse(t, unknown)
	if unknown.StatusCode != http.StatusMisdirectedRequest || !strings.Contains(unknownBody, `"code":"UNKNOWN_HOST"`) {
		t.Fatalf("unknown host returned %s: %s", unknown.Status, unknownBody)
	}

	dnsContext, cancelDNS := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancelDNS()
	if err := relayhealth.CheckDNS(dnsContext); err != nil {
		t.Fatalf("direct relay DNS check failed: %v", err)
	}
	if err := relayhealth.CheckResolver(dnsContext); err != nil {
		t.Fatalf("system resolver did not use the installed Portless route: %v", err)
	}
	if os.Getenv(destructiveRelayResourceOptIn) == "1" {
		assertCleanValkeyEndpoint(t, harness)
	}

	restartResult := harness.runTest("--json", "relay", "restart")
	if restartResult.Err != nil {
		t.Fatalf("relay restart failed: %v\nstdout:\n%s\nstderr:\n%s", restartResult.Err, restartResult.Stdout, restartResult.Stderr)
	}
	var restarted struct {
		Action string `json:"action"`
		State  string `json:"state"`
		relayinstallation.InstallationStatus
	}
	decodeJSON(t, restartResult.Stdout, &restarted)
	if restarted.Action != "restart" || restarted.State != "ready" || !restarted.Healthy {
		t.Fatalf("unexpected relay restart result: %#v", restarted)
	}
	application = relayRequest(t, "checkout.local.relay-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1")
	if body := readResponse(t, application); application.StatusCode != http.StatusOK || !strings.Contains(body, `"checkout":"accepted"`) {
		t.Fatalf("application route failed after relay restart: %s: %s", application.Status, body)
	}

	stop := harness.runTest("daemon", "stop", "--force")
	if stop.Err != nil {
		t.Fatalf("stop isolated daemon: %v\n%s\n%s", stop.Err, stop.Stdout, stop.Stderr)
	}
	unavailable := waitForRelayStatus(t, http.StatusServiceUnavailable)
	unavailableBody := readResponse(t, unavailable)
	if !strings.Contains(unavailableBody, "Portless is not running") {
		t.Fatalf("relay did not explain the unavailable daemon: %s", unavailableBody)
	}
	waitForHealthyEnvironment(t, harness)
	application = relayRequest(t, "checkout.local.relay-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1")
	if body := readResponse(t, application); application.StatusCode != http.StatusOK || !strings.Contains(body, `"checkout":"accepted"`) {
		t.Fatalf("application route did not recover with the daemon: %s: %s", application.Status, body)
	}

	previewResult := harness.runTest("--json", "uninstall")
	if previewResult.Err != nil {
		t.Fatalf("full uninstall preview failed: %v\nstdout:\n%s\nstderr:\n%s", previewResult.Err, previewResult.Stdout, previewResult.Stderr)
	}
	var preview struct {
		Action       string `json:"action"`
		Confirmed    bool   `json:"confirmed"`
		Changed      bool   `json:"changed"`
		Projects     int    `json:"projects"`
		Environments int    `json:"environments"`
		Relay        struct {
			Installed bool `json:"installed"`
		} `json:"relay"`
		Data struct {
			Present bool `json:"present"`
		} `json:"data"`
		Launcher struct {
			Action string `json:"action"`
		} `json:"launcher"`
	}
	decodeJSON(t, previewResult.Stdout, &preview)
	if preview.Action != "uninstall" || preview.Confirmed || preview.Changed || preview.Projects != 1 || preview.Environments != 1 || !preview.Relay.Installed || !preview.Data.Present || preview.Launcher.Action != "preserve" {
		t.Fatalf("unexpected full uninstall preview: %#v", preview)
	}
	if status := mustRelayStatus(t, harness.runTest("--json", "relay", "status")); !status.Healthy {
		t.Fatalf("uninstall preview changed relay state: %#v", status)
	}

	uninstallResult := harness.runTest("--json", "uninstall", "--force", "--yes")
	if uninstallResult.Err != nil {
		t.Fatalf("full uninstall failed: %v\nstdout:\n%s\nstderr:\n%s", uninstallResult.Err, uninstallResult.Stdout, uninstallResult.Stderr)
	}
	var uninstalled struct {
		Action           string `json:"action"`
		Confirmed        bool   `json:"confirmed"`
		Forced           bool   `json:"forced"`
		Changed          bool   `json:"changed"`
		Complete         bool   `json:"complete"`
		ProcessesStopped int    `json:"processesStopped"`
		Relay            struct {
			Removed bool `json:"removed"`
		} `json:"relay"`
		Data struct {
			Removed bool `json:"removed"`
		} `json:"data"`
		Launcher struct {
			Action  string `json:"action"`
			Removed bool   `json:"removed"`
		} `json:"launcher"`
	}
	decodeJSON(t, uninstallResult.Stdout, &uninstalled)
	if uninstalled.Action != "uninstall" || !uninstalled.Confirmed || !uninstalled.Forced || !uninstalled.Changed || !uninstalled.Complete || uninstalled.ProcessesStopped < 3 || !uninstalled.Relay.Removed || !uninstalled.Data.Removed || uninstalled.Launcher.Action != "preserve" || uninstalled.Launcher.Removed {
		t.Fatalf("unexpected uninstall result: %#v", uninstalled)
	}
	if _, err := os.Stat(harness.binary); err != nil {
		t.Fatalf("full uninstall removed the source-tree test binary: %v", err)
	}
	if _, err := os.Lstat(harness.home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("full uninstall retained test installation data: %v", err)
	}
	assertRelayAbsent(t, mustRelayStatus(t, harness.runTest("--json", "relay", "status")))
	waitForNoTCPListener(t, relayruntime.DefaultListenAddress)
	waitForNoTCPListener(t, relayruntime.DefaultDNSAddress)
	if err := waitForResolverRemoval(); err != nil {
		t.Fatal(err)
	}

	secondUninstall := harness.runTest("--json", "uninstall", "--force", "--yes")
	if secondUninstall.Err != nil {
		t.Fatalf("idempotent full uninstall failed: %v\n%s\n%s", secondUninstall.Err, secondUninstall.Stdout, secondUninstall.Stderr)
	}
	decodeJSON(t, secondUninstall.Stdout, &uninstalled)
	if !uninstalled.Complete || uninstalled.Relay.Removed || uninstalled.Launcher.Removed {
		t.Fatalf("second full uninstall was not idempotent: %#v", uninstalled)
	}

	if result := harness.runTest("reset", "--force", "--yes"); result.Err != nil {
		t.Fatalf("reset isolated test installation: %v\n%s\n%s", result.Err, result.Stdout, result.Stderr)
	}
	if result := harness.runTest("daemon", "stop", "--force"); result.Err != nil && !strings.Contains(result.Output(), "not running") {
		t.Fatalf("stop isolated test daemon: %v\n%s", result.Err, result.Output())
	}
}

type relayStatus struct {
	State string `json:"state"`
	relayinstallation.InstallationStatus
}

type daemonStatus struct {
	State              string   `json:"state"`
	ActiveEnvironments []string `json:"activeEnvironments"`
	Problems           []string `json:"problems"`
}

type commandResult struct {
	Stdout string
	Stderr string
	Err    error
}

func (result commandResult) Output() string { return result.Stdout + result.Stderr }

type machineHarness struct {
	binary               string
	root                 string
	home                 string
	checkout             string
	lock                 *os.File
	original             relayStatus
	originalRoot         string
	originalDaemon       daemonStatus
	mutationStarted      bool
	testInstallAttempted bool
}

func newMachineHarness() (*machineHarness, error) {
	if os.Geteuid() == 0 {
		return nil, errors.New("run the destructive relay suite as your normal user, not as root")
	}
	binary := strings.TrimSpace(os.Getenv("PORTLESS_RELAY_E2E_BINARY"))
	if binary == "" {
		return nil, errors.New("PORTLESS_RELAY_E2E_BINARY must name the production Portless binary")
	}
	absolute, err := filepath.Abs(binary)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("production E2E binary %s is not an executable regular file", absolute)
	}
	lock, err := acquireMachineLock()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("/tmp", "portless-relay-e2e-")
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	harness := &machineHarness{
		binary: absolute, root: root, home: filepath.Join(root, "home"), checkout: filepath.Join(root, "store-lite"), lock: lock,
	}
	if err := os.MkdirAll(harness.home, 0o700); err != nil {
		_ = harness.closeWithoutMutation()
		return nil, err
	}
	if err := copyDirectory(repositoryPath("tests", "fixtures", "store-lite"), harness.checkout); err != nil {
		_ = harness.closeWithoutMutation()
		return nil, err
	}
	statusResult := harness.run(harness.home, harness.checkout, "--json", "relay", "status")
	if statusResult.Err != nil {
		_ = harness.closeWithoutMutation()
		return nil, fmt.Errorf("inspect existing relay: %w\n%s", statusResult.Err, statusResult.Output())
	}
	if err := json.Unmarshal([]byte(statusResult.Stdout), &harness.original); err != nil {
		_ = harness.closeWithoutMutation()
		return nil, fmt.Errorf("decode existing relay status: %w", err)
	}
	if harness.original.Installed {
		if !harness.original.ReceiptPresent {
			_ = harness.closeWithoutMutation()
			return nil, errors.New("existing relay has no valid ownership receipt; this suite will not remove it")
		}
		if schema, schemaErr := relayReceiptSchema(harness.original.ReceiptPath); schemaErr != nil || schema != 3 {
			_ = harness.closeWithoutMutation()
			return nil, fmt.Errorf("existing relay receipt must use current schema 3 before destructive testing (schema=%d, err=%v)", schema, schemaErr)
		}
		if harness.original.OwnerUID != os.Getuid() {
			_ = harness.closeWithoutMutation()
			return nil, fmt.Errorf("existing relay belongs to UID %d; this suite will not remove it", harness.original.OwnerUID)
		}
		root, rootErr := installationRoot(harness.original.InstallationStatus)
		if rootErr != nil {
			_ = harness.closeWithoutMutation()
			return nil, rootErr
		}
		harness.originalRoot = root
		daemonResult := harness.waitForOriginalDaemon(root, 10*time.Second)
		if daemonResult.Err != nil {
			_ = harness.closeWithoutMutation()
			return nil, fmt.Errorf("daemon behind existing relay did not become reachable: %w\n%s\nrun `PORTLESS_HOME=%q portless daemon restart --force`, then retry", daemonResult.Err, daemonResult.Output(), root)
		}
		if err := json.Unmarshal([]byte(daemonResult.Stdout), &harness.originalDaemon); err != nil {
			_ = harness.closeWithoutMutation()
			return nil, fmt.Errorf("decode daemon behind existing relay: %w", err)
		}
		if harness.originalDaemon.State != "running" && harness.originalDaemon.State != "outdated" {
			_ = harness.closeWithoutMutation()
			return nil, fmt.Errorf("daemon behind existing relay is %s; start or repair it so the suite can prove that no runtime is active", harness.originalDaemon.State)
		}
		if len(harness.originalDaemon.ActiveEnvironments) > 0 {
			_ = harness.closeWithoutMutation()
			return nil, fmt.Errorf("existing Portless installation has active environments: %s; run `portless down --all`, then retry", strings.Join(harness.originalDaemon.ActiveEnvironments, ", "))
		}
	}
	if err := cacheAdministratorApproval(); err != nil {
		_ = harness.closeWithoutMutation()
		return nil, err
	}
	return harness, nil
}

func cacheAdministratorApproval() error {
	command := exec.Command("sudo", "-v")
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("administrator approval is required for the destructive relay suite: %w", err)
	}
	return nil
}

func (harness *machineHarness) takeMachineOwnership() error {
	harness.mutationStarted = true
	if harness.original.Installed {
		result := harness.run(harness.originalRoot, harness.originalRoot, "relay", "uninstall")
		if result.Err != nil {
			return fmt.Errorf("remove existing owned relay before test: %w\n%s", result.Err, result.Output())
		}
	}
	statusResult := harness.runTest("--json", "relay", "status")
	if statusResult.Err != nil {
		return fmt.Errorf("inspect relay after pre-test removal: %w\n%s", statusResult.Err, statusResult.Output())
	}
	var status relayStatus
	if err := json.Unmarshal([]byte(statusResult.Stdout), &status); err != nil {
		return err
	}
	if status.Installed {
		return fmt.Errorf("machine-wide relay remains installed after pre-test removal: %#v", status)
	}
	if listenerAccepting(relayruntime.DefaultListenAddress) {
		return fmt.Errorf("%s is occupied after relay removal; refusing to install the test relay", relayruntime.DefaultListenAddress)
	}
	if listenerAccepting(relayruntime.DefaultDNSAddress) {
		return fmt.Errorf("%s is occupied after relay removal; refusing to install the test relay", relayruntime.DefaultDNSAddress)
	}
	return nil
}

func (harness *machineHarness) cleanup() error {
	var problems []error
	if harness.mutationStarted {
		statusResult := harness.runTest("--json", "relay", "status")
		var current relayStatus
		if statusResult.Err == nil {
			if err := json.Unmarshal([]byte(statusResult.Stdout), &current); err != nil {
				problems = append(problems, fmt.Errorf("decode relay during cleanup: %w", err))
			} else if current.Installed && !harness.isOriginalRelay(current) {
				if harness.isTestRelay(current) {
					result := harness.runTest("relay", "uninstall", "--force")
					if result.Err != nil {
						problems = append(problems, fmt.Errorf("remove test relay: %w\n%s", result.Err, result.Output()))
					}
				} else {
					problems = append(problems, fmt.Errorf("refusing to remove unexpected relay during cleanup: owner UID %d, target %s", current.OwnerUID, current.TargetSocket))
				}
			}
		} else {
			problems = append(problems, fmt.Errorf("inspect test relay during cleanup: %w\n%s", statusResult.Err, statusResult.Output()))
		}
		if result := harness.runTest("reset", "--force", "--yes"); result.Err != nil {
			problems = append(problems, fmt.Errorf("reset test installation: %w\n%s", result.Err, result.Output()))
		}
		if result := harness.runTest("daemon", "stop", "--force"); result.Err != nil && !strings.Contains(result.Output(), "not running") {
			problems = append(problems, fmt.Errorf("stop test daemon: %w\n%s", result.Err, result.Output()))
		}
		if harness.original.Installed {
			statusResult = harness.runTest("--json", "relay", "status")
			current = relayStatus{}
			if statusResult.Err == nil {
				_ = json.Unmarshal([]byte(statusResult.Stdout), &current)
			}
			if !current.Installed {
				result := harness.run(harness.originalRoot, harness.originalRoot, "relay", "install")
				if result.Err != nil {
					problems = append(problems, fmt.Errorf("restore original relay: %w\n%s", result.Err, result.Output()))
				}
			}
			restoredResult := harness.run(harness.originalRoot, harness.originalRoot, "--json", "relay", "status")
			var restored relayStatus
			if restoredResult.Err != nil {
				problems = append(problems, fmt.Errorf("inspect restored relay: %w\n%s", restoredResult.Err, restoredResult.Output()))
			} else if err := json.Unmarshal([]byte(restoredResult.Stdout), &restored); err != nil {
				problems = append(problems, fmt.Errorf("decode restored relay: %w", err))
			} else if !restored.Installed || restored.OwnerUID != harness.original.OwnerUID || restored.TargetSocket != harness.original.TargetSocket || restored.DNSTargetSocket != harness.original.DNSTargetSocket {
				problems = append(problems, fmt.Errorf("restored relay does not match original ownership: %#v", restored))
			} else if harness.original.Healthy && !restored.Healthy {
				problems = append(problems, fmt.Errorf("original relay was healthy but restored relay is not: %#v", restored))
			}
		}
	}
	if err := harness.closeWithoutMutation(); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

func (harness *machineHarness) isOriginalRelay(status relayStatus) bool {
	return harness.original.Installed && status.OwnerUID == harness.original.OwnerUID && status.TargetSocket == harness.original.TargetSocket && status.DNSTargetSocket == harness.original.DNSTargetSocket
}

func (harness *machineHarness) isTestRelay(status relayStatus) bool {
	if !harness.testInstallAttempted || (status.OwnerUID != 0 && status.OwnerUID != os.Getuid()) {
		return false
	}
	expectedHTTP := filepath.Join(harness.home, "ingress.sock")
	expectedDNS := filepath.Join(harness.home, "dns.sock")
	httpTargetMatches := status.TargetSocket == "" || status.TargetSocket == expectedHTTP
	dnsTargetMatches := status.DNSTargetSocket == "" || status.DNSTargetSocket == expectedDNS
	return httpTargetMatches && dnsTargetMatches && (status.TargetSocket == expectedHTTP || status.DNSTargetSocket == expectedDNS || (status.TargetSocket == "" && status.DNSTargetSocket == ""))
}

func (harness *machineHarness) closeWithoutMutation() error {
	var problems []error
	if harness.lock != nil {
		if err := unix.Flock(int(harness.lock.Fd()), unix.LOCK_UN); err != nil {
			problems = append(problems, err)
		}
		if err := harness.lock.Close(); err != nil {
			problems = append(problems, err)
		}
		harness.lock = nil
	}
	if harness.root != "" {
		if err := os.RemoveAll(harness.root); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

func (harness *machineHarness) runTest(arguments ...string) commandResult {
	return harness.run(harness.home, harness.checkout, arguments...)
}

func (harness *machineHarness) waitForOriginalDaemon(root string, timeout time.Duration) commandResult {
	deadline := time.Now().Add(timeout)
	var result commandResult
	for {
		result = harness.run(root, root, "--json", "daemon", "status")
		if result.Err == nil || !time.Now().Before(deadline) {
			return result
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (harness *machineHarness) run(home, directory string, arguments ...string) commandResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, harness.binary, arguments...)
	command.Dir = directory
	command.Stdin = os.Stdin
	command.Env = isolatedEnvironment(home)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

func (harness *machineHarness) daemonLog() string {
	content, err := os.ReadFile(filepath.Join(harness.home, "daemon.log"))
	if err != nil {
		return "<unavailable: " + err.Error() + ">"
	}
	return string(content)
}

func mustRelayStatus(t *testing.T, result commandResult) relayStatus {
	t.Helper()
	if result.Err != nil {
		t.Fatalf("relay command failed: %v\nstdout:\n%s\nstderr:\n%s", result.Err, result.Stdout, result.Stderr)
	}
	var status relayStatus
	decodeJSON(t, result.Stdout, &status)
	return status
}

func assertInstalledRelay(t *testing.T, status relayStatus, home string) {
	t.Helper()
	if status.State != "ready" || !status.Installed || !status.Running || !status.Healthy || !status.HTTPHealthy || !status.DNSHealthy || !status.ResolverHealthy || !status.EndpointPoolReady ||
		!status.HelperPresent || !status.ConfigurationPresent || !status.ReceiptPresent || !status.ResolverPresent {
		t.Fatalf("relay is not completely ready: %#v", status)
	}
	if status.OwnerUID != os.Getuid() || status.OwnerGID != os.Getgid() || status.TargetSocket != filepath.Join(home, "ingress.sock") || status.DNSTargetSocket != filepath.Join(home, "dns.sock") {
		t.Fatalf("relay ownership or target is incorrect: %#v", status)
	}
}

func assertRelayAbsent(t *testing.T, status relayStatus) {
	t.Helper()
	if status.State != "not installed" || status.Installed || status.Running || status.HelperPresent || status.ConfigurationPresent || status.ReceiptPresent || status.ResolverPresent {
		t.Fatalf("relay artifacts remain after uninstall: %#v", status)
	}
	for _, path := range []string{status.HelperPath, status.ConfigurationPath, status.ReceiptPath, status.ResolverPath, status.LocalhostResolverPath} {
		if path == "" {
			continue
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("relay path remains after uninstall: %s (err=%v)", path, err)
		}
	}
	if runtime.GOOS == "darwin" {
		if status.EndpointPoolReady {
			t.Fatalf("relay endpoint pool still reports ready after uninstall: %s", status.EndpointPoolDetail)
		}
		assertDarwinLoopbackPoolAbsent(t)
	}
}

func assertDarwinLoopbackPoolAbsent(t *testing.T) {
	t.Helper()
	loopback, err := net.InterfaceByName("lo0")
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := loopback.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	configured := make(map[string]bool, len(addresses))
	for _, address := range addresses {
		if ip, _, parseErr := net.ParseCIDR(address.String()); parseErr == nil {
			configured[ip.String()] = true
		}
	}
	managed := append([]string{}, networking.EndpointLoopbackAddresses()...)
	dnsHost, _, _ := net.SplitHostPort(relayruntime.DefaultDNSAddress)
	managed = append(managed, dnsHost)
	for _, address := range managed {
		if configured[address] {
			t.Fatalf("managed relay loopback alias %s remains after uninstall", address)
		}
	}
}

func healthyDoctorCheck(report doctor.Report, code string) bool {
	for _, check := range report.Checks {
		if check.Code == code {
			return check.Status == doctor.StatusPass || check.Status == doctor.StatusInfo
		}
	}
	return false
}

func waitForHealthyEnvironment(t *testing.T, harness *machineHarness) model.Environment {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last commandResult
	for time.Now().Before(deadline) {
		last = harness.runTest("--env", "relay-e2e/local", "--json", "status")
		if last.Err == nil {
			var environment model.Environment
			if json.Unmarshal([]byte(last.Stdout), &environment) == nil && environment.Status == model.EnvironmentHealthy {
				return environment
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("environment did not recover after daemon restart: %v\n%s", last.Err, last.Output())
	return model.Environment{}
}

func relayRequest(t *testing.T, host, path string) *http.Response {
	t.Helper()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", relayruntime.DefaultListenAddress)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequest(http.MethodGet, "http://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func newRelayClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", relayruntime.DefaultListenAddress)
		},
		DisableKeepAlives: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second}
}

func assertProductionBrowserSession(t *testing.T, harness *machineHarness) {
	t.Helper()
	token, err := os.ReadFile(filepath.Join(harness.home, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	client := newRelayClient(t)
	request, err := http.NewRequest(http.MethodPost, "http://portless.localhost/api/v1/browser-claims", strings.NewReader(`{"next":"/projects"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var claim struct {
		URL string `json:"url"`
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create browser claim through relay returned %s: %s", response.Status, readResponse(t, response))
	}
	if err := json.NewDecoder(response.Body).Decode(&claim); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	claimURL, err := url.Parse(claim.URL)
	if err != nil || claimURL.Path == "" {
		t.Fatalf("invalid browser claim URL %q: %v", claim.URL, err)
	}
	claimURL.Scheme, claimURL.Host = "http", "portless.localhost"
	claimed, err := client.Get(claimURL.String())
	if err != nil {
		t.Fatal(err)
	}
	claimedBody := readResponse(t, claimed)
	if claimed.StatusCode != http.StatusOK || claimed.Request.URL.Path != "/projects" || !strings.Contains(claimedBody, `id="root"`) {
		t.Fatalf("browser claim did not establish a production relay session: status=%s url=%s body=%s", claimed.Status, claimed.Request.URL, claimedBody)
	}
	projects, err := client.Get("http://portless.localhost/api/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	projectsBody := readResponse(t, projects)
	if projects.StatusCode != http.StatusOK || !strings.Contains(projectsBody, `"name":"relay-e2e"`) {
		t.Fatalf("browser session did not authenticate API through relay: %s: %s", projects.Status, projectsBody)
	}
	reused, err := newRelayClient(t).Get(claimURL.String())
	if err != nil {
		t.Fatal(err)
	}
	reusedBody := readResponse(t, reused)
	if reused.StatusCode != http.StatusUnauthorized || !strings.Contains(reusedBody, "INVALID_BROWSER_CLAIM") {
		t.Fatalf("browser claim was reusable through production relay: %s: %s", reused.Status, reusedBody)
	}
}

func appendRelayResourceMarker(t *testing.T, checkout string) {
	t.Helper()
	path := filepath.Join(checkout, "apps", "checkout", ".env.example")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("REDIS_URL=redis://redis\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertCleanValkeyEndpoint(t *testing.T, harness *machineHarness) {
	t.Helper()
	result := harness.runTest("--env", "relay-e2e/local", "--json", "status")
	if result.Err != nil {
		t.Fatalf("inspect relay resource environment: %v\n%s", result.Err, result.Output())
	}
	var environment model.Environment
	decodeJSON(t, result.Stdout, &environment)
	var endpoint *model.Endpoint
	for _, service := range environment.Services {
		if service.Name != "checkout-redis" || service.Status != model.ServiceReady {
			continue
		}
		for index := range service.Endpoints {
			if service.Endpoints[index].Kind == model.EndpointPublic && service.Endpoints[index].Protocol == model.ProtocolTCP {
				endpoint = &service.Endpoints[index]
			}
		}
	}
	if endpoint == nil {
		t.Fatalf("ready Valkey clean endpoint not found: %#v", environment.Services)
	}
	addresses, err := net.LookupHost(endpoint.Host)
	if err != nil || len(addresses) == 0 {
		t.Fatalf("resolve clean Valkey host %s: addresses=%v err=%v", endpoint.Host, addresses, err)
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)), 5*time.Second)
	if err != nil {
		t.Fatalf("connect to clean Valkey endpoint %s: %v", endpoint.URL, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(connection, "*1\r\n$4\r\nPING\r\n"); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 7)
	if _, err := io.ReadFull(connection, response); err != nil || string(response) != "+PONG\r\n" {
		t.Fatalf("clean Valkey PING response = %q, err=%v", response, err)
	}
}

func waitForRelayStatus(t *testing.T, status int) *http.Response {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response := relayRequest(t, "portless.localhost", "/api/v1/health")
		if response.StatusCode == status {
			return response
		}
		_ = response.Body.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("relay did not return HTTP %d", status)
	return nil
}

func readResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func waitForNoTCPListener(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !listenerAccepting(address) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("listener remains on %s", address)
}

func listenerAccepting(address string) bool {
	connection, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = connection.Close()
	return true
}

func waitForResolverRemoval() error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := relayhealth.CheckResolver(ctx)
		cancel()
		if err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("system resolver still routes portless.test after relay uninstall")
}

func installationRoot(status relayinstallation.InstallationStatus) (string, error) {
	if !filepath.IsAbs(status.TargetSocket) || !filepath.IsAbs(status.DNSTargetSocket) {
		return "", errors.New("existing relay has non-absolute daemon socket targets")
	}
	root := filepath.Dir(status.TargetSocket)
	if root == "/" || root == "." || filepath.Dir(status.DNSTargetSocket) != root || filepath.Base(status.TargetSocket) != "ingress.sock" || filepath.Base(status.DNSTargetSocket) != "dns.sock" {
		return "", fmt.Errorf("existing relay targets are not a recognizable Portless home: %s and %s", status.TargetSocket, status.DNSTargetSocket)
	}
	return root, nil
}

func relayReceiptSchema(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var receipt struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(content, &receipt); err != nil {
		return 0, err
	}
	return receipt.SchemaVersion, nil
}

func acquireMachineLock() (*os.File, error) {
	path := "/tmp/portless-relay-e2e.lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("another destructive Portless relay E2E suite is running")
	}
	return file, nil
}

func isolatedEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PORTLESS_HOME=") || strings.HasPrefix(entry, "NO_COLOR=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "PORTLESS_HOME="+home, "NO_COLOR=1")
}

func decodeJSON(t *testing.T, content string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(content), target); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, content)
	}
}

func repositoryPath(elements ...string) string {
	_, source, _, _ := runtime.Caller(0)
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
