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
	"time"
)

func TestMockPolicyToolReturnsReceiptAndPreservesRetryKey(t *testing.T) {
	var firstKey string
	var calls int
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPut || req.URL.Path != "/api/v1/environments/shop/local/mocks/sample/policy" {
			t.Errorf("request: %s %s", req.Method, req.URL.Path)
		}
		var input contract.SetMockScenarioPolicyRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil || input.UnmatchedRequests != "forward" {
			t.Errorf("input: %#v %v", input, err)
		}
		if calls == 0 {
			firstKey = req.Header.Get("Idempotency-Key")
		}
		if firstKey == "" || req.Header.Get("Idempotency-Key") != firstKey {
			t.Error("retry key changed")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contract.Operation{Project: "shop", Environment: "local", Number: 9, State: "running"})
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowTrafficControl: true, AllowLifecycle: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	for range 2 {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_set_mock_scenario_policy", Arguments: map[string]any{"environment": "shop/local", "scenario": "sample", "unmatchedRequests": "forward", "idempotencyKey": "same-change", "waitSeconds": 0}})
		if err != nil || result.IsError {
			t.Fatalf("result: %#v %v", result, err)
		}
		encoded, _ := json.Marshal(result)
		if !strings.Contains(string(encoded), `"number":9`) || !strings.Contains(string(encoded), `"state":"running"`) {
			t.Fatalf("receipt: %s", encoded)
		}
	}
}

func TestMockMutationReturnsMetadataAndNeverRetriesImport(t *testing.T) {
	var imports atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get(contract.MockMetadataHeader) != "1" {
			t.Error("mutation requested a full payload response")
		}
		if strings.HasSuffix(req.URL.Path, "/imports/openapi") {
			imports.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		// Mapping must remain safe even when a server returns an unexpectedly full scenario.
		_, _ = w.Write([]byte(`{"name":"sample","routes":[{"name":"one","body":"saved-secret","query":{"password":{"value":"matcher-secret"}}}],"activation":{"state":"disabled"}}`))
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowTrafficControl: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_create_mock_scenario", Arguments: map[string]any{"environment": "shop/local", "scenario": "sample"}})
	if err != nil || result.IsError {
		t.Fatalf("create=%#v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("mutation leaked payload: %s", encoded)
	}
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_import_mock_openapi", Arguments: map[string]any{"environment": "shop/local", "scenario": "sample", "service": "web", "document": "{}"}})
	if err != nil || !result.IsError || imports.Load() != 1 {
		t.Fatalf("uncertain import retried: calls=%d result=%#v err=%v", imports.Load(), result, err)
	}
}

func TestRecordingPayloadCaptureRequiresSensitivePermission(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		var recording contract.Recording
		if err := json.NewDecoder(req.Body).Decode(&recording); err != nil {
			t.Error(err)
		}
		if !recording.CapturePayloads || recording.MaxPayloadBytes != 1<<20 || recording.ExpiresAt == nil || recording.ExpiresAt.After(time.Now().Add(61*time.Second)) {
			t.Errorf("incorrect recording bounds: %#v", recording)
		}
		recording.Status = "active"
		_ = json.NewEncoder(w).Encode(recording)
	}))
	defer daemon.Close()
	for _, sensitive := range []bool{false, true} {
		session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowTrafficControl: true, AllowSensitiveTraffic: sensitive}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_start_recording", Arguments: map[string]any{"environment": "shop/local", "recording": "capture", "durationSeconds": 60, "capturePayloads": true, "maxPayloadBytes": 1 << 20}})
		closeSession()
		if err != nil || result.IsError == sensitive {
			t.Fatalf("sensitive=%t result=%#v err=%v", sensitive, result, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("capture permission did not gate API access: %d", calls.Load())
	}
}

func TestMockCleanupDefaultsToPreviewAndRequiresConfirmation(t *testing.T) {
	var previews, applies atomic.Int32
	now := time.Now().UTC()
	expected := contract.ResourceVersion{CreatedAt: now, ModifiedAt: now, ParentCreatedAt: now}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "DELETE" {
			t.Errorf("unexpected method %s", req.Method)
		}
		if req.URL.Query().Get("mode") == "preview" {
			previews.Add(1)
			_ = json.NewEncoder(w).Encode(contract.MockDeletionPreview{Scenario: contract.MockScenarioMetadata{UnmatchedRequests: "reject"}, Expected: expected, Routes: []string{"one"}})
			return
		}
		applies.Add(1)
		if req.Header.Get("If-Match") == "" {
			t.Error("missing atomic precondition")
		}
		w.WriteHeader(204)
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowTrafficControl: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	args := map[string]any{"environment": "shop/local", "scenario": "sample"}
	for step := 0; step < 3; step++ {
		if step > 0 {
			args["mode"] = "apply"
			args["expected"] = expected
		}
		if step == 2 {
			args["confirm"] = true
		}
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_delete_mock_scenario", Arguments: args})
		if err != nil || result.IsError != (step == 1) {
			t.Fatalf("step=%d result=%#v err=%v", step, result, err)
		}
	}
	if previews.Load() != 1 || applies.Load() != 1 {
		t.Fatalf("previews=%d applies=%d", previews.Load(), applies.Load())
	}
}

func TestMockDraftUsesEditableFieldsWithoutServerTimestamps(t *testing.T) {
	calls := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "PUT" {
			t.Errorf("method=%s", req.Method)
		}
		var draft contract.MockRoute
		if err := json.NewDecoder(req.Body).Decode(&draft); err != nil {
			t.Error(err)
		}
		if draft.Name != "health" || draft.Status != 200 || !draft.CreatedAt.IsZero() {
			t.Errorf("draft=%#v", draft)
		}
		_ = json.NewEncoder(w).Encode(contract.MockScenario{UnmatchedRequests: "reject", Name: "test", RouteCount: 1, PayloadsOmitted: true})
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowTrafficControl: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_put_mock_route", Arguments: map[string]any{"environment": "shop/local", "scenario": "test", "route": "health", "draft": map[string]any{"name": "health", "service": "web", "method": "GET", "path": "/health", "status": 200, "enabled": true}}})
	if err != nil || result.IsError || calls != 1 {
		t.Fatalf("editable draft rejected: %v %s calls=%d", err, textContent(result), calls)
	}
}
