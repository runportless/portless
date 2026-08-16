package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/daemon/instance"
	"github.com/portless-run/portless/internal/diagnostics"
	"github.com/portless-run/portless/internal/model"
)

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

func TestResetCommandHelpExplainsConfirmationAndPreservedState(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"reset", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"--yes", "--force", "active or unknown", "permanent deletion", "preserves CLI preferences", "localhost relay installation"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("reset help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestForcedResetPreviewExplainsRuntimeTerminationAndExactConfirmation(t *testing.T) {
	application, output, _ := newTestCLI(t)
	result := resetOutput{
		Action: "reset", Forced: true, Projects: 1, Environments: 1,
		ActiveEnvironments: []string{"store/local"},
		WillRemove:         append([]string(nil), resetRemovalCategories...),
		Preserved:          append([]string(nil), resetPreservedCategories...),
	}
	if err := application.printResetPreview(result); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Force reset will terminate verified Portless runtimes", "store/local", "portless reset --force --yes"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("forced reset preview does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestResetPreviewIsNonMutatingAndExplainsConfirmation(t *testing.T) {
	application, output, _ := newTestCLI(t)
	result := resetOutput{
		Action: "reset", Projects: 2, Environments: 3, ManagedVolumeEnvironments: 1,
		ActiveEnvironments: []string{"billing/local"},
		WillRemove:         append([]string(nil), resetRemovalCategories...),
		Preserved:          append([]string(nil), resetPreservedCategories...),
	}
	if err := application.printResetPreview(result); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Portless reset preview", "2 projects", "3 environments", "billing/local", "No changes were made", "portless reset --yes", "Preserved:"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("reset preview does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestResetActiveEnvironmentErrorGivesExplicitShutdownCommand(t *testing.T) {
	err := activeResetError([]string{"billing/local", "search/dev"})
	for _, expected := range []string{"billing/local", "search/dev", "portless down --all"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("active reset error does not contain %q: %v", expected, err)
		}
	}
}

func TestIncompatibleActiveResetRequiresForcedRecovery(t *testing.T) {
	err := incompatibleActiveResetError([]string{"store/local"})
	for _, expected := range []string{"store/local", "cannot be shut down individually", "portless reset --force --yes"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("incompatible reset error does not contain %q: %v", expected, err)
		}
	}
	application, output, _ := newTestCLI(t)
	if err := application.printResetPreview(resetOutput{
		Projects: 1, Environments: 1, ActiveEnvironments: []string{"store/local"},
		WillRemove: append([]string(nil), resetRemovalCategories...), Preserved: append([]string(nil), resetPreservedCategories...),
		TopologyIncompatible: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"format-independent runtime ownership records", "portless reset --force --yes"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("incompatible reset preview does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestCobraDaemonStatusExplainsOneTimeLegacyReplacement(t *testing.T) {
	application, _, errorsOutput := newTestCLI(t)
	record := instance.Record{PID: os.Getpid(), Port: 7331, APIVersion: "1.0.0", TokenPath: application.paths.AuthToken}
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

func TestPrintDaemonStatusUsesExplicitVersionLabels(t *testing.T) {
	application, output, _ := newTestCLI(t)
	application.printDaemonStatus(daemonStatusOutput{
		State:              "running",
		PID:                33083,
		InstanceID:         "f8ecffdf6d6f",
		BuildID:            "9f15670e7324",
		ProtocolVersion:    "2.0.0",
		APIVersion:         "3.0.0",
		RuntimeState:       "ready",
		HandoffReady:       true,
		ActiveEnvironments: []string{"store/local"},
	})

	for _, expected := range []string{"Protocol Version: 2.0.0\n", "API Version: 3.0.0\n"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("daemon status does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "Protocol: 2.0.0  API: 3.0.0") {
		t.Fatalf("daemon status still combines protocol and API versions:\n%s", output.String())
	}
}

func TestPrintStatusShowsHTTPAndPublishedContainerEndpoints(t *testing.T) {
	application, output, _ := newTestCLI(t)
	application.printStatus(model.Environment{
		Project: "billing", Name: "local", DashboardURL: "http://portless.localhost/environments/billing/local",
		Services: []model.Service{
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess}, Status: model.ServiceReady, Endpoints: []model.Endpoint{{Kind: model.EndpointPublic, Protocol: model.ProtocolHTTP, URL: "http://checkout.local.billing.localhost"}}, UpstreamPort: 49100},
			{ServiceDefinition: model.ServiceDefinition{Name: "postgres", Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432}, Status: model.ServiceReady, Endpoints: []model.Endpoint{{Kind: model.EndpointPublic, Protocol: model.ProtocolTCP, URL: "tcp://postgres.local.billing.portless.test:5432"}}, UpstreamPort: 49101},
		},
	})
	for _, expected := range []string{"http://checkout.local.billing.localhost", "tcp://postgres.local.billing.portless.test:5432", "http://portless.localhost/environments/billing/local"} {
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
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess}, Status: model.ServiceReady, Endpoints: []model.Endpoint{{Kind: model.EndpointPublic, Protocol: model.ProtocolHTTP, URL: "http://checkout.local.billing.localhost"}}},
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
