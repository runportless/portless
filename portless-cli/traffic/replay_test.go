package traffic

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestReplayDraftEditsPreserveEscapingRepeatedHeadersAndOriginal(t *testing.T) {
	original := contract.TrafficReplayDraft{Environment: "local", Method: "POST", RequestTarget: "/original?tag=a&tag=b", BodyMode: "captured", Body: "original", Headers: map[string][]string{"Authorization": {"[REDACTED]"}, "X-Retain": {"one", "two"}}}
	draft, err := editReplayDraft(original, "qa", replayOptions{method: "patch", path: "/orders/a%2Fb?tag=x%20y&tag=z", bodyFile: "-", headers: []string{"X-Repeat: first", "X-Repeat: second"}, removedHeaders: []string{"authorization"}}, strings.NewReader(`{"id":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if draft.Environment != "qa" || draft.Method != "PATCH" || draft.RequestTarget != "/orders/a%2Fb?tag=x%20y&tag=z" || draft.BodyMode != "replacement" || draft.Body != `{"id":9007199254740993}` {
		t.Fatalf("edited request was altered: %#v", draft)
	}
	if !reflect.DeepEqual(draft.Headers["X-Repeat"], []string{"first", "second"}) || !reflect.DeepEqual(draft.Headers["X-Retain"], []string{"one", "two"}) {
		t.Fatalf("repeated headers changed: %#v", draft.Headers)
	}
	if _, retained := draft.Headers["Authorization"]; retained || !reflect.DeepEqual(draft.OmittedHeaders, []string{"Authorization"}) {
		t.Fatalf("explicit credential omission was lost: %#v", draft)
	}
	draft.Headers["X-Retain"][0] = "edited"
	if original.Headers["X-Retain"][0] != "one" || original.Headers["Authorization"][0] != "[REDACTED]" || original.Body != "original" {
		t.Fatal("editing mutated the frozen original")
	}
}

func TestReplayInputRejectsAmbiguousAndOversizedOverrides(t *testing.T) {
	for _, test := range []struct {
		name    string
		options replayOptions
		input   string
	}{
		{"case duplicate", replayOptions{headersFile: "-"}, `{"x-key":["one"],"X-Key":["two"]}`},
		{"invalid headers JSON", replayOptions{headersFile: "-"}, `{"X-Key":"value"}`},
		{"trailing headers JSON", replayOptions{headersFile: "-"}, `{"X-Key":["value"]}{}`},
		{"header replacement and omission", replayOptions{headers: []string{"X-Key: value"}, removedHeaders: []string{"x-key"}}, ""},
		{"invalid header argument", replayOptions{headers: []string{"bad header"}}, ""},
		{"oversized body", replayOptions{bodyFile: "-"}, strings.Repeat("x", contract.TrafficReplayMaxBodyBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := editReplayDraft(contract.TrafficReplayDraft{}, "local", test.options, strings.NewReader(test.input))
			var usage *command.UsageFailure
			if !errors.As(err, &usage) {
				t.Fatalf("invalid input must be a usage failure, got %v", err)
			}
		})
	}
	draft, err := editReplayDraft(contract.TrafficReplayDraft{Body: "prefix", BodyMode: "replacement"}, "local", replayOptions{emptyBody: true}, strings.NewReader(""))
	if err != nil || draft.BodyMode != "empty" || draft.Body != "" {
		t.Fatalf("explicit empty body was not preserved: %#v, %v", draft, err)
	}
}

func TestReplayBodyFileAcceptsFullSizeUTF8Text(t *testing.T) {
	body := strings.Repeat("é", contract.TrafficReplayMaxBodyBytes/2)
	draft, err := editReplayDraft(contract.TrafficReplayDraft{}, "local", replayOptions{bodyFile: "-"}, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if draft.BodyMode != "replacement" || draft.Body != body {
		t.Fatal("full-size input body was changed")
	}
}

func TestReplayReceiptIdentityAndSingleJSONFailure(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 123, time.UTC)
	identity, err := replayExpectedIdentity(now.Format(time.RFC3339Nano), now.Add(-time.Minute).Format(time.RFC3339Nano))
	if err != nil || !identity.CreatedAt.Equal(now) {
		t.Fatalf("identity parsing failed: %#v, %v", identity, err)
	}
	if _, err := replayExpectedIdentity(now.Format(time.RFC3339Nano), ""); err == nil {
		t.Fatal("partial expected identity accepted")
	}
	application, output, _ := newTestCommands(t)
	application.JSONOutput = true
	workspace := contract.TrafficReplayWorkspace{TrafficReplayIdentity: identity, Project: "billing", Environment: "local", Number: 7, Run: &contract.TrafficReplayRun{Number: 2, State: "failed", Outcome: "unknown", Error: "Request deadline reached."}}
	var reported *command.ReportedError
	if err := application.printReplay(workspace); !errors.As(err, &reported) {
		t.Fatalf("failed JSON result must suppress a second error object, got %v", err)
	}
	var decoded contract.TrafficReplayWorkspace
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded.Run.Outcome != "unknown" {
		t.Fatalf("output is not one complete receipt: %s, %v", output.String(), err)
	}
	message := replayWaitError(workspace, errors.New("connection lost")).Error()
	for _, expected := range []string{"may have reached", "--env billing/local traffic replay show 7", "--expected-created-at " + now.Format(time.RFC3339Nano), "--expected-daemon-started-at "} {
		if !strings.Contains(message, expected) {
			t.Fatalf("recovery guidance omits %q: %s", expected, message)
		}
	}
}
