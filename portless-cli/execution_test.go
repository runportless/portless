package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCobraGlobalJSONFlagWorksBeforeAndAfterSubcommands(t *testing.T) {
	for _, args := range [][]string{{"--json", "daemon", "status"}, {"daemon", "status", "--json"}} {
		application, output, errorsOutput := newTestCLI(t)
		if code := application.Run(context.Background(), args); code != 0 {
			t.Fatalf("Run(%v) returned %d; stderr: %s", args, code, errorsOutput.String())
		}
		var result struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("Run(%v) did not emit valid JSON: %v\n%s", args, err, output.String())
		}
		if result.State != "stopped" {
			t.Fatalf("Run(%v) state = %q, want stopped", args, result.State)
		}
	}
}

func TestCobraJSONUsageErrorsUseErrorEnvelope(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"runtime", "use", "containerd", "--json"}); code != 2 {
		t.Fatalf("Run returned %d, want 2; stdout: %s; stderr: %s", code, output.String(), errorsOutput.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
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

func TestGlobalFlagsAreInheritedWithoutContactingTheDaemonForHelp(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--env", "billing/qa", "status", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if application.context.EnvironmentOverride != "billing/qa" {
		t.Fatalf("environment override = %q, want billing/qa", application.context.EnvironmentOverride)
	}
	for _, expected := range []string{"Show environment status", "--env", "--json", "--no-color"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("nested help does not contain inherited value %q:\n%s", expected, output.String())
		}
	}
	if _, err := os.Stat(application.context.Paths.Control); !os.IsNotExist(err) {
		t.Fatalf("help with --env contacted or started the daemon: %v", err)
	}
}

func TestConfigResetRecoversFromMalformedPreferences(t *testing.T) {
	root := t.TempDir()
	application, output, errorsOutput := newTestCLIAt(t, root)
	path := application.context.PreferencesPath()
	if err := os.WriteFile(path, []byte("not JSON\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := application.Run(context.Background(), []string{"--json", "config", "reset"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	var result struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("reset did not emit valid JSON: %v\n%s", err, output.String())
	}
	if result.Action != "reset" || result.Status != "reset" || result.Path != path {
		t.Fatalf("unexpected reset result: %#v", result)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed preferences still exist after reset: %v", err)
	}
}
