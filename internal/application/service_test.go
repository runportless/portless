package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/store"
)

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
