//go:build darwin

package installation

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/networking"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

func TestRenderLaunchdPlistEscapesSocketAndUsesFixedHelper(t *testing.T) {
	content, err := renderLaunchdPlist(SetupRequest{TargetSocket: "/Users/dev/a&b/ingress.sock", DNSTargetSocket: "/Users/dev/a&b/dns.sock", UID: 501, GID: 20})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("invalid launchd plist XML: %v\n%s", err, content)
	}
	text := string(content)
	for _, expected := range []string{launchdLabel, launchdHelperPath, "/Users/dev/a&amp;b/ingress.sock", "<string>501</string>", "<string>20</string>"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("plist did not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "/Users/dev/a&b/") {
		t.Fatal("socket path was not XML escaped")
	}
}

func TestDarwinInstallRollbackRestoresArtifactsAndPreviousServiceState(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "relay.plist")
	if err := os.WriteFile(artifact, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(artifact)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	if err := os.WriteFile(replacement, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, artifact); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedCommandRunner{}
	platform := darwinPlatform{commands: runner}
	if err := platform.rollbackInstall(context.Background(), transaction, relayServiceRunning, true, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(artifact)
	if err != nil || string(content) != "before" {
		t.Fatalf("artifact was not restored: content=%q err=%v", content, err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, expected := range []string{
		" bootout system/" + launchdLabel,
		" bootstrap system " + launchdPlistPath,
		" kickstart -k system/" + launchdLabel,
		"/usr/bin/dscacheutil -flushcache",
		"/usr/bin/killall -HUP mDNSResponder",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("rollback did not run %q:\n%s", expected, joined)
		}
	}
}

func TestDarwinRollbackRestartsPreviousServiceBeforeAnyArtifactChanged(t *testing.T) {
	runner := &scriptedCommandRunner{}
	platform := darwinPlatform{commands: runner}
	if err := platform.rollbackInstall(context.Background(), &artifactTransaction{}, relayServiceRunning, true, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, " bootstrap system "+launchdPlistPath) || !strings.Contains(joined, " kickstart -k system/"+launchdLabel) {
		t.Fatalf("rollback did not restore the previously running service:\n%s", joined)
	}
}

func TestLoopbackOwnershipAndStaleAddressReconciliation(t *testing.T) {
	configured := map[string]bool{"127.77.0.1": true, "127.77.0.2": true}
	if conflict := firstUnownedLoopbackAddress(configured, []string{"127.77.0.1", "127.77.0.2"}, []string{"127.77.0.1"}); conflict != "127.77.0.2" {
		t.Fatalf("conflict = %q, want 127.77.0.2", conflict)
	}
	if conflict := firstUnownedLoopbackAddress(configured, []string{"127.77.0.1", "127.77.0.2"}, []string{"127.77.0.2", "127.77.0.1"}); conflict != "" {
		t.Fatalf("owned address was reported as a conflict: %q", conflict)
	}
	obsolete := loopbackAddressDifference([]string{"127.77.0.1", "127.77.0.2", "127.77.0.9"}, []string{"127.77.0.1", "127.77.0.2", "127.77.0.3"})
	if len(obsolete) != 1 || obsolete[0] != "127.77.0.9" {
		t.Fatalf("obsolete addresses = %#v, want [127.77.0.9]", obsolete)
	}
}

func TestResidualPoolDetectionIncludesObsoleteReservedAliases(t *testing.T) {
	configured := map[string]bool{"127.77.0.200": true, "127.0.0.1": true, "203.0.113.10": true}
	if count := reservedRelayLoopbackAddressCount(configured); count != 1 {
		t.Fatalf("reserved relay loopback count = %d, want 1", count)
	}
}

func TestManagedRelayLoopbackAddressesReserveDNSOutsideEndpointPool(t *testing.T) {
	addresses := managedRelayLoopbackAddresses()
	if len(addresses) != networking.EndpointPoolSize+1 || addresses[0] != "127.77.0.1" || addresses[1] != "127.77.0.2" {
		t.Fatalf("unexpected managed loopback addresses: %#v", addresses)
	}
}

func TestDarwinResolverUsesDedicatedPortlessDNSPort(t *testing.T) {
	if actual := string(renderDarwinResolverConfiguration()); actual != "nameserver 127.77.0.1\nport 1053\n" {
		t.Fatalf("unexpected resolver configuration: %q", actual)
	}
	if actual := string(renderDarwinLocalhostResolverConfiguration()); actual != "domain localhost\nnameserver 127.77.0.1\nport 1053\n" {
		t.Fatalf("unexpected localhost resolver configuration: %q", actual)
	}
	details := (darwinPlatform{}).installation()
	if details.ResolverPath != "/etc/resolver/portless.test" || details.LocalhostResolverPath != "/etc/resolver/portless.localhost" {
		t.Fatalf("unexpected resolver paths: %#v", details.resolverPaths())
	}
}

func TestWaitForLaunchdUnloadedAllowsAsynchronousBootout(t *testing.T) {
	calls := 0
	err := waitForLaunchdUnloaded(context.Background(), time.Second, func(context.Context) (relayServiceState, error) {
		calls++
		if calls < 3 {
			return relayServiceRunning, nil
		}
		return relayServiceStopped, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("launchd probe calls = %d, want 3", calls)
	}
}

func TestWaitForLaunchdUnloadedReportsProbeAndTimeoutFailures(t *testing.T) {
	probeError := errors.New("launchctl unavailable")
	if err := waitForLaunchdUnloaded(context.Background(), time.Second, func(context.Context) (relayServiceState, error) {
		return relayServiceUnknown, probeError
	}); !errors.Is(err, probeError) {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if err := waitForLaunchdUnloaded(context.Background(), 0, func(context.Context) (relayServiceState, error) {
		return relayServiceRunning, nil
	}); err == nil || !strings.Contains(err.Error(), "still loaded after") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}

func TestDarwinServiceStateDistinguishesMissingFromProbeFailure(t *testing.T) {
	missing := &scriptedCommandRunner{responses: []commandResponse{{output: "Could not find service", err: testCommandExitError(113)}}}
	state, err := (darwinPlatform{commands: missing}).serviceState(context.Background())
	if err != nil || state != relayServiceStopped {
		t.Fatalf("missing service state=%v err=%v", state, err)
	}
	failing := &scriptedCommandRunner{responses: []commandResponse{{output: "permission denied", err: testCommandExitError(1)}}}
	state, err = (darwinPlatform{commands: failing}).serviceState(context.Background())
	if err == nil || state != relayServiceUnknown || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("failed probe state=%v err=%v", state, err)
	}
}

func TestDarwinInstallCommitsOnlyAfterEndToEndReadiness(t *testing.T) {
	events := []string{}
	started := false
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "print" {
			if started {
				return nil, nil
			}
			return []byte("Could not find service"), testCommandExitError(113)
		}
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "kickstart" {
			started = true
		}
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "bootout" {
			started = false
		}
		return nil, nil
	})
	details := (darwinPlatform{}).installation()
	directory := t.TempDir()
	details.HelperPath = filepath.Join(directory, "helper")
	details.ConfigurationPath = filepath.Join(directory, "relay.plist")
	details.ReceiptPath = filepath.Join(directory, "relay.json")
	details.ResolverPath = filepath.Join(directory, "portless.test")
	details.LocalhostResolverPath = filepath.Join(directory, "portless.localhost")
	platform := darwinPlatform{
		commands: runner, details: &details, operations: recordingPlatformOperations(&events, nil),
		prepareLoopbackPoolFunc: func(context.Context, commandRunner, []string) ([]string, error) {
			events = append(events, "prepare-pool")
			return []string{"127.77.0.2"}, nil
		},
		removeLoopbackPoolFunc: func(_ context.Context, _ commandRunner, addresses []string) ([]string, error) {
			events = append(events, "remove-pool:"+strings.Join(addresses, ","))
			return append([]string(nil), addresses...), nil
		},
		addLoopbackAddressesFunc: func(_ context.Context, _ commandRunner, addresses []string) error {
			events = append(events, "add-pool:"+strings.Join(addresses, ","))
			return nil
		},
	}
	request := SetupRequest{Executable: "/tmp/portless", TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock", UID: 501, GID: 20}
	if err := platform.install(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	readinessIndex, commitIndex := eventIndex(events, "readiness"), eventIndex(events, "commit")
	if readinessIndex < 0 || commitIndex < 0 || readinessIndex > commitIndex || eventIndex(events, "rollback") >= 0 {
		t.Fatalf("installation did not commit after readiness: %#v", events)
	}
}

func TestDarwinInstallRollsBackWhenReadinessFails(t *testing.T) {
	readinessError := errors.New("resolver did not become ready")
	events := []string{}
	started := false
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "print" {
			if started {
				return nil, nil
			}
			return []byte("Could not find service"), testCommandExitError(113)
		}
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "kickstart" {
			started = true
		}
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "bootout" {
			started = false
		}
		return nil, nil
	})
	platform := darwinPlatform{
		commands: runner, operations: recordingPlatformOperations(&events, readinessError),
		prepareLoopbackPoolFunc: func(context.Context, commandRunner, []string) ([]string, error) {
			return nil, nil
		},
		removeLoopbackPoolFunc: func(context.Context, commandRunner, []string) ([]string, error) {
			return nil, nil
		},
		addLoopbackAddressesFunc: func(context.Context, commandRunner, []string) error { return nil },
	}
	request := SetupRequest{Executable: "/tmp/portless", TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock", UID: 501, GID: 20}
	err := platform.install(context.Background(), request)
	if !errors.Is(err, readinessError) || eventIndex(events, "rollback") < eventIndex(events, "readiness") || eventIndex(events, "commit") >= 0 {
		t.Fatalf("readiness failure did not roll back the installation: err=%v events=%#v", err, events)
	}
}

func TestDarwinRuntimeValidatesReceiptBeforeProvisioningAliases(t *testing.T) {
	receipt := installationReceipt{
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock",
		LoopbackAddresses: managedRelayLoopbackAddresses(),
	}
	prepared := false
	platform := darwinPlatform{
		operations: platformOperations{readReceiptFunc: func(platformInstallation) (installationReceipt, error) { return receipt, nil }},
		prepareLoopbackPoolFunc: func(context.Context, commandRunner, []string) ([]string, error) {
			prepared = true
			return nil, nil
		},
	}
	config := relayruntime.Identity{TargetSocket: receipt.TargetSocket, DNSTargetSocket: receipt.DNSTargetSocket, UID: receipt.OwnerUID, GID: receipt.OwnerGID}
	mismatch := config
	mismatch.UID++
	if err := platform.prepareRuntime(context.Background(), mismatch); err == nil || prepared {
		t.Fatalf("mismatched receipt reached alias provisioning: err=%v prepared=%v", err, prepared)
	}
	if err := platform.prepareRuntime(context.Background(), config); err != nil || !prepared {
		t.Fatalf("matching receipt was not prepared: err=%v prepared=%v", err, prepared)
	}
}

func TestDarwinExpectedArtifactsAreDerivedFromReceipt(t *testing.T) {
	receipt := installationReceipt{
		OwnerUID: 501, OwnerGID: 20, TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock",
	}
	artifacts, err := (darwinPlatform{}).expectedArtifacts(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 3 || !strings.Contains(string(artifacts[0].content), receipt.TargetSocket) || !strings.Contains(string(artifacts[0].content), "<string>501</string>") {
		t.Fatalf("unexpected expected Darwin artifacts: %#v", artifacts)
	}
}

func TestDarwinRestartUsesFixedInstalledService(t *testing.T) {
	runner := &scriptedCommandRunner{}
	if err := (darwinPlatform{commands: runner}).restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "kickstart -k system/"+launchdLabel) {
		t.Fatalf("unexpected launchd restart calls: %#v", runner.calls)
	}
}

func TestDarwinUninstallRemovesReceiptOnlyAfterResolverRefresh(t *testing.T) {
	events := []string{}
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "print" {
			return []byte("Could not find service"), testCommandExitError(113)
		}
		return nil, nil
	})
	details := (darwinPlatform{}).installation()
	directory := t.TempDir()
	details.HelperPath = filepath.Join(directory, "helper")
	details.ConfigurationPath = filepath.Join(directory, "relay.plist")
	details.ReceiptPath = filepath.Join(directory, "relay.json")
	details.ResolverPath = filepath.Join(directory, "portless.test")
	details.LocalhostResolverPath = filepath.Join(directory, "portless.localhost")
	platform := darwinPlatform{
		commands: runner, details: &details, operations: recordingPlatformOperations(&events, nil),
		removeLoopbackPoolFunc: func(_ context.Context, _ commandRunner, addresses []string) ([]string, error) {
			events = append(events, "remove-pool:"+strings.Join(addresses, ","))
			return append([]string(nil), addresses...), nil
		},
	}
	if err := platform.uninstall(context.Background(), uninstallSpec{loopbackAddresses: []string{"127.77.0.1"}}); err != nil {
		t.Fatal(err)
	}
	receiptIndex := eventIndex(events, "remove:"+details.ReceiptPath)
	resolverRefreshIndex := eventIndex(events, "command:/usr/bin/killall -HUP mDNSResponder")
	if receiptIndex < 0 || resolverRefreshIndex < 0 || receiptIndex < resolverRefreshIndex {
		t.Fatalf("receipt was not removed last: %#v", events)
	}
}

func TestDarwinUninstallPreservesReceiptWhenResolverRefreshFails(t *testing.T) {
	events := []string{}
	refreshError := errors.New("mDNSResponder unavailable")
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable == "/bin/launchctl" && len(arguments) > 0 && arguments[0] == "print" {
			return []byte("Could not find service"), testCommandExitError(113)
		}
		if executable == "/usr/bin/killall" {
			return nil, refreshError
		}
		return nil, nil
	})
	details := (darwinPlatform{}).installation()
	platform := darwinPlatform{commands: runner, details: &details, operations: recordingPlatformOperations(&events, nil)}
	err := platform.uninstall(context.Background(), uninstallSpec{})
	if !errors.Is(err, refreshError) || eventIndex(events, "remove:"+details.ReceiptPath) >= 0 {
		t.Fatalf("failed resolver refresh removed the ownership receipt: err=%v events=%#v", err, events)
	}
}
