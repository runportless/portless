package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestExchangeTraceContextPreservesW3CParentAndCreatesChild(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	headers := http.Header{"Traceparent": {parent}}
	context := newExchangeTraceContext(headers)
	if context.traceID != "4bf92f3577b34da6a3ce929d0e0e4736" || context.parentSpanID != "00f067aa0ba902b7" || context.flags != "00" || context.source != model.TrafficTraceContextW3C {
		t.Fatalf("unexpected context: %#v", context)
	}
	if !validTraceHex(context.spanID, 8) || context.spanID == context.parentSpanID {
		t.Fatalf("generated span ID = %q", context.spanID)
	}
	outgoing := headers.Clone()
	context.inject(outgoing)
	if outgoing.Get("Traceparent") != "00-"+context.traceID+"-"+context.spanID+"-00" {
		t.Fatalf("outgoing traceparent = %q", outgoing.Get("Traceparent"))
	}
}

func TestExchangeTraceContextExtractsAndInjectsB3Single(t *testing.T) {
	headers := http.Header{
		"B3": {"80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-d-05e3ac9a4f6e3b90"},
	}
	context := newExchangeTraceContext(headers)
	if context.traceID != "80f198ee56343ba864fe8b2a57d3eff7" || context.parentSpanID != "e457b5a2e4d86bd1" || context.flags != "01" || context.source != model.TrafficTraceContextB3 {
		t.Fatalf("unexpected B3 single context: %#v", context)
	}
	outgoing := headers.Clone()
	context.inject(outgoing)
	if outgoing.Get("B3") != context.traceID+"-"+context.spanID+"-d" {
		t.Fatalf("outgoing B3 single header = %q", outgoing.Get("B3"))
	}
	if outgoing.Get("Traceparent") != context.header() {
		t.Fatalf("outgoing W3C bridge = %q", outgoing.Get("Traceparent"))
	}
}

func TestExchangeTraceContextExtractsAndInjectsB3Multi(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-B3-TraceId", "48485a3953bb6124")
	headers.Set("X-B3-SpanId", "a2fb4a1d1a96d312")
	headers.Set("X-B3-ParentSpanId", "0020000000000001")
	headers.Set("X-B3-Sampled", "0")
	context := newExchangeTraceContext(headers)
	if context.traceID != "000000000000000048485a3953bb6124" || context.parentSpanID != "a2fb4a1d1a96d312" || context.flags != "00" || context.source != model.TrafficTraceContextB3 {
		t.Fatalf("unexpected B3 multi context: %#v", context)
	}
	outgoing := headers.Clone()
	context.inject(outgoing)
	if outgoing.Get("X-B3-TraceId") != "48485a3953bb6124" || outgoing.Get("X-B3-SpanId") != context.spanID || outgoing.Get("X-B3-ParentSpanId") != "" || outgoing.Get("X-B3-Sampled") != "0" {
		t.Fatalf("outgoing B3 multi headers = %#v", outgoing)
	}
	if outgoing.Get("Traceparent") != context.header() {
		t.Fatalf("outgoing W3C bridge = %q", outgoing.Get("Traceparent"))
	}
}

func TestExchangeTraceContextPrefersB3SingleAndSynchronizesB3Multi(t *testing.T) {
	headers := make(http.Header)
	headers.Set("B3", "80f198ee56343ba864fe8b2a57d3eff7-e457b5a2e4d86bd1-1")
	headers.Set("X-B3-TraceId", "48485a3953bb6124")
	headers.Set("X-B3-SpanId", "a2fb4a1d1a96d312")
	context := newExchangeTraceContext(headers)
	if context.traceID != "80f198ee56343ba864fe8b2a57d3eff7" || context.parentSpanID != "e457b5a2e4d86bd1" {
		t.Fatalf("B3 precedence = %#v", context)
	}
	outgoing := headers.Clone()
	context.inject(outgoing)
	if outgoing.Get("X-B3-TraceId") != context.traceID || outgoing.Get("X-B3-SpanId") != context.spanID {
		t.Fatalf("synchronized B3 headers = %#v", outgoing)
	}
}

func TestExchangeTraceContextExtractsAndInjectsDatadog(t *testing.T) {
	headers := http.Header{
		"X-Datadog-Trace-Id":          {"1"},
		"X-Datadog-Parent-Id":         {"2"},
		"X-Datadog-Sampling-Priority": {"-1"},
		"X-Datadog-Tags":              {"_dd.p.dm=-0,_dd.p.tid=463ac35c9f6413ad"},
	}
	context := newExchangeTraceContext(headers)
	if context.traceID != "463ac35c9f6413ad0000000000000001" || context.parentSpanID != "0000000000000002" || context.flags != "00" || context.source != model.TrafficTraceContextDatadog {
		t.Fatalf("unexpected Datadog context: %#v", context)
	}
	outgoing := headers.Clone()
	context.inject(outgoing)
	span, err := strconv.ParseUint(context.spanID, 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	if outgoing.Get("X-Datadog-Trace-Id") != "1" || outgoing.Get("X-Datadog-Parent-Id") != strconv.FormatUint(span, 10) || outgoing.Get("X-Datadog-Tags") != "_dd.p.dm=-0,_dd.p.tid=463ac35c9f6413ad" {
		t.Fatalf("outgoing Datadog headers = %#v", outgoing)
	}
	if outgoing.Get("Traceparent") != context.header() {
		t.Fatalf("outgoing W3C bridge = %q", outgoing.Get("Traceparent"))
	}
}

func TestExchangeTraceContextPrefersW3CAndSynchronizesAlternateCarriers(t *testing.T) {
	headers := http.Header{
		"Traceparent":         {"00-11111111111111110000000000000001-0000000000000002-01"},
		"X-Datadog-Trace-Id":  {"3"},
		"X-Datadog-Parent-Id": {"4"},
		"X-Datadog-Tags":      {"_dd.p.tid=2222222222222222"},
		"B3":                  {"33333333333333330000000000000003-0000000000000005-1"},
	}
	context := newExchangeTraceContext(headers)
	if context.traceID != "11111111111111110000000000000001" || context.parentSpanID != "0000000000000002" {
		t.Fatalf("context precedence = %#v", context)
	}
	outgoing := headers.Clone()
	context.inject(outgoing)
	if outgoing.Get("B3") != context.traceID+"-"+context.spanID+"-1" || outgoing.Get("X-Datadog-Trace-Id") != "1" || !strings.Contains(outgoing.Get("X-Datadog-Tags"), "_dd.p.tid=1111111111111111") {
		t.Fatalf("synchronized headers = %#v", outgoing)
	}
}

func TestExchangeTraceContextRejectsInvalidParentsAndClassifiesRequests(t *testing.T) {
	headers := http.Header{
		"Traceparent":         {"00-00000000000000000000000000000000-0000000000000000-01"},
		"B3":                  {"0"},
		"X-B3-TraceId":        {"48485a3953bb6124"},
		"X-B3-SpanId":         {"a2fb4a1d1a96d312"},
		"X-Datadog-Trace-Id":  {"0"},
		"X-Datadog-Parent-Id": {"2"},
	}
	context := newExchangeTraceContext(headers)
	if !validTraceHex(context.traceID, 16) || context.parentSpanID != "" || !validTraceHex(context.spanID, 8) || context.source != model.TrafficTraceContextGenerated {
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

func TestInjectedTraceContextRegistryRecognizesEachCarrierWithinItsScope(t *testing.T) {
	context := exchangeTraceContext{traceID: "00000000000000000000000000000001", spanID: "0000000000000002"}
	var registry injectedTraceContextRegistry
	registry.remember("store/local", context)

	headers := make(http.Header)
	headers.Set("Traceparent", "00-00000000000000000000000000000001-0000000000000002-01")
	headers.Set("B3", "00000000000000000000000000000001-0000000000000002-1")
	headers.Set("X-B3-TraceId", "00000000000000000000000000000001")
	headers.Set("X-B3-SpanId", "0000000000000002")
	headers.Set("X-Datadog-Trace-Id", "1")
	headers.Set("X-Datadog-Parent-Id", "2")
	headers.Set("X-Datadog-Sampling-Priority", "1")
	want := tracePropagationW3C | tracePropagationB3Single | tracePropagationB3Multi | tracePropagationDatadog
	if got := registry.formats("store/local", headers); got != want {
		t.Fatalf("recognized formats = %04b, want %04b", got, want)
	}
	if got := registry.formats("store/qa", headers); got != 0 {
		t.Fatalf("cross-environment formats = %04b", got)
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
