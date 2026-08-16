package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery"
	resourcebuiltin "github.com/portless-run/portless/internal/resource/builtin"
	"github.com/portless-run/portless/internal/runtime/supervisor"
	"github.com/portless-run/portless/internal/store"
)

func TestMain(testingMain *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "__runner" && os.Args[2] == "--manifest" {
		if err := supervisor.Run(context.Background(), os.Args[3]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(testingMain.Run())
}

type fixtureDiscoverer struct {
	result discovery.Result
	path   string
}

func (d *fixtureDiscoverer) FindRoot(context.Context, string) (string, error) {
	return d.result.Root, nil
}

func (d *fixtureDiscoverer) Discover(_ context.Context, path string) (discovery.Result, error) {
	d.path = path
	return d.result, nil
}

func TestApplicationUsesInjectedDiscoveryEngine(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	discoverer := &fixtureDiscoverer{result: discovery.Result{Root: source, Model: model.ProjectModel{
		SuggestedName: "fixture", PrimaryService: "api",
		Services: []model.ServiceDefinition{{
			Name: "api", Kind: model.ServiceProcess, Framework: "fixture", Command: []string{"serve"}, WorkingDirectory: source,
			PortEnvironment: "PORT", Required: true, Health: model.HealthCheck{Kind: "tcp", Timeout: time.Second, Interval: time.Second},
		}},
	}}}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", Discoverer: discoverer})
	defer app.Close(ctx)

	_, environment, _, err := app.CreateProject(ctx, "fixture", []SourceInput{{Name: "fixture", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if discoverer.path != source || len(environment.Services) != 1 || environment.Services[0].Framework != "fixture" {
		t.Fatalf("injected discovery result was not used: path=%q environment=%#v", discoverer.path, environment)
	}
}

func TestEnvironmentCanSwitchProviderAndSourceCheckout(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	var active store.ActiveProjectEnvironmentsError
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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

	if _, err := app.Environments(ctx, ""); !errors.Is(err, store.ErrIncompatibleState) {
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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

func TestUpStartsFocusedServiceUnderPortlessWithDebugger(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	definition := model.ProjectModel{
		SuggestedName: "billing", PrimaryService: "checkout",
		Services: []model.ServiceDefinition{{
			Name: "checkout", Kind: model.ServiceProcess, Framework: "nestjs", Required: true,
			Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, WorkingDirectory: source, ServiceDirectory: source,
			Debug:           &model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNestCLI, Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}},
			PortEnvironment: "PORT", Environment: map[string]string{"LOG_LEVEL": "debug", "PORTLESS_APPLICATION_TEST_HELPER": "1"},
			Health: model.HealthCheck{Kind: "http", Path: "/health", Timeout: time.Second, Interval: 20 * time.Millisecond},
		}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "billing", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "billing", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "billing"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-one", Executable: os.Args[0]})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), "billing/local", "checkout", time.Second)

	operation, err := app.Up(ctx, "billing", "local", "test", "debug-up", UpOptions{DebugServices: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("up operation = %#v", operation)
	}
	service := serviceSnapshot(t, app, "checkout")
	if service.Status != model.ServiceReady || service.LaunchMode != model.LaunchDebug || service.Debugger == nil || service.Debugger.State != "listening" || service.PID == 0 {
		t.Fatalf("debug service = %#v", service)
	}
	environment, err := app.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if environment.Status != model.EnvironmentDevelopment {
		t.Fatalf("environment status = %s, want development", environment.Status)
	}
	assertIngressStatus(t, app, http.StatusOK)
	response, err := http.Get("http://" + net.JoinHostPort(service.Debugger.Host, strconv.Itoa(service.Debugger.Port)) + "/json/list")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("debugger status = %s", response.Status)
	}
	debugPort := service.Debugger.Port
	managed, err := app.ManageService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if managed = waitForOperation(t, app, managed); managed.State != "succeeded" {
		t.Fatalf("manage operation = %#v", managed)
	}
	service = serviceSnapshot(t, app, "checkout")
	if service.LaunchMode != model.LaunchManaged || service.Debugger != nil || service.Status != model.ServiceReady || service.Generation != 2 {
		t.Fatalf("managed service = %#v", service)
	}
	probe, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(probe, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(debugPort)+"/json/list", nil)
	if response, requestErr := http.DefaultClient.Do(request); requestErr == nil {
		response.Body.Close()
		t.Fatal("old debugger remained reachable after returning to managed mode")
	}
}

func TestReconcileAdoptsDebugProcessAndDebugger(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	definition := model.ProjectModel{
		SuggestedName: "billing", PrimaryService: "checkout",
		Services: []model.ServiceDefinition{{
			Name: "checkout", Kind: model.ServiceProcess, Framework: "nestjs", Required: true,
			Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, WorkingDirectory: source, ServiceDirectory: source,
			Debug:           &model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNestCLI, Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}},
			Environment:     map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			PortEnvironment: "PORT",
			Health:          model.HealthCheck{Kind: "http", Path: "/health", Timeout: time.Second, Interval: 20 * time.Millisecond},
		}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "billing", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "billing", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "billing"}}); err != nil {
		t.Fatal(err)
	}

	first := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-one", Executable: os.Args[0]})
	operation, err := first.Up(ctx, "billing", "local", "test", "debug-up", UpOptions{DebugServices: []string{"checkout"}})
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, first, operation); operation.State != "succeeded" {
		t.Fatalf("up operation = %#v", operation)
	}
	before := serviceSnapshot(t, first, "checkout")
	first.Close(ctx)

	second := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-two", Executable: os.Args[0]})
	defer second.Close(ctx)
	defer second.processes.Stop(context.Background(), "billing/local", "checkout", time.Second)
	report, err := second.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unverifiable) != 0 || len(report.Recovered) != 1 || report.Recovered[0] != "billing/local" {
		t.Fatalf("reconciliation report = %#v", report)
	}
	recovered := serviceSnapshot(t, second, "checkout")
	if recovered.LaunchMode != model.LaunchDebug || recovered.Debugger == nil || recovered.Debugger.State != "listening" || recovered.Debugger.Port != before.Debugger.Port || recovered.PID != before.PID {
		t.Fatalf("debug process changed across daemon restart: before=%#v after=%#v", before, recovered)
	}
	assertIngressStatus(t, second, http.StatusOK)
	if ok, reasons := second.CanHandoff(ctx); !ok {
		t.Fatalf("recovered debug environment cannot hand off: %v", reasons)
	}
}

func TestIndividualServiceLifecycleIsIdempotentAndCountsRestarts(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing",
		Services: []model.ServiceDefinition{{
			Name: "checkout", Kind: model.ServiceProcess, Required: true,
			Command:         []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"},
			Environment:     map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			PortEnvironment: "PORT",
			Health:          model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
		}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, sources, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), model.EnvironmentSelector("billing", "local"), "checkout", time.Second)

	started, err := app.StartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	concurrent, err := app.StartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	started = waitForOperation(t, app, started)
	concurrent = waitForOperation(t, app, concurrent)
	if started.State != "succeeded" {
		t.Fatalf("start operation = %#v", started)
	}
	if concurrent.State != "succeeded" {
		t.Fatalf("concurrent idempotent start = %#v", concurrent)
	}
	service := serviceSnapshot(t, app, "checkout")
	if service.Status != model.ServiceReady || service.Generation != 1 || service.RestartCount != 0 {
		t.Fatalf("started service = %#v", service)
	}

	again, err := app.StartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	again = waitForOperation(t, app, again)
	if again.State != "succeeded" || serviceSnapshot(t, app, "checkout").Generation != 1 {
		t.Fatalf("idempotent start changed the running generation: %#v", again)
	}

	restarted, err := app.RestartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	restarted = waitForOperation(t, app, restarted)
	service = serviceSnapshot(t, app, "checkout")
	if restarted.State != "succeeded" || service.Status != model.ServiceReady || service.Generation != 2 || service.RestartCount != 1 {
		t.Fatalf("restarted service = %#v, operation = %#v", service, restarted)
	}

	stopped, err := app.StopService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	stopped = waitForOperation(t, app, stopped)
	service = serviceSnapshot(t, app, "checkout")
	if stopped.State != "succeeded" || service.Status != model.ServiceStopped || service.Generation != 2 || service.RestartCount != 1 {
		t.Fatalf("stopped service = %#v, operation = %#v", service, stopped)
	}

	stoppedAgain, err := app.StopService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitForOperation(t, app, stoppedAgain); completed.State != "succeeded" {
		t.Fatalf("idempotent stop = %#v", completed)
	}
}

func TestEffectiveConnectionsAndConfigurationExplainRemoteTargetsAndMaskCredentials(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess, Environment: map[string]string{"DATABASE_URL": "postgresql://user:secret@localhost/billing", "JDBC_URL": "jdbc:postgresql://user:secret@localhost/billing", "LOG_LEVEL": "debug"}},
			{Name: "payments", Kind: model.ServiceProcess},
		},
		Connections: []model.Connection{{Source: "checkout", Target: "payments", Protocol: model.ProtocolHTTP, Environment: "PAYMENTS_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	remoteURL := "https://payments.qa.example.test"
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "hybrid", definition, nil, []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remoteURL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}},
	}); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	connections, err := app.Connections(ctx, "billing", "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].TargetProvider != model.ProviderRemote || connections[0].RuntimeTarget != remoteURL || connections[0].InjectedEnvironment["PAYMENTS_URL"] == "" {
		t.Fatalf("effective connections = %#v", connections)
	}
	configuration, err := app.ServiceConfiguration(ctx, "billing", "hybrid", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]model.ConfigurationValue)
	for _, value := range configuration.Environment {
		values[value.Key] = value
	}
	if values["DATABASE_URL"].Classification != "masked" || values["DATABASE_URL"].Value != "••••••••" {
		t.Fatalf("credential URL was exposed: %#v", values["DATABASE_URL"])
	}
	if values["JDBC_URL"].Classification != "masked" || values["JDBC_URL"].Value != "••••••••" {
		t.Fatalf("JDBC credential URL was exposed: %#v", values["JDBC_URL"])
	}
	if values["LOG_LEVEL"].Value != "debug" || values["LOG_LEVEL"].Classification != "public" {
		t.Fatalf("public configuration was not preserved: %#v", values["LOG_LEVEL"])
	}
	if values["PAYMENTS_URL"].Classification != "generated" {
		t.Fatalf("generated connection value was not explained: %#v", values["PAYMENTS_URL"])
	}
}

func TestStableTCPServiceAndConnectionEndpointsAreExposedBeforeStartup(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "orders",
		Services: []model.ServiceDefinition{
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432},
		},
		Connections: []model.Connection{{Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, Binding: "postgres", Environment: "DATABASE_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	environment, err := app.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	var postgres model.Service
	for _, service := range environment.Services {
		if service.Name == "postgres" {
			postgres = service
			break
		}
	}
	if len(postgres.Endpoints) != 1 || postgres.Endpoints[0].Host != "postgres.local.store.portless.test" || postgres.Endpoints[0].Port != 5432 {
		t.Fatalf("public Postgres endpoint = %#v", postgres.Endpoints)
	}
	connections, err := app.Connections(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 1 || connections[0].Endpoint == nil || connections[0].Endpoint.Host != "postgres.via-orders.local.store.portless.test" || connections[0].Endpoint.Port != 5432 {
		t.Fatalf("directed Postgres endpoint = %#v", connections)
	}
	if connections[0].InjectedEnvironment["DATABASE_URL"] != "not active" {
		t.Fatalf("stopped connection was reported as injected: %#v", connections[0])
	}
}

func TestFaultsRemainActiveUntilDisabledUnlessExpiryIsRequested(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	persistent, err := app.CreateFault(ctx, model.FaultRule{
		Project: "billing", Environment: "local", Name: "persistent-latency",
		Source: "external", Target: "checkout", Probability: 1, LatencyMS: 250,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !persistent.Enabled || persistent.ExpiresAt != nil {
		t.Fatalf("default fault = %#v, want enabled with no expiry", persistent)
	}

	expires := time.Now().UTC().Add(2 * time.Hour)
	timed, err := app.CreateFault(ctx, model.FaultRule{
		Project: "billing", Environment: "local", Name: "timed-latency",
		Source: "external", Target: "checkout", Probability: 1, LatencyMS: 500, ExpiresAt: &expires,
	}, "test")
	if err != nil {
		t.Fatalf("two-hour timed fault was rejected: %v", err)
	}
	if timed.ExpiresAt == nil || !timed.ExpiresAt.Equal(expires) {
		t.Fatalf("timed fault expiry = %v, want %v", timed.ExpiresAt, expires)
	}

	past := time.Now().UTC().Add(-time.Minute)
	if _, err := app.CreateFault(ctx, model.FaultRule{
		Project: "billing", Environment: "local", Name: "expired-latency",
		Source: "external", Target: "checkout", Probability: 1, LatencyMS: 500, ExpiresAt: &past,
	}, "test"); err == nil || !strings.Contains(err.Error(), "must be in the future") {
		t.Fatalf("past expiry error = %v", err)
	}

	disabled, err := app.DisableAllFaults(ctx, "billing", "local", "test")
	if err != nil {
		t.Fatal(err)
	}
	if disabled != 2 {
		t.Fatalf("disabled fault count = %d, want 2", disabled)
	}
	persistent, err = app.EnableFault(ctx, "billing", "local", persistent.Name, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !persistent.Enabled || persistent.Revision != 3 {
		t.Fatalf("re-enabled fault = %#v, want enabled revision 3", persistent)
	}
	if err := app.DisableFault(ctx, "billing", "local", persistent.Name, "test"); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteFault(ctx, "billing", "local", persistent.Name, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Fault(ctx, "billing", "local", persistent.Name); err == nil {
		t.Fatal("deleted fault rule is still readable")
	}
}

func TestApplicationRestoresTrafficSequenceFromRetainedRecordings(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "retained"}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.PersistTraffic(ctx, model.TrafficEvent{Project: "billing", Environment: "local", Recording: "retained", Sequence: 41}); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	app := New(controlStore, broker, Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	event := broker.AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP})
	if event.Sequence != 42 {
		t.Fatalf("restored sequence = %d, want 42", event.Sequence)
	}
}

func TestIndividualServiceStartHonorsCrossEnvironmentSourceLeases(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{
		Name: "checkout", Kind: model.ServiceProcess, Required: true,
		Command:         []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"},
		Environment:     map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
		PortEnvironment: "PORT",
		Health:          model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
	}}}
	projectSources := []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, projectSources); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: source, Status: "ready", Definition: definition}}
	bindings := []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "experiment", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), model.EnvironmentSelector("billing", "local"), "checkout", time.Second)
	defer app.processes.Stop(context.Background(), model.EnvironmentSelector("billing", "experiment"), "checkout", time.Second)

	local, err := app.StartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if local = waitForOperation(t, app, local); local.State != "succeeded" {
		t.Fatalf("local start = %#v", local)
	}
	experiment, err := app.StartService(ctx, "billing", "experiment", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if experiment = waitForOperation(t, app, experiment); experiment.State != "failed" || !strings.Contains(experiment.Error, "already running") {
		t.Fatalf("shared-checkout start = %#v", experiment)
	}
	stopped, err := app.StopService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if stopped = waitForOperation(t, app, stopped); stopped.State != "succeeded" {
		t.Fatalf("local stop = %#v", stopped)
	}
	experiment, err = app.StartService(ctx, "billing", "experiment", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if experiment = waitForOperation(t, app, experiment); experiment.State != "succeeded" {
		t.Fatalf("experiment start after lease release = %#v", experiment)
	}
	stopped, err = app.StopService(ctx, "billing", "experiment", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if stopped = waitForOperation(t, app, stopped); stopped.State != "succeeded" {
		t.Fatalf("experiment stop = %#v", stopped)
	}
}

func TestIndividualServiceStartPreparesRequiredRemoteDependency(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
	defer remote.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess, Required: true, Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"}, PortEnvironment: "PORT", Health: model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond}},
			{Name: "payments", Kind: model.ServiceProcess, Required: true},
		},
		Connections: []model.Connection{{Source: "checkout", Target: "payments", Protocol: model.ProtocolHTTP, Environment: "PAYMENTS_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"}},
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "hybrid", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	defer app.processes.Stop(context.Background(), model.EnvironmentSelector("billing", "hybrid"), "checkout", time.Second)

	operation, err := app.StartService(ctx, "billing", "hybrid", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("start with remote dependency = %#v", operation)
	}
	environment, err := app.Environment(ctx, "billing", "hybrid")
	if err != nil {
		t.Fatal(err)
	}
	if runtimeFor(environment, "checkout").Status != model.ServiceReady || runtimeFor(environment, "payments").Status != model.ServiceReady {
		t.Fatalf("remote dependency was not prepared: %#v", environment.Services)
	}
	stopped, err := app.StopService(ctx, "billing", "hybrid", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if stopped = waitForOperation(t, app, stopped); stopped.State != "succeeded" {
		t.Fatalf("stop = %#v", stopped)
	}
}

func TestDaemonRestartRecoversSupervisedProcessIngressAndDependencyProxy(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing",
		Services: []model.ServiceDefinition{
			{
				Name: "checkout", Kind: model.ServiceProcess, Required: true,
				Command:     []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"},
				Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"}, PortEnvironment: "PORT",
				Health: model.HealthCheck{Kind: "http", Path: "/health", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
			},
			{Name: "payments", Kind: model.ServiceProcess, Required: true},
		},
		Connections: []model.Connection{{Source: "checkout", Target: "payments", Protocol: model.ProtocolHTTP, Environment: "PAYMENTS_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"}},
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}

	first := New(controlStore, events.NewBroker(), Config{
		DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-one", Executable: os.Args[0],
	})
	operation, err := first.StartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		first.Close(ctx)
		t.Fatal(err)
	}
	if operation = waitForOperation(t, first, operation); operation.State != "succeeded" {
		first.Close(ctx)
		t.Fatalf("start operation = %#v", operation)
	}
	before := serviceSnapshot(t, first, "checkout")
	connectionBefore, err := controlStore.ConnectionRuntime(ctx, "billing/local", "checkout", "payments")
	if err != nil {
		first.Close(ctx)
		t.Fatal(err)
	}
	assertIngressStatus(t, first, http.StatusOK)
	first.Close(ctx)

	second := New(controlStore, events.NewBroker(), Config{
		DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-two", Executable: os.Args[0],
	})
	defer second.Close(ctx)
	report, err := second.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unverifiable) != 0 || len(report.Recovered) != 1 || report.Recovered[0] != "billing/local" {
		t.Fatalf("reconciliation report = %#v", report)
	}
	after := serviceSnapshot(t, second, "checkout")
	if after.Status != model.ServiceReady || after.PID != before.PID || after.Generation != before.Generation {
		t.Fatalf("service was restarted instead of adopted: before=%#v after=%#v", before, after)
	}
	connectionAfter, err := controlStore.ConnectionRuntime(ctx, "billing/local", "checkout", "payments")
	if err != nil {
		t.Fatal(err)
	}
	if connectionAfter.ListenPort != connectionBefore.ListenPort || connectionAfter.OwnerInstanceID != "daemon-two" {
		t.Fatalf("dependency proxy was not restored at the saved port: before=%#v after=%#v", connectionBefore, connectionAfter)
	}
	assertIngressStatus(t, second, http.StatusOK)
	if ready, problems := second.CanHandoff(ctx); !ready || len(problems) != 0 {
		t.Fatalf("recovered runtime is not handoff-ready: ready=%v problems=%v", ready, problems)
	}
	timeline, err := second.Timeline(ctx, "billing", "local", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range timeline {
		if event.Type == "environment.reconciled" {
			t.Fatalf("successful daemon recovery leaked bookkeeping into the user timeline: %#v", event)
		}
	}
	if err := syscall.Kill(after.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for serviceSnapshot(t, second, "checkout").Status != model.ServiceFailed && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if terminal := serviceSnapshot(t, second, "checkout"); terminal.Status != model.ServiceFailed {
		t.Fatalf("unexpected service state after process exit: %#v", terminal)
	}
	if ready, problems := second.CanHandoff(ctx); !ready || len(problems) != 0 {
		t.Fatalf("known terminal runtime is not handoff-ready: ready=%v problems=%v", ready, problems)
	}
	second.Close(ctx)

	third := New(controlStore, events.NewBroker(), Config{
		DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-three", Executable: os.Args[0],
	})
	defer third.Close(ctx)
	report, err = third.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if terminal := serviceSnapshot(t, third, "checkout"); terminal.Status != model.ServiceFailed {
		t.Fatalf("terminal service became unverifiable after handoff: %#v report=%#v", terminal, report)
	}
	if ready, problems := third.CanHandoff(ctx); !ready || len(problems) != 0 {
		t.Fatalf("reconciled terminal runtime is not handoff-ready: ready=%v problems=%v", ready, problems)
	}
	restarted, err := third.StartService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if restarted = waitForOperation(t, third, restarted); restarted.State != "succeeded" {
		t.Fatalf("restart after recovered exit = %#v", restarted)
	}
	stopped, err := third.StopService(ctx, "billing", "local", "checkout", "test")
	if err != nil {
		t.Fatal(err)
	}
	if stopped = waitForOperation(t, third, stopped); stopped.State != "succeeded" {
		t.Fatalf("stop after recovered exit = %#v", stopped)
	}
}

func assertIngressStatus(t *testing.T, app *Service, expected int) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/health", nil)
	app.Proxy().ServeIngress(response, request, "billing/local", "checkout")
	if response.Code != expected {
		t.Fatalf("ingress status = %d, want %d; body=%s", response.Code, expected, response.Body.String())
	}
}

func waitForOperation(t *testing.T, app *Service, operation model.Operation) model.Operation {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last model.Operation
	for time.Now().Before(deadline) {
		current, err := app.Operation(context.Background(), operation.Project, operation.Environment, operation.Number)
		if err != nil {
			t.Fatal(err)
		}
		if current.State != "running" {
			return current
		}
		last = current
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("operation %d did not complete: %#v", operation.Number, last)
	return model.Operation{}
}

func serviceSnapshot(t *testing.T, app *Service, name string) model.Service {
	t.Helper()
	environment, err := app.Environment(context.Background(), "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range environment.Services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service %s was not found", name)
	return model.Service{}
}

func TestValidateExperimentScopeUsesConfiguredDirectedConnections(t *testing.T) {
	definition := model.ProjectModel{
		PrimaryService: "checkout",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceResource},
			{Name: "redis", Kind: model.ServiceResource},
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP},
			{Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, Binding: "postgres"},
			{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey"},
		},
	}

	for _, scope := range [][2]string{
		{"external", "checkout"},
		{"checkout", "orders"},
		{"orders", "postgres"},
		{"orders", "redis"},
	} {
		if err := validateExperimentScope(definition, scope[0], scope[1], false); err != nil {
			t.Fatalf("valid scope %s → %s was rejected: %v", scope[0], scope[1], err)
		}
	}

	err := validateExperimentScope(definition, "orders", "checkout", false)
	if err == nil {
		t.Fatal("reverse connection was accepted")
	}
	for _, expected := range []string{
		"orders → checkout is not a configured connection",
		"external → checkout",
		"checkout → orders",
		"orders → postgres",
		"orders → redis",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not contain %q", err, expected)
		}
	}

	err = validateExperimentScope(definition, "external", "orders", false)
	if err == nil || !strings.Contains(err.Error(), "external → orders is not a configured connection") {
		t.Fatalf("non-primary external connection error = %v", err)
	}
}

func TestApplyBindingUsesDNSHostForGenericTCP(t *testing.T) {
	service := &Service{resources: resourcebuiltin.Registry()}
	binding, err := service.connectionBinding(model.ServiceDefinition{Name: "broker", Kind: model.ServiceProcess}, model.Connection{Protocol: model.ProtocolTCP, Environment: "BROKER_ADDRESS"}, "broker.via-orders.local.store.portless.test", 4222, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Values["BROKER_ADDRESS"] != "broker.via-orders.local.store.portless.test:4222" {
		t.Fatalf("generic TCP binding = %q", binding.Values["BROKER_ADDRESS"])
	}
}

func TestResourceBindingInjectsMultipleVariablesAndRedactsSecrets(t *testing.T) {
	service := &Service{resources: resourcebuiltin.Registry()}
	target := model.ServiceDefinition{
		Name: "postgres", Kind: model.ServiceResource,
		Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432,
	}
	connection := model.Connection{
		Source: "inventory", Target: "postgres", Protocol: model.ProtocolTCP,
		Binding: "postgres", Environment: "SPRING_DATASOURCE_URL", Required: true,
	}
	binding, err := service.connectionBinding(target, connection, "postgres.via-inventory.local.store.portless.test", 5432, map[string]string{
		"POSTGRES_DB": "portless", "POSTGRES_USER": "portless", "POSTGRES_PASSWORD": "generated-secret",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.Values) != 3 || binding.Values["SPRING_DATASOURCE_USERNAME"] != "portless" || !strings.HasPrefix(binding.Values["SPRING_DATASOURCE_URL"], "jdbc:postgresql://") {
		t.Fatalf("resource binding values = %#v", binding.Values)
	}
	if binding.SafeValues["SPRING_DATASOURCE_PASSWORD"] != "••••••••" {
		t.Fatalf("resource binding exposed its secret: %#v", binding.SafeValues)
	}
	for _, value := range binding.SafeValues {
		if strings.Contains(value, "generated-secret") {
			t.Fatalf("resource binding exposed its secret: %#v", binding.SafeValues)
		}
	}
}

func TestApplicationProcessHelper(t *testing.T) {
	if os.Getenv("PORTLESS_APPLICATION_TEST_HELPER") != "1" {
		return
	}
	for index, argument := range os.Args {
		if argument != "--debug" || index+1 >= len(os.Args) {
			continue
		}
		debugListener, err := net.Listen("tcp", os.Args[index+1])
		if err != nil {
			os.Exit(4)
		}
		debugServer := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/json/list" {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"type":"node","title":"checkout"}]`))
		})}
		go func() { _ = debugServer.Serve(debugListener) }()
		break
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(2)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"service":"checkout"}`))
	})}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		os.Exit(3)
	}
}

func nestFixture(t *testing.T, root string) string {
	return nestNamedFixture(t, root, "checkout")
}

func nestNamedFixture(t *testing.T, root, name string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"name":"` + name + `","scripts":{"start:dev":"node server.js"},"dependencies":{"@nestjs/core":"1.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func sourcePath(sources []model.SourceBinding, name string) string {
	for _, source := range sources {
		if strings.EqualFold(source.Name, name) {
			return source.Path
		}
	}
	return ""
}
