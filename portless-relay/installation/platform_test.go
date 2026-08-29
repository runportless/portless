package installation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	systeminstallation "github.com/runportless/portless/portless-daemon/system/installation"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

type commandResponse struct {
	output string
	err    error
}

type scriptedCommandRunner struct {
	mutex     sync.Mutex
	responses []commandResponse
	calls     []string
}

type commandRunnerFunc func(context.Context, string, ...string) ([]byte, error)

func (run commandRunnerFunc) combinedOutput(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	return run(ctx, executable, arguments...)
}

func (runner *scriptedCommandRunner) combinedOutput(_ context.Context, executable string, arguments ...string) ([]byte, error) {
	runner.mutex.Lock()
	defer runner.mutex.Unlock()
	runner.calls = append(runner.calls, strings.Join(append([]string{executable}, arguments...), " "))
	if len(runner.responses) == 0 {
		return nil, nil
	}
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	return []byte(response.output), response.err
}

type testCommandExitError int

func (err testCommandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", err)
}
func (err testCommandExitError) ExitCode() int { return int(err) }

type fakeHostPlatform struct {
	details            platformInstallation
	state              relayServiceState
	stateErr           error
	pool               endpointPoolStatus
	poolErr            error
	expected           []expectedArtifact
	expectedErr        error
	installFunc        func(context.Context, SetupRequest) error
	restartFunc        func(context.Context) error
	uninstallFunc      func(context.Context, uninstallSpec) error
	prepareRuntimeFunc func(context.Context, relayruntime.Identity) error
}

func (platform *fakeHostPlatform) installation() platformInstallation { return platform.details }
func (platform *fakeHostPlatform) install(ctx context.Context, request SetupRequest) error {
	if platform.installFunc != nil {
		return platform.installFunc(ctx, request)
	}
	return nil
}
func (platform *fakeHostPlatform) restart(ctx context.Context) error {
	if platform.restartFunc != nil {
		return platform.restartFunc(ctx)
	}
	return nil
}
func (platform *fakeHostPlatform) uninstall(ctx context.Context, spec uninstallSpec) error {
	if platform.uninstallFunc != nil {
		return platform.uninstallFunc(ctx, spec)
	}
	return nil
}
func (platform *fakeHostPlatform) prepareRuntime(ctx context.Context, config relayruntime.Identity) error {
	if platform.prepareRuntimeFunc != nil {
		return platform.prepareRuntimeFunc(ctx, config)
	}
	return nil
}
func (platform *fakeHostPlatform) expectedArtifacts(installationReceipt) ([]expectedArtifact, error) {
	return platform.expected, platform.expectedErr
}
func (platform *fakeHostPlatform) serviceState(context.Context) (relayServiceState, error) {
	return platform.state, platform.stateErr
}
func (platform *fakeHostPlatform) loopbackPoolStatus() (endpointPoolStatus, error) {
	pool := platform.pool
	if pool.detail == "" {
		pool.detail = "fixture pool"
	}
	return pool, platform.poolErr
}

func successfulInspectionProbes() inspectionProbes {
	return inspectionProbes{
		http:     func(context.Context) error { return nil },
		dns:      func(context.Context) error { return nil },
		resolver: func(context.Context) error { return nil },
	}
}

func noLockPrivilegedDependencies() privilegedLifecycleDependencies {
	return privilegedLifecycleDependencies{
		effectiveUID: func() int { return 0 },
		withLock: func(_ context.Context, _ platformInstallation, operation func() error) error {
			return operation()
		},
	}
}

func recordingPlatformOperations(events *[]string, readinessErr error) platformOperations {
	return platformOperations{
		beginArtifactTransactionFunc: func(paths ...string) (*artifactTransaction, error) {
			*events = append(*events, "begin:"+strings.Join(paths, ","))
			return &artifactTransaction{
				rollbackFunc: func() error {
					*events = append(*events, "rollback")
					return nil
				},
				commitFunc: func() error {
					*events = append(*events, "commit")
					return nil
				},
			}, nil
		},
		copyExecutableFunc: func(_, destination string) error {
			*events = append(*events, "copy:"+destination)
			return nil
		},
		writeRootFileFunc: func(destination string, _ []byte, _ os.FileMode) error {
			*events = append(*events, "write:"+destination)
			return nil
		},
		writeReceiptFunc: func(_ platformInstallation, _ SetupRequest) error {
			*events = append(*events, "receipt")
			return nil
		},
		pathExistsFunc: func(string) (bool, error) { return false, nil },
		removeFileFunc: func(path string) error {
			*events = append(*events, "remove:"+path)
			return nil
		},
		waitForAddressesFunc: func(context.Context, time.Duration) error {
			*events = append(*events, "addresses-ready")
			return nil
		},
		waitUntilReadyFunc: func(context.Context, time.Duration) error {
			*events = append(*events, "readiness")
			return readinessErr
		},
	}
}

func eventIndex(events []string, target string) int {
	for index, event := range events {
		if event == target {
			return index
		}
	}
	return -1
}

func TestInspectDoesNotInferOwnershipWithoutReceipt(t *testing.T) {
	directory := t.TempDir()
	configuration := filepath.Join(directory, "relay.service")
	if err := os.WriteFile(configuration, []byte("owner=501"), 0o644); err != nil {
		t.Fatal(err)
	}
	platform := &fakeHostPlatform{
		details: platformInstallation{
			Name: "fixture", Service: "relay", ConfigurationPath: configuration,
			ReceiptPath: filepath.Join(directory, "receipt.json"), ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
		},
		state: relayServiceStopped, pool: endpointPoolStatus{ready: true},
	}
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.OwnerUID != 0 || !strings.Contains(status.Problem, "receipt is missing") {
		t.Fatalf("configuration-only installation was treated as owned: %#v", status)
	}
	if err := ValidateOwnership(status, os.Geteuid()); err == nil {
		t.Fatal("configuration-only installation passed ownership validation")
	}
	lifecycleStatus, err := inspectInstallation(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleStatus.HTTPHealthy || lifecycleStatus.DNSHealthy || lifecycleStatus.ResolverHealthy {
		t.Fatalf("lifecycle inspection unexpectedly ran network health probes: %#v", lifecycleStatus)
	}
	if err := validateInstallOwnership(context.Background(), platform, os.Geteuid()); err == nil || !strings.Contains(err.Error(), "refuse to replace") {
		t.Fatalf("installation replaced an unowned partial relay: %v", err)
	}
}

func TestInspectFailsClosedWhenServiceStateIsUnknown(t *testing.T) {
	probeError := errors.New("service manager unavailable")
	platform := &fakeHostPlatform{state: relayServiceUnknown, stateErr: probeError, pool: endpointPoolStatus{ready: true}}
	if _, err := inspect(context.Background(), platform, successfulInspectionProbes()); !errors.Is(err, probeError) {
		t.Fatalf("inspect error = %v, want %v", err, probeError)
	}
}

func TestInspectDoesNotReportStoppedOrIncompleteInstallationHealthy(t *testing.T) {
	directory := t.TempDir()
	configuration := filepath.Join(directory, "relay.service")
	resolver := filepath.Join(directory, "resolver.conf")
	for _, path := range []string{configuration, resolver} {
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	platform := &fakeHostPlatform{
		details: platformInstallation{
			Name: "fixture", Service: "relay", ConfigurationPath: configuration,
			ReceiptPath: filepath.Join(directory, "receipt.json"), ResolverPath: resolver,
			ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
		},
		state: relayServiceStopped, pool: endpointPoolStatus{ready: true},
	}
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if status.Healthy || !status.HTTPHealthy || !status.DNSHealthy || !status.ResolverHealthy {
		t.Fatalf("stopped partial installation health = %#v", status)
	}
}

func TestInspectReportsUnverifiedResidualEndpointPool(t *testing.T) {
	platform := &fakeHostPlatform{
		state: relayServiceStopped,
		pool:  endpointPoolStatus{ready: true, configured: true, managed: true, detail: "64/64 endpoint addresses configured on lo0"},
	}
	status, err := inspectInstallation(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	if status.Installed || !status.EndpointPoolResidual || status.State() != "not installed; residual endpoint pool" || !strings.Contains(status.Problem, "will not remove unverified aliases") {
		t.Fatalf("residual endpoint pool was not reported safely: %#v", status)
	}
}

func TestInspectHelperIdentityIgnoresUnrelatedCurrentExecutableBuild(t *testing.T) {
	platform, _, receipt := helperInspectionFixture(t, relayruntime.HelperVersion)
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Healthy || !status.HelperVerified || !status.HelperCompatible || status.HelperBuildID != receipt.HelperBuildID || status.HelperVersion != relayruntime.HelperVersion || status.RequiredHelperVersion != relayruntime.HelperVersion {
		t.Fatalf("receipt-bound helper was not healthy independently of the current executable: %#v", status)
	}
}

func TestInspectReportsHelperVersionDriftSeparatelyFromIntegrity(t *testing.T) {
	platform, _, _ := helperInspectionFixture(t, "0.9.0")
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if status.Healthy || !status.HelperVerified || status.HelperCompatible || status.HelperVersion != "0.9.0" || !strings.Contains(status.HelperError, "does not match required version") {
		t.Fatalf("helper version drift was not classified independently: %#v", status)
	}
}

func TestInspectReportsHelperContentTamperingSeparatelyFromCompatibility(t *testing.T) {
	platform, details, receipt := helperInspectionFixture(t, relayruntime.HelperVersion)
	if err := os.WriteFile(details.HelperPath, []byte("replaced helper content"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if status.Healthy || status.HelperVerified || !status.HelperCompatible || status.HelperBuildID == receipt.HelperBuildID || !strings.Contains(status.HelperError, "does not match its ownership receipt") {
		t.Fatalf("helper content replacement was not classified as an integrity failure: %#v", status)
	}
}

func TestInspectRejectsUnsupportedReceiptSchema(t *testing.T) {
	platform, details, receipt := helperInspectionFixture(t, relayruntime.HelperVersion)
	receipt.SchemaVersion = 3
	receipt.HelperVersion = ""
	receipt.HelperBuildID = ""
	writeTestReceipt(t, details, receipt)
	status, err := inspectInstallation(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	if status.OwnerUID != 0 || status.HelperVerified || status.HelperCompatible || !status.EndpointPoolResidual || !strings.Contains(status.Problem, "unsupported relay installation receipt schema 3") {
		t.Fatalf("unsupported receipt was not rejected fail-closed: %#v", status)
	}
	if err := validateInstallOwnership(context.Background(), platform, 501); err == nil || !strings.Contains(err.Error(), "owner could not be determined") {
		t.Fatalf("unsupported receipt authorized reinstall: %v", err)
	}
}

func TestInspectReportsServiceConfigurationDriftFromReceipt(t *testing.T) {
	platform, details, _ := helperInspectionFixture(t, relayruntime.HelperVersion)
	if err := os.WriteFile(details.ConfigurationPath, []byte("stale service configuration"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := inspectInstallation(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	if status.OwnerUID != 501 || !status.HelperVerified || !status.HelperCompatible || !strings.Contains(status.ConfigurationError, "content does not match the ownership receipt") || !strings.Contains(status.Problem, "content does not match the ownership receipt") {
		t.Fatalf("service configuration drift was not reported: %#v", status)
	}
}

func TestPrivilegedInstallHoldsLifecycleLockAcrossRecheckAndMutation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	locked := false
	installed := false
	platform := &fakeHostPlatform{
		state: relayServiceStopped,
		installFunc: func(context.Context, SetupRequest) error {
			if !locked {
				t.Fatal("relay installation ran outside the lifecycle lock")
			}
			installed = true
			return nil
		},
	}
	dependencies := privilegedLifecycleDependencies{
		effectiveUID: func() int { return 0 },
		withLock: func(_ context.Context, _ platformInstallation, operation func() error) error {
			locked = true
			defer func() { locked = false }()
			return operation()
		},
	}
	err = installPrivilegedWithDependencies(
		context.Background(), platform, executable, "/tmp/portless/ingress.sock", "/tmp/portless/dns.sock", 501, 20, dependencies,
	)
	if err != nil || !installed || locked {
		t.Fatalf("privileged install err=%v installed=%v lockedAfter=%v", err, installed, locked)
	}
}

func TestPrivilegedRestartRechecksReceiptOwnershipInsideLock(t *testing.T) {
	directory := t.TempDir()
	details := platformInstallation{
		Name: "fixture", Service: "relay", HelperPath: filepath.Join(directory, "helper"),
		ConfigurationPath: filepath.Join(directory, "config"), ReceiptPath: filepath.Join(directory, "relay.json"),
		ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
	}
	writeTestReceipt(t, details, installationReceipt{
		SchemaVersion: installationReceiptSchema, Platform: details.Name, Service: details.Service,
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock",
		LoopbackAddresses: managedRelayLoopbackAddresses(), HelperPath: details.HelperPath, ConfigurationPath: details.ConfigurationPath,
		InstalledAt: time.Now().UTC(),
	})
	restarted := false
	platform := &fakeHostPlatform{
		details: details, state: relayServiceRunning, pool: endpointPoolStatus{ready: true},
		restartFunc: func(context.Context) error {
			restarted = true
			return nil
		},
	}
	if err := restartPrivilegedWithDependencies(context.Background(), platform, 501, noLockPrivilegedDependencies()); err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("privileged restart did not reach the platform after ownership recheck")
	}
	if err := restartPrivilegedWithDependencies(context.Background(), platform, 502, noLockPrivilegedDependencies()); err == nil || !strings.Contains(err.Error(), "belongs to user ID 501") {
		t.Fatalf("privileged restart accepted another user: %v", err)
	}
}

func TestPrivilegedUninstallReportsAliasesLeftWithoutReceipt(t *testing.T) {
	directory := t.TempDir()
	configuration := filepath.Join(directory, "relay.service")
	if err := os.WriteFile(configuration, []byte("partial installation"), 0o644); err != nil {
		t.Fatal(err)
	}
	platform := &fakeHostPlatform{
		details: platformInstallation{
			Name: "fixture", Service: "relay", ConfigurationPath: configuration,
			ReceiptPath: filepath.Join(directory, "missing-receipt.json"), ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
		},
		state: relayServiceStopped,
		pool:  endpointPoolStatus{ready: true, configured: true, managed: true, detail: "reserved aliases configured"},
	}
	platform.uninstallFunc = func(_ context.Context, spec uninstallSpec) error {
		if len(spec.loopbackAddresses) != 0 {
			t.Fatalf("unverified loopback aliases were authorized for removal: %#v", spec.loopbackAddresses)
		}
		return os.Remove(configuration)
	}
	err := uninstallPrivilegedWithDependencies(context.Background(), platform, 501, true, noLockPrivilegedDependencies())
	if err == nil || !strings.Contains(err.Error(), "reserved loopback aliases remain") {
		t.Fatalf("residual alias cleanup was reported as successful: %v", err)
	}
}

func writeTestReceipt(t *testing.T, details platformInstallation, receipt installationReceipt) {
	t.Helper()
	if receipt.SchemaVersion == installationReceiptSchema {
		if receipt.HelperVersion == "" {
			receipt.HelperVersion = relayruntime.HelperVersion
		}
		if receipt.HelperBuildID == "" {
			if buildID, err := systeminstallation.BuildIDForPath(details.HelperPath); err == nil {
				receipt.HelperBuildID = buildID
			} else {
				receipt.HelperBuildID = strings.Repeat("0", 64)
			}
		}
	}
	content, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(details.ReceiptPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func helperInspectionFixture(t *testing.T, helperVersion string) (*fakeHostPlatform, platformInstallation, installationReceipt) {
	t.Helper()
	directory := t.TempDir()
	details := platformInstallation{
		Name: "fixture", Service: "relay", HelperPath: filepath.Join(directory, "helper"),
		ConfigurationPath: filepath.Join(directory, "relay.service"), ReceiptPath: filepath.Join(directory, "relay.json"),
		ResolverPath: filepath.Join(directory, "resolver.conf"), ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
	}
	helperBuildID := writeTestHelper(t, details.HelperPath, "fixture relay helper independent of the current test executable")
	for path, content := range map[string]string{
		details.ConfigurationPath: "expected service configuration",
		details.ResolverPath:      "expected resolver",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	receipt := installationReceipt{
		SchemaVersion: installationReceiptSchema, Platform: details.Name, Service: details.Service,
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock",
		LoopbackAddresses: managedRelayLoopbackAddresses(), HelperPath: details.HelperPath,
		HelperVersion: helperVersion, HelperBuildID: helperBuildID, ConfigurationPath: details.ConfigurationPath,
		InstalledAt: time.Now().UTC(),
	}
	writeTestReceipt(t, details, receipt)
	platform := &fakeHostPlatform{
		details: details, state: relayServiceRunning,
		pool: endpointPoolStatus{ready: true, configured: true, managed: true},
		expected: []expectedArtifact{
			{path: details.ConfigurationPath, content: []byte("expected service configuration")},
			{path: details.ResolverPath, content: []byte("expected resolver")},
		},
	}
	return platform, details, receipt
}

func writeTestHelper(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	buildID, err := systeminstallation.BuildIDForPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return buildID
}

func TestArtifactTransactionRestoresExistingAndRemovesNewFiles(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "existing")
	created := filepath.Join(directory, "created")
	if err := os.WriteFile(existing, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(existing, created)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	if err := os.WriteFile(replacement, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, existing); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(existing)
	if err != nil || string(content) != "before" {
		t.Fatalf("existing artifact was not restored: content=%q err=%v", content, err)
	}
	if _, err := os.Lstat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new artifact remains after rollback: %v", err)
	}
}

func TestArtifactTransactionRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := beginArtifactTransaction(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink transaction error = %v", err)
	}
}

func TestArtifactTransactionPreservesBackupWhenRestoreCannotReplaceTarget(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "artifact")
	if err := os.WriteFile(artifact, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err == nil || !strings.Contains(err.Error(), "preserved backup") {
		t.Fatalf("restore obstruction error = %v", err)
	}
	backupPath := transaction.backups[0].backupPath
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("recovery backup was not preserved at %s: %v", backupPath, err)
	}
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(artifact)
	if err != nil || string(content) != "before" {
		t.Fatalf("retry did not restore preserved backup: content=%q err=%v", content, err)
	}
}

func TestArtifactTransactionCommitDiscardsBackupWithoutChangingArtifact(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(artifact, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(artifact)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := transaction.backups[0].backupPath
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed transaction retained backup: %v", err)
	}
	content, err := os.ReadFile(artifact)
	if err != nil || string(content) != "current" {
		t.Fatalf("commit changed artifact: content=%q err=%v", content, err)
	}
}
