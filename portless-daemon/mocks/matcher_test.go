package mocks

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/portless-run/portless/portless-daemon/model"
)

func TestCompiledProfileUsesDeterministicSpecificity(t *testing.T) {
	profile := model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{
		{Name: "parameter", Method: "GET", Path: "/inventory/{sku}", Status: 200, Body: "parameter", Enabled: true},
		{Name: "literal", Method: "GET", Path: "/inventory/featured", Status: 200, Body: "literal", Enabled: true},
		{Name: "warehouse", Method: "GET", Path: "/inventory/{sku}", Query: map[string]string{"warehouse": "central"}, Status: 200, Body: "warehouse", Enabled: true},
	}}
	compiled, err := Compile(profile)
	if err != nil {
		t.Fatal(err)
	}
	profile.Routes[2].Query["warehouse"] = "changed"
	returned := compiled.Profile()
	returned.Routes[2].Query["warehouse"] = "also-changed"
	cases := []struct {
		path, query, route string
	}{
		{"/inventory/featured", "", "literal"},
		{"/inventory/coffee", "", "parameter"},
		{"/inventory/coffee", "warehouse=central", "warehouse"},
	}
	for _, test := range cases {
		values, _ := url.ParseQuery(test.query)
		route, err := compiled.Match("get", test.path, values)
		if err != nil || route.Name != test.route {
			t.Fatalf("match %s?%s = %#v, %v; want %s", test.path, test.query, route, err, test.route)
		}
	}
	preview, err := compiled.Preview(model.MockRequest{Method: "GET", Path: "/missing"})
	if err != nil || preview.Matched || preview.Status != http.StatusNotImplemented {
		t.Fatalf("unmatched preview = %#v", preview)
	}
	if _, err := compiled.Match("GET", "/inventory/", nil); err == nil {
		t.Fatal("an empty path segment matched a named parameter")
	}
}

func TestPreviewAcceptsRequestHeadersAndBodyAndRejectsUnsafeInput(t *testing.T) {
	compiled, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{{
		Name: "reserve", Method: "POST", Path: "/inventory", Status: 202, Enabled: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := compiled.Preview(model.MockRequest{
		Method: "POST", Path: "/inventory", Headers: map[string][]string{"Content-Type": {"application/json"}, "X-Trace": {"one", "two"}}, Body: `{"sku":"coffee-mug"}`,
	})
	if err != nil || !preview.Matched || preview.Route != "reserve" {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	invalid := []model.MockRequest{
		{Method: "POST", Path: "/inventory", Headers: map[string][]string{"Bad Header": {"value"}}},
		{Method: "POST", Path: "/inventory", Headers: map[string][]string{"X-Test": {"value\r\ninjected: true"}}},
		{Method: "POST", Path: "/inventory", Headers: map[string][]string{"X-Test": {"one"}, "x-test": {"two"}}},
		{Method: "POST", Path: "/inventory", Body: strings.Repeat("x", MaxPreviewRequestBodyBytes+1)},
	}
	for index, request := range invalid {
		if _, err := compiled.Preview(request); err == nil {
			t.Errorf("invalid preview request %d was accepted", index)
		}
	}
}

func TestCompileRejectsAmbiguousRoutes(t *testing.T) {
	_, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{
		{Name: "by-id", Method: "GET", Path: "/inventory/{id}", Status: 200, Enabled: true},
		{Name: "by-sku", Method: "GET", Path: "/inventory/{sku}", Status: 404, Enabled: true},
	}})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompileRejectsUnsafeResponseHeaders(t *testing.T) {
	for name, value := range map[string]string{
		"Content-Length": "100",
		"Bad Header":     "value",
		"X-Test":         "value\r\ninjected: true",
	} {
		_, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{{
			Name: "unsafe", Method: "GET", Path: "/inventory", Status: 200, Headers: map[string]string{name: value}, Enabled: true,
		}}})
		if err == nil {
			t.Fatalf("header %q was accepted", name)
		}
	}
	_, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{{
		Name: "duplicates", Method: "GET", Path: "/inventory", Status: 200,
		Headers: map[string]string{"X-Mode": "one", "x-mode": "two"}, Enabled: true,
	}}})
	if err == nil || !strings.Contains(err.Error(), "different casing") {
		t.Fatalf("case-insensitive duplicate header error = %v", err)
	}
}

func TestCompileBoundsProfileSize(t *testing.T) {
	routes := make([]model.MockRoute, MaxRoutesPerProfile+1)
	for index := range routes {
		routes[index] = model.MockRoute{Name: "route-" + strconv.Itoa(index), Method: "GET", Path: "/" + strconv.Itoa(index), Status: 200, Enabled: true}
	}
	if _, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: routes}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("route limit error = %v", err)
	}
	if _, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{{Name: "large", Method: "GET", Path: "/large", Status: 200, Body: strings.Repeat("x", MaxResponseBodyBytes+1), Enabled: true}}}); err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("body limit error = %v", err)
	}
}

func TestCompileRejectsUnknownOrInformationalResponseStatuses(t *testing.T) {
	for _, status := range []int{103, 299, 306, 419, 499, 509, 599} {
		_, err := Compile(model.MockProfile{Name: "inventory", Service: "inventory", Routes: []model.MockRoute{{Name: "status", Method: "GET", Path: "/inventory", Status: status, Enabled: true}}})
		if err == nil || !strings.Contains(err.Error(), "registered final HTTP response status") {
			t.Errorf("status %d error = %v", status, err)
		}
	}
}

func TestManagerHotReloadsOnePrivateListener(t *testing.T) {
	manager := NewManager()
	defer manager.Close(context.Background())
	profile := model.MockProfile{Name: "sold-out", Service: "inventory", Routes: []model.MockRoute{{Name: "lookup", Method: "GET", Path: "/inventory/{sku}", Status: 409, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"available":false}`, Enabled: true}}}
	port, err := manager.Set("store/local", "inventory", profile)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/inventory/coffee")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 409 || string(body) != `{"available":false}` || response.Header.Get(ProfileHeader) != "sold-out" || response.Header.Get(RouteHeader) != "lookup" {
		t.Fatalf("response = %d %q %#v", response.StatusCode, body, response.Header)
	}

	profile.Routes[0].Status = http.StatusOK
	profile.Routes[0].Body = `{"available":true}`
	reloadedPort, err := manager.Set("store/local", "inventory", profile)
	if err != nil || reloadedPort != port {
		t.Fatalf("reloaded port = %d, err = %v; want %d", reloadedPort, err, port)
	}
	response, err = http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/inventory/coffee")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != `{"available":true}` {
		t.Fatalf("reloaded response = %d %q", response.StatusCode, body)
	}

	unmatched, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	unmatchedBody, _ := io.ReadAll(unmatched.Body)
	unmatched.Body.Close()
	if unmatched.StatusCode != http.StatusNotImplemented || !strings.Contains(string(unmatchedBody), "MOCK_ROUTE_NOT_MATCHED") {
		t.Fatalf("unmatched response = %d %q", unmatched.StatusCode, unmatchedBody)
	}
}
