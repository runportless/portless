package proxy

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/portless-run/portless/portless-daemon/model"
)

func TestExchangeTraceContextPreservesValidParentAndCreatesChild(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	context := newExchangeTraceContext(parent)
	if context.traceID != "4bf92f3577b34da6a3ce929d0e0e4736" || context.parentSpanID != "00f067aa0ba902b7" || context.flags != "01" {
		t.Fatalf("unexpected context: %#v", context)
	}
	if !validTraceHex(context.spanID, 8) || context.spanID == context.parentSpanID {
		t.Fatalf("generated span ID = %q", context.spanID)
	}
	if context.header() != "00-"+context.traceID+"-"+context.spanID+"-01" {
		t.Fatalf("outgoing traceparent = %q", context.header())
	}
}

func TestExchangeTraceContextRejectsInvalidParentAndClassifiesRequests(t *testing.T) {
	context := newExchangeTraceContext("00-00000000000000000000000000000000-0000000000000000-01")
	if !validTraceHex(context.traceID, 16) || context.parentSpanID != "" || !validTraceHex(context.spanID, 8) {
		t.Fatalf("invalid parent was accepted: %#v", context)
	}

	request := httptest.NewRequest("GET", "http://checkout.local.store.localhost/checkout", nil)
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	if kind := classifyRequest("external", request); kind != model.TrafficRequestNavigation {
		t.Fatalf("request kind = %q, want navigation", kind)
	}
	if kind := classifyRequest("checkout", request); kind != model.TrafficRequestService {
		t.Fatalf("service request kind = %q", kind)
	}
}

func TestExactRequestTargetPreservesEscapingAndRawQuery(t *testing.T) {
	value, err := url.Parse("http://orders.local.store.localhost/orders/%2Fitem?sku=coffee%20mug&quantity=2")
	if err != nil {
		t.Fatal(err)
	}
	if target := exactRequestTarget(value); target != "/orders/%2Fitem?sku=coffee%20mug&quantity=2" {
		t.Fatalf("request target = %q", target)
	}
}
