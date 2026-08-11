package api

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
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

func TestControlAndApplicationHostsAreSeparated(t *testing.T) {
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	_, err = controlStore.CreateProject(context.Background(), "billing", "/tmp/api-fixture", model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}})
	if err != nil {
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

	unauthenticated := httptest.NewRequest(http.MethodGet, "http://localhost:7331/api/v1/projects", nil)
	unauthenticated.Host = "localhost:7331"
	unauthenticatedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control request returned %d", unauthenticatedResponse.Code)
	}

	authenticated := httptest.NewRequest(http.MethodGet, "http://localhost:7331/api/v1/projects", nil)
	authenticated.Host = "localhost:7331"
	authenticated.Header.Set("Authorization", "Bearer "+authManager.Token())
	authenticatedResponse := httptest.NewRecorder()
	server.ServeHTTP(authenticatedResponse, authenticated)
	if authenticatedResponse.Code != http.StatusOK || !strings.Contains(authenticatedResponse.Body.String(), `"name":"billing"`) {
		t.Fatalf("authenticated response code=%d body=%s", authenticatedResponse.Code, authenticatedResponse.Body.String())
	}
	if strings.Contains(authenticatedResponse.Body.String(), `"trusted"`) || strings.Contains(authenticatedResponse.Body.String(), "review_required") {
		t.Fatalf("removed trust state leaked through project API: %s", authenticatedResponse.Body.String())
	}
	if !strings.Contains(authenticatedResponse.Body.String(), `"dashboardUrl":"http://portless.localhost/projects/billing"`) ||
		!strings.Contains(authenticatedResponse.Body.String(), `"ingressUrl":"http://checkout.billing.localhost"`) ||
		strings.Contains(authenticatedResponse.Body.String(), ".localhost:7331") {
		t.Fatalf("project API did not use clean localhost URLs: %s", authenticatedResponse.Body.String())
	}

	browserClaim := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/browser-claims", strings.NewReader(`{"next":"/projects/billing"}`))
	browserClaim.Host = "127.0.0.1:7331"
	browserClaim.Header.Set("Authorization", "Bearer "+authManager.Token())
	browserClaimResponse := httptest.NewRecorder()
	server.ServeHTTP(browserClaimResponse, browserClaim)
	if browserClaimResponse.Code != http.StatusCreated || !strings.Contains(browserClaimResponse.Body.String(), `"url":"http://portless.localhost/auth/claim/`) || strings.Contains(browserClaimResponse.Body.String(), ":7331") {
		t.Fatalf("browser claim did not use clean control origin: %d %s", browserClaimResponse.Code, browserClaimResponse.Body.String())
	}

	selectRuntime := httptest.NewRequest(http.MethodPut, "http://localhost:7331/api/v1/runtime", strings.NewReader(`{"preference":"podman"}`))
	selectRuntime.Host = "localhost:7331"
	selectRuntime.Header.Set("Authorization", "Bearer "+authManager.Token())
	selectRuntimeResponse := httptest.NewRecorder()
	server.ServeHTTP(selectRuntimeResponse, selectRuntime)
	if selectRuntimeResponse.Code != http.StatusOK || !strings.Contains(selectRuntimeResponse.Body.String(), `"preference":"podman"`) || !strings.Contains(selectRuntimeResponse.Body.String(), `"candidates"`) {
		t.Fatalf("runtime selection code=%d body=%s", selectRuntimeResponse.Code, selectRuntimeResponse.Body.String())
	}

	invalidRuntime := httptest.NewRequest(http.MethodPut, "http://localhost:7331/api/v1/runtime", strings.NewReader(`{"preference":"compose"}`))
	invalidRuntime.Host = "localhost:7331"
	invalidRuntime.Header.Set("Authorization", "Bearer "+authManager.Token())
	invalidRuntimeResponse := httptest.NewRecorder()
	server.ServeHTTP(invalidRuntimeResponse, invalidRuntime)
	if invalidRuntimeResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime returned %d: %s", invalidRuntimeResponse.Code, invalidRuntimeResponse.Body.String())
	}

	removedDraft := httptest.NewRequest(http.MethodGet, "http://localhost:7331/api/v1/projects/billing/draft", nil)
	removedDraft.Host = "localhost:7331"
	removedDraft.Header.Set("Authorization", "Bearer "+authManager.Token())
	removedDraftResponse := httptest.NewRecorder()
	server.ServeHTTP(removedDraftResponse, removedDraft)
	if removedDraftResponse.Code != http.StatusNotFound {
		t.Fatalf("removed draft API returned %d", removedDraftResponse.Code)
	}

	applicationAPI := httptest.NewRequest(http.MethodGet, "http://checkout.billing.localhost/api/v1/projects", nil)
	applicationAPI.Host = "checkout.billing.localhost"
	applicationResponse := httptest.NewRecorder()
	server.ServeHTTP(applicationResponse, applicationAPI)
	if applicationResponse.Code != http.StatusMisdirectedRequest {
		t.Fatalf("application host reached control API: %d", applicationResponse.Code)
	}

	unknown := httptest.NewRequest(http.MethodGet, "http://malicious.example/api/v1/health", nil)
	unknown.Host = "malicious.example"
	unknownResponse := httptest.NewRecorder()
	server.ServeHTTP(unknownResponse, unknown)
	if unknownResponse.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host returned %d", unknownResponse.Code)
	}
}
