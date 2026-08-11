package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDNSName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		" Billing API ": "billing-api",
		"123 orders":    "project-123-orders",
		"Hello___World": "hello-world",
		"déjà service":  "d-j-service",
	}
	for input, expected := range cases {
		if actual := NormalizeDNSName(input); actual != expected {
			t.Errorf("NormalizeDNSName(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestPublicModelContainsNamesAndNoGenericIDs(t *testing.T) {
	t.Parallel()
	project := Project{Name: "billing", Services: []Service{{ServiceDefinition: ServiceDefinition{Name: "orders"}}}}
	encoded, err := json.Marshal(project)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"name":"billing"`) || !strings.Contains(text, `"name":"orders"`) {
		t.Fatalf("public names missing from %s", text)
	}
	for _, forbidden := range []string{`"id"`, `"trusted"`, "privateKey", "projectKey", "runKey"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("private or generic identifier %q leaked in %s", forbidden, text)
		}
	}
}

func TestDeriveProjectStatus(t *testing.T) {
	t.Parallel()
	services := []Service{
		{ServiceDefinition: ServiceDefinition{Name: "gateway", Required: true}, Status: ServiceReady},
		{ServiceDefinition: ServiceDefinition{Name: "orders", Required: true}, Status: ServiceUnhealthy},
	}
	status, reason := DeriveProjectStatus(services, "")
	if status != ProjectDegraded || !strings.Contains(reason, "orders") {
		t.Fatalf("got %s %q", status, reason)
	}
}
