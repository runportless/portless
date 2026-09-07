package database

import (
	"errors"
	"github.com/runportless/portless/portless-daemon/model"
	"path/filepath"
	"testing"
	"time"
)

func TestArtifactPreconditionsPreserveChangedAndRecreatedResources(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	definition := model.ProjectModel{SuggestedName: "shop", Services: []model.ServiceDefinition{{Name: "web", Kind: model.ServiceProcess}}}
	if _, err := store.CreateProject(t.Context(), "shop", definition, nil); err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateEnvironment(t.Context(), "shop", "local", definition, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute)
	fault, err := store.CreateFault(t.Context(), model.FaultRule{Project: "shop", Environment: "local", Name: "delay", Source: "external", Target: "web", LatencyMS: 5, ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	version := model.ResourceVersion{CreatedAt: fault.CreatedAt, Revision: fault.Revision, ParentCreatedAt: env.CreatedAt}
	if err := store.DisableFault(t.Context(), "shop/local", "delay"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFault(t.Context(), "shop/local", "delay", &version); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale fault delete=%v", err)
	}
	if err := store.EnableFault(t.Context(), "shop/local", "delay", &version); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale fault enable=%v", err)
	}
	fault, err = store.Fault(t.Context(), "shop/local", "delay")
	if err != nil {
		t.Fatal(err)
	}
	version.Revision = fault.Revision
	if err := store.EnableFault(t.Context(), "shop/local", "delay", &version); err != nil {
		t.Fatal(err)
	}
	current, err := store.Fault(t.Context(), "shop/local", "delay")
	if err != nil || !current.Enabled || !current.ExpiresAt.Equal(expires) {
		t.Fatal("enable changed expiry or failed to activate")
	}
	version.Revision = current.Revision
	if err := store.DeleteFault(t.Context(), "shop/local", "delay", &version); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFault(t.Context(), model.FaultRule{Project: "shop", Environment: "local", Name: "delay", Source: "external", Target: "web", LatencyMS: 5, ExpiresAt: &expires}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFault(t.Context(), "shop/local", "delay", &version); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused fault name=%v", err)
	}
	recording, err := store.CreateRecording(t.Context(), model.Recording{Project: "shop", Environment: "local", Name: "capture"})
	if err != nil {
		t.Fatal(err)
	}
	version = model.ResourceVersion{CreatedAt: recording.StartedAt, ParentCreatedAt: env.CreatedAt}
	if err := store.DeleteRecording(t.Context(), "shop/local", "capture", &version); err == nil {
		t.Fatal("deleted active recording")
	}
	if err := store.StopRecording(t.Context(), "shop/local", "capture", "stopped"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRecording(t.Context(), "shop/local", "capture", &version); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRecording(t.Context(), model.Recording{Project: "shop", Environment: "local", Name: "capture"}); err != nil {
		t.Fatal(err)
	}
	if err := store.StopRecording(t.Context(), "shop/local", "capture", "stopped"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRecording(t.Context(), "shop/local", "capture", &version); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused recording name=%v", err)
	}
}
