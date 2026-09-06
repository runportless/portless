package controlplane

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestRenameMockRoutePreservesActiveProviderAndUpdatesLiveResponses(t *testing.T) {
	app, store := mockScenarioTestService(t)
	ctx := t.Context()
	createTestMockScenario(t, app, "rename", "inventory")
	operation, err := app.SetMockScenarioEnabled(ctx, "store", "local", "rename", true, "test", "enable-rename")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("enable = %#v", operation)
	}
	operation, err = app.StartService(ctx, "store", "local", "inventory", "test", "start-rename-inventory")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("start mock = %#v", operation)
	}
	before, err := app.MockScenario(ctx, "store", "local", "rename")
	if err != nil {
		t.Fatal(err)
	}
	environmentBefore, err := store.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	address, ok := app.mocks.Address("store/local", "inventory")
	if !ok {
		t.Fatal("active mock listener missing")
	}
	route := before.Routes[0]
	route.Name, route.Status, route.Body = "inventory-ready", 202, "renamed response"
	route.Query = map[string]model.MockQueryMatcher{"sku": {Match: "regex", Value: "coffee-.*"}}
	route.Headers = map[string]string{"X-Custom": "retained"}
	updated, err := app.PutMockRoute(ctx, "store", "local", "rename", before.Routes[0].Name, route, "test")
	if err != nil || len(updated.Routes) != 1 {
		t.Fatalf("rename = %#v, %v", updated, err)
	}
	if updated.Routes[0].Name != route.Name || !updated.Routes[0].CreatedAt.Equal(before.Routes[0].CreatedAt) || !reflect.DeepEqual(updated.Activation, before.Activation) {
		t.Fatalf("rename changed ownership or creation time: %#v", updated)
	}
	if current, ok := app.mocks.Address("store/local", "inventory"); !ok || current != address {
		t.Fatalf("rename replaced listener: %q -> %q", address, current)
	}
	environmentAfter, err := store.Environment(ctx, "store", "local")
	if err != nil || !reflect.DeepEqual(environmentBefore, environmentAfter) {
		t.Fatalf("rename changed environment: %#v, %v", environmentAfter, err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/health?sku=coffee-mug", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 202 || string(body) != route.Body || response.Header.Get(mocks.RouteHeader) != route.Name || response.Header.Get("X-Custom") != "retained" {
		t.Fatalf("renamed live response = %d %q %#v, %v", response.StatusCode, body, response.Header, err)
	}
	// A stale original identity must never create a second route.
	route.Name = "another-name"
	if _, err := app.PutMockRoute(ctx, "store", "local", "rename", before.Routes[0].Name, route, "test"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("stale rename = %v", err)
	}
	after, err := app.MockScenario(ctx, "store", "local", "rename")
	if err != nil || !reflect.DeepEqual(updated, after) {
		t.Fatalf("stale rename changed scenario: %#v, %v", after, err)
	}
}
