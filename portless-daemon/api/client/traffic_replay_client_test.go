package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestTrafficReplayClientReadsLargePreparedAndSubmittedBodies(t *testing.T) {
	body := strings.Repeat("é", contract.TrafficReplayMaxBodyBytes/2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contract.TrafficReplayWorkspace{
			Draft:  &contract.TrafficReplayDraft{Body: body},
			Result: &contract.TrafficReplayResult{Request: contract.TrafficReplayDraft{Body: body}},
		})
	}))
	defer server.Close()
	client := New(server.URL, "control-token", server.Client())
	workspace, err := client.TrafficReplay(t.Context(), "billing", "local", 1, contract.TrafficReplayIdentity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Draft == nil || workspace.Result == nil || workspace.Draft.Body != body || workspace.Result.Request.Body != body {
		t.Fatal("large replay bodies were lost while decoding the workspace")
	}
}

func TestTrafficReplayClientUsesTypedRequestsAndIdentityQueries(t *testing.T) {
	identity := contract.TrafficReplayIdentity{CreatedAt: time.Date(2026, 9, 6, 12, 34, 56, 123456789, time.UTC), DaemonStartedAt: time.Date(2026, 9, 6, 12, 30, 0, 987654321, time.UTC)}
	baselineTime := identity.CreatedAt.Add(-time.Second)
	draft := contract.TrafficReplayDraft{Environment: "fix", Method: "POST", RequestTarget: "/orders/%2F?tag=1&tag=2", Headers: map[string][]string{"X-Repeated": {"one", "two"}, "Authorization": {"Bearer supplied"}}, BodyMode: "replacement", Body: `{"order":1}`}
	prepare := contract.PrepareTrafficReplayRequest{Sequence: 42, StartedAt: baselineTime}
	update := contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: identity, Revision: 1, Draft: draft}
	run := contract.RunTrafficReplayRequest{TrafficReplayIdentity: identity, Revision: 2, RunNumber: 1, ConfirmRemoteWrite: true}
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if r.Header.Get("Authorization") != "Bearer control-token" || r.Header.Get("Portless-Client-Kind") != "cli" {
			t.Errorf("control authentication=%v", r.Header)
		}
		var expected any
		wantMethod, wantPath := "", "/api/v1/environments/billing/local/traffic/replays"
		switch call {
		case 1:
			wantMethod = http.MethodPost
			expected = prepare
		case 2:
			wantMethod = http.MethodPut
			wantPath += "/3/draft"
			expected = update
		case 3:
			wantMethod = http.MethodPost
			wantPath += "/3/runs"
			expected = run
		case 4:
			wantMethod = http.MethodGet
			wantPath += "/3"
			query := r.URL.Query()
			if query.Get("expectedCreatedAt") != identity.CreatedAt.Format(time.RFC3339Nano) || query.Get("expectedDaemonStartedAt") != identity.DaemonStartedAt.Format(time.RFC3339Nano) || query.Get("include") != "result" {
				t.Errorf("identity query=%v", query)
			}
		case 5:
			wantMethod = http.MethodPost
			wantPath += "/3/activity"
			expected = identity
		case 6:
			wantMethod = http.MethodDelete
			wantPath += "/3"
			expected = identity
		default:
			t.Errorf("unexpected request %d", call)
		}
		if r.Method != wantMethod || r.URL.Path != wantPath {
			t.Errorf("request=%s %s want=%s %s", r.Method, r.URL.Path, wantMethod, wantPath)
		}
		if expected != nil {
			var actual, normalized any
			if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
				t.Error(err)
			}
			encoded, _ := json.Marshal(expected)
			_ = json.Unmarshal(encoded, &normalized)
			if !reflect.DeepEqual(actual, normalized) {
				t.Errorf("request body=%#v want=%#v", actual, normalized)
			}
		}
		if call >= 5 {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(contract.TrafficReplayWorkspace{TrafficReplayIdentity: identity, Project: "billing", Environment: "local", Number: 3, Revision: 2, NextRunNumber: 2, Run: &contract.TrafficReplayRun{Number: 1, State: "completed", Outcome: "response-received"}})
	}))
	defer server.Close()
	client := New(server.URL, "control-token", server.Client()).WithClientKind(contract.ClientKindCLI)
	if got, err := client.PrepareTrafficReplay(t.Context(), "billing", "local", prepare); err != nil || got.Number != 3 || !got.CreatedAt.Equal(identity.CreatedAt) {
		t.Fatalf("prepare=%#v err=%v", got, err)
	}
	if _, err := client.UpdateTrafficReplayDraft(t.Context(), "billing", "local", 3, update); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunTrafficReplay(t.Context(), "billing", "local", 3, run); err != nil {
		t.Fatal(err)
	}
	if got, err := client.TrafficReplay(t.Context(), "billing", "local", 3, identity, true); err != nil || got.Run == nil || got.Run.Outcome != "response-received" {
		t.Fatalf("read=%#v err=%v", got, err)
	}
	if err := client.TouchTrafficReplay(t.Context(), "billing", "local", 3, identity); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteTrafficReplay(t.Context(), "billing", "local", 3, identity); err != nil {
		t.Fatal(err)
	}
	if call != 6 {
		t.Fatalf("calls=%d want=6", call)
	}
}

func TestTrafficReplayClientPreservesStructuredPolicyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		_ = json.NewEncoder(w).Encode(contract.ErrorEnvelope{Error: contract.APIError{Code: "replay_remote_read_only", Message: "The destination remote policy blocks this HTTP method.", Subject: map[string]any{"project": "billing", "environment": "fix"}}})
	}))
	defer server.Close()
	client := New(server.URL, "control-token", server.Client())
	_, err := client.UpdateTrafficReplayDraft(t.Context(), "billing", "local", 3, contract.UpdateTrafficReplayDraftRequest{})
	failure, ok := err.(*ClientError)
	if !ok || failure.Status != 403 || failure.Code != "replay_remote_read_only" || failure.Subject["environment"] != "fix" {
		t.Fatalf("failure=%#v", err)
	}
}
