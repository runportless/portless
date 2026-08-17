package command

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-relay"
)

func TestPrintStatusShowsHTTPAndPublishedContainerEndpoints(t *testing.T) {
	context, output, _ := newTestContext(t, t.TempDir())
	context.PrintStatus(model.Environment{
		Project: "billing", Name: "local", DashboardURL: "http://portless.localhost/environments/billing/local",
		Services: []model.Service{
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess}, Status: model.ServiceReady, Endpoints: []model.Endpoint{{Kind: model.EndpointPublic, Protocol: model.ProtocolHTTP, URL: "http://checkout.local.billing.localhost"}}, UpstreamPort: 49100},
			{ServiceDefinition: model.ServiceDefinition{Name: "postgres", Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432}, Status: model.ServiceReady, Endpoints: []model.Endpoint{{Kind: model.EndpointPublic, Protocol: model.ProtocolTCP, URL: "tcp://postgres.local.billing.portless.test:5432"}}, UpstreamPort: 49101},
		},
	})
	for _, expected := range []string{"http://checkout.local.billing.localhost", "tcp://postgres.local.billing.portless.test:5432", "http://portless.localhost/environments/billing/local"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("status does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestStatusUsesRestrainedPaletteWhenColorIsEnabled(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	context, output, _ := newTestContext(t, t.TempDir())
	context.ColorPreference = ColorAlways
	context.PrintStatus(model.Environment{
		Project: "billing", Name: "local", Status: model.EnvironmentHealthy,
		DashboardURL: "http://portless.localhost/environments/billing/local",
		Services: []model.Service{
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess}, Status: model.ServiceReady, Endpoints: []model.Endpoint{{Kind: model.EndpointPublic, Protocol: model.ProtocolHTTP, URL: "http://checkout.local.billing.localhost"}}},
		},
	})
	for _, expected := range []string{
		ansiBoldCyan + "billing/local" + ansiReset,
		ansiGreen + string(model.EnvironmentHealthy) + ansiReset,
		ansiDim + "SERVICE",
		ansiCyan + "http://checkout.local.billing.localhost" + ansiReset,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("colored status does not contain %q:\n%q", expected, output.String())
		}
	}
}

func TestWriteJSONLineEmitsOneCompactDocument(t *testing.T) {
	var output bytes.Buffer
	if err := WriteJSONLine(&output, map[string]any{"event": "ready", "details": map[string]any{"count": 2}}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 || strings.Contains(strings.TrimSuffix(output.String(), "\n"), "\n") {
		t.Fatalf("JSON Line is not one line: %q", output.String())
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("JSON Line is invalid: %v", err)
	}
}

func TestRelayStatusJSONIncludesComputedState(t *testing.T) {
	var output bytes.Buffer
	if err := WriteRelayStatusJSON(&output, relay.InstallationStatus{Installed: true, Running: true, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["state"] != "ready" || result["healthy"] != true {
		t.Fatalf("unexpected relay status: %#v", result)
	}
}
