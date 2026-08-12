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

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/diagnostics"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/spf13/cobra"
)

func TestCobraRootHelpShowsCommandTree(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"Available Commands:", "completion", "config", "daemon", "doctor", "env", "project", "record", "runtime", "--env", "--json", "--no-color"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("help does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "\n  use ") {
		t.Fatalf("root help still exposes the ambiguous top-level use command:\n%s", output.String())
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("automatic color escaped into redirected help output:\n%q", output.String())
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

func TestCobraDaemonHelpAndStoppedStatus(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"daemon", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"status", "stop", "restart", "Inspect, stop, or restart"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("daemon help does not contain %q:\n%s", expected, output.String())
		}
	}

	application, output, errorsOutput = newTestCLI(t)
	if code := application.Run(context.Background(), []string{"daemon", "status"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Portless daemon is stopped") {
		t.Fatalf("unexpected stopped status: %s", output.String())
	}

	application, output, errorsOutput = newTestCLI(t)
	if code := application.Run(context.Background(), []string{"daemon", "stop"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "already stopped") {
		t.Fatalf("unexpected stopped result: %s", output.String())
	}
}

func TestCobraDaemonStatusExplainsOneTimeLegacyReplacement(t *testing.T) {
	application, _, errorsOutput := newTestCLI(t)
	record := bootstrap.ControlRecord{PID: os.Getpid(), Port: 7331, APIVersion: "1", TokenPath: application.paths.Token}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(application.paths.Control, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if code := application.Run(context.Background(), []string{"daemon", "status"}); code != 1 {
		t.Fatalf("Run returned %d, want 1; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(errorsOutput.String(), "portless daemon restart --force") {
		t.Fatalf("legacy daemon remediation is missing: %s", errorsOutput.String())
	}
}

func TestPrintStatusShowsHTTPAndPublishedContainerEndpoints(t *testing.T) {
	application, output, _ := newTestCLI(t)
	application.printStatus(model.Environment{
		Project: "billing", Name: "local", DashboardURL: "http://portless.localhost/environments/billing/local",
		Services: []model.Service{
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess}, Status: model.ServiceReady, IngressURL: "http://checkout.local.billing.localhost", UpstreamPort: 49100},
			{ServiceDefinition: model.ServiceDefinition{Name: "postgres", Kind: model.ServiceContainer, Template: "postgres"}, Status: model.ServiceReady, UpstreamPort: 49101},
		},
	})
	for _, expected := range []string{"http://checkout.local.billing.localhost", "127.0.0.1:49101", "http://portless.localhost/environments/billing/local"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("status does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestStatusUsesRestrainedPaletteWhenColorIsEnabled(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	application, output, _ := newTestCLI(t)
	application.colorPreference = colorAlways
	application.printStatus(model.Environment{
		Project: "billing", Name: "local", Status: model.EnvironmentHealthy,
		DashboardURL: "http://portless.localhost/environments/billing/local",
		Services: []model.Service{
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess}, Status: model.ServiceReady, IngressURL: "http://checkout.local.billing.localhost"},
		},
	})
	for _, expected := range []string{
		ansiBoldCyan + "billing/local" + ansiReset,
		ansiGreen + string(model.EnvironmentHealthy) + ansiReset,
		ansiDim + "SERVICE",
		ansiCyan + "http://checkout.local.billing.localhost" + ansiReset,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("colored status does not contain %q:\n%q", expected, output.String())
		}
	}
}

func TestCobraDoctorJSONReportsFailuresWithoutStartingDaemon(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if err := os.Chmod(application.paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := application.Run(context.Background(), []string{"doctor", "daemon", "--json"}); code != 1 {
		t.Fatalf("Run returned %d, want 1; stdout: %s; stderr: %s", code, output.String(), errorsOutput.String())
	}
	if errorsOutput.Len() != 0 {
		t.Fatalf("doctor printed a duplicate command error: %s", errorsOutput.String())
	}
	var report diagnostics.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("doctor did not emit valid JSON: %v\n%s", err, output.String())
	}
	if report.Scope != diagnostics.ScopeDaemon || report.Summary.Failed != 1 || report.Healthy {
		t.Fatalf("unexpected doctor report: %#v", report)
	}
	if _, err := os.Stat(application.paths.Control); !os.IsNotExist(err) {
		t.Fatalf("doctor created a daemon control record: %v", err)
	}
}

func TestCobraDoctorHelpDocumentsScopesAndJSON(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"doctor", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"portless doctor [daemon|relay|runtime]", "--json", "Diagnose the local Portless installation"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("doctor help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestCobraDoctorRejectsUnknownScope(t *testing.T) {
	application, _, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"doctor", "database"}); code != 2 {
		t.Fatalf("Run returned %d, want 2; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(errorsOutput.String(), "doctor scope must be daemon, relay, or runtime") {
		t.Fatalf("unexpected doctor scope error: %s", errorsOutput.String())
	}
}

func TestCobraNestedHelpShowsProviderFlags(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"env", "bind", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"portless env bind <service>", "--local", "--container", "--remote", "--classification", "--write-policy"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("bind help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestEnvironmentHelpMakesCheckoutSelectionExplicit(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"env"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{
		"select", "Select an environment for the current checkout",
		"current", "Show the effective environment and how it was resolved",
		"clear", "Clear the saved environment selection for the current checkout",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("environment help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestEnvironmentOverrideIsGlobalAndDoesNotContactDaemonForHelp(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--env", "billing/qa", "status", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if application.environmentOverride != "billing/qa" {
		t.Fatalf("environment override = %q, want billing/qa", application.environmentOverride)
	}
	if !strings.Contains(output.String(), "Show environment status") {
		t.Fatalf("status help was not rendered:\n%s", output.String())
	}
	if _, err := os.Stat(application.paths.Control); !os.IsNotExist(err) {
		t.Fatalf("help with --env contacted or started the daemon: %v", err)
	}
}

func TestEffectiveEnvironmentSelectorPrefersOneInvocationOverride(t *testing.T) {
	application, _, _ := newTestCLI(t)
	if actual, err := application.effectiveEnvironmentSelector(""); err != nil || actual != "" {
		t.Fatalf("empty selector = %q, %v", actual, err)
	}
	if actual, err := application.effectiveEnvironmentSelector("billing/local"); err != nil || actual != "billing/local" {
		t.Fatalf("explicit selector = %q, %v", actual, err)
	}

	application.environmentOverride = "billing/qa"
	if actual, err := application.effectiveEnvironmentSelector(""); err != nil || actual != "billing/qa" {
		t.Fatalf("override selector = %q, %v", actual, err)
	}
	if _, err := application.effectiveEnvironmentSelector("billing/local"); err == nil || !strings.Contains(err.Error(), "provided twice") {
		t.Fatalf("duplicate selector error = %v", err)
	}
	for resolution, expected := range map[string]string{
		"flag":     "--env override for this invocation",
		"selected": "saved selection for this checkout",
		"inferred": "only environment using this checkout",
	} {
		if actual := environmentResolutionDescription(resolution); actual != expected {
			t.Errorf("description for %q = %q, want %q", resolution, actual, expected)
		}
	}
}

func TestLogsCommandAcceptsAnOptionalService(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	command := application.logsCommand()
	if err := command.Args(command, nil); err != nil {
		t.Fatalf("logs rejected an omitted service: %v", err)
	}
	if err := command.Args(command, []string{"checkout"}); err != nil {
		t.Fatalf("logs rejected one service: %v", err)
	}
	if err := command.Args(command, []string{"checkout", "orders"}); err == nil {
		t.Fatal("logs accepted more than one service")
	}
	command.SetOut(output)
	command.SetErr(errorsOutput)
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"logs [service]", "every service", "--tail"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("logs help does not contain %q:\n%s", expected, output.String())
		}
	}
	if command.Flags().Lookup("follow") != nil {
		t.Fatal("logs still exposes --follow")
	}
	if tail := command.Flags().Lookup("tail"); tail == nil || tail.Shorthand != "t" {
		t.Fatalf("logs --tail flag = %#v, want shorthand -t", tail)
	}
}

func TestTrafficUsesTailFlag(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	command := application.trafficCommand()
	list, _, err := command.Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Flags().Lookup("follow") != nil {
		t.Fatal("traffic still exposes --follow")
	}
	if tail := list.Flags().Lookup("tail"); tail == nil || tail.Shorthand != "t" {
		t.Fatalf("traffic --tail flag = %#v, want shorthand -t", tail)
	}

	if code := application.Run(context.Background(), []string{"traffic"}); code != 0 {
		t.Fatalf("bare traffic returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"Available Commands:", "list", "List captured HTTP traffic"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("traffic help does not contain %q:\n%s", expected, output.String())
		}
	}
	if _, err := os.Stat(application.paths.Control); !os.IsNotExist(err) {
		t.Fatalf("bare traffic contacted or started the daemon: %v", err)
	}
}

func TestLogServiceSelectionAndCombinedFormatting(t *testing.T) {
	environment := model.Environment{Project: "billing", Name: "local", Services: []model.Service{
		{ServiceDefinition: model.ServiceDefinition{Name: "checkout"}},
		{ServiceDefinition: model.ServiceDefinition{Name: "orders"}},
	}}
	services, err := logServiceNames(environment, "")
	if err != nil || strings.Join(services, ",") != "checkout,orders" {
		t.Fatalf("all services = %v, %v", services, err)
	}
	services, err = logServiceNames(environment, "CHECKOUT")
	if err != nil || len(services) != 1 || services[0] != "checkout" {
		t.Fatalf("selected service = %v, %v", services, err)
	}
	if _, err := logServiceNames(environment, "missing"); err == nil {
		t.Fatal("missing service was accepted")
	}

	application, output, _ := newTestCLI(t)
	application.printLogs(environment, []serviceLogs{
		{Service: "checkout", Lines: []string{"listening on 3000"}},
		{Service: "orders", Lines: []string{"connected to postgres"}},
	}, false)
	for _, expected := range []string{"[checkout] listening on 3000", "[orders] connected to postgres"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("combined logs do not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestRuntimeStatusUsesHumanReadableOutput(t *testing.T) {
	application, output, _ := newTestCLI(t)
	application.printRuntimeStatus(container.Status{
		Preference: container.RuntimeAuto,
		Selected:   container.RuntimeDocker,
		State:      "ready",
		Version:    "29.4.0",
		Candidates: []container.ProbeResult{
			{Name: container.RuntimePodman, State: "missing", Reason: "Podman is not installed or is not on PATH"},
			{Name: container.RuntimeDocker, State: "ready", Version: "29.4.0"},
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

func TestCobraRuntimeHelpDocumentsJSONOutput(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"runtime", "status", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "--json") {
		t.Fatalf("runtime status help does not document JSON output:\n%s", output.String())
	}
}

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

func TestCobraVersionSupportsHumanAndJSONOutput(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"version"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if output.String() != "portless "+Version+"\n" {
		t.Fatalf("unexpected human version output: %q", output.String())
	}

	for _, args := range [][]string{{"version", "--json"}, {"--version", "--json"}} {
		application, output, errorsOutput = newTestCLI(t)
		if code := application.Run(context.Background(), args); code != 0 {
			t.Fatalf("Run(%v) returned %d; stderr: %s", args, code, errorsOutput.String())
		}
		var result map[string]string
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatalf("Run(%v) did not emit valid JSON: %v\n%s", args, err, output.String())
		}
		if result["version"] != Version {
			t.Fatalf("Run(%v) version = %q, want %q", args, result["version"], Version)
		}
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

func TestSetupStatusJSONIncludesComputedState(t *testing.T) {
	var output bytes.Buffer
	if err := writeSetupStatusJSON(&output, ingress.InstallationStatus{Installed: true, Running: true, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["state"] != "ready" || result["healthy"] != true {
		t.Fatalf("unexpected setup status: %#v", result)
	}
}

func TestCobraSetupHelpShowsStatusAndUninstall(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"setup", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"status", "uninstall", "Install, inspect, or remove"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("setup help does not contain %q:\n%s", expected, output.String())
		}

	}
	application, output, errorsOutput = newTestCLI(t)
	if code := application.Run(context.Background(), []string{"setup", "uninstall", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "--force") {
		t.Fatalf("uninstall help does not document --force:\n%s", output.String())
	}
}

func TestCobraUsageErrorsReturnExitCodeTwo(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  string
		usage string
	}{
		{name: "unknown command", args: []string{"not-a-command"}, want: "unknown command", usage: "portless [flags]"},
		{name: "missing repeatable source", args: []string{"project", "create", "billing"}, want: `required flag(s) "source" not set`, usage: "portless project create <name>"},
		{name: "missing provider", args: []string{"env", "bind", "checkout"}, want: "at least one of the flags", usage: "portless env bind <service>"},
		{name: "exclusive provider", args: []string{"env", "bind", "checkout", "--local", "checkout", "--container"}, want: "none of the others can be", usage: "portless env bind <service>"},
		{name: "invalid runtime", args: []string{"runtime", "use", "containerd"}, want: "runtime must be auto, docker, or podman", usage: "portless runtime use <auto|docker|podman>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, _, errorsOutput := newTestCLI(t)
			if code := application.Run(context.Background(), test.args); code != 2 {
				t.Fatalf("Run returned %d, want 2; stderr: %s", code, errorsOutput.String())
			}
			if !strings.Contains(errorsOutput.String(), test.want) {
				t.Fatalf("stderr does not contain %q:\n%s", test.want, errorsOutput.String())
			}
			if !strings.Contains(errorsOutput.String(), "Usage:\n  "+test.usage) {
				t.Fatalf("stderr does not contain command usage %q:\n%s", test.usage, errorsOutput.String())
			}
		})
	}
}

func TestEveryPublicCommandHasAuditedBareBehavior(t *testing.T) {
	const (
		showHelp  = "help"
		runAction = "run"
	)
	expected := map[string]string{
		"portless":                 showHelp,
		"portless config":          showHelp,
		"portless config color":    runAction,
		"portless config reset":    runAction,
		"portless setup":           runAction,
		"portless setup status":    runAction,
		"portless setup uninstall": runAction,
		"portless daemon":          showHelp,
		"portless daemon status":   runAction,
		"portless daemon stop":     runAction,
		"portless daemon restart":  runAction,
		"portless doctor":          runAction,
		"portless up":              runAction,
		"portless down":            runAction,
		"portless status":          runAction,
		"portless open":            runAction,
		"portless ui":              runAction,
		"portless logs":            runAction,
		"portless traffic":         showHelp,
		"portless traffic list":    runAction,
		"portless record":          showHelp,
		"portless record list":     runAction,
		"portless record start":    showHelp,
		"portless record stop":     runAction,
		"portless record export":   showHelp,
		"portless record delete":   showHelp,
		"portless fault":           showHelp,
		"portless fault list":      runAction,
		"portless fault add":       showHelp,
		"portless fault disable":   showHelp,
		"portless fault clear":     runAction,
		"portless project":         showHelp,
		"portless project create":  showHelp,
		"portless project export":  runAction,
		"portless project rename":  showHelp,
		"portless project forget":  runAction,
		"portless env":             showHelp,
		"portless env select":      showHelp,
		"portless env current":     runAction,
		"portless env clear":       runAction,
		"portless env list":        runAction,
		"portless env clone":       showHelp,
		"portless env bind":        showHelp,
		"portless env source":      showHelp,
		"portless env rescan":      runAction,
		"portless env forget":      runAction,
		"portless runtime":         showHelp,
		"portless runtime status":  runAction,
		"portless runtime start":   runAction,
		"portless runtime use":     showHelp,
		"portless version":         runAction,
	}

	application, _, _ := newTestCLI(t)
	actual := map[string]*cobra.Command{}
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		actual[command.CommandPath()] = command
		for _, child := range command.Commands() {
			if child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			visit(child)
		}
	}
	visit(application.rootCommand())

	for path := range actual {
		if _, ok := expected[path]; !ok {
			t.Errorf("public command %q has no audited bare behavior", path)
		}
	}
	for path := range expected {
		if _, ok := actual[path]; !ok {
			t.Errorf("audited command %q no longer exists", path)
		}
	}

	for path, behavior := range expected {
		if behavior != showHelp {
			continue
		}
		t.Run(path, func(t *testing.T) {
			application, output, errorsOutput := newTestCLI(t)
			args := strings.Fields(strings.TrimSpace(strings.TrimPrefix(path, "portless")))
			if code := application.Run(context.Background(), args); code != 0 {
				t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
			}
			if errorsOutput.Len() != 0 {
				t.Fatalf("bare command printed an error: %s", errorsOutput.String())
			}
			if !strings.Contains(output.String(), "Usage:") {
				t.Fatalf("bare command did not print help:\n%s", output.String())
			}
			if _, err := os.Stat(application.paths.Control); !os.IsNotExist(err) {
				t.Fatalf("bare help contacted or started the daemon: %v", err)
			}
		})
	}

	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"completion"}); code != 0 {
		t.Fatalf("bare completion returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "Available Commands:") || !strings.Contains(output.String(), "zsh") {
		t.Fatalf("bare completion did not print help:\n%s", output.String())
	}
}

func TestBareLeafCommandsWithRequiredArgumentsShowHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "env select", args: []string{"env", "select"}, want: "portless env select <project/environment>"},
		{name: "record start", args: []string{"record", "start"}, want: "portless record start <name>"},
		{name: "partial fault add", args: []string{"fault", "add", "slow"}, want: "portless fault add <name> <source:target>"},
		{name: "runtime use", args: []string{"runtime", "use"}, want: "portless runtime use <auto|docker|podman>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application, output, errorsOutput := newTestCLI(t)
			if code := application.Run(context.Background(), test.args); code != 0 {
				t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
			}
			if errorsOutput.Len() != 0 {
				t.Fatalf("bare command printed an error: %s", errorsOutput.String())
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("help does not contain %q:\n%s", test.want, output.String())
			}
			if _, err := os.Stat(application.paths.Control); !os.IsNotExist(err) {
				t.Fatalf("bare command contacted or started the daemon: %v", err)
			}
		})
	}
}

func TestRequiredArgumentCountUsesCommandSyntax(t *testing.T) {
	for use, expected := range map[string]int{
		"env select <project/environment>": 1,
		"fault add <name> <source:target>": 2,
		"logs [service]":                   0,
		"doctor [daemon|relay|runtime]":    0,
		"runtime use <auto|docker|podman>": 1,
	} {
		if actual := requiredArgumentCount(use); actual != expected {
			t.Errorf("requiredArgumentCount(%q) = %d, want %d", use, actual, expected)
		}
	}
}

func TestCobraGeneratesShellCompletion(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"completion", "zsh"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "#compdef portless") || !strings.Contains(output.String(), "_portless") {
		t.Fatalf("unexpected completion output:\n%s", output.String())
	}
}

func newTestCLI(t *testing.T) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	return newTestCLIAt(t, t.TempDir())
}

func newTestCLIAt(t *testing.T, root string) (*CLI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	errorsOutput := &bytes.Buffer{}
	application, err := New(output, errorsOutput, root)
	if err != nil {
		t.Fatal(err)
	}
	return application, output, errorsOutput
}

func assertFileMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("%s permissions = %04o, want %04o", path, actual, expected)
	}
}

func TestAbsoluteSourcePathUsesCLIWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	actual, err := absoluteSourcePath("checkout")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("absoluteSourcePath = %q, want %q", actual, expected)
	}
}
