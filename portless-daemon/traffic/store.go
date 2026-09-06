// Package traffic owns bounded live exchange retention, environment-local
// sequencing, trace projection, and traffic notifications.
package traffic

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

const (
	defaultExchangeLimit = 5000
	defaultPayloadLimit  = 64 << 20
)

// Store retains bounded immutable exchanges and shares asynchronously computed
// metadata projections across traffic readers.
type Store struct {
	mu           sync.RWMutex
	windows      map[string]*trafficWindow
	requests     sync.Map
	nextRequest  atomic.Uint64
	broker       *events.Broker
	limit        int
	payloadLimit int64
	project      func([]traceInput) []projectedTrace
	builds       atomic.Uint64
	pendingMu    sync.Mutex
	pending      map[*trafficWindow]time.Time
	wake         chan struct{}
	jobs         chan *trafficWindow
	done         chan struct{}
	closeOnce    sync.Once
	workers      sync.WaitGroup
}

type retainedExchange struct {
	exchange model.TrafficExchange
	input    traceInput
	bytes    int64
}

type trafficWindow struct {
	mu                           sync.RWMutex
	publishMu                    sync.Mutex
	ring                         []*retainedExchange
	head, count                  int
	entries                      map[int64]*retainedExchange
	sequence                     int64
	revision, generation, wanted uint64
	payloadBytes                 int64
	prunedAt                     *time.Time
	active                       map[uint64]activeHTTPRequest
	cache                        *projectionCache
	building                     bool
	disposed                     bool
	changed                      chan struct{}
}

type activeHTTPRequest struct {
	target  string
	started time.Time
}

// RetentionStats summarizes the live in-memory window and per-environment bounds.
type RetentionStats struct {
	Exchanges         int
	PayloadBytes      int64
	ExchangeLimit     int
	PayloadLimitBytes int64
	LastPrunedAt      *time.Time
}

// NewStore constructs bounded retention with a two-worker trace projection pool.
// The owner must call Close after stopping capture producers.
func NewStore(broker *events.Broker) *Store { return newStore(broker, buildProjection) }

func newStore(broker *events.Broker, project func([]traceInput) []projectedTrace) *Store {
	s := &Store{windows: make(map[string]*trafficWindow), broker: broker,
		limit: defaultExchangeLimit, payloadLimit: defaultPayloadLimit, project: project,
		pending: make(map[*trafficWindow]time.Time), wake: make(chan struct{}, 1),
		jobs: make(chan *trafficWindow), done: make(chan struct{})}
	s.workers.Add(3)
	go s.scheduleProjections()
	go s.projectionWorker()
	go s.projectionWorker()
	return s
}

// Close cancels pending projection work and joins the bounded CPU workers.
func (s *Store) Close() {
	s.closeOnce.Do(func() { close(s.done) })
	s.workers.Wait()
	s.pendingMu.Lock()
	clear(s.pending)
	s.pendingMu.Unlock()
	s.mu.Lock()
	clear(s.windows)
	s.mu.Unlock()
	s.requests.Clear()
}

// DisposeEnvironment releases retained traffic after an environment's producers
// have stopped and its persistent state has been successfully removed.
func (s *Store) DisposeEnvironment(scope string) {
	s.mu.Lock()
	window := s.windows[scope]
	delete(s.windows, scope)
	s.mu.Unlock()
	if window == nil {
		return
	}
	window.publishMu.Lock()
	defer window.publishMu.Unlock()
	window.mu.Lock()
	window.disposed = true
	window.generation++
	window.revision++
	clear(window.ring)
	clear(window.entries)
	window.count, window.payloadBytes = 0, 0
	for identifier := range window.active {
		s.requests.Delete(identifier)
	}
	clear(window.active)
	window.cache = emptyProjection(window.revision, window.generation, window.sequence)
	close(window.changed)
	window.changed = make(chan struct{})
	window.mu.Unlock()
	s.pendingMu.Lock()
	delete(s.pending, window)
	s.pendingMu.Unlock()
}

func (s *Store) window(scope string) *trafficWindow {
	s.mu.RLock()
	window := s.windows[scope]
	s.mu.RUnlock()
	if window != nil {
		return window
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if window = s.windows[scope]; window == nil {
		window = &trafficWindow{ring: make([]*retainedExchange, max(1, s.limit)), entries: make(map[int64]*retainedExchange),
			active: make(map[uint64]activeHTTPRequest), changed: make(chan struct{}), cache: emptyProjection(0, 0, 0)}
		s.windows[scope] = window
	}
	return window
}

// BeginHTTPRequest registers an HTTP request eligible to absorb dependency spans.
func (s *Store) BeginHTTPRequest(scope, target string, started time.Time) uint64 {
	window := s.window(scope)
	id := s.nextRequest.Add(1)
	window.mu.Lock()
	window.active[id] = activeHTTPRequest{target, started}
	window.revision++
	window.mu.Unlock()
	s.requests.Store(id, window)
	s.schedule(window, false)
	return id
}

// AbandonHTTPRequest removes an unfinished HTTP parent and updates provisional traces.
func (s *Store) AbandonHTTPRequest(identifier uint64) {
	value, exists := s.requests.LoadAndDelete(identifier)
	if !exists {
		return
	}
	window := value.(*trafficWindow)
	window.mu.Lock()
	delete(window.active, identifier)
	window.revision++
	window.mu.Unlock()
	s.schedule(window, false)
}

// CompleteHTTPRequest atomically retains a completed HTTP exchange and removes
// its active-parent registration before scheduling a shared projection.
func (s *Store) CompleteHTTPRequest(identifier uint64, exchange model.TrafficExchange) model.TrafficExchange {
	return s.addExchange(identifier, exchange)
}

// AddExchange retains an immutable exchange and immediately publishes its summary.
// Trace projection runs separately from the capture caller.
func (s *Store) AddExchange(exchange model.TrafficExchange) model.TrafficExchange {
	return s.addExchange(0, exchange)
}

func (s *Store) addExchange(identifier uint64, exchange model.TrafficExchange) model.TrafficExchange {
	exchange.Background = backgroundExchange(exchange)
	exchange = cloneExchange(exchange)
	window := s.window(model.EnvironmentSelector(exchange.Project, exchange.Environment))
	entry := &retainedExchange{exchange: exchange, bytes: exchangePayloadBytes(exchange)}
	window.mu.Lock()
	if identifier != 0 {
		delete(window.active, identifier)
		s.requests.Delete(identifier)
	}
	window.sequence++
	window.revision++
	entry.exchange.Sequence = window.sequence
	entry.input = traceInputFor(entry.exchange)
	for window.count > 0 && (window.count == len(window.ring) || window.payloadBytes+entry.bytes > s.payloadLimit) {
		window.evict()
	}
	if entry.bytes <= s.payloadLimit {
		window.ring[(window.head+window.count)%len(window.ring)] = entry
		window.count++
		window.payloadBytes += entry.bytes
		window.entries[entry.exchange.Sequence] = entry
	} else {
		pruned := time.Now().UTC()
		window.prunedAt = &pruned
	}
	window.mu.Unlock()
	if s.broker != nil {
		s.broker.Publish(events.Event{Type: "traffic.exchange", Project: exchange.Project, Environment: exchange.Environment, Data: exchangeSummary(entry.exchange)})
	}
	s.schedule(window, false)
	return cloneExchange(entry.exchange)
}

func (window *trafficWindow) evict() {
	entry := window.ring[window.head]
	delete(window.entries, entry.exchange.Sequence)
	window.payloadBytes -= entry.bytes
	window.ring[window.head] = nil
	window.head = (window.head + 1) % len(window.ring)
	window.count--
	pruned := time.Now().UTC()
	window.prunedAt = &pruned
}

// EnsureSequence restores the durable high-water mark without reusing sequences.
func (s *Store) EnsureSequence(scope string, sequence int64) {
	window := s.window(scope)
	window.mu.Lock()
	if window.sequence < sequence {
		window.sequence = sequence
		window.revision++
	}
	window.mu.Unlock()
	s.schedule(window, false)
}

// Clear removes live history and invalidates older projection work while
// preserving active requests, durable recordings, and the sequence high-water mark.
func (s *Store) Clear(project, environment string) (int, int64, uint64) {
	window := s.window(model.EnvironmentSelector(project, environment))
	window.mu.Lock()
	count, sequence := window.count, window.sequence
	window.revision++
	window.generation++
	clear(window.ring)
	clear(window.entries)
	window.head, window.count, window.payloadBytes = 0, 0, 0
	window.cache = emptyProjection(window.revision, window.generation, window.sequence)
	close(window.changed)
	window.changed = make(chan struct{})
	revision := window.revision
	window.mu.Unlock()
	if s.broker != nil {
		window.publishMu.Lock()
		s.broker.Publish(events.Event{Type: "traffic.cleared", Project: project, Environment: environment,
			Data: map[string]any{"cleared": count, "throughSequence": sequence, "revision": revision}})
		window.publishMu.Unlock()
	}
	return count, sequence, revision
}

// RetentionStats returns usage without cloning exchanges or taking a long global lock.
func (s *Store) RetentionStats() RetentionStats {
	s.mu.RLock()
	windows := make([]*trafficWindow, 0, len(s.windows))
	for _, window := range s.windows {
		windows = append(windows, window)
	}
	s.mu.RUnlock()
	result := RetentionStats{ExchangeLimit: s.limit, PayloadLimitBytes: s.payloadLimit}
	for _, window := range windows {
		window.mu.RLock()
		result.Exchanges += window.count
		result.PayloadBytes += window.payloadBytes
		if window.prunedAt != nil && (result.LastPrunedAt == nil || window.prunedAt.After(*result.LastPrunedAt)) {
			value := *window.prunedAt
			result.LastPrunedAt = &value
		}
		window.mu.RUnlock()
	}
	return result
}

func (s *Store) recent(scope string, limit int, summaries bool) []model.TrafficExchange {
	window := s.window(scope)
	window.mu.RLock()
	if limit <= 0 || limit > window.count {
		limit = window.count
	}
	entries := make([]*retainedExchange, limit)
	for i := range entries {
		entries[i] = window.ring[(window.head+window.count-1-i)%len(window.ring)]
	}
	window.mu.RUnlock()
	result := make([]model.TrafficExchange, len(entries))
	for i, entry := range entries {
		if summaries {
			result[i] = exchangeSummary(entry.exchange)
		} else {
			result[i] = cloneExchange(entry.exchange)
		}
	}
	return result
}

// RecentExchanges returns newest-completed-first defensive copies of retained detail.
func (s *Store) RecentExchanges(scope string, limit int) []model.TrafficExchange {
	return s.recent(scope, limit, false)
}

// ExchangeSummaries returns bounded metadata without cloning headers or message content.
func (s *Store) ExchangeSummaries(scope string, limit int) []model.TrafficExchange {
	return s.recent(scope, limit, true)
}

// Exchange looks up one retained exchange by sequence and copies only its detail.
func (s *Store) Exchange(scope string, sequence int64) (model.TrafficExchange, bool) {
	window := s.window(scope)
	window.mu.RLock()
	entry := window.entries[sequence]
	window.mu.RUnlock()
	if entry == nil {
		return model.TrafficExchange{}, false
	}
	return cloneExchange(entry.exchange), true
}

// Trace returns a coherent detail projection for one trace, waiting only for the
// requested revision and respecting cancellation during snapshot reconciliation.
func (s *Store) Trace(ctx context.Context, scope string, number int64) (model.TrafficTrace, bool, error) {
	window := s.window(scope)
	for {
		cache, err := s.projection(ctx, window)
		if err != nil {
			return model.TrafficTrace{}, false, err
		}
		index, exists := cache.byNumber[number]
		if !exists {
			return model.TrafficTrace{}, false, nil
		}
		trace := &cache.traces[index]
		window.mu.RLock()
		valid := !window.disposed && window.generation == cache.generation
		entries := make([]*retainedExchange, len(trace.spans))
		if valid {
			for i, span := range trace.spans {
				entries[i] = window.entries[span.sequence]
				if entries[i] == nil {
					valid = false
					break
				}
			}
		}
		window.mu.RUnlock()
		if !valid {
			if err := ctx.Err(); err != nil {
				return model.TrafficTrace{}, false, err
			}
			continue
		}
		result := trace.summary
		result.Spans = make([]model.TrafficTraceSpan, len(trace.spans))
		for i, span := range trace.spans {
			result.Spans[i] = model.TrafficTraceSpan{Exchange: cloneExchange(entries[i].exchange), ParentSequence: span.parent, Depth: span.depth, StartOffsetMS: span.offset, Correlation: span.correlation, TransactionGroup: span.transactionGroup}
		}
		return result, true, nil
	}
}
