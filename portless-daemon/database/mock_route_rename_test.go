package database

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestRenameMockRouteCommitsNameAndConfigurationTogether(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()
	definition := model.ProjectModel{SuggestedName: "store", Services: []model.ServiceDefinition{{Name: "inventory", Kind: model.ServiceProcess}}}
	if _, err := store.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "store", Services: []string{"inventory"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEnvironment(ctx, "store", "local", definition, nil, []model.ComponentBinding{{Service: "inventory", Provider: model.ProviderLocal, Source: "store"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMockScenario(ctx, "store", "local", model.MockScenario{Name: "rename"}); err != nil {
		t.Fatal(err)
	}
	before, err := store.PutMockRoutes(ctx, "store", "local", "rename", []model.MockRoute{
		{Name: "lookup", Service: "inventory", Method: "GET", Path: "/items", Status: 200, Enabled: true, Body: "original", Query: map[string]model.MockQueryMatcher{"sku": {Match: "regex", Value: "coffee-.*"}}},
		{Name: "occupied", Service: "inventory", Method: "GET", Path: "/health", Status: 204, Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	route := before.Routes[0]
	route.Name = "occupied"
	if _, err := store.RenameMockRoute(ctx, "store", "local", "rename", "lookup", route); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("collision error = %v", err)
	}
	if _, err := store.RenameMockRoute(ctx, "store", "local", "rename", "missing", route); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing original error = %v", err)
	}
	route.Name, route.Body, route.Status = "renamed", "updated", 202
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_renamed_route BEFORE UPDATE OF body ON mock_scenario_routes WHEN NEW.name = 'renamed' BEGIN SELECT RAISE(FAIL, 'injected save failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenameMockRoute(ctx, "store", "local", "rename", "lookup", route); err == nil {
		t.Fatal("injected failure did not abort the save")
	}
	after, err := store.MockScenario(ctx, "store", "local", "rename")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed rename changed scenario: %#v, %v", after, err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER fail_renamed_route`); err != nil {
		t.Fatal(err)
	}
	after, err = store.RenameMockRoute(ctx, "store", "local", "rename", "LOOKUP", route)
	if err != nil || len(after.Routes) != 2 {
		t.Fatalf("rename = %#v, %v", after, err)
	}
	for _, saved := range after.Routes {
		if saved.Name == "lookup" {
			t.Fatal("original route remained after rename")
		}
		if saved.Name == "occupied" && !reflect.DeepEqual(saved, before.Routes[1]) {
			t.Fatal("rename changed another route")
		}
		if saved.Name == "renamed" && (saved.Body != route.Body || saved.Status != route.Status || !saved.CreatedAt.Equal(before.Routes[0].CreatedAt) || !reflect.DeepEqual(saved.Query, route.Query)) {
			t.Fatalf("renamed configuration = %#v", saved)
		}
	}
}
