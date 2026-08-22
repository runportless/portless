package traffic

import (
	"sort"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
)

type traceNode struct {
	exchange    model.TrafficExchange
	parent      int64
	correlation model.TrafficCorrelation
}

type disjointSet struct {
	parent map[int64]int64
}

func newDisjointSet(exchanges []model.TrafficExchange) *disjointSet {
	set := &disjointSet{parent: make(map[int64]int64, len(exchanges))}
	for _, exchange := range exchanges {
		set.parent[exchange.Sequence] = exchange.Sequence
	}
	return set
}

func (s *disjointSet) find(value int64) int64 {
	parent := s.parent[value]
	if parent != value {
		s.parent[value] = s.find(parent)
	}
	return s.parent[value]
}

func (s *disjointSet) union(left, right int64) {
	leftRoot, rightRoot := s.find(left), s.find(right)
	if leftRoot != rightRoot {
		s.parent[rightRoot] = leftRoot
	}
}

func buildTraces(exchanges []model.TrafficExchange) []model.TrafficTrace {
	if len(exchanges) == 0 {
		return []model.TrafficTrace{}
	}
	ordered := make([]model.TrafficExchange, len(exchanges))
	for index, exchange := range exchanges {
		ordered[index] = cloneExchange(exchange)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].StartedAt.Equal(ordered[right].StartedAt) {
			return ordered[left].Sequence < ordered[right].Sequence
		}
		return ordered[left].StartedAt.Before(ordered[right].StartedAt)
	})

	nodes := make(map[int64]*traceNode, len(ordered))
	spanOwners := make(map[string]int64)
	traceMembers := make(map[string][]int64)
	for _, exchange := range ordered {
		nodes[exchange.Sequence] = &traceNode{exchange: exchange, correlation: model.TrafficCorrelationPartial}
		if exchange.TraceID != "" {
			traceMembers[exchange.TraceID] = append(traceMembers[exchange.TraceID], exchange.Sequence)
		}
		if exchange.TraceID != "" && exchange.SpanID != "" {
			spanOwners[exchange.TraceID+"\x00"+exchange.SpanID] = exchange.Sequence
		}
	}

	set := newDisjointSet(ordered)
	for _, members := range traceMembers {
		for index := 1; index < len(members); index++ {
			set.union(members[0], members[index])
		}
	}
	for _, exchange := range ordered {
		node := nodes[exchange.Sequence]
		if exchange.TraceID != "" && exchange.ParentSpanID != "" {
			if parent := spanOwners[exchange.TraceID+"\x00"+exchange.ParentSpanID]; parent != 0 && parent != exchange.Sequence {
				node.parent = parent
				node.correlation = model.TrafficCorrelationExact
				set.union(parent, exchange.Sequence)
				continue
			}
		}
		candidates := inferenceCandidates(ordered, exchange)
		switch len(candidates) {
		case 1:
			node.parent = candidates[0]
			node.correlation = model.TrafficCorrelationInferred
			set.union(candidates[0], exchange.Sequence)
		case 0:
			if exchange.Source == "external" {
				node.correlation = model.TrafficCorrelationExact
			}
		default:
			node.correlation = model.TrafficCorrelationAmbiguous
		}
	}

	components := make(map[int64][]*traceNode)
	for _, exchange := range ordered {
		root := set.find(exchange.Sequence)
		components[root] = append(components[root], nodes[exchange.Sequence])
	}
	traces := make([]model.TrafficTrace, 0, len(components))
	for _, members := range components {
		traces = append(traces, projectTrace(members, nodes))
	}
	return traces
}

func inferenceCandidates(exchanges []model.TrafficExchange, child model.TrafficExchange) []int64 {
	candidates := make([]int64, 0, 2)
	for _, candidate := range exchanges {
		if candidate.Sequence == child.Sequence || candidate.Target != child.Source {
			continue
		}
		if candidate.StartedAt.After(child.StartedAt) || candidate.CompletedAt.Before(child.StartedAt) {
			continue
		}
		candidates = append(candidates, candidate.Sequence)
	}
	return candidates
}

func projectTrace(members []*traceNode, all map[int64]*traceNode) model.TrafficTrace {
	sort.SliceStable(members, func(left, right int) bool {
		if members[left].exchange.StartedAt.Equal(members[right].exchange.StartedAt) {
			return members[left].exchange.Sequence < members[right].exchange.Sequence
		}
		return members[left].exchange.StartedAt.Before(members[right].exchange.StartedAt)
	})
	root := traceRoot(members)
	started, completed := members[0].exchange.StartedAt, members[0].exchange.CompletedAt
	number := members[0].exchange.Sequence
	lastSequence := members[0].exchange.Sequence
	correlation := model.TrafficCorrelationExact
	errorResult, faulted := false, false
	for _, member := range members {
		if member.exchange.Sequence < number {
			number = member.exchange.Sequence
		}
		if member.exchange.Sequence > lastSequence {
			lastSequence = member.exchange.Sequence
		}
		if member.exchange.StartedAt.Before(started) {
			started = member.exchange.StartedAt
		}
		if member.exchange.CompletedAt.After(completed) {
			completed = member.exchange.CompletedAt
		}
		if member.exchange.Error != "" || member.exchange.Status >= 500 {
			errorResult = true
		}
		faulted = faulted || member.exchange.Fault != ""
		correlation = weakerCorrelation(correlation, member.correlation)
	}
	spans := make([]model.TrafficTraceSpan, 0, len(members))
	for _, member := range members {
		spanCorrelation := member.correlation
		if member != root && member.parent == 0 && spanCorrelation != model.TrafficCorrelationAmbiguous {
			spanCorrelation = model.TrafficCorrelationPartial
			correlation = weakerCorrelation(correlation, spanCorrelation)
		}
		spans = append(spans, model.TrafficTraceSpan{
			Exchange: member.exchange, ParentSequence: member.parent,
			Depth: traceDepth(member, all), StartOffsetMS: member.exchange.StartedAt.Sub(started).Milliseconds(),
			Correlation: spanCorrelation,
		})
	}
	rootExchange := root.exchange
	requestTarget := rootExchange.RequestTarget
	if requestTarget == "" {
		requestTarget = rootExchange.Path
	}
	return model.TrafficTrace{
		Project: rootExchange.Project, Environment: rootExchange.Environment,
		Number: number, LastSequence: lastSequence, TraceID: rootExchange.TraceID, RootSequence: rootExchange.Sequence,
		StartedAt: started, CompletedAt: completed, DurationMS: completed.Sub(started).Milliseconds(),
		Method: rootExchange.Method, RequestTarget: requestTarget, Source: rootExchange.Source, Target: rootExchange.Target,
		Status: rootExchange.Status, Error: errorResult, Faulted: faulted,
		Background: backgroundExchange(rootExchange), SpanCount: len(spans), Correlation: correlation, Spans: spans,
	}
}

func traceRoot(members []*traceNode) *traceNode {
	for _, member := range members {
		if member.parent == 0 && member.exchange.Source == "external" {
			return member
		}
	}
	for _, member := range members {
		if member.parent == 0 {
			return member
		}
	}
	return members[0]
}

func traceDepth(node *traceNode, all map[int64]*traceNode) int {
	depth := 0
	visited := map[int64]struct{}{node.exchange.Sequence: {}}
	for parent := node.parent; parent != 0; {
		if _, exists := visited[parent]; exists {
			break
		}
		visited[parent] = struct{}{}
		depth++
		parentNode := all[parent]
		if parentNode == nil {
			break
		}
		parent = parentNode.parent
	}
	return depth
}

func weakerCorrelation(current, candidate model.TrafficCorrelation) model.TrafficCorrelation {
	strength := map[model.TrafficCorrelation]int{
		model.TrafficCorrelationExact: 0, model.TrafficCorrelationInferred: 1,
		model.TrafficCorrelationPartial: 2, model.TrafficCorrelationAmbiguous: 3,
	}
	if strength[candidate] > strength[current] {
		return candidate
	}
	return current
}

func backgroundExchange(exchange model.TrafficExchange) bool {
	if exchange.RequestKind == model.TrafficRequestSubresource {
		return true
	}
	requestTarget := strings.ToLower(exchange.RequestTarget)
	return strings.HasPrefix(requestTarget, "/favicon.") || strings.HasPrefix(requestTarget, "/robots.txt")
}
