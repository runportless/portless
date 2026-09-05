package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/networking"
	trafficstore "github.com/runportless/portless/portless-daemon/traffic"
	"github.com/runportless/portless/portless-daemon/traffic/protocol"
)

func TestIngressTrafficCaptureRecordingAndFaultAreEnvironmentScoped(t *testing.T) {
	ctx := context.Background()
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	var upstreamRequestBody string
	var upstreamTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		upstreamRequestBody = string(body)
		upstreamTraceparent = request.Header.Get("Traceparent")
		writer.Header().Add("X-Upstream", "checkout")
		writer.Header().Add("X-Upstream", "orders")
		writer.Header().Set("Set-Cookie", "session=should-not-leak")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"created":true}`))
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	manager.SetTarget(scope, "checkout", port)

	request := httptest.NewRequest(http.MethodPost, "http://checkout.local.billing.localhost/orders/%2Fitem?sku=coffee%20mug&quantity=2", strings.NewReader(`{"sku":"coffee"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer should-not-leak")
	request.Header.Set("X-Trace", "visible")
	request.Header.Add("X-Trace", "also-visible")
	request.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	manager.ServeIngress(response, request, scope, "checkout")
	if response.Code != http.StatusCreated || len(response.Header().Values("X-Upstream")) != 2 || upstreamRequestBody != `{"sku":"coffee"}` {
		t.Fatalf("response code=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	exchanges := trafficStore.RecentExchanges(scope, 10)
	if len(exchanges) != 1 || exchanges[0].Project != "billing" || exchanges[0].Environment != "local" || exchanges[0].RequestTarget != "/orders/%2Fitem?sku=coffee%20mug&quantity=2" || exchanges[0].TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || exchanges[0].ParentSpanID != "00f067aa0ba902b7" || upstreamTraceparent != "00-"+exchanges[0].TraceID+"-"+exchanges[0].SpanID+"-01" || firstHeader(exchanges[0].RequestHeaders, "Authorization") != "[REDACTED]" || len(exchanges[0].RequestHeaders["X-Trace"]) != 2 || firstHeader(exchanges[0].RequestHeaders, "X-Trace") != "visible" || firstHeader(exchanges[0].ResponseHeaders, "Set-Cookie") != "[REDACTED]" || len(exchanges[0].ResponseHeaders["X-Upstream"]) != 2 || firstHeader(exchanges[0].ResponseHeaders, "X-Upstream") != "checkout" || exchanges[0].RequestBody != `{"sku":"coffee"}` || exchanges[0].ResponseBody != `{"created":true}` {
		t.Fatalf("unexpected traffic %#v", exchanges)
	}
	if exchanges[0].TraceContextSource != model.TrafficTraceContextW3C {
		t.Fatalf("unexpected propagated trace metadata: %#v", exchanges[0])
	}

	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "checkout-debug"}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/recorded", nil), scope, "checkout")
	recorded, err := controlStore.RecordedTraffic(ctx, scope, "checkout-debug", 10)
	if err != nil || len(recorded) != 1 || recorded[0].Recording != "checkout-debug" || recorded[0].RequestBody != "" || recorded[0].ResponseBody != "" || recorded[0].TraceContextSource != model.TrafficTraceContextGenerated || recorded[0].ParentSpanID != "" {
		t.Fatalf("recorded traffic=%#v err=%v", recorded, err)
	}

	if _, err := controlStore.CreateFault(ctx, model.FaultRule{Project: "billing", Environment: "local", Name: "checkout-down", Source: "external", Target: "checkout", StatusCode: http.StatusServiceUnavailable, Probability: 1}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/faulted", nil), scope, "checkout")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("X-Portless-Fault") != "checkout-down" {
		t.Fatalf("fault response code=%d headers=%v", response.Code, response.Header())
	}
	faulted := trafficStore.RecentExchanges(scope, 1)
	if len(faulted) != 1 || faulted[0].TraceContextSource != model.TrafficTraceContextGenerated {
		t.Fatalf("faulted request unexpectedly reported forwarded trace context: %#v", faulted)
	}
}

func TestPortlessInjectedTraceContextRemainsInternalToHeaderCapture(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("store", "local")
	var upstreamTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamTraceparent = request.Header.Get("Traceparent")
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	manager.SetTarget(scope, "checkout", port)

	manager.ServeIngress(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://checkout.local.store.localhost/root", nil), scope, "checkout")
	if upstreamTraceparent == "" {
		t.Fatal("Portless did not inject a root traceparent")
	}
	downstream := httptest.NewRequest(http.MethodGet, "http://checkout.local.store.localhost/downstream", nil)
	downstream.Header.Set("Traceparent", upstreamTraceparent)
	downstream.Header.Set("Tracestate", "vendor=value")
	downstream.Header.Set("X-Request-Id", "application-value")
	manager.ServeIngress(httptest.NewRecorder(), downstream, scope, "checkout")

	exchanges := trafficStore.RecentExchanges(scope, 2)
	if len(exchanges) != 2 || exchanges[0].TraceContextSource != model.TrafficTraceContextPortless {
		t.Fatalf("downstream context was not recognized as Portless: %#v", exchanges)
	}
	if firstHeader(exchanges[0].RequestHeaders, "Traceparent") != "" {
		t.Fatalf("Portless traceparent leaked into captured headers: %#v", exchanges[0].RequestHeaders)
	}
	if firstHeader(exchanges[0].RequestHeaders, "Tracestate") != "vendor=value" || firstHeader(exchanges[0].RequestHeaders, "X-Request-Id") != "application-value" {
		t.Fatalf("application headers were removed: %#v", exchanges[0].RequestHeaders)
	}
}

func TestCaptureRequestHeadersRemovesOnlyMatchingPortlessCarrierFormats(t *testing.T) {
	headers := http.Header{
		"Traceparent":         {"00-11111111111111111111111111111111-2222222222222222-01"},
		"B3":                  {"33333333333333333333333333333333-4444444444444444-1"},
		"X-B3-TraceId":        {"55555555555555555555555555555555"},
		"X-B3-SpanId":         {"6666666666666666"},
		"X-Datadog-Trace-Id":  {"7"},
		"X-Datadog-Parent-Id": {"8"},
		"X-Datadog-Tags":      {"_dd.p.dm=-0"},
		"X-B3-Sampled":        {"1"},
		"X-Application-Trace": {"visible"},
	}
	captured := captureRequestHeaders(headers, tracePropagationW3C|tracePropagationB3Multi)
	if firstHeader(captured, "Traceparent") != "" || firstHeader(captured, "X-B3-TraceId") != "" || firstHeader(captured, "X-B3-SpanId") != "" {
		t.Fatalf("Portless carriers were retained: %#v", captured)
	}
	for _, name := range []string{"B3", "X-Datadog-Trace-Id", "X-Datadog-Parent-Id", "X-Datadog-Tags", "X-B3-Sampled", "X-Application-Trace"} {
		if firstHeader(captured, name) == "" {
			t.Fatalf("application header %s was removed: %#v", name, captured)
		}
	}
}

func TestHTTPBodyCaptureIsBoundedWithoutChangingForwardedPayloads(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	requestContent := strings.Repeat("r", trafficBodyLimit+128)
	responseContent := strings.Repeat("s", trafficBodyLimit+256)
	var received int
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		received = len(body)
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte(responseContent))
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	manager.SetTarget(scope, "checkout", port)

	request := httptest.NewRequest(http.MethodPost, "http://checkout.local.billing.localhost/upload", strings.NewReader(requestContent))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	manager.ServeIngress(response, request, scope, "checkout")

	exchanges := trafficStore.RecentExchanges(scope, 1)
	if received != len(requestContent) || response.Body.String() != responseContent {
		t.Fatalf("proxy changed payload lengths: request=%d response=%d", received, response.Body.Len())
	}
	if len(exchanges) != 1 || len(exchanges[0].RequestBody) != trafficBodyLimit || len(exchanges[0].ResponseBody) != trafficBodyLimit || !exchanges[0].RequestBodyTruncated || !exchanges[0].ResponseBodyTruncated {
		t.Fatalf("unexpected bounded capture: %#v", exchanges)
	}
}

func TestHTTPBodyCaptureSkipsBinaryContent(t *testing.T) {
	if inspectableBody("application/octet-stream") {
		t.Fatal("binary content should not be captured")
	}
	for _, contentType := range []string{"application/json", "application/problem+json; charset=utf-8", "text/plain", ""} {
		if !inspectableBody(contentType) {
			t.Fatalf("text content %q should be captured", contentType)
		}
	}
}

func TestMockProviderMetadataIsCapturedButNotExposed(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set(mocks.ScenarioHeader, "sold-out")
		writer.Header().Set(mocks.RouteHeader, "lookup")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"available":false}`))
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	manager.SetTargetProvider(scope, "inventory", port, model.ProviderMock)

	response := httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://inventory.local.billing.localhost/inventory/coffee", nil), scope, "inventory")
	if response.Code != http.StatusOK || response.Header().Get(mocks.ScenarioHeader) != "" || response.Header().Get(mocks.RouteHeader) != "" {
		t.Fatalf("public response = %d %#v", response.Code, response.Header())
	}
	exchanges := trafficStore.RecentExchanges(scope, 1)
	if len(exchanges) != 1 || exchanges[0].TargetProvider != model.ProviderMock || exchanges[0].MockScenario != "sold-out" || exchanges[0].MockRoute != "lookup" {
		t.Fatalf("exchange = %#v", exchanges)
	}
}

func TestRecordingBodyLimitCanExceedLiveTrafficLimit(t *testing.T) {
	ctx := context.Background()
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	content := strings.Repeat("x", trafficBodyLimit+1024)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte(content))
	}))
	defer upstream.Close()
	parsed, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(parsed.Port())
	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	manager.SetTarget(scope, "checkout", port)
	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "mock-source", CapturePayloads: true, MaxPayloadBytes: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/inventory", nil), scope, "checkout")
	recorded, err := controlStore.RecordedTraffic(ctx, scope, "mock-source", 1)
	if err != nil || len(recorded) != 1 || recorded[0].ResponseBody != content || recorded[0].ResponseBodyTruncated {
		t.Fatalf("recorded = %#v, err = %v", recorded, err)
	}
	live := trafficStore.RecentExchanges(scope, 1)
	if len(live) != 1 || len(live[0].ResponseBody) != trafficBodyLimit || !live[0].ResponseBodyTruncated || live[0].ResponseCapturedBytes != trafficBodyLimit {
		t.Fatalf("live exchange exceeded its independent body limit: %#v", live)
	}
}

func TestMissingTargetReturnsBadGateway(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	manager := newManagerForTest(controlStore)

	missing := httptest.NewRecorder()
	manager.ServeIngress(missing, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/health", nil), scope, "checkout")
	if missing.Code != http.StatusBadGateway || missing.Header().Get("Retry-After") != "" || !strings.Contains(missing.Body.String(), "not available") {
		t.Fatalf("ordinary missing target = %d headers=%v body=%q", missing.Code, missing.Header(), missing.Body.String())
	}
}

func TestDependencyProxyCanBeRestoredAtItsPersistedPort(t *testing.T) {
	controlStore, err := database.Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	connection := model.Connection{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP}
	first := newManagerForTest(controlStore)
	port, err := first.EnsureEdge(context.Background(), "billing/local", connection)
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasEdge("billing/local", "checkout", "orders", port) {
		t.Fatal("first manager did not report its edge")
	}
	if first.ListenerCount() != 1 {
		t.Fatalf("listener count = %d, want 1", first.ListenerCount())
	}
	first.CloseEnvironment(context.Background(), "billing/local")
	if first.ListenerCount() != 0 {
		t.Fatalf("listener count after close = %d, want 0", first.ListenerCount())
	}
	second := newManagerForTest(controlStore)
	defer second.Close(context.Background())
	restored, err := second.EnsureEdgeAtPort(context.Background(), "billing/local", connection, port)
	if err != nil {
		t.Fatal(err)
	}
	if restored != port || !second.HasEdge("billing/local", "checkout", "orders", port) {
		t.Fatalf("restored port = %d, want %d", restored, port)
	}
}

func TestTCPEdgeOutlivesTheOperationContextThatCreatedIt(t *testing.T) {
	controlStore, err := database.Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()

	manager := newManagerForTest(controlStore)
	defer manager.Close(context.Background())
	manager.SetTarget("billing/local", "redis", upstream.Addr().(*net.TCPAddr).Port)
	operationContext, cancelOperation := context.WithCancel(context.Background())
	port, err := manager.EnsureEdge(operationContext, "billing/local", model.Connection{Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP})
	if err != nil {
		t.Fatal(err)
	}
	cancelOperation()

	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "ping" {
		t.Fatalf("TCP proxy response = %q", response)
	}
}

func TestStableTCPEdgesShareTheConventionalPortAndKeepSourceFaultsIsolated(t *testing.T) {
	ctx := context.Background()
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			connection, acceptErr := upstream.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()

	firstIP, secondIP := networking.EndpointLoopbackIP(0), networking.EndpointLoopbackIP(1)
	probe, err := net.Listen("tcp", net.JoinHostPort(firstIP, "0"))
	if err != nil {
		t.Skipf("Portless loopback pool is not provisioned on this host: %v", err)
	}
	sharedPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	checkoutAddress := net.JoinHostPort(firstIP, strconv.Itoa(sharedPort))
	ordersAddress := net.JoinHostPort(secondIP, strconv.Itoa(sharedPort))

	manager := newManagerForTest(controlStore)
	defer manager.Close(ctx)
	manager.SetTarget(scope, "redis", upstream.Addr().(*net.TCPAddr).Port)
	if _, err := manager.EnsureEdgeAtAddress(ctx, scope, model.Connection{Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP}, checkoutAddress); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.EnsureEdgeAtAddress(ctx, scope, model.Connection{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP}, ordersAddress); err != nil {
		t.Fatal(err)
	}
	if !manager.HasEdgeAtAddress(scope, "checkout", "redis", checkoutAddress) || !manager.HasEdgeAtAddress(scope, "orders", "redis", ordersAddress) {
		t.Fatal("source-specific TCP edges were not bound to their stable addresses")
	}
	if _, err := controlStore.CreateFault(ctx, model.FaultRule{Project: "billing", Environment: "local", Name: "checkout-redis-down", Source: "checkout", Target: "redis", Abort: true, Probability: 1}); err != nil {
		t.Fatal(err)
	}

	rejected, err := net.DialTimeout("tcp", checkoutAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = rejected.SetDeadline(time.Now().Add(time.Second))
	_, _ = rejected.Write([]byte("ping"))
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(rejected, buffer); err == nil {
		t.Fatal("faulted checkout edge unexpectedly reached Redis")
	}
	_ = rejected.Close()

	allowed, err := net.DialTimeout("tcp", ordersAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	_ = allowed.SetDeadline(time.Now().Add(time.Second))
	if _, err := allowed.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(allowed, buffer); err != nil || string(buffer) != "pong" {
		t.Fatalf("unfaulted orders edge did not reach Redis: response=%q err=%v", buffer, err)
	}
}

func TestTCPFaultAppliesOnlyToItsDirectedSourceEdge(t *testing.T) {
	ctx := context.Background()
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			connection, acceptErr := upstream.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = io.Copy(connection, connection)
			}()
		}
	}()

	manager := newManagerForTest(controlStore)
	defer manager.Close(ctx)
	manager.SetTarget(scope, "redis", upstream.Addr().(*net.TCPAddr).Port)
	checkoutPort, err := manager.EnsureEdge(ctx, scope, model.Connection{Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP})
	if err != nil {
		t.Fatal(err)
	}
	ordersPort, err := manager.EnsureEdge(ctx, scope, model.Connection{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateFault(ctx, model.FaultRule{Project: "billing", Environment: "local", Name: "checkout-redis-down", Source: "checkout", Target: "redis", Abort: true, Probability: 1}); err != nil {
		t.Fatal(err)
	}

	rejected, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(checkoutPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = rejected.SetDeadline(time.Now().Add(time.Second))
	_, _ = rejected.Write([]byte("ping"))
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(rejected, buffer); err == nil {
		t.Fatal("faulted checkout edge unexpectedly reached Redis")
	}
	_ = rejected.Close()

	allowed, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ordersPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer allowed.Close()
	_ = allowed.SetDeadline(time.Now().Add(time.Second))
	if _, err := allowed.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(allowed, buffer); err != nil || string(buffer) != "pong" {
		t.Fatalf("unfaulted orders edge did not reach Redis: response=%q err=%v", buffer, err)
	}
}

func TestTCPEdgePublishesActivityBeforeTheConnectionCloses(t *testing.T) {
	controlStore, err := database.Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()

	broker := events.NewBroker()
	manager := NewManager(controlStore, trafficstore.NewStore(broker), broker)
	defer manager.Close(context.Background())
	scope := model.EnvironmentSelector("billing", "local")
	subscription := broker.Subscribe(context.Background(), scope, []string{"traffic.tcp.activity"})
	defer subscription.Close()
	manager.SetTarget(scope, "redis", upstream.Addr().(*net.TCPAddr).Port)
	port, err := manager.EnsureEdge(context.Background(), scope, model.Connection{Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-subscription.C:
			activity, ok := event.Data.(model.TrafficActivity)
			if ok && activity.Phase == "data" && activity.ActiveConnections == 1 && activity.RequestBytes >= 4 && activity.ResponseBytes >= 4 {
				return
			}
		case <-deadline:
			t.Fatal("TCP byte activity was not published while the connection remained open")
		}
	}
}

func TestRedisOperationIsCapturedBeforeThePooledConnectionCloses(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	release := make(chan struct{})
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		request := make([]byte, len("*1\r\n$4\r\nPING\r\n"))
		if _, readErr := io.ReadFull(connection, request); readErr != nil {
			return
		}
		_, _ = connection.Write([]byte("+PONG\r\n"))
		<-release
	}()

	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	defer manager.Close(context.Background())
	scope := model.EnvironmentSelector("billing", "local")
	manager.SetTarget(scope, "redis", upstream.Addr().(*net.TCPAddr).Port)
	port, err := manager.EnsureEdge(context.Background(), scope, model.Connection{
		Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP, ApplicationProtocol: model.ApplicationProtocolRedis,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("+PONG\r\n"))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exchanges := trafficStore.RecentExchanges(scope, 10)
		if len(exchanges) > 0 {
			exchange := exchanges[0]
			if exchange.TCP == nil || exchange.TCP.Kind != model.TrafficTCPKindOperation || exchange.TCP.ApplicationProtocol != model.ApplicationProtocolRedis || exchange.TCP.Operation != "PING" || !exchange.Background || len(exchange.TCP.RequestMessages) != 1 || len(exchange.TCP.ResponseMessages) != 1 || !strings.Contains(exchange.TCP.RequestMessages[0].Content, "PING") || !strings.Contains(exchange.TCP.ResponseMessages[0].Content, "PONG") {
				t.Fatalf("decoded Redis exchange = %#v", exchange)
			}
			close(release)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	t.Fatal("Redis operation was not retained while its TCP connection remained open")
}

func TestBackgroundTCPOperationRequiresSuccessfulUnfaultedHousekeeping(t *testing.T) {
	tests := []struct {
		name      string
		operation protocol.Operation
		fault     string
		want      bool
	}{
		{name: "successful housekeeping", operation: protocol.Operation{Background: true, Outcome: model.TrafficTCPOutcomeSuccess}, want: true},
		{name: "application operation", operation: protocol.Operation{Outcome: model.TrafficTCPOutcomeSuccess}},
		{name: "protocol error outcome", operation: protocol.Operation{Background: true, Outcome: model.TrafficTCPOutcomeError}},
		{name: "connection error", operation: protocol.Operation{Background: true, Outcome: model.TrafficTCPOutcomeSuccess, Error: "connection reset"}},
		{name: "fault intervention", operation: protocol.Operation{Background: true, Outcome: model.TrafficTCPOutcomeSuccess}, fault: "redis-delay"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := backgroundTCPOperation(test.operation, test.fault); got != test.want {
				t.Fatalf("backgroundTCPOperation() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRedisRecordingPayloadPolicyPreservesOperationMetadata(t *testing.T) {
	ctx := context.Background()
	controlStore := environmentStore(t)
	defer controlStore.Close()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			connection, acceptErr := upstream.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				request := make([]byte, len("*1\r\n$4\r\nPING\r\n"))
				if _, readErr := io.ReadFull(connection, request); readErr == nil {
					_, _ = connection.Write([]byte("+PONG\r\n"))
				}
			}()
		}
	}()

	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	defer manager.Close(ctx)
	scope := model.EnvironmentSelector("billing", "local")
	manager.SetTarget(scope, "redis", upstream.Addr().(*net.TCPAddr).Port)
	port, err := manager.EnsureEdge(ctx, scope, model.Connection{
		Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP, ApplicationProtocol: model.ApplicationProtocolRedis,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "metadata-only"}); err != nil {
		t.Fatal(err)
	}
	exerciseRedisPing(t, port)
	metadataOnly := waitForRecordedTraffic(t, controlStore, scope, "metadata-only")
	if metadataOnly.TCP == nil || metadataOnly.TCP.Operation != "PING" || !metadataOnly.Background || metadataOnly.TCP.RequestMessageCount != 1 || metadataOnly.TCP.ResponseMessageCount != 1 {
		t.Fatalf("metadata-only recording lost operation details: %#v", metadataOnly)
	}
	if metadataOnly.TCP.RequestMessages != nil || metadataOnly.TCP.ResponseMessages != nil || metadataOnly.RequestCapturedBytes != 0 || metadataOnly.ResponseCapturedBytes != 0 {
		t.Fatalf("metadata-only recording retained payloads: %#v", metadataOnly)
	}
	if err := controlStore.StopRecording(ctx, scope, "metadata-only", "stopped"); err != nil {
		t.Fatal(err)
	}

	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "with-payloads", CapturePayloads: true, MaxPayloadBytes: 4}); err != nil {
		t.Fatal(err)
	}
	exerciseRedisPing(t, port)
	withPayloads := waitForRecordedTraffic(t, controlStore, scope, "with-payloads")
	if withPayloads.TCP == nil || len(withPayloads.TCP.RequestMessages) != 1 || len(withPayloads.TCP.ResponseMessages) != 1 || withPayloads.TCP.RequestMessages[0].CapturedBytes != 4 || withPayloads.TCP.ResponseMessages[0].CapturedBytes != 4 || !withPayloads.TCP.RequestMessages[0].Truncated || !withPayloads.TCP.ResponseMessages[0].Truncated {
		t.Fatalf("payload recording did not retain decoded messages: %#v", withPayloads)
	}
	live := trafficStore.RecentExchanges(scope, 1)
	if len(live) != 1 || live[0].TCP == nil || !strings.Contains(live[0].TCP.RequestMessages[0].Content, "PING") || !strings.Contains(live[0].TCP.ResponseMessages[0].Content, "PONG") {
		t.Fatalf("small recording limit changed the independent live payload: %#v", live)
	}
}

func TestProtocolObservationPreservesForwardingOrderWithinABound(t *testing.T) {
	session := &orderedProtocolSession{state: protocol.State{Inspection: model.TrafficInspectionDecoded}}
	observation := &tcpProtocolObservation{session: session, emit: func([]protocol.Operation) {}}
	requestTicket := observation.reserve()
	responseTicket := observation.reserve()
	observed := time.Now().UTC()
	observation.submit(responseTicket, protocol.DirectionResponse, []byte("response"), observed.Add(time.Millisecond))
	if len(session.directions) != 0 {
		t.Fatalf("response was observed before the earlier request: %#v", session.directions)
	}
	observation.submit(requestTicket, protocol.DirectionRequest, []byte("request"), observed)
	if len(session.directions) != 2 || session.directions[0] != protocol.DirectionRequest || session.directions[1] != protocol.DirectionResponse {
		t.Fatalf("protocol observation order = %#v", session.directions)
	}

	limited := &tcpProtocolObservation{session: &orderedProtocolSession{state: protocol.State{Inspection: model.TrafficInspectionDecoded}}, emit: func([]protocol.Operation) {}}
	tickets := make([]uint64, maximumReorderedProtocolChunks+2)
	for index := range tickets {
		tickets[index] = limited.reserve()
	}
	for _, ticket := range tickets[1:] {
		limited.submit(ticket, protocol.DirectionResponse, []byte("deferred"), observed)
	}
	if state := limited.state(); state.Inspection != model.TrafficInspectionLimited {
		t.Fatalf("unbounded reorder state = %#v", state)
	}
}

type orderedProtocolSession struct {
	directions []protocol.Direction
	state      protocol.State
}

func (s *orderedProtocolSession) Observe(direction protocol.Direction, _ []byte, _ time.Time) []protocol.Operation {
	s.directions = append(s.directions, direction)
	return nil
}

func (s *orderedProtocolSession) Close(time.Time, error) []protocol.Operation { return nil }

func (s *orderedProtocolSession) State() protocol.State { return s.state }

func exerciseRedisPing(t *testing.T, port int) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Second))
	if _, err := connection.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, len("+PONG\r\n"))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
}

func waitForRecordedTraffic(t *testing.T, controlStore *database.Store, scope, recording string) model.TrafficExchange {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exchanges, err := controlStore.RecordedTraffic(context.Background(), scope, recording, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(exchanges) == 1 {
			return exchanges[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("recording %q did not receive Redis traffic", recording)
	return model.TrafficExchange{}
}

func TestTCPEdgeDoesNotReportForcedCopyShutdownAsAnError(t *testing.T) {
	controlStore, err := database.Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		connection, acceptErr := upstream.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		request := make([]byte, 4)
		if _, readErr := io.ReadFull(connection, request); readErr != nil {
			return
		}
		_, _ = connection.Write([]byte("pong"))
		time.Sleep(time.Second)
	}()

	broker := events.NewBroker()
	trafficStore := trafficstore.NewStore(broker)
	manager := NewManager(controlStore, trafficStore, broker)
	defer manager.Close(context.Background())
	scope := model.EnvironmentSelector("billing", "local")
	manager.SetTarget(scope, "redis", upstream.Addr().(*net.TCPAddr).Port)
	port, err := manager.EnsureEdge(context.Background(), scope, model.Connection{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		exchanges := trafficStore.RecentExchanges(scope, 1)
		if len(exchanges) == 1 {
			if exchanges[0].Error != "" {
				t.Fatalf("successful TCP exchange error = %q", exchanges[0].Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("completed TCP traffic was not published")
}

func TestLocalProcessResponseHeadersCanWaitForDebugger(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		select {
		case <-releaseResponse:
			writer.WriteHeader(http.StatusNoContent)
		case <-request.Context().Done():
		}
	}))
	defer upstream.Close()
	defer func() {
		select {
		case <-releaseResponse:
		default:
			close(releaseResponse)
		}
	}()

	manager := newManagerForTest(controlStore)
	defer manager.Close(context.Background())
	if manager.localProcessTransport.ResponseHeaderTimeout != 0 {
		t.Fatalf("local process response header timeout = %s, want no timeout", manager.localProcessTransport.ResponseHeaderTimeout)
	}
	manager.boundedTransport.ResponseHeaderTimeout = 20 * time.Millisecond
	manager.SetTarget(scope, "checkout", upstream.Listener.Addr().(*net.TCPAddr).Port)
	response := httptest.NewRecorder()
	finished := make(chan struct{})
	go func() {
		manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/debug", nil), scope, "checkout")
		close(finished)
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("local process did not receive the request")
	}
	select {
	case <-finished:
		t.Fatalf("local process request ended before the debugger delay was released: %d %q", response.Code, response.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseResponse)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("local process request did not finish after releasing the response")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("local process response = %d %q", response.Code, response.Body.String())
	}
}

func TestRemoteResponseHeadersRemainBounded(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		select {
		case <-releaseResponse:
			writer.WriteHeader(http.StatusNoContent)
		case <-request.Context().Done():
		}
	}))
	defer remote.Close()
	defer func() {
		select {
		case <-releaseResponse:
		default:
			close(releaseResponse)
		}
	}()

	manager := newManagerForTest(controlStore)
	defer manager.Close(context.Background())
	if manager.boundedTransport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("remote response header timeout = %s, want 30s", manager.boundedTransport.ResponseHeaderTimeout)
	}
	manager.boundedTransport.ResponseHeaderTimeout = 25 * time.Millisecond
	if err := manager.SetRemoteTarget(scope, "payments", model.RemoteTarget{URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://payments.local.billing.localhost/debug", nil), scope, "payments")

	select {
	case <-requestStarted:
	default:
		t.Fatal("remote target did not receive the request")
	}
	if response.Code != http.StatusBadGateway || !strings.Contains(strings.ToLower(response.Body.String()), "timeout") {
		t.Fatalf("remote timeout response = %d %q", response.Code, response.Body.String())
	}
}

func TestUpstreamRequestsHonorClientCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		remote bool
	}{
		{name: "local process"},
		{name: "remote", remote: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			controlStore := environmentStore(t)
			defer controlStore.Close()
			scope := model.EnvironmentSelector("billing", "local")
			requestStarted := make(chan struct{})
			requestCanceled := make(chan struct{})
			releaseResponse := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				close(requestStarted)
				select {
				case <-request.Context().Done():
					close(requestCanceled)
				case <-releaseResponse:
					writer.WriteHeader(http.StatusNoContent)
				}
			}))
			defer upstream.Close()
			defer func() {
				select {
				case <-releaseResponse:
				default:
					close(releaseResponse)
				}
			}()

			manager := newManagerForTest(controlStore)
			defer manager.Close(context.Background())
			if test.remote {
				if err := manager.SetRemoteTarget(scope, "payments", model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}); err != nil {
					t.Fatal(err)
				}
			} else {
				manager.SetTarget(scope, "payments", upstream.Listener.Addr().(*net.TCPAddr).Port)
			}
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, "http://payments.local.billing.localhost/debug", nil).WithContext(requestContext)
			response := httptest.NewRecorder()
			finished := make(chan struct{})
			go func() {
				manager.ServeIngress(response, request, scope, "payments")
				close(finished)
			}()

			select {
			case <-requestStarted:
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive the request")
			}
			cancel()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("proxy did not stop after client cancellation")
			}
			select {
			case <-requestCanceled:
			case <-time.After(time.Second):
				t.Fatal("upstream request was not canceled")
			}
			if response.Code != http.StatusBadGateway {
				t.Fatalf("canceled response status = %d, want %d", response.Code, http.StatusBadGateway)
			}
		})
	}
}

func TestRemoteTargetForwardsHTTPAndEnforcesReadOnlyPolicy(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	var receivedPath, receivedHost string
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedPath, receivedHost = request.URL.Path, request.Host
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()
	manager := newManagerForTest(controlStore)
	if err := manager.SetRemoteTarget(scope, "payments", model.RemoteTarget{URL: remote.URL + "/api", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health"}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://payments.local.billing.localhost/charges/42", nil), scope, "payments")
	parsed, _ := url.Parse(remote.URL)
	if response.Code != http.StatusNoContent || receivedPath != "/api/charges/42" || receivedHost != parsed.Host {
		t.Fatalf("remote response=%d path=%q host=%q", response.Code, receivedPath, receivedHost)
	}

	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodPost, "http://payments.local.billing.localhost/charges", nil), scope, "payments")
	if response.Code != http.StatusForbidden || response.Header().Get("X-Portless-Remote-Policy") != "read-only" {
		t.Fatalf("write policy response=%d headers=%v", response.Code, response.Header())
	}
}

func TestRemotePreflightDoesNotReplaceServingTarget(t *testing.T) {
	controlStore := environmentStore(t)
	defer controlStore.Close()
	scope := model.EnvironmentSelector("billing", "local")
	local := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("local"))
	}))
	defer local.Close()
	localURL, _ := url.Parse(local.URL)
	localPort, _ := strconv.Atoi(localURL.Port())
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = writer.Write([]byte("remote"))
	}))
	defer remote.Close()
	manager := newManagerForTest(controlStore)
	manager.SetTarget(scope, "payments", localPort)
	if err := manager.CheckRemoteTarget(context.Background(), model.RemoteTarget{
		URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/health",
	}); err != nil {
		t.Fatal(err)
	}
	_, provider, targetEndpoint := manager.ConnectionRuntime(scope, "external", "payments")
	if provider != model.ProviderLocal || targetEndpoint != net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)) {
		t.Fatalf("preflight changed target: provider=%s endpoint=%s", provider, targetEndpoint)
	}
	response := httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://payments.local.billing.localhost/charges", nil), scope, "payments")
	if response.Code != http.StatusOK || response.Body.String() != "local" {
		t.Fatalf("preflight interrupted local traffic: %d %q", response.Code, response.Body.String())
	}
}

func environmentStore(t *testing.T) *database.Store {
	t.Helper()
	controlStore, err := database.Open(filepath.Join(t.TempDir(), "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}, {Name: "payments", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(context.Background(), "billing", definition, nil); err != nil {
		t.Fatal(err)
	}
	bindings := []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}, {Service: "payments", Provider: model.ProviderLocal, Source: "payments"}}
	if _, err := controlStore.CreateEnvironment(context.Background(), "billing", "local", definition, nil, bindings); err != nil {
		t.Fatal(err)
	}
	return controlStore
}

func newManagerForTest(controlStore *database.Store) *Manager {
	broker := events.NewBroker()
	return NewManager(controlStore, trafficstore.NewStore(broker), broker)
}

func firstHeader(headers map[string][]string, name string) string {
	if len(headers[name]) == 0 {
		return ""
	}
	return headers[name][0]
}
