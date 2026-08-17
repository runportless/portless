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

	"github.com/portless-run/portless/portless-daemon/model"
)

var fallbackTraceCounter atomic.Uint64

type exchangeTraceContext struct {
	traceID      string
	spanID       string
	parentSpanID string
	flags        string
}

func newExchangeTraceContext(traceparent string) exchangeTraceContext {
	context := exchangeTraceContext{spanID: randomTraceHex(8), flags: "01"}
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) == 4 && parts[0] == "00" && validTraceHex(parts[1], 16) && validTraceHex(parts[2], 8) && validTraceHex(parts[3], 1) {
		context.traceID = strings.ToLower(parts[1])
		context.parentSpanID = strings.ToLower(parts[2])
		context.flags = strings.ToLower(parts[3])
	}
	if context.traceID == "" {
		context.traceID = randomTraceHex(16)
	}
	return context
}

func (c exchangeTraceContext) header() string {
	return "00-" + c.traceID + "-" + c.spanID + "-" + c.flags
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
	if len(value) != bytes*2 || strings.Trim(value, "0") == "" {
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
