package mocks

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestPreviewMatchesRuntimeResponseSemantics(t *testing.T) {
	for _, test := range []struct {
		name, routeMethod, requestMethod, path string
		status                                 int
		matched                                bool
	}{
		{name: "matched", routeMethod: "GET", requestMethod: "GET", path: "/items/coffee", status: 200, matched: true},
		{name: "head", routeMethod: "HEAD", requestMethod: "HEAD", path: "/items/coffee", status: 200, matched: true},
		{name: "no content", routeMethod: "GET", requestMethod: "GET", path: "/items/coffee", status: 204, matched: true},
		{name: "not modified", routeMethod: "GET", requestMethod: "GET", path: "/items/coffee", status: 304, matched: true},
		{name: "unmatched", routeMethod: "GET", requestMethod: "GET", path: "/missing", status: 200},
		{name: "head has no get fallback", routeMethod: "GET", requestMethod: "HEAD", path: "/items/coffee", status: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := model.MockScenario{Name: "preview", Routes: []model.MockRoute{{
				Name: "lookup", Service: "inventory", Method: test.routeMethod, Path: "/items/{sku}", Status: test.status, Body: "{\"available\":true}", Enabled: true,
				Headers: map[string]string{"content-type": "application/json", "X-Custom": "retained", "x-portless-mock-scenario": "spoofed", "x-portless-mock-route": "spoofed"},
			}}}
			compiled, err := Compile(scenario)
			if err != nil {
				t.Fatal(err)
			}
			query := url.Values{"warehouse": {"central", "east"}}
			preview, err := compiled.Preview(model.MockRequest{Service: "inventory", Method: test.requestMethod, Path: test.path, Query: query})
			if err != nil || (preview.Outcome == "mocked") != test.matched {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			manager := NewManager()
			t.Cleanup(func() { _ = manager.Close(t.Context()) })
			port, err := manager.Set("store/local", "inventory", scenario)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(t.Context(), test.requestMethod, "http://127.0.0.1:"+strconv.Itoa(port)+test.path+"?"+query.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Timeout: 2 * time.Second}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != preview.Response.Status || string(body) != preview.Response.Body {
				t.Fatalf("runtime = %d %q, preview = %d %q, err = %v", response.StatusCode, body, preview.Response.Status, preview.Response.Body, err)
			}
			for name, value := range preview.Response.Headers {
				if got := response.Header.Get(name); got != value {
					t.Errorf("%s = %q, preview = %q", name, got, value)
				}
			}
			if preview.Response.Headers[ScenarioHeader] != scenario.Name || (test.matched && preview.Response.Headers[RouteHeader] != "lookup") {
				t.Fatalf("mock-owned headers = %#v", preview.Response.Headers)
			}
			if test.status == http.StatusNotModified && preview.Response.Headers["Content-Type"] != "" {
				t.Fatalf("304 preview retained Content-Type: %#v", preview.Response.Headers)
			}
			if !test.matched && test.requestMethod != http.MethodHead && !strings.Contains(preview.Response.Body, "MOCK_ROUTE_NOT_MATCHED") {
				t.Fatalf("unmatched preview omitted structured error: %q", preview.Response.Body)
			}
		})
	}
}

func TestPreviewReportsDelayWithoutWaiting(t *testing.T) {
	compiled, err := Compile(model.MockScenario{Name: "preview", Routes: []model.MockRoute{{Name: "delayed", Service: "inventory", Method: "GET", Path: "/", Status: 200, DelayMS: 300_000, Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan model.MockPreview, 1)
	go func() {
		preview, _ := compiled.Preview(model.MockRequest{Service: "inventory", Method: "GET", Path: "/"})
		result <- preview
	}()
	select {
	case preview := <-result:
		if preview.Response.DelayMS != 300_000 || preview.Outcome != "mocked" {
			t.Fatalf("delay preview = %#v", preview)
		}
	case <-time.After(time.Second):
		t.Fatal("preview waited for the configured delay")
	}
}
