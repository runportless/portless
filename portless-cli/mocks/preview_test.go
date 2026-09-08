package mocks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-cli/command"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/identity"
)

func TestPreviewCommandUsesSavedScenarioEnvelopeAndPreservesOutput(t *testing.T) {
	for _, test := range []struct {
		name       string
		jsonOutput bool
		response   string
		output     string
	}{
		{name: "matched", response: `{"service":"checkout","route":"create-order","outcome":"mocked","response":{"status":201,"body":"created","delayMs":25}}`, output: "matched create-order for checkout · 201 · 25ms delay\n\ncreated\n"},
		{name: "unmatched", response: `{"service":"checkout","outcome":"rejected","response":{"status":501}}`, output: "no checkout route matched; the mock would return 501\n"},
		{name: "json", jsonOutput: true, response: `{"service":"checkout","route":"create-order","outcome":"mocked","response":{"status":201,"headers":{"X-Portless-Mock":"checkout-empty"},"body":"created","delayMs":25}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var previewCalls int
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.Method + " " + request.URL.Path {
				case "GET /api/v1/environments/billing/local":
					_, _ = io.WriteString(writer, `{"project":"billing","name":"local"}`)
				case "POST /api/v1/environments/billing/local/mocks/checkout-empty/preview":
					previewCalls++
					var fields map[string]json.RawMessage
					if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
						t.Error(err)
						return
					}
					if len(fields) != 1 || fields["request"] == nil {
						t.Errorf("CLI preview must contain only the request envelope: %#v", fields)
					}
					var actual contract.MockRequest
					if err := json.Unmarshal(fields["request"], &actual); err != nil {
						t.Error(err)
					}
					want := contract.MockRequest{
						Service: "checkout", Method: "POST", Path: "/orders",
						Query:   map[string][]string{"item": {"first", "a+b%20c"}, "empty": {""}},
						Headers: map[string][]string{"X-Trace": {"one", "two"}}, Body: `{"quantity":2}`,
					}
					if !reflect.DeepEqual(actual, want) {
						t.Errorf("preview request = %#v, want %#v", actual, want)
					}
					_, _ = io.WriteString(writer, test.response)
				default:
					t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			out := &bytes.Buffer{}
			commands := New(&command.Context{
				Out: out, Err: io.Discard, NoColor: true, JSONOutput: test.jsonOutput, EnvironmentOverride: "billing/local",
				Daemon: &previewDaemon{client: apiclient.New(server.URL, "test", server.Client())},
			})
			cmd := commands.mockCommand()
			cmd.SetArgs([]string{"preview", "checkout-empty", "--service", "checkout", "--method", "POST", "--path", "/orders", "--query", "item=first", "--query", "item=a+b%20c", "--query", "empty=", "--header", "X-Trace=one", "--header", "X-Trace=two", "--body", `{"quantity":2}`})
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			if previewCalls != 1 {
				t.Fatalf("preview calls = %d", previewCalls)
			}
			if test.jsonOutput {
				var actual, want contract.MockPreview
				if err := json.Unmarshal(out.Bytes(), &actual); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(test.response), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual, want) {
					t.Fatalf("JSON preview = %#v, want %#v", actual, want)
				}
			} else if out.String() != test.output {
				t.Fatalf("output = %q, want %q", out.String(), test.output)
			}
		})
	}
}

type previewDaemon struct {
	command.DaemonController
	client *apiclient.Client
}

// Connect returns the HTTP fixture without inspecting or starting a daemon.
func (d *previewDaemon) Connect(context.Context) (*apiclient.Client, identity.Record, error) {
	return d.client, identity.Record{}, nil
}
