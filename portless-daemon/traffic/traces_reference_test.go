package traffic

import (
	"sort"

	"github.com/runportless/portless/portless-daemon/model"
)

type referenceTraceNode struct {
	exchange    model.TrafficExchange
	parent      int64
	correlation model.TrafficCorrelation
}

type referenceTCPTransactionKey struct {
	session     uint64
	transaction uint64
}

type referenceDisjointSet struct {
	parent map[int64]int64
}

func referenceNewDisjointSet(exchanges []model.TrafficExchange) *referenceDisjointSet {
	set := &referenceDisjointSet{parent: make(map[int64]int64, len(exchanges))}
	for _, exchange := range exchanges {
		set.parent[exchange.Sequence] = exchange.Sequence
	}
	return set
}

func (s *referenceDisjointSet) find(value int64) int64 {
	parent := s.parent[value]
	if parent != value {
		s.parent[value] = s.find(parent)
	}
	return s.parent[value]
}

func (s *referenceDisjointSet) union(left, right int64) {
	leftRoot, rightRoot := s.find(left), s.find(right)
	if leftRoot != rightRoot {
		s.parent[rightRoot] = leftRoot
	}
}

func referenceBuildTraces(exchanges []model.TrafficExchange) []model.TrafficTrace {
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

	nodes := make(map[int64]*referenceTraceNode, len(ordered))
	spanOwners := make(map[string]int64)
	traceMembers := make(map[string][]int64)
	for _, exchange := range ordered {
		nodes[exchange.Sequence] = &referenceTraceNode{exchange: exchange, correlation: model.TrafficCorrelationPartial}
		if exchange.TraceID != "" {
			traceMembers[exchange.TraceID] = append(traceMembers[exchange.TraceID], exchange.Sequence)
		}
		if exchange.TraceID != "" && exchange.SpanID != "" {
			spanOwners[exchange.TraceID+"\x00"+exchange.SpanID] = exchange.Sequence
		}
	}

	set := referenceNewDisjointSet(ordered)
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
		candidates := referenceInferenceCandidates(ordered, exchange)
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

	components := make(map[int64][]*referenceTraceNode)
	for _, exchange := range ordered {
		root := set.find(exchange.Sequence)
		components[root] = append(components[root], nodes[exchange.Sequence])
	}
	traces := make([]model.TrafficTrace, 0, len(components))
	for _, members := range components {
		traces = append(traces, referenceProjectTrace(members, nodes))
	}
	return traces
}

func referenceInferenceCandidates(exchanges []model.TrafficExchange, child model.TrafficExchange) []int64 {
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

func referenceProjectTrace(members []*referenceTraceNode, all map[int64]*referenceTraceNode) model.TrafficTrace {
	sort.SliceStable(members, func(left, right int) bool {
		if members[left].exchange.StartedAt.Equal(members[right].exchange.StartedAt) {
			return members[left].exchange.Sequence < members[right].exchange.Sequence
		}
		return members[left].exchange.StartedAt.Before(members[right].exchange.StartedAt)
	})
	root := referenceTraceRoot(members)
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
		correlation = referenceWeakerCorrelation(correlation, member.correlation)
	}
	spans := make([]model.TrafficTraceSpan, 0, len(members))
	transactionGroups := make(map[referenceTCPTransactionKey]int)
	nextTransactionGroup := 0
	for _, member := range members {
		spanCorrelation := member.correlation
		if member != root && member.parent == 0 && spanCorrelation != model.TrafficCorrelationAmbiguous {
			spanCorrelation = model.TrafficCorrelationPartial
			correlation = referenceWeakerCorrelation(correlation, spanCorrelation)
		}
		span := model.TrafficTraceSpan{
			Exchange: member.exchange, ParentSequence: member.parent,
			Depth: referenceTraceDepth(member, all), StartOffsetMS: member.exchange.StartedAt.Sub(started).Milliseconds(),
			Correlation: spanCorrelation,
		}
		if tcp := member.exchange.TCP; tcp != nil && tcp.SessionSequence != 0 && tcp.TransactionSequence != 0 {
			key := referenceTCPTransactionKey{session: tcp.SessionSequence, transaction: tcp.TransactionSequence}
			group := transactionGroups[key]
			if group == 0 {
				nextTransactionGroup++
				group = nextTransactionGroup
				transactionGroups[key] = group
			}
			span.TransactionGroup = group
		}
		spans = append(spans, span)
	}
	rootExchange := root.exchange
	requestTarget := rootExchange.RequestTarget
	if requestTarget == "" {
		requestTarget = rootExchange.Path
	}
	return model.TrafficTrace{
		Project: rootExchange.Project, Environment: rootExchange.Environment,
		Number: number, LastSequence: lastSequence, TraceID: rootExchange.TraceID, RootSequence: rootExchange.Sequence,
		Protocol:  rootExchange.Protocol,
		StartedAt: started, CompletedAt: completed, DurationMS: completed.Sub(started).Milliseconds(),
		Method: rootExchange.Method, RequestTarget: requestTarget, Source: rootExchange.Source, Target: rootExchange.Target,
		Status: rootExchange.Status, Error: errorResult, Faulted: faulted,
		Background: backgroundExchange(rootExchange), SpanCount: len(spans), Correlation: correlation, Spans: spans,
	}
}

func referenceTraceRoot(members []*referenceTraceNode) *referenceTraceNode {
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

func referenceTraceDepth(node *referenceTraceNode, all map[int64]*referenceTraceNode) int {
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

func referenceWeakerCorrelation(current, candidate model.TrafficCorrelation) model.TrafficCorrelation {
	strength := map[model.TrafficCorrelation]int{
		model.TrafficCorrelationExact: 0, model.TrafficCorrelationInferred: 1,
		model.TrafficCorrelationPartial: 2, model.TrafficCorrelationAmbiguous: 3,
	}
	if strength[candidate] > strength[current] {
		return candidate
	}
	return current
}
