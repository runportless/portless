package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestMockPreviewEnvelopeUsesDraftWithoutSaving(t *testing.T) {
	server, authManager := newMockPreviewServer(t)
	base := "/api/v1/environments/billing/local/mocks/checkout-empty"
	before := request(server, authManager, http.MethodGet, base, "", true)
	if before.Code != http.StatusOK {
		t.Fatalf("saved scenario code=%d body=%s", before.Code, before.Body.String())
	}
	for _, test := range []struct {
		name   string
		body   string
		route  string
		status int
		result string
	}{
		{
			name: "saved route", body: `{"request":{"service":"checkout","method":"GET","path":"/health"}}`,
			route: "health", status: 200, result: "saved response",
		},
		{
			name: "existing draft", body: `{"request":{"service":"checkout","method":"GET","path":"/health"},"originalRoute":"health","draft":{"name":"health","service":"checkout","method":"GET","path":"/health","status":503,"body":"draft response","enabled":true}}`,
			route: "health", status: 503, result: "draft response",
		},
		{
			name: "new draft", body: `{"request":{"service":"checkout","method":"POST","path":"/orders"},"draft":{"name":"create-order","service":"checkout","method":"POST","path":"/orders","status":202,"body":"new response","enabled":true}}`,
			route: "create-order", status: 202, result: "new response",
		},
		{
			name: "regex draft", body: `{"request":{"service":"checkout","method":"GET","path":"/health","query":{"sku":["tea","coffee-mug"]}},"originalRoute":"health","draft":{"name":"health","service":"checkout","method":"GET","path":"/health","query":{"sku":{"match":"regex","value":"coffee-.*"}},"status":202,"body":"regex response","enabled":true}}`,
			route: "health", status: 202, result: "regex response",
		},
		{
			name: "renamed draft", body: `{"request":{"service":"checkout","method":"GET","path":"/health"},"originalRoute":"health","draft":{"name":"renamed","service":"checkout","method":"GET","path":"/health","status":202,"body":"renamed response","enabled":true}}`,
			route: "renamed", status: 202, result: "renamed response",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(server, authManager, http.MethodPost, base+"/preview", test.body, true)
			if response.Code != http.StatusOK {
				t.Fatalf("preview code=%d body=%s", response.Code, response.Body.String())
			}
			var preview contract.MockPreview
			if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			if !preview.Matched || preview.Service != "checkout" || preview.Route != test.route || preview.Status != test.status || preview.Body != test.result {
				t.Fatalf("preview = %#v", preview)
			}
			after := request(server, authManager, http.MethodGet, base, "", true)
			if after.Code != http.StatusOK || after.Body.String() != before.Body.String() {
				t.Fatalf("preview changed saved scenario: before=%s after=%s", before.Body.String(), after.Body.String())
			}
		})
	}
}

func TestMockPreviewRejectsInvalidEnvelopeAndOriginalIdentity(t *testing.T) {
	server, authManager := newMockPreviewServer(t)
	for _, test := range []struct {
		name   string
		body   string
		code   string
		status int
	}{
		{name: "bare request", body: `{"service":"checkout","method":"GET","path":"/health"}`, code: "INVALID_JSON"},
		{name: "unknown draft field", body: `{"request":{"service":"checkout","method":"GET","path":"/health"},"draft":{"name":"health","unknown":true}}`, code: "INVALID_JSON"},
		{name: "missing request", body: `{}`, code: "REQUEST_FAILED"},
		{name: "null request", body: `{"request":null}`, code: "REQUEST_FAILED"},
		{name: "original without draft", body: `{"request":{"service":"checkout","method":"GET","path":"/health"},"originalRoute":"health"}`, code: "REQUEST_FAILED"},
		{name: "missing original", body: `{"request":{"service":"checkout","method":"GET","path":"/health"},"originalRoute":"deleted","draft":{"name":"deleted","service":"checkout","method":"GET","path":"/health","status":200,"enabled":true}}`, code: "RESOURCE_NOT_FOUND", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := request(server, authManager, http.MethodPost, "/api/v1/environments/billing/local/mocks/checkout-empty/preview", test.body, true)
			status := test.status
			if status == 0 {
				status = http.StatusBadRequest
			}
			if response.Code != status {
				t.Fatalf("invalid preview code=%d body=%s", response.Code, response.Body.String())
			}
			var failure contract.ErrorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error.Code != test.code || failure.Error.Message == "" {
				t.Fatalf("structured failure = %#v", failure.Error)
			}
			if test.code != "INVALID_JSON" && (failure.Error.Subject["project"] != "billing" || failure.Error.Subject["environment"] != "local" || failure.Error.Subject["scenario"] != "checkout-empty") {
				t.Fatalf("preview failure lost scenario identity: %#v", failure.Error)
			}
		})
	}
}

func TestPutMockRouteRenamesAndRejectsNameCollisions(t *testing.T) {
	server, authManager := newMockPreviewServer(t)
	base := "/api/v1/environments/billing/local/mocks/checkout-empty"
	before := request(server, authManager, http.MethodGet, base, "", true)
	for _, test := range []struct {
		target, body string
		status       int
	}{
		{"health", `{"service":"checkout","method":"GET","path":"/health","status":200,"enabled":true}`, http.StatusBadRequest},
		{"missing", `{"name":"renamed","service":"checkout","method":"GET","path":"/health","status":200,"enabled":true}`, http.StatusNotFound},
	} {
		response := request(server, authManager, http.MethodPut, base+"/routes/"+test.target, test.body, true)
		if response.Code != test.status {
			t.Fatalf("invalid rename = %d %s", response.Code, response.Body.String())
		}
	}
	if after := request(server, authManager, http.MethodGet, base, "", true); after.Body.String() != before.Body.String() {
		t.Fatal("failed rename changed saved routes")
	}
	renamed := request(server, authManager, http.MethodPut, base+"/routes/health", `{"name":"renamed","service":"checkout","method":"GET","path":"/health","status":202,"body":"renamed response","enabled":true}`, true)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", renamed.Code, renamed.Body.String())
	}
	var scenario contract.MockScenario
	if err := json.Unmarshal(renamed.Body.Bytes(), &scenario); err != nil {
		t.Fatal(err)
	}
	if len(scenario.Routes) != 1 || scenario.Routes[0].Name != "renamed" || scenario.Routes[0].Body != "renamed response" {
		t.Fatalf("renamed scenario = %#v", scenario)
	}
	created := request(server, authManager, http.MethodPut, base+"/routes/occupied", `{"name":"occupied","service":"checkout","method":"GET","path":"/other","status":200,"enabled":true}`, true)
	if created.Code != http.StatusOK {
		t.Fatalf("create peer = %d %s", created.Code, created.Body.String())
	}
	before = request(server, authManager, http.MethodGet, base, "", true)
	collision := request(server, authManager, http.MethodPut, base+"/routes/renamed", `{"name":"occupied","service":"checkout","method":"GET","path":"/health","status":200,"enabled":true}`, true)
	if collision.Code != http.StatusConflict {
		t.Fatalf("collision = %d %s", collision.Code, collision.Body.String())
	}
	if after := request(server, authManager, http.MethodGet, base, "", true); after.Body.String() != before.Body.String() {
		t.Fatal("collision changed saved routes")
	}
}

func newMockPreviewServer(t *testing.T) (*Server, *auth.Manager) {
	t.Helper()
	data := t.TempDir()
	store, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := store.CreateProject(t.Context(), "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEnvironment(t.Context(), "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	app := controlplane.New(store, events.NewBroker(), controlplane.Config{DataDirectory: data, InstallationKey: "test-installation"})
	t.Cleanup(func() { app.Close(context.Background()) })
	server, err := New(Dependencies{Application: app, Auth: authManager, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>portless</html>")}}})
	if err != nil {
		t.Fatal(err)
	}
	created := request(server, authManager, http.MethodPost, "/api/v1/environments/billing/local/mocks", `{"name":"checkout-empty"}`, true)
	if created.Code != http.StatusCreated {
		t.Fatalf("create preview scenario code=%d body=%s", created.Code, created.Body.String())
	}
	saved := request(server, authManager, http.MethodPut, "/api/v1/environments/billing/local/mocks/checkout-empty/routes/health", `{"name":"health","service":"checkout","method":"GET","path":"/health","status":200,"body":"saved response","enabled":true}`, true)
	if saved.Code != http.StatusOK {
		t.Fatalf("create preview route code=%d body=%s", saved.Code, saved.Body.String())
	}
	return server, authManager
}
