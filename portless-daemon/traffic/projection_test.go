package traffic

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestProjectionReadersShareWorkWithoutBlockingCapture(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	store := newStore(nil, func(inputs []traceInput) []projectedTrace {
		if len(inputs) > 0 && inputs[0].environment == "local" {
			first.Do(func() { close(started); <-release })
		}
		return buildProjection(inputs)
	})
	t.Cleanup(store.Close)
	defer close(release)
	store.AddExchange(benchmarkExchanges(1, false)[0])
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	results := make(chan error, 20)
	for range 20 {
		go func() {
			_, err := store.Traces(ctx, "review/local", TraceQuery{})
			results <- err
		}()
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// An unrelated environment can both capture and project while local is held.
	other := benchmarkExchanges(1, false)[0]
	other.Environment = "other"
	store.AddExchange(other)
	if snapshot, err := store.Traces(ctx, "review/other", TraceQuery{}); err != nil || len(snapshot.Traces) != 1 {
		t.Fatalf("unrelated snapshot: %#v %v", snapshot, err)
	}
	// Cancel waiting readers without canceling shared work for other readers.
	cancel()
	for range 20 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting reader: %v", err)
		}
	}
	if count := store.builds.Load(); count != 2 {
		t.Fatalf("builds = %d; concurrent readers should share one per environment", count)
	}
}

func TestClearRejectsInFlightProjectionAndKeepsSequence(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	broker := events.NewBroker()
	store := newStore(broker, func(inputs []traceInput) []projectedTrace {
		once.Do(func() { close(started); <-release })
		return buildProjection(inputs)
	})
	t.Cleanup(store.Close)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	subscription := broker.Subscribe(ctx, "review/local", []string{"traffic.trace"})
	defer subscription.Close()
	store.AddExchange(benchmarkExchanges(1, false)[0])
	result := make(chan TraceSnapshot, 1)
	go func() { snapshot, _ := store.Traces(ctx, "review/local", TraceQuery{}); result <- snapshot }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	count, sequence, revision := store.Clear("review", "local")
	if count != 1 || sequence != 1 {
		t.Fatalf("clear: %d %d", count, sequence)
	}
	select {
	case snapshot := <-result:
		if len(snapshot.Traces) != 0 || snapshot.Revision != revision || snapshot.ThroughSequence != 1 {
			t.Fatalf("clear snapshot: %#v", snapshot)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	next := store.AddExchange(benchmarkExchanges(1, false)[0])
	snapshot, err := store.Traces(ctx, "review/local", TraceQuery{})
	if err != nil || len(snapshot.Traces) != 1 || snapshot.Traces[0].Number != 2 || next.Sequence != 2 {
		t.Fatalf("after clear: %#v %v", snapshot, err)
	}
	select {
	case event := <-subscription.C:
		if event.Data.(model.TrafficTrace).Number != 2 {
			t.Fatalf("stale event: %#v", event)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestSummaryFilteringUsesAllMembersBeforeLimit(t *testing.T) {
	store := newTestStore(t, nil)
	for _, item := range benchmarkExchanges(12, true) {
		store.AddExchange(item)
	}
	for _, query := range []TraceQuery{
		{Source: "orders", Target: "postgres", Limit: 1},
		{Service: "postgres", Limit: 1},
		{Service: "orders", Source: "orders", Target: "postgres", Limit: 1},
	} {
		snapshot, err := store.Traces(t.Context(), "review/local", query)
		if err != nil || len(snapshot.Traces) != 1 || snapshot.Traces[0].Number != 9 || snapshot.Traces[0].Spans != nil || snapshot.ThroughSequence != 12 {
			t.Fatalf("query %#v: %#v %v", query, snapshot, err)
		}
	}
	snapshot, err := store.Traces(t.Context(), "review/local", TraceQuery{Service: "inventory", Source: "orders", Target: "postgres"})
	if err != nil || len(snapshot.Traces) != 0 {
		t.Fatalf("service must match the selected edge: %#v %v", snapshot, err)
	}
}

func TestCachedSummariesDoNotRetainPayloadsOrChangeUnrelatedRevisions(t *testing.T) {
	store := newTestStore(t, nil)
	store.limit = 2
	items := benchmarkExchanges(3, false)
	items[0].RequestHeaders = map[string][]string{"X-Test": {"original"}}
	items[0].ResponseBody = "captured body"
	store.AddExchange(items[0])
	before, _ := store.Traces(t.Context(), "review/local", TraceQuery{})
	store.AddExchange(items[1])
	after, _ := store.Traces(t.Context(), "review/local", TraceQuery{})
	if after.Traces[1].Revision != before.Traces[0].Revision || after.Revision <= before.Revision {
		t.Fatalf("unrelated append changed trace revision: %#v %#v", before, after)
	}
	summaries := store.ExchangeSummaries("review/local", 2)
	if summaries[1].RequestHeaders != nil || summaries[1].ResponseBody != "" {
		t.Fatalf("payload in summary: %#v", summaries[1])
	}
	detail, _, err := store.Trace(t.Context(), "review/local", 1)
	if err != nil || detail.Spans[0].Exchange.ResponseBody != "captured body" {
		t.Fatalf("detail: %#v %v", detail, err)
	}
	detail.Spans[0].Exchange.RequestHeaders["X-Test"][0] = "mutated"
	again, _, _ := store.Trace(t.Context(), "review/local", 1)
	if again.Spans[0].Exchange.RequestHeaders["X-Test"][0] != "original" {
		t.Fatal("detail mutated retention")
	}
	builds := store.builds.Load()
	for range 10 {
		if _, err := store.Traces(t.Context(), "review/local", TraceQuery{}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Trace(t.Context(), "review/local", 1); err != nil {
			t.Fatal(err)
		}
	}
	if store.builds.Load() != builds {
		t.Fatal("cached readers rebuilt the projection")
	}
	store.AddExchange(items[2])
	if _, exists, err := store.Trace(t.Context(), "review/local", 1); exists || err != nil {
		t.Fatalf("evicted trace returned: %v %v", exists, err)
	}
	if stats := store.RetentionStats(); stats.Exchanges != 2 || stats.PayloadBytes != 0 {
		t.Fatalf("payload pinned after eviction: %#v", stats)
	}
}

func TestConcurrentCaptureProjectionAndClear(t *testing.T) {
	store := newTestStore(t, nil)
	store.limit = 32
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	errors := make(chan error, 12)
	for env := range 4 {
		name := fmt.Sprint(env)
		for role := range 3 {
			workers.Go(func() {
				for i := range 100 {
					scope := "review/" + name
					switch role {
					case 0:
						exchange := benchmarkExchanges(1, false)[0]
						exchange.Environment = name
						id := store.BeginHTTPRequest(scope, exchange.Target, exchange.StartedAt)
						store.CompleteHTTPRequest(id, exchange)
					case 1:
						snapshot, err := store.Traces(ctx, scope, TraceQuery{Limit: 5})
						if err != nil {
							errors <- err
							return
						}
						for _, trace := range snapshot.Traces {
							detail, exists, err := store.Trace(ctx, scope, trace.Number)
							if err != nil {
								errors <- err
								return
							}
							if exists && len(detail.Spans) != detail.SpanCount {
								errors <- fmt.Errorf("partial detail: %#v", detail)
								return
							}
						}
					case 2:
						if i%10 == 0 {
							store.Clear("review", name)
						}
						store.RetentionStats()
					}
				}
			})
		}
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestDisposeAndCloseCancelObsoleteWork(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var builds atomic.Int32
	store := newStore(nil, func(inputs []traceInput) []projectedTrace {
		if builds.Add(1) == 1 {
			close(started)
			<-release
		}
		return buildProjection(inputs)
	})
	t.Cleanup(store.Close)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	store.AddExchange(benchmarkExchanges(1, false)[0])
	result := make(chan error, 1)
	go func() { _, err := store.Traces(ctx, "review/local", TraceQuery{}); result <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	store.DisposeEnvironment("review/local")
	if err := <-result; !errors.Is(err, errEnvironmentDisposed) {
		t.Fatalf("disposed reader: %v", err)
	}
	close(release)
	if stats := store.RetentionStats(); stats.Exchanges != 0 {
		t.Fatalf("disposed retention: %#v", stats)
	}
	store.AddExchange(benchmarkExchanges(1, false)[0])
	if snapshot, err := store.Traces(ctx, "review/local", TraceQuery{}); err != nil || len(snapshot.Traces) != 1 || snapshot.Traces[0].Number != 1 {
		t.Fatalf("recreated environment: %#v %v", snapshot, err)
	}
	store.Close()
	if _, err := store.Traces(ctx, "review/local", TraceQuery{}); !errors.Is(err, errStoreClosed) {
		t.Fatalf("closed reader: %v", err)
	}
	store.pendingMu.Lock()
	defer store.pendingMu.Unlock()
	if len(store.pending) != 0 {
		t.Fatal("closed store retained queued projections")
	}
}
