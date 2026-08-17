package administration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/portless-run/portless/portless-cli/command"
	"github.com/portless-run/portless/portless-cli/doctor"
	"github.com/portless-run/portless/portless-daemon/identity"
)

func TestConfigHelpShowsColorAndReset(t *testing.T) {
	application, output, _ := newTestCommands(t)
	root := application.configCommand()
	root.SetOut(output)
	root.SetUsageTemplate(application.UsageTemplate())
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"color", "reset", "Reset all CLI preferences"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("config help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestInvalidColorPreferenceIsAUsageFailure(t *testing.T) {
	application, _, _ := newTestCommands(t)
	root := application.configCommand()
	root.SetArgs([]string{"color", "neon"})
	err := root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "color must be auto, always, or never") {
		t.Fatalf("unexpected color preference error: %v", err)
	}
	if _, ok := err.(*command.UsageFailure); !ok {
		t.Fatalf("color preference error = %T, want *command.UsageFailure", err)
	}
}

func TestDaemonHelpAndStoppedStatus(t *testing.T) {
	application, output, _ := newTestCommands(t)
	root := application.daemonCommand()
	root.SetOut(output)
	root.SetUsageTemplate(application.UsageTemplate())
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"status", "stop", "restart", "Inspect, stop, or restart"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("daemon help does not contain %q:\n%s", expected, output.String())
		}
	}

	output.Reset()
	root = application.daemonCommand()
	root.SetArgs([]string{"status"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Portless daemon is stopped") {
		t.Fatalf("unexpected stopped status: %s", output.String())
	}

	output.Reset()
	root = application.daemonCommand()
	root.SetArgs([]string{"stop"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already stopped") {
		t.Fatalf("unexpected stopped result: %s", output.String())
	}
}

func TestResetCommandHelpExplainsConfirmationAndPreservedState(t *testing.T) {
	application, output, _ := newTestCommands(t)
	root := application.resetCommand()
	root.SetOut(output)
	root.SetUsageTemplate(application.UsageTemplate())
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"--yes", "--force", "active or unknown", "permanent deletion", "preserves CLI preferences", "localhost relay installation"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("reset help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestDaemonStatusExplainsOneTimeLegacyReplacement(t *testing.T) {
	application, _, _ := newTestCommands(t)
	record := identity.Record{PID: os.Getpid(), Port: 7331, APIVersion: "1.0.0", TokenPath: application.Paths.AuthToken}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(application.Paths.Control, content, 0o600); err != nil {
		t.Fatal(err)
	}
	root := application.daemonCommand()
	root.SetArgs([]string{"status"})
	err = root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "portless daemon restart --force") {
		t.Fatalf("legacy daemon remediation is missing: %v", err)
	}
}

func TestDoctorJSONReportsFailuresWithoutStartingDaemon(t *testing.T) {
	application, output, _ := newTestCommands(t)
	if err := os.Chmod(application.Paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	application.JSONOutput = true
	root := application.doctorCommand()
	root.SetArgs([]string{"daemon"})
	err := root.ExecuteContext(context.Background())
	if _, ok := err.(*command.ReportedError); !ok {
		t.Fatalf("doctor error = %v, want reported failure", err)
	}
	var report doctor.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("doctor did not emit valid JSON: %v\n%s", err, output.String())
	}
	if report.Scope != doctor.ScopeDaemon || report.Summary.Failed != 1 || report.Healthy {
		t.Fatalf("unexpected doctor report: %#v", report)
	}
	if _, err := os.Stat(application.Paths.Control); !os.IsNotExist(err) {
		t.Fatalf("doctor created a daemon control record: %v", err)
	}
}

func TestDoctorHelpDocumentsScopes(t *testing.T) {
	application, output, _ := newTestCommands(t)
	root := application.doctorCommand()
	root.SetOut(output)
	root.SetUsageTemplate(application.UsageTemplate())
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"doctor [daemon|relay|runtime]", "Diagnose the local Portless installation"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("doctor help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestDoctorRejectsUnknownScope(t *testing.T) {
	application, _, _ := newTestCommands(t)
	root := application.doctorCommand()
	root.SetArgs([]string{"database"})
	err := root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "doctor scope must be daemon, relay, or runtime") {
		t.Fatalf("unexpected doctor scope error: %v", err)
	}
}

func TestSetupAndRelayCommandSurfaces(t *testing.T) {
	application, output, _ := newTestCommands(t)
	setup := application.setupCommand()
	setup.SetOut(output)
	setup.SetUsageTemplate(application.UsageTemplate())
	if err := setup.Help(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Configure clean HTTP URLs and TCP endpoint DNS") {
		t.Fatalf("setup help does not describe first-run configuration:\n%s", output.String())
	}
	if len(setup.Commands()) != 0 {
		t.Fatalf("setup still exposes lifecycle subcommands: %#v", setup.Commands())
	}

	output.Reset()
	relay := application.relayCommand()
	relay.SetOut(output)
	relay.SetUsageTemplate(application.UsageTemplate())
	if err := relay.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"install", "status", "restart", "uninstall", "Manage clean local endpoint networking"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("relay help does not contain %q:\n%s", expected, output.String())
		}
	}
	uninstall, _, err := relay.Find([]string{"uninstall"})
	if err != nil {
		t.Fatal(err)
	}
	if uninstall.Flags().Lookup("force") == nil {
		t.Fatal("relay uninstall does not expose --force")
	}
	restart, _, err := relay.Find([]string{"restart"})
	if err != nil {
		t.Fatal(err)
	}
	if restart.Flags().Lookup("force") != nil {
		t.Fatal("relay restart must not expose a force option")
	}
}

func TestRuntimeAndUninstallCommandSurfaces(t *testing.T) {
	application, output, _ := newTestCommands(t)
	runtime := application.runtimeCommand()
	status, _, err := runtime.Find([]string{"status"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Short != "Show container runtime status" {
		t.Fatalf("unexpected runtime status description: %q", status.Short)
	}

	uninstall := application.uninstallCommand()
	uninstall.SetOut(output)
	uninstall.SetUsageTemplate(application.UsageTemplate())
	if err := uninstall.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"only previews", "daemon", "relay", "resolver", "CLI launcher", "--yes", "--force", "active or unknown"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("uninstall help does not contain %q:\n%s", expected, output.String())
		}
	}
}
