package traffic

import (
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestReplayIsAnIndependentForegroundRootWithDownstreamChildren(t *testing.T) {
	store := newTestStore(t, nil)
	start := time.Now().UTC()
	outer := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Source: "external", Target: "checkout", StartedAt: start, CompletedAt: start.Add(time.Second), TraceID: "original", SpanID: "outer", Method: "GET", Status: 200})
	replay := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Source: "checkout", Target: "orders", StartedAt: start.Add(time.Millisecond), CompletedAt: start.Add(100 * time.Millisecond), TraceID: "replay", SpanID: "root", Method: "GET", RequestTarget: "/favicon.ico", RequestKind: model.TrafficRequestSubresource, Status: 200, Replay: &model.TrafficReplay{Sequence: 42}})
	child := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Source: "orders", Target: "payments", StartedAt: start.Add(2 * time.Millisecond), CompletedAt: start.Add(3 * time.Millisecond), TraceID: "replay", SpanID: "child", ParentSpanID: "root", Method: "GET", Status: 200})
	traces := testTraceDetails(t, store, "billing/local", 10)
	if len(traces) != 2 || replay.Background {
		t.Fatalf("traces=%#v replay=%#v", traces, replay)
	}
	for _, trace := range traces {
		if trace.RootSequence == outer.Sequence && len(trace.Spans) != 1 {
			t.Fatalf("replay attached to unrelated root: %#v", trace)
		}
		if trace.RootSequence == replay.Sequence {
			if len(trace.Spans) != 2 || trace.Spans[0].ParentSequence != 0 || trace.Spans[1].Exchange.Sequence != child.Sequence || trace.Spans[1].ParentSequence != replay.Sequence {
				t.Fatalf("replay descendants=%#v", trace)
			}
		}
	}
}

func TestReplayMetadataIsClonedForDetailsAndSummaries(t *testing.T) {
	store := newTestStore(t, nil)
	exchange := store.AddExchange(model.TrafficExchange{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Replay: &model.TrafficReplay{Sequence: 42}, RequestCapture: &model.HTTPCapture{State: "complete", Exact: true}})
	exchange.Replay.Sequence = 99
	exchange.RequestCapture.State = "changed"
	summary := store.ExchangeSummaries("billing/local", 1)[0]
	summary.Replay.Sequence = 100
	summary.RequestCapture.State = "changed again"
	detail, ok := store.Exchange("billing/local", exchange.Sequence)
	if !ok || detail.Replay.Sequence != 42 || detail.RequestCapture.State != "complete" {
		t.Fatalf("retained metadata mutated: %#v", detail)
	}
}
