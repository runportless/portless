package runtime

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRelayForwardsHTTPToPrivateUnixSocket(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "portless-relay-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	targetPath := filepath.Join(directory, "ingress.sock")
	target, err := net.Listen("unix", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go serveOneHTTPResponse(target, "checkout reached")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = serveHTTPRelay(ctx, listener, targetPath, 4) }()

	request, _ := http.NewRequest(http.MethodGet, "http://"+listener.Addr().String()+"/checkout", nil)
	request.Host = "checkout.store.localhost"
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "checkout reached" {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}
}

func TestRelayReturnsServiceUnavailableWhenDaemonSocketIsAbsent(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	missingPath := filepath.Join(t.TempDir(), "missing.sock")
	go func() { _ = serveHTTPRelay(ctx, listener, missingPath, 4) }()

	// Read explicitly after writing: a missing daemon can produce an immediate
	// rejection before net/http registers its pending request under -race.
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = io.WriteString(client, "GET / HTTP/1.1\r\nHost: portless.localhost\r\nConnection: close\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(string(body), "portless up") {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}
	for _, expected := range []string{`class="brand"`, `class="signal"`, `class="spinner"`, `<strong>portless</strong>`, `http-equiv="refresh" content="2"`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("unavailable page does not contain %q", expected)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("unavailable response is missing browser safety headers: %#v", response.Header)
	}
}

func TestUnavailablePageEscapesMessage(t *testing.T) {
	page := renderUnavailablePage(`<script>alert("unsafe")</script>`)
	if strings.Contains(page, `<script>`) || !strings.Contains(page, `&lt;script&gt;`) {
		t.Fatalf("unavailable page did not escape its message: %s", page)
	}
}

func TestHTTPRelayCancellationClosesActiveConnections(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "portless-relay-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	targetPath := filepath.Join(directory, "ingress.sock")
	target, err := net.Listen("unix", targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := target.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- serveHTTPRelay(ctx, listener, targetPath, 1) }()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var upstream net.Conn
	select {
	case upstream = <-accepted:
		defer upstream.Close()
	case <-time.After(time.Second):
		t.Fatal("relay did not establish its upstream connection")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not stop after cancellation")
	}
}

func TestHTTPRelayReportsUnexpectedListenerClosure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "ingress.sock")
	finished := make(chan error, 1)
	go func() {
		finished <- serveHTTPRelay(context.Background(), listener, targetPath, 1)
	}()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil || !strings.Contains(err.Error(), "accept localhost relay connection") {
			t.Fatalf("unexpected listener closure error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not report the unexpected listener closure")
	}
}

func TestRunShutsDownAllProtocolListenersWhenCanceled(t *testing.T) {
	dnsAddress := availableTCPAndUDPAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := run(ctx, config{
		ListenAddress: "127.0.0.1:0", DNSListenAddress: dnsAddress,
		identity: Identity{TargetSocket: filepath.Join(t.TempDir(), "ingress.sock"), DNSTargetSocket: filepath.Join(t.TempDir(), "dns.sock"), UID: 501, GID: 20},
	})
	if err != nil {
		t.Fatalf("canceled relay runtime returned an error: %v", err)
	}
}

func availableTCPAndUDPAddress(t *testing.T) string {
	t.Helper()
	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := tcpListener.Addr().String()
	packetListener, err := net.ListenPacket("udp", address)
	if err != nil {
		tcpListener.Close()
		t.Fatal(err)
	}
	if err := packetListener.Close(); err != nil {
		tcpListener.Close()
		t.Fatal(err)
	}
	if err := tcpListener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func serveOneHTTPResponse(listener net.Listener, body string) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}
	_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\nConnection: close\r\n\r\n"+body)
}

func TestRelayForwardsWebSocketBytesAndClosesOnCancellation(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "portless-relay-ws-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socketPath := filepath.Join(directory, "ingress.sock")
	upstream, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "missing upgrade", 400)
			return
		}
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\nfirst frame")
		_ = rw.Flush()
		_, _ = io.Copy(c, rw.Reader)
	})}
	go func() { _ = server.Serve(upstream) }()
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = serveHTTPRelay(ctx, listener, socketPath, 4); close(done) }()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(client, "GET /ws HTTP/1.1\r\nHost: checkout.local.billing.localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != 101 {
		t.Fatalf("response %#v, error %v", response, err)
	}
	_, _ = io.WriteString(client, "client frame")
	payload := make([]byte, len("first frameclient frame"))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	if string(payload) != "first frameclient frame" {
		t.Fatalf("changed bytes %q", payload)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay did not stop")
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("relay left WebSocket open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("relay timed out instead of closing")
	}
}
