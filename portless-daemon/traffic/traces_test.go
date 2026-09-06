package traffic

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func newTestStore(t *testing.T, broker *events.Broker) *Store {
	store := NewStore(broker)
	t.Cleanup(store.Close)
	return store
}

func testTraceDetails(t *testing.T, store *Store, scope string, limit int) []model.TrafficTrace {
	t.Helper()
	snapshot, err := store.Traces(t.Context(), scope, TraceQuery{Limit: limit, IncludeBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	traces := make([]model.TrafficTrace, 0, len(snapshot.Traces))
	for _, summary := range snapshot.Traces {
		trace, exists, err := store.Trace(t.Context(), scope, summary.Number)
		if err != nil || !exists {
			t.Fatalf("trace %d: exists=%v err=%v", summary.Number, exists, err)
		}
		traces = append(traces, trace)
	}
	return traces
}

func projectedDetails(exchanges []model.TrafficExchange) []model.TrafficTrace {
	inputs := make([]traceInput, len(exchanges))
	bySequence := make(map[int64]model.TrafficExchange)
	for i, exchange := range exchanges {
		inputs[i] = traceInputFor(exchange)
		bySequence[exchange.Sequence] = exchange
	}
	projected := buildProjection(inputs)
	result := make([]model.TrafficTrace, 0, len(projected))
	for _, trace := range projected {
		full := trace.summary
		for _, span := range trace.spans {
			full.Spans = append(full.Spans, model.TrafficTraceSpan{Exchange: cloneExchange(bySequence[span.sequence]), ParentSequence: span.parent, Depth: span.depth, StartOffsetMS: span.offset, Correlation: span.correlation, TransactionGroup: span.transactionGroup})
		}
		result = append(result, full)
	}
	return result
}

func assertProjectionMatches(t *testing.T, exchanges []model.TrafficExchange) {
	t.Helper()
	want, got := referenceBuildTraces(exchanges), projectedDetails(exchanges)
	order := func(a, b model.TrafficTrace) int {
		if a.Number < b.Number {
			return -1
		}
		if a.Number > b.Number {
			return 1
		}
		return 0
	}
	slices.SortFunc(want, order)
	slices.SortFunc(got, order)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection differs\ninputs: %#v\ngot: %#v\nwant: %#v", exchanges, got, want)
	}
}

func TestIndexedProjectionMatchesReferenceAcrossHistories(t *testing.T) {
	for seed := uint64(1); seed <= 30; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			random := rand.New(rand.NewPCG(seed, 81))
			base := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
			services := []string{"external", "checkout", "orders", "inventory", "postgres"}
			var history []model.TrafficExchange
			for i := 0; i < 50; i++ {
				start := base.Add(time.Duration(random.IntN(10)) * time.Millisecond)
				exchange := model.TrafficExchange{Sequence: int64(i + 1), Project: "store", Environment: "local", Source: services[random.IntN(len(services))], Target: services[random.IntN(len(services))], Protocol: model.ProtocolHTTP, StartedAt: start, CompletedAt: start.Add(time.Duration(random.IntN(10)) * time.Millisecond)}
				if random.IntN(2) == 0 {
					exchange.TraceID = fmt.Sprint(random.IntN(8))
					exchange.SpanID = fmt.Sprint(random.IntN(15))
					exchange.ParentSpanID = fmt.Sprint(random.IntN(15))
				}
				if random.IntN(3) == 0 {
					exchange.Protocol = model.ProtocolTCP
					exchange.TCP = &model.TrafficTCPExchange{SessionSequence: uint64(random.IntN(4)), TransactionSequence: uint64(random.IntN(5))}
				}
				exchange.Background = random.IntN(5) == 0
				history = append(history, exchange)
				assertProjectionMatches(t, history)
				if len(history) > 10 {
					assertProjectionMatches(t, history[5:])
				}
			}
		})
	}
}

func TestIndexedProjectionHandlesDeepChainsAndCycles(t *testing.T) {
	base := time.Now().UTC()
	for _, cycle := range []bool{false, true} {
		items := make([]model.TrafficExchange, 250)
		for i := range items {
			items[i] = model.TrafficExchange{Sequence: int64(i + 1), TraceID: "chain", SpanID: fmt.Sprint(i), Source: "service", Target: "service", StartedAt: base, CompletedAt: base.Add(time.Second)}
			if i > 0 {
				items[i].ParentSpanID = fmt.Sprint(i - 1)
			}
		}
		if cycle {
			items[0].ParentSpanID = fmt.Sprint(len(items) - 1)
		}
		assertProjectionMatches(t, items)
	}
}

func TestAbandonHTTPRequestInvalidatesCachedProvisionalState(t *testing.T) {
	store := newTestStore(t, nil)
	scope := model.EnvironmentSelector("store", "local")
	started := time.Now().UTC()
	request := store.BeginHTTPRequest(scope, "orders", started)
	store.AddExchange(model.TrafficExchange{Project: "store", Environment: "local", Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, StartedAt: started.Add(time.Millisecond), CompletedAt: started.Add(2 * time.Millisecond)})
	before, err := store.Traces(t.Context(), scope, TraceQuery{IncludeBackground: true})
	if err != nil || len(before.Traces) != 1 || !before.Traces[0].Provisional {
		t.Fatalf("before: %#v %v", before, err)
	}
	store.AbandonHTTPRequest(request)
	after, err := store.Traces(t.Context(), scope, TraceQuery{IncludeBackground: true})
	if err != nil || len(after.Traces) != 1 || after.Traces[0].Provisional || after.Revision <= before.Revision {
		t.Fatalf("after: %#v %v", after, err)
	}
}

func TestProjectionReadCancellation(t *testing.T) {
	store := newTestStore(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Traces(ctx, "store/local", TraceQuery{}); err != context.Canceled {
		t.Fatalf("error = %v", err)
	}
}
