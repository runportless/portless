package proxy

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
)

type partialMock struct {
	scenario string
	compiled *mocks.CompiledScenario
	admitted bool
}

type routingDecision struct {
	upstream  target
	available bool
	response  *mocks.Response
	guarded   bool
}

// SetPartialMock atomically publishes a compiled policy for all scenario targets.
// A nil compiled policy guards ownership while recovery reconstructs the routes.
func (m *Manager) SetPartialMock(scope, scenario string, services []string, compiled *mocks.CompiledScenario) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return errors.New("proxy manager is closed")
	}
	if m.partialMocks == nil {
		m.partialMocks = make(map[string]partialMock)
	}
	if m.routingRevisions == nil {
		m.routingRevisions = make(map[string]uint64)
	}
	m.routingRevision++
	for _, service := range services {
		key := targetKey(scope, service)
		previous := m.partialMocks[key]
		if previous.scenario != "" && !strings.EqualFold(previous.scenario, scenario) {
			return errors.New("service already has a partial mock owner")
		}
	}
	for _, service := range services {
		key := targetKey(scope, service)
		_, available := m.targets[key]
		m.partialMocks[key] = partialMock{scenario: scenario, compiled: compiled, admitted: available || m.partialMocks[key].admitted}
		m.routingRevisions[key] = m.routingRevision
	}
	return nil
}

// RemovePartialMock withdraws a scenario policy without changing any provider or session.
func (m *Manager) RemovePartialMock(scope, scenario string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routingRevision++
	for key, policy := range m.partialMocks {
		if strings.HasPrefix(key, strings.ToLower(scope)+"\x00") && strings.EqualFold(policy.scenario, scenario) {
			delete(m.partialMocks, key)
			m.routingRevisions[key] = m.routingRevision
		}
	}
}

// PartialMockApplied reports whether ownership has a compiled runtime policy.
func (m *Manager) PartialMockApplied(scope, service, scenario string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.partialMocks[targetKey(scope, service)]
	return !m.closed.Load() && ok && p.compiled != nil && strings.EqualFold(p.scenario, scenario)
}

// StopTarget closes explicit service admission, retaining desired partial configuration.
func (m *Manager) StopTarget(scope, service string) {
	m.mu.Lock()
	key := targetKey(scope, service)
	if p, ok := m.partialMocks[key]; ok {
		p.admitted = false
		m.partialMocks[key] = p
	}
	delete(m.targets, key)
	closing := m.invalidateWebSocketsLocked(scope, service)
	replays := m.invalidateReplaysLocked(scope, service)
	m.mu.Unlock()
	closeWebSockets(closing)
	closeReplays(replays)
}

// ResumePartialMockAdmission restores saved admission for an unexpectedly failed provider.
func (m *Manager) ResumePartialMockAdmission(scope, service string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := targetKey(scope, service)
	if p, ok := m.partialMocks[key]; ok {
		p.admitted = true
		m.partialMocks[key] = p
	}
}

// InvalidateMockRouting changes replay identity without changing provider generations.
func (m *Manager) InvalidateMockRouting(scope, service string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.routingRevisions == nil {
		m.routingRevisions = make(map[string]uint64)
	}
	m.routingRevision++
	m.routingRevisions[targetKey(scope, service)] = m.routingRevision
}

func decidePartial(upstream target, available bool, policy partialMock, service, method, path string, query url.Values, upgrade bool) routingDecision {
	result := routingDecision{upstream: upstream, available: available}
	if policy.scenario == "" || !policy.admitted {
		return result
	}
	result.upstream.mockScenario = policy.scenario
	if policy.compiled == nil {
		result.guarded = true
		result.upstream.mockOutcome = "blocked"
		return result
	}
	if !upgrade {
		response, err := policy.compiled.MatchResponse(service, method, path, query)
		if err == nil {
			result.response = &response
			result.available = true
			result.upstream.provider = model.ProviderMock
			result.upstream.classification = ""
			result.upstream.mockRoute = response.Route
			result.upstream.mockOutcome = "mocked"
			return result
		}
		if !errors.Is(err, mocks.ErrNoMatch) {
			result.guarded = true
			result.upstream.mockOutcome = "blocked"
			return result
		}
	}
	result.upstream.mockOutcome = "forwarded"
	return result
}

func (m *Manager) requestDecision(scope, service string, request *http.Request) routingDecision {
	m.mu.RLock()
	upstream, available := m.targets[targetKey(scope, service)]
	policy := m.partialMocks[targetKey(scope, service)]
	m.mu.RUnlock()
	return decidePartial(upstream, available, policy, service, request.Method, request.URL.Path, request.URL.Query(), isHTTPUpgrade(request.Header))
}

// InspectMockPreviewUpgrade validates an upgrade using the runtime handshake rules without I/O.
func InspectMockPreviewUpgrade(request *http.Request) (bool, error) {
	if !isHTTPUpgrade(request.Header) {
		return false, nil
	}
	if failure := validateWebSocketRequest(request); failure != nil {
		return true, errors.New(failure.message)
	}
	return true, nil
}
