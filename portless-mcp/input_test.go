package portlessmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInputFramingCannotResetBoundsOrImpersonateReplay(t *testing.T) {
	cases := []struct {
		name       string
		frame      string
		replay, ok bool
	}{
		{"pretty", "{\n\"jsonrpc\":\"2.0\",\"method\":\"tools/list\",\"id\":1\n}\n", false, true},
		{"truncated", `{"params":{"value":"unterminated`, false, false},
		{"ordinary over", `{"method":"tools/call","params":{"name":"portless_import_mock_openapi","arguments":{"document":"` + strings.Repeat("x", ordinaryInputLimit) + `"}}}`, false, false},
		{"replay name in payload", `{"method":"tools/call","params":{"arguments":{"body":"portless_update_replay ` + strings.Repeat("x", ordinaryInputLimit) + `"},"name":"portless_import_mock_openapi"}}`, true, false},
		{"large replay", `{"method":"tools/call","params":{"arguments":{"body":"` + strings.Repeat("x", contract.TrafficReplayMaxBodyBytes) + `"},"name":"portless_update_replay"}}`, true, true},
		{"nesting", `{"a":` + strings.Repeat("[", 129) + `0` + strings.Repeat("]", 129) + `}`, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value, err := io.ReadAll(newMessageReader(strings.NewReader(test.frame), test.replay))
			if (err == nil) != test.ok {
				t.Fatalf("read=%d err=%v", len(value), err)
			}
			if !test.ok && len(value) != 0 {
				t.Fatal("rejected frame reached SDK")
			}
			if test.ok && !json.Valid(value) {
				t.Fatal("frame corrupted")
			}
		})
	}
	input := `{"id":1,"params":{"body":"line\nwith\"quotes{}"}}` + "\n" + `{"id":2}`
	decoder := json.NewDecoder(newMessageReader(bytes.NewBufferString(input), false))
	for i := 1; i <= 2; i++ {
		var value struct {
			ID int `json:"id"`
		}
		if err := decoder.Decode(&value); err != nil || value.ID != i {
			t.Fatalf("sequential frames=%#v %v", value, err)
		}
	}
}
func TestHistoricalReplayReceiptDoesNotReturnNewerComparison(t *testing.T) {
	old := contract.TrafficReplayRun{Number: 1, State: "complete"}
	newer := contract.TrafficReplayRun{Number: 2, State: "running"}
	result := replayWorkspaceForRun(contract.TrafficReplayWorkspace{Run: &newer, Receipts: []contract.TrafficReplayRun{old, newer}, Result: &contract.TrafficReplayResult{RunNumber: 2}}, 1)
	if result.Run == nil || result.Run.Number != 1 || result.Result != nil {
		t.Fatal("recovered a different run")
	}
}

func TestServeAcceptsReplayExecutionBodyWithoutEchoingIt(t *testing.T) {
	identity := contract.TrafficReplayIdentity{CreatedAt: time.Now().UTC(), DaemonStartedAt: time.Now().UTC()}
	var edits atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/status") {
			_ = json.NewEncoder(w).Encode(contract.TrafficReplayStatus{TrafficReplayIdentity: identity, Project: "shop", Environment: "local", Number: 1, Revision: 1, Destinations: []string{"local"}})
			return
		}
		if req.Method != "PUT" || !strings.HasSuffix(req.URL.Path, "/draft") {
			t.Errorf("unexpected request %s %s", req.Method, req.URL)
			w.WriteHeader(500)
			return
		}
		var update contract.UpdateTrafficReplayDraftRequest
		if err := json.NewDecoder(req.Body).Decode(&update); err != nil {
			t.Error(err)
		}
		if len(update.Draft.Body) != contract.TrafficReplayMaxBodyBytes {
			t.Errorf("body bytes=%d", len(update.Draft.Body))
		}
		edits.Add(1)
		_ = json.NewEncoder(w).Encode(contract.TrafficReplayWorkspace{TrafficReplayIdentity: identity, Project: "shop", Environment: "local", Number: 1, Revision: 2, Draft: &update.Draft})
	}))
	defer daemon.Close()
	input, send := io.Pipe()
	receive, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{Project: "shop", AllowReplay: true, AllowSensitiveTraffic: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())}, Streams{In: input, Out: output, Err: io.Discard})
	}()
	sdk := mcp.NewClient(&mcp.Implementation{Name: "large-stdio-test", Version: "test"}, nil)
	session, err := sdk.Connect(ctx, &mcp.IOTransport{Reader: receive, Writer: send}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, size := range []int{contract.TrafficReplayMaxBodyBytes, contract.TrafficReplayMaxBodyBytes + 1} {
		arguments := map[string]any{"environment": "shop/local", "number": 1, "createdAt": identity.CreatedAt, "daemonStartedAt": identity.DaemonStartedAt, "revision": 1, "draft": contract.TrafficReplayDraft{Environment: "local", Method: "POST", RequestTarget: "/", Headers: map[string][]string{}, BodyMode: "replace", Body: strings.Repeat("x", size)}}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "portless_update_replay", Arguments: arguments})
		if err != nil || result.IsError != (size > contract.TrafficReplayMaxBodyBytes) {
			t.Fatalf("stdio replay size %d: %v %s", size, err, textContent(result))
		}
		encoded, _ := json.Marshal(result)
		if len(encoded) > maximumResultSize {
			t.Fatal("mutation result exceeded complete MCP budget")
		}
	}
	if edits.Load() != 1 {
		t.Fatalf("oversized draft reached daemon: edits=%d", edits.Load())
	}
	_ = session.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdio EOF did not close server")
	}
}
