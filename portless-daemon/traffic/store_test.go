package traffic

import (
	"context"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
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

func TestTCPTraceIsProvisionalOnlyWhilePotentialHTTPParentIsActive(t *testing.T) {
	broker := events.NewBroker()
	store := NewStore(broker)
	scope := model.EnvironmentSelector("store", "local")
	subscription := broker.Subscribe(context.Background(), scope, []string{"traffic.trace"})
	defer subscription.Close()
	base := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	activeRequest := store.BeginHTTPRequest(scope, "orders", base)

	store.AddExchange(model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolTCP,
		Source: "orders", Target: "redis", StartedAt: base.Add(5 * time.Millisecond), CompletedAt: base.Add(10 * time.Millisecond),
	})
	provisional := store.Traces(scope, 10)
	if len(provisional) != 1 || provisional[0].Protocol != model.ProtocolTCP || !provisional[0].Provisional {
		t.Fatalf("active-parent TCP trace = %#v, want one provisional TCP root", provisional)
	}
	select {
	case event := <-subscription.C:
		live, ok := event.Data.(model.TrafficTrace)
		if !ok || live.Protocol != model.ProtocolTCP || !live.Provisional {
			t.Fatalf("live TCP trace = %#v, want provisional TCP root", event.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provisional TCP trace")
	}

	store.CompleteHTTPRequest(activeRequest, model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolHTTP,
		Source: "external", Target: "orders", StartedAt: base, CompletedAt: base.Add(20 * time.Millisecond), Method: "GET", RequestTarget: "/orders",
	})
	settled := store.Traces(scope, 10)
	if len(settled) != 1 || settled[0].Protocol != model.ProtocolHTTP || settled[0].Provisional || settled[0].SpanCount != 2 {
		t.Fatalf("completed-parent trace = %#v, want one settled HTTP-rooted trace", settled)
	}
	select {
	case event := <-subscription.C:
		live, ok := event.Data.(model.TrafficTrace)
		if !ok || live.Protocol != model.ProtocolHTTP || live.Provisional || live.SpanCount != 2 {
			t.Fatalf("live settled trace = %#v, want settled HTTP root", event.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for settled HTTP trace")
	}

	standalone := NewStore(nil)
	standalone.AddExchange(model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolTCP,
		Source: "worker", Target: "redis", StartedAt: base, CompletedAt: base.Add(time.Millisecond),
	})
	standaloneTraces := standalone.Traces(scope, 10)
	if len(standaloneTraces) != 1 || standaloneTraces[0].Protocol != model.ProtocolTCP || standaloneTraces[0].Provisional {
		t.Fatalf("standalone TCP trace = %#v, want one settled TCP root", standaloneTraces)
	}
}

func TestBackgroundExchangeIsRetainedButOnlyHidesStandaloneSuccessfulTrace(t *testing.T) {
	base := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	scope := model.EnvironmentSelector("store", "local")
	standalone := NewStore(nil)
	background := standalone.AddExchange(model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolTCP,
		Source: "orders", Target: "redis", Background: true,
		StartedAt: base, CompletedAt: base.Add(time.Millisecond),
		TCP: &model.TrafficTCPExchange{Kind: model.TrafficTCPKindOperation, Outcome: model.TrafficTCPOutcomeSuccess},
	})
	if !background.Background || len(standalone.RecentExchanges(scope, 10)) != 1 {
		t.Fatalf("background raw exchange was not retained: %#v", background)
	}
	traces := standalone.Traces(scope, 10)
	if len(traces) != 1 || !traces[0].Background {
		t.Fatalf("standalone housekeeping trace = %#v, want background", traces)
	}

	correlated := NewStore(nil)
	activeRequest := correlated.BeginHTTPRequest(scope, "orders", base)
	correlated.AddExchange(model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolTCP,
		Source: "orders", Target: "redis", Background: true,
		StartedAt: base.Add(time.Millisecond), CompletedAt: base.Add(2 * time.Millisecond),
		TCP: &model.TrafficTCPExchange{Kind: model.TrafficTCPKindOperation, Outcome: model.TrafficTCPOutcomeSuccess},
	})
	correlated.CompleteHTTPRequest(activeRequest, model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolHTTP,
		Source: "external", Target: "orders", StartedAt: base, CompletedAt: base.Add(3 * time.Millisecond), Status: 200,
	})
	traces = correlated.Traces(scope, 10)
	if len(traces) != 1 || traces[0].Background || traces[0].SpanCount != 2 {
		t.Fatalf("HTTP trace with housekeeping child = %#v, want visible two-span trace", traces)
	}

	failed := standalone.AddExchange(model.TrafficExchange{
		Project: "store", Environment: "local", Protocol: model.ProtocolTCP,
		Source: "orders", Target: "redis", Background: true, Error: "validation failed",
		StartedAt: base.Add(time.Second), CompletedAt: base.Add(time.Second + time.Millisecond),
		TCP: &model.TrafficTCPExchange{Kind: model.TrafficTCPKindOperation, Outcome: model.TrafficTCPOutcomeError},
	})
	if failed.Background {
		t.Fatalf("failed housekeeping exchange remained background: %#v", failed)
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

func TestStoreClonesDecodedTCPMessagesAndEvictsByPayloadBytes(t *testing.T) {
	store := NewStore(nil)
	store.payloadLimit = 20
	first := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local", TCP: &model.TrafficTCPExchange{
		Kind: model.TrafficTCPKindOperation, Inspection: model.TrafficInspectionDecoded,
		RequestMessages: []model.TrafficMessage{{Type: "q", Content: "123456", CapturedBytes: 6, Fields: []model.TrafficMessageField{{Name: "key", Value: "one"}}}},
	}})
	first.TCP.RequestMessages[0].Content = "changed"
	first.TCP.RequestMessages[0].Fields[0].Value = "changed"
	stored, ok := store.Exchange(model.EnvironmentSelector("billing", "local"), first.Sequence)
	if !ok || stored.TCP.RequestMessages[0].Content != "123456" || stored.TCP.RequestMessages[0].Fields[0].Value != "one" {
		t.Fatalf("stored TCP messages were not isolated: %#v", stored)
	}
	store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local", RequestBody: "abcdefgh"})
	if _, exists := store.Exchange(model.EnvironmentSelector("billing", "local"), first.Sequence); exists {
		t.Fatal("oldest exchange was not evicted by the retained payload limit")
	}
}

func TestStoreClearPreservesSequenceAndPublishesScopeUpdate(t *testing.T) {
	broker := events.NewBroker()
	store := NewStore(broker)
	scope := model.EnvironmentSelector("billing", "local")
	subscription := broker.Subscribe(context.Background(), scope, []string{"traffic.cleared"})
	defer subscription.Close()

	store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local"})
	store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local"})
	cleared, throughSequence := store.Clear("billing", "local")
	if cleared != 2 || throughSequence != 2 || len(store.RecentExchanges(scope, 10)) != 0 || len(store.Traces(scope, 10)) != 0 {
		t.Fatalf("clear result = (%d, %d), exchanges=%d traces=%d", cleared, throughSequence, len(store.RecentExchanges(scope, 10)), len(store.Traces(scope, 10)))
	}
	if next := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local"}); next.Sequence != 3 {
		t.Fatalf("sequence after clear = %d, want 3", next.Sequence)
	}

	select {
	case event := <-subscription.C:
		payload, ok := event.Data.(map[string]any)
		if event.Type != "traffic.cleared" || !ok || payload["cleared"] != 2 || payload["throughSequence"] != int64(2) {
			t.Fatalf("clear event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for traffic.cleared")
	}
}
