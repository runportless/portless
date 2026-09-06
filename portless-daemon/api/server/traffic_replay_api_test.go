package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

const replayAPIBase = "/api/v1/environments/billing/local/traffic/replays"

func replayAPIFixture(t *testing.T) (*Server, model.TrafficExchange, *atomic.Int64) {
	t.Helper()
	calls := new(atomic.Int64)
	server, _ := newApplicationHostServerWithUpstream(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(204)
			return
		}
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			if r.Header.Get("Authorization") != "Bearer api-replay-secret" {
				t.Errorf("application auth=%q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"version":2}`)
			return
		}
		_, _ = io.WriteString(w, `{"version":1}`)
	}))
	response := requestHost(server, server.auth, http.MethodGet, "/orders?version=1", "", false, "checkout.local.billing.localhost")
	if response.Code != 200 {
		t.Fatalf("baseline response=%d %s", response.Code, response.Body.String())
	}
	list := server.app.TrafficExchanges("billing", "local", 1)
	if len(list) != 1 {
		t.Fatalf("baseline list=%#v", list)
	}
	baseline, err := server.app.TrafficExchange(t.Context(), "billing", "local", list[0].Sequence)
	if err != nil {
		t.Fatal(err)
	}
	return server, baseline, calls
}

func replayJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func replayAPIWorkspace(t *testing.T, response *httptest.ResponseRecorder, want int) contract.TrafficReplayWorkspace {
	t.Helper()
	if response.Code != want {
		t.Fatalf("response=%d want=%d body=%s", response.Code, want, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("replay response must be no-store: %v", response.Header())
	}
	var workspace contract.TrafficReplayWorkspace
	if err := json.Unmarshal(response.Body.Bytes(), &workspace); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func replayAPIGetPath(number int64, identity contract.TrafficReplayIdentity, result bool) string {
	query := url.Values{"expectedCreatedAt": {identity.CreatedAt.Format(time.RFC3339Nano)}, "expectedDaemonStartedAt": {identity.DaemonStartedAt.Format(time.RFC3339Nano)}}
	if result {
		query.Set("include", "result")
	}
	return replayAPIBase + "/" + strconv.FormatInt(number, 10) + "?" + query.Encode()
}

func TestTrafficReplayAPIExecutesOnceAndReconcilesThroughGET(t *testing.T) {
	server, baseline, calls := replayAPIFixture(t)
	prepared := request(server, server.auth, http.MethodPost, replayAPIBase, replayJSON(t, contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt}), true)
	workspace := replayAPIWorkspace(t, prepared, 201)
	if calls.Load() != 1 || workspace.Baseline == nil {
		t.Fatalf("preparation calls=%d workspace=%#v", calls.Load(), workspace)
	}
	draft := *workspace.Draft
	draft.Method = "POST"
	draft.BodyMode = "replacement"
	draft.Body = `{"edited":true}`
	draft.Headers = map[string][]string{"Content-Type": {"application/json"}, "Authorization": {"Bearer api-replay-secret"}, "X-Repeated": {"one", "two"}}
	updated := request(server, server.auth, http.MethodPut, replayAPIBase+"/"+strconv.FormatInt(workspace.Number, 10)+"/draft", replayJSON(t, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: draft}), true)
	workspace = replayAPIWorkspace(t, updated, 200)
	if calls.Load() != 1 || workspace.Destination == nil || !workspace.Destination.RequiresConfirmation || strings.Contains(updated.Body.String(), "api-replay-secret") {
		t.Fatalf("prepared unsafe or sent: calls=%d body=%s", calls.Load(), updated.Body.String())
	}
	runPath := replayAPIBase + "/" + strconv.FormatInt(workspace.Number, 10) + "/runs"
	input := contract.RunTrafficReplayRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, RunNumber: 1}
	denied := request(server, server.auth, http.MethodPost, runPath, replayJSON(t, input), true)
	if denied.Code != 403 || calls.Load() != 1 {
		t.Fatalf("unconfirmed write=%d calls=%d body=%s", denied.Code, calls.Load(), denied.Body.String())
	}
	input.ConfirmRemoteWrite = true
	admitted := replayAPIWorkspace(t, request(server, server.auth, http.MethodPost, runPath, replayJSON(t, input), true), 202)
	if admitted.Run == nil || admitted.Run.Number != 1 {
		t.Fatalf("admission=%#v", admitted)
	}
	duplicate := replayAPIWorkspace(t, request(server, server.auth, http.MethodPost, runPath, replayJSON(t, input), true), 202)
	if duplicate.Run == nil || duplicate.Run.Number != 1 {
		t.Fatalf("duplicate=%#v", duplicate)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		workspace = replayAPIWorkspace(t, request(server, server.auth, http.MethodGet, replayAPIGetPath(workspace.Number, workspace.TrafficReplayIdentity, false), "", true), 200)
		if workspace.Baseline != nil || workspace.Draft != nil || workspace.Result != nil {
			t.Fatalf("metadata poll contained request payloads: %#v", workspace)
		}
		if workspace.Run != nil && workspace.Run.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	full := request(server, server.auth, http.MethodGet, replayAPIGetPath(workspace.Number, workspace.TrafficReplayIdentity, true), "", true)
	workspace = replayAPIWorkspace(t, full, 200)
	if calls.Load() != 2 || workspace.Run.State != "completed" || workspace.Result == nil || workspace.Result.Exchange == nil || workspace.Result.Exchange.Status != 201 || workspace.Baseline.Status != 200 || workspace.Result.Comparison.Body.State != "different" || strings.Contains(full.Body.String(), "api-replay-secret") {
		t.Fatalf("calls=%d body=%s", calls.Load(), full.Body.String())
	}
	stale := workspace.TrafficReplayIdentity
	stale.CreatedAt = stale.CreatedAt.Add(time.Nanosecond)
	if response := request(server, server.auth, http.MethodGet, replayAPIGetPath(workspace.Number, stale, true), "", true); response.Code != 410 {
		t.Fatalf("stale GET=%d %s", response.Code, response.Body.String())
	}
	if response := request(server, server.auth, http.MethodDelete, replayAPIBase+"/"+strconv.FormatInt(workspace.Number, 10), replayJSON(t, workspace.TrafficReplayIdentity), true); response.Code != 204 {
		t.Fatalf("delete=%d %s", response.Code, response.Body.String())
	}
	_ = replayAPIWorkspace(t, request(server, server.auth, http.MethodPost, runPath, replayJSON(t, input), true), 202)
	if calls.Load() != 2 {
		t.Fatalf("deleted receipt was executed again: %d calls", calls.Load())
	}
}

func TestTrafficReplayAPIEnforcesAuthenticationCSRFAndMCPBoundary(t *testing.T) {
	server, baseline, calls := replayAPIFixture(t)
	body := replayJSON(t, contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
	if response := request(server, server.auth, http.MethodPost, replayAPIBase, body, false); response.Code != 401 {
		t.Fatalf("unauthenticated=%d %s", response.Code, response.Body.String())
	}
	for _, route := range []struct{ method, path, body string }{{http.MethodPost, replayAPIBase, body}, {http.MethodGet, replayAPIBase + "/1", ""}, {http.MethodPost, replayAPIBase + "/1/activity", "{}"}, {http.MethodPut, replayAPIBase + "/1/draft", "{}"}, {http.MethodPost, replayAPIBase + "/1/runs", "{}"}, {http.MethodDelete, replayAPIBase + "/1", "{}"}} {
		response := requestClientKind(server, server.auth, route.method, route.path, route.body, string(contract.ClientKindMCP))
		if response.Code != 403 || !strings.Contains(response.Body.String(), "REPLAY_CAPABILITY_REQUIRED") {
			t.Fatalf("MCP %s %s=%d %s", route.method, route.path, response.Code, response.Body.String())
		}
	}
	claim, _, err := server.auth.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, _, _, err := server.auth.ConsumeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	if response := requestBrowser(server, http.MethodPost, replayAPIBase, body, token, ""); response.Code != 403 {
		t.Fatalf("missing CSRF=%d %s", response.Code, response.Body.String())
	}
	_ = replayAPIWorkspace(t, requestBrowser(server, http.MethodPost, replayAPIBase, body, token, csrf), 201)
	if calls.Load() != 1 {
		t.Fatalf("security/preparation sent %d application calls", calls.Load())
	}
}

func TestTrafficReplayAPIRejectsInvalidInputsAndBoundsWorkspaces(t *testing.T) {
	server, baseline, calls := replayAPIFixture(t)
	body := replayJSON(t, contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt})
	stale := request(server, server.auth, http.MethodPost, replayAPIBase, replayJSON(t, contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt.Add(time.Second)}), true)
	if stale.Code != 409 {
		t.Fatalf("stale baseline=%d %s", stale.Code, stale.Body.String())
	}
	for _, input := range []string{`{"secret":"do-not-echo-this-secret"}`, body + body} {
		response := request(server, server.auth, http.MethodPost, replayAPIBase, input, true)
		if response.Code != 400 || strings.Contains(response.Body.String(), "do-not-echo-this-secret") {
			t.Fatalf("invalid JSON=%d %s", response.Code, response.Body.String())
		}
	}
	oversized := request(server, server.auth, http.MethodPost, replayAPIBase, strings.Repeat(" ", 1<<20)+"{}", true)
	if oversized.Code != 413 {
		t.Fatalf("oversized input=%d %s", oversized.Code, oversized.Body.String())
	}
	for count := 0; count < 8; count++ {
		_ = replayAPIWorkspace(t, request(server, server.auth, http.MethodPost, replayAPIBase, body, true), 201)
	}
	full := request(server, server.auth, http.MethodPost, replayAPIBase, body, true)
	if full.Code != 429 || calls.Load() != 1 {
		t.Fatalf("capacity=%d calls=%d body=%s", full.Code, calls.Load(), full.Body.String())
	}
	server.app.ClearTraffic("billing", "local")
	missing := request(server, server.auth, http.MethodPost, replayAPIBase, body, true)
	if missing.Code != 404 {
		t.Fatalf("cleared baseline=%d %s", missing.Code, missing.Body.String())
	}
}

func TestTrafficReplayAPIDraftBodySizeBoundary(t *testing.T) {
	server, baseline, calls := replayAPIFixture(t)
	workspace := replayAPIWorkspace(t, request(server, server.auth, http.MethodPost, replayAPIBase,
		replayJSON(t, contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt}), true), 201)
	path := replayAPIBase + "/" + strconv.FormatInt(workspace.Number, 10) + "/draft"
	draft := *workspace.Draft
	draft.BodyMode = "replacement"
	draft.Body = strings.Repeat("é", contract.TrafficReplayMaxBodyBytes/2)
	input := contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: workspace.TrafficReplayIdentity, Revision: workspace.Revision, Draft: draft}
	response := request(server, server.auth, http.MethodPut, path, replayJSON(t, input), true)
	if response.Code != 200 {
		t.Fatalf("full-size draft status=%d body=%s", response.Code, response.Body.String())
	}
	workspace = replayAPIWorkspace(t, response, 200)
	if workspace.Draft == nil || workspace.Draft.Body != draft.Body {
		t.Fatal("full-size UTF-8 draft was not preserved")
	}
	input.Revision = workspace.Revision
	input.Draft.Body += "x"
	response = request(server, server.auth, http.MethodPut, path, replayJSON(t, input), true)
	var failure contract.ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if response.Code != 413 || failure.Error.Code != "replay_input_limit" || failure.Error.Message != "The request body is too large. Reduce its size and try again." {
		t.Fatalf("oversized draft status=%d failure=%+v", response.Code, failure.Error)
	}
	// JSON escaping is envelope overhead, not part of the application's body.
	input.Draft.Body = strings.Repeat("\x00", 1<<20)
	response = request(server, server.auth, http.MethodPut, path, replayJSON(t, input), true)
	if response.Code != 200 {
		t.Fatalf("escaped draft status=%d body=%s", response.Code, response.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("draft preparation sent application traffic: %d calls", calls.Load())
	}
}

func TestTrafficReplayActivityRequiresIdentityAndNeverSendsTraffic(t *testing.T) {
	server, baseline, calls := replayAPIFixture(t)
	prepared := request(server, server.auth, http.MethodPost, replayAPIBase, replayJSON(t, contract.PrepareTrafficReplayRequest{Sequence: baseline.Sequence, StartedAt: baseline.StartedAt}), true)
	workspace := replayAPIWorkspace(t, prepared, 201)
	if strings.Contains(prepared.Body.String(), "expiresAt") {
		t.Fatal("replay exposes a fixed expiry")
	}
	path := replayAPIBase + "/" + strconv.FormatInt(workspace.Number, 10) + "/activity"
	for _, test := range []struct {
		identity contract.TrafficReplayIdentity
		status   int
	}{
		{contract.TrafficReplayIdentity{}, 400},
		{contract.TrafficReplayIdentity{CreatedAt: workspace.CreatedAt.Add(time.Second), DaemonStartedAt: workspace.DaemonStartedAt}, 410},
		{workspace.TrafficReplayIdentity, 204},
	} {
		response := request(server, server.auth, http.MethodPost, path, replayJSON(t, test.identity), true)
		if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("activity=%d want=%d body=%s", response.Code, test.status, response.Body.String())
		}
	}
	if response := request(server, server.auth, http.MethodGet, path, "", true); response.Code != 405 {
		t.Fatalf("GET activity=%d", response.Code)
	}
	claim, _, err := server.auth.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, _, err := server.auth.ConsumeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	if response := requestBrowser(server, http.MethodPost, path, replayJSON(t, workspace.TrafficReplayIdentity), token, ""); response.Code != 403 {
		t.Fatalf("activity without CSRF=%d", response.Code)
	}
	if calls.Load() != 1 {
		t.Fatalf("activity sent application traffic: %d", calls.Load())
	}
}
