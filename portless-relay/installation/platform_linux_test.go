//go:build linux

package installation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

func TestRenderSystemdUnitEscapesSpecifiersAndHardensService(t *testing.T) {
	request := SetupRequest{TargetSocket: "/home/dev/Portless % Data/ingress.sock", DNSTargetSocket: "/home/dev/Portless % Data/dns.sock", UID: 1000, GID: 1000}
	unit := string(renderSystemdUnit(request))
	for _, expected := range []string{
		`--socket "/home/dev/Portless %% Data/ingress.sock"`,
		`--dns-socket "/home/dev/Portless %% Data/dns.sock"`,
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"RestrictAddressFamilies=AF_UNIX AF_INET",
		"CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SETGID CAP_SETUID",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("systemd unit does not contain %q:\n%s", expected, unit)
		}
	}
}

func TestLinuxInstallWritesReceiptBeforeActivationAndCommitsAfterReadiness(t *testing.T) {
	events := []string{}
	started := false
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable != "/usr/bin/systemctl" || len(arguments) == 0 {
			return nil, nil
		}
		switch {
		case arguments[0] == "is-active" && arguments[len(arguments)-1] == "systemd-resolved.service":
			return nil, nil
		case arguments[0] == "is-active":
			if started {
				return nil, nil
			}
			return nil, testCommandExitError(3)
		case arguments[0] == "is-enabled":
			return nil, testCommandExitError(1)
		case arguments[0] == "restart" && arguments[len(arguments)-1] == systemdUnitName:
			started = true
		case arguments[0] == "stop" && arguments[len(arguments)-1] == systemdUnitName:
			started = false
		}
		return nil, nil
	})
	platform := linuxPlatform{commands: runner, operations: recordingPlatformOperations(&events, nil)}
	request := SetupRequest{Executable: "/tmp/portless", TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock", UID: 1000, GID: 1000}
	if err := platform.install(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	receiptIndex := eventIndex(events, "receipt")
	restartIndex := eventIndex(events, "command:/usr/bin/systemctl restart "+systemdUnitName)
	readinessIndex, commitIndex := eventIndex(events, "readiness"), eventIndex(events, "commit")
	if receiptIndex < 0 || restartIndex < 0 || receiptIndex > restartIndex || readinessIndex < restartIndex || commitIndex < readinessIndex || eventIndex(events, "rollback") >= 0 {
		t.Fatalf("Linux activation ordering is unsafe: %#v", events)
	}
}

func TestLinuxInstallRollsBackWhenReadinessFails(t *testing.T) {
	readinessError := errors.New("relay did not become ready")
	events := []string{}
	started := false
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable != "/usr/bin/systemctl" || len(arguments) == 0 {
			return nil, nil
		}
		switch {
		case arguments[0] == "is-active" && arguments[len(arguments)-1] == "systemd-resolved.service":
			return nil, nil
		case arguments[0] == "is-active":
			if started {
				return nil, nil
			}
			return nil, testCommandExitError(3)
		case arguments[0] == "is-enabled":
			return nil, testCommandExitError(1)
		case arguments[0] == "restart" && arguments[len(arguments)-1] == systemdUnitName:
			started = true
		case arguments[0] == "stop" && arguments[len(arguments)-1] == systemdUnitName:
			started = false
		}
		return nil, nil
	})
	platform := linuxPlatform{commands: runner, operations: recordingPlatformOperations(&events, readinessError)}
	request := SetupRequest{Executable: "/tmp/portless", TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock", UID: 1000, GID: 1000}
	err := platform.install(context.Background(), request)
	if !errors.Is(err, readinessError) || eventIndex(events, "rollback") < eventIndex(events, "readiness") || eventIndex(events, "commit") >= 0 {
		t.Fatalf("readiness failure did not roll back Linux installation: err=%v events=%#v", err, events)
	}
}

func TestLinuxRuntimeRequiresMatchingOwnershipReceipt(t *testing.T) {
	details := platformInstallation{HelperPath: filepath.Join(t.TempDir(), "relay-helper")}
	helperBuildID := writeTestHelper(t, details.HelperPath, "linux relay helper")
	receipt := installationReceipt{
		SchemaVersion: installationReceiptSchema, HelperVersion: relayruntime.HelperVersion, HelperBuildID: helperBuildID,
		OwnerUID: 1000, OwnerGID: 1000, TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock",
		LoopbackAddresses: managedRelayLoopbackAddresses(),
	}
	platform := linuxPlatform{details: &details, operations: platformOperations{readReceiptFunc: func(platformInstallation) (installationReceipt, error) { return receipt, nil }}}
	config := relayruntime.Identity{TargetSocket: receipt.TargetSocket, DNSTargetSocket: receipt.DNSTargetSocket, UID: receipt.OwnerUID, GID: receipt.OwnerGID}
	if err := platform.prepareRuntime(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	config.DNSTargetSocket = "/tmp/other/dns.sock"
	if err := platform.prepareRuntime(context.Background(), config); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched Linux runtime identity was accepted: %v", err)
	}
}

func TestLinuxExpectedArtifactsAreDerivedFromReceipt(t *testing.T) {
	receipt := installationReceipt{
		OwnerUID: 1000, OwnerGID: 1000, TargetSocket: "/tmp/portless/ingress.sock", DNSTargetSocket: "/tmp/portless/dns.sock",
	}
	artifacts, err := (linuxPlatform{}).expectedArtifacts(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || !strings.Contains(string(artifacts[0].content), receipt.TargetSocket) || !strings.Contains(string(artifacts[0].content), "--uid 1000") {
		t.Fatalf("unexpected expected Linux artifacts: %#v", artifacts)
	}
}

func TestLinuxRestartUsesFixedInstalledService(t *testing.T) {
	runner := &scriptedCommandRunner{}
	if err := (linuxPlatform{commands: runner}).restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "restart "+systemdUnitName) {
		t.Fatalf("unexpected systemd restart calls: %#v", runner.calls)
	}
}

func TestLinuxUninstallRemovesReceiptLast(t *testing.T) {
	events := []string{}
	runner := commandRunnerFunc(func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
		call := strings.Join(append([]string{executable}, arguments...), " ")
		events = append(events, "command:"+call)
		if executable == "/usr/bin/systemctl" && len(arguments) > 0 && arguments[0] == "is-active" {
			return nil, testCommandExitError(3)
		}
		return nil, nil
	})
	details := (linuxPlatform{}).installation()
	directory := t.TempDir()
	details.HelperPath = filepath.Join(directory, "helper")
	details.ConfigurationPath = filepath.Join(directory, "relay.service")
	details.ReceiptPath = filepath.Join(directory, "relay.json")
	details.ResolverPath = filepath.Join(directory, "resolver.conf")
	platform := linuxPlatform{commands: runner, details: &details, operations: recordingPlatformOperations(&events, nil)}
	if err := platform.uninstall(context.Background(), uninstallSpec{}); err != nil {
		t.Fatal(err)
	}
	receiptIndex := eventIndex(events, "remove:"+details.ReceiptPath)
	helperIndex := eventIndex(events, "remove:"+details.HelperPath)
	resolverRefreshIndex := eventIndex(events, "command:/usr/bin/systemctl restart systemd-resolved.service")
	if receiptIndex < 0 || helperIndex < 0 || resolverRefreshIndex < 0 || receiptIndex < helperIndex || receiptIndex < resolverRefreshIndex {
		t.Fatalf("Linux receipt was not removed last: %#v", events)
	}
}

func TestLinuxInstallRollbackRestoresArtifactsAndPreviousServiceState(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "relay.service")
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
	platform := linuxPlatform{commands: runner}
	if err := platform.rollbackInstall(context.Background(), transaction, relayServiceRunning, true, true, true, true); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(artifact)
	if err != nil || string(content) != "before" {
		t.Fatalf("artifact was not restored: content=%q err=%v", content, err)
	}
	joined := strings.Join(runner.calls, "\n")
	for _, expected := range []string{" stop portless-relay.service", " daemon-reload", " enable portless-relay.service", " restart portless-relay.service", " restart systemd-resolved.service"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("rollback did not run %q:\n%s", expected, joined)
		}
	}
}

func TestLinuxRollbackDisablesNewUnitBeforeRemovingIt(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "relay.service")
	if err := os.WriteFile(artifact, []byte("new unit"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(artifact)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedCommandRunner{}
	platform := linuxPlatform{commands: runner}
	if err := platform.rollbackInstall(context.Background(), transaction, relayServiceStopped, false, false, true, true); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	disableIndex := strings.Index(joined, " disable "+systemdUnitName)
	reloadIndex := strings.Index(joined, " daemon-reload")
	if disableIndex < 0 || reloadIndex < 0 || disableIndex > reloadIndex {
		t.Fatalf("rollback did not disable the new unit before restoring artifacts:\n%s", joined)
	}
}

func TestLinuxServiceStateDistinguishesInactiveFromProbeFailure(t *testing.T) {
	inactive := &scriptedCommandRunner{responses: []commandResponse{{err: testCommandExitError(3)}}}
	state, err := (linuxPlatform{commands: inactive}).serviceState(context.Background())
	if err != nil || state != relayServiceStopped {
		t.Fatalf("inactive service state=%v err=%v", state, err)
	}
	failing := &scriptedCommandRunner{responses: []commandResponse{{output: "Failed to connect to bus", err: testCommandExitError(1)}}}
	state, err = (linuxPlatform{commands: failing}).serviceState(context.Background())
	if err == nil || state != relayServiceUnknown || !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Fatalf("failed probe state=%v err=%v", state, err)
	}
}

func TestLinuxUnitEnablementDistinguishesDisabledFromProbeFailure(t *testing.T) {
	disabled := &scriptedCommandRunner{responses: []commandResponse{{err: testCommandExitError(1)}}}
	enabled, err := (linuxPlatform{commands: disabled}).unitEnabled(context.Background())
	if err != nil || enabled {
		t.Fatalf("disabled service enabled=%v err=%v", enabled, err)
	}
	failing := &scriptedCommandRunner{responses: []commandResponse{{output: "Failed to connect to bus", err: testCommandExitError(2)}}}
	if _, err := (linuxPlatform{commands: failing}).unitEnabled(context.Background()); err == nil {
		t.Fatal("systemd enablement probe failure was treated as disabled")
	}
}

func TestResolvedConfigurationUsesDedicatedPortlessDNSPort(t *testing.T) {
	if actual := string(renderResolvedConfiguration()); actual != "[Resolve]\nDNS=127.77.0.1:1053\nDomains=~portless.test\n" {
		t.Fatalf("unexpected resolved configuration: %q", actual)
	}
}
