package traffic

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

var benchmarkTraceSink []projectedTrace
var benchmarkExchangeSink model.TrafficExchange

func benchmarkExchanges(count int, mixed bool) []model.TrafficExchange {
	base := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	items := make([]model.TrafficExchange, count)
	for i := range items {
		start := base.Add(time.Duration(i) * time.Second)
		item := model.TrafficExchange{Project: "review", Environment: "local", Sequence: int64(i + 1), Protocol: model.ProtocolHTTP, Source: "external", Target: "checkout", StartedAt: start, CompletedAt: start.Add(50 * time.Millisecond), Method: "GET", RequestTarget: "/orders", Status: 200, TraceID: fmt.Sprintf("%032x", i+1), SpanID: fmt.Sprintf("%016x", i+1)}
		if mixed {
			group := i / 4
			start = base.Add(time.Duration(group) * time.Second)
			item.StartedAt, item.CompletedAt = start, start.Add(50*time.Millisecond)
			item.TraceID = fmt.Sprintf("%032x", group+1)
			switch i % 4 {
			case 1:
				item.Source, item.Target = "checkout", "orders"
				item.ParentSpanID = fmt.Sprintf("%016x", group*4+1)
				item.StartedAt, item.CompletedAt = start.Add(5*time.Millisecond), start.Add(25*time.Millisecond)
			case 2:
				item.Protocol, item.Source, item.Target = model.ProtocolTCP, "orders", "postgres"
				item.TraceID, item.SpanID, item.Method, item.RequestTarget = "", "", "", ""
				item.StartedAt, item.CompletedAt = start.Add(8*time.Millisecond), start.Add(9*time.Millisecond)
			case 3:
				item.Source, item.Target, item.TraceID, item.SpanID = "checkout", "inventory", "", ""
				item.StartedAt, item.CompletedAt = start.Add(30*time.Millisecond), start.Add(35*time.Millisecond)
			}
		}
		items[i] = item
	}
	return items
}

func benchmarkStore(b *testing.B, items []model.TrafficExchange) *Store {
	b.Helper()
	store := NewStore(nil)
	b.Cleanup(store.Close)
	store.limit = len(items)
	window := store.window(model.EnvironmentSelector("review", "local"))
	for i, exchange := range items {
		entry := &retainedExchange{exchange: exchange, input: traceInputFor(exchange), bytes: exchangePayloadBytes(exchange)}
		window.ring[i] = entry
		window.entries[exchange.Sequence] = entry
		window.payloadBytes += entry.bytes
	}
	window.count, window.sequence, window.revision = len(items), int64(len(items)), uint64(len(items))
	return store
}

func BenchmarkTracing(b *testing.B) {
	scope := model.EnvironmentSelector("review", "local")
	ctx := context.Background()
	for _, size := range []int{250, 1000, 5000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			for _, workload := range []string{"HTTP", "Mixed", "Overlapping", "NoContext", "DeepChain", "Payload"} {
				items := benchmarkExchanges(size, workload != "HTTP")
				for i := range items {
					switch workload {
					case "Overlapping":
						items[i].StartedAt, items[i].CompletedAt = items[0].StartedAt, items[0].CompletedAt
						items[i].TraceID, items[i].SpanID, items[i].ParentSpanID = "", "", ""
					case "NoContext":
						items[i].TraceID, items[i].SpanID, items[i].ParentSpanID = "", "", ""
					case "DeepChain":
						items[i].TraceID, items[i].SpanID = "chain", fmt.Sprint(i+1)
						items[i].ParentSpanID = ""
						if i > 0 {
							items[i].ParentSpanID = fmt.Sprint(i)
						}
					case "Payload":
						items[i].RequestHeaders = map[string][]string{"Content-Type": {"application/json"}}
						items[i].ResponseBody = string(make([]byte, 8192))
					}
				}
				b.Run("Build"+workload, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						inputs := make([]traceInput, len(items))
						for i, item := range items {
							inputs[i] = traceInputFor(item)
						}
						benchmarkTraceSink = buildProjection(inputs)
					}
				})
			}
			items := benchmarkExchanges(size, true)
			b.Run("List100", func(b *testing.B) {
				store := benchmarkStore(b, items)
				if _, err := store.Traces(ctx, scope, TraceQuery{Limit: 100}); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := store.Traces(ctx, scope, TraceQuery{Limit: 100}); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("DetailOne", func(b *testing.B) {
				store := benchmarkStore(b, items)
				if _, _, err := store.Trace(ctx, scope, 1); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, found, err := store.Trace(ctx, scope, 1); err != nil || !found {
						b.Fatalf("detail missing: %v", err)
					}
				}
			})
			b.Run("Append", func(b *testing.B) {
				store := benchmarkStore(b, items)
				next := benchmarkExchanges(1, false)[0]
				next.StartedAt = next.StartedAt.Add(24 * time.Hour)
				next.CompletedAt = next.StartedAt.Add(time.Millisecond)
				b.ReportAllocs()
				for b.Loop() {
					benchmarkExchangeSink = store.AddExchange(next)
				}
			})
		})
	}
}

func BenchmarkTracingColdReaders(b *testing.B) {
	for _, readers := range []int{1, 8} {
		b.Run(fmt.Sprint(readers), func(b *testing.B) {
			store := benchmarkStore(b, benchmarkExchanges(5000, true))
			ctx := context.Background()
			if _, err := store.Traces(ctx, "review/local", TraceQuery{Limit: 100}); err != nil {
				b.Fatal(err)
			}
			initialBuilds := store.builds.Load()
			next := benchmarkExchanges(1, false)[0]
			next.StartedAt = next.StartedAt.Add(24 * time.Hour)
			next.CompletedAt = next.StartedAt.Add(time.Millisecond)
			failures := make(chan error, readers)
			b.ReportAllocs()
			for b.Loop() {
				store.AddExchange(next)
				var group sync.WaitGroup
				for range readers {
					group.Go(func() {
						_, err := store.Traces(ctx, "review/local", TraceQuery{Limit: 100})
						if err != nil {
							failures <- err
						}
					})
				}
				group.Wait()
				select {
				case err := <-failures:
					b.Fatal(err)
				default:
				}
			}
			b.ReportMetric(float64(store.builds.Load()-initialBuilds)/float64(b.N), "builds/op")
		})
	}
}

func BenchmarkTracingLiveBatch(b *testing.B) {
	store := benchmarkStore(b, benchmarkExchanges(5000, true))
	store.broker = events.NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := store.Traces(ctx, "review/local", TraceQuery{Limit: 100}); err != nil {
		b.Fatal(err)
	}
	subscription := store.broker.Subscribe(ctx, "review/local", []string{"traffic.trace"})
	defer subscription.Close()
	initialBuilds := store.builds.Load()
	next := benchmarkExchanges(1, false)[0]
	next.StartedAt = next.StartedAt.Add(24 * time.Hour)
	next.CompletedAt = next.StartedAt.Add(time.Millisecond)
	b.ReportAllocs()
	for b.Loop() {
		var sequence int64
		for range 100 {
			sequence = store.AddExchange(next).Sequence
		}
		for {
			select {
			case event := <-subscription.C:
				if event.Data.(model.TrafficTrace).Revision >= uint64(sequence) {
					goto projected
				}
			case <-ctx.Done():
				b.Fatal(ctx.Err())
			}
		}
	projected:
	}
	b.ReportMetric(float64(store.builds.Load()-initialBuilds)/float64(b.N), "builds/burst")
}
