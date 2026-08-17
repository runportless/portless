package environment

import (
	"context"
	"strings"
	"testing"
)

func TestDownCommandExposesMachineWideFlag(t *testing.T) {
	application, output, _ := newTestCommands(t)
	command := application.downCommand()
	command.SetOut(output)
	command.SetUsageTemplate(application.UsageTemplate())
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Stop one or all environments", "--all", "stop every active environment"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("down help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestDownAllRejectsEnvironmentOverrideBeforeConnecting(t *testing.T) {
	application, _, _ := newTestCommands(t)
	application.EnvironmentOverride = "store/local"
	command := application.downCommand()
	command.SetArgs([]string{"--all"})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--all cannot be combined with --env") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

func TestDownAllVolumesRequiresConfirmationBeforeConnecting(t *testing.T) {
	application, _, _ := newTestCommands(t)
	command := application.downCommand()
	command.SetArgs([]string{"--all", "--volumes"})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeat with --yes") {
		t.Fatalf("unexpected confirmation error: %v", err)
	}
}
