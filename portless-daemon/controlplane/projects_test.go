package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/events"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/runtime/supervisor"
)

func TestEnvironmentCanSwitchProviderAndSourceCheckout(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	first := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	worktree := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	_, local, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: first}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneEnvironment(ctx, "billing", "local", "hybrid"); err != nil {
		t.Fatal(err)
	}
	remote := model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: "https://checkout.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly,
	}}
	operation, err := app.ChangeBinding(ctx, "billing", "hybrid", "checkout", remote, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("remote provider operation = %#v", operation)
	}
	hybrid, err := app.Environment(ctx, "billing", "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	if hybrid.Bindings[0].Provider != model.ProviderRemote || local.Bindings[0].Provider != model.ProviderLocal {
		t.Fatalf("provider changes leaked between environments: local=%#v hybrid=%#v", local.Bindings, hybrid.Bindings)
	}
	createdAt := hybrid.Sources[0].CreatedAt
	hybrid, _, err = app.SetSourceCheckout(ctx, "billing", "hybrid", "checkout", worktree, "test")
	if err != nil {
		t.Fatal(err)
	}
	operation, err = app.ChangeBinding(ctx, "billing", "hybrid", "checkout", model.ComponentBinding{Provider: model.ProviderLocal, Source: "checkout"}, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("local provider operation = %#v", operation)
	}
	hybrid, err = app.Environment(ctx, "billing", "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if hybrid.Bindings[0].Provider != model.ProviderLocal || hybrid.Sources[0].Path != canonicalWorktree {
		t.Fatalf("hybrid environment did not use its worktree: %#v", hybrid)
	}
	if !hybrid.Sources[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("source path change replaced creation time: got %s, want %s", hybrid.Sources[0].CreatedAt, createdAt)
	}
}

func TestEnvironmentCanRemoveUnusedCheckoutWithoutDeletingProjectSource(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	checkout := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	project, local, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: checkout}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RemoveSourceCheckout(ctx, "billing", "local", "checkout", "test"); err == nil {
		t.Fatal("checkout removal succeeded while a Checkout provider still used it")
	} else {
		var inUse CheckoutInUseError
		if !errors.As(err, &inUse) || strings.Join(inUse.Services, ",") != "checkout" {
			t.Fatalf("checkout removal error = %#v, %v", inUse, err)
		}
	}
	if _, err := app.CloneEnvironment(ctx, "billing", "local", "hybrid"); err != nil {
		t.Fatal(err)
	}
	remote := model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: "https://checkout.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly,
	}}
	operation, err := app.ChangeBinding(ctx, "billing", "hybrid", "checkout", remote, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("remote provider operation = %#v", operation)
	}
	hybrid, err := app.RemoveSourceCheckout(ctx, "billing", "hybrid", "checkout", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if len(hybrid.Sources) != 0 || hybrid.Bindings[0].Provider != model.ProviderRemote {
		t.Fatalf("checkout removal changed the wrong configuration: %#v", hybrid)
	}
	project, err = app.Project(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Sources) != 1 || project.Sources[0].Name != "checkout" || len(local.Sources) != 1 {
		t.Fatalf("checkout removal changed project or sibling environment: project=%#v local=%#v", project.Sources, local.Sources)
	}
	timeline, err := app.Timeline(ctx, "billing", "hybrid", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) == 0 || timeline[0].Type != "environment.checkout_removed" || timeline[0].Actor != "browser" {
		t.Fatalf("checkout removal timeline = %#v", timeline)
	}
}

func TestProjectSourceAdditionIsGlobalAndBindsOnlyTheSelectedEnvironment(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	checkout := nestNamedFixture(t, filepath.Join(t.TempDir(), "checkout"), "checkout")
	if _, _, _, err := app.CreateProject(ctx, "store", []SourceInput{{Name: "checkout", Path: checkout}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneEnvironment(ctx, "store", "local", "qa"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneEnvironment(ctx, "store", "local", "remote"); err != nil {
		t.Fatal(err)
	}
	inventoryLocal := nestNamedFixture(t, filepath.Join(t.TempDir(), "inventory-local"), "inventory")
	project, local, warnings, err := app.AddProjectSource(ctx, "store", "local", "inventory", inventoryLocal, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 || len(project.Sources) != 2 || len(local.Sources) != 2 {
		t.Fatalf("addition = project %#v environment %#v warnings %v", project.Sources, local.Sources, warnings)
	}
	if binding, ok := bindingByName(local.Bindings, "inventory"); !ok || binding.Provider != model.ProviderLocal || binding.Source != "inventory" {
		t.Fatalf("local inventory binding = %#v, %v", binding, ok)
	}

	qa, err := app.Environment(ctx, "store", "qa")
	if err != nil {
		t.Fatal(err)
	}
	if len(qa.Sources) != 1 || len(qa.Issues) != 1 || qa.Issues[0].Code != "MISSING_BINDING" || qa.Issues[0].Subject != "inventory" {
		t.Fatalf("qa after global source addition = %#v", qa)
	}
	listed, err := app.Environments(ctx, "store")
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range listed {
		if candidate.Name != "local" && (len(candidate.Issues) != 1 || candidate.Issues[0].Subject != "inventory") {
			t.Fatalf("listed environment %s did not expose its source-addition issue: %#v", candidate.Name, candidate.Issues)
		}
	}
	if _, err := app.Up(ctx, "store", "qa", "test", "", UpOptions{}); err == nil || !strings.Contains(err.Error(), "no provider binding") {
		t.Fatalf("incomplete environment start error = %v", err)
	}

	inventoryQA := nestNamedFixture(t, filepath.Join(t.TempDir(), "inventory-qa"), "inventory")
	qa, _, err = app.SetSourceCheckout(ctx, "store", "qa", "inventory", inventoryQA, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(qa.Issues) != 0 || len(qa.Sources) != 2 {
		t.Fatalf("configured qa = %#v", qa)
	}
	qaBinding, ok := bindingByName(qa.Bindings, "inventory")
	if !ok || qaBinding.Provider != model.ProviderLocal || qaBinding.Source != "inventory" {
		t.Fatalf("qa inventory binding = %#v, %v", qaBinding, ok)
	}
	if sourcePath(qa.Sources, "inventory") == sourcePath(local.Sources, "inventory") {
		t.Fatal("selected-environment source path leaked into another environment")
	}

	operation, err := app.ChangeBinding(ctx, "store", "remote", "inventory", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: "https://inventory.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly,
	}}, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("remote inventory operation = %#v", operation)
	}
	remote, err := app.Environment(ctx, "store", "remote")
	if err != nil {
		t.Fatal(err)
	}
	remoteBinding, ok := bindingByName(remote.Bindings, "inventory")
	if !ok || remoteBinding.Provider != model.ProviderRemote || len(remote.Issues) != 0 || sourcePath(remote.Sources, "inventory") != "" {
		t.Fatalf("remote inventory configuration = environment %#v binding %#v", remote, remoteBinding)
	}
}

func TestProjectSourceAdditionRequiresEveryEnvironmentToBeStoppedAndRollsBack(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	checkout := nestNamedFixture(t, filepath.Join(t.TempDir(), "checkout"), "checkout")
	if _, _, _, err := app.CreateProject(ctx, "store", []SourceInput{{Name: "checkout", Path: checkout}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneEnvironment(ctx, "store", "local", "qa"); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "store", "qa", model.EnvironmentHealthy, "test"); err != nil {
		t.Fatal(err)
	}
	inventory := nestNamedFixture(t, filepath.Join(t.TempDir(), "inventory"), "inventory")
	_, _, _, err = app.AddProjectSource(ctx, "store", "local", "inventory", inventory, "test")
	var active database.ActiveProjectEnvironmentsError
	if !errors.As(err, &active) || strings.Join(active.Environments, ",") != "store/qa" {
		t.Fatalf("active environment error = %#v, %v", active, err)
	}
	project, err := app.Project(ctx, "store")
	if err != nil {
		t.Fatal(err)
	}
	local, err := app.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Sources) != 1 || len(local.Sources) != 1 {
		t.Fatalf("source addition was partially committed: project=%#v local=%#v", project.Sources, local.Sources)
	}
}

func TestProjectSourceRemovalUpdatesEveryStoppedEnvironmentAndDeletesOwnedMocks(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	checkout := nestNamedFixture(t, filepath.Join(t.TempDir(), "checkout"), "checkout")
	if _, _, _, err := app.CreateProject(ctx, "store", []SourceInput{{Name: "checkout", Path: checkout}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CloneEnvironment(ctx, "store", "local", "qa"); err != nil {
		t.Fatal(err)
	}
	inventory := nestNamedFixture(t, filepath.Join(t.TempDir(), "inventory"), "inventory")
	if _, _, _, err := app.AddProjectSource(ctx, "store", "local", "inventory", inventory, "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.SetSourceCheckout(ctx, "store", "qa", "inventory", inventory, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateMockProfile(ctx, "store", "local", model.MockProfile{Name: "empty-inventory", Service: "inventory"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateFault(ctx, model.FaultRule{Project: "store", Environment: "local", Name: "inventory-timeout", Source: "checkout", Target: "inventory", Abort: true}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "store", "qa", model.EnvironmentHealthy, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RemoveProjectSource(ctx, "store", "inventory", "test"); err == nil {
		t.Fatal("active environment did not block source removal")
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "store", "qa", model.EnvironmentStopped, "test"); err != nil {
		t.Fatal(err)
	}

	removed, err := app.RemoveProjectSource(ctx, "store", "inventory", "test")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed.RemovedServices, ",") != "inventory" || len(removed.Environments) != 2 {
		t.Fatalf("removal = %#v", removed)
	}
	if len(removed.Project.Sources) != 1 || removed.Project.Sources[0].Name != "checkout" {
		t.Fatalf("project sources = %#v", removed.Project.Sources)
	}
	for _, environment := range removed.Environments {
		if sourcePath(environment.Sources, "inventory") != "" {
			t.Fatalf("environment %s retained inventory source: %#v", environment.Name, environment.Sources)
		}
		if _, ok := bindingByName(environment.Bindings, "inventory"); ok {
			t.Fatalf("environment %s retained inventory binding: %#v", environment.Name, environment.Bindings)
		}
		for _, service := range environment.Services {
			if strings.EqualFold(service.Name, "inventory") {
				t.Fatalf("environment %s retained inventory service: %#v", environment.Name, environment.Services)
			}
		}
	}
	mocks, err := app.MockProfiles(ctx, "store", "local")
	if err != nil || len(mocks) != 0 {
		t.Fatalf("owned mocks after source removal = %#v, err = %v", mocks, err)
	}
	fault, err := controlStore.Fault(ctx, "store/local", "inventory-timeout")
	if err != nil || fault.Enabled || fault.Revision != 2 {
		t.Fatalf("obsolete fault after source removal = %#v, err = %v", fault, err)
	}
	if _, err := app.RemoveProjectSource(ctx, "store", "checkout", "test"); err == nil || !strings.Contains(err.Error(), "retain at least one") {
		t.Fatalf("last source removal error = %v", err)
	}
	if _, err := app.RemoveProjectSource(ctx, "store", "missing", "test"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("missing source removal error = %v", err)
	}
}

func TestProjectSourceRemovalPersistsARequiredDependencyAsAnExplicitIssue(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "checkout",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess, Required: true},
			{Name: "inventory", Kind: model.ServiceProcess, Required: true},
		},
		Connections: []model.Connection{{Source: "checkout", Target: "inventory", Protocol: model.ProtocolHTTP, Environment: "INVENTORY_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}, {Name: "inventory", Services: []string{"inventory"}}}); err != nil {
		t.Fatal(err)
	}
	checkoutDefinition := model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}, References: []model.ConnectionReference{{Source: "checkout", TargetHint: "inventory", Protocol: model.ProtocolHTTP, Environment: "INVENTORY_URL", Required: true}}}
	inventoryDefinition := model.ProjectModel{Services: []model.ServiceDefinition{{Name: "inventory", Kind: model.ServiceProcess}}}
	sources := []model.SourceBinding{
		{Name: "checkout", Path: t.TempDir(), Definition: checkoutDefinition},
		{Name: "inventory", Path: t.TempDir(), Definition: inventoryDefinition},
	}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"},
		{Service: "inventory", Provider: model.ProviderLocal, Source: "inventory"},
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	removed, err := app.RemoveProjectSource(ctx, "store", "inventory", "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.RemovedConnections) != 1 || len(removed.Environments) != 1 || !containsProjectIssue(removed.Environments[0].Issues, "UNRESOLVED_CONNECTION", "checkout:inventory") {
		t.Fatalf("source removal did not retain the required dependency issue: %#v", removed)
	}
}

func TestPrepareResetRequiresStoppedIdleEnvironmentsAndBlocksNewStarts(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, sources, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareReset(ctx, false); err == nil || !strings.Contains(err.Error(), "billing/local") {
		t.Fatalf("active environment did not block reset: %v", err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentStopped, ""); err != nil {
		t.Fatal(err)
	}
	operation, err := controlStore.CreateOperation(ctx, "billing/local", "up", "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareReset(ctx, false); err == nil || !strings.Contains(err.Error(), "operations are still running") {
		t.Fatalf("running operation did not block reset: %v", err)
	}
	if err := controlStore.CompleteOperation(ctx, "billing/local", operation.Number, "failed", "test cleanup"); err != nil {
		t.Fatal(err)
	}
	result, err := app.PrepareReset(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processes != 0 || len(result.Runtimes) != 0 {
		t.Fatalf("unexpected reset runtime cleanup: %#v", result)
	}
	if _, err := app.Up(ctx, "billing", "local", "test", "", UpOptions{}); err == nil || !strings.Contains(err.Error(), "reset preparation") {
		t.Fatalf("new start was accepted after reset preparation: %v", err)
	}
	app.CancelReset()
	if app.resetting {
		t.Fatal("reset cancellation did not reopen runtime starts")
	}
}

func TestResetRecoveryUsesRuntimeInventoryWhenStoredTopologyIsIncompatible(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "store", Services: []model.ServiceDefinition{{Name: "api", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(ctx, "store", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "store", "local", model.EnvironmentHealthy, "legacy runtime is active"); err != nil {
		t.Fatal(err)
	}
	legacyModel, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE projects SET model_json = ?`, legacyModel); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE environments SET model_json = ?`, legacyModel); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	if _, err := app.Environments(ctx, ""); !errors.Is(err, database.ErrIncompatibleState) {
		t.Fatalf("ordinary environment inventory error = %v, want ErrIncompatibleState", err)
	}
	active, err := app.ActiveEnvironments(ctx)
	if err != nil || strings.Join(active, ",") != "store/local" {
		t.Fatalf("format-independent active inventory = %#v, err = %v", active, err)
	}
	plan, err := app.ResetPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Projects != 1 || plan.Environments != 1 || !plan.TopologyIncompatible || strings.Join(plan.ActiveEnvironments, ",") != "store/local" {
		t.Fatalf("unexpected incompatible-state reset plan: %#v", plan)
	}
	if _, err := app.PrepareReset(ctx, false); err == nil || !strings.Contains(err.Error(), "store/local") {
		t.Fatalf("ordinary reset accepted active incompatible state: %v", err)
	}
	if _, err := app.PrepareReset(ctx, true); err != nil {
		t.Fatalf("forced reset could not prepare incompatible state: %v", err)
	}
	app.CancelReset()
}

func TestPrepareResetStopsAuthenticatedLingeringSupervisor(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{
		Name: "checkout", Kind: model.ServiceProcess, Required: true,
		Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
		PortEnvironment: "PORT", Health: model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
	}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, sources, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-test", Executable: os.Args[0]})
	defer app.Close(ctx)
	operation, err := app.StartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("start = %#v", operation)
	}
	runtime, err := controlStore.ServiceRuntime(ctx, "billing/local", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentUnknown, "simulated failed recovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareReset(ctx, false); err == nil || !strings.Contains(err.Error(), "billing/local") {
		t.Fatalf("ordinary reset accepted unknown environment: %v", err)
	}
	result, err := app.PrepareReset(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processes != 1 {
		t.Fatalf("stopped supervisors = %d, want 1", result.Processes)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, liveErr := supervisor.LiveStatus(probeCtx, runtime.SupervisorSocket, runtime.PrivateRunKey)
	cancel()
	if liveErr == nil {
		t.Fatal("lingering supervisor is still reachable after reset preparation")
	}
}

func TestPrepareResetAcceptsProvablyGoneReadySupervisor(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	const deadPID = 1 << 30
	statePath := filepath.Join(data, "runs", "stale.state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	status := supervisor.Status{
		ProtocolVersion: supervisor.ProtocolVersion, Scope: "billing/local", Service: "checkout", Generation: 3,
		SupervisorPID: deadPID + 1, PID: deadPID, Port: 43123, LaunchMode: model.LaunchManaged, State: "ready",
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetServiceRuntime(ctx, "billing/local", "checkout", database.ServiceRuntimeUpdate{
		Status: model.ServiceUnknown, Generation: 3, PID: deadPID, UpstreamPort: status.Port,
		PrivateRunKey: "private-run-key", OwnerInstanceID: "daemon-before-reboot",
		SupervisorSocket: filepath.Join(data, "missing.sock"), SupervisorState: statePath,
		SupervisorPID: deadPID + 1, LaunchMode: model.LaunchManaged,
	}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentUnknown, "checkout state cannot be verified"); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	result, err := app.PrepareReset(ctx, true)
	if err != nil {
		t.Fatalf("forced reset rejected a provably gone runtime: %v", err)
	}
	if result.Processes != 0 {
		t.Fatalf("forced reset reported stopping an absent supervisor: %#v", result)
	}
	app.CancelReset()
}

func TestPrepareResetRefusesIncompletePersistedProcessOwnership(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal}}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetServiceRuntime(ctx, "billing/local", "checkout", database.ServiceRuntimeUpdate{
		Status: model.ServiceUnknown, Generation: 2, PID: 1 << 30, OwnerInstanceID: "daemon-before-reboot",
	}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentUnknown, "previous ownership is incomplete"); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	if _, err := app.PrepareReset(ctx, true); err == nil || !strings.Contains(err.Error(), "persisted supervisor ownership record is incomplete") {
		t.Fatalf("forced reset accepted incomplete process ownership: %v", err)
	}
}

func TestCreateProjectRejectsDaemonRelativeSourcePath(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	_, _, _, err = app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: "."}})
	if err == nil {
		t.Fatal("relative source path was accepted by the daemon")
	}
}

func TestEnvironmentsForPathDecoratesResolvedEnvironmentURLs(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	source := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	if _, _, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: source}}); err != nil {
		t.Fatal(err)
	}
	environments, err := app.EnvironmentsForPath(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(environments) != 1 {
		t.Fatalf("resolved %d environments, want 1", len(environments))
	}
	resolved := environments[0]
	if resolved.DashboardURL != "http://portless.localhost/environments/billing/local" {
		t.Fatalf("dashboard URL = %q", resolved.DashboardURL)
	}
	if len(resolved.Services) != 1 || len(resolved.Services[0].Endpoints) != 1 || resolved.Services[0].Endpoints[0].URL != "http://checkout.local.billing.localhost" {
		t.Fatalf("resolved services were not decorated: %#v", resolved.Services)
	}
}

func TestRescanRemovesConnectionNoLongerDiscoveredFromSource(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	source := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	environmentFile := filepath.Join(source, ".env.example")
	if err := os.WriteFile(environmentFile, []byte("REDIS_URL=redis://redis\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, environment, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(environment.Connections) != 1 || environment.Connections[0].Source != "checkout" || environment.Connections[0].Target != "redis" {
		t.Fatalf("initial connections = %#v", environment.Connections)
	}
	if err := os.WriteFile(environmentFile, []byte("LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rescanned, _, err := app.Rescan(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(rescanned.Connections) != 0 {
		t.Fatalf("rescanned connections = %#v, want none", rescanned.Connections)
	}
	projectDefinition, err := app.ProjectModel(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if len(projectDefinition.Connections) != 0 {
		t.Fatalf("stored project connections = %#v, want none", projectDefinition.Connections)
	}
}

func TestIncompleteRescanPreservesLastCompleteDiscovery(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	source := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	_, before, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"name":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.Rescan(ctx, "billing", "local"); err == nil {
		t.Fatal("malformed discovery unexpectedly replaced the stored source")
	}
	after, err := app.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Services) != 1 || len(after.Services) != 1 || before.Services[0].Name != after.Services[0].Name {
		t.Fatalf("last complete discovery was not preserved: before=%#v after=%#v", before.Services, after.Services)
	}
}

func TestEnvironmentDecoratesServicesWithRecentHTTPTraffic(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{
		{Name: "checkout", Kind: model.ServiceProcess},
		{Name: "orders", Kind: model.ServiceProcess},
	}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal},
		{Service: "orders", Provider: model.ProviderLocal},
	}); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	app := New(controlStore, broker, Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	now := time.Now().UTC()
	app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Target: "orders", CompletedAt: now.Add(-time.Second), DurationMS: 10})
	app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Target: "orders", CompletedAt: now.Add(-2 * time.Second), DurationMS: 100})
	app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Target: "checkout", CompletedAt: now.Add(-time.Minute), DurationMS: 999})
	app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolTCP, Target: "orders", CompletedAt: now, DurationMS: 1})

	environment, err := app.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range environment.Services {
		switch service.Name {
		case "orders":
			if service.RecentRequest != 2 || service.P95Millis != 100 {
				t.Fatalf("orders traffic = %d requests at p95 %dms", service.RecentRequest, service.P95Millis)
			}
		case "checkout":
			if service.RecentRequest != 0 || service.P95Millis != 0 {
				t.Fatalf("stale checkout traffic was counted: %#v", service)
			}
		}
	}
}

func TestEnvironmentContextExplainsSelectionAndInference(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	source := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	if _, _, _, err := app.CreateProject(ctx, "billing", []SourceInput{{Name: "checkout", Path: source}}); err != nil {
		t.Fatal(err)
	}
	resolved, err := app.EnvironmentContext(ctx, source)
	if err != nil || resolved.Resolution != "inferred" || resolved.Environment == nil || resolved.Environment.Name != "local" {
		t.Fatalf("inferred context = %#v, err = %v", resolved, err)
	}
	if _, err := app.CloneEnvironment(ctx, "billing", "local", "qa-assisted"); err != nil {
		t.Fatal(err)
	}
	resolved, err = app.EnvironmentContext(ctx, source)
	if err != nil || resolved.Resolution != "ambiguous" || resolved.Environment != nil || len(resolved.Candidates) != 2 {
		t.Fatalf("ambiguous context = %#v, err = %v", resolved, err)
	}
	if err := app.SelectEnvironment(ctx, source, "billing", "qa-assisted"); err != nil {
		t.Fatal(err)
	}
	resolved, err = app.EnvironmentContext(ctx, source)
	if err != nil || resolved.Resolution != "selected" || resolved.Environment == nil || resolved.Environment.Name != "qa-assisted" || len(resolved.Candidates) != 0 {
		t.Fatalf("selected context = %#v, err = %v", resolved, err)
	}
	cleared, err := app.ClearEnvironmentSelection(ctx, source)
	if err != nil || !cleared {
		t.Fatalf("clear = %v, %v", cleared, err)
	}
	resolved, err = app.EnvironmentContext(ctx, source)
	if err != nil || resolved.Resolution != "ambiguous" || len(resolved.Candidates) != 2 {
		t.Fatalf("context after clear = %#v, err = %v", resolved, err)
	}
}

func containsProjectIssue(issues []model.ConfigurationIssue, code, subject string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Subject == subject {
			return true
		}
	}
	return false
}
