package replay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestStatusRetainsDestinationScopeAfterPayloadRelease(t *testing.T) {
	m := manager(t, nil, nil)
	w := prepare(t, m, original())
	draft := *w.Draft
	draft.Environment = "qa"
	var err error
	w, err = m.Update(t.Context(), w.Project, w.Environment, w.Number, contract.UpdateTrafficReplayDraftRequest{TrafficReplayIdentity: w.TrafficReplayIdentity, Revision: w.Revision, Draft: draft})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(t.Context(), w.Project, w.Environment, w.Number, runInput(w)); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, m, w)
	if err := m.Delete(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(w.Project, w.Environment, w.Number, w.TrafficReplayIdentity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(status)
	if !status.Closed || len(status.Destinations) != 1 || status.Destinations[0] != "qa" || len(status.Receipts) != 1 {
		t.Fatalf("status=%s", encoded)
	}
	for _, field := range []string{"coffee", "available", "requestBody", "responseBody", "headers"} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("status contains %s: %s", field, encoded)
		}
	}
}

func TestClearThroughOnlyDisposesReviewedBaselines(t *testing.T) {
	m := manager(t, nil, nil)
	baseline := original()
	baseline.Sequence = 10
	first := prepare(t, m, baseline)
	baseline.Sequence = 11
	second := prepare(t, m, baseline)
	m.ClearThrough(first.Project, first.Environment, 10)
	if _, err := m.Get(first.Project, first.Environment, first.Number, first.TrafficReplayIdentity, true); err == nil {
		t.Fatal("reviewed baseline survived clear")
	}
	if value, err := m.Get(second.Project, second.Environment, second.Number, second.TrafficReplayIdentity, true); err != nil || value.Baseline == nil || value.Baseline.Sequence != 11 {
		t.Fatalf("newer workspace removed: %#v %v", value, err)
	}
}
