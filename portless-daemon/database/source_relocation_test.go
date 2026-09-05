package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestSourceRelocationPreservesRuntimeAndRejectsActiveOrStaleWrites(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := testDefinition()
	if _, err := store.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	source := model.SourceBinding{Name: "checkout", Path: t.TempDir(), Definition: definition}
	environment, err := store.CreateEnvironment(ctx, "billing", "local", definition, []model.SourceBinding{source}, []model.ComponentBinding{{Service: "checkout", Source: "checkout", Provider: model.ProviderLocal}})
	if err != nil {
		t.Fatal(err)
	}
	source = environment.Sources[0]
	if err := store.SetServiceRuntime(ctx, "billing/local", "checkout", ServiceRuntimeUpdate{Status: model.ServiceUnknown, Generation: 7, RestartCount: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentFailed, "previous startup failed"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetContextSelection(ctx, source.Path, "billing", "local"); err != nil {
		t.Fatal(err)
	}
	relocated := source
	relocated.Path, err = canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RelocateEnvironmentSources(ctx, "billing", "local", environment.Revision, definition, []model.SourceBinding{relocated}); err == nil {
		t.Fatal("relocated an unverified active source")
	}
	unchanged, _ := store.Environment(ctx, "billing", "local")
	if unchanged.Revision != environment.Revision || unchanged.Sources[0].Path != source.Path {
		t.Fatal("rejected relocation changed the source")
	}
	if err := store.SetServiceRuntime(ctx, "billing/local", "checkout", ServiceRuntimeUpdate{Status: model.ServiceExited, Generation: 7, RestartCount: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RelocateEnvironmentSources(ctx, "billing", "local", environment.Revision+1, definition, []model.SourceBinding{relocated}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale relocation error = %v", err)
	}
	updated, err := store.RelocateEnvironmentSources(ctx, "billing", "local", environment.Revision, definition, []model.SourceBinding{relocated})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != environment.Revision+1 || updated.Sources[0].Path != relocated.Path || updated.Status != model.EnvironmentFailed {
		t.Fatalf("relocated environment = %#v", updated)
	}
	if service := updated.Services[0]; service.Generation != 7 || service.RestartCount != 3 || service.Status != model.ServiceExited {
		t.Fatalf("relocation erased runtime history: %#v", service)
	}
	if selected, err := store.ContextSelection(ctx, source.Path); err != nil || selected.Name != "local" {
		t.Fatalf("relocation lost explicit selection: %#v, %v", selected, err)
	}
	if _, err := store.CreateEnvironment(ctx, "billing", "baseline", definition, []model.SourceBinding{source}, updated.Bindings); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnvironmentStatus(ctx, "billing", "local", model.EnvironmentStopped, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceEnvironmentConfiguration(ctx, "billing", "local", updated.Revision, definition, updated.Sources, updated.Bindings); err != nil {
		t.Fatal(err)
	}
	if selected, err := store.ContextSelection(ctx, source.Path); err != nil || selected.Name != "local" {
		t.Fatalf("configuration update lost the original project checkout's selection: %#v, %v", selected, err)
	}
}
