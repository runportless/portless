package traffic

import (
	"github.com/runportless/portless/portless-daemon/model"
	"testing"
)

func TestClearThroughRetainsTrafficArrivingAfterPreview(t *testing.T) {
	store := newTestStore(t, nil)
	scope := "shop/local"
	store.AddExchange(model.TrafficExchange{Project: "shop", Environment: "local", Protocol: model.ProtocolHTTP, Source: "external", Target: "web", Path: "/first", ResponseBody: "old"})
	count, watermark := store.ClearWatermark("shop", "local")
	if count != 1 || watermark != 1 {
		t.Fatalf("preview=%d %d", count, watermark)
	}
	newer := store.AddExchange(model.TrafficExchange{Project: "shop", Environment: "local", Protocol: model.ProtocolHTTP, Source: "external", Target: "web", Path: "/newer", ResponseBody: "retained"})
	cleared, through, _ := store.ClearThrough("shop", "local", watermark)
	if cleared != 1 || through != 1 {
		t.Fatalf("clear=%d %d", cleared, through)
	}
	if _, ok := store.Exchange(scope, 1); ok {
		t.Fatal("reviewed exchange retained")
	}
	if got, ok := store.Exchange(scope, newer.Sequence); !ok || got.ResponseBody != "retained" {
		t.Fatal("new exchange removed")
	}
	traces := testTraceDetails(t, store, scope, 10)
	if len(traces) != 1 || traces[0].LastSequence != newer.Sequence {
		t.Fatalf("projection lost new traffic=%#v", traces)
	}
	if next := store.AddExchange(model.TrafficExchange{Project: "shop", Environment: "local"}); next.Sequence != 3 {
		t.Fatal("clear reset sequence")
	}
}
