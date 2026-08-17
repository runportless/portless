package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestColorPreferencePersistsAndNoColorOverridesIt(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	root := t.TempDir()
	context, _, _ := newTestContext(t, root)
	if err := context.SaveColorPreference(ColorAlways); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(filepath.Join(root, preferencesFile))
	if err != nil {
		t.Fatal(err)
	}
	var preferences cliPreferences
	if err := json.Unmarshal(content, &preferences); err != nil {
		t.Fatalf("preferences are not valid JSON: %v\n%s", err, content)
	}
	if preferences.Color != ColorAlways {
		t.Fatalf("saved color = %q, want %q", preferences.Color, ColorAlways)
	}
	assertFileMode(t, root, 0o700)
	assertFileMode(t, filepath.Join(root, preferencesFile), 0o600)

	context, output, _ := newTestContext(t, root)
	if err := context.LoadPreferences(); err != nil {
		t.Fatal(err)
	}
	context.NoColor = true
	if err := context.PrintColorConfig(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Preference:  always (saved)", "Effective:   disabled", "Reason:      --no-color"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("color status does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestColorAlwaysColorsHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	context, _, _ := newTestContext(t, t.TempDir())
	context.ColorPreference = ColorAlways
	root := &cobra.Command{Use: "portless", Short: "test command", Run: func(*cobra.Command, []string) {}}
	root.SetUsageTemplate(context.UsageTemplate())
	usage := root.UsageString()
	if !strings.Contains(usage, ansiBoldCyan+"Usage:"+ansiReset) {
		t.Fatalf("always preference did not color help headings: %q", usage)
	}
}

func TestNoColorEnvironmentSuppressesSavedColor(t *testing.T) {
	context, output, _ := newTestContext(t, t.TempDir())
	context.ColorPreference = ColorAlways
	t.Setenv("NO_COLOR", "1")
	if actual := context.Heading(output, "Usage:"); strings.Contains(actual, "\x1b[") {
		t.Fatalf("NO_COLOR did not suppress saved color: %q", actual)
	}
}

func TestJSONAndCompletionNeverContainColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	context, output, _ := newTestContext(t, t.TempDir())
	context.ColorPreference = ColorAlways

	context.JSONOutput = true
	if actual := context.Heading(output, "JSON"); strings.Contains(actual, "\x1b[") {
		t.Fatalf("color escaped into JSON output: %q", actual)
	}
	if enabled, reason := context.ColorDecision(output); enabled || reason != "JSON output" {
		t.Fatalf("JSON color decision = %t, %q", enabled, reason)
	}

	context.JSONOutput = false
	context.CompletionOutput = true
	if actual := context.Heading(output, "completion"); strings.Contains(actual, "\x1b[") {
		t.Fatalf("color escaped into shell completion: %q", actual)
	}
	if enabled, reason := context.ColorDecision(output); enabled || reason != "shell completion" {
		t.Fatalf("completion color decision = %t, %q", enabled, reason)
	}
}

func TestColorPreferenceRepairsPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	context, _, _ := newTestContext(t, root)
	if err := context.SaveColorPreference(ColorNever); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, preferencesFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	context, _, _ = newTestContext(t, root)
	if err := context.LoadPreferences(); err != nil {
		t.Fatal(err)
	}
	assertFileMode(t, root, 0o700)
	assertFileMode(t, path, 0o600)
}

func TestInvalidColorPreferenceIsAUsageError(t *testing.T) {
	if _, err := ParseColorPreference("neon"); err == nil || !strings.Contains(err.Error(), "color must be auto, always, or never") {
		t.Fatalf("unexpected color preference error: %v", err)
	}
}

func TestResetPreferencesRemovesSavedValueAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	context, output, _ := newTestContext(t, root)
	if err := context.SaveColorPreference(ColorAlways); err != nil {
		t.Fatal(err)
	}
	path := context.PreferencesPath()
	if err := context.ResetPreferences(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "CLI preferences reset to defaults.\n" {
		t.Fatalf("unexpected reset output: %q", output.String())
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preferences still exist after reset: %v", err)
	}

	context, output, _ = newTestContext(t, root)
	if err := context.ResetPreferences(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "CLI preferences are already at defaults.\n" {
		t.Fatalf("unexpected idempotent reset output: %q", output.String())
	}
}

func TestResetPreferencesSupportsMalformedInputAndJSON(t *testing.T) {
	root := t.TempDir()
	context, output, _ := newTestContext(t, root)
	path := context.PreferencesPath()
	if err := os.WriteFile(path, []byte("not JSON\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	context.JSONOutput = true
	if err := context.ResetPreferences(); err != nil {
		t.Fatal(err)
	}
	var result ActionOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("reset did not emit valid JSON: %v\n%s", err, output.String())
	}
	if result.Action != "reset" || result.Status != "reset" || result.Path != path {
		t.Fatalf("unexpected reset result: %#v", result)
	}
}

func TestResetPreferencesUnlinksSymlinkWithoutTouchingTarget(t *testing.T) {
	root := t.TempDir()
	context, _, _ := newTestContext(t, root)
	target := filepath.Join(t.TempDir(), "target.json")
	content := []byte("keep me")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
	path := context.PreferencesPath()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := context.ResetPreferences(); err != nil {
		t.Fatal(err)
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

func TestBoolFlagDetectionStopsAtArgumentSeparator(t *testing.T) {
	if !BoolFlagRequested([]string{"version", "--json"}, "json") {
		t.Fatal("--json was not detected")
	}
	if BoolFlagRequested([]string{"command", "--", "--json"}, "json") {
		t.Fatal("positional --json after -- was treated as a flag")
	}
}
