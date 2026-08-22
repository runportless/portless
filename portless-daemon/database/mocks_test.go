package database

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestMockProfilesPersistRoutesAndCloneWithEnvironment(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "store", Services: []model.ServiceDefinition{{Name: "inventory", Kind: model.ServiceProcess}}}
	if _, err := controlStore.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "store", Services: []string{"inventory"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, []model.ComponentBinding{{Service: "inventory", Provider: model.ProviderLocal, Source: "store"}}); err != nil {
		t.Fatal(err)
	}
	created, err := controlStore.CreateMockProfile(ctx, "store", "local", model.MockProfile{Name: "sold-out", Service: "inventory", Description: "No stock"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Project != "store" || created.Environment != "local" || len(created.Routes) != 0 || created.CreatedAt.IsZero() {
		t.Fatalf("created profile = %#v", created)
	}
	updated, err := controlStore.PutMockRoute(ctx, "store", "local", "SOLD-OUT", model.MockRoute{Name: "lookup", Method: "get", Path: "/inventory/{sku}", Query: map[string]string{"warehouse": "central"}, Status: 409, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"available":false}`, DelayMS: 25, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Routes) != 1 || updated.Routes[0].Method != "GET" || updated.Routes[0].Query["warehouse"] != "central" || updated.Routes[0].ModifiedAt.IsZero() {
		t.Fatalf("updated profile = %#v", updated)
	}
	if _, err := controlStore.CloneEnvironment(ctx, "store", "local", "qa"); err != nil {
		t.Fatal(err)
	}
	cloned, err := controlStore.MockProfile(ctx, "store", "qa", "sold-out")
	if err != nil || len(cloned.Routes) != 1 || cloned.Routes[0].Body != `{"available":false}` {
		t.Fatalf("cloned profile = %#v, err = %v", cloned, err)
	}
	if _, err := controlStore.DeleteMockRoute(ctx, "store", "qa", "sold-out", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.DeleteMockProfile(ctx, "store", "qa", "sold-out"); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.MockProfile(ctx, "store", "qa", "sold-out"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted profile error = %v", err)
	}
}
