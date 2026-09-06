package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestPreviewMockEvaluatesDraftWithoutChangingSavedOrLiveState(t *testing.T) {
	app, store := mockScenarioTestService(t)
	ctx := t.Context()
	createTestMockScenario(t, app, "preview", "inventory")
	route := model.MockRoute{Name: "lookup", Service: "inventory", Method: "GET", Path: "/inventory/{sku}", Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: `{"saved":true}`, Enabled: true}
	if _, err := app.PutMockRoute(ctx, "store", "local", "preview", route.Name, route, "test"); err != nil {
		t.Fatal(err)
	}
	literal := route
	literal.Name, literal.Path, literal.Status = "featured", "/inventory/featured", 202
	before, err := app.PutMockRoute(ctx, "store", "local", "preview", literal.Name, literal, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Keep a real matcher running while preview evaluates a different response.
	port, err := app.mocks.Set("store/local", "inventory", before)
	if err != nil {
		t.Fatal(err)
	}
	environmentBefore, err := store.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	timelineBefore, err := app.Timeline(ctx, "store", "local", 100)
	if err != nil {
		t.Fatal(err)
	}
	storageBefore, err := store.RecordingStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	subscription := app.broker.Subscribe(ctx, "store/local", nil)
	defer subscription.Close()
	request := model.MockRequest{Service: "inventory", Method: "GET", Path: "/inventory/coffee"}
	saved, err := app.PreviewMock(ctx, "store", "local", "preview", request, nil, "")
	if err != nil || saved.Body != route.Body || saved.Status != 200 {
		t.Fatalf("saved preview = %#v, %v", saved, err)
	}
	draft := route
	draft.Status, draft.Body, draft.DelayMS = 503, `{"draft":true}`, 300_000
	draft.Name = "renamed-lookup"
	preview, err := app.PreviewMock(ctx, "store", "local", "preview", request, &draft, "lookup")
	if err != nil || preview.Route != draft.Name || preview.Status != 503 || preview.Body != draft.Body || preview.DelayMS != draft.DelayMS {
		t.Fatalf("draft preview = %#v, %v", preview, err)
	}
	request.Path = "/inventory/featured"
	preview, err = app.PreviewMock(ctx, "store", "local", "preview", request, &draft, "lookup")
	if err != nil || preview.Route != "featured" || preview.Status != 202 {
		t.Fatalf("saved peer precedence = %#v, %v", preview, err)
	}
	request.Path = "/inventory/coffee"
	draft.Enabled = false
	preview, err = app.PreviewMock(ctx, "store", "local", "preview", request, &draft, "lookup")
	if err != nil || preview.Matched || preview.Status != 501 {
		t.Fatalf("disabled draft preview = %#v, %v", preview, err)
	}
	draft.Name, draft.Path, draft.Enabled = "new-route", "/new", true
	request.Path = "/new"
	preview, err = app.PreviewMock(ctx, "store", "local", "preview", request, &draft, "")
	if err != nil || preview.Route != "new-route" || preview.Body != draft.Body {
		t.Fatalf("new draft preview = %#v, %v", preview, err)
	}
	after, err := app.MockScenario(ctx, "store", "local", "preview")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("preview changed saved scenario: %#v, %v", after, err)
	}
	environmentAfter, err := store.Environment(ctx, "store", "local")
	if err != nil || !reflect.DeepEqual(environmentBefore, environmentAfter) {
		t.Fatalf("preview changed environment: %#v, %v", environmentAfter, err)
	}
	timelineAfter, err := app.Timeline(ctx, "store", "local", 100)
	if err != nil || !reflect.DeepEqual(timelineBefore, timelineAfter) {
		t.Fatalf("preview changed timeline: %#v, %v", timelineAfter, err)
	}
	storageAfter, err := store.RecordingStorage(ctx)
	if err != nil || storageAfter != storageBefore || len(app.TrafficExchanges("store", "local", 100)) != 0 {
		t.Fatalf("preview created traffic or recording data: %#v, %v", storageAfter, err)
	}
	select {
	case event := <-subscription.C:
		t.Fatalf("preview emitted an event: %#v", event)
	default:
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/inventory/coffee")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != route.Body {
		t.Fatalf("preview changed active matcher: status=%d body=%q err=%v", response.StatusCode, body, err)
	}
}

func TestPreviewMockRejectsInvalidDraftsAndRequests(t *testing.T) {
	app, _ := mockScenarioTestService(t)
	createTestMockScenario(t, app, "preview", "inventory")
	request := model.MockRequest{Service: "inventory", Method: "GET", Path: "/health"}
	route := model.MockRoute{Name: "inventory-health", Service: "inventory", Method: "GET", Path: "/health", Status: 200, Enabled: true}
	cases := []struct {
		name, original, message string
		request                 model.MockRequest
		draft                   *model.MockRoute
	}{
		{name: "original without draft", original: route.Name, message: "requires a route draft"},
		{name: "missing original", original: "deleted", draft: &route, message: "preview original route"},
		{name: "new duplicate", draft: &route, message: "duplicated"},
		{name: "ambiguous draft", draft: newRouteWith(route, func(r *model.MockRoute) { r.Name = "another" }), message: "ambiguous"},
		{name: "invalid draft service", draft: newRouteWith(route, func(r *model.MockRoute) { r.Service = "missing" }), message: "validate preview draft service"},
		{name: "invalid draft path", original: route.Name, draft: newRouteWith(route, func(r *model.MockRoute) { r.Path = "/{bad-param}" }), message: "not a valid"},
		{name: "response bound", original: route.Name, draft: newRouteWith(route, func(r *model.MockRoute) { r.Body = strings.Repeat("x", mocks.MaxResponseBodyBytes+1) }), message: "response body exceeds"},
		{name: "request service", request: model.MockRequest{Service: "missing", Method: "GET", Path: "/health"}, message: "not found"},
		{name: "request path", request: model.MockRequest{Service: "inventory", Method: "GET", Path: "https://example.test/health"}, message: "absolute URL path"},
		{name: "request body bound", request: model.MockRequest{Service: "inventory", Method: "GET", Path: "/health", Body: strings.Repeat("x", mocks.MaxPreviewRequestBodyBytes+1)}, message: "request body exceeds"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := test.request
			if input.Service == "" {
				input = request
			}
			_, err := app.PreviewMock(t.Context(), "store", "local", "preview", input, test.draft, test.original)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("preview error = %v; want %q", err, test.message)
			}
			if test.name == "missing original" && !errors.Is(err, database.ErrNotFound) {
				t.Fatalf("missing original lost not-found classification: %v", err)
			}
		})
	}
}

func TestPreviewMockWorksForEnabledScenariosWithoutChangingCoverage(t *testing.T) {
	app, _ := mockScenarioTestService(t)
	ctx := context.Background()
	createTestMockScenario(t, app, "preview", "inventory")
	operation, err := app.SetMockScenarioEnabled(ctx, "store", "local", "preview", true, "test", "enable-preview")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("enable = %#v", operation)
	}
	draft := model.MockRoute{Name: "inventory-health", Service: "payments", Method: "GET", Path: "/health", Status: 202, Enabled: true}
	preview, err := app.PreviewMock(ctx, "store", "local", "preview", model.MockRequest{Service: "payments", Method: "GET", Path: "/health"}, &draft, draft.Name)
	if err != nil || !preview.Matched || preview.Service != "payments" || preview.Status != 202 {
		t.Fatalf("active scenario draft = %#v, %v", preview, err)
	}
	scenario, err := app.MockScenario(ctx, "store", "local", "preview")
	if err != nil || scenario.Activation.State != model.MockScenarioEnabled || !reflect.DeepEqual(scenario.Activation.TargetServices, []string{"inventory"}) {
		t.Fatalf("preview changed active coverage: %#v, %v", scenario, err)
	}
}

func newRouteWith(route model.MockRoute, change func(*model.MockRoute)) *model.MockRoute {
	change(&route)
	return &route
}
