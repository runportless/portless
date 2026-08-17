package administration

import (
	"strings"
	"testing"
)

func TestForcedResetPreviewExplainsRuntimeTerminationAndExactConfirmation(t *testing.T) {
	application, output, _ := newTestCommands(t)
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
	application, output, _ := newTestCommands(t)
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
	application, output, _ := newTestCommands(t)
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

func TestPrintDaemonStatusUsesExplicitVersionLabels(t *testing.T) {
	application, output, _ := newTestCommands(t)
	application.printDaemonStatus(daemonStatusOutput{
		State: "running", PID: 33083, InstanceID: "f8ecffdf6d6f", BuildID: "9f15670e7324",
		ProtocolVersion: "2.0.0", APIVersion: "3.0.0", RuntimeState: "ready", HandoffReady: true,
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
