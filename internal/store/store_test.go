package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
)

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
	privateKey, err := controlStore.PrivateEnvironmentKey(ctx, "billing", "local")
	if err != nil || privateKey == "" {
		t.Fatalf("private key = %q, err = %v", privateKey, err)
	}
	encoded, _ := json.Marshal(environment)
	if strings.Contains(string(encoded), privateKey) || strings.Contains(string(encoded), "privateKey") {
		t.Fatalf("private key leaked through environment JSON: %s", encoded)
	}

	scope := model.EnvironmentSelector("billing", "local")
	operation, err := controlStore.CreateOperation(ctx, scope, "up", "CLI", "same-request")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := controlStore.CreateOperation(ctx, scope, "up", "CLI", "same-request")
	if err != nil || repeated.Number != operation.Number {
		t.Fatalf("idempotent operation = %#v, err = %v", repeated, err)
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
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := controlStore.ServiceRuntime(ctx, "billing/local", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.OwnerInstanceID != "daemon-two" || runtime.SupervisorSocket != "/tmp/runner.sock" || runtime.SupervisorState != "/private/state.json" || runtime.Generation != 4 {
		t.Fatalf("runtime ownership was not persisted: %#v", runtime)
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
	if err := controlStore.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	qa, err := controlStore.CloneEnvironment(ctx, "billing", "local", "qa-local")
	if err != nil {
		t.Fatal(err)
	}
	qa, err = controlStore.SetEnvironmentBinding(ctx, "billing", "qa-local", model.ComponentBinding{Service: "checkout", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://checkout.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}})
	if err != nil {
		t.Fatal(err)
	}
	local, _ = controlStore.Environment(ctx, "billing", "local")
	if local.Bindings[0].Provider != model.ProviderLocal || qa.Bindings[0].Provider != model.ProviderRemote {
		t.Fatalf("bindings were not isolated: local=%#v qa=%#v", local.Bindings, qa.Bindings)
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
	cleared, err := controlStore.ClearContextSelection(ctx, sourcePath)
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

func testDefinition() model.ProjectModel {
	return model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true, Command: []string{"node", "server.js"}, Health: model.HealthCheck{Kind: "http", Path: "/health"}}}}
}
