//go:build e2e

package e2e_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func openApplicationWebSocket(t *testing.T, home, host, path string) (net.Conn, *bufio.Reader) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(home, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	var control struct{ Port int }
	if err = json.Unmarshal(content, &control); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", control.Port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	request, err := http.NewRequest(http.MethodGet, "http://"+host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Protocol", "portless-test")
	request.Header.Set("Authorization", "Bearer websocket-e2e-secret")
	if err = request.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 || response.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" || response.Header.Get("Sec-WebSocket-Protocol") != "portless-test" {
		t.Fatalf("WebSocket handshake: %s %#v", response.Status, response.Header)
	}
	return conn, reader
}

func exchangeWebSocketMessage(t *testing.T, c net.Conn, r *bufio.Reader) {
	t.Helper()
	// A small RFC 6455 masked client text frame; the server echo must be unmasked.
	message := []byte("application websocket")
	frame := []byte{0x81, 0x80 | byte(len(message)), 1, 2, 3, 4}
	for i, value := range message {
		frame = append(frame, value^byte(i%4+1))
	}
	if _, err := c.Write(frame); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		t.Fatal(err)
	}
	if header[0] != 0x81 || int(header[1]) != len(message) {
		t.Fatalf("frame header: %x", header)
	}
	echoed := make([]byte, len(message))
	if _, err := io.ReadFull(r, echoed); err != nil {
		t.Fatal(err)
	}
	if string(echoed) != string(message) {
		t.Fatalf("echo: %q", echoed)
	}
}

func requireWebSocketClosed(t *testing.T, c net.Conn, r *bufio.Reader) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := r.ReadByte(); err == nil {
		t.Fatal("connection stayed open")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("connection timed out instead of closing")
	}
	_ = c.Close()
}

func TestCLIWebSocketForwardingRecordingAndLifecycle(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	run := func(args ...string) string {
		t.Helper()
		output, err := runCLIAt(binary, home, checkout, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s\ndaemon log:\n%s", args, err, output, readDaemonLog(home))
		}
		return output
	}
	run("up", "--name", "websockets", "--no-open", "--timeout", "2m")
	const host = "checkout.local.websockets.localhost"
	run("record", "start", "websocket", "--edge", "external:checkout", "--duration", "5m", "--max-events", "20")
	c, reader := openApplicationWebSocket(t, home, host, "/api/ws?run=cli-websocket")
	exchangeWebSocketMessage(t, c, reader)
	output := run("--json", "traffic", "list", "--edge", "external:checkout", "--limit", "30")
	var traffic struct{ Exchanges []model.TrafficExchange }
	if err := json.Unmarshal([]byte(output), &traffic); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range traffic.Exchanges {
		if e.RequestTarget == "/api/ws?run=cli-websocket" {
			found = true
			if e.Status != 101 || e.Source != "external" || e.Target != "checkout" || e.ResponseBytes != 0 || e.ResponseBody != "" {
				t.Fatalf("handshake: %#v", e)
			}
		}
	}
	if !found {
		t.Fatal("open WebSocket handshake missing from traffic")
	}
	run("record", "stop", "websocket")
	exported := run("record", "export", "websocket", "--output", "-")
	if err := json.Unmarshal([]byte(exported), &traffic); err != nil {
		t.Fatal(err)
	}
	if len(traffic.Exchanges) != 1 || traffic.Exchanges[0].Status != 101 || strings.Contains(exported, "websocket-e2e-secret") || strings.Contains(exported, "application websocket") || http.Header(traffic.Exchanges[0].RequestHeaders).Get("Sec-WebSocket-Protocol") != "[REDACTED]" {
		t.Fatalf("recording: %s", exported)
	}
	dependency := applicationRequest(t, home, host, "/websocket-dependency", nil)
	body, err := io.ReadAll(dependency.Body)
	dependency.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if dependency.StatusCode != 200 || !strings.Contains(string(body), "checkout to orders") {
		t.Fatalf("dependency: %s %s", dependency.Status, body)
	}
	output = run("--json", "traffic", "list", "--edge", "checkout:orders", "--limit", "30")
	if err = json.Unmarshal([]byte(output), &traffic); err != nil {
		t.Fatal(err)
	}
	found = false
	for _, e := range traffic.Exchanges {
		if e.Status == 101 && e.Source == "checkout" && e.Target == "orders" {
			found = true
		}
	}
	if !found {
		t.Fatal("dependency handshake missing its source/target")
	}
	before := servicePIDs(environmentStatus(t, binary, home, checkout))
	run("daemon", "restart")
	requireWebSocketClosed(t, c, reader)
	after := servicePIDs(environmentStatus(t, binary, home, checkout))
	if !maps.Equal(before, after) {
		t.Fatalf("daemon restart replaced application processes: %v -> %v", before, after)
	}
	c, reader = openApplicationWebSocket(t, home, host, "/auth/ws?run=after-restart")
	exchangeWebSocketMessage(t, c, reader)
	run("down")
	requireWebSocketClosed(t, c, reader)
	run("up", "--no-open", "--timeout", "2m")
	c, reader = openApplicationWebSocket(t, home, host, "/ws?run=after-up")
	exchangeWebSocketMessage(t, c, reader)
	_ = c.Close()
}
