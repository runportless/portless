package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/diagnostics"
	"github.com/portless-run/portless/internal/model"
)

func TestCobraRootHelpShowsCommandTree(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"Available Commands:", "completion", "daemon", "doctor", "env", "project", "record", "runtime"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("help does not contain %q:\n%s", expected, output.String())
		}
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
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"not-a-command"}, want: "unknown command"},
		{name: "missing repeatable source", args: []string{"project", "create", "billing"}, want: `required flag(s) "source" not set`},
		{name: "missing provider", args: []string{"env", "bind", "checkout"}, want: "at least one of the flags"},
		{name: "exclusive provider", args: []string{"env", "bind", "checkout", "--local", "checkout", "--container"}, want: "none of the others can be"},
		{name: "invalid runtime", args: []string{"runtime", "use", "containerd"}, want: "runtime must be auto, docker, or podman"},
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
		})
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
	output := &bytes.Buffer{}
	errorsOutput := &bytes.Buffer{}
	application, err := New(output, errorsOutput, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return application, output, errorsOutput
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
