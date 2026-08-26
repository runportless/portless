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

const (
	defaultExchangeLimit = 5000
	defaultPayloadLimit  = 64 << 20
)

// Store retains a bounded live traffic window for each environment and
// publishes metadata notifications without blocking application proxying.
type Store struct {
	mu                 sync.RWMutex
	broker             *events.Broker
	sequences          map[string]int64
	exchanges          map[string][]model.TrafficExchange
	payloadBytes       map[string]int64
	activeHTTPRequests map[uint64]activeHTTPRequest
	nextHTTPRequest    uint64
	limit              int
	payloadLimit       int64
	lastPrunedAt       *time.Time
}

// RetentionStats summarizes the live in-memory traffic window and its fixed
// per-environment bounds.
type RetentionStats struct {
	Exchanges         int
	PayloadBytes      int64
	ExchangeLimit     int
	PayloadLimitBytes int64
	LastPrunedAt      *time.Time
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
		exchanges: make(map[string][]model.TrafficExchange), payloadBytes: make(map[string]int64),
		activeHTTPRequests: make(map[uint64]activeHTTPRequest), limit: defaultExchangeLimit, payloadLimit: defaultPayloadLimit,
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
	exchange.Background = backgroundExchange(exchange)
	exchange = cloneExchange(exchange)
	s.mu.Lock()
	if completedHTTPRequest != 0 {
		delete(s.activeHTTPRequests, completedHTTPRequest)
	}
	s.sequences[scope]++
	exchange.Sequence = s.sequences[scope]
	items := append(s.exchanges[scope], exchange)
	payloadBytes := s.payloadBytes[scope] + exchangePayloadBytes(exchange)
	pruned := false
	for len(items) > 0 && (len(items) > s.limit || payloadBytes > s.payloadLimit) {
		payloadBytes -= exchangePayloadBytes(items[0])
		items = items[1:]
		pruned = true
	}
	if pruned {
		prunedAt := time.Now().UTC()
		s.lastPrunedAt = &prunedAt
	}
	s.exchanges[scope] = append([]model.TrafficExchange(nil), items...)
	s.payloadBytes[scope] = payloadBytes
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

// RetentionStats returns aggregate live traffic usage without copying retained
// exchanges or application payloads.
func (s *Store) RetentionStats() RetentionStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := RetentionStats{ExchangeLimit: s.limit, PayloadLimitBytes: s.payloadLimit}
	for _, exchanges := range s.exchanges {
		result.Exchanges += len(exchanges)
	}
	for _, bytes := range s.payloadBytes {
		result.PayloadBytes += bytes
	}
	if s.lastPrunedAt != nil {
		value := *s.lastPrunedAt
		result.LastPrunedAt = &value
	}
	return result
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
	delete(s.payloadBytes, scope)
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
	if exchange.TCP != nil {
		tcp := *exchange.TCP
		tcp.RequestMessages = cloneMessages(tcp.RequestMessages)
		tcp.ResponseMessages = cloneMessages(tcp.ResponseMessages)
		exchange.TCP = &tcp
	}
	return exchange
}

func cloneMessages(messages []model.TrafficMessage) []model.TrafficMessage {
	if messages == nil {
		return nil
	}
	result := make([]model.TrafficMessage, len(messages))
	for index, message := range messages {
		result[index] = message
		result[index].Fields = append([]model.TrafficMessageField(nil), message.Fields...)
	}
	return result
}

func exchangePayloadBytes(exchange model.TrafficExchange) int64 {
	total := int64(len(exchange.RequestBody) + len(exchange.ResponseBody))
	for _, headers := range []map[string][]string{exchange.RequestHeaders, exchange.ResponseHeaders} {
		for name, values := range headers {
			total += int64(len(name))
			for _, value := range values {
				total += int64(len(value))
			}
		}
	}
	if exchange.TCP == nil {
		return total
	}
	for _, messages := range [][]model.TrafficMessage{exchange.TCP.RequestMessages, exchange.TCP.ResponseMessages} {
		for _, message := range messages {
			total += int64(len(message.Content) + len(message.Summary) + len(message.Type) + len(message.ContentType))
			for _, field := range message.Fields {
				total += int64(len(field.Name) + len(field.Value))
			}
		}
	}
	return total
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
