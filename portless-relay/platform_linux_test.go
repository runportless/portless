//go:build linux

package relay

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
