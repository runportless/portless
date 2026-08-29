package administration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/api/contract"
	relayinstallation "github.com/runportless/portless/portless-relay/installation"
)

func TestRuntimeStatusUsesHumanReadableOutput(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.printRuntimeStatus(contract.RuntimeStatus{
		Preference: contract.RuntimeAuto,
		Selected:   contract.RuntimeDocker,
		State:      "ready",
		Version:    "29.4.0",
		Candidates: []contract.RuntimeProbe{
			{Name: contract.RuntimePodman, State: "missing", Reason: "Podman is not installed or is not on PATH"},
			{Name: contract.RuntimeDocker, State: "ready", Version: "29.4.0"},
		},
	})

	for _, expected := range []string{
		"Container runtime",
		"Status:     ready",
		"Selected:   docker 29.4.0",
		"Preference: auto",
		"RUNTIME    STATE",
		"docker     ready      29.4.0     selected",
		"podman     missing    —          Podman is not installed or is not on PATH",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("runtime status does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.HasPrefix(strings.TrimSpace(output.String()), "{") {
		t.Fatalf("runtime status unexpectedly emitted JSON:\n%s", output.String())
	}
}

func TestRelayStatusExplainsResidualUnverifiedEndpointPool(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.Local.InspectRelay = func(context.Context) (relayinstallation.InstallationStatus, error) {
		return relayinstallation.InstallationStatus{
			Platform: "launchd", EndpointPoolReady: true, EndpointPoolResidual: true,
			EndpointPoolDetail: "64/64 endpoint addresses configured on lo0", Problem: "ownership receipt is missing",
		}, nil
	}
	if err := application.relayStatus(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"not installed; residual endpoint pool", "present without a valid ownership receipt", "ifconfig lo0", "remove only aliases you can independently verify"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("residual endpoint status does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestRelayStatusExplainsHowToUpdateAnIncompatibleHelperAndRepairConfigurationDrift(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.Local.UserIDs = func() (int, int) { return 501, 20 }
	configurationError := "relay artifact /Library/LaunchDaemons/dev.portless.relay.plist content does not match the ownership receipt"
	application.Local.InspectRelay = func(context.Context) (relayinstallation.InstallationStatus, error) {
		return relayinstallation.InstallationStatus{
			Platform: "launchd", Service: "dev.portless.relay", Installed: true, Running: true,
			HelperPresent: true, HelperVerified: true, HelperBuildID: "aaaaaaaaaaaaaaaa", HelperVersion: "0.9.0", RequiredHelperVersion: "1.0.0",
			ConfigurationPresent: true, ConfigurationError: configurationError, ReceiptPresent: true,
			OwnerUID: 501, OwnerGID: 20, HelperPath: "/Library/PrivilegedHelperTools/dev.portless.relay",
			ConfigurationPath: "/Library/LaunchDaemons/dev.portless.relay.plist", ReceiptPath: "/var/db/portless/relay.json",
			Problem: configurationError,
		}, nil
	}
	if err := application.relayStatus(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Helper build: verified (aaaaaaaaaaaa)",
		"Helper version: update required (installed 0.9.0, required 1.0.0)",
		"Configuration: /Library/LaunchDaemons/dev.portless.relay.plist (drifted)",
		"Action required: The installed privileged helper version 0.9.0 does not match required version 1.0.0, and its system configuration has drifted.",
		"Run `portless relay install` to update the privileged helper and repair the system service and DNS configuration.",
		"may request administrator approval and does not stop running environments",
		"Details: " + configurationError,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("relay repair status does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "Problem: "+configurationError) {
		t.Fatalf("repairable configuration drift was rendered as an unexplained problem:\n%s", output.String())
	}
}

func TestRelayStatusExplainsReceiptBoundHelperIntegrityFailure(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.Local.UserIDs = func() (int, int) { return 501, 20 }
	helperError := "installed relay helper content does not match its ownership receipt"
	application.Local.InspectRelay = func(context.Context) (relayinstallation.InstallationStatus, error) {
		return relayinstallation.InstallationStatus{
			Platform: "launchd", Service: "dev.portless.relay", Installed: true, Running: true,
			HelperPresent: true, HelperCompatible: true, HelperBuildID: "aaaaaaaaaaaaaaaa", HelperVersion: "1.0.0", RequiredHelperVersion: "1.0.0", HelperError: helperError,
			ConfigurationPresent: true, ReceiptPresent: true, OwnerUID: 501, OwnerGID: 20,
			HelperPath: "/Library/PrivilegedHelperTools/dev.portless.relay", ConfigurationPath: "/Library/LaunchDaemons/dev.portless.relay.plist",
			ReceiptPath: "/var/db/portless/relay.json", Problem: helperError,
		}, nil
	}
	if err := application.relayStatus(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Helper build: not verified (aaaaaaaaaaaa)",
		"Helper version: compatible (1.0.0)",
		"Action required: The installed privileged helper must be reinstalled to establish receipt-bound integrity.",
		"Details: " + helperError,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("relay integrity status does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestRelayRepairGuidanceRequiresAVerifiedCurrentOwner(t *testing.T) {
	status := relayinstallation.InstallationStatus{
		ReceiptPresent: true, OwnerUID: 501, HelperPresent: true, ConfigurationError: "configuration drifted",
	}
	if guidance := relayRepairGuidance(status, 501); guidance == "" {
		t.Fatal("matching verified owner did not receive repair guidance")
	}
	for name, mutate := range map[string]func(*relayinstallation.InstallationStatus){
		"missing receipt": func(status *relayinstallation.InstallationStatus) { status.ReceiptPresent = false },
		"unknown owner":   func(status *relayinstallation.InstallationStatus) { status.OwnerUID = 0 },
		"different owner": func(status *relayinstallation.InstallationStatus) { status.OwnerUID = 502 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := status
			mutate(&candidate)
			if guidance := relayRepairGuidance(candidate, 501); guidance != "" {
				t.Fatalf("unsafe repair guidance = %q", guidance)
			}
		})
	}
}

func TestRelayRestartJSONIncludesActionAndStatus(t *testing.T) {
	var output bytes.Buffer
	status := relayinstallation.InstallationStatus{Installed: true, Running: true, Healthy: true, Service: "dev.portless.relay"}
	if err := command.WriteJSON(&output, relayActionOutput{
		Action:            "restart",
		relayStatusOutput: relayStatusOutput{State: status.State(), InstallationStatus: status},
	}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["action"] != "restart" || result["state"] != "ready" || result["service"] != "dev.portless.relay" {
		t.Fatalf("unexpected relay restart output: %#v", result)
	}
}
