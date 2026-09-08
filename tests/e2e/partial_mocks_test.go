//go:build e2e

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestCLIPartialMocksPreserveProcessesDependenciesAndWebSockets(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	run := func(args ...string) string {
		t.Helper()
		output, err := runCLIAt(binary, home, checkout, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s\n%s", args, err, output, readDaemonLog(home))
		}
		return output
	}
	run("up", "--name", "partial-e2e", "--no-open", "--timeout", "2m")
	before := environmentStatus(t, binary, home, checkout)
	run("mock", "create", "partial", "--unmatched-requests", "forward")
	run("mock", "route", "set", "partial", "fixed", "--service", "checkout", "--path", "/partial-fixed", "--status", "503", "--body", "fixed failure")
	preview := run("--json", "mock", "preview", "partial", "--service", "checkout", "--path", "/checkout")
	var prediction model.MockPreview
	if err := json.Unmarshal([]byte(preview), &prediction); err != nil || prediction.Outcome != "forward" || prediction.Response != nil {
		t.Fatalf("preview=%s %v", preview, err)
	}
	const host = "checkout.local.partial-e2e.localhost"
	c, reader := openApplicationWebSocket(t, home, host, "/api/ws")
	run("mock", "enable", "partial")
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	exchangeWebSocketMessage(t, c, reader)
	request := func(path string, expected int) string {
		t.Helper()
		response := applicationRequest(t, home, host, path, nil)
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != expected {
			t.Fatalf("%s: %s %s", path, response.Status, body)
		}
		return string(body)
	}
	if request("/partial-fixed", 503) != "fixed failure" {
		t.Fatal("mock response changed")
	}
	if !strings.Contains(request("/checkout?sku=coffee-mug&quantity=1", http.StatusOK), "accepted") {
		t.Fatal("partial caller lost outgoing dependencies")
	}
	for _, previous := range before.Services {
		assertSameServiceProcess(t, previous, requireService(t, environmentStatus(t, binary, home, checkout), previous.Name))
	}
	run("mock", "route", "set", "partial", "fixed", "--service", "checkout", "--path", "/partial-fixed", "--status", "202", "--body", "edited")
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	exchangeWebSocketMessage(t, c, reader)
	request("/partial-fixed", 202)
	run("mock", "disable", "partial")
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	exchangeWebSocketMessage(t, c, reader)
	_ = c.Close()
	request("/partial-fixed", 404)
	run("mock", "enable", "partial")
	started := time.Now()
	run("daemon", "restart")
	if time.Since(started) > 5*time.Second {
		t.Fatal("partial policy broke normal restart deadline")
	}
	request("/partial-fixed", 202)
	request("/checkout?sku=coffee-mug&quantity=1", 200)
	recovered := environmentStatus(t, binary, home, checkout)
	for _, previous := range before.Services {
		assertSameServiceProcess(t, previous, requireService(t, recovered, previous.Name))
	}
	run("mock", "route", "set", "partial", "fixed", "--service", "checkout", "--path", "/partial-fixed", "--status", "202", "--disabled")
	request("/partial-fixed", 404)
	traffic := run("--json", "traffic", "list", "--edge", "external:checkout", "--limit", "100")
	var captured struct{ Exchanges []model.TrafficExchange }
	if err := json.Unmarshal([]byte(traffic), &captured); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]bool{}
	for _, exchange := range captured.Exchanges {
		if exchange.MockScenario == "partial" {
			outcomes[exchange.MockOutcome] = true
		}
	}
	if !outcomes["mocked"] || !outcomes["forwarded"] {
		t.Fatalf("missing routing outcomes: %#v", outcomes)
	}
	run("down")
	response := applicationRequest(t, home, host, "/partial-fixed", nil)
	response.Body.Close()
	if response.StatusCode == 202 {
		t.Fatal("stopped partial service still admitted mock traffic")
	}
	run("up", "--no-open", "--timeout", "2m")
	request("/checkout?sku=coffee-mug&quantity=1", 200)
	run("mock", "disable", "partial")
	run("mock", "create", "full", "--unmatched-requests", "reject")
	run("mock", "route", "set", "full", "fixed", "--service", "checkout", "--path", "/partial-fixed", "--status", "202")
	strict := run("--json", "mock", "preview", "full", "--service", "checkout", "--path", "/checkout")
	if err := json.Unmarshal([]byte(strict), &prediction); err != nil || prediction.Outcome != "rejected" || prediction.Response.Status != 501 {
		t.Fatalf("strict preview=%s %v", strict, err)
	}
}
