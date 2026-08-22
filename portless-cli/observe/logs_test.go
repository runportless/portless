package observe

import (
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestLogsCommandAcceptsAnOptionalService(t *testing.T) {
	application, output, errorsOutput := newTestCommands(t)
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
	command.SetUsageTemplate(application.UsageTemplate())
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

	application, output, _ := newTestCommands(t)
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
