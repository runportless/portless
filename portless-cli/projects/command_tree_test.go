package projects

import (
	"strings"
	"testing"
)

func TestEnvironmentBindHelpShowsProviderFlags(t *testing.T) {
	application, output, _ := newTestCommands(t)
	root := application.environmentCommand()
	root.SetOut(output)
	root.SetUsageTemplate(application.UsageTemplate())
	bind, _, err := root.Find([]string{"bind"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bind.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"env bind <service>", "--local", "--container", "--remote", "--classification", "--write-policy"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("bind help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestEnvironmentHelpMakesCheckoutSelectionExplicit(t *testing.T) {
	application, output, _ := newTestCommands(t)
	command := application.environmentCommand()
	command.SetOut(output)
	command.SetUsageTemplate(application.UsageTemplate())
	if err := command.Help(); err != nil {
		t.Fatal(err)
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

func TestProjectSourceDeleteRequiresExplicitConfirmation(t *testing.T) {
	application, output, errorsOutput := newTestCommands(t)
	root := application.projectCommand()
	root.SetOut(output)
	root.SetErr(errorsOutput)
	root.SetArgs([]string{"source", "delete", "inventory"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "repeat with --yes") {
		t.Fatalf("delete error = %v, want explicit confirmation", err)
	}
}

func TestEnvironmentCheckoutRemoveRequiresExplicitConfirmation(t *testing.T) {
	application, output, errorsOutput := newTestCommands(t)
	root := application.environmentCommand()
	root.SetOut(output)
	root.SetErr(errorsOutput)
	root.SetArgs([]string{"checkout", "remove", "inventory"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "repeat with --yes") {
		t.Fatalf("remove error = %v, want explicit confirmation", err)
	}
}
