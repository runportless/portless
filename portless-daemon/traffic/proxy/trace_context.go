package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

var fallbackTraceCounter atomic.Uint64

type exchangeTraceContext struct {
	traceID      string
	spanID       string
	parentSpanID string
	flags        string
	formats      tracePropagationFormat
	b3Sampling   string
	b3TraceWidth int
}

type tracePropagationFormat uint8

const (
	tracePropagationW3C tracePropagationFormat = 1 << iota
	tracePropagationB3Single
	tracePropagationB3Multi
	tracePropagationDatadog
)

type extractedTraceContext struct {
	traceID      string
	parentSpanID string
	flags        string
	b3Sampling   string
	b3TraceWidth int
}

func newExchangeTraceContext(headers http.Header) exchangeTraceContext {
	context := exchangeTraceContext{spanID: randomTraceHex(8), flags: "01"}
	w3c, w3cOK := extractW3CTraceContext(headers.Get("Traceparent"))
	b3, b3Formats, b3OK := extractB3TraceContext(headers)
	datadog, datadogOK := extractDatadogTraceContext(headers)
	if w3cOK {
		context.formats |= tracePropagationW3C
	}
	context.formats |= b3Formats
	if datadogOK {
		context.formats |= tracePropagationDatadog
	}

	selected, ok := w3c, w3cOK
	if !ok {
		selected, ok = b3, b3OK
	}
	if !ok {
		selected, ok = datadog, datadogOK
	}
	if ok {
		context.traceID = selected.traceID
		context.parentSpanID = selected.parentSpanID
		context.flags = selected.flags
	}
	if b3OK {
		context.b3Sampling = b3.b3Sampling
		context.b3TraceWidth = b3.b3TraceWidth
	}
	if context.traceID == "" {
		context.traceID = randomTraceHex(16)
	}
	return context
}

func (c exchangeTraceContext) header() string {
	return "00-" + c.traceID + "-" + c.spanID + "-" + c.flags
}

func (c exchangeTraceContext) inject(headers http.Header) {
	headers.Set("Traceparent", c.header())
	if c.formats&tracePropagationB3Single != 0 {
		value := c.b3TraceID() + "-" + c.spanID
		if c.b3Sampling != "" {
			value += "-" + c.b3Sampling
		}
		headers.Set("B3", value)
	}
	if c.formats&tracePropagationB3Multi != 0 {
		headers.Set("X-B3-TraceId", c.b3TraceID())
		headers.Set("X-B3-SpanId", c.spanID)
		headers.Del("X-B3-ParentSpanId")
	}
	if c.formats&tracePropagationDatadog != 0 {
		traceLow, _ := strconv.ParseUint(c.traceID[16:], 16, 64)
		span, _ := strconv.ParseUint(c.spanID, 16, 64)
		headers.Set("X-Datadog-Trace-Id", strconv.FormatUint(traceLow, 10))
		headers.Set("X-Datadog-Parent-Id", strconv.FormatUint(span, 10))
		setDatadogTraceHigh(headers, c.traceID[:16])
	}
}

func (c exchangeTraceContext) b3TraceID() string {
	if c.b3TraceWidth == 16 && c.traceID[:16] == "0000000000000000" {
		return c.traceID[16:]
	}
	return c.traceID
}

func extractW3CTraceContext(traceparent string) (extractedTraceContext, bool) {
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) != 4 || parts[0] != "00" || !validTraceHex(parts[1], 16) || !validTraceHex(parts[2], 8) || !validFixedHex(parts[3], 1) {
		return extractedTraceContext{}, false
	}
	return extractedTraceContext{
		traceID: strings.ToLower(parts[1]), parentSpanID: strings.ToLower(parts[2]), flags: strings.ToLower(parts[3]),
	}, true
}

func extractB3TraceContext(headers http.Header) (extractedTraceContext, tracePropagationFormat, bool) {
	singleValue := strings.TrimSpace(headers.Get("B3"))
	single, singleOK := parseB3Single(singleValue)
	if singleValue != "" {
		if !singleOK {
			return extractedTraceContext{}, 0, false
		}
		formats := tracePropagationB3Single
		if _, multiOK := parseB3Multi(headers); multiOK {
			formats |= tracePropagationB3Multi
		}
		return single, formats, true
	}
	multi, multiOK := parseB3Multi(headers)
	if !multiOK {
		return extractedTraceContext{}, 0, false
	}
	return multi, tracePropagationB3Multi, true
}

func parseB3Single(value string) (extractedTraceContext, bool) {
	parts := strings.Split(value, "-")
	if len(parts) < 2 || len(parts) > 4 {
		return extractedTraceContext{}, false
	}
	traceID, width, ok := normalizeB3TraceID(parts[0])
	if !ok || !validTraceHex(parts[1], 8) {
		return extractedTraceContext{}, false
	}
	sampling := ""
	flags := "01"
	if len(parts) >= 3 {
		sampling = strings.ToLower(parts[2])
		switch sampling {
		case "", "1", "d":
			flags = "01"
		case "0":
			flags = "00"
		default:
			return extractedTraceContext{}, false
		}
	}
	if len(parts) == 4 && !validTraceHex(parts[3], 8) {
		return extractedTraceContext{}, false
	}
	return extractedTraceContext{
		traceID: traceID, parentSpanID: strings.ToLower(parts[1]), flags: flags,
		b3Sampling: sampling, b3TraceWidth: width,
	}, true
}

func parseB3Multi(headers http.Header) (extractedTraceContext, bool) {
	traceID, width, ok := normalizeB3TraceID(headers.Get("X-B3-TraceId"))
	spanID := strings.TrimSpace(headers.Get("X-B3-SpanId"))
	if !ok || !validTraceHex(spanID, 8) {
		return extractedTraceContext{}, false
	}
	flags := "01"
	if strings.TrimSpace(headers.Get("X-B3-Flags")) != "1" {
		switch strings.ToLower(strings.TrimSpace(headers.Get("X-B3-Sampled"))) {
		case "", "1", "true":
			flags = "01"
		case "0", "false":
			flags = "00"
		default:
			return extractedTraceContext{}, false
		}
	}
	return extractedTraceContext{
		traceID: traceID, parentSpanID: strings.ToLower(spanID), flags: flags, b3TraceWidth: width,
	}, true
}

func normalizeB3TraceID(value string) (string, int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch len(value) {
	case 16:
		if !validTraceHex(value, 8) {
			return "", 0, false
		}
		return "0000000000000000" + value, 16, true
	case 32:
		if !validTraceHex(value, 16) {
			return "", 0, false
		}
		return value, 32, true
	default:
		return "", 0, false
	}
}

func extractDatadogTraceContext(headers http.Header) (extractedTraceContext, bool) {
	traceLow, traceOK := parseDecimalTraceID(headers.Get("X-Datadog-Trace-Id"))
	parent, parentOK := parseDecimalTraceID(headers.Get("X-Datadog-Parent-Id"))
	if !traceOK || !parentOK {
		return extractedTraceContext{}, false
	}
	flags := "01"
	if priority := strings.TrimSpace(headers.Get("X-Datadog-Sampling-Priority")); priority != "" {
		value, err := strconv.ParseInt(priority, 10, 64)
		if err != nil {
			return extractedTraceContext{}, false
		}
		if value <= 0 {
			flags = "00"
		}
	}
	return extractedTraceContext{
		traceID:      datadogTraceHigh(headers.Get("X-Datadog-Tags")) + traceLow,
		parentSpanID: parent, flags: flags,
	}, true
}

func parseDecimalTraceID(value string) (string, bool) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return "", false
	}
	encoded := strconv.FormatUint(parsed, 16)
	return strings.Repeat("0", 16-len(encoded)) + encoded, true
}

func datadogTraceHigh(tags string) string {
	for _, tag := range strings.Split(tags, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(tag), "=")
		if ok && name == "_dd.p.tid" && validFixedHex(value, 8) {
			return strings.ToLower(value)
		}
	}
	return "0000000000000000"
}

func setDatadogTraceHigh(headers http.Header, high string) {
	tags := make([]string, 0)
	for _, tag := range strings.Split(headers.Get("X-Datadog-Tags"), ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.HasPrefix(tag, "_dd.p.tid=") {
			continue
		}
		tags = append(tags, tag)
	}
	if high != "0000000000000000" {
		tags = append(tags, "_dd.p.tid="+high)
	}
	if len(tags) == 0 {
		headers.Del("X-Datadog-Tags")
		return
	}
	headers.Set("X-Datadog-Tags", strings.Join(tags, ","))
}

func randomTraceHex(size int) string {
	content := make([]byte, size)
	if _, err := rand.Read(content); err == nil {
		return hex.EncodeToString(content)
	}
	// Tracing must never take down an application request. The counter makes
	// this process-local fallback unique even if the clock has low resolution.
	seed := strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.FormatUint(fallbackTraceCounter.Add(1), 10)
	digest := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(digest[:size])
}

func validTraceHex(value string, bytes int) bool {
	return validFixedHex(value, bytes) && strings.Trim(value, "0") != ""
}

func validFixedHex(value string, bytes int) bool {
	if len(value) != bytes*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func exactRequestTarget(requestURL *url.URL) string {
	requestTarget := requestURL.EscapedPath()
	if requestTarget == "" {
		requestTarget = "/"
	}
	if requestURL.RawQuery != "" {
		requestTarget += "?" + requestURL.RawQuery
	}
	return requestTarget
}

func classifyRequest(source string, request *http.Request) model.TrafficRequestKind {
	if source != "external" {
		return model.TrafficRequestService
	}
	mode := strings.ToLower(request.Header.Get("Sec-Fetch-Mode"))
	destination := strings.ToLower(request.Header.Get("Sec-Fetch-Dest"))
	if mode == "navigate" || destination == "document" {
		return model.TrafficRequestNavigation
	}
	if destination != "" && destination != "empty" {
		return model.TrafficRequestSubresource
	}
	if strings.HasPrefix(strings.ToLower(request.URL.Path), "/favicon.") {
		return model.TrafficRequestSubresource
	}
	if destination == "empty" || mode == "cors" || mode == "same-origin" || request.Header.Get("X-Requested-With") != "" {
		return model.TrafficRequestFetch
	}
	return model.TrafficRequestUnknown
}

func capturedBytes(capture *bodyCapture) int64 {
	if capture == nil {
		return 0
	}
	return int64(len(capture.body))
}
