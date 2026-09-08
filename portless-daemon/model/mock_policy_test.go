package model

import (
	"encoding/json"
	"testing"
)

func TestMockPolicyRejectsExplicitInvalidValues(t *testing.T) {
	for _, raw := range []string{`""`, `null`, `"fallback"`, `false`, `1`} {
		var policy MockUnmatchedRequests
		if err := json.Unmarshal([]byte(raw), &policy); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`"reject"`, `"forward"`} {
		var policy MockUnmatchedRequests
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}
