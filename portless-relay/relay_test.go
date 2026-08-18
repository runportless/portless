package relay

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
	go func() { _ = ServeRelay(ctx, listener, targetPath, 4) }()

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
	go func() { _ = ServeRelay(ctx, listener, missingPath, 4) }()

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
