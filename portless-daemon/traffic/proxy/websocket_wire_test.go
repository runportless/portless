package proxy

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestWebSocketPreservesBufferedFramesAndNegotiatedCompression(t *testing.T) {
	var compressed bytes.Buffer
	compressor, _ := flate.NewWriter(&compressed, flate.DefaultCompression)
	_, _ = io.WriteString(compressor, "compressed message")
	_ = compressor.Flush()
	compressedPayload := bytes.Clone(compressed.Bytes()[:compressed.Len()-4])
	_ = compressor.Close()
	// Server text, binary fragments, compressed text, ping, pong and close.
	serverFrames := []byte{0x81, 2, 'h', 'i', 0x02, 1, 0, 0x80, 1, 255, 0xc1, byte(len(compressedPayload))}
	serverFrames = append(serverFrames, compressedPayload...)
	serverFrames = append(serverFrames, 0x89, 1, 'p', 0x8a, 1, 'p', 0x88, 2, 3, 232)
	clientFrames := []byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2, 0x89, 0x81, 1, 2, 3, 4, 'p' ^ 1, 0x88, 0x82, 1, 2, 3, 4, 3 ^ 1, 232 ^ 2}
	received := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Keep-Alive, Upgrade, X-Hop\r\nUpgrade: WebSocket\r\nX-Hop: private\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Extensions: %s\r\n\r\n", testWebSocketAccept, r.Header.Get("Sec-WebSocket-Extensions"))
		_, _ = rw.Write(serverFrames)
		_ = rw.Flush()
		buf := make([]byte, len(clientFrames))
		_, _ = io.ReadFull(rw.Reader, buf)
		received <- buf
	}))
	defer upstream.Close()
	m := websocketManager(t)
	websocketTarget(t, m, "billing/local", "orders", upstream)
	server := websocketIngress(t, m, "billing/local", "orders")
	c, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	r := websocketRequest()
	r.Header.Set("Sec-WebSocket-Extensions", "permessage-deflate; client_no_context_takeover; server_no_context_takeover")
	writer := bufio.NewWriter(c)
	if err := r.Write(writer); err != nil {
		t.Fatal(err)
	}
	_, _ = writer.Write(clientFrames)
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(c)
	response, err := http.ReadResponse(reader, r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 || response.Header.Get("X-Hop") != "" || response.Header.Get("Sec-WebSocket-Extensions") != r.Header.Get("Sec-WebSocket-Extensions") {
		t.Fatalf("response %#v", response)
	}
	echoed := make([]byte, len(serverFrames))
	if _, err := io.ReadFull(reader, echoed); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(echoed, serverFrames) {
		t.Fatalf("server frames changed: %x", echoed)
	}
	select {
	case got := <-received:
		if !bytes.Equal(got, clientFrames) {
			t.Fatalf("buffered client frames changed: %x", got)
		}
	case <-time.After(time.Second):
		t.Fatal("client frames were lost")
	}
}

func TestWebSocketSecureRemoteUpgrade(t *testing.T) {
	echo := websocketEcho(t)
	secure := httptest.NewUnstartedServer(echo.Config.Handler)
	secure.EnableHTTP2 = true
	secure.StartTLS()
	defer secure.Close()
	m := websocketManager(t)
	m.websocketTransport.TLSClientConfig.RootCAs = x509.NewCertPool()
	m.websocketTransport.TLSClientConfig.RootCAs.AddCert(secure.Certificate())
	if err := m.SetRemoteTarget("billing/local", "orders", model.RemoteTarget{URL: secure.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite}); err != nil {
		t.Fatal(err)
	}
	server := websocketIngress(t, m, "billing/local", "orders")
	c, reader, res := websocketDial(t, server.Listener.Addr().String(), "orders.local.billing.localhost", "/ws")
	if res.StatusCode != 101 {
		t.Fatal(res.Status)
	}
	if _, err := io.WriteString(c, "tls echo"); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 13)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "readytls echo" {
		t.Fatalf("TLS bytes %q", payload)
	}
}

type gatedWebSocketWriter struct {
	*httptest.ResponseRecorder
	connection net.Conn
	entered    chan struct{}
	resume     chan struct{}
}

func (w *gatedWebSocketWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	close(w.entered)
	<-w.resume
	return w.connection, bufio.NewReadWriter(bufio.NewReader(w.connection), bufio.NewWriter(w.connection)), nil
}

func TestWebSocketCancellationDuringHandoffAndBlockedHandshakeWrite(t *testing.T) {
	for _, phase := range []string{"handoff", "write"} {
		t.Run(phase, func(t *testing.T) {
			upstream := websocketEcho(t)
			m := websocketManager(t)
			websocketTarget(t, m, "billing/local", "orders", upstream)
			proxySide, clientSide := net.Pipe()
			defer clientSide.Close()
			w := &gatedWebSocketWriter{ResponseRecorder: httptest.NewRecorder(), connection: proxySide, entered: make(chan struct{}), resume: make(chan struct{})}
			done := make(chan struct{})
			go func() { defer close(done); m.ServeIngress(w, websocketRequest(), "billing/local", "orders") }()
			select {
			case <-w.entered:
			case <-time.After(time.Second):
				close(w.resume)
				t.Fatal("handoff did not begin")
			}
			if phase == "handoff" {
				m.RemoveTarget("billing/local", "orders")
				close(w.resume)
			} else {
				close(w.resume)
				// Reading one byte starts the flush. Stop reading so cancellation must
				// release the remaining blocked header write.
				_ = clientSide.SetReadDeadline(time.Now().Add(time.Second))
				if _, err := clientSide.Read(make([]byte, 1)); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				m.Close(ctx)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handoff leaked after cancellation")
			}
			m.mu.RLock()
			remaining := len(m.websocketSessions)
			m.mu.RUnlock()
			if remaining != 0 {
				t.Fatal("canceled handoff retained capacity")
			}
		})
	}
}
