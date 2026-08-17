package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/events"
	"github.com/portless-run/portless/portless-daemon/model"
)

func TestStoreRestoresSequencesAndPublishesProtocolNeutralUpdates(t *testing.T) {
	broker := events.NewBroker()
	store := NewStore(broker)
	scope := model.EnvironmentSelector("billing", "local")
	store.EnsureSequence(scope, 41)
	subscription := broker.Subscribe(context.Background(), scope, []string{"traffic.exchange", "traffic.trace"})
	defer subscription.Close()

	exchange := store.AddExchange(model.TrafficExchange{
		Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP,
		Source: "external", Target: "checkout", StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	})
	if exchange.Sequence != 42 {
		t.Fatalf("sequence = %d, want 42", exchange.Sequence)
	}
	store.EnsureSequence(scope, 10)
	if next := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local"}); next.Sequence != 43 {
		t.Fatalf("lower restoration moved sequence backwards: %d", next.Sequence)
	}

	topics := map[string]bool{}
	deadline := time.After(time.Second)
	for len(topics) < 2 {
		select {
		case event := <-subscription.C:
			topics[event.Type] = true
		case <-deadline:
			t.Fatalf("traffic updates = %v, want exchange and trace", topics)
		}
	}
}

func TestTraceProjectionUsesStartTimeAndTopologyAcrossCompletionOrder(t *testing.T) {
	store := NewStore(nil)
	base := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	exchanges := []model.TrafficExchange{
		{Project: "store", Environment: "local", Protocol: model.ProtocolHTTP, Source: "checkout", Target: "inventory", StartedAt: base.Add(10 * time.Millisecond), CompletedAt: base.Add(25 * time.Millisecond), Method: "GET", RequestTarget: "/inventory/mug", Status: 200},
		{Project: "store", Environment: "local", Protocol: model.ProtocolTCP, Source: "orders", Target: "redis", StartedAt: base.Add(35 * time.Millisecond), CompletedAt: base.Add(40 * time.Millisecond)},
		{Project: "store", Environment: "local", Protocol: model.ProtocolTCP, Source: "orders", Target: "postgres", StartedAt: base.Add(32 * time.Millisecond), CompletedAt: base.Add(48 * time.Millisecond)},
		{Project: "store", Environment: "local", Protocol: model.ProtocolHTTP, Source: "checkout", Target: "orders", StartedAt: base.Add(20 * time.Millisecond), CompletedAt: base.Add(60 * time.Millisecond), Method: "GET", RequestTarget: "/orders", Status: 200},
		{Project: "store", Environment: "local", Protocol: model.ProtocolHTTP, Source: "external", Target: "checkout", StartedAt: base, CompletedAt: base.Add(80 * time.Millisecond), Method: "GET", RequestTarget: "/checkout?sku=mug", Status: 200},
	}
	for _, exchange := range exchanges {
		store.AddExchange(exchange)
	}
	traces := store.Traces(model.EnvironmentSelector("store", "local"), 10)
	if len(traces) != 1 {
		t.Fatalf("traces = %#v, want one inferred trace", traces)
	}
	trace := traces[0]
	if trace.Number != 1 || trace.LastSequence != 5 || trace.RootSequence != 5 || trace.RequestTarget != "/checkout?sku=mug" || trace.SpanCount != 5 || trace.DurationMS != 80 || trace.Correlation != model.TrafficCorrelationInferred {
		t.Fatalf("unexpected trace summary: %#v", trace)
	}
	depths := make(map[string]int)
	for _, span := range trace.Spans {
		depths[span.Exchange.Source+":"+span.Exchange.Target] = span.Depth
	}
	for edge, expected := range map[string]int{"external:checkout": 0, "checkout:inventory": 1, "checkout:orders": 1, "orders:redis": 2, "orders:postgres": 2} {
		if depths[edge] != expected {
			t.Fatalf("depth %s = %d, want %d; spans=%#v", edge, depths[edge], expected, trace.Spans)
		}
	}
}

func TestTraceProjectionUsesExactContextAndRefusesAmbiguousInference(t *testing.T) {
	base := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	exact := buildTraces([]model.TrafficExchange{
		{Sequence: 1, Project: "store", Environment: "local", Source: "external", Target: "checkout", TraceID: "trace", SpanID: "root", StartedAt: base, CompletedAt: base.Add(50 * time.Millisecond)},
		{Sequence: 2, Project: "store", Environment: "local", Source: "checkout", Target: "orders", TraceID: "trace", SpanID: "child", ParentSpanID: "root", StartedAt: base.Add(5 * time.Millisecond), CompletedAt: base.Add(20 * time.Millisecond)},
	})
	if len(exact) != 1 || exact[0].Correlation != model.TrafficCorrelationExact || exact[0].Spans[1].ParentSequence != 1 {
		t.Fatalf("exact trace = %#v", exact)
	}

	ambiguous := buildTraces([]model.TrafficExchange{
		{Sequence: 1, Project: "store", Environment: "local", Source: "external", Target: "checkout", StartedAt: base, CompletedAt: base.Add(50 * time.Millisecond)},
		{Sequence: 2, Project: "store", Environment: "local", Source: "external", Target: "checkout", StartedAt: base.Add(time.Millisecond), CompletedAt: base.Add(45 * time.Millisecond)},
		{Sequence: 3, Project: "store", Environment: "local", Source: "checkout", Target: "orders", StartedAt: base.Add(5 * time.Millisecond), CompletedAt: base.Add(10 * time.Millisecond)},
	})
	if len(ambiguous) != 3 {
		t.Fatalf("ambiguous child should remain ungrouped: %#v", ambiguous)
	}
	var child model.TrafficTrace
	for _, trace := range ambiguous {
		if trace.RootSequence == 3 {
			child = trace
		}
	}
	if child.Correlation != model.TrafficCorrelationAmbiguous || child.Spans[0].ParentSequence != 0 {
		t.Fatalf("ambiguous trace = %#v", child)
	}
}

func TestStoreClonesRepeatedHeaders(t *testing.T) {
	store := NewStore(nil)
	exchange := store.AddExchange(model.TrafficExchange{
		Project: "billing", Environment: "local", RequestHeaders: map[string][]string{"X-Value": {"one", "two"}},
	})
	exchange.RequestHeaders["X-Value"][0] = "changed"
	stored, ok := store.Exchange(model.EnvironmentSelector("billing", "local"), exchange.Sequence)
	if !ok || len(stored.RequestHeaders["X-Value"]) != 2 || stored.RequestHeaders["X-Value"][0] != "one" {
		t.Fatalf("stored headers were not isolated: %#v", stored.RequestHeaders)
	}
}
