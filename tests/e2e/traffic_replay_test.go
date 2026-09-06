//go:build e2e

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestCLITrafficReplay(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "replay-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("portless up: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	const host = "checkout.local.replay-e2e.localhost"
	const originalTarget = "/api/orders?tag=original&tag=two"
	response := applicationRequest(t, home, host, originalTarget, map[string]string{"X-E2E-Application": "original", "X-E2E-Remove": "drop"})
	originalBody, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("capture original request: status=%s err=%v body=%s", response.Status, err, originalBody)
	}
	original := replayCapturedExchange(t, binary, home, checkout, "external:checkout", "/api/orders")

	// A dependency replay retains its caller, independently of public ingress.
	response = applicationRequest(t, home, host, "/checkout?sku=coffee-mug&quantity=2", nil)
	_, err = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("capture dependency request: status=%s err=%v", response.Status, err)
	}
	dependency := replayCapturedExchange(t, binary, home, checkout, "checkout:orders", "/orders")
	dependencyReplay, output, err := replayCLI(t, binary, home, checkout, strconv.FormatInt(dependency.Sequence, 10))
	if err != nil {
		t.Fatalf("replay dependency: %v\n%s", err, output)
	}
	assertReplayResult(t, dependencyReplay, dependency, "checkout", "orders")
	if dependencyReplay.Result.Exchange.Status != http.StatusOK {
		t.Fatalf("dependency replay response: %#v", dependencyReplay.Result.Exchange)
	}

	ordinary, output, err := replayCLI(t, binary, home, checkout, strconv.FormatInt(original.Sequence, 10))
	if err != nil {
		t.Fatalf("replay original: %v\n%s", err, output)
	}
	assertReplayResult(t, ordinary, original, "external", "checkout")
	if ordinary.Result.Exchange.ResponseBody != string(originalBody) || ordinary.Result.Comparison.Body.State != "equal" {
		t.Fatalf("ordinary replay changed the response: %#v", ordinary.Result)
	}

	const replacement = `{"message":"edited replay"}`
	bodyFile := filepath.Join(checkout, "replay-body.json")
	if err := os.WriteFile(bodyFile, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	edited, output, err := replayCLI(t, binary, home, checkout, strconv.FormatInt(original.Sequence, 10),
		"--method", "POST", "--path", "/auth/login?tag=one&tag=two&search=coffee%20mug",
		"--remove-header", "X-E2E-Remove", "--header", "X-E2E-Application: edited",
		"--header", "X-E2E-Repeated: first", "--header", "X-E2E-Repeated: second",
		"--header", "Content-Type: application/json", "--body-file", bodyFile)
	if err != nil {
		t.Fatalf("replay edited request: %v\n%s", err, output)
	}
	assertReplayResult(t, edited, original, "external", "checkout")
	var echoed struct{ Method, Path, Query, Body, Header string }
	if err := json.Unmarshal([]byte(edited.Result.Exchange.ResponseBody), &echoed); err != nil {
		t.Fatalf("decode edited application response: %v\n%s", err, output)
	}
	if echoed.Method != "POST" || echoed.Path != "/auth/login" || echoed.Query != "tag=one&tag=two&search=coffee%20mug" || echoed.Body != replacement || echoed.Header != "edited" {
		t.Fatalf("edited request did not reach the application intact: %#v", echoed)
	}
	if values := edited.Result.Exchange.RequestHeaders["X-E2e-Repeated"]; len(values) != 2 || values[0] != "first" || values[1] != "second" {
		t.Fatalf("repeated headers were lost: %#v", edited.Result.Exchange.RequestHeaders)
	}
	if values := edited.Result.Exchange.RequestHeaders["X-E2e-Remove"]; len(values) != 0 {
		t.Fatalf("removed header reached the application: %#v", values)
	}
	if edited.Baseline.RequestTarget != originalTarget || edited.Baseline.ResponseBody != string(originalBody) || edited.Result.Comparison.Body.State != "different" {
		t.Fatalf("editing did not preserve the frozen baseline and comparison: %#v", edited)
	}
	shown, output, err := replayCLI(t, binary, home, checkout, "show", strconv.FormatInt(edited.Number, 10),
		"--expected-created-at", edited.CreatedAt.Format(time.RFC3339Nano),
		"--expected-daemon-started-at", edited.DaemonStartedAt.Format(time.RFC3339Nano))
	if err != nil || shown.Result == nil || shown.Result.Exchange.Sequence != edited.Result.Exchange.Sequence || shown.NextRunNumber != 2 {
		t.Fatalf("show replay receipt: %v\n%s", err, output)
	}
	retained := replayExchangeDetail(t, binary, home, checkout, edited.Result.Exchange.Sequence)
	if retained.Replay == nil || *retained.Replay != *edited.Result.Exchange.Replay {
		t.Fatalf("traffic inspection lost replay provenance: %#v", retained)
	}
	unchanged := replayExchangeDetail(t, binary, home, checkout, original.Sequence)
	if unchanged.Replay != nil || unchanged.RequestTarget != originalTarget || unchanged.ResponseBody != string(originalBody) {
		t.Fatalf("replay mutated the original traffic: %#v", unchanged)
	}

	// An observed HTTP error is a successful inspection, while an aborted send
	// returns a failed receipt and one JSON object with a nonzero process status.
	missing, output, err := replayCLI(t, binary, home, checkout, strconv.FormatInt(original.Sequence, 10), "--path", "/missing-replay-route")
	if err != nil || missing.Result == nil || missing.Result.Exchange.Status != http.StatusNotFound || !missing.Result.Comparison.StatusChanged {
		t.Fatalf("HTTP error response should remain inspectable: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "fault", "add", "abort-replay", "external:checkout", "--path", "/api/orders", "--abort"); err != nil {
		t.Fatalf("add replay abort fault: %v\n%s", err, output)
	}
	failed, output, err := replayCLI(t, binary, home, checkout, strconv.FormatInt(original.Sequence, 10))
	if err == nil || failed.Run == nil || failed.Run.State != "failed" || failed.Run.Outcome != "not-sent" || failed.Result == nil {
		t.Fatalf("aborted replay must fail with its JSON receipt: err=%v\n%s", err, output)
	}
	failedShown, output, err := replayCLI(t, binary, home, checkout, "show", strconv.FormatInt(failed.Number, 10))
	if err == nil || failedShown.Run == nil || failedShown.Run.Number != failed.Run.Number || failedShown.Run.State != "failed" {
		t.Fatalf("show failed replay must preserve failure status and receipt: err=%v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "fault", "disable", "abort-replay"); err != nil {
		t.Fatalf("disable replay abort fault: %v\n%s", err, output)
	}

	var remoteReads, remoteWrites atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/health" {
			_, _ = io.WriteString(writer, `{"status":"ready"}`)
			return
		}
		if request.Method == http.MethodGet {
			remoteReads.Add(1)
		} else {
			remoteWrites.Add(1)
		}
		_, _ = io.WriteString(writer, `{"provider":"qa"}`)
	}))
	defer remote.Close()
	if output, err := runCLIAt(binary, home, checkout, "env", "bind", "orders", "--remote", remote.URL,
		"--classification", "qa", "--write-policy", "read-only", "--health-path", "/health"); err != nil {
		t.Fatalf("bind replay destination read-only: %v\n%s", err, output)
	}
	remoteReplay, output, err := replayCLI(t, binary, home, checkout, strconv.FormatInt(dependency.Sequence, 10))
	if err != nil {
		t.Fatalf("replay read-only remote GET: %v\n%s", err, output)
	}
	assertReplayResult(t, remoteReplay, dependency, "checkout", "orders")
	if remoteReads.Load() != 1 || remoteReplay.Result.Destination.Provider != "remote" || remoteReplay.Result.Destination.WritePolicy != "read-only" {
		t.Fatalf("remote replay did not use the effective binding: reads=%d result=%#v", remoteReads.Load(), remoteReplay.Result)
	}
	output, err = runCLIAt(binary, home, checkout, "--json", "traffic", "replay", strconv.FormatInt(dependency.Sequence, 10), "--method", "POST", "--yes")
	if err == nil || !strings.Contains(output, "replay_remote_read_only") || remoteWrites.Load() != 0 || remoteReads.Load() != 1 {
		t.Fatalf("--yes bypassed remote read-only policy: err=%v reads=%d writes=%d\n%s", err, remoteReads.Load(), remoteWrites.Load(), output)
	}
	var denied map[string]any
	if err := json.Unmarshal([]byte(output), &denied); err != nil {
		t.Fatalf("denied replay did not emit one JSON error object: %v\n%s", err, output)
	}
}

func replayCLI(t *testing.T, binary, home, checkout string, arguments ...string) (contract.TrafficReplayWorkspace, string, error) {
	t.Helper()
	arguments = append([]string{"--json", "traffic", "replay"}, arguments...)
	output, runErr := runCLIAt(binary, home, checkout, arguments...)
	var workspace contract.TrafficReplayWorkspace
	if err := json.Unmarshal([]byte(output), &workspace); err != nil {
		t.Fatalf("replay did not emit exactly one JSON object: %v (command error: %v)\n%s", err, runErr, output)
	}
	return workspace, output, runErr
}

func replayCapturedExchange(t *testing.T, binary, home, checkout, edge, path string) model.TrafficExchange {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--json", "traffic", "list", "--edge", edge, "--limit", "100")
	if err != nil {
		t.Fatalf("list baseline traffic: %v\n%s", err, output)
	}
	var result struct{ Exchanges []model.TrafficExchange }
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode baseline traffic: %v\n%s", err, output)
	}
	for _, exchange := range result.Exchanges {
		if exchange.Path == path && exchange.Replay == nil {
			return replayExchangeDetail(t, binary, home, checkout, exchange.Sequence)
		}
	}
	t.Fatalf("baseline %s %s was not captured:\n%s", edge, path, output)
	return model.TrafficExchange{}
}

func replayExchangeDetail(t *testing.T, binary, home, checkout string, sequence int64) model.TrafficExchange {
	t.Helper()
	output, err := runCLIAt(binary, home, checkout, "--json", "traffic", "show", strconv.FormatInt(sequence, 10))
	if err != nil {
		t.Fatalf("inspect traffic #%d: %v\n%s", sequence, err, output)
	}
	var exchange model.TrafficExchange
	if err := json.Unmarshal([]byte(output), &exchange); err != nil {
		t.Fatalf("decode traffic #%d: %v\n%s", sequence, err, output)
	}
	return exchange
}

func assertReplayResult(t *testing.T, workspace contract.TrafficReplayWorkspace, baseline model.TrafficExchange, source, target string) {
	t.Helper()
	if workspace.Run == nil || workspace.Run.State != "completed" || workspace.Run.Outcome != "response-received" || workspace.Result == nil || workspace.Result.Exchange == nil || workspace.Baseline == nil {
		t.Fatalf("replay did not retain a completed receipt, baseline, and result: %#v", workspace)
	}
	exchange := workspace.Result.Exchange
	if exchange.Source != source || exchange.Target != target || exchange.TraceID == "" || exchange.TraceID == baseline.TraceID || exchange.Sequence == baseline.Sequence {
		t.Fatalf("replay lost its edge or reused original trace identity: %#v", exchange)
	}
	if exchange.Replay == nil || exchange.Replay.Project != "replay-e2e" || exchange.Replay.Environment != "local" || exchange.Replay.Sequence != baseline.Sequence || !exchange.Replay.StartedAt.Equal(baseline.StartedAt) || exchange.Replay.Workspace != workspace.Number || exchange.Replay.Run != workspace.Run.Number {
		t.Fatalf("replay provenance does not identify the frozen baseline: %#v", exchange.Replay)
	}
	if workspace.Baseline.Sequence != baseline.Sequence || !workspace.Baseline.StartedAt.Equal(baseline.StartedAt) || workspace.Project != "replay-e2e" || workspace.Environment != "local" || workspace.Result.Request.Environment != "local" {
		t.Fatalf("replay changed its original environment or baseline: %#v", workspace)
	}
}
