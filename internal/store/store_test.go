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
	if _, err := controlStore.AddTimelineEvent(ctx, model.TimelineEvent{Project: "billing", Environment: "local", Actor: "CLI", Type: "test", Severity: "info", Summary: "environment event"}); err != nil {
		t.Fatal(err)
	}
	timeline, err := controlStore.Timeline(ctx, scope, 10)
	if err != nil || len(timeline) != 1 || timeline[0].Project != "billing" || timeline[0].Environment != "local" {
		t.Fatalf("timeline = %#v, err = %v", timeline, err)
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
