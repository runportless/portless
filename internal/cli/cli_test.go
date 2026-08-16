package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCobraRootHelpShowsCommandTree(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"Environment:", "Observe:", "Projects:", "Traffic:", "Administration:", "Help:", "completion", "config", "daemon", "doctor", "env", "project", "record", "relay", "reset", "runtime", "uninstall", "--env", "--json", "--no-color"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("help does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "Available Commands:") || strings.Contains(output.String(), "Additional Commands:") {
		t.Fatalf("root help contains an ungrouped command section:\n%s", output.String())
	}
	if strings.Contains(output.String(), "\n  use ") {
		t.Fatalf("root help still exposes the ambiguous top-level use command:\n%s", output.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("automatic color escaped into redirected help output:\n%q", output.String())
	}
}

func TestCobraRootCommandsAreGroupedByTask(t *testing.T) {
	application, _, _ := newTestCLI(t)
	root := application.rootCommand()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()

	expected := map[string]string{
		"up": rootGroupRun, "down": rootGroupRun, "status": rootGroupRun,
		"open": rootGroupRun, "url": rootGroupRun, "ui": rootGroupRun,
		"logs": rootGroupInspect, "traffic": rootGroupInspect, "timeline": rootGroupInspect,
		"service": rootGroupInspect, "connection": rootGroupInspect,
		"project": rootGroupConfigure, "env": rootGroupConfigure,
		"record": rootGroupTest, "fault": rootGroupTest,
		"runtime": rootGroupSystem, "setup": rootGroupSystem, "relay": rootGroupSystem, "daemon": rootGroupSystem,
		"doctor": rootGroupSystem, "config": rootGroupSystem, "reset": rootGroupSystem, "uninstall": rootGroupSystem,
		"completion": rootGroupOther, "help": rootGroupOther,
	}

	seen := make(map[string]bool, len(expected))
	for _, command := range root.Commands() {
		if !command.IsAvailableCommand() && command.Name() != "help" {
			continue
		}
		want, ok := expected[command.Name()]
		if !ok {
			t.Errorf("unexpected top-level command %q in group %q", command.Name(), command.GroupID)
			continue
		}
		seen[command.Name()] = true
		if command.GroupID != want {
			t.Errorf("command %q group = %q, want %q", command.Name(), command.GroupID, want)
		}
	}
	for command := range expected {
		if !seen[command] {
			t.Errorf("expected top-level command %q was not registered", command)
		}
	}
	if !root.AllChildCommandsHaveGroup() {
		t.Fatal("at least one available top-level command has no group")
	}
}

func TestConfigHelpShowsColorAndReset(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"config"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"color", "reset", "Reset all CLI preferences"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("config help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestColorPreferencePersistsAndNoColorOverridesIt(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := t.TempDir()
	application, output, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "color", "always"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("always preference did not color human output: %q", output.String())
	}

	content, err := os.ReadFile(filepath.Join(root, preferencesFile))
	if err != nil {
		t.Fatal(err)
	}
	var preferences cliPreferences
	if err := json.Unmarshal(content, &preferences); err != nil {
		t.Fatalf("preferences are not valid JSON: %v\n%s", err, content)
	}
	if preferences.Color != colorAlways {
		t.Fatalf("saved color = %q, want %q", preferences.Color, colorAlways)
	}
	assertFileMode(t, root, 0o700)
	assertFileMode(t, filepath.Join(root, preferencesFile), 0o600)

	application, output, errorsOutput = newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"--no-color", "config", "color"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("--no-color did not suppress saved color: %q", output.String())
	}
	for _, expected := range []string{"Preference:  always (saved)", "Effective:   disabled", "Reason:      --no-color"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("color status does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestColorAlwaysColorsHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := t.TempDir()
	application, _, _ := newTestCLIAt(t, root)
	if err := application.saveColorPreference(colorAlways); err != nil {
		t.Fatal(err)
	}

	application, output, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), ansiBoldCyan+"Usage:"+ansiReset) {
		t.Fatalf("saved always preference did not color help headings: %q", output.String())
	}
}

func TestNoColorEnvironmentSuppressesSavedColor(t *testing.T) {
	root := t.TempDir()
	application, _, _ := newTestCLIAt(t, root)
	if err := application.saveColorPreference(colorAlways); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NO_COLOR", "1")

	application, output, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("NO_COLOR did not suppress saved color: %q", output.String())
	}
}

func TestJSONAndCompletionNeverContainColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := t.TempDir()
	application, _, _ := newTestCLIAt(t, root)
	if err := application.saveColorPreference(colorAlways); err != nil {
		t.Fatal(err)
	}

	application, output, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "color", "--json"}); code != 0 {
		t.Fatalf("JSON Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("color escaped into JSON: %q", output.String())
	}
	var status colorConfigOutput
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatalf("color status is not valid JSON: %v\n%s", err, output.String())
	}
	if status.Preference != colorAlways || status.Effective || status.Reason != "JSON output" {
		t.Fatalf("unexpected JSON color status: %#v", status)
	}

	application, output, errorsOutput = newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"completion", "zsh"}); code != 0 {
		t.Fatalf("completion Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("color escaped into shell completion: %q", output.String())
	}
}

func TestColorPreferenceRepairsPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	application, _, _ := newTestCLIAt(t, root)
	if err := application.saveColorPreference(colorNever); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, preferencesFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	application, _, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "color"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	assertFileMode(t, root, 0o700)
	assertFileMode(t, path, 0o600)
}

func TestInvalidColorPreferenceIsAUsageError(t *testing.T) {
	application, _, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"config", "color", "neon"}); code != 2 {
		t.Fatalf("Run returned %d, want 2; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"color must be auto, always, or never", "Usage:\n  portless config color"} {
		if !strings.Contains(errorsOutput.String(), expected) {
			t.Errorf("stderr does not contain %q:\n%s", expected, errorsOutput.String())
		}
	}
}

func TestConfigResetRemovesSavedPreferencesAndIsIdempotent(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := t.TempDir()
	application, _, _ := newTestCLIAt(t, root)
	if err := application.saveColorPreference(colorAlways); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, preferencesFile)

	application, output, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "reset"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if output.String() != "CLI preferences reset to defaults.\n" {
		t.Fatalf("unexpected reset output: %q", output.String())
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preferences still exist after reset: %v", err)
	}
	if _, err := os.Stat(application.paths.Control); !os.IsNotExist(err) {
		t.Fatalf("config reset contacted or started the daemon: %v", err)
	}

	application, output, errorsOutput = newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "reset"}); code != 0 {
		t.Fatalf("second Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if output.String() != "CLI preferences are already at defaults.\n" {
		t.Fatalf("unexpected idempotent reset output: %q", output.String())
	}

	application, output, errorsOutput = newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "color"}); code != 0 {
		t.Fatalf("color Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Preference:  auto (default)") {
		t.Fatalf("reset did not restore built-in color default:\n%s", output.String())
	}
}

func TestConfigResetRecoversFromMalformedPreferencesAndSupportsJSON(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, preferencesFile)
	if err := os.WriteFile(path, []byte("not JSON\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	application, output, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"--json", "config", "reset"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("color escaped into reset JSON: %q", output.String())
	}
	var result actionOutput
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

func TestConfigResetUnlinksPreferenceSymlinkWithoutTouchingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.json")
	content := []byte("keep me")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, preferencesFile)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	application, _, errorsOutput := newTestCLIAt(t, root)
	if code := application.Run(context.Background(), []string{"config", "reset", "--no-color"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preferences symlink still exists after reset: %v", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, content) {
		t.Fatalf("reset modified symlink target: %q", actual)
	}
}
