package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/providers"
	resourcebuiltin "github.com/runportless/portless/portless-daemon/providers/builtin"
	"github.com/runportless/portless/portless-daemon/runtime/container"
	"github.com/runportless/portless/portless-daemon/runtime/supervisor"
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

func TestReconcileMarksProvablyGoneReadyProcessStoppedAndUpRestartsIt(t *testing.T) {
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
	runs := filepath.Join(data, "runs")
	if err := os.MkdirAll(runs, 0o700); err != nil {
		t.Fatal(err)
	}
	const deadPID = 1 << 30
	statePath := filepath.Join(runs, "stale.state.json")
	status := supervisor.Status{
		ProtocolVersion: supervisor.ProtocolVersion, Scope: "billing/local", Service: "checkout", Generation: 4,
		SupervisorPID: deadPID + 1, PID: deadPID, Port: 43123, LaunchMode: model.LaunchManaged, State: "ready",
		StartedAt: time.Now().UTC().Add(-time.Hour), LogDirectory: filepath.Join(data, "logs", "checkout", "4"),
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetServiceRuntime(ctx, "billing/local", "checkout", database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: 4, PID: deadPID, UpstreamPort: status.Port,
		StartedAt: &status.StartedAt, LogPath: status.LogDirectory, PrivateRunKey: "private-run-key",
		OwnerInstanceID: "daemon-before-reboot", SupervisorSocket: filepath.Join(t.TempDir(), "missing.sock"),
		SupervisorState: statePath, SupervisorPID: deadPID + 1, LaunchMode: model.LaunchManaged,
	}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}

	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-after-reboot", Executable: os.Args[0]})
	defer app.Close(ctx)
	report, err := app.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unverifiable) != 0 {
		t.Fatalf("provably gone process remained unverifiable: %#v", report)
	}
	recovered, err := app.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	stopped := runtimeFor(recovered, "checkout")
	if recovered.Status != model.EnvironmentStopped || stopped.Status != model.ServiceStopped || stopped.PID != 0 || stopped.Generation != 4 || !strings.Contains(stopped.Reason, "no longer running") {
		t.Fatalf("stale process was not converted to stopped: %#v", recovered)
	}
	if ready, problems := app.CanHandoff(ctx); !ready || len(problems) != 0 {
		t.Fatalf("known-absent runtime blocked handoff: ready=%v problems=%v", ready, problems)
	}

	operation, err := app.Up(ctx, "billing", "local", "test", "reboot-recovery", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	operation = waitForOperation(t, app, operation)
	if operation.State != "succeeded" {
		t.Fatalf("up after reboot recovery = %#v", operation)
	}
	service := serviceSnapshot(t, app, "checkout")
	if service.Status != model.ServiceReady || service.Generation != 5 || service.PID == 0 || service.PID == deadPID {
		t.Fatalf("up did not launch a new generation: %#v", service)
	}
	defer app.processes.Stop(context.Background(), "billing/local", "checkout", time.Second)
}

func TestReconcileMarksOwnedStoppedContainerStoppedAndUpRecreatesIt(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "store",
		Services: []model.ServiceDefinition{
			{
				Name: "checkout", Kind: model.ServiceProcess, Required: true,
				Command: []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"}, Environment: map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
				PortEnvironment: "PORT", Health: model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
			},
			{Name: "postgres", Kind: model.ServiceResource, Required: true, Port: 5432, Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}},
		},
		Connections: []model.Connection{{Source: "checkout", Target: "postgres", Protocol: model.ProtocolTCP, Environment: "DATABASE_URL", Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sources := []model.SourceBinding{{Name: "checkout", Path: t.TempDir(), Status: "ready", Definition: definition}}
	bindings := []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}, {Service: "postgres", Provider: model.ProviderContainer}}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetServiceRuntime(ctx, "store/local", "postgres", database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: 2, UpstreamPort: 45432,
		OwnerInstanceID: "daemon-before-reboot", ContainerName: "portless-store-local-postgres",
	}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "store", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	runtime := &recoveryContainerRuntime{name: container.RuntimeDocker, inspection: container.RecoveryInspection{
		State: container.RecoveryStopped, ContainerName: "portless-store-local-postgres",
	}}
	containers := container.NewManager(filepath.Join(data, "runtime.json"), resourcebuiltin.Registry(), runtime)
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", DaemonInstanceID: "daemon-after-reboot", PrivateTCPIngress: true})
	app.containers.Close()
	app.containers = containers
	defer app.Close(ctx)

	report, err := app.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Unverifiable) != 0 {
		t.Fatalf("owned stopped container remained unverifiable: %#v", report)
	}
	recovered, err := app.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	stopped := runtimeFor(recovered, "postgres")
	if recovered.Status != model.EnvironmentStopped || stopped.Status != model.ServiceStopped || stopped.Generation != 2 {
		t.Fatalf("stopped container was not converted to stopped runtime: %#v", recovered)
	}

	operation, err := app.Up(ctx, "store", "local", "test", "container-recovery", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	operation = waitForOperation(t, app, operation)
	if operation.State != "succeeded" {
		t.Fatalf("up after stopped container recovery = %#v", operation)
	}
	healthy, err := app.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	service := runtimeFor(healthy, "postgres")
	if service.Status != model.ServiceReady || service.Generation != 3 || service.UpstreamPort == 0 || runtime.startCalls != 1 {
		t.Fatalf("container was not recreated at the next generation: service=%#v starts=%d", service, runtime.startCalls)
	}
	defer app.processes.Stop(context.Background(), "store/local", "checkout", time.Second)
}

func TestDownRefusesToForgetUnverifiablePersistedProcess(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", Services: []model.ServiceDefinition{{
		Name: "checkout", Kind: model.ServiceProcess, Required: true, Command: []string{"unused"},
	}}}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal}}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetServiceRuntime(ctx, "billing/local", "checkout", database.ServiceRuntimeUpdate{
		Status: model.ServiceUnknown, Reason: "previous ownership is incomplete", Generation: 3,
		PID: 43002, SupervisorPID: 43001, OwnerInstanceID: "daemon-before-reboot",
	}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentUnknown, "previous ownership is incomplete"); err != nil {
		t.Fatal(err)
	}

	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	operation, err := app.Down(ctx, "billing", "local", "test", "unverifiable-down", false)
	if err != nil {
		t.Fatal(err)
	}
	operation = waitForOperation(t, app, operation)
	if operation.State != "failed" || !strings.Contains(operation.Error, "persisted supervisor ownership record is incomplete") {
		t.Fatalf("down did not fail closed for unverifiable ownership: %#v", operation)
	}
	persisted, err := controlStore.ServiceRuntime(ctx, "billing/local", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != model.ServiceUnknown || persisted.Generation != 3 || persisted.PID != 43002 || persisted.SupervisorPID != 43001 {
		t.Fatalf("down forgot unverifiable persisted ownership: %#v", persisted)
	}
}

type recoveryContainerRuntime struct {
	name       container.RuntimeName
	inspection container.RecoveryInspection
	startCalls int
}

func (r *recoveryContainerRuntime) Name() container.RuntimeName { return r.name }
func (r *recoveryContainerRuntime) Probe(context.Context) container.ProbeResult {
	return container.ProbeResult{Name: r.name, State: "ready"}
}
func (r *recoveryContainerRuntime) StartHost(ctx context.Context) container.ProbeResult {
	return r.Probe(ctx)
}
func (r *recoveryContainerRuntime) Start(_ context.Context, _, _ string, service model.ServiceDefinition, _ providers.ContainerPlan, generation int64, logsRoot string) (container.StartResult, error) {
	r.startCalls++
	return container.StartResult{
		ContainerName: "recreated-" + service.Name, Port: 45433, StartedAt: time.Now().UTC(), LogDirectory: filepath.Join(logsRoot, service.Name, "3"),
		Environment: map[string]string{"POSTGRES_USER": "portless", "POSTGRES_DB": "portless", "POSTGRES_PASSWORD": "private"},
	}, nil
}
func (r *recoveryContainerRuntime) Adopt(context.Context, string, string, model.ServiceDefinition, providers.ContainerPlan, int64, string) (container.StartResult, error) {
	return container.StartResult{ContainerName: r.inspection.ContainerName, Port: r.inspection.Port}, nil
}
func (r *recoveryContainerRuntime) InspectRecovery(context.Context, string, model.ServiceDefinition, providers.ContainerPlan, int64, string) (container.RecoveryInspection, error) {
	return r.inspection, nil
}
func (r *recoveryContainerRuntime) StopEnvironment(context.Context, string, bool) error { return nil }
func (r *recoveryContainerRuntime) StopService(context.Context, string, string) error   { return nil }
func (r *recoveryContainerRuntime) ResetInstallation(context.Context) (container.ResetResult, error) {
	return container.ResetResult{Runtime: r.name}, nil
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
