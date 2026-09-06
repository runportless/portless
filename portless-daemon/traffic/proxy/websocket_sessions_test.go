package proxy

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type websocketTestCloser struct{ closed atomic.Int64 }

func (c *websocketTestCloser) Close() error { c.closed.Add(1); return nil }

func TestWebSocketAdmissionLimitAndLateAttachment(t *testing.T) {
	m := websocketManager(t)
	m.SetTarget("billing/local", "orders", 12345)
	type admission struct {
		session *websocketSession
		failure *websocketFailure
	}
	const attempts = maximumWebSocketSessions * 2
	results := make(chan admission, attempts)
	start := make(chan struct{})
	for range attempts {
		go func() {
			<-start
			_, s, failure := m.admitWebSocket(t.Context(), "billing/local", "checkout", "orders")
			results <- admission{s, failure}
		}()
	}
	close(start)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var sessions []*websocketSession
	for range attempts {
		select {
		case result := <-results:
			if result.failure != nil {
				if result.failure.status != http.StatusServiceUnavailable {
					t.Errorf("admit: %#v", result.failure)
				}
				continue
			}
			sessions = append(sessions, result.session)
		case <-deadline.C:
			t.Fatal("concurrent admission did not complete")
		}
	}
	if len(sessions) != maximumWebSocketSessions {
		t.Fatalf("concurrent admissions = %d, want %d", len(sessions), maximumWebSocketSessions)
	}
	_, _, failure := m.admitWebSocket(t.Context(), "billing/local", "checkout", "orders")
	if failure == nil || failure.status != http.StatusServiceUnavailable {
		t.Fatalf("capacity failure: %#v", failure)
	}
	m.releaseWebSocket(sessions[0])
	sessions = sessions[1:]
	_, replacement, failure := m.admitWebSocket(t.Context(), "billing/local", "checkout", "orders")
	if failure != nil {
		t.Fatal("capacity was not recovered")
	}
	sessions = append(sessions, replacement)
	m.RemoveTarget("billing/local", "orders")
	m.SetTarget("billing/local", "orders", 12345)
	for _, s := range sessions {
		late := new(websocketTestCloser)
		if s.attach(late) || late.closed.Load() != 1 {
			t.Fatal("canceled admission retained a late connection")
		}
		m.releaseWebSocket(s)
	}
	_, fresh, failure := m.admitWebSocket(t.Context(), "billing/local", "checkout", "orders")
	if failure != nil {
		t.Fatal("replacement target unavailable")
	}
	if fresh.generation == replacement.generation {
		t.Fatal("re-added target reused retired generation")
	}
	m.releaseWebSocket(fresh)
}

func TestWebSocketCancellationRejectsLateAttachment(t *testing.T) {
	// A pending/active socket is always canceled, including a connection attached
	// after the request context was canceled but before its callback ran.
	m := websocketManager(t)
	m.SetTarget("billing/local", "orders", 12345)
	ctx, cancel := context.WithCancel(t.Context())
	_, s, failure := m.admitWebSocket(ctx, "billing/local", "external", "orders")
	if failure != nil {
		t.Fatal(failure.message)
	}
	cancel()
	late := new(websocketTestCloser)
	if s.attach(late) || late.closed.Load() != 1 {
		t.Fatal("connection attached after cancellation")
	}
	m.releaseWebSocket(s)
}

func BenchmarkWebSocketAdmission(b *testing.B) {
	// Allocation cost stays per connection, independent of message size/count.
	m := &Manager{targets: map[string]target{targetKey("billing/local", "orders"): {generation: 1}}, websocketSessions: make(map[*websocketSession]struct{})}
	b.ReportAllocs()
	for b.Loop() {
		_, s, failure := m.admitWebSocket(b.Context(), "billing/local", "checkout", "orders")
		if failure != nil {
			b.Fatal(failure.message)
		}
		m.releaseWebSocket(s)
	}
}
