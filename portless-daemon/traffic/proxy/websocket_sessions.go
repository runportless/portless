package proxy

import (
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/runportless/portless/portless-daemon/model"
)

const maximumWebSocketSessions = 256

type websocketEdgeContextKey struct{}

// websocketSession owns an admission from before dialing until both copy loops exit.
// The manager lock protects registry membership; mu protects connection attachment.
// Network operations never run under either lock.
type websocketSession struct {
	scope, source, target string
	generation            uint64
	ctx                   context.Context
	cancel                context.CancelFunc
	stopCancellation      func() bool
	done                  chan struct{}
	mu                    sync.Mutex
	closed                bool
	connections           []io.Closer
}

func (s *websocketSession) attach(connection io.Closer) bool {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		_ = connection.Close()
		return false
	}
	s.connections = append(s.connections, connection)
	s.mu.Unlock()
	return true
}

func (s *websocketSession) invalidate() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func (s *websocketSession) close() {
	s.mu.Lock()
	s.closed = true
	connections := s.connections
	s.connections = nil
	s.mu.Unlock()
	s.cancel()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (m *Manager) admitWebSocket(ctx context.Context, scope, source, service string) (target, *websocketSession, *websocketFailure) {
	m.mu.Lock()
	defer m.mu.Unlock()
	configured, ok := m.targets[targetKey(scope, service)]
	if m.closed.Load() || ctx.Err() != nil {
		return configured, nil, &websocketFailure{http.StatusServiceUnavailable, "WebSocket proxy is stopping"}
	}
	if !ok {
		return configured, nil, &websocketFailure{http.StatusBadGateway, "WebSocket target is not available"}
	}
	if current, scoped := ctx.Value(websocketEdgeContextKey{}).(*edge); scoped && m.edges[edgeKey(scope, source, service)] != current {
		return configured, nil, &websocketFailure{http.StatusServiceUnavailable, "WebSocket edge is stopping"}
	}
	if configured.provider == model.ProviderRemote && configured.writePolicy != model.WriteReadWrite {
		return configured, nil, &websocketFailure{http.StatusForbidden, "remote target is read-only"}
	}
	if configured.provider == model.ProviderMock {
		return configured, nil, &websocketFailure{http.StatusNotImplemented, "WebSockets are not supported by the mock provider"}
	}
	if len(m.websocketSessions) >= maximumWebSocketSessions {
		return configured, nil, &websocketFailure{http.StatusServiceUnavailable, "WebSocket connection limit reached"}
	}
	sessionContext, cancel := context.WithCancel(ctx)
	s := &websocketSession{scope: scope, source: source, target: service, generation: configured.generation,
		ctx: sessionContext, cancel: cancel, done: make(chan struct{})}
	s.stopCancellation = context.AfterFunc(sessionContext, s.close)
	m.websocketSessions[s] = struct{}{}
	return configured, s, nil
}

func (m *Manager) releaseWebSocket(s *websocketSession) {
	s.stopCancellation()
	s.close()
	m.mu.Lock()
	delete(m.websocketSessions, s)
	close(s.done)
	m.mu.Unlock()
}

// invalidateWebSocketsLocked prevents late attachment before routing changes
// become visible. Callers close the returned sessions after releasing m.mu.
func (m *Manager) invalidateWebSocketsLocked(scope, service string) []*websocketSession {
	var sessions []*websocketSession
	for s := range m.websocketSessions {
		if (scope == "" || s.scope == scope) && (service == "" || s.source == service || s.target == service) {
			s.invalidate()
			sessions = append(sessions, s)
		}
	}
	return sessions
}

func closeWebSockets(sessions []*websocketSession) {
	for _, s := range sessions {
		s.close()
	}
}

func waitWebSockets(ctx context.Context, sessions []*websocketSession) {
	for _, s := range sessions {
		select {
		case <-s.done:
		case <-ctx.Done():
			return
		}
	}
}

func sameWebSocketTarget(a, b target) bool {
	if a.provider != b.provider || a.address != b.address || a.writePolicy != b.writePolicy || a.classification != b.classification {
		return false
	}
	if a.baseURL == nil || b.baseURL == nil {
		return a.baseURL == nil && b.baseURL == nil
	}
	return a.baseURL.String() == b.baseURL.String()
}

func (m *Manager) setTarget(scope, service string, configured target) bool {
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		return false
	}
	key := targetKey(scope, service)
	current, exists := m.targets[key]
	var closing []*websocketSession
	var replays []*replayAttempt
	if exists && sameWebSocketTarget(current, configured) {
		configured.generation = current.generation
	} else {
		m.targetGeneration++
		configured.generation = m.targetGeneration
		closing = m.invalidateWebSocketsLocked(scope, service)
		replays = m.invalidateReplaysLocked(scope, service)
	}
	m.targets[key] = configured
	m.mu.Unlock()
	closeWebSockets(closing)
	closeReplays(replays)
	return true
}
