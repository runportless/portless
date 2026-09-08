package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestSetMockScenarioPolicyUsesDurableOperationAndIdempotency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/v1/environments/store/local/mocks/scenario/policy" || request.Header.Get("Idempotency-Key") != "policy-key" {
			t.Errorf("request: %s %s %v", request.Method, request.URL.Path, request.Header)
		}
		var input contract.SetMockScenarioPolicyRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.UnmatchedRequests != "forward" {
			t.Errorf("input: %#v %v", input, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(writer).Encode(contract.Operation{Number: 7, State: "running"})
	}))
	defer server.Close()
	op, err := New(server.URL, "test", server.Client()).SetMockScenarioPolicy(t.Context(), "store", "local", "scenario", contract.SetMockScenarioPolicyRequest{UnmatchedRequests: "forward"}, "policy-key")
	if err != nil || op.Number != 7 || op.State != "running" {
		t.Fatalf("operation: %#v %v", op, err)
	}
}

func TestPutMockRouteSendsOriginalIdentityAndNewName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut || request.URL.Path != "/api/v1/environments/store/local/mocks/scenario/routes/original" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		var route contract.MockRoute
		if err := json.NewDecoder(request.Body).Decode(&route); err != nil || route.Name != "renamed" {
			t.Errorf("route = %#v, %v", route, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(contract.MockScenario{UnmatchedRequests: "reject", Name: "scenario", Routes: []contract.MockRoute{route}})
	}))
	defer server.Close()
	client := New(server.URL, "test", server.Client())
	scenario, err := client.PutMockRoute(t.Context(), "store", "local", "scenario", "original", contract.MockRoute{Name: "renamed", Service: "inventory", Method: "GET", Path: "/", Status: 200, Enabled: true})
	if err != nil || len(scenario.Routes) != 1 || scenario.Routes[0].Name != "renamed" {
		t.Fatalf("rename = %#v, %v", scenario, err)
	}
}

func TestPreviewMockEncodesRequestEnvelopeAndDecodesResponse(t *testing.T) {
	request := contract.MockRequest{
		Service: "checkout", Method: "POST", Path: "/orders",
		Query:   map[string][]string{"item": {"first", "second"}, "empty": {""}},
		Headers: map[string][]string{"X-Trace": {"one", "two"}}, Body: `{"quantity":2}`,
	}
	draft := contract.MockRoute{
		Name: "create-order", Service: "checkout", Method: "POST", Path: "/orders", Status: 503,
		Query:   map[string]contract.MockQueryMatcher{"item": {Match: "regex", Value: "first|second"}},
		Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"available":false}`, DelayMS: 25, Enabled: true,
	}
	for _, test := range []struct {
		name  string
		input contract.PreviewMockRequest
	}{
		{name: "saved routes", input: contract.PreviewMockRequest{Request: request}},
		{name: "new draft", input: contract.PreviewMockRequest{Request: request, Draft: &draft}},
		{name: "existing draft", input: contract.PreviewMockRequest{Request: request, Draft: &draft, OriginalRoute: draft.Name}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
				if incoming.Method != http.MethodPost || incoming.URL.Path != "/api/v1/environments/billing/local/mocks/checkout-empty/preview" {
					t.Errorf("request = %s %s", incoming.Method, incoming.URL.Path)
				}
				content, err := io.ReadAll(incoming.Body)
				if err != nil {
					t.Error(err)
					return
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(content, &fields); err != nil {
					t.Error(err)
					return
				}
				if fields["request"] == nil || fields["service"] != nil || (fields["draft"] != nil) != (test.input.Draft != nil) || (fields["originalRoute"] != nil) != (test.input.OriginalRoute != "") {
					t.Errorf("preview envelope = %s", content)
				}
				var decoded contract.PreviewMockRequest
				if err := json.Unmarshal(content, &decoded); err != nil || !reflect.DeepEqual(decoded, test.input) {
					t.Errorf("decoded preview = %#v, error = %v; want %#v", decoded, err, test.input)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"service":"checkout","route":"create-order","outcome":"mocked","response":{"status":503,"headers":{"Content-Type":"application/json","X-Portless-Mock":"checkout-empty"},"body":"{\"available\":false}","delayMs":25}}`)
			}))
			defer server.Close()

			client := New(server.URL, "test", server.Client())
			preview, err := client.PreviewMock(t.Context(), "billing", "local", "checkout-empty", test.input)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Outcome != "mocked" || preview.Service != "checkout" || preview.Route != draft.Name || preview.Response.Status != draft.Status || preview.Response.Body != draft.Body || preview.Response.DelayMS != draft.DelayMS || preview.Response.Headers["X-Portless-Mock"] != "checkout-empty" {
				t.Fatalf("preview = %#v", preview)
			}
		})
	}
}
