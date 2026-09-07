//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"io"
	"testing"
)

// Every control action in this release journey goes through the compiled stdio
// server. Only ordinary application requests and isolated harness teardown bypass MCP.
func TestMCPApplicationWorkflowParity(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	session, _ := connectMCPCommand(t, binary, home, checkout, "mcp", "serve", "--allow-configuration", "--allow-lifecycle", "--allow-traffic-control", "--allow-sensitive-traffic", "--allow-replay")
	if count := len(mcpToolNames(t, session)); count != 63 {
		t.Fatalf("full inventory=%d", count)
	}
	args := func(extra map[string]any) map[string]any {
		result := map[string]any{"environment": "mcp-parity/local"}
		for key, value := range extra {
			result[key] = value
		}
		return result
	}
	invokeMCP[map[string]any](t, session, "portless_discover_project", map[string]any{"path": checkout, "name": "mcp-parity"})
	started := invokeMCP[struct {
		Operation contract.Operation `json:"operation"`
	}](t, session, "portless_start_environment", args(map[string]any{"waitSeconds": 120}))
	if started.Operation.State != "succeeded" || started.Operation.Actor != "MCP" {
		t.Fatalf("startup=%#v", started)
	}
	invokeMCP[map[string]any](t, session, "portless_start_recording", args(map[string]any{"recording": "capture", "durationSeconds": 120, "maxEvents": 1000, "capturePayloads": true}))
	for _, path := range []string{"/api/orders", "/checkout?sku=coffee-mug&quantity=1"} {
		response := applicationRequest(t, home, "checkout.local.mcp-parity.localhost", path, nil)
		_, err := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("application %s: %v %s", path, err, response.Status)
		}
	}
	invokeMCP[map[string]any](t, session, "portless_stop_recording", args(map[string]any{"recording": "capture"}))
	exchanges := invokeMCP[struct {
		Exchanges []contract.TrafficExchange `json:"exchanges"`
	}](t, session, "portless_query_traffic", args(map[string]any{"limit": 100}))
	var original contract.TrafficExchange
	for _, exchange := range exchanges.Exchanges {
		if exchange.Source == "external" && exchange.Target == "checkout" && exchange.Path == "/api/orders" {
			original = exchange
		}
	}
	if original.Sequence == 0 {
		t.Fatalf("missing root exchange: %#v", exchanges)
	}
	traces := invokeMCP[contract.TrafficTraceList](t, session, "portless_list_traces", args(map[string]any{"limit": 20}))
	if len(traces.Traces) == 0 {
		t.Fatal("missing traces")
	}
	invokeMCP[map[string]any](t, session, "portless_get_trace", args(map[string]any{"number": traces.Traces[0].Number, "limit": 20}))
	var exported bytes.Buffer
	cursor := ""
	for {
		chunk := invokeMCP[contract.RecordingExportChunk](t, session, "portless_export_recording", args(map[string]any{"recording": "capture", "cursor": cursor, "maxBytes": 1024}))
		data, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		exported.Write(data)
		if chunk.Complete {
			break
		}
		if chunk.NextCursor == "" {
			t.Fatal("incomplete export has no cursor")
		}
		cursor = chunk.NextCursor
	}
	var recording contract.RecordingExport
	if err := json.Unmarshal(exported.Bytes(), &recording); err != nil || recording.SchemaVersion != 4 || len(recording.Exchanges) == 0 {
		t.Fatalf("recording export=%s %v", exported.Bytes(), err)
	}
	invokeMCP[map[string]any](t, session, "portless_create_mock_scenario", args(map[string]any{"scenario": "review"}))
	draft := map[string]any{"name": "orders", "service": "checkout", "method": "GET", "path": "/api/orders", "status": 202, "body": "{\"mocked\":true}", "enabled": true}
	invokeMCP[map[string]any](t, session, "portless_put_mock_route", args(map[string]any{"scenario": "review", "route": "orders", "draft": draft}))
	invokeMCP[map[string]any](t, session, "portless_preview_mock", args(map[string]any{"scenario": "review", "request": map[string]any{"service": "checkout", "method": "GET", "path": "/api/orders"}, "includePayloads": true}))
	enabled := invokeMCP[struct {
		Operation contract.Operation `json:"operation"`
	}](t, session, "portless_set_mock_scenario_enabled", args(map[string]any{"scenario": "review", "enabled": true, "waitSeconds": 120}))
	if enabled.Operation.State != "succeeded" {
		t.Fatalf("mock activation=%#v", enabled)
	}
	replay := invokeMCP[struct {
		Workspace contract.TrafficReplayWorkspace `json:"workspace"`
	}](t, session, "portless_prepare_replay", args(map[string]any{"sequence": original.Sequence, "startedAt": original.StartedAt}))
	identity := map[string]any{"number": replay.Workspace.Number, "createdAt": replay.Workspace.CreatedAt, "daemonStartedAt": replay.Workspace.DaemonStartedAt}
	update := args(identity)
	update["revision"] = replay.Workspace.Revision
	update["draft"] = replay.Workspace.Draft
	replay = invokeMCP[struct {
		Workspace contract.TrafficReplayWorkspace `json:"workspace"`
	}](t, session, "portless_update_replay", update)
	run := args(identity)
	run["revision"] = replay.Workspace.Revision
	run["runNumber"] = replay.Workspace.NextRunNumber
	run["waitSeconds"] = 120
	first := invokeMCP[struct {
		Workspace contract.TrafficReplayWorkspace `json:"workspace"`
	}](t, session, "portless_run_replay", run)
	repeated := invokeMCP[struct {
		Workspace contract.TrafficReplayWorkspace `json:"workspace"`
	}](t, session, "portless_run_replay", run)
	if first.Workspace.Result == nil || first.Workspace.Result.Exchange.Status != 202 || !first.Workspace.Result.Comparison.StatusChanged || repeated.Workspace.Run.Number != first.Workspace.Run.Number || repeated.Workspace.Result.Exchange.Sequence != first.Workspace.Result.Exchange.Sequence {
		t.Fatalf("replay/deduplication failed: %#v %#v", first, repeated)
	}
	invokeMCP[map[string]any](t, session, "portless_get_replay", args(identity))
	invokeMCP[map[string]any](t, session, "portless_close_replay", args(identity))
	restored := invokeMCP[struct {
		Operation contract.Operation `json:"operation"`
	}](t, session, "portless_set_mock_scenario_enabled", args(map[string]any{"scenario": "review", "enabled": false, "waitSeconds": 120}))
	if restored.Operation.State != "succeeded" {
		t.Fatalf("restore=%#v", restored)
	}
	fault := invokeMCP[struct {
		Fault contract.FaultRule `json:"fault"`
	}](t, session, "portless_apply_fault", args(map[string]any{"fault": "delay", "source": "external", "target": "checkout", "latencyMs": 1, "durationSeconds": 120}))
	invokeMCP[map[string]any](t, session, "portless_disable_fault", args(map[string]any{"fault": "delay"}))
	disabled := invokeMCP[struct {
		Fault contract.FaultRule `json:"fault"`
	}](t, session, "portless_get_fault", args(map[string]any{"fault": "delay"}))
	invokeMCP[map[string]any](t, session, "portless_enable_fault", args(map[string]any{"fault": "delay", "expectedRevision": disabled.Fault.Revision}))
	current := invokeMCP[struct {
		Fault contract.FaultRule `json:"fault"`
	}](t, session, "portless_get_fault", args(map[string]any{"fault": "delay"}))
	if !current.Fault.ExpiresAt.Equal(*fault.Fault.ExpiresAt) {
		t.Fatal("enabling a fault extended its expiry")
	}
	invokeMCP[map[string]any](t, session, "portless_disable_fault", args(map[string]any{"fault": "delay"}))
	for _, artifact := range []struct{ tool, key, name string }{{"portless_delete_mock_scenario", "scenario", "review"}, {"portless_delete_recording", "recording", "capture"}, {"portless_delete_fault", "fault", "delay"}} {
		input := args(map[string]any{artifact.key: artifact.name})
		preview := invokeMCP[struct {
			Preview struct {
				Expected contract.ResourceVersion `json:"expected"`
			} `json:"preview"`
		}](t, session, artifact.tool, input)
		input["mode"] = "apply"
		input["confirm"] = true
		input["expected"] = preview.Preview.Expected
		invokeMCP[map[string]any](t, session, artifact.tool, input)
	}
	stopped := invokeMCP[struct {
		Operation contract.Operation `json:"operation"`
	}](t, session, "portless_stop_environment", args(map[string]any{"waitSeconds": 120}))
	if stopped.Operation.State != "succeeded" {
		t.Fatalf("stop=%#v", stopped)
	}
	projectSession, _ := connectMCPCommand(t, binary, home, checkout, "mcp", "serve", "--project", "mcp-parity", "--allow-configuration", "--source-root", checkout)
	invokeMCP[map[string]any](t, projectSession, "portless_clone_environment", args(map[string]any{"name": "qa"}))
	preview := invokeMCP[struct {
		Preview contract.ConfigurationPreview `json:"preview"`
	}](t, projectSession, "portless_forget_project", map[string]any{"project": "mcp-parity"})
	if len(preview.Preview.Environments) != 2 {
		t.Fatalf("project preview=%#v", preview)
	}
	invokeMCP[map[string]any](t, projectSession, "portless_forget_project", map[string]any{"project": "mcp-parity", "mode": "apply", "confirm": true, "expected": preview.Preview.Expected})
}

func invokeMCP[T any](t *testing.T, session *mcp.ClientSession, name string, args map[string]any) T {
	t.Helper()
	ctx := t.Context()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || result.IsError {
		encoded, _ := json.Marshal(result)
		t.Fatalf("%s: %v %s", name, err, encoded)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if scoped, ok := envelope["result"]; ok {
		data = scoped
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v %s", name, err, data)
	}
	return value
}
