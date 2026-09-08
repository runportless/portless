package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
)

func installPartial(t *testing.T, manager *Manager, routes ...model.MockRoute) {
	t.Helper()
	scenario := model.MockScenario{Name: "partial", UnmatchedRequests: model.MockUnmatchedForward, Routes: routes}
	compiled, err := mocks.Compile(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SetPartialMock("billing/local", "partial", []string{"orders"}, compiled); err != nil {
		t.Fatal(err)
	}
}

func TestPartialMocksForwardOnlyTypedMissesAndPreserveRequests(t *testing.T) {
	var calls atomic.Int64
	manager, _, traffic, upstream := replayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.RequestURI != "/base/a%2Fb?tag=one&tag=&space=+&space=%20" || string(body) != "streamed body" {
			t.Errorf("request changed: %s %s %q", r.Method, r.RequestURI, body)
		}
		w.Header().Set(mocks.ScenarioHeader, "spoofed")
		w.Header().Set(mocks.RouteHeader, "spoofed")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "real")
	})
	remote := model.RemoteTarget{URL: upstream.URL + "/base", Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite}
	if err := manager.SetRemoteTarget("billing/local", "orders", remote); err != nil {
		t.Fatal(err)
	}
	installPartial(t, manager, model.MockRoute{Name: "failure", Service: "orders", Method: "POST", Path: "/mock", Status: 503, Body: "fixed failure", Enabled: true})
	before, _ := manager.target("billing/local", "orders")
	for _, test := range []struct {
		path          string
		status        int
		body, outcome string
		provider      model.ProviderKind
	}{
		{"/mock", 503, "fixed failure", "mocked", model.ProviderMock},
		{"/a%2Fb?tag=one&tag=&space=+&space=%20", 200, "real", "forwarded", model.ProviderRemote},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "http://orders.local.billing.localhost"+test.path, strings.NewReader("streamed body"))
		request.Header.Set("Content-Type", "text/plain")
		manager.forwardHTTP(recorder, request, "billing/local", "checkout", "orders")
		if recorder.Code != test.status || recorder.Body.String() != test.body {
			t.Fatalf("response %d %q", recorder.Code, recorder.Body.String())
		}
		exchanges := traffic.RecentExchanges("billing/local", 1)
		if len(exchanges) != 1 || exchanges[0].MockOutcome != test.outcome || exchanges[0].TargetProvider != test.provider || exchanges[0].Source != "checkout" || exchanges[0].MockScenario != "partial" {
			t.Fatalf("attribution %#v", exchanges)
		}
		if test.outcome == "forwarded" && exchanges[0].MockRoute != "" {
			t.Fatal("upstream spoofed route attribution")
		}
	}
	if calls.Load() != 1 || len(traffic.RecentExchanges("billing/local", 10)) != 2 {
		t.Fatal("requests dispatched or captured more than once")
	}
	manager.RemovePartialMock("billing/local", "partial")
	after, _ := manager.target("billing/local", "orders")
	if before.generation != after.generation || before.address != after.address {
		t.Fatal("policy changed the real target")
	}
}

func TestPartialRemoteReadOnlyAppliesOnlyAfterMiss(t *testing.T) {
	var calls atomic.Int64
	manager, _, traffic, upstream := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) })
	remote := model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}
	if err := manager.SetRemoteTarget("billing/local", "orders", remote); err != nil {
		t.Fatal(err)
	}
	installPartial(t, manager, model.MockRoute{Name: "create", Service: "orders", Method: "POST", Path: "/mock", Status: 201, Enabled: true})
	for _, test := range []struct {
		path    string
		status  int
		outcome string
	}{{"/mock", 201, "mocked"}, {"/real", 403, "blocked"}} {
		recorder := httptest.NewRecorder()
		manager.ServeIngress(recorder, httptest.NewRequest("POST", test.path, nil), "billing/local", "orders")
		if recorder.Code != test.status {
			t.Fatalf("status=%d", recorder.Code)
		}
		if got := traffic.RecentExchanges("billing/local", 1)[0]; got.MockOutcome != test.outcome {
			t.Fatalf("exchange=%#v", got)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("remote write escaped")
	}
	exchange, outcome, err := runReplay(t, manager, t.Context(), "POST", "/mock", nil, "")
	if err != nil || outcome != "response-received" || exchange.Status != 201 || exchange.MockOutcome != "mocked" {
		t.Fatalf("mock replay=%#v %s %v", exchange, outcome, err)
	}
}

func TestPartialMocksRetainCrashPolicyButCloseExplicitAdmission(t *testing.T) {
	manager, _, _, upstream := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	installPartial(t, manager, model.MockRoute{Name: "fixed", Service: "orders", Method: "GET", Path: "/mock", Status: 202, Enabled: true})
	assert := func(path string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		manager.ServeIngress(w, httptest.NewRequest("GET", path, nil), "billing/local", "orders")
		if w.Code != status {
			t.Fatalf("%s: %d != %d", path, w.Code, status)
		}
		if status == http.StatusServiceUnavailable && (w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), `"code":"MOCK_POLICY_UNAVAILABLE"`)) {
			t.Fatalf("unstructured guard response: %s", w.Body.String())
		}
	}
	manager.RemoveTarget("billing/local", "orders")
	assert("/mock", 202)
	assert("/real", 502)
	manager.StopTarget("billing/local", "orders")
	assert("/mock", 502)
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	manager.SetTarget("billing/local", "orders", port)
	assert("/mock", 202)
	if err := manager.SetPartialMock("billing/local", "partial", []string{"orders"}, nil); err != nil {
		t.Fatal(err)
	}
	assert("/mock", 503)
	assert("/real", 503)
	manager.CloseEnvironment(context.Background(), "billing/local")
	if manager.PartialMockApplied("billing/local", "orders", "partial") || len(manager.partialMocks) != 0 {
		t.Fatal("environment retained mock runtime")
	}
}

func TestPartialTerminalFaultDoesNotClaimAMockDecision(t *testing.T) {
	var calls atomic.Int64
	manager, db, traffic, _ := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) })
	installPartial(t, manager, model.MockRoute{Name: "fixed", Service: "orders", Method: "GET", Path: "/mock", Status: 202, Enabled: true})
	if _, err := db.CreateFault(t.Context(), model.FaultRule{Project: "billing", Environment: "local", Name: "failure", Source: "checkout", Target: "orders", Probability: 1, StatusCode: 503}); err != nil {
		t.Fatal(err)
	}
	for _, upgrade := range []bool{false, true} {
		request := httptest.NewRequest("GET", "/mock", nil)
		if upgrade {
			request.Header.Set("Connection", "Upgrade")
			request.Header.Set("Upgrade", "websocket")
			request.Header.Set("Sec-WebSocket-Version", "13")
			request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		}
		manager.forwardHTTP(httptest.NewRecorder(), request, "billing/local", "checkout", "orders")
		exchange := traffic.RecentExchanges("billing/local", 1)[0]
		if exchange.Status != 503 || exchange.Fault != "failure" || exchange.MockOutcome != "" || exchange.MockScenario != "" {
			t.Fatalf("fault attribution=%#v", exchange)
		}
	}
	exchange, _, err := runReplay(t, manager, t.Context(), "GET", "/mock", nil, "")
	if err != nil || exchange.Status != 503 || exchange.MockOutcome != "" || exchange.MockScenario != "" || calls.Load() != 0 {
		t.Fatalf("replay fault=%#v %v calls=%d", exchange, err, calls.Load())
	}
}

func TestPartialRoutePublicationHasNoForwardingGap(t *testing.T) {
	var calls atomic.Int64
	manager, _, _, _ := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) })
	route := model.MockRoute{Name: "fixed", Service: "orders", Method: "GET", Path: "/mock", Status: 202, Enabled: true}
	installPartial(t, manager, route)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 100 {
				response := httptest.NewRecorder()
				manager.ServeIngress(response, httptest.NewRequest("GET", "/mock", nil), "billing/local", "orders")
				if response.Code != 202 && response.Code != 203 {
					t.Errorf("incomplete routing decision: %d", response.Code)
					return
				}
			}
		})
	}
	for index := range 100 {
		route.Status = 202 + index%2
		installPartial(t, manager, route)
	}
	workers.Wait()
	if calls.Load() != 0 {
		t.Fatalf("%d requests escaped during publication", calls.Load())
	}
}

func TestPartialReplayRejectsStaleRouteDecision(t *testing.T) {
	var calls atomic.Int64
	manager, _, _, _ := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) })
	route := model.MockRoute{Name: "create", Service: "orders", Method: "POST", Path: "/orders", Status: 201, Enabled: true}
	installPartial(t, manager, route)
	selected, err := manager.ReplayTarget("billing/local", "orders", model.ComponentBinding{Provider: model.ProviderLocal}, "POST", "/orders")
	if err != nil || selected.Provider != model.ProviderMock || selected.MockScenario != "partial" || selected.MockRoute != "create" {
		t.Fatalf("prepared=%#v %v", selected, err)
	}
	route.Enabled = false
	installPartial(t, manager, route)
	_, outcome, err := manager.ReplayHTTP(t.Context(), "billing/local", "checkout", "orders", "POST", "/orders", nil, "", selected.Generation, selected.RoutingRevision, model.TrafficReplay{})
	if err == nil || outcome != "not-sent" || calls.Load() != 0 {
		t.Fatalf("stale replay=%s %v calls=%d", outcome, err, calls.Load())
	}
}

func TestPartialWebSocketReadOnlyRejectionIsAttributedWithoutHTTPMatching(t *testing.T) {
	manager, _, traffic, upstream := replayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("WebSocket reached read-only upstream") })
	if err := manager.SetRemoteTarget("billing/local", "orders", model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}); err != nil {
		t.Fatal(err)
	}
	installPartial(t, manager, model.MockRoute{Name: "ws-shaped", Service: "orders", Method: "GET", Path: "/ws", Status: 200, Enabled: true})
	request := httptest.NewRequest("GET", "/ws", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	response := httptest.NewRecorder()
	manager.ServeIngress(response, request, "billing/local", "orders")
	if response.Code != 403 {
		t.Fatalf("WS=%d", response.Code)
	}
	exchange := traffic.RecentExchanges("billing/local", 1)[0]
	if exchange.MockOutcome != "blocked" || exchange.MockScenario != "partial" || exchange.MockRoute != "" || exchange.TargetProvider != model.ProviderRemote {
		t.Fatalf("WS attribution=%#v", exchange)
	}
}
