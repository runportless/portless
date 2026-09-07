package portlessmcp

import (
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProjectInspectionFiltersHiddenEnvironments(t *testing.T) {
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/projects/shop" || req.URL.Query().Get("view") != "metadata" {
			t.Errorf("unexpected request %s", req.URL)
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"name":"shop","revision":1,"environments":[{"project":"shop","name":"local","clonedFrom":"hidden"},{"project":"shop","name":"hidden","reason":"hidden-sentinel"}]}`))
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local"}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_get_project", Arguments: map[string]any{"project": "shop"}})
	if err != nil || result.IsError {
		t.Fatalf("get project=%#v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "hidden") {
		t.Fatalf("hidden environment leaked: %s", encoded)
	}
	denied, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_get_project", Arguments: map[string]any{"project": "other"}})
	if err != nil || !denied.IsError {
		t.Fatalf("scope escape=%#v %v", denied, err)
	}
}

func TestMockPayloadPermissionAndMetadataPreview(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Header.Get(contract.MockMetadataHeader) != "1" {
			t.Error("preview failed to request metadata response")
		}
		// Defend against a server returning payloads even for a metadata request.
		_, _ = w.Write([]byte(`{"matched":true,"route":"health","status":200,"body":"saved-secret","headers":{"X-Secret":"header-secret"}}`))
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local"}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	for _, name := range []string{"portless_get_mock_route", "portless_preview_mock"} {
		args := map[string]any{"environment": "shop/local", "scenario": "test", "includePayloads": true}
		if name == "portless_get_mock_route" {
			args["route"] = "health"
		} else {
			args["request"] = map[string]any{"service": "web", "method": "GET", "path": "/"}
		}
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || !result.IsError {
			t.Fatalf("sensitive call=%#v %v", result, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("sensitive denial happened after daemon access")
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_preview_mock", Arguments: map[string]any{"environment": "shop/local", "scenario": "test", "request": map[string]any{"service": "web", "method": "GET", "path": "/"}}})
	if err != nil || result.IsError {
		t.Fatalf("preview=%#v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("preview leaked payload=%s", encoded)
	}
}

func TestTrafficSummaryPreservesProtocolMetadataWithoutMessages(t *testing.T) {
	var exchange contract.TrafficExchange
	if err := json.Unmarshal([]byte(`{"sequence":9,"path":"/query?token=secret","background":true,"mockScenario":"empty","mockRoute":"list","traceId":"trace","spanId":"span","requestKind":"websocket","tcp":{"kind":"operation","applicationProtocol":"postgresql","operation":"SELECT","inspection":"decoded","outcome":"success","requestMessages":[{"payload":"SELECT secret FROM secrets"}],"responseMessages":[{"payload":"secret"}],"requestMessageCount":2}}`), &exchange); err != nil {
		t.Fatal(err)
	}
	result := trafficSummaryResult(exchange)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "requestMessages") || result.TCP == nil || result.TCP.Operation != "SELECT" || result.TCP.RequestMessageCount != 2 || result.MockScenario != "empty" || !result.Background {
		t.Fatalf("summary=%s", encoded)
	}
}
