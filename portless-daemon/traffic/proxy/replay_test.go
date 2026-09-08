package proxy

import (
	"context"
	"encoding/json"
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

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	trafficstore "github.com/runportless/portless/portless-daemon/traffic"
)

func replayFixture(t *testing.T, handler http.HandlerFunc) (*Manager, *database.Store, *trafficstore.Store, *httptest.Server) {
	t.Helper()
	db := environmentStore(t)
	t.Cleanup(func() { _ = db.Close() })
	broker := events.NewBroker()
	store := newTestTrafficStore(t, broker)
	manager := NewManager(db, store, broker)
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	address, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(address.Port())
	manager.SetTarget("billing/local", "orders", port)
	t.Cleanup(func() { manager.Close(context.Background()) })
	return manager, db, store, upstream
}

func runReplay(t *testing.T, manager *Manager, ctx context.Context, method, path string, headers map[string][]string, body string) (model.TrafficExchange, string, error) {
	t.Helper()
	upstream, _ := manager.target("billing/local", "orders")
	expected := model.ComponentBinding{Service: "orders", Provider: upstream.provider}
	if upstream.baseURL != nil {
		expected.Remote = &model.RemoteTarget{URL: upstream.baseURL.String(), Classification: upstream.classification, WritePolicy: upstream.writePolicy, HealthPath: upstream.healthPath}
	}
	selected, err := manager.ReplayTarget("billing/local", "orders", expected, method, path)
	if err != nil {
		return model.TrafficExchange{}, "not-sent", err
	}
	return manager.ReplayHTTP(ctx, "billing/local", "checkout", "orders", method, path, headers, body, selected.Generation, selected.RoutingRevision, model.TrafficReplay{Project: "billing", Environment: "local", Sequence: 42, StartedAt: time.Unix(1, 0), Workspace: 1, Run: 1})
}

func TestReplayRequestCaptureCompletesWithLastBytes(t *testing.T) {
	const body = `{"order":1}`
	request, err := replayRequest(t.Context(), "billing/local", "orders", "POST", "/orders", nil, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = request.Body.Close() })
	if request.ContentLength != int64(len(body)) || request.GetBody != nil {
		t.Fatalf("content length=%d retryable=%v", request.ContentLength, request.GetBody != nil)
	}
	capture := captureRequestBody(request, trafficBodyLimit)
	// A transport can receive the response as soon as ContentLength bytes have
	// been written, before it asks the request body for another read.
	for index := range len(body) {
		var content [1]byte
		read, err := request.Body.Read(content[:])
		if read != 1 || content[0] != body[index] || err != nil && err != io.EOF {
			t.Fatalf("byte %d: read=%d content=%q err=%v", index, read, content, err)
		}
		metadata := captureMetadata(freezeCapture(capture))
		if index < len(body)-1 {
			if err != nil || metadata.State != "incomplete" || metadata.Exact {
				t.Fatalf("partial capture=%#v err=%v", metadata, err)
			}
		} else if metadata.State != "complete" || !metadata.Exact || metadata.ObservedBytes != int64(len(body)) || capture.text() != body {
			t.Fatalf("final capture=%#v body=%q", metadata, capture.text())
		}
	}
}

func TestReplaySendsFullSizeBodyAndRejectsOversizeBeforeDispatch(t *testing.T) {
	var calls, received atomic.Int64
	manager, _, _, _ := replayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		size, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Error(err)
		}
		received.Store(size)
		w.WriteHeader(http.StatusNoContent)
	})
	body := strings.Repeat("é", contract.TrafficReplayMaxBodyBytes/2)
	exchange, outcome, err := runReplay(t, manager, t.Context(), "POST", "/orders", map[string][]string{"Content-Type": {"text/plain"}}, body)
	if err != nil || outcome != "response-received" || exchange.Status != 204 || received.Load() != int64(len(body)) || calls.Load() != 1 {
		t.Fatalf("large body status=%d outcome=%s bytes=%d calls=%d err=%v", exchange.Status, outcome, received.Load(), calls.Load(), err)
	}
	if len(exchange.RequestBody) > trafficBodyLimit || exchange.RequestCapture.State != "truncated" {
		t.Fatal("large outgoing body bypassed ordinary traffic capture limits")
	}
	_, outcome, err = runReplay(t, manager, t.Context(), "POST", "/orders", nil, body+"x")
	if err == nil || outcome != "not-sent" || calls.Load() != 1 {
		t.Fatalf("oversized body outcome=%s calls=%d err=%v", outcome, calls.Load(), err)
	}
}

func TestReplayPreservesEdgeAndExactRequestAndRedactsBeforeRetention(t *testing.T) {
	var calls atomic.Int64
	manager, db, store, _ := replayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.RequestURI != "/orders/%2Fitem?a=1&a=2&space=+" || r.Host != "orders.local.billing.localhost" || string(body) != `{"order":1}` || r.ProtoMajor != 1 {
			t.Errorf("request uri=%q host=%q body=%q protocol=%s", r.RequestURI, r.Host, body, r.Proto)
		}
		if len(r.Header.Values("X-Repeated")) != 2 || r.Header.Get("Traceparent") == "00-11111111111111111111111111111111-2222222222222222-01" || r.Header.Get("Tracestate") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("Portless-Client-Kind") != "" {
			t.Errorf("headers=%v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"echo":"replay-secret-123"}`)
	})
	_, err := db.CreateRecording(t.Context(), model.Recording{Project: "billing", Environment: "local", Name: "replay-test", CapturePayloads: true, MaxPayloadBytes: trafficBodyLimit})
	if err != nil {
		t.Fatal(err)
	}
	exchange, outcome, err := runReplay(t, manager, t.Context(), "POST", "/orders/%2Fitem?a=1&a=2&space=+", map[string][]string{"Content-Type": {"application/json"}, "Authorization": {"Bearer replay-secret-123"}, "X-Repeated": {"first", "second"}, "Traceparent": {"00-11111111111111111111111111111111-2222222222222222-01"}, "Tracestate": {"old=true"}, "X-Forwarded-Host": {"evil.example"}, "Portless-Client-Kind": {"cli"}}, `{"order":1}`)
	if err != nil || outcome != "response-received" || exchange.Status != 201 || calls.Load() != 1 {
		t.Fatalf("exchange=%#v outcome=%s err=%v calls=%d", exchange, outcome, err, calls.Load())
	}
	if exchange.Replay == nil || exchange.Replay.Sequence != 42 || exchange.Source != "checkout" || exchange.Target != "orders" || exchange.ParentSpanID != "" {
		t.Fatalf("replay provenance=%#v exchange=%#v", exchange.Replay, exchange)
	}
	if exchange.RequestCapture == nil || exchange.RequestCapture.State != "complete" || !exchange.RequestCapture.Exact {
		t.Fatalf("request capture=%#v", exchange.RequestCapture)
	}
	if exchange.ResponseCapture == nil || exchange.ResponseCapture.Exact {
		t.Fatalf("redacted response capture=%#v", exchange.ResponseCapture)
	}
	recorded, err := db.RecordedTraffic(t.Context(), "billing/local", "replay-test", 10)
	if err != nil || len(recorded) != 1 {
		t.Fatalf("recorded=%#v err=%v", recorded, err)
	}
	for _, value := range []any{exchange, store.RecentExchanges("billing/local", 10), recorded} {
		encoded, _ := json.Marshal(value)
		if strings.Contains(string(encoded), "replay-secret-123") {
			t.Fatalf("credential leaked: %s", encoded)
		}
	}
}

func TestReplayRemotePolicyAndEscapedBasePath(t *testing.T) {
	var calls atomic.Int64
	manager, _, _, upstream := replayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.RequestURI != "/api%2Fv1/items/%2F?x=1&x=2" {
			t.Errorf("remote request target=%q", r.RequestURI)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	remote := model.RemoteTarget{URL: upstream.URL + "/api%2Fv1", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}
	if err := manager.SetRemoteTarget("billing/local", "orders", remote); err != nil {
		t.Fatal(err)
	}
	exchange, outcome, err := runReplay(t, manager, t.Context(), "POST", "/items/%2F?x=1&x=2", nil, "")
	if err == nil || outcome != "not-sent" || calls.Load() != 0 || exchange.Sequence == 0 || exchange.Status != 0 {
		t.Fatalf("policy result=%#v outcome=%s err=%v calls=%d", exchange, outcome, err, calls.Load())
	}
	exchange, outcome, err = runReplay(t, manager, t.Context(), "GET", "/items/%2F?x=1&x=2", nil, "")
	if err != nil || outcome != "response-received" || calls.Load() != 1 || exchange.Status != 204 || exchange.ResponseCapture.State != "empty" {
		t.Fatalf("read result=%#v outcome=%s err=%v", exchange, outcome, err)
	}
}

func TestReplayGenerationChangeDuringFaultDelayStopsDispatch(t *testing.T) {
	var calls atomic.Int64
	manager, db, _, _ := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) })
	_, err := db.CreateFault(t.Context(), model.FaultRule{Project: "billing", Environment: "local", Name: "delayed", Source: "checkout", Target: "orders", Probability: 1, LatencyMS: 500})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		exchange model.TrafficExchange
		outcome  string
		err      error
	}
	finished := make(chan result, 1)
	go func() {
		exchange, outcome, err := runReplay(t, manager, t.Context(), "POST", "/orders", nil, "")
		finished <- result{exchange, outcome, err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		fault, err := db.Fault(t.Context(), "billing/local", "delayed")
		if err != nil {
			t.Fatal(err)
		}
		if fault.MatchCount > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fault did not match")
		}
		time.Sleep(time.Millisecond)
	}
	manager.SetTarget("billing/local", "orders", 9)
	select {
	case result := <-finished:
		if result.err == nil || result.outcome != "not-sent" || result.exchange.Sequence == 0 || calls.Load() != 0 {
			t.Fatalf("result=%#v calls=%d", result, calls.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not cancel replay")
	}
}

func TestReplayPartialResponseRetainsStatusAndCapsConsumption(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		manager, _, _, _ := replayFixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(202)
			_, _ = io.WriteString(w, "started")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		})
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		exchange, outcome, err := runReplay(t, manager, ctx, "GET", "/slow", nil, "")
		if err == nil || outcome != "response-received" || exchange.Status != 202 || exchange.ResponseCapture.State != "incomplete" {
			t.Fatalf("exchange=%#v outcome=%s err=%v", exchange, outcome, err)
		}
	})
	t.Run("consumption", func(t *testing.T) {
		manager, _, _, _ := replayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, strings.Repeat("x", replayResponseLimit+1024))
		})
		exchange, outcome, err := runReplay(t, manager, t.Context(), "GET", "/large", nil, "")
		if err == nil || outcome != "response-received" || exchange.ResponseBytes != replayResponseLimit || len(exchange.ResponseBody) != trafficBodyLimit || exchange.ResponseCapture.State != "incomplete" {
			t.Fatalf("status=%d bytes=%d retained=%d capture=%#v outcome=%s err=%v", exchange.Status, exchange.ResponseBytes, len(exchange.ResponseBody), exchange.ResponseCapture, outcome, err)
		}
	})
}

func TestReplayDoesNotRetryOrFollowRedirect(t *testing.T) {
	for _, mode := range []string{"disconnect", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			manager, _, _, _ := replayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "redirect" {
					http.Redirect(w, r, "/followed", http.StatusFound)
					return
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
			})
			exchange, outcome, err := runReplay(t, manager, t.Context(), "GET", "/original", map[string][]string{"Idempotency-Key": {"one"}}, "")
			if calls.Load() != 1 {
				t.Fatalf("request sent %d times", calls.Load())
			}
			if mode == "disconnect" && (err == nil || outcome != "unknown" || exchange.Status != 0) {
				t.Fatalf("exchange=%#v outcome=%s err=%v", exchange, outcome, err)
			}
			if mode == "redirect" && (err != nil || outcome != "response-received" || exchange.Status != 302) {
				t.Fatalf("exchange=%#v outcome=%s err=%v", exchange, outcome, err)
			}
		})
	}
}

func TestReplayAbortFaultAndUntrustedTLS(t *testing.T) {
	t.Run("abort", func(t *testing.T) {
		manager, db, _, _ := replayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("abort reached upstream") })
		_, err := db.CreateFault(t.Context(), model.FaultRule{Project: "billing", Environment: "local", Name: "abort", Source: "checkout", Target: "orders", Probability: 1, Abort: true})
		if err != nil {
			t.Fatal(err)
		}
		exchange, outcome, err := runReplay(t, manager, t.Context(), "GET", "/abort", nil, "")
		if err == nil || outcome != "not-sent" || exchange.Status != 0 || exchange.Fault != "abort" {
			t.Fatalf("exchange=%#v outcome=%s err=%v", exchange, outcome, err)
		}
	})
	t.Run("tls", func(t *testing.T) {
		manager, _, _, _ := replayFixture(t, func(http.ResponseWriter, *http.Request) {})
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS request reached application") }))
		defer tlsServer.Close()
		if err := manager.SetRemoteTarget("billing/local", "orders", model.RemoteTarget{URL: tlsServer.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}); err != nil {
			t.Fatal(err)
		}
		exchange, outcome, err := runReplay(t, manager, t.Context(), "GET", "/secure", nil, "")
		if err == nil || outcome != "not-sent" || strings.Contains(exchange.Error, tlsServer.Listener.Addr().String()) {
			t.Fatalf("outcome=%s exchange=%#v err=%v", outcome, exchange, err)
		}
	})
}

func TestReplayRejectsUnsafeRequestBeforeDial(t *testing.T) {
	manager, _, _, _ := replayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid request dispatched") })
	for _, path := range []string{"https://example.com/", "//example.com/path", "/bad#fragment", "/bad%zz", "/bad\npath"} {
		_, outcome, err := runReplay(t, manager, t.Context(), "GET", path, nil, "")
		if err == nil || outcome != "not-sent" {
			t.Errorf("accepted %q: outcome=%s err=%v", path, outcome, err)
		}
	}
	_, outcome, err := runReplay(t, manager, t.Context(), "CONNECT", "/", nil, "")
	if err == nil || outcome != "not-sent" {
		t.Fatalf("CONNECT outcome=%s err=%v", outcome, err)
	}
}

func TestReplayConnectionRejectsInvalidatedGenerationBeforeWrite(t *testing.T) {
	manager, _, _, _ := replayFixture(t, func(http.ResponseWriter, *http.Request) {})
	upstream, _ := manager.target("billing/local", "orders")
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attempt := &replayAttempt{manager: manager, scope: "billing/local", targetName: "orders", upstream: upstream, ctx: ctx}
	connection := &replayConnection{Conn: client, attempt: attempt}
	manager.SetTarget("billing/local", "orders", 9)
	if _, err := connection.Write([]byte("GET / HTTP/1.1\r\n\r\n")); err == nil || attempt.dispatched.Load() {
		t.Fatalf("invalidated write err=%v dispatched=%v", err, attempt.dispatched.Load())
	}
}

func TestReplayTargetRequiresMatchingEffectiveProviderAndRemotePolicy(t *testing.T) {
	manager, _, _, upstream := replayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("target inspection sent application traffic") })
	remote := model.RemoteTarget{URL: upstream.URL + "/api", Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite, HealthPath: "/health"}
	if err := manager.SetRemoteTarget("billing/local", "orders", remote); err != nil {
		t.Fatal(err)
	}
	if selected, err := manager.ReplayTarget("billing/local", "orders", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &remote}, "GET", "/"); err != nil || selected.Generation == 0 {
		t.Fatalf("matching snapshot=%#v err=%v", selected, err)
	}
	if _, err := manager.ReplayTarget("billing/local", "orders", model.ComponentBinding{Provider: model.ProviderLocal}, "GET", "/"); err == nil {
		t.Fatal("saved local binding matched effective remote target")
	}
	if _, err := manager.ReplayTarget("billing/local", "orders", model.ComponentBinding{Provider: model.ProviderRemote}, "GET", "/"); err == nil {
		t.Fatal("remote binding without policy accepted")
	}
	for _, test := range []struct {
		name   string
		change func(*model.RemoteTarget)
	}{
		{"url", func(value *model.RemoteTarget) { value.URL += "/changed" }},
		{"classification", func(value *model.RemoteTarget) { value.Classification = model.RemoteStaging }},
		{"write policy", func(value *model.RemoteTarget) { value.WritePolicy = model.WriteReadOnly }},
		{"health path", func(value *model.RemoteTarget) { value.HealthPath = "/ready" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected := remote
			test.change(&expected)
			if _, err := manager.ReplayTarget("billing/local", "orders", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &expected}, "GET", "/"); err == nil {
				t.Fatal("mismatched remote snapshot accepted")
			}
		})
	}
}
