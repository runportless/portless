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

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 2 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String())
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
