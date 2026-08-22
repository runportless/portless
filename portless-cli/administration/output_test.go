package administration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-relay"
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

func TestRelayRestartJSONIncludesActionAndStatus(t *testing.T) {
	var output bytes.Buffer
	status := relay.InstallationStatus{Installed: true, Running: true, Healthy: true, Service: "dev.portless.relay"}
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
