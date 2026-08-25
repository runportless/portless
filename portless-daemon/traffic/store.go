// Package traffic owns bounded live exchange retention, environment-local
// sequencing, trace projection, and traffic notifications.
package traffic

import (
	"sort"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

const defaultExchangeLimit = 5000

// Store retains a bounded live traffic window for each environment and
// publishes metadata notifications without blocking application proxying.
type Store struct {
	mu                 sync.RWMutex
	broker             *events.Broker
	sequences          map[string]int64
	exchanges          map[string][]model.TrafficExchange
	activeHTTPRequests map[uint64]activeHTTPRequest
	nextHTTPRequest    uint64
	limit              int
}

type activeHTTPRequest struct {
	scope   string
	target  string
	started time.Time
}

// NewStore constructs an empty traffic store backed by broker notifications.
func NewStore(broker *events.Broker) *Store {
	return &Store{
		broker: broker, sequences: make(map[string]int64),
		exchanges: make(map[string][]model.TrafficExchange), activeHTTPRequests: make(map[uint64]activeHTTPRequest), limit: defaultExchangeLimit,
	}
}

// BeginHTTPRequest registers an active HTTP request that may become the
// parent of a completed dependency exchange.
func (s *Store) BeginHTTPRequest(scope, target string, started time.Time) uint64 {
	s.mu.Lock()
	s.nextHTTPRequest++
	identifier := s.nextHTTPRequest
	s.activeHTTPRequests[identifier] = activeHTTPRequest{scope: scope, target: target, started: started}
	s.mu.Unlock()
	return identifier
}

// AbandonHTTPRequest removes an active HTTP request that completed without a
// retained exchange.
func (s *Store) AbandonHTTPRequest(identifier uint64) {
	if identifier == 0 {
		return
	}
	s.mu.Lock()
	delete(s.activeHTTPRequests, identifier)
	s.mu.Unlock()
}

// CompleteHTTPRequest atomically removes an active HTTP request and retains
// its completed exchange so trace projections never observe a gap between the
// two states.
func (s *Store) CompleteHTTPRequest(identifier uint64, exchange model.TrafficExchange) model.TrafficExchange {
	return s.addExchange(identifier, exchange)
}

// AddExchange assigns an environment-local sequence, retains the completed
// exchange, and publishes exchange and trace updates.
func (s *Store) AddExchange(exchange model.TrafficExchange) model.TrafficExchange {
	return s.addExchange(0, exchange)
}

func (s *Store) addExchange(completedHTTPRequest uint64, exchange model.TrafficExchange) model.TrafficExchange {
	scope := model.EnvironmentSelector(exchange.Project, exchange.Environment)
	exchange = cloneExchange(exchange)
	s.mu.Lock()
	if completedHTTPRequest != 0 {
		delete(s.activeHTTPRequests, completedHTTPRequest)
	}
	s.sequences[scope]++
	exchange.Sequence = s.sequences[scope]
	items := append(s.exchanges[scope], exchange)
	if len(items) > s.limit {
		items = append([]model.TrafficExchange(nil), items[len(items)-s.limit:]...)
	}
	s.exchanges[scope] = items
	traces := buildTraces(items)
	markProvisionalTraces(traces, activeRequestsForScope(s.activeHTTPRequests, scope))
	trace := traceContaining(traces, exchange.Sequence)
	s.mu.Unlock()

	if s.broker != nil {
		s.broker.Publish(events.Event{Type: "traffic.exchange", Project: exchange.Project, Environment: exchange.Environment, Data: exchange})
		if trace.Number > 0 {
			trace.Spans = nil
			s.broker.Publish(events.Event{Type: "traffic.trace", Project: exchange.Project, Environment: exchange.Environment, Data: trace})
		}
	}
	return cloneExchange(exchange)
}

// EnsureSequence restores an environment's durable traffic high-water mark so
// new live exchanges cannot reuse a retained recording sequence.
func (s *Store) EnsureSequence(scope string, sequence int64) {
	s.mu.Lock()
	if s.sequences[scope] < sequence {
		s.sequences[scope] = sequence
	}
	s.mu.Unlock()
}

// Clear removes the live exchange window for an environment while preserving
// its sequence high-water mark and any separately persisted recordings.
func (s *Store) Clear(project, environment string) (int, int64) {
	scope := model.EnvironmentSelector(project, environment)
	s.mu.Lock()
	cleared := len(s.exchanges[scope])
	throughSequence := s.sequences[scope]
	delete(s.exchanges, scope)
	s.mu.Unlock()

	if s.broker != nil {
		s.broker.Publish(events.Event{
			Type: "traffic.cleared", Project: project, Environment: environment,
			Data: map[string]any{"cleared": cleared, "throughSequence": throughSequence},
		})
	}
	return cleared, throughSequence
}

// RecentExchanges returns newest-completed-first retained exchanges for scope.
func (s *Store) RecentExchanges(scope string, limit int) []model.TrafficExchange {
	s.mu.RLock()
	items := s.exchanges[scope]
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	result := make([]model.TrafficExchange, 0, limit)
	for index := len(items) - 1; index >= len(items)-limit; index-- {
		result = append(result, cloneExchange(items[index]))
	}
	s.mu.RUnlock()
	return result
}

// Exchange returns one retained live exchange by environment-local sequence.
func (s *Store) Exchange(scope string, sequence int64) (model.TrafficExchange, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for index := len(s.exchanges[scope]) - 1; index >= 0; index-- {
		if s.exchanges[scope][index].Sequence == sequence {
			return cloneExchange(s.exchanges[scope][index]), true
		}
	}
	return model.TrafficExchange{}, false
}

// Traces returns newest-started-first trace projections rebuilt from the
// current bounded exchange window.
func (s *Store) Traces(scope string, limit int) []model.TrafficTrace {
	items, active := s.traceInputs(scope)
	traces := buildTraces(items)
	markProvisionalTraces(traces, active)
	sort.SliceStable(traces, func(left, right int) bool {
		if traces[left].StartedAt.Equal(traces[right].StartedAt) {
			return traces[left].Number > traces[right].Number
		}
		return traces[left].StartedAt.After(traces[right].StartedAt)
	})
	if limit > 0 && limit < len(traces) {
		traces = traces[:limit]
	}
	return traces
}

// Trace returns a full trace projection by environment-local trace number.
func (s *Store) Trace(scope string, number int64) (model.TrafficTrace, bool) {
	items, active := s.traceInputs(scope)
	traces := buildTraces(items)
	markProvisionalTraces(traces, active)
	for _, trace := range traces {
		if trace.Number == number {
			return trace, true
		}
	}
	return model.TrafficTrace{}, false
}

func (s *Store) traceInputs(scope string) ([]model.TrafficExchange, []activeHTTPRequest) {
	s.mu.RLock()
	items := append([]model.TrafficExchange(nil), s.exchanges[scope]...)
	active := activeRequestsForScope(s.activeHTTPRequests, scope)
	s.mu.RUnlock()
	return items, active
}

func activeRequestsForScope(requests map[uint64]activeHTTPRequest, scope string) []activeHTTPRequest {
	active := make([]activeHTTPRequest, 0, len(requests))
	for _, request := range requests {
		if request.scope == scope {
			active = append(active, request)
		}
	}
	return active
}

func markProvisionalTraces(traces []model.TrafficTrace, active []activeHTTPRequest) {
	for index := range traces {
		trace := &traces[index]
		if trace.Protocol != model.ProtocolTCP {
			continue
		}
		for _, request := range active {
			if request.target == trace.Source && !request.started.After(trace.StartedAt) {
				trace.Provisional = true
				break
			}
		}
	}
}

func traceContaining(traces []model.TrafficTrace, sequence int64) model.TrafficTrace {
	for _, trace := range traces {
		for _, span := range trace.Spans {
			if span.Exchange.Sequence == sequence {
				return trace
			}
		}
	}
	return model.TrafficTrace{}
}

func cloneExchange(exchange model.TrafficExchange) model.TrafficExchange {
	exchange.RequestHeaders = cloneHeaders(exchange.RequestHeaders)
	exchange.ResponseHeaders = cloneHeaders(exchange.ResponseHeaders)
	return exchange
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if headers == nil {
		return nil
	}
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		result[name] = append([]string(nil), values...)
	}
	return result
}
