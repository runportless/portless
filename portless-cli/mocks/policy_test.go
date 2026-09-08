package mocks

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-cli/command"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestConfigureMockPolicyWaitsAndSupportsJSON(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[jsonOutput], func(t *testing.T) {
			polled := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.Method + " " + req.URL.Path {
				case "GET /api/v1/environments/store/local":
					_, _ = io.WriteString(w, `{"project":"store","name":"local"}`)
				case "PUT /api/v1/environments/store/local/mocks/scenario/policy":
					var input contract.SetMockScenarioPolicyRequest
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil || input.UnmatchedRequests != "forward" || req.Header.Get("Idempotency-Key") == "" {
						t.Errorf("request: %#v %v", input, err)
					}
					_, _ = io.WriteString(w, `{"project":"store","environment":"local","number":1,"state":"running"}`)
				case "GET /api/v1/environments/store/local/operations/1":
					polled = true
					_, _ = io.WriteString(w, `{"project":"store","environment":"local","number":1,"state":"succeeded"}`)
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			out := &bytes.Buffer{}
			commands := New(&command.Context{Out: out, Err: io.Discard, NoColor: true, JSONOutput: jsonOutput, EnvironmentOverride: "store/local", Daemon: &previewDaemon{client: apiclient.New(server.URL, "test", server.Client())}})
			cmd := commands.mockCommand()
			cmd.SetArgs([]string{"configure", "scenario", "--unmatched-requests", "forward"})
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !polled {
				t.Fatal("returned before the operation completed")
			}
			if jsonOutput {
				var op contract.Operation
				if err := json.Unmarshal(out.Bytes(), &op); err != nil || op.State != "succeeded" {
					t.Fatalf("JSON: %s %v", out, err)
				}
			} else if !strings.Contains(out.String(), "mock scenario scenario now uses forward") {
				t.Fatalf("output: %s", out)
			}
		})
	}
}
