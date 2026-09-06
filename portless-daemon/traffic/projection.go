package traffic

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

const projectionBatchDelay = 50 * time.Millisecond

var errStoreClosed = errors.New("traffic store is closed")
var errEnvironmentDisposed = errors.New("traffic environment was removed")

// TraceQuery selects summary metadata before applying its result limit.
type TraceQuery struct {
	Service, Source, Target string
	IncludeBackground       bool
	Limit                   int
}

// TraceSnapshot is an authoritative filtered projection at an environment-local
// revision and exchange high-water mark. Revisions reset with the daemon.
type TraceSnapshot struct {
	Traces          []model.TrafficTrace
	Revision        uint64
	ThroughSequence int64
}

type projectionCache struct {
	revision, generation uint64
	throughSequence      int64
	traces               []projectedTrace
	byNumber             map[int64]int
}

func emptyProjection(revision, generation uint64, sequence int64) *projectionCache {
	return &projectionCache{revision: revision, generation: generation, throughSequence: sequence, byNumber: make(map[int64]int)}
}

func (s *Store) schedule(window *trafficWindow, urgent bool) {
	select {
	case <-s.done:
		return
	default:
	}
	due := time.Now()
	if !urgent {
		due = due.Add(projectionBatchDelay)
	}
	s.pendingMu.Lock()
	select {
	case <-s.done:
		s.pendingMu.Unlock()
		return
	default:
	}
	if current, exists := s.pending[window]; !exists || due.Before(current) {
		s.pending[window] = due
	}
	s.pendingMu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Store) scheduleProjections() {
	defer s.workers.Done()
	for {
		var next *trafficWindow
		var due time.Time
		s.pendingMu.Lock()
		for window, candidate := range s.pending {
			if next == nil || candidate.Before(due) {
				next, due = window, candidate
			}
		}
		ready := next != nil && !time.Now().Before(due)
		if ready {
			delete(s.pending, next)
		}
		s.pendingMu.Unlock()
		if ready {
			select {
			case s.jobs <- next:
			case <-s.done:
				return
			}
			continue
		}
		if next == nil {
			select {
			case <-s.wake:
			case <-s.done:
				return
			}
			continue
		}
		timer := time.NewTimer(time.Until(due))
		select {
		case <-timer.C:
		case <-s.wake:
		case <-s.done:
			timer.Stop()
			return
		}
		timer.Stop()
	}
}

func (s *Store) projectionWorker() {
	defer s.workers.Done()
	for {
		select {
		case <-s.done:
			return
		case window := <-s.jobs:
			s.buildWindow(window)
		}
	}
}

func (s *Store) buildWindow(window *trafficWindow) {
	window.mu.Lock()
	if window.disposed || window.building || window.cache.revision >= window.revision {
		window.mu.Unlock()
		return
	}
	window.building = true
	revision, generation, sequence := window.revision, window.generation, window.sequence
	inputs := make([]traceInput, window.count)
	for i := range inputs {
		inputs[i] = window.ring[(window.head+i)%len(window.ring)].input
	}
	earliest := make(map[string]time.Time)
	for _, request := range window.active {
		if current, exists := earliest[request.target]; !exists || request.started.Before(current) {
			earliest[request.target] = request.started
		}
	}
	previous := window.cache
	window.mu.Unlock()

	s.builds.Add(1)
	traces := s.project(inputs)
	cache := &projectionCache{revision: revision, generation: generation, throughSequence: sequence, traces: traces, byNumber: make(map[int64]int, len(traces))}
	updates := make([]model.TrafficTrace, 0)
	for index := range traces {
		trace := &traces[index]
		if started, exists := earliest[trace.summary.Source]; exists && trace.summary.Protocol == model.ProtocolTCP && !started.After(trace.summary.StartedAt) {
			trace.summary.Provisional = true
		}
		trace.summary.Revision = revision
		cache.byNumber[trace.summary.Number] = index
		oldIndex, exists := previous.byNumber[trace.summary.Number]
		if exists && previous.generation == generation && tracesUnchanged(*trace, previous.traces[oldIndex]) {
			trace.summary.Revision = previous.traces[oldIndex].summary.Revision
		} else {
			updates = append(updates, trace.summary)
		}
	}

	window.mu.Lock()
	window.building = false
	valid := !window.disposed && window.generation == generation && window.cache.revision < revision
	select {
	case <-s.done:
		valid = false
	default:
	}
	if valid {
		window.cache = cache
		close(window.changed)
		window.changed = make(chan struct{})
	}
	more, urgent := !window.disposed && window.cache.revision < window.revision, window.wanted > window.cache.revision
	window.mu.Unlock()
	if valid && s.broker != nil {
		window.publishMu.Lock()
		window.mu.RLock()
		valid = !window.disposed && window.generation == generation
		window.mu.RUnlock()
		if valid {
			for _, trace := range updates {
				s.broker.Publish(events.Event{Type: "traffic.trace", Project: trace.Project, Environment: trace.Environment, Data: trace})
			}
		}
		window.publishMu.Unlock()
	}
	if more {
		s.schedule(window, urgent)
	}
}

func tracesUnchanged(current, previous projectedTrace) bool {
	// Retained exchanges are immutable. Equal member/parent/group metadata plus
	// provisional state implies equal summary fields, including the root.
	return current.summary.Provisional == previous.summary.Provisional && slices.Equal(current.spans, previous.spans)
}

func (s *Store) projection(ctx context.Context, window *trafficWindow) (*projectionCache, error) {
	window.mu.RLock()
	required := window.revision
	window.mu.RUnlock()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case <-s.done:
			return nil, errStoreClosed
		default:
		}
		window.mu.Lock()
		if window.disposed {
			window.mu.Unlock()
			return nil, errEnvironmentDisposed
		}
		if window.cache.revision >= required && window.cache.generation == window.generation {
			cache := window.cache
			window.mu.Unlock()
			return cache, nil
		}
		window.wanted = max(window.wanted, required)
		changed := window.changed
		window.mu.Unlock()
		s.schedule(window, true)
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
			return nil, errStoreClosed
		}
	}
}

// Traces returns cached metadata filtered before limiting, sharing projection
// work with other readers and respecting the caller's cancellation deadline.
func (s *Store) Traces(ctx context.Context, scope string, query TraceQuery) (TraceSnapshot, error) {
	cache, err := s.projection(ctx, s.window(scope))
	if err != nil {
		return TraceSnapshot{}, err
	}
	limit := query.Limit
	if limit <= 0 || limit > len(cache.traces) {
		limit = len(cache.traces)
	}
	result := TraceSnapshot{Revision: cache.revision, ThroughSequence: cache.throughSequence, Traces: make([]model.TrafficTrace, 0, limit)}
	for _, trace := range cache.traces {
		if !traceMatchesQuery(trace, query) {
			continue
		}
		result.Traces = append(result.Traces, trace.summary)
		if len(result.Traces) == limit {
			break
		}
	}
	return result, nil
}

func traceMatchesQuery(trace projectedTrace, query TraceQuery) bool {
	if trace.summary.Background && !query.IncludeBackground {
		return false
	}
	if query.Service != "" {
		if _, exists := trace.services[query.Service]; !exists {
			return false
		}
	}
	if query.Source == "" && query.Target == "" {
		return true
	}
	for edge := range trace.edges {
		if query.Source != "" && edge.source != query.Source {
			continue
		}
		if query.Target != "" && edge.target != query.Target {
			continue
		}
		if query.Service != "" && edge.source != query.Service && edge.target != query.Service {
			continue
		}
		return true
	}
	return false
}
