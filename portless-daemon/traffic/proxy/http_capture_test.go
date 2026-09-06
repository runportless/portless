package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPCaptureRequiresEOFAndPreservesFidelity(t *testing.T) {
	for _, test := range []struct {
		name, body, contentType, encoding, state string
		read                                     bool
		limit                                    int
		exact                                    bool
	}{
		{name: "empty", state: "empty", read: true, limit: 16, exact: true},
		{name: "unread", body: "body", state: "incomplete", limit: 16},
		{name: "text", body: "body", state: "complete", read: true, limit: 16, exact: true},
		{name: "truncated", body: "more than four", state: "truncated", read: true, limit: 4},
		{name: "binary", body: "binary", contentType: "application/octet-stream", state: "unsupported", read: true, limit: 16},
		{name: "compressed", body: "compressed", encoding: "gzip", state: "unsupported", read: true, limit: 16},
		{name: "invalid utf8", body: "\xff\xfe", state: "unsupported", read: true, limit: 16},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "http://test.localhost/", strings.NewReader(test.body))
			if test.body == "" {
				request.Body = http.NoBody
			}
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Content-Encoding", test.encoding)
			capture := captureRequestBody(request, test.limit)
			if test.read && request.Body != http.NoBody {
				_, _ = io.Copy(io.Discard, request.Body)
			}
			metadata := captureMetadata(capture)
			if metadata.State != test.state || metadata.Exact != test.exact {
				t.Fatalf("metadata=%#v", metadata)
			}
			if test.read && metadata.ObservedBytes != int64(len(test.body)) {
				t.Fatalf("observed=%d want=%d", metadata.ObservedBytes, len(test.body))
			}
			if omitted := omittedCapture(metadata); test.state != "empty" && (omitted.State != "omitted" || omitted.Exact || omitted.CapturedBytes != 0) {
				t.Fatalf("persisted metadata=%#v", omitted)
			}
		})
	}
}
