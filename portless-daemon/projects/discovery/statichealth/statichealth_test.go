package statichealth

import (
	"reflect"
	"testing"
)

func TestSelectPrefersReadinessAndFailsClosedOnEqualRank(t *testing.T) {
	selected := Select([]Candidate{
		{Path: "/health", File: "server.go", Explanation: "health", Rank: 110},
		{Path: "/ready", File: "server.go", Explanation: "ready", Rank: 120},
	})
	if selected.Candidate.Path != "/ready" || len(selected.Ambiguous) != 0 {
		t.Fatalf("selection = %#v", selected)
	}

	ambiguous := Select([]Candidate{
		{Path: "/health", File: "a.go", Explanation: "first", Rank: 110},
		{Path: "/api/health", File: "b.go", Explanation: "second", Rank: 110},
		{Path: "/health", File: "c.go", Explanation: "duplicate", Rank: 110},
	})
	paths := []string{ambiguous.Ambiguous[0].Path, ambiguous.Ambiguous[1].Path}
	if ambiguous.Candidate.Path != "" || !reflect.DeepEqual(paths, []string{"/api/health", "/health"}) {
		t.Fatalf("ambiguous selection = %#v", ambiguous)
	}
}

func TestLiteralPathClassificationRejectsDynamicAndLivenessRoutes(t *testing.T) {
	for _, value := range []string{"/health/:id", "/health?full=true", "/health#details", "/../health", "/livez", "/status"} {
		if rank := SemanticRank(value); rank != 0 {
			t.Errorf("SemanticRank(%q) = %d", value, rank)
		}
	}
	if path := JoinPath("/api", "health"); path != "/api/health" {
		t.Fatalf("joined path = %q", path)
	}
	if SemanticRank("/api/readiness") <= SemanticRank("/api/health") {
		t.Fatal("readiness route did not outrank health route")
	}
}
