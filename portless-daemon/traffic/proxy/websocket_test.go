package proxy

import (
	"bufio"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

const testWebSocketKey = "dGhlIHNhbXBsZSBub25jZQ=="
const testWebSocketAccept = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="

func websocketManager(t *testing.T) *Manager {
	t.Helper()
	db := environmentStore(t)
	t.Cleanup(func() { _ = db.Close() })
	m := newManagerForTest(t, db)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		m.Close(ctx)
	})
	return m
}

func websocketEcho(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Key") != testWebSocketKey {
			http.Error(w, "missing upgrade", http.StatusBadRequest)
			return
		}
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: test\r\n\r\nready", testWebSocketAccept)
		_ = rw.Flush()
		_, _ = io.Copy(c, rw.Reader)
	}))
	t.Cleanup(s.Close)
	return s
}

func websocketTarget(t *testing.T, m *Manager, scope, service string, upstream *httptest.Server) {
	t.Helper()
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	m.SetTarget(scope, service, port)
}

func websocketDial(t *testing.T, address, host, path string) (net.Conn, *bufio.Reader, *http.Response) {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive, Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Protocol: test\r\n\r\n", path, host, testWebSocketKey)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(c)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, reader, response
}

func TestWebSocketForwardsIngressAndDependencyEdge(t *testing.T) {
	for _, test := range []struct {
		ingress  bool
		provider model.ProviderKind
	}{
		{true, model.ProviderLocal}, {false, model.ProviderLocal},
		{true, model.ProviderContainer}, {false, model.ProviderContainer},
	} {
		t.Run(fmt.Sprintf("ingress=%t/provider=%s", test.ingress, test.provider), func(t *testing.T) {
			upstream := websocketEcho(t)
			m := websocketManager(t)
			const scope = "billing/local"
			u, _ := url.Parse(upstream.URL)
			port, _ := strconv.Atoi(u.Port())
			m.SetTargetProvider(scope, "orders", port, test.provider)
			var address string
			if test.ingress {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.ServeIngress(w, r, scope, "orders") }))
				t.Cleanup(server.Close)
				address = server.Listener.Addr().String()
			} else {
				port, err := m.EnsureEdge(context.Background(), scope, model.Connection{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP})
				if err != nil {
					t.Fatal(err)
				}
				address = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
			}
			c, reader, response := websocketDial(t, address, "orders.local.billing.localhost", "/api/ws?tag=one&tag=two")
			if response.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("upgrade status = %d", response.StatusCode)
			}
			payload := strings.Repeat("opaque frame bytes", 8000)
			sent := make(chan error, 1)
			go func() { _, err := io.WriteString(c, payload); sent <- err }()
			received := make([]byte, len("ready")+len(payload))
			if _, err := io.ReadFull(reader, received); err != nil {
				t.Fatal(err)
			}
			if string(received) != "ready"+payload {
				t.Fatal("buffered or duplex bytes changed")
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func websocketRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://orders.local.billing.localhost/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", testWebSocketKey)
	r.Header.Set("Sec-WebSocket-Protocol", "test")
	return r
}

func websocketIngress(t *testing.T, m *Manager, scope, target string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { m.ServeIngress(w, r, scope, target) }))
	t.Cleanup(s.Close)
	return s
}

func TestWebSocketRejectsMalformedHandshakes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"method", func(r *http.Request) { r.Method = http.MethodPost }, 400},
		{"http2", func(r *http.Request) { r.ProtoMajor = 2 }, 400},
		{"connection", func(r *http.Request) { r.Header.Del("Connection") }, 400},
		{"key", func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "invalid") }, 400},
		{"duplicate key", func(r *http.Request) { r.Header.Add("Sec-WebSocket-Key", testWebSocketKey) }, 400},
		{"version", func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "12") }, 426},
		{"duplicate version", func(r *http.Request) { r.Header.Add("Sec-WebSocket-Version", "13") }, 400},
		{"body", func(r *http.Request) { r.ContentLength = 1 }, 400},
		{"chunked", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, 400},
		{"connection nominated key", func(r *http.Request) { r.Header.Add("Connection", "sec-websocket-key") }, 400},
		{"other protocol", func(r *http.Request) { r.Header.Set("Upgrade", "other") }, 501},
	}
	m := websocketManager(t)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := websocketRequest()
			test.mutate(r)
			w := httptest.NewRecorder()
			m.ServeIngress(w, r, "billing/local", "orders")
			if w.Code != test.status {
				t.Fatalf("status = %d, want %d", w.Code, test.status)
			}
			if test.status == 426 && w.Header().Get("Sec-WebSocket-Version") != "13" {
				t.Fatal("missing supported version")
			}
		})
	}
}

func TestWebSocketReadOnlyAndMockNeverReachUpstream(t *testing.T) {
	var reached atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Add(1); w.WriteHeader(204) }))
	defer upstream.Close()
	for _, provider := range []model.ProviderKind{model.ProviderRemote, model.ProviderMock} {
		t.Run(string(provider), func(t *testing.T) {
			m := websocketManager(t)
			status := http.StatusNotImplemented
			if provider == model.ProviderRemote {
				status = http.StatusForbidden
				if err := m.SetRemoteTarget("billing/local", "orders", model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}); err != nil {
					t.Fatal(err)
				}
			} else {
				u, _ := url.Parse(upstream.URL)
				port, _ := strconv.Atoi(u.Port())
				m.SetTargetProvider("billing/local", "orders", port, provider)
			}
			w := httptest.NewRecorder()
			m.ServeIngress(w, websocketRequest(), "billing/local", "orders")
			if w.Code != status || reached.Load() != 0 {
				t.Fatalf("status %d, requests %d", w.Code, reached.Load())
			}
			if provider == model.ProviderRemote && w.Header().Get("X-Portless-Remote-Policy") != "read-only" {
				t.Fatal("missing policy header")
			}
		})
	}
}

func TestWebSocketCaptureCompletesWhileConnected(t *testing.T) {
	upstream := websocketEcho(t)
	m := websocketManager(t)
	websocketTarget(t, m, "billing/local", "orders", upstream)
	_, err := m.database.CreateRecording(t.Context(), model.Recording{Project: "billing", Environment: "local", Name: "websocket", CapturePayloads: true, MaxPayloadBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server := websocketIngress(t, m, "billing/local", "orders")
	c, reader, res := websocketDial(t, server.Listener.Addr().String(), "orders.local.billing.localhost", "/auth/ws?tag=one&tag=two")
	if res.StatusCode != 101 {
		t.Fatalf("status %d", res.StatusCode)
	}
	ready := make([]byte, 5)
	if _, err := io.ReadFull(reader, ready); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := io.WriteString(c, "frame secret"); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 12)
		if _, err := io.ReadFull(reader, buf); err != nil {
			t.Fatal(err)
		}
	}
	exchanges := m.traffic.RecentExchanges("billing/local", 10)
	if len(exchanges) != 1 {
		t.Fatalf("exchanges: %#v", exchanges)
	}
	e := exchanges[0]
	if e.Status != 101 || e.Source != "external" || e.Target != "orders" || e.RequestTarget != "/auth/ws?tag=one&tag=two" || e.RequestBytes != 0 || e.ResponseBytes != 0 || e.RequestBody != "" || e.ResponseBody != "" || e.ResponseCapturedBytes != 0 {
		t.Fatalf("handshake: %#v", e)
	}
	for _, headers := range []map[string][]string{e.RequestHeaders, e.ResponseHeaders} {
		if http.Header(headers).Get("Sec-WebSocket-Protocol") != "[REDACTED]" {
			t.Fatalf("subprotocol leaked: %#v", headers)
		}
	}
	recorded, err := m.database.RecordedTraffic(t.Context(), "billing/local", "websocket", 10)
	if err != nil || len(recorded) != 1 || recorded[0].ResponseBody != "" || http.Header(recorded[0].ResponseHeaders).Get("Sec-WebSocket-Protocol") != "[REDACTED]" {
		t.Fatalf("recorded %#v, err %v", recorded, err)
	}
	_ = c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	m.CloseEnvironment(ctx, "billing/local")
	if len(m.traffic.RecentExchanges("billing/local", 10)) != 1 {
		t.Fatal("close duplicated handshake")
	}
}

func TestWebSocketLifecycleClosesOnlyAffectedSessions(t *testing.T) {
	for _, action := range []string{"remove target", "remove source", "provider", "policy", "environment", "manager"} {
		t.Run(action, func(t *testing.T) {
			upstream := websocketEcho(t)
			m := websocketManager(t)
			websocketTarget(t, m, "billing/local", "orders", upstream)
			websocketTarget(t, m, "other/local", "orders", upstream)
			first := websocketIngress(t, m, "billing/local", "orders")
			other := websocketIngress(t, m, "other/local", "orders")
			c, r, res := websocketDial(t, first.Listener.Addr().String(), "orders.local.billing.localhost", "/ws")
			if res.StatusCode != 101 {
				t.Fatal(res.Status)
			}
			ready := make([]byte, 5)
			if _, err := io.ReadFull(r, ready); err != nil {
				t.Fatal(err)
			}
			peer, pr, pres := websocketDial(t, other.Listener.Addr().String(), "orders.local.other.localhost", "/ws")
			if pres.StatusCode != 101 {
				t.Fatal(pres.Status)
			}
			if _, err := io.ReadFull(pr, ready); err != nil {
				t.Fatal(err)
			}
			// Reconciliation of an identical target must leave the first connection alive.
			websocketTarget(t, m, "billing/local", "orders", upstream)
			if _, err := io.WriteString(c, "still here"); err != nil {
				t.Fatal(err)
			}
			echo := make([]byte, 10)
			if _, err := io.ReadFull(r, echo); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			switch action {
			case "remove target":
				m.RemoveTarget("billing/local", "orders")
			case "remove source":
				m.RemoveTarget("billing/local", "external")
			case "provider":
				u, _ := url.Parse(upstream.URL)
				port, _ := strconv.Atoi(u.Port())
				m.SetTargetProvider("billing/local", "orders", port, model.ProviderContainer)
			case "policy":
				if err := m.SetRemoteTarget("billing/local", "orders", model.RemoteTarget{URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}); err != nil {
					t.Fatal(err)
				}
			case "environment":
				m.CloseEnvironment(ctx, "billing/local")
			case "manager":
				m.Close(ctx)
			}
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := r.ReadByte(); err == nil {
				t.Fatal("affected socket stayed open")
			} else if n, ok := err.(net.Error); ok && n.Timeout() {
				t.Fatal("socket timed out instead of closing")
			}
			if action != "manager" {
				if _, err := io.WriteString(peer, "unaffected"); err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadFull(pr, echo); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestWebSocketPendingHandshakeCancellationAndHeaderTimeout(t *testing.T) {
	for _, action := range []string{"remove", "environment", "manager", "timeout"} {
		t.Run(action, func(t *testing.T) {
			received := make(chan struct{})
			canceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(received); <-r.Context().Done(); close(canceled) }))
			defer upstream.Close()
			m := websocketManager(t)
			websocketTarget(t, m, "billing/local", "orders", upstream)
			if action == "timeout" {
				m.websocketTransport.ResponseHeaderTimeout = 30 * time.Millisecond
			}
			result := make(chan int, 1)
			go func() {
				w := httptest.NewRecorder()
				m.ServeIngress(w, websocketRequest(), "billing/local", "orders")
				result <- w.Code
			}()
			select {
			case <-received:
			case <-time.After(time.Second):
				t.Fatal("upstream did not start")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			switch action {
			case "remove":
				m.RemoveTarget("billing/local", "orders")
			case "environment":
				m.CloseEnvironment(ctx, "billing/local")
			case "manager":
				m.Close(ctx)
			}
			select {
			case status := <-result:
				if status != 502 {
					t.Fatalf("status %d", status)
				}
			case <-ctx.Done():
				t.Fatal("pending handshake leaked")
			}
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("upstream was not canceled")
			}
			m.mu.RLock()
			remaining := len(m.websocketSessions)
			m.mu.RUnlock()
			if remaining != 0 {
				t.Fatalf("%d reservations leaked", remaining)
			}
		})
	}
}

func TestWebSocketRemoteTLSAndWireHeaders(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("trusted=%t", trusted), func(t *testing.T) {
			received := make(chan *http.Request, 1)
			upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Clone(context.Background())
				w.Header().Set("Content-Type", "text/plain")
				w.Header().Set("Content-Security-Policy", "application policy")
				w.Header().Set("Connection", "keep-alive, X-Private-Hop")
				w.Header().Set("X-Private-Hop", "remove")
				w.Header().Add("X-Application", "one")
				w.Header().Add("X-Application", "two")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, "application denied")
			}))
			upstream.EnableHTTP2 = true
			upstream.StartTLS()
			defer upstream.Close()
			m := websocketManager(t)
			if trusted {
				m.websocketTransport.TLSClientConfig.RootCAs = x509.NewCertPool()
				m.websocketTransport.TLSClientConfig.RootCAs.AddCert(upstream.Certificate())
			}
			if err := m.SetRemoteTarget("billing/local", "orders", model.RemoteTarget{URL: upstream.URL + "/base%2Fsegment", Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite}); err != nil {
				t.Fatal(err)
			}
			r := websocketRequest()
			r.URL.Path = "/ws/item"
			r.URL.RawPath = "/ws%2Fitem"
			r.URL.RawQuery = "tag=one&tag=two&q=coffee%20mug"
			r.Header.Add("Connection", "X-Request-Hop")
			r.Header.Set("X-Request-Hop", "remove")
			r.Header.Set("Origin", "http://application.localhost")
			r.Header.Set("Authorization", "Bearer secret")
			r.Header.Set("Cookie", "session=secret")
			w := httptest.NewRecorder()
			m.ServeIngress(w, r, "billing/local", "orders")
			if !trusted {
				if w.Code != 502 {
					t.Fatalf("untrusted status %d", w.Code)
				}
				select {
				case <-received:
					t.Fatal("untrusted TLS forwarded a request")
				default:
				}
				return
			}
			if w.Code != 401 || w.Body.String() != "application denied" || w.Header().Get("Content-Security-Policy") != "application policy" || w.Header().Get("X-Private-Hop") != "" || len(w.Header().Values("X-Application")) != 2 {
				t.Fatalf("response %d %#v %q", w.Code, w.Header(), w.Body.String())
			}
			got := <-received
			u, _ := url.Parse(upstream.URL)
			if got.ProtoMajor != 1 || got.Host != u.Host || got.RequestURI != "/base%2Fsegment/ws%2Fitem?tag=one&tag=two&q=coffee%20mug" || got.Header.Get("X-Request-Hop") != "" || got.Header.Get("Origin") != r.Header.Get("Origin") || got.Header.Get("Cookie") != r.Header.Get("Cookie") || got.Header.Get("Authorization") != r.Header.Get("Authorization") {
				t.Fatalf("forwarded request %#v", got)
			}
			exchange := m.traffic.RecentExchanges("billing/local", 1)[0]
			if http.Header(exchange.RequestHeaders).Get("Authorization") != "[REDACTED]" || http.Header(exchange.RequestHeaders).Get("Cookie") != "[REDACTED]" {
				t.Fatal("credentials captured")
			}
		})
	}
}

func TestWebSocketRejectsInvalidUpstreamResponsesAndUnsupportedHandoff(t *testing.T) {
	for _, kind := range []string{"accept", "protocol", "connection", "upgrade", "hijack", "unsolicited", "large headers"} {
		t.Run(kind, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Connection", "Upgrade")
				w.Header().Set("Upgrade", "websocket")
				w.Header().Set("Sec-WebSocket-Accept", testWebSocketAccept)
				switch kind {
				case "accept":
					w.Header().Set("Sec-WebSocket-Accept", "incorrect")
				case "protocol":
					w.Header().Set("Sec-WebSocket-Protocol", "secret-not-offered")
				case "connection":
					w.Header().Del("Connection")
				case "upgrade":
					w.Header().Set("Upgrade", "other")
				case "large headers":
					w.Header().Set("X-Large", strings.Repeat("x", 70<<10))
				}
				w.WriteHeader(101)
			}))
			defer upstream.Close()
			m := websocketManager(t)
			websocketTarget(t, m, "billing/local", "orders", upstream)
			r := websocketRequest()
			if kind == "unsolicited" {
				r.Header = make(http.Header)
			}
			w := httptest.NewRecorder()
			m.ServeIngress(w, r, "billing/local", "orders")
			if w.Code != 502 || strings.Contains(w.Body.String(), "secret-not-offered") {
				t.Fatalf("response %d %q", w.Code, w.Body.String())
			}
			m.mu.RLock()
			remaining := len(m.websocketSessions)
			m.mu.RUnlock()
			if remaining != 0 {
				t.Fatal("failed handshake leaked")
			}
		})
	}
	r := websocketRequest()
	response := &http.Response{Header: http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Sec-Websocket-Accept": {testWebSocketAccept}}, Body: io.NopCloser(strings.NewReader(""))}
	if _, err := validateWebSocketResponse(r, response); err == nil {
		t.Fatal("read-only response body accepted")
	}
}

func TestWebSocketHandshakeFaultsAndCancellation(t *testing.T) {
	for _, kind := range []string{"status", "delay", "abort"} {
		t.Run(kind, func(t *testing.T) {
			upstream := websocketEcho(t)
			m := websocketManager(t)
			websocketTarget(t, m, "billing/local", "orders", upstream)
			fault := model.FaultRule{Project: "billing", Environment: "local", Name: "socket-fault", Source: "external", Target: "orders", Method: "GET", Path: "/ws", Probability: 1}
			switch kind {
			case "status":
				fault.StatusCode = 503
			case "delay":
				fault.LatencyMS = 20
			case "abort":
				fault.Abort = true
			}
			if _, err := m.database.CreateFault(t.Context(), fault); err != nil {
				t.Fatal(err)
			}
			s := websocketIngress(t, m, "billing/local", "orders")
			r := websocketRequest()
			r.URL, _ = url.Parse(s.URL + "/ws")
			r.RequestURI = ""
			client := &http.Client{Timeout: time.Second}
			response, err := client.Do(r)
			if kind == "abort" {
				if err == nil {
					response.Body.Close()
					t.Fatal("fault did not abort connection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if kind == "status" {
				if response.StatusCode != 503 || response.Header.Get("X-Portless-Fault") != "socket-fault" {
					t.Fatal(response.Status)
				}
				return
			}
			if response.StatusCode != 101 {
				t.Fatal(response.Status)
			}
			ready := make([]byte, 5)
			if _, err := io.ReadFull(response.Body, ready); err != nil {
				t.Fatal(err)
			}
			e := m.traffic.RecentExchanges("billing/local", 1)[0]
			if e.Fault != "socket-fault" || e.DurationMS < 20 {
				t.Fatalf("fault trace %#v", e)
			}
		})
	}
}
