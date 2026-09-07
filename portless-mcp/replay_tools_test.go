package portlessmcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestReplayRecoversAdmissionWithoutResubmitting(t *testing.T) {
	now := time.Now().UTC()
	identity := contract.TrafficReplayIdentity{CreatedAt: now, DaemonStartedAt: now.Add(-time.Hour)}
	var mu sync.Mutex
	runs, edits := 0, 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Header.Get(contract.ClientKindHeader) != "mcp" || req.Header.Get(contract.MCPReplayCapabilityHeader) != "1" {
			t.Error("missing fixed MCP replay capability")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(req.URL.Path, "/status"):
			status := contract.TrafficReplayStatus{TrafficReplayIdentity: identity, Project: "billing", Environment: "local", Number: 1, Revision: 2, Destinations: []string{"local"}, Receipts: []contract.TrafficReplayRun{}}
			if runs > 0 {
				status.Run = &contract.TrafficReplayRun{Number: 1, Revision: 2, State: "completed", Outcome: "response-received"}
				status.Receipts = append(status.Receipts, *status.Run)
			}
			_ = json.NewEncoder(w).Encode(status)
		case strings.HasSuffix(req.URL.Path, "/runs"):
			runs++
			var input contract.RunTrafficReplayRequest
			_ = json.NewDecoder(req.Body).Decode(&input)
			if input.RunNumber != 1 || input.Revision != 2 || !input.CreatedAt.Equal(now) {
				t.Errorf("incorrect admission: %#v", input)
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
		case strings.HasSuffix(req.URL.Path, "/draft"):
			edits++
			_, _ = io.WriteString(w, `{}`)
		default:
			_ = json.NewEncoder(w).Encode(contract.TrafficReplayWorkspace{TrafficReplayIdentity: identity, Project: "billing", Environment: "local", Number: 1, Revision: 2, Run: &contract.TrafficReplayRun{Number: 1, Revision: 2, State: "completed", Outcome: "response-received"}, Result: &contract.TrafficReplayResult{RunNumber: 1, Destination: contract.TrafficReplayDestination{Environment: "local"}, Request: contract.TrafficReplayDraft{Environment: "local"}, Comparison: contract.TrafficResponseComparison{State: "different", OriginalStatus: 200, ReplayStatus: 201}}})
		}
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "billing/local", AllowReplay: true, AllowSensitiveTraffic: true}, testConnector{client: apiclient.New(daemon.URL, "token", daemon.Client())})
	defer closeSession()
	arguments := map[string]any{"environment": "billing/local", "number": 1, "createdAt": now.Format(time.RFC3339Nano), "daemonStartedAt": identity.DaemonStartedAt.Format(time.RFC3339Nano), "revision": 2, "runNumber": 1}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_run_replay", Arguments: arguments})
	if err != nil || result.IsError {
		t.Fatalf("run: %v %#v", err, result)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(encoded), `"replayStatus":201`) || !strings.Contains(string(encoded), `"admissionUnknown":false`) {
		t.Fatalf("recovered result=%s", encoded)
	}
	mu.Lock()
	gotRuns := runs
	mu.Unlock()
	if gotRuns != 1 {
		t.Fatalf("application send retried: %d", gotRuns)
	}
	delete(arguments, "runNumber")
	arguments["draft"] = map[string]any{"environment": "qa", "method": "GET", "requestTarget": "/", "headers": map[string][]string{}, "bodyMode": "empty", "body": ""}
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_update_replay", Arguments: arguments})
	if err != nil || !result.IsError || !strings.Contains(textContent(result), "SCOPE_DENIED") {
		t.Fatalf("cross-environment update: %v %#v", err, result)
	}
	mu.Lock()
	defer mu.Unlock()
	if edits != 0 {
		t.Fatal("unauthorized destination reached draft endpoint")
	}
}

func TestReplayResultsBoundLargeBodiesWithoutLosingReceipt(t *testing.T) {
	r := newRuntime(Config{}, testConnector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	value := contract.TrafficReplayWorkspace{Project: "billing", Environment: "local", Number: 3, Draft: &contract.TrafficReplayDraft{Body: strings.Repeat("\x00", contract.TrafficReplayMaxBodyBytes)}, Run: &contract.TrafficReplayRun{Number: 2, State: "completed"}}
	result := r.replayResult(value, 0)
	if !result.DisplayTruncated || result.Workspace.Run.Number != 2 {
		t.Fatalf("missing receipt/truncation: %#v", result.Workspace.Run)
	}
	if err := r.checkOutput(result); err != nil {
		t.Fatal(err)
	}
	if len(value.Draft.Body) != contract.TrafficReplayMaxBodyBytes {
		t.Fatal("mapping mutated source draft")
	}
}

func TestReplayResultScopeChecksLatestDestination(t *testing.T) {
	r := newRuntime(Config{Environment: "billing/local"}, testConnector{client: apiclient.New("http://localhost", "token", nil)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	value := contract.TrafficReplayWorkspace{Draft: &contract.TrafficReplayDraft{Environment: "local"}, Result: &contract.TrafficReplayResult{Destination: contract.TrafficReplayDestination{Environment: "qa"}}}
	err := r.checkReplayPayloadScope(context.Background(), selectedEnvironment{project: "billing", environment: "local"}, value)
	if err == nil || !strings.Contains(err.Error(), "SCOPE_DENIED") {
		t.Fatalf("previous result leaked across scope: %v", err)
	}
}
