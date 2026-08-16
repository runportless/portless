package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/model"
)

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
	for _, expected := range []string{"Available Commands:", "list", "List captured application traffic"} {
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
	application.printLogs(environment, []model.LogEntry{
		{Service: "checkout", Message: "listening on 3000"},
		{Service: "orders", Message: "connected to postgres"},
	}, false, false)
	for _, expected := range []string{"[checkout] listening on 3000", "[orders] connected to postgres"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("combined logs do not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestTCPApplicationTrafficUsesProtocolSpecificHumanOutput(t *testing.T) {
	application, output, _ := newTestCLI(t)
	application.printTrafficList(model.Environment{Project: "billing", Name: "local"}, "tcp", []model.TrafficEvent{{
		Sequence: 9, Protocol: model.ProtocolTCP, Source: "checkout", Target: "postgres", DurationMS: 4,
	}})
	for _, expected := range []string{"TCP traffic", "PROTOCOL", "TCP", "checkout:postgres", "ok"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("TCP traffic output does not contain %q:\n%s", expected, output.String())
		}
	}
	if strings.Contains(output.String(), "METHOD") || strings.Contains(output.String(), "CODE") {
		t.Fatalf("TCP traffic used HTTP columns:\n%s", output.String())
	}
}

func TestRuntimeStatusUsesHumanReadableOutput(t *testing.T) {
	application, output, _ := newTestCLI(t)
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

func TestCobraRuntimeHelpDocumentsJSONOutput(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"runtime", "status", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "--json") {
		t.Fatalf("runtime status help does not document JSON output:\n%s", output.String())
	}
}

func TestFaultDurationIsOptIn(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"fault", "add", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	if !strings.Contains(output.String(), "--duration") || !strings.Contains(output.String(), "automatically disable") {
		t.Fatalf("fault help does not explain optional automatic disable:\n%s", output.String())
	}
	if strings.Contains(output.String(), "default 10m") {
		t.Fatalf("fault duration still defaults to ten minutes:\n%s", output.String())
	}

	command, _, err := application.rootCommand().Find([]string{"fault", "add"})
	if err != nil {
		t.Fatal(err)
	}
	durationFlag := command.Flags().Lookup("duration")
	if durationFlag == nil || durationFlag.DefValue != "0s" {
		t.Fatalf("duration default = %v, want 0s", durationFlag)
	}
}
