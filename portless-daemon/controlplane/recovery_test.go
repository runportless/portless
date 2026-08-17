package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/events"
	"github.com/portless-run/portless/portless-daemon/model"
)

func TestFaultsRemainActiveUntilDisabledUnlessExpiryIsRequested(t *testing.T) {
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
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
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
	if err := controlStore.PersistTraffic(ctx, model.TrafficExchange{Project: "billing", Environment: "local", Recording: "retained", Sequence: 41}); err != nil {
		t.Fatal(err)
	}
	broker := events.NewBroker()
	app := New(controlStore, broker, Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	exchange := app.AddTrafficExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP})
	if exchange.Sequence != 42 {
		t.Fatalf("restored sequence = %d, want 42", exchange.Sequence)
	}
}

func TestIndividualServiceStartHonorsCrossEnvironmentSourceLeases(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
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

	local, err := app.StartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if local = waitForOperation(t, app, local); local.State != "succeeded" {
		t.Fatalf("local start = %#v", local)
	}
	experiment, err := app.StartService(ctx, "billing", "experiment", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if experiment = waitForOperation(t, app, experiment); experiment.State != "failed" || !strings.Contains(experiment.Error, "already running") {
		t.Fatalf("shared-checkout start = %#v", experiment)
	}
	stopped, err := app.StopService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if stopped = waitForOperation(t, app, stopped); stopped.State != "succeeded" {
		t.Fatalf("local stop = %#v", stopped)
	}
	experiment, err = app.StartService(ctx, "billing", "experiment", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if experiment = waitForOperation(t, app, experiment); experiment.State != "succeeded" {
		t.Fatalf("experiment start after lease release = %#v", experiment)
	}
	stopped, err = app.StopService(ctx, "billing", "experiment", "checkout", "test", "")
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
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
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

	operation, err := app.StartService(ctx, "billing", "hybrid", "checkout", "test", "")
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
	stopped, err := app.StopService(ctx, "billing", "hybrid", "checkout", "test", "")
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
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
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
	operation, err := first.StartService(ctx, "billing", "local", "checkout", "test", "")
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
	restarted, err := third.StartService(ctx, "billing", "local", "checkout", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if restarted = waitForOperation(t, third, restarted); restarted.State != "succeeded" {
		t.Fatalf("restart after recovered exit = %#v", restarted)
	}
	stopped, err := third.StopService(ctx, "billing", "local", "checkout", "test", "")
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
	app.ServeIngress(response, request, "billing/local", "checkout")
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
