package traffic

import (
	"container/heap"
	"slices"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

// traceInput contains correlation metadata only. Cached projections never own
// headers, bodies, or decoded message payloads.
type traceInput struct {
	sequence                             int64
	project, environment, source, target string
	traceID, spanID, parentSpanID        string
	started, completed                   time.Time
	protocol                             model.Protocol
	method, requestTarget                string
	status                               int
	error, faulted, background           bool
	session, transaction                 uint64
}

func traceInputFor(exchange model.TrafficExchange) traceInput {
	requestTarget := exchange.RequestTarget
	if requestTarget == "" {
		requestTarget = exchange.Path
	}
	input := traceInput{
		sequence: exchange.Sequence, project: exchange.Project, environment: exchange.Environment,
		source: exchange.Source, target: exchange.Target, traceID: exchange.TraceID,
		spanID: exchange.SpanID, parentSpanID: exchange.ParentSpanID,
		started: exchange.StartedAt, completed: exchange.CompletedAt, protocol: exchange.Protocol,
		method: exchange.Method, requestTarget: requestTarget, status: exchange.Status,
		error: exchange.Error != "" || exchange.Status >= 500, faulted: exchange.Fault != "",
		background: backgroundExchange(exchange),
	}
	if exchange.TCP != nil {
		input.session, input.transaction = exchange.TCP.SessionSequence, exchange.TCP.TransactionSequence
	}
	return input
}

type traceNode struct {
	input       *traceInput
	parent      int
	correlation model.TrafficCorrelation
}

type projectedSpan struct {
	sequence, parent int64
	depth            int
	offset           int64
	correlation      model.TrafficCorrelation
	transactionGroup int
}

type serviceEdge struct{ source, target string }

type projectedTrace struct {
	summary  model.TrafficTrace
	spans    []projectedSpan
	services map[string]struct{}
	edges    map[serviceEdge]struct{}
}

type traceUnion struct{ parents, ranks []int }

func (set *traceUnion) find(value int) int {
	for value != set.parents[value] {
		set.parents[value] = set.parents[set.parents[value]]
		value = set.parents[value]
	}
	return value
}

func (set *traceUnion) union(left, right int) {
	left, right = set.find(left), set.find(right)
	if left == right {
		return
	}
	if set.ranks[left] < set.ranks[right] {
		left, right = right, left
	}
	set.parents[right] = left
	if set.ranks[left] == set.ranks[right] {
		set.ranks[left]++
	}
}

type parentExpiry struct {
	index     int
	completed time.Time
}
type parentExpirations []parentExpiry

// Len returns the number of parent intervals awaiting expiry.
func (h parentExpirations) Len() int { return len(h) }

// Less orders parent intervals by completion time.
func (h parentExpirations) Less(i, j int) bool { return h[i].completed.Before(h[j].completed) }

// Swap exchanges two heap entries.
func (h parentExpirations) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push appends a parent interval for container/heap.
func (h *parentExpirations) Push(value any) { *h = append(*h, value.(parentExpiry)) }

// Pop removes the final heap entry for container/heap.
func (h *parentExpirations) Pop() any {
	values := *h
	value := values[len(values)-1]
	*h = values[:len(values)-1]
	return value
}

// buildProjection consumes an owned metadata slice, sorting it in place. Parent
// candidates are visited at most twice per child, even with heavy overlap.
func buildProjection(inputs []traceInput) []projectedTrace {
	slices.SortFunc(inputs, func(left, right traceInput) int {
		if order := left.started.Compare(right.started); order != 0 {
			return order
		}
		if left.sequence < right.sequence {
			return -1
		}
		if left.sequence > right.sequence {
			return 1
		}
		return 0
	})
	nodes := make([]traceNode, len(inputs))
	set := traceUnion{parents: make([]int, len(inputs)), ranks: make([]int, len(inputs))}
	spanOwners := make(map[string]int, len(inputs))
	traceOwners := make(map[string]int, len(inputs))
	for index := range inputs {
		input := &inputs[index]
		nodes[index] = traceNode{input: input, parent: -1, correlation: model.TrafficCorrelationPartial}
		set.parents[index] = index
		if input.traceID != "" {
			if owner, exists := traceOwners[input.traceID]; exists {
				set.union(owner, index)
			} else {
				traceOwners[input.traceID] = index
			}
			if input.spanID != "" {
				spanOwners[input.traceID+"\x00"+input.spanID] = index
			}
		}
	}
	active := make(map[string]map[int]struct{})
	expirations := make(parentExpirations, 0, len(inputs))
	for first := 0; first < len(inputs); {
		end := first + 1
		for end < len(inputs) && inputs[first].started.Equal(inputs[end].started) {
			end++
		}
		// Equal-time parents must all be eligible, regardless of completion order.
		for index := first; index < end; index++ {
			input := &inputs[index]
			if active[input.target] == nil {
				active[input.target] = make(map[int]struct{})
			}
			active[input.target][index] = struct{}{}
			heap.Push(&expirations, parentExpiry{index, input.completed})
		}
		for len(expirations) > 0 && expirations[0].completed.Before(inputs[first].started) {
			expired := heap.Pop(&expirations).(parentExpiry)
			delete(active[inputs[expired.index].target], expired.index)
		}
		for index := first; index < end; index++ {
			node, input := &nodes[index], &inputs[index]
			if parent, exists := spanOwners[input.traceID+"\x00"+input.parentSpanID]; input.traceID != "" && input.parentSpanID != "" && exists && parent != index {
				node.parent, node.correlation = parent, model.TrafficCorrelationExact
				set.union(parent, index)
				continue
			}
			parent, count := -1, 0
			for candidate := range active[input.source] {
				if candidate == index {
					continue
				}
				parent = candidate
				count++
				if count == 2 {
					break
				}
			}
			switch count {
			case 0:
				if input.source == "external" {
					node.correlation = model.TrafficCorrelationExact
				}
			case 1:
				node.parent, node.correlation = parent, model.TrafficCorrelationInferred
				set.union(parent, index)
			default:
				node.correlation = model.TrafficCorrelationAmbiguous
			}
		}
		first = end
	}
	depths := traceDepths(nodes)
	components := make(map[int][]int)
	for index := range nodes {
		root := set.find(index)
		components[root] = append(components[root], index)
	}
	traces := make([]projectedTrace, 0, len(components))
	for _, members := range components {
		traces = append(traces, projectTrace(members, nodes, depths))
	}
	slices.SortFunc(traces, func(left, right projectedTrace) int {
		if order := right.summary.StartedAt.Compare(left.summary.StartedAt); order != 0 {
			return order
		}
		if left.summary.Number > right.summary.Number {
			return -1
		}
		if left.summary.Number < right.summary.Number {
			return 1
		}
		return 0
	})
	return traces
}

func projectTrace(members []int, nodes []traceNode, depths []int) projectedTrace {
	root := members[0]
	for _, index := range members {
		if nodes[index].parent == -1 {
			root = index
			break
		}
	}
	for _, index := range members {
		if nodes[index].parent == -1 && nodes[index].input.source == "external" {
			root = index
			break
		}
	}
	input := nodes[root].input
	first := nodes[members[0]].input
	trace := projectedTrace{
		summary: model.TrafficTrace{Project: input.project, Environment: input.environment,
			Number: first.sequence, LastSequence: first.sequence, TraceID: input.traceID, RootSequence: input.sequence,
			Protocol: input.protocol, StartedAt: first.started, CompletedAt: first.completed,
			Method: input.method, RequestTarget: input.requestTarget, Source: input.source, Target: input.target,
			Status: input.status, Background: input.background, SpanCount: len(members), Correlation: model.TrafficCorrelationExact},
		spans: make([]projectedSpan, 0, len(members)), services: make(map[string]struct{}), edges: make(map[serviceEdge]struct{}),
	}
	groups := make(map[[2]uint64]int)
	for _, index := range members {
		node := &nodes[index]
		item := node.input
		trace.summary.Number = min(trace.summary.Number, item.sequence)
		trace.summary.LastSequence = max(trace.summary.LastSequence, item.sequence)
		if item.completed.After(trace.summary.CompletedAt) {
			trace.summary.CompletedAt = item.completed
		}
		trace.summary.Error = trace.summary.Error || item.error
		trace.summary.Faulted = trace.summary.Faulted || item.faulted
		trace.summary.Correlation = weakerCorrelation(trace.summary.Correlation, node.correlation)
		span := projectedSpan{sequence: item.sequence, depth: depths[index], offset: item.started.Sub(first.started).Milliseconds(), correlation: node.correlation}
		if node.parent != -1 {
			span.parent = nodes[node.parent].input.sequence
		}
		if index != root && node.parent == -1 && span.correlation != model.TrafficCorrelationAmbiguous {
			span.correlation = model.TrafficCorrelationPartial
			trace.summary.Correlation = weakerCorrelation(trace.summary.Correlation, span.correlation)
		}
		if item.session != 0 && item.transaction != 0 {
			key := [2]uint64{item.session, item.transaction}
			if groups[key] == 0 {
				groups[key] = len(groups) + 1
			}
			span.transactionGroup = groups[key]
		}
		trace.spans = append(trace.spans, span)
		trace.services[item.source] = struct{}{}
		trace.services[item.target] = struct{}{}
		trace.edges[serviceEdge{item.source, item.target}] = struct{}{}
	}
	trace.summary.DurationMS = trace.summary.CompletedAt.Sub(trace.summary.StartedAt).Milliseconds()
	return trace
}

func traceDepths(nodes []traceNode) []int {
	depths, visiting := make([]int, len(nodes)), make([]int, len(nodes))
	for i := range depths {
		depths[i] = -1
		visiting[i] = -1
	}
	path := make([]int, 0)
	for start := range nodes {
		if depths[start] >= 0 {
			continue
		}
		path = path[:0]
		current := start
		for current != -1 && depths[current] < 0 && visiting[current] < 0 {
			visiting[current] = len(path)
			path = append(path, current)
			current = nodes[current].parent
		}
		if current != -1 && depths[current] < 0 {
			cycleStart := visiting[current]
			for _, index := range path[cycleStart:] {
				depths[index] = len(path) - cycleStart - 1
			}
		}
		for i := len(path) - 1; i >= 0; i-- {
			index := path[i]
			if depths[index] < 0 {
				if nodes[index].parent == -1 {
					depths[index] = 0
				} else {
					depths[index] = depths[nodes[index].parent] + 1
				}
			}
			visiting[index] = -1
		}
	}
	return depths
}

func weakerCorrelation(current, candidate model.TrafficCorrelation) model.TrafficCorrelation {
	strength := func(value model.TrafficCorrelation) int {
		switch value {
		case model.TrafficCorrelationInferred:
			return 1
		case model.TrafficCorrelationPartial:
			return 2
		case model.TrafficCorrelationAmbiguous:
			return 3
		default:
			return 0
		}
	}
	if strength(candidate) > strength(current) {
		return candidate
	}
	return current
}

func backgroundExchange(exchange model.TrafficExchange) bool {
	if exchange.Error != "" || exchange.Fault != "" || exchange.Status >= 500 || exchange.TCP != nil && exchange.TCP.Outcome == model.TrafficTCPOutcomeError {
		return false
	}
	if exchange.Background || exchange.RequestKind == model.TrafficRequestSubresource {
		return true
	}
	requestTarget := strings.ToLower(exchange.RequestTarget)
	return strings.HasPrefix(requestTarget, "/favicon.") || strings.HasPrefix(requestTarget, "/robots.txt")
}
