package controlplane

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestPartialScenarioOwnershipCloneAndProviderPreservation(t *testing.T) {
	app, store := mockScenarioTestService(t)
	createTestPartialMockScenario(t, app, "partial", "inventory")
	ctx := t.Context()
	scenario, _ := app.MockScenario(ctx, "store", "local", "partial")
	before, _ := store.Environment(ctx, "store", "local")
	op, err := app.SetMockScenarioEnabled(ctx, "store", "local", "partial", true, "test", "enable-partial")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "succeeded" {
		t.Fatalf("enable=%#v", op)
	}
	after, _ := app.Environment(ctx, "store", "local")
	if !reflect.DeepEqual(before.Bindings, after.Bindings) {
		t.Fatal("partial activation rewrote provider bindings")
	}
	scenario, _ = app.MockScenario(ctx, "store", "local", "partial")
	if scenario.Activation.State != model.MockScenarioEnabled || scenario.UnmatchedRequests != model.MockUnmatchedForward {
		t.Fatalf("scenario=%#v", scenario)
	}
	meta, _, _, err := app.InspectMockScenario(ctx, "store", "local", "partial", "", 0, 10, false)
	if err != nil || !reflect.DeepEqual(meta.Activation, scenario.Activation) || meta.UnmatchedRequests != model.MockUnmatchedForward || meta.Version != scenario.Version {
		t.Fatalf("metadata=%#v %v", meta, err)
	}
	if _, err = app.CloneEnvironment(ctx, "store", "local", "clone", "test"); err != nil {
		t.Fatal(err)
	}
	cloned, err := app.MockScenario(ctx, "store", "clone", "partial")
	if err != nil || cloned.UnmatchedRequests != model.MockUnmatchedForward || cloned.Activation.State != model.MockScenarioEnabled {
		t.Fatalf("clone=%#v %v", cloned, err)
	}
	if app.proxy.PartialMockApplied("store/clone", "inventory", "partial") {
		t.Fatal("clone copied a runtime policy")
	}
	op, err = app.SetMockScenarioEnabled(ctx, "store", "local", "partial", false, "test", "disable-partial")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "succeeded" {
		t.Fatalf("disable=%#v", op)
	}
	after, _ = store.Environment(ctx, "store", "local")
	if !reflect.DeepEqual(before.Bindings, after.Bindings) {
		t.Fatal("partial disable reapplied baseline bindings")
	}
}

func TestPartialLiveActivationPreviewAndRecovery(t *testing.T) {
	app, store := mockScenarioTestService(t)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write([]byte("real")) }))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	app.proxy.SetTarget("store/local", "inventory", port)
	if err := store.SetEnvironmentStatus(t.Context(), "store", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	createTestPartialMockScenario(t, app, "partial", "inventory")
	op, err := app.SetMockScenarioEnabled(t.Context(), "store", "local", "partial", true, "test", "live")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "succeeded" {
		t.Fatalf("enable=%#v", op)
	}
	for _, test := range []struct{ path, outcome string }{{"/health", "mocked"}, {"/other", "forward"}} {
		preview, err := app.PreviewMock(t.Context(), "store", "local", "partial", model.MockRequest{Service: "inventory", Method: "GET", Path: test.path}, nil, "")
		if err != nil || preview.Outcome != test.outcome {
			t.Fatalf("preview=%#v %v", preview, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("preview or activation contacted upstream")
	}
	environment, _ := app.Environment(t.Context(), "store", "local")
	for _, service := range environment.Services {
		if service.Name == "inventory" && (service.Mock == nil || service.Mock.UnmatchedRequests != model.MockUnmatchedForward) {
			t.Fatalf("service context=%#v", service)
		}
	}
	app.proxy.RemovePartialMock("store/local", "partial")
	scenario, _ := app.MockScenario(t.Context(), "store", "local", "partial")
	if scenario.Activation.State != model.MockScenarioDegraded {
		t.Fatal("missing runtime policy appeared enabled")
	}
	if err := app.restorePartialMocks(t.Context(), environment); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	app.ServeIngress(recorder, httptest.NewRequest("GET", "/health", nil), "store/local", "inventory")
	if recorder.Code != 503 || calls.Load() != 0 {
		t.Fatal("recovery failed to restore fixed response")
	}
}

func TestPartialPreviewRemotePolicyAndUpgradeDecisions(t *testing.T) {
	app, _ := mockScenarioTestService(t)
	createTestPartialMockScenario(t, app, "partial", "payments")
	draft := model.MockRoute{Name: "create", Service: "payments", Method: "POST", Path: "/payments", Status: 201, Enabled: true}
	for _, test := range []struct {
		method, path, outcome string
		draft                 *model.MockRoute
	}{{"POST", "/payments", "mocked", &draft}, {"POST", "/unmatched", "blocked", nil}, {"GET", "/unmatched", "forward", nil}, {"TRACE", "/unmatched", "forward", nil}} {
		preview, err := app.PreviewMock(t.Context(), "store", "local", "partial", model.MockRequest{Service: "payments", Method: test.method, Path: test.path}, test.draft, "")
		if err != nil || preview.Outcome != test.outcome {
			t.Fatalf("preview=%#v %v", preview, err)
		}
	}
	headers := map[string][]string{"Connection": {"upgrade"}, "Upgrade": {"websocket"}, "Sec-WebSocket-Version": {"13"}, "Sec-WebSocket-Key": {"dGhlIHNhbXBsZSBub25jZQ=="}}
	preview, err := app.PreviewMock(t.Context(), "store", "local", "partial", model.MockRequest{Service: "payments", Method: "GET", Path: "/health", Headers: headers}, nil, "")
	if err != nil || preview.Outcome != "blocked" {
		t.Fatalf("WS preview=%#v %v", preview, err)
	}
	headers["Sec-WebSocket-Version"] = []string{"12"}
	preview, err = app.PreviewMock(t.Context(), "store", "local", "partial", model.MockRequest{Service: "payments", Method: "GET", Path: "/health", Headers: headers}, nil, "")
	if err != nil || preview.Outcome != "blocked" {
		t.Fatalf("invalid WS preview=%#v %v", preview, err)
	}
}

func TestPartialCorruptRecoveryGuardsTrafficAndCanBeDisabled(t *testing.T) {
	app, store := mockScenarioTestService(t)
	ctx := t.Context()
	createTestPartialMockScenario(t, app, "partial", "inventory")
	op, err := app.SetMockScenarioEnabled(ctx, "store", "local", "partial", true, "test", "enable")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "succeeded" {
		t.Fatalf("enable=%#v", op)
	}
	scenario, _ := store.MockScenario(ctx, "store", "local", "partial")
	route := scenario.Routes[0]
	route.Path = "/{invalid-param}"
	if _, err := store.PutMockRoute(ctx, "store", "local", "partial", route); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnvironmentStatus(ctx, "store", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServiceStatus(ctx, "store/local", "inventory", model.ServiceReady, ""); err != nil {
		t.Fatal(err)
	}
	app.proxy.RemovePartialMock("store/local", "partial")
	environment, _ := store.Environment(ctx, "store", "local")
	if err := app.restorePartialMocks(ctx, environment); err == nil {
		t.Fatal("corrupt saved route restored successfully")
	}
	response := httptest.NewRecorder()
	app.ServeIngress(response, httptest.NewRequest("GET", "/unmatched", nil), "store/local", "inventory")
	if response.Code != 503 || !strings.Contains(response.Body.String(), "MOCK_POLICY_UNAVAILABLE") {
		t.Fatalf("recovery guard=%d %s", response.Code, response.Body.String())
	}
	scenario, _ = app.MockScenario(ctx, "store", "local", "partial")
	if scenario.Activation.State != model.MockScenarioDegraded {
		t.Fatalf("corrupt activation=%#v", scenario.Activation)
	}
	op, err = app.SetMockScenarioEnabled(ctx, "store", "local", "partial", false, "test", "disable-corrupt")
	if err != nil {
		t.Fatal(err)
	}
	if op = waitForOperation(t, app, op); op.State != "succeeded" {
		t.Fatalf("disable=%#v", op)
	}
	scenario, _ = app.MockScenario(ctx, "store", "local", "partial")
	if scenario.Activation.State != model.MockScenarioDisabled {
		t.Fatalf("disabled activation=%#v", scenario.Activation)
	}
}

func createTestPartialMockScenario(t *testing.T, app *Service, name string, services ...string) {
	t.Helper()
	if _, err := app.CreateMockScenario(t.Context(), "store", "local", model.MockScenario{Name: name, UnmatchedRequests: model.MockUnmatchedForward}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		route := model.MockRoute{Name: service + "-health", Service: service, Method: "GET", Path: "/health", Status: 503, Enabled: true}
		if _, err := app.PutMockRoute(t.Context(), "store", "local", name, route.Name, route, "test"); err != nil {
			t.Fatal(err)
		}
	}
}
