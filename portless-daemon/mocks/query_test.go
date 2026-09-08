package mocks

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestRegexQueryMatchesPreviewAndLiveRequests(t *testing.T) {
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close(t.Context()) })
	client := &http.Client{Timeout: 2 * time.Second}
	for _, test := range []struct {
		name, pattern, query string
		matched              bool
	}{
		{"match", `coffee-(mug|beans)`, "sku=coffee-mug", true},
		{"alternative", `coffee-mug|tea-cup`, "sku=tea-cup", true},
		{"whole prefix", `coffee-mug|tea-cup`, "sku=iced-tea-cup", false},
		{"whole suffix", `coffee-mug|tea-cup`, "sku=coffee-mug-large", false},
		{"case sensitive", `coffee-.*`, "sku=Coffee-mug", false},
		{"inline flag", `(?i)coffee-.*`, "sku=COFFEE-mug", true},
		{"repeated values", `coffee-.*`, "sku=tea&sku=coffee-mug", true},
		{"no repeated value matches", `coffee-.*`, "sku=tea&sku=milk", false},
		{"decoded exactly once", `a\+b&c`, "sku=a%2Bb%26c", true},
		{"encoded text stays literal", `a\+b&c`, "sku=a%252Bb%2526c", false},
		{"unicode", `\p{L}+`, "sku=caf%C3%A9", true},
		{"anchored", `^coffee-.*$`, "sku=coffee-mug", true},
		{"multiline stays whole", `(?m)^coffee$`, "sku=tea%0Acoffee", false},
		{"empty value", `.*`, "sku=", true},
		{"missing name", `.*`, "other=coffee-mug", false},
		{"case sensitive name", `.*`, "SKU=coffee-mug", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			scenario := model.MockScenario{Name: "query", Routes: []model.MockRoute{{
				Name: "lookup", Service: "inventory", Method: "GET", Path: "/items", Status: 200, Enabled: true,
				Query: map[string]model.MockQueryMatcher{"sku": {Match: "regex", Value: test.pattern}, "warehouse": {Match: "equals", Value: "central"}, "include": {Match: "exists"}},
			}}}
			compiled, err := Compile(scenario)
			if err != nil {
				t.Fatal(err)
			}
			port, err := manager.Set("store/local", "inventory", scenario)
			if err != nil {
				t.Fatal(err)
			}
			values, err := url.ParseQuery(test.query + "&warehouse=central&include=")
			if err != nil {
				t.Fatal(err)
			}
			preview, err := compiled.Preview(model.MockRequest{Service: "inventory", Method: "GET", Path: "/items", Query: values})
			if err != nil || (preview.Outcome == "mocked") != test.matched {
				t.Fatalf("preview = %#v, %v", preview, err)
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/items?"+values.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != preview.Response.Status || response.Header.Get(RouteHeader) != preview.Route {
				t.Fatalf("live status/route = %d/%s, preview = %#v", response.StatusCode, response.Header.Get(RouteHeader), preview)
			}
			delete(values, "warehouse")
			if _, err := compiled.Match("inventory", "GET", "/items", values); err == nil {
				t.Fatal("regex bypassed another required query matcher")
			}
		})
	}
}

func TestQueryOperatorsHaveDeterministicSpecificity(t *testing.T) {
	scenario := model.MockScenario{Name: "query"}
	for _, matcher := range []model.MockQueryMatcher{{Match: "exists"}, {Match: "regex", Value: "coffee-.*"}, {Match: "equals", Value: "coffee-mug"}} {
		scenario.Routes = append(scenario.Routes, model.MockRoute{Name: matcher.Match, Service: "inventory", Method: "GET", Path: "/items", Status: 200, Enabled: true, Query: map[string]model.MockQueryMatcher{"sku": matcher}})
	}
	compiled, err := Compile(scenario)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ value, route string }{{"coffee-mug", "equals"}, {"coffee-beans", "regex"}, {"tea", "exists"}, {"", "exists"}} {
		route, err := compiled.Match("inventory", "GET", "/items", url.Values{"sku": {test.value}})
		if err != nil || route.Name != test.route {
			t.Fatalf("%q = %s, %v; want %s", test.value, route.Name, err, test.route)
		}
	}
	scenario.Routes[2].Query["sku"] = model.MockQueryMatcher{Match: "regex", Value: "tea-.*"}
	if _, err := Compile(scenario); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("equal-rank regex rules were not rejected: %v", err)
	}
	scenario.Routes[2].Enabled = false
	if _, err := Compile(scenario); err != nil {
		t.Fatalf("disabled regex rule caused ambiguity: %v", err)
	}
}

func TestCompileValidatesQueryOperatorsAndRegexBounds(t *testing.T) {
	for _, matcher := range []model.MockQueryMatcher{
		{Match: "regex", Value: "["}, {Match: "regex", Value: "(?=coffee)"},
		{Match: "regex", Value: `(a)\1`}, {Match: "regex", Value: `a)\z|(?:b`},
		{Match: "regex"}, {Match: "regex", Value: strings.Repeat("x", MaxQueryPatternBytes+1)},
		{Match: "unknown", Value: "x"}, {Match: "equals"}, {Match: "exists", Value: "retained"},
	} {
		scenario := model.MockScenario{Name: "query", Routes: []model.MockRoute{{Name: "lookup", Service: "inventory", Method: "GET", Path: "/", Status: 200, Enabled: true, Query: map[string]model.MockQueryMatcher{"sku": matcher}}}}
		if _, err := Compile(scenario); err == nil || !strings.Contains(err.Error(), "query parameter sku") {
			t.Fatalf("invalid matcher %#v: %v", matcher, err)
		}
	}
	query := map[string]model.MockQueryMatcher{}
	for i := 0; i <= MaxScenarioQueryPatternBytes/MaxQueryPatternBytes; i++ {
		query[fmt.Sprintf("key-%d", i)] = model.MockQueryMatcher{Match: "regex", Value: strings.Repeat("x", MaxQueryPatternBytes)}
	}
	if _, err := Compile(model.MockScenario{Name: "query", Routes: []model.MockRoute{{Name: "lookup", Service: "inventory", Method: "GET", Path: "/", Status: 200, Enabled: true, Query: query}}}); err == nil || !strings.Contains(err.Error(), "scenario query regex patterns exceed") {
		t.Fatalf("combined pattern limit: %v", err)
	}
}
