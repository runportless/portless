package api

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/store"
)

func TestProjectAndEnvironmentAPIsAndHostsAreSeparated(t *testing.T) {
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(context.Background(), "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(context.Background(), "billing", "local", definition, nil, []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}}); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(controlStore, events.NewBroker(), application.Config{DataDirectory: data, InstallationKey: "test-installation"})
	defer app.Close(context.Background())
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>portless</html>"), Mode: fs.FileMode(0o600)}}
	server, err := New(app, authManager, assets)
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := request(server, authManager, http.MethodGet, "/api/v1/projects", "", false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control request returned %d", unauthenticated.Code)
	}
	projects := request(server, authManager, http.MethodGet, "/api/v1/projects", "", true)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body.String(), `"name":"billing"`) || !strings.Contains(projects.Body.String(), `"environments":[`) {
		t.Fatalf("projects response code=%d body=%s", projects.Code, projects.Body.String())
	}
	environment := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local", "", true)
	if environment.Code != http.StatusOK || !strings.Contains(environment.Body.String(), `"project":"billing"`) || !strings.Contains(environment.Body.String(), `"name":"local"`) ||
		!strings.Contains(environment.Body.String(), `"dashboardUrl":"http://portless.localhost/environments/billing/local"`) ||
		!strings.Contains(environment.Body.String(), `"ingressUrl":"http://checkout.local.billing.localhost"`) || strings.Contains(environment.Body.String(), ".localhost:7331") {
		t.Fatalf("environment API did not use clean scoped URLs: %s", environment.Body.String())
	}

	binding := request(server, authManager, http.MethodPut, "/api/v1/environments/billing/local/bindings/checkout", `{"provider":"remote","remote":{"url":"https://checkout.qa.example.test","classification":"qa","writePolicy":"read-only","healthPath":"/health"}}`, true)
	if binding.Code != http.StatusOK || !strings.Contains(binding.Body.String(), `"provider":"remote"`) || !strings.Contains(binding.Body.String(), `"writePolicy":"read-only"`) {
		t.Fatalf("remote binding response code=%d body=%s", binding.Code, binding.Body.String())
	}

	browserClaim := requestHost(server, authManager, http.MethodPost, "/api/v1/browser-claims", `{"next":"/environments/billing/local"}`, true, "127.0.0.1:7331")
	if browserClaim.Code != http.StatusCreated || !strings.Contains(browserClaim.Body.String(), `"url":"http://portless.localhost/auth/claim/`) || strings.Contains(browserClaim.Body.String(), ":7331") {
		t.Fatalf("browser claim did not use clean control origin: %d %s", browserClaim.Code, browserClaim.Body.String())
	}

	applicationAPI := requestHost(server, authManager, http.MethodGet, "/api/v1/projects", "", false, "checkout.local.billing.localhost")
	if applicationAPI.Code != http.StatusMisdirectedRequest {
		t.Fatalf("application host reached control API: %d", applicationAPI.Code)
	}
	unknown := requestHost(server, authManager, http.MethodGet, "/api/v1/health", "", false, "malicious.example")
	if unknown.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host returned %d", unknown.Code)
	}
}

func TestApplicationHostRequiresServiceEnvironmentProject(t *testing.T) {
	if service, environment, project, ok := applicationHost("checkout.local.billing.localhost"); !ok || service != "checkout" || environment != "local" || project != "billing" {
		t.Fatalf("canonical host parsed as %q %q %q %v", service, environment, project, ok)
	}
	if _, _, _, ok := applicationHost("checkout.billing.localhost"); ok {
		t.Fatal("two-label application host should not be accepted")
	}
}

func TestEnvironmentContextSelectionCanBeInspectedAndCleared(t *testing.T) {
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := controlStore.CreateProject(context.Background(), "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if _, err := controlStore.CreateEnvironment(context.Background(), "billing", "local", definition,
		[]model.SourceBinding{{Name: "checkout", Path: source, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"}},
	); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	app := application.New(controlStore, events.NewBroker(), application.Config{DataDirectory: data, InstallationKey: "test-installation"})
	defer app.Close(context.Background())
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>portless</html>"), Mode: fs.FileMode(0o600)}}
	server, err := New(app, authManager, assets)
	if err != nil {
		t.Fatal(err)
	}

	contextPath := "/api/v1/environments/context?path=" + url.QueryEscape(source)
	resolved := request(server, authManager, http.MethodGet, contextPath, "", true)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"resolution":"inferred"`) || !strings.Contains(resolved.Body.String(), `"name":"local"`) {
		t.Fatalf("inferred context response code=%d body=%s", resolved.Code, resolved.Body.String())
	}
	selected := request(server, authManager, http.MethodPut, "/api/v1/environments/select", `{"path":"`+source+`","project":"billing","environment":"local"}`, true)
	if selected.Code != http.StatusNoContent {
		t.Fatalf("select response code=%d body=%s", selected.Code, selected.Body.String())
	}
	resolved = request(server, authManager, http.MethodGet, contextPath, "", true)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"resolution":"selected"`) {
		t.Fatalf("selected context response code=%d body=%s", resolved.Code, resolved.Body.String())
	}
	cleared := request(server, authManager, http.MethodDelete, "/api/v1/environments/select?path="+url.QueryEscape(source), "", true)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"cleared":true`) {
		t.Fatalf("clear response code=%d body=%s", cleared.Code, cleared.Body.String())
	}
	cleared = request(server, authManager, http.MethodDelete, "/api/v1/environments/select?path="+url.QueryEscape(source), "", true)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"cleared":false`) {
		t.Fatalf("idempotent clear response code=%d body=%s", cleared.Code, cleared.Body.String())
	}
}

func request(server *Server, authManager *auth.Manager, method, path, body string, authenticated bool) *httptest.ResponseRecorder {
	return requestHost(server, authManager, method, path, body, authenticated, "localhost:7331")
}

func requestHost(server *Server, authManager *auth.Manager, method, path, body string, authenticated bool, host string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	request.Host = host
	if authenticated {
		request.Header.Set("Authorization", "Bearer "+authManager.Token())
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}
