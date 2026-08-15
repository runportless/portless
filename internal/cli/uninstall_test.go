package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/ingress"
	"github.com/portless-run/portless/internal/runtime/container"
)

func TestUninstallCommandHelpDocumentsPreviewConfirmationAndForce(t *testing.T) {
	application, output, errorsOutput := newTestCLI(t)
	if code := application.Run(context.Background(), []string{"uninstall", "--help"}); code != 0 {
		t.Fatalf("Run returned %d; stderr: %s", code, errorsOutput.String())
	}
	for _, expected := range []string{"only previews", "daemon", "relay", "resolver", "CLI launcher", "--yes", "--force", "active or unknown"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("uninstall help does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestUninstallPreviewExplainsExactScopeAndConfirmation(t *testing.T) {
	application, output, _ := newTestCLI(t)
	result := uninstallOutput{
		Action: "uninstall", Forced: true, Projects: 1, Environments: 2, ManagedVolumeEnvironments: 1,
		Daemon:     uninstallDaemonOutput{State: "running", PID: 123, InventoryAvailable: true, ActiveEnvironments: []string{"store/local"}},
		Relay:      uninstallRelayOutput{Installed: true, State: "ready", Service: "dev.portless.ingress", ResolverPath: "/etc/resolver/portless.test", EndpointPoolReady: true, AdministratorPrompt: true},
		Data:       uninstallDataOutput{Path: "/Users/test/.portless", Present: true},
		Launcher:   launcherPlan{Path: "/Users/test/bin/portless", Target: "/src/portless/bin/portless", Kind: "symlink", Action: "remove"},
		WillRemove: append([]string(nil), uninstallRemovalCategories...),
	}
	if err := application.printUninstallPreview(result); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"Portless uninstall preview", "Daemon: running (PID 123)", "1 project, 2 environments", "dev.portless.ingress",
		"/etc/resolver/portless.test", "Administrator approval", "remove /Users/test/bin/portless", "preserve /src/portless/bin/portless",
		"store/local", "No changes were made", "portless uninstall --force --yes",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("uninstall preview does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestUninstallPreviewSupportsJSON(t *testing.T) {
	application, output, _ := newTestCLI(t)
	application.jsonOutput = true
	result := uninstallOutput{
		Action: "uninstall", Data: uninstallDataOutput{Path: application.paths.Root, Present: true},
		Daemon:     uninstallDaemonOutput{State: "stopped", InventoryAvailable: false, ActiveEnvironments: []string{}},
		Relay:      uninstallRelayOutput{State: "not installed"},
		Launcher:   launcherPlan{Kind: "regular-file", Action: "preserve", Reason: "source-tree build"},
		WillRemove: append([]string(nil), uninstallRemovalCategories...),
	}
	if err := application.printUninstallPreview(result); err != nil {
		t.Fatal(err)
	}
	var decoded uninstallOutput
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("preview is not valid JSON: %v\n%s", err, output.String())
	}
	if decoded.Action != "uninstall" || decoded.Data.Path != application.paths.Root || decoded.Launcher.Action != "preserve" || len(decoded.WillRemove) != 4 {
		t.Fatalf("unexpected JSON preview: %#v", decoded)
	}
}

func TestLauncherClassifierRemovesOnlySymlinkAndPreservesSourceBuild(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "bin", "portless")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bin", "portless")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, launcher); err != nil {
		t.Fatal(err)
	}

	plan := classifyLauncher(launcher, source, nil)
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != "remove" || plan.Kind != "symlink" || plan.Target != resolvedSource {
		t.Fatalf("unexpected launcher plan: %#v", plan)
	}
	removed, err := removeLauncher(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("launcher was not removed")
	}
	if _, err := os.Lstat(launcher); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launcher still exists: %v", err)
	}
	if content, err := os.ReadFile(source); err != nil || string(content) != "binary" {
		t.Fatalf("source build was changed: content=%q err=%v", content, err)
	}
}

func TestLauncherClassifierPreservesSourceTreeAndMismatchedSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "bin", "portless")
	other := filepath.Join(root, "other", "portless")
	for _, path := range []string{source, other} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if plan := classifyLauncher(source, source, []string{filepath.Join(root, "installed")}); plan.Action != "preserve" || !strings.Contains(plan.Reason, "source-tree") {
		t.Fatalf("source build was not preserved: %#v", plan)
	}
	launcher := filepath.Join(root, "bin", "portless")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, launcher); err != nil {
		t.Fatal(err)
	}
	if plan := classifyLauncher(launcher, source, nil); plan.Action != "preserve" || !strings.Contains(plan.Reason, "does not target") {
		t.Fatalf("mismatched symlink was not preserved: %#v", plan)
	}
}

func TestLauncherClassifierRemovesVerifiedRegularInstall(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "bin", "portless")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := classifyLauncher(launcher, launcher, []string{filepath.Dir(launcher)})
	if plan.Action != "remove" || plan.Kind != "regular-file" {
		t.Fatalf("verified installed executable was not removable: %#v", plan)
	}
}

func TestExecuteUninstallStepsUsesSafeOrderAndDoesNotCancelAfterStateRemoval(t *testing.T) {
	var calls []string
	preview := uninstallOutput{
		Action: "uninstall", Data: uninstallDataOutput{Path: "/tmp/state", Present: true},
		Relay: uninstallRelayOutput{Installed: true}, Launcher: launcherPlan{Path: "/tmp/bin/portless", Action: "remove"},
	}
	hooks := uninstallStepHooks{
		prepareRuntime: func(context.Context, bool) (uninstallRuntimePreparation, error) {
			calls = append(calls, "runtime")
			return uninstallRuntimePreparation{
				Prepared: true, Projects: 1, Environments: 2, ManagedVolumeEnvironments: 1, Processes: 3,
				Runtimes: []container.ResetResult{{Runtime: container.RuntimeDocker, Containers: 2, Volumes: 1, Networks: 1}},
				Cancel:   func() { calls = append(calls, "cancel") },
			}, nil
		},
		removeRelay: func(context.Context) (bool, error) {
			calls = append(calls, "relay")
			return true, nil
		},
		removeState: func(context.Context, bool) (bootstrap.InstallationStateRemoval, error) {
			calls = append(calls, "state")
			return bootstrap.InstallationStateRemoval{Path: "/tmp/state", Removed: true}, nil
		},
		removeLauncher: func(launcherPlan) (bool, error) {
			calls = append(calls, "launcher")
			return true, nil
		},
	}
	result, err := executeUninstallSteps(context.Background(), preview, uninstallOptions{yes: true, force: true}, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"runtime", "relay", "state", "launcher"}) {
		t.Fatalf("unexpected cleanup order: %#v", calls)
	}
	if !result.Complete || !result.Relay.Removed || !result.Data.Removed || !result.Launcher.Removed || result.ProcessesStopped != 3 {
		t.Fatalf("unexpected uninstall result: %#v", result)
	}
}

func TestExecuteUninstallStepsKeepsStateAndLauncherWhenRelayRemovalFails(t *testing.T) {
	var calls []string
	hooks := uninstallStepHooks{
		prepareRuntime: func(context.Context, bool) (uninstallRuntimePreparation, error) {
			calls = append(calls, "runtime")
			return uninstallRuntimePreparation{Prepared: true, Cancel: func() { calls = append(calls, "cancel") }}, nil
		},
		removeRelay: func(context.Context) (bool, error) {
			calls = append(calls, "relay")
			return false, errors.New("sudo denied")
		},
		removeState: func(context.Context, bool) (bootstrap.InstallationStateRemoval, error) {
			calls = append(calls, "state")
			return bootstrap.InstallationStateRemoval{}, nil
		},
		removeLauncher: func(launcherPlan) (bool, error) {
			calls = append(calls, "launcher")
			return true, nil
		},
	}
	_, err := executeUninstallSteps(context.Background(), uninstallOutput{}, uninstallOptions{yes: true}, hooks)
	if err == nil || !strings.Contains(err.Error(), "sudo denied") {
		t.Fatalf("unexpected relay failure: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"runtime", "relay", "cancel"}) {
		t.Fatalf("state or launcher changed after relay failure: %#v", calls)
	}
}

func TestPartialUninstallOutputSaysVerifiedLauncherIsStillInstalled(t *testing.T) {
	application, output, _ := newTestCLI(t)
	result := uninstallOutput{
		Relay:    uninstallRelayOutput{Installed: true},
		Data:     uninstallDataOutput{Path: application.paths.Root, Present: true},
		Launcher: launcherPlan{Path: "/Users/test/bin/portless", Action: "remove", Kind: "symlink"},
		Errors:   []string{"relay removal failed"},
	}
	if err := application.printUninstallComplete(result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "CLI launcher: still installed at /Users/test/bin/portless") {
		t.Fatalf("partial uninstall output misreported the launcher:\n%s", output.String())
	}
}

func TestExecuteUninstallStepsReportsLauncherAsOnlyPartialFailure(t *testing.T) {
	var cancelled bool
	hooks := uninstallStepHooks{
		prepareRuntime: func(context.Context, bool) (uninstallRuntimePreparation, error) {
			return uninstallRuntimePreparation{Prepared: true, Cancel: func() { cancelled = true }}, nil
		},
		removeRelay: func(context.Context) (bool, error) { return true, nil },
		removeState: func(context.Context, bool) (bootstrap.InstallationStateRemoval, error) {
			return bootstrap.InstallationStateRemoval{Removed: true}, nil
		},
		removeLauncher: func(launcherPlan) (bool, error) { return false, errors.New("permission denied") },
	}
	preview := uninstallOutput{Launcher: launcherPlan{Path: "/usr/local/bin/portless", Action: "remove"}}
	result, err := executeUninstallSteps(context.Background(), preview, uninstallOptions{yes: true}, hooks)
	if err == nil || !strings.Contains(err.Error(), "/usr/local/bin/portless") {
		t.Fatalf("unexpected launcher error: %v", err)
	}
	if result.Complete || len(result.Errors) != 1 || cancelled {
		t.Fatalf("unexpected partial uninstall result: %#v; cancelled=%v", result, cancelled)
	}
}

func TestActiveUninstallErrorOffersStoppedAndForcedPaths(t *testing.T) {
	err := activeUninstallError([]string{"store/local", "billing/qa"})
	for _, expected := range []string{"store/local", "billing/qa", "portless down --all", "portless uninstall --force --yes"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("active uninstall error does not contain %q: %v", expected, err)
		}
	}
}

func TestIncompatibleActiveUninstallRequiresForcedRecovery(t *testing.T) {
	err := incompatibleActiveUninstallError([]string{"store/local"})
	for _, expected := range []string{"store/local", "cannot be shut down individually", "portless uninstall --force --yes"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("incompatible uninstall error does not contain %q: %v", expected, err)
		}
	}
}

func TestFullUninstallNeverOverridesRelayOwnershipOrAnotherDataDirectory(t *testing.T) {
	paths, err := bootstrap.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owned := ingress.InstallationStatus{Installed: true, OwnerUID: 501, TargetSocket: paths.Ingress, DNSTargetSocket: paths.DNS}
	if blockers := relayUninstallBlockers(owned, paths, 501); len(blockers) != 0 {
		t.Fatalf("current installation was blocked: %#v", blockers)
	}

	foreignOwner := owned
	foreignOwner.OwnerUID = 502
	blockers := relayUninstallBlockers(foreignOwner, paths, 501)
	if len(blockers) != 1 || !strings.Contains(blockers[0], "will not override") || !strings.Contains(blockers[0], "relay uninstall --force") {
		t.Fatalf("foreign relay ownership was not a hard full-uninstall blocker: %#v", blockers)
	}

	foreignTarget := owned
	foreignTarget.TargetSocket = filepath.Join(t.TempDir(), "ingress.sock")
	blockers = relayUninstallBlockers(foreignTarget, paths, 501)
	if len(blockers) != 1 || !strings.Contains(blockers[0], "different Portless data directory") {
		t.Fatalf("foreign relay target was not blocked: %#v", blockers)
	}
}
