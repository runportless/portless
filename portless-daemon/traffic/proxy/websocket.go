package proxy

import (
	"crypto/sha1" // RFC 6455 requires SHA-1 for its non-secret handshake accept value.
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const websocketWriteTimeout = 5 * time.Second

type websocketFailure struct {
	status  int
	message string
}

func headerHasToken(headers http.Header, name, token string) bool {
	for _, value := range headers.Values(name) {
		for candidate := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(candidate), token) {
				return true
			}
		}
	}
	return false
}

func isHTTPUpgrade(headers http.Header) bool {
	return len(headers.Values("Upgrade")) != 0 || headerHasToken(headers, "Connection", "upgrade")
}

func conflictingWebSocketHeaders(headers http.Header) bool {
	for _, value := range headers.Values("Connection") {
		for name := range strings.SplitSeq(value, ",") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "sec-websocket-") {
				return true
			}
		}
	}
	return false
}

func singleWebSocketHeader(headers http.Header, name string) string {
	values := headers.Values(name)
	if len(values) != 1 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func validateWebSocketRequest(r *http.Request) *websocketFailure {
	if len(r.Header.Values("Upgrade")) != 1 || singleWebSocketHeader(r.Header, "Upgrade") == "" {
		return &websocketFailure{http.StatusBadRequest, "invalid upgrade header"}
	}
	if !strings.EqualFold(singleWebSocketHeader(r.Header, "Upgrade"), "websocket") {
		return &websocketFailure{http.StatusNotImplemented, "unsupported upgrade protocol"}
	}
	if r.Method != http.MethodGet || r.ProtoMajor != 1 || r.ProtoMinor != 1 ||
		!headerHasToken(r.Header, "Connection", "upgrade") || conflictingWebSocketHeaders(r.Header) ||
		r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		return &websocketFailure{http.StatusBadRequest, "invalid WebSocket handshake"}
	}
	if len(r.Header.Values("Sec-WebSocket-Version")) != 1 {
		return &websocketFailure{http.StatusBadRequest, "invalid WebSocket version header"}
	}
	if singleWebSocketHeader(r.Header, "Sec-WebSocket-Version") != "13" {
		return &websocketFailure{http.StatusUpgradeRequired, "WebSocket version 13 is required"}
	}
	key, err := base64.StdEncoding.DecodeString(singleWebSocketHeader(r.Header, "Sec-WebSocket-Key"))
	if err != nil || len(key) != 16 {
		return &websocketFailure{http.StatusBadRequest, "invalid WebSocket key"}
	}
	return nil
}

func validateWebSocketResponse(request *http.Request, response *http.Response) (io.ReadWriteCloser, error) {
	if !headerHasToken(response.Header, "Connection", "upgrade") ||
		!strings.EqualFold(singleWebSocketHeader(response.Header, "Upgrade"), "websocket") || conflictingWebSocketHeaders(response.Header) {
		return nil, errors.New("upstream returned an invalid WebSocket upgrade")
	}
	sum := sha1.Sum([]byte(singleWebSocketHeader(request.Header, "Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if singleWebSocketHeader(response.Header, "Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]) {
		return nil, errors.New("upstream returned an invalid WebSocket accept value")
	}
	if values := response.Header.Values("Sec-WebSocket-Protocol"); len(values) != 0 {
		selected := singleWebSocketHeader(response.Header, "Sec-WebSocket-Protocol")
		offered := false
		for _, value := range request.Header.Values("Sec-WebSocket-Protocol") {
			for candidate := range strings.SplitSeq(value, ",") {
				if selected != "" && strings.TrimSpace(candidate) == selected {
					offered = true
				}
			}
		}
		if !offered {
			return nil, errors.New("upstream selected an unoffered WebSocket subprotocol")
		}
	}
	connection, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		return nil, errors.New("upstream WebSocket connection is not writable")
	}
	return connection, nil
}

func newWebSocketTransport() *http.Transport {
	transport := newUpstreamTransport(30 * time.Second)
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.MaxResponseHeaderBytes = 64 << 10
	return transport
}

// switchWebSocket finishes HTTP accounting at the handshake boundary, then owns
// both copy loops until cancellation or either peer closes the transport.
func switchWebSocket(writer http.ResponseWriter, request *http.Request, response *http.Response, session *websocketSession, finish func(int, string, http.Header)) {
	failHTTP := func(message string) {
		http.Error(writer, "Portless: "+message, http.StatusBadGateway)
		finish(http.StatusBadGateway, message, writer.Header())
	}
	upstream, err := validateWebSocketResponse(request, response)
	if err != nil {
		failHTTP(err.Error())
		return
	}
	if !session.attach(upstream) {
		failHTTP("WebSocket handshake canceled")
		return
	}
	downstream, buffered, err := http.NewResponseController(writer).Hijack()
	if err != nil {
		failHTTP("WebSocket connection handoff failed")
		return
	}
	if !session.attach(downstream) {
		finish(0, "WebSocket handshake canceled", nil)
		return
	}
	if err := downstream.SetDeadline(time.Time{}); err != nil {
		finish(0, "WebSocket deadline reset failed", nil)
		return
	}
	if err := downstream.SetWriteDeadline(time.Now().Add(websocketWriteTimeout)); err != nil {
		finish(0, "WebSocket handshake deadline failed", nil)
		return
	}
	headers := response.Header.Clone()
	removeHopHeaders(headers)
	headers.Del("Content-Length")
	headers.Set("Connection", "Upgrade")
	headers.Set("Upgrade", "websocket")
	copyHeaders(writer.Header(), headers)
	handshake := &http.Response{StatusCode: http.StatusSwitchingProtocols, ProtoMajor: 1, ProtoMinor: 1, Header: writer.Header()}
	if err := handshake.Write(buffered); err != nil {
		finish(0, "WebSocket handshake write failed", nil)
		return
	}
	if err := buffered.Flush(); err != nil {
		finish(0, "WebSocket handshake flush failed", nil)
		return
	}
	if err := downstream.SetWriteDeadline(time.Time{}); err != nil {
		finish(0, "WebSocket deadline reset failed", nil)
		return
	}
	finish(http.StatusSwitchingProtocols, "", headers)
	copied := make(chan struct{}, 2)
	go func() { _, _ = io.CopyBuffer(upstream, buffered.Reader, make([]byte, 32<<10)); copied <- struct{}{} }()
	go func() { _, _ = io.CopyBuffer(downstream, upstream, make([]byte, 32<<10)); copied <- struct{}{} }()
	<-copied
	session.close()
	<-copied
}
