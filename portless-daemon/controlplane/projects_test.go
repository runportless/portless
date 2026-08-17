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
	hybrid, err := app.SetBinding(ctx, "billing", "hybrid", "checkout", remote)
	if err != nil {
		t.Fatal(err)
	}
	if hybrid.Bindings[0].Provider != model.ProviderRemote || local.Bindings[0].Provider != model.ProviderLocal {
		t.Fatalf("provider changes leaked between environments: local=%#v hybrid=%#v", local.Bindings, hybrid.Bindings)
	}
	hybrid, _, err = app.SetSource(ctx, "billing", "hybrid", "checkout", worktree)
	if err != nil {
		t.Fatal(err)
	}
	hybrid, err = app.SetBinding(ctx, "billing", "hybrid", "checkout", model.ComponentBinding{Provider: model.ProviderLocal, Source: "checkout"})
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
	project, local, warnings, err := app.AddProjectSource(ctx, "store", "local", "inventory", inventoryLocal)
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
	qa, _, err = app.SetSource(ctx, "store", "qa", "inventory", inventoryQA)
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

	remote, err := app.SetBinding(ctx, "store", "remote", "inventory", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: "https://inventory.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly,
	}})
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
	_, _, _, err = app.AddProjectSource(ctx, "store", "local", "inventory", inventory)
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
	operation, err := controlStore.CreateOperation(ctx, "billing/local", "up", "test", "")
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
	operation, err := app.StartService(ctx, "billing", "local", "checkout", "test")
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
	broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Target: "orders", CompletedAt: now.Add(-time.Second), DurationMS: 10})
	broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Target: "orders", CompletedAt: now.Add(-2 * time.Second), DurationMS: 100})
	broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Target: "checkout", CompletedAt: now.Add(-time.Minute), DurationMS: 999})
	broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolTCP, Target: "orders", CompletedAt: now, DurationMS: 1})

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
