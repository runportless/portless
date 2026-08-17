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

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/events"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/networking"
	trafficstore "github.com/portless-run/portless/portless-daemon/traffic"
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

	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Environment: "local", Name: "checkout-debug"}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	manager.ServeIngress(response, httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost/recorded", nil), scope, "checkout")
	recorded, err := controlStore.RecordedTraffic(ctx, scope, "checkout-debug", 10)
	if err != nil || len(recorded) != 1 || recorded[0].Recording != "checkout-debug" || recorded[0].RequestBody != "" || recorded[0].ResponseBody != "" {
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
	first.CloseEnvironment(context.Background(), "billing/local")
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
