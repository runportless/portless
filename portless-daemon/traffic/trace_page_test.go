package traffic

import (
	"encoding/json"
	"github.com/runportless/portless/portless-daemon/model"
	"strings"
	"testing"
)

func TestTracePagePreservesRelationshipsWithoutCopyingPayloads(t *testing.T) {
	store := newTestStore(t, nil)
	var exchange model.TrafficExchange
	if err := json.Unmarshal([]byte(`{"project":"shop","environment":"local","protocol":"http","source":"external","target":"web","traceId":"11111111111111111111111111111111","spanId":"1111111111111111","requestBody":"body-secret","responseHeaders":{"Cookie":["header-secret"]}}`), &exchange); err != nil {
		t.Fatal(err)
	}
	first := store.AddExchange(exchange)
	exchange.Source, exchange.Target = "web", "api"
	exchange.ParentSpanID = exchange.SpanID
	exchange.SpanID = "2222222222222222"
	second := store.AddExchange(exchange)
	page, next, found, err := store.TracePage(t.Context(), "shop/local", first.Sequence, 0, 1)
	if err != nil || !found || next != 1 || len(page.Spans) != 1 || page.SpanCount != 2 {
		t.Fatalf("page=%#v next=%d found=%t err=%v", page, next, found, err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("page leaked payloads=%s", encoded)
	}
	last, next, found, err := store.TracePage(t.Context(), "shop/local", first.Sequence, 1, 1)
	if err != nil || !found || next != 0 || last.Revision != page.Revision || len(last.Spans) != 1 || last.Spans[0].Exchange.Sequence != second.Sequence || last.Spans[0].ParentSequence != first.Sequence {
		t.Fatalf("continuation=%#v next=%d err=%v", last, next, err)
	}
	full, found, err := store.Trace(t.Context(), "shop/local", first.Sequence)
	if err != nil || !found || len(full.Spans) != 2 || full.Spans[0].Exchange.RequestBody != "body-secret" {
		t.Fatal("metadata read modified retained payloads")
	}
}
