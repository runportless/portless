package application

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
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

func TestPrepareResetRequiresStoppedIdleEnvironmentsAndBlocksNewStarts(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
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
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)

	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareReset(ctx); err == nil || !strings.Contains(err.Error(), "billing/local") {
		t.Fatalf("active environment did not block reset: %v", err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentStopped, ""); err != nil {
		t.Fatal(err)
	}
	operation, err := controlStore.CreateOperation(ctx, "billing/local", "up", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareReset(ctx); err == nil || !strings.Contains(err.Error(), "operations are still running") {
		t.Fatalf("running operation did not block reset: %v", err)
	}
	if err := controlStore.CompleteOperation(ctx, "billing/local", operation.Number, "failed", "test cleanup"); err != nil {
		t.Fatal(err)
	}
	result, err := app.PrepareReset(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processes != 0 || len(result.Runtimes) != 0 {
		t.Fatalf("unexpected reset runtime cleanup: %#v", result)
	}
	if _, err := app.Up(ctx, "billing", "local", "test", ""); err == nil || !strings.Contains(err.Error(), "reset preparation") {
		t.Fatalf("new start was accepted after reset preparation: %v", err)
	}
	app.CancelReset()
	if app.resetting {
		t.Fatal("reset cancellation did not reopen runtime starts")
	}
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
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal}}); err != nil {
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
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentStopped, "simulated stale status"); err != nil {
		t.Fatal(err)
	}
	result, err := app.PrepareReset(ctx)
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
	if len(resolved.Services) != 1 || resolved.Services[0].IngressURL != "http://checkout.local.billing.localhost" {
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
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
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
	if len(connections) != 1 || connections[0].TargetProvider != model.ProviderRemote || connections[0].TargetEndpoint != remoteURL || connections[0].InjectedEnvVar != "PAYMENTS_URL" {
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
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"}},
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "hybrid", definition, nil, bindings); err != nil {
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
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"}},
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, bindings); err != nil {
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

func TestApplicationProcessHelper(t *testing.T) {
	if os.Getenv("PORTLESS_APPLICATION_TEST_HELPER") != "1" {
		return
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
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"name":"checkout","scripts":{"start:dev":"node server.js"},"dependencies":{"@nestjs/core":"1.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
