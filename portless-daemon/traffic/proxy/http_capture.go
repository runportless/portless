package proxy

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/model"
)

func newBodyCapture(headers http.Header, limit int) *bodyCapture {
	encoding := strings.ToLower(strings.TrimSpace(headers.Get("Content-Encoding")))
	if encoding == "" {
		encoding = "identity"
	}
	supported := inspectableBody(headers.Get("Content-Type")) && encoding == "identity"
	if !supported {
		limit = 0
	}
	return &bodyCapture{limit: limit, supported: supported, encoding: encoding}
}

func (c *bodyCapture) finish(complete bool) {
	if c != nil {
		c.mu.Lock()
		c.complete = complete
		c.mu.Unlock()
	}
}

func (c *bodyCapture) observed() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func captureMetadata(c *bodyCapture) *model.HTTPCapture {
	if c == nil {
		return &model.HTTPCapture{State: "omitted", Encoding: "identity"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	metadata := &model.HTTPCapture{State: "incomplete", ObservedBytes: c.total, CapturedBytes: int64(len(c.body)), Encoding: c.encoding}
	switch {
	case c.complete && c.total == 0:
		metadata.State, metadata.Exact = "empty", true
	case !c.complete:
		metadata.State = "incomplete"
	case !c.supported:
		metadata.State = "unsupported"
	case c.total > int64(len(c.body)):
		metadata.State = "truncated"
	case c.complete && utf8.Valid(c.body):
		metadata.State, metadata.Exact = "complete", true
	case c.complete:
		metadata.State = "unsupported"
	}
	return metadata
}

func freezeCapture(capture *bodyCapture) *bodyCapture {
	if capture == nil {
		return nil
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return &bodyCapture{body: append([]byte(nil), capture.body...), total: capture.total, limit: capture.limit, complete: capture.complete, supported: capture.supported, encoding: capture.encoding}
}

func omittedCapture(capture *model.HTTPCapture) *model.HTTPCapture {
	if capture == nil {
		return nil
	}
	value := *capture
	if value.State != "empty" {
		value.State, value.Exact = "omitted", false
	}
	value.CapturedBytes = 0
	return &value
}

func boundedCapture(capture *model.HTTPCapture, limit int64) *model.HTTPCapture {
	if capture == nil {
		return nil
	}
	if limit <= 0 {
		limit = trafficBodyLimit
	}
	value := *capture
	if value.CapturedBytes > limit {
		value.CapturedBytes, value.State, value.Exact = limit, "truncated", false
	}
	return &value
}
