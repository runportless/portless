package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestOpenBackfillsLegacyFaultEnableTime(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "portless.db")
	legacy, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	createdAt := "2026-08-29T12:00:00Z"
	if _, err := legacy.Exec(`CREATE TABLE fault_rules (created_at TEXT NOT NULL)`); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO fault_rules(created_at) VALUES(?)`, createdAt); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	controlStore, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	var enabledAt string
	if err := controlStore.DB().QueryRow(`SELECT enabled_at FROM fault_rules`).Scan(&enabledAt); err != nil {
		t.Fatal(err)
	}
	if enabledAt != createdAt {
		t.Fatalf("migrated enable time = %q, want %q", enabledAt, createdAt)
	}
}

func TestProjectAndEnvironmentStateAreSeparated(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := testDefinition()
	project, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}})
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "checkout")
	environment, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "checkout", Path: sourcePath, Status: "ready", ScannedAt: time.Now(), Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "billing" || len(project.Environments) != 0 {
		t.Fatalf("unexpected project before environment refresh: %#v", project)
	}
	if environment.Project != "billing" || environment.Name != "local" || environment.Status != model.EnvironmentStopped {
		t.Fatalf("unexpected environment: %#v", environment)
	}
	if len(environment.Sources) != 1 || environment.Sources[0].CreatedAt.IsZero() {
		t.Fatalf("source creation time was not persisted: %#v", environment.Sources)
	}
	privateKey, err := controlStore.PrivateEnvironmentKey(ctx, "billing", "local")
	if err != nil || privateKey == "" {
		t.Fatalf("private key = %q, err = %v", privateKey, err)
	}
	encoded, _ := json.Marshal(environment)
	if strings.Contains(string(encoded), privateKey) || strings.Contains(string(encoded), "privateKey") {
		t.Fatalf("private key leaked through environment JSON: %s", encoded)
	}

	scope := model.EnvironmentSelector("billing", "local")
	operation, err := controlStore.CreateOperation(ctx, scope, "up", "CLI", "same-request", `{"type":"up"}`)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := controlStore.CreateOperation(ctx, scope, "up", "CLI", "same-request", `{"type":"up"}`)
	if err != nil || repeated.Number != operation.Number {
		t.Fatalf("idempotent operation = %#v, err = %v", repeated, err)
	}
	if _, err := controlStore.CreateOperation(ctx, scope, "down", "CLI", "same-request", `{"type":"down"}`); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("mismatched idempotent request error = %v, want ErrIdempotencyConflict", err)
	}
	running, err := controlStore.RunningOperationScopes(ctx)
	if err != nil || len(running) != 1 || running[0] != scope {
		t.Fatalf("running operation scopes = %#v, err = %v", running, err)
	}
	if err := controlStore.CompleteOperation(ctx, scope, operation.Number, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
	running, err = controlStore.RunningOperationScopes(ctx)
	if err != nil || len(running) != 0 {
		t.Fatalf("completed operation remained in reset inventory: %#v, err = %v", running, err)
	}
	if _, err := controlStore.AddTimelineEvent(ctx, model.TimelineEvent{Project: "billing", Environment: "local", Actor: "CLI", Type: "test", Severity: "info", Summary: "environment event"}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.AddTimelineEvent(ctx, model.TimelineEvent{Project: "billing", Environment: "local", Actor: "daemon", Type: "environment.reconciled", Severity: "info", Summary: "Recovered runtime ownership and proxy routes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.AddTimelineEvent(ctx, model.TimelineEvent{Project: "billing", Environment: "local", Actor: "daemon", Type: "environment.reconciled", Severity: "warning", Summary: "Runtime recovery completed with unavailable services"}); err != nil {
		t.Fatal(err)
	}
	timeline, err := controlStore.Timeline(ctx, scope, 10)
	if err != nil || len(timeline) != 2 || timeline[0].Project != "billing" || timeline[0].Environment != "local" {
		t.Fatalf("timeline = %#v, err = %v", timeline, err)
	}
	for _, event := range timeline {
		if event.Type == "environment.reconciled" && event.Severity == "info" {
			t.Fatalf("successful daemon recovery leaked into the user timeline: %#v", event)
		}
	}
}

func TestRecoverableRuntimeOwnershipAndProxyPortsPersist(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := testDefinition()
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Round(0)
	observed := started.Add(time.Second)
	if err := controlStore.SetServiceRuntime(ctx, "billing/local", "checkout", ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: 4, PID: 1234, UpstreamPort: 43210,
		StartedAt: &started, RestartCount: 2, LogPath: "/private/logs", PrivateRunKey: "run-key",
		OwnerInstanceID: "daemon-two", SupervisorSocket: "/tmp/runner.sock",
		SupervisorState: "/private/state.json", SupervisorPID: 1200, ObservedAt: &observed,
		LaunchMode: model.LaunchDebug, Debugger: &model.DebuggerRuntime{Adapter: model.DebugNodeInspector, Host: "127.0.0.1", Port: 43123, State: "listening"},
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := controlStore.ServiceRuntime(ctx, "billing/local", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.OwnerInstanceID != "daemon-two" || runtime.SupervisorSocket != "/tmp/runner.sock" || runtime.SupervisorState != "/private/state.json" || runtime.Generation != 4 || runtime.LaunchMode != model.LaunchDebug || runtime.Debugger == nil || runtime.Debugger.Port != 43123 {
		t.Fatalf("runtime ownership was not persisted: %#v", runtime)
	}
	if err := controlStore.SetServiceDebuggerState(ctx, "billing/local", "checkout", "stopped"); err != nil {
		t.Fatal(err)
	}
	runtime, err = controlStore.ServiceRuntime(ctx, "billing/local", "checkout")
	if err != nil || runtime.Debugger == nil || runtime.Debugger.State != "stopped" {
		t.Fatalf("debugger state = %#v, err=%v", runtime.Debugger, err)
	}
	if err := controlStore.SaveConnectionRuntime(ctx, "billing/local", ConnectionRuntime{
		Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, SourceGeneration: 4,
		ListenPort: 45678, OwnerInstanceID: "daemon-two", State: "ready", ObservedAt: &observed,
	}); err != nil {
		t.Fatal(err)
	}
	connection, err := controlStore.ConnectionRuntime(ctx, "billing/local", "checkout", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if connection.ListenPort != 45678 || connection.SourceGeneration != 4 || connection.OwnerInstanceID != "daemon-two" {
		t.Fatalf("connection runtime was not persisted: %#v", connection)
	}
}

func TestActiveBindingConfigurationPreservesRuntimeAndSourceState(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{
		SuggestedName: "billing", PrimaryService: "checkout",
		Services:    []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}, {Name: "orders", Kind: model.ServiceProcess, Required: true}},
		Connections: []model.Connection{{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "app", Services: []string{"checkout", "orders"}}}); err != nil {
		t.Fatal(err)
	}
	sourcePath := t.TempDir()
	environment, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "app", Path: sourcePath, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "app"}, {Service: "orders", Provider: model.ProviderLocal, Source: "app"}})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Round(0)
	for index, service := range []string{"checkout", "orders"} {
		if err := controlStore.SetServiceRuntime(ctx, "billing/local", service, ServiceRuntimeUpdate{
			Status: model.ServiceReady, Generation: int64(index + 4), PID: 1200 + index,
			UpstreamPort: 43000 + index, StartedAt: &started, OwnerInstanceID: "daemon-one",
		}); err != nil {
			t.Fatal(err)
		}
	}
	observed := started.Add(time.Second)
	if err := controlStore.SaveConnectionRuntime(ctx, "billing/local", ConnectionRuntime{
		Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, SourceGeneration: 4,
		ListenIP: "127.0.0.1", ListenPort: 45678, OwnerInstanceID: "daemon-one", State: "ready", ObservedAt: &observed,
	}); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	remote := model.ComponentBinding{Service: "orders", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://orders.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}, ModifiedAt: time.Now().UTC()}
	updated, err := controlStore.ApplyActiveBindingConfiguration(ctx, "billing", "local", environment.Revision, definition, remote)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSourcePath, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != environment.Revision+1 || len(updated.Sources) != 1 || updated.Sources[0].Path != canonicalSourcePath {
		t.Fatalf("active configuration metadata = %#v", updated)
	}
	checkout, err := controlStore.ServiceRuntime(ctx, "billing/local", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	orders, err := controlStore.ServiceRuntime(ctx, "billing/local", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if checkout.PID != 1200 || checkout.Generation != 4 || checkout.OwnerInstanceID != "daemon-one" || orders.PID != 1201 || orders.Generation != 5 || orders.OwnerInstanceID != "daemon-one" {
		t.Fatalf("service runtimes changed: checkout=%#v orders=%#v", checkout, orders)
	}
	connection, err := controlStore.ConnectionRuntime(ctx, "billing/local", "checkout", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if connection.ListenPort != 45678 || connection.SourceGeneration != 4 || connection.OwnerInstanceID != "daemon-one" {
		t.Fatalf("connection runtime changed: %#v", connection)
	}
	var binding model.ComponentBinding
	for _, candidate := range updated.Bindings {
		if candidate.Service == "orders" {
			binding = candidate
			break
		}
	}
	if binding.Provider != model.ProviderRemote || binding.Remote == nil || binding.Remote.URL != remote.Remote.URL {
		t.Fatalf("active binding was not saved: %#v", updated.Bindings)
	}
}

func TestClonedEnvironmentCanUseRemoteProviderIndependently(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := testDefinition()
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "checkout")
	local, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "checkout", Path: sourcePath, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	localModifiedAt := local.Bindings[0].ModifiedAt
	if localModifiedAt.IsZero() {
		t.Fatal("initial provider binding has no modification time")
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	qa, err := controlStore.CloneEnvironment(ctx, "billing", "local", "qa-local")
	if err != nil {
		t.Fatal(err)
	}
	if qa.Bindings[0].ModifiedAt.IsZero() {
		t.Fatal("cloned provider binding has no modification time")
	}
	qa, err = controlStore.SetEnvironmentBinding(ctx, "billing", "qa-local", model.ComponentBinding{Service: "checkout", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://checkout.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}})
	if err != nil {
		t.Fatal(err)
	}
	local, _ = controlStore.Environment(ctx, "billing", "local")
	if local.Bindings[0].Provider != model.ProviderLocal || qa.Bindings[0].Provider != model.ProviderRemote {
		t.Fatalf("bindings were not isolated: local=%#v qa=%#v", local.Bindings, qa.Bindings)
	}
	if qa.Bindings[0].ModifiedAt.IsZero() {
		t.Fatal("changed provider binding has no modification time")
	}
	if !local.Bindings[0].ModifiedAt.Equal(localModifiedAt) {
		t.Fatalf("unchanged provider modification time changed from %s to %s", localModifiedAt, local.Bindings[0].ModifiedAt)
	}
}

func TestOpenBackfillsProviderBindingModificationTimes(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "portless.db")
	controlStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	definition := testDefinition()
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "checkout")
	environment, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "checkout", Path: sourcePath, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE environment_bindings SET modified_at = ''`); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.Close(); err != nil {
		t.Fatal(err)
	}

	controlStore, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	reloaded, err := controlStore.Environment(ctx, "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Bindings) != 1 || reloaded.Bindings[0].ModifiedAt.IsZero() {
		t.Fatalf("backfilled bindings = %#v", reloaded.Bindings)
	}
	if !reloaded.Bindings[0].ModifiedAt.Equal(environment.UpdatedAt) {
		t.Fatalf("backfilled modification time = %s, want environment update time %s", reloaded.Bindings[0].ModifiedAt, environment.UpdatedAt)
	}
}

func TestContextSelectionCanBeClearedIdempotently(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := testDefinition()
	if _, err := controlStore.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(t.TempDir(), "checkout")
	environment, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition,
		[]model.SourceBinding{{Name: "checkout", Path: sourcePath, Status: "ready", ScannedAt: time.Now(), Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := controlStore.SetContextSelection(ctx, sourcePath, "billing", "local"); err != nil {
		t.Fatal(err)
	}
	if selected, err := controlStore.ContextSelection(ctx, sourcePath); err != nil || selected.Name != "local" {
		t.Fatalf("selection = %#v, err = %v", selected, err)
	}
	nestedPath := filepath.Join(sourcePath, "apps", "checkout")
	if err := controlStore.SetContextSelection(ctx, nestedPath, "billing", "local"); err != nil {
		t.Fatalf("select environment from nested service path: %v", err)
	}
	if selected, err := controlStore.ContextSelection(ctx, nestedPath); err != nil || selected.Name != "local" {
		t.Fatalf("nested selection = %#v, err = %v", selected, err)
	}
	matching, err := controlStore.EnvironmentsByPath(ctx, nestedPath)
	if err != nil || len(matching) != 1 || matching[0].Project != "billing" {
		t.Fatalf("nested path environments = %#v, err = %v", matching, err)
	}
	cleared, err := controlStore.ClearContextSelection(ctx, nestedPath)
	if err != nil || !cleared {
		t.Fatalf("first clear = %v, %v; want true, nil", cleared, err)
	}
	if _, err := controlStore.ContextSelection(ctx, sourcePath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("selection still resolves after clear: %v", err)
	}
	cleared, err = controlStore.ClearContextSelection(ctx, sourcePath)
	if err != nil || cleared {
		t.Fatalf("second clear = %v, %v; want false, nil", cleared, err)
	}

	if err := controlStore.SetContextSelection(ctx, sourcePath, "billing", "local"); err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(t.TempDir(), "checkout-worktree")
	if _, err := controlStore.ReplaceEnvironmentConfiguration(ctx, "billing", "local", environment.Revision, definition,
		[]model.SourceBinding{{Name: "checkout", Path: replacementPath, Status: "ready", ScannedAt: time.Now(), Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.ContextSelection(ctx, sourcePath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("selection still resolves after the environment moved to another checkout: %v", err)
	}
}

func TestOnlyOneRecordingIsActivePerEnvironment(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := testDefinition()
	if _, err := controlStore.CreateProject(ctx, "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "billing", "qa-local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "qa-local", Name: "first"}); err != nil {
		t.Fatalf("another environment should have an independent active recording: %v", err)
	}
	_, err = controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "second"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestLegacyProjectModelFailsWithExplicitFormatError(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	if _, err := controlStore.CreateProject(ctx, "billing", testDefinition(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE projects SET model_json = ? WHERE name = 'billing'`, []byte(`{"suggestedName":"billing"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.ProjectModel(ctx, "billing"); !errors.Is(err, ErrIncompatibleState) || !strings.Contains(err.Error(), "reset application state") {
		t.Fatalf("legacy format error = %v", err)
	}
}

func testDefinition() model.ProjectModel {
	return model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true, Command: []string{"node", "server.js"}, Health: model.HealthCheck{Kind: "http", Path: "/health"}}}}
}
