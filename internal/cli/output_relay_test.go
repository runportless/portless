package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	relayinstall "github.com/portless-run/portless/internal/relay/install"
)

func TestCobraGlobalJSONFlagWorksBeforeAndAfterSubcommands(t *testing.T) {
	for _, args := range [][]string{{"--json", "daemon", "status"}, {"daemon", "status", "--json"}} {
		application, output, errorsOutput := newTestCLI(t)
		if code := application.Run(context.Background(), args); code != 0 {
			t.Fatalf("Run(%v) returned %d; stderr: %s", args, code, errorsOutput.String())
		}
		var result daemonStatusOutput
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("Run(%v) did not emit valid JSON: %v\n%s", args, err, output.String())
		}
		if result.State != "stopped" {
			t.Fatalf("Run(%v) state = %q, want stopped", args, result.State)
		}
	}
}

func TestJSONFlagDetectionStopsAtArgumentSeparator(t *testing.T) {
	if !jsonFlagRequested([]string{"version", "--json"}) {
		t.Fatal("--json was not detected")
	}
	if jsonFlagRequested([]string{"command", "--", "--json"}) {
		t.Fatal("positional --json after -- was treated as a flag")
	}
}

func TestCobraJSONUsageErrorsUseErrorEnvelope(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"runtime", "use", "containerd", "--json"}); code != 2 {
		t.Fatalf("Run returned %d, want 2; stdout: %s; stderr: %s", code, output.String(), errorsOutput.String())
	}
	if output.Len() != 0 {
		t.Fatalf("usage error wrote to stdout: %s", output.String())
	}
	var envelope errorOutput
	if err := json.Unmarshal(errorsOutput.Bytes(), &envelope); err != nil {
		t.Fatalf("stderr is not valid JSON: %v\n%s", err, errorsOutput.String())
	}
	if envelope.Error.Code != "USAGE_ERROR" || !strings.Contains(envelope.Error.Message, "runtime must be") {
		t.Fatalf("unexpected error envelope: %#v", envelope)
	}
}

func TestCobraVersionFlagSupportsHumanAndJSONOutput(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--version"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if output.String() != "portless "+Version+"\n" {
		t.Fatalf("unexpected human version output: %q", output.String())
	}

	application, output, errorsOutput = newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--version", "--json"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	var result map[string]string
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("Run did not emit valid JSON: %v\n%s", err, output.String())
	}
	if result["version"] != Version {
		t.Fatalf("Run version = %q, want %q", result["version"], Version)
	}
}

func TestWriteJSONLineEmitsOneCompactDocument(t *testing.T) {
	var output bytes.Buffer
	if err := writeJSONLine(&output, map[string]any{"event": "ready", "details": map[string]any{"count": 2}}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 || strings.Contains(strings.TrimSuffix(output.String(), "\n"), "\n") {
		t.Fatalf("JSON Line is not one line: %q", output.String())
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("JSON Line is invalid: %v", err)
	}
}

func TestRelayStatusJSONIncludesComputedState(t *testing.T) {
	var output bytes.Buffer
	if err := writeRelayStatusJSON(&output, relayinstall.InstallationStatus{Installed: true, Running: true, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["state"] != "ready" || result["healthy"] != true {
		t.Fatalf("unexpected relay status: %#v", result)
	}
}

func TestRelayRestartJSONIncludesActionAndStatus(t *testing.T) {
	var output bytes.Buffer
	status := relayinstall.InstallationStatus{Installed: true, Running: true, Healthy: true, Service: "dev.portless.relay"}
	if err := writeJSON(&output, relayActionOutput{
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

func TestCobraSetupIsOnlyTheFirstRunShortcut(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"setup", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Configure clean HTTP URLs and TCP endpoint DNS") {
		t.Fatalf("setup help does not describe first-run configuration:\n%s", output.String())
	}
	setup, _, err := application.rootCommand().Find([]string{"setup"})
	if err != nil {
		t.Fatal(err)
	}
	if len(setup.Commands()) != 0 {
		t.Fatalf("setup still exposes lifecycle subcommands: %#v", setup.Commands())
	}
}

func TestCobraRelayHelpShowsLifecycleCommands(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"relay", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"install", "status", "restart", "uninstall", "Manage clean local endpoint networking"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("relay help does not contain %q:\n%s", expected, output.String())
		}
	}

	application, output, errorsOutput = newTestCLI(t)
	if code := application.Run(context.Background(), []string{"relay", "uninstall", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "--force") {
		t.Fatalf("uninstall help does not document --force:\n%s", output.String())
	}

	application, output, errorsOutput = newTestCLI(t)
	if code := application.Run(context.Background(), []string{"relay", "restart", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "--force") {
		t.Fatalf("relay restart must not expose a force option:\n%s", output.String())
	}
}
