package database

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestMockScenariosPersistServiceRoutesAndCloneWithEnvironment(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "portless.db")
	controlStore, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if controlStore != nil {
			_ = controlStore.Close()
		}
	}()
	definition := model.ProjectModel{SuggestedName: "store", Services: []model.ServiceDefinition{{Name: "inventory", Kind: model.ServiceProcess}}}
	if _, err := controlStore.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "store", Services: []string{"inventory"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, []model.ComponentBinding{{Service: "inventory", Provider: model.ProviderLocal, Source: "store"}}); err != nil {
		t.Fatal(err)
	}
	created, err := controlStore.CreateMockScenario(ctx, "store", "local", model.MockScenario{Name: "sold-out", Description: "No stock"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Project != "store" || created.Environment != "local" || len(created.Routes) != 0 || created.CreatedAt.IsZero() {
		t.Fatalf("created scenario = %#v", created)
	}
	updated, err := controlStore.PutMockRoute(ctx, "store", "local", "SOLD-OUT", model.MockRoute{Name: "lookup", Service: "inventory", Method: "get", Path: "/inventory/{sku}", Query: map[string]model.MockQueryMatcher{"warehouse": {Match: "equals", Value: "central"}}, Status: 409, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"available":false}`, DelayMS: 25, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Routes) != 1 || updated.Routes[0].Service != "inventory" || updated.Routes[0].Method != "GET" || updated.Routes[0].Query["warehouse"] != (model.MockQueryMatcher{Match: "equals", Value: "central"}) || updated.Routes[0].ModifiedAt.IsZero() {
		t.Fatalf("updated scenario = %#v", updated)
	}
	// Exercise the on-disk schema transition using an existing saved route.
	if _, err := controlStore.db.ExecContext(ctx, `UPDATE mock_scenario_routes SET query_json = ?`, `{"warehouse":"central","include":""}`); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = 11`); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.Close(); err != nil {
		t.Fatal(err)
	}
	controlStore, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = controlStore.MockScenario(ctx, "store", "local", "sold-out")
	if err != nil || !reflect.DeepEqual(updated.Routes[0].Query, map[string]model.MockQueryMatcher{"warehouse": {Match: "equals", Value: "central"}, "include": {Match: "exists"}}) {
		t.Fatalf("migrated query = %#v, %v", updated, err)
	}
	updated.Routes[0].Query["sku"] = model.MockQueryMatcher{Match: "regex", Value: `coffee-\w+`}
	updated, err = controlStore.PutMockRoute(ctx, "store", "local", "sold-out", updated.Routes[0])
	if err != nil {
		t.Fatal(err)
	}
	// A subsequent open must retain typed matchers without converting them again.
	if err := controlStore.Close(); err != nil {
		t.Fatal(err)
	}
	controlStore, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CloneEnvironment(ctx, "store", "local", "qa"); err != nil {
		t.Fatal(err)
	}
	cloned, err := controlStore.MockScenario(ctx, "store", "qa", "sold-out")
	if err != nil || len(cloned.Routes) != 1 || cloned.Routes[0].Body != `{"available":false}` || !reflect.DeepEqual(cloned.Routes[0].Query, updated.Routes[0].Query) {
		t.Fatalf("cloned scenario = %#v, err = %v", cloned, err)
	}
	if _, err := controlStore.DeleteMockRoute(ctx, "store", "qa", "sold-out", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.DeleteMockScenario(ctx, "store", "qa", "sold-out"); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.MockScenario(ctx, "store", "qa", "sold-out"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted scenario error = %v", err)
	}
}
