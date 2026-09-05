package controlplane

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestMockScenarioRecordingRoutesPreserveServicesAndDeduplicatePerService(t *testing.T) {
	exchanges := []model.TrafficExchange{
		{Protocol: model.ProtocolHTTP, Target: "inventory", Method: "GET", RequestTarget: "/health", Status: 503, ResponseBody: "inventory newest"},
		{Protocol: model.ProtocolHTTP, Target: "payments", Method: "GET", RequestTarget: "/health", Status: 200, ResponseBody: "payments"},
		{Protocol: model.ProtocolHTTP, Target: "inventory", Method: "GET", RequestTarget: "/health", Status: 200, ResponseBody: "inventory older"},
		{Protocol: model.ProtocolTCP, Target: "inventory", Method: "GET", RequestTarget: "/tcp", Status: 200},
	}
	routes, warnings := mockRoutesFromRecording(map[string]string{"inventory": "inventory", "payments": "payments"}, exchanges)
	if len(routes) != 2 || routes[0].Service != "inventory" || routes[0].Body != "inventory newest" || routes[1].Service != "payments" || routes[0].Name == routes[1].Name || len(warnings) != 1 {
		t.Fatalf("recording routes = %#v, warnings = %#v", routes, warnings)
	}
	filtered, _ := mockRoutesFromRecording(map[string]string{"payments": "payments"}, exchanges)
	if len(filtered) != 1 || filtered[0].Service != "payments" {
		t.Fatalf("filtered routes = %#v", filtered)
	}
}

func TestMockScenarioOpenAPIImportAppendsAtomicallyForAnExplicitService(t *testing.T) {
	app, _ := mockScenarioTestService(t)
	ctx := context.Background()
	createTestMockScenario(t, app, "imported", "inventory")
	document := []byte(`{"openapi":"3.1.0","info":{"title":"payments","version":"1"},"paths":{"/health":{"get":{"responses":{"200":{"description":"ready"}}}}}}`)
	scenario, _, err := app.ImportMockScenarioOpenAPI(ctx, "store", "local", "imported", "payments", document, "test")
	if err != nil || len(scenario.Routes) != 2 || !reflect.DeepEqual(scenario.Activation.TargetServices, []string{"inventory", "payments"}) {
		t.Fatalf("import = %#v, %v", scenario, err)
	}
	ambiguous := []byte(`{"openapi":"3.1.0","info":{"title":"payments","version":"1"},"paths":{"/new":{"get":{"responses":{"200":{"description":"new"}}}},"/health":{"get":{"responses":{"200":{"description":"duplicate"}}}}}}`)
	if _, _, err := app.ImportMockScenarioOpenAPI(ctx, "store", "local", "imported", "payments", ambiguous, "test"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous import error = %v", err)
	}
	after, err := app.MockScenario(ctx, "store", "local", "imported")
	if err != nil || !reflect.DeepEqual(scenario.Routes, after.Routes) {
		t.Fatalf("failed import changed routes: %#v, %v", after, err)
	}
}
