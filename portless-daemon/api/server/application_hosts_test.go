package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestApplicationHostsForwardApplicationAndControlPaths(t *testing.T) {
	server, daemonControl := newApplicationHostServer(t)
	for _, test := range []struct {
		method, target, body string
		authenticated        bool
	}{
		{http.MethodGet, "/api", "", false},
		{http.MethodGet, "/api/", "", false},
		{http.MethodGet, "/api/orders?tag=one&tag=two&search=coffee%20mug", "", false},
		{http.MethodGet, "/auth", "", false},
		{http.MethodGet, "/auth/", "", false},
		{http.MethodPost, "/auth/login", `{"fixture":"login request"}`, false},
		{http.MethodGet, "/api/v1/projects", "", false},
		{http.MethodGet, "/api/v1/health", "", false},
		{http.MethodGet, "/api/v1/daemon/logs", "", true},
		{http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"test-daemon"}`, true},
	} {
		t.Run(test.method+" "+test.target, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			req := httptest.NewRequestWithContext(ctx, test.method, "http://checkout.local.billing.localhost"+test.target, strings.NewReader(test.body))
			req.Header["X-Application-Header"] = []string{"one", "two"}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-Host", "portless.localhost")
			if test.authenticated {
				req.Header.Set("Authorization", "Bearer "+server.auth.Token())
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != http.StatusCreated || response.Header().Get("X-Application-Service") != "checkout" {
				t.Fatalf("application response = %d %s", response.Code, response.Body.String())
			}
			var received applicationHostRequest
			if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil {
				t.Fatal(err)
			}
			if received.Method != test.method || received.Target != test.target || received.Body != test.body ||
				!slices.Equal(received.Headers, []string{"one", "two"}) || received.ContentType != "application/json" {
				t.Fatalf("application received a changed request: %#v", received)
			}
		})
	}
	if daemonControl.restartCalls != 0 || daemonControl.logCalls != 0 || daemonControl.committedRestart != "" {
		t.Fatalf("application request invoked daemon control: %#v", daemonControl)
	}
}

func TestApplicationHostDoesNotConsumeBrowserClaim(t *testing.T) {
	server, _ := newApplicationHostServer(t)
	claim, _, err := server.auth.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	path := "/auth/claim/" + claim
	application := requestHost(server, server.auth, http.MethodGet, path, "", false, "checkout.local.billing.localhost")
	if application.Code != http.StatusCreated || application.Header().Get("X-Application-Service") != "checkout" || len(application.Result().Cookies()) != 0 {
		t.Fatalf("application claim request = %d, cookies = %v", application.Code, application.Result().Cookies())
	}
	control := requestHost(server, server.auth, http.MethodGet, path, "", false, "portless.localhost")
	if control.Code != http.StatusSeeOther || control.Header().Get("Location") != "/projects" {
		t.Fatalf("control host could not consume the claim: %d %s", control.Code, control.Body.String())
	}
	cookies := control.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != auth.SessionCookie || !cookies[0].HttpOnly || cookies[0].Domain != "" {
		t.Fatalf("control host did not issue a host-only session cookie: %v", cookies)
	}
	reuse := requestHost(server, server.auth, http.MethodGet, path, "", false, "portless.localhost")
	if reuse.Code != http.StatusUnauthorized || !strings.Contains(reuse.Body.String(), `"code":"INVALID_BROWSER_CLAIM"`) {
		t.Fatalf("claim reuse = %d %s", reuse.Code, reuse.Body.String())
	}
}

func TestApplicationHostRoutingDoesNotFallBackToControlHandlers(t *testing.T) {
	server, _ := newApplicationHostServer(t)
	for _, host := range []string{"missing.local.billing.localhost", "checkout.stopped.billing.localhost"} {
		t.Run(host, func(t *testing.T) {
			response := requestHost(server, server.auth, http.MethodGet, "/api/v1/projects", "", true, host)
			if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "is not available") {
				t.Fatalf("missing application did not return an ingress failure: %d %s", response.Code, response.Body.String())
			}
		})
	}
	for _, host := range []string{"malicious.example", "billing.localhost", "checkout.local.bad_name.localhost"} {
		t.Run(host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/projects", nil)
			req.Header.Set("Authorization", "Bearer "+server.auth.Token())
			req.Header.Set("X-Forwarded-Host", "portless.localhost")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != http.StatusMisdirectedRequest || !strings.Contains(response.Body.String(), `"code":"UNKNOWN_HOST"`) {
				t.Fatalf("unknown host reached a handler: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestApplicationAndControlHostsKeepNormalizationAndAuthentication(t *testing.T) {
	server, _ := newApplicationHostServer(t)
	application := requestHost(server, server.auth, http.MethodGet, "/api/orders", "", false, "CHECKOUT.Local.Billing.Localhost:7331")
	if application.Code != http.StatusCreated || application.Header().Get("X-Application-Service") != "checkout" {
		t.Fatalf("normalized application host = %d %s", application.Code, application.Body.String())
	}
	for _, host := range []string{"PORTLESS.Localhost:7331", "localhost", "127.0.0.1:7331", "[::1]:7331"} {
		t.Run(host, func(t *testing.T) {
			health := requestHost(server, server.auth, http.MethodGet, "/api/v1/health", "", false, host)
			if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"ready":true`) {
				t.Fatalf("control health = %d %s", health.Code, health.Body.String())
			}
			denied := requestHost(server, server.auth, http.MethodGet, "/api/v1/projects", "", false, host)
			if denied.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated control request = %d", denied.Code)
			}
			allowed := requestHost(server, server.auth, http.MethodGet, "/api/v1/projects", "", true, host)
			if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), `"name":"billing"`) {
				t.Fatalf("authenticated control request = %d %s", allowed.Code, allowed.Body.String())
			}
		})
	}
}

type applicationHostRequest struct {
	Method      string
	Target      string
	Body        string
	Headers     []string
	ContentType string
}

func newApplicationHostServer(t *testing.T) (*Server, *fakeDaemonControl) {
	t.Helper()
	return newApplicationHostServerWithUpstream(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			writer.WriteHeader(http.StatusOK)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 4096))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("X-Application-Service", "checkout")
		writeJSON(writer, http.StatusCreated, applicationHostRequest{
			Method: request.Method, Target: request.URL.RequestURI(), Body: string(body),
			Headers: request.Header.Values("X-Application-Header"), ContentType: request.Header.Get("Content-Type"),
		})
	}))
}

func newApplicationHostServerWithUpstream(t *testing.T, handler http.Handler) (*Server, *fakeDaemonControl) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	data := t.TempDir()
	store, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Required: true}}}
	if _, err := store.CreateProject(ctx, "billing", definition, []model.ProjectSource{{Name: "checkout", Services: []string{"checkout"}}}); err != nil {
		t.Fatal(err)
	}
	bindings := []model.ComponentBinding{{Service: "checkout", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: upstream.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadWrite, HealthPath: "/health",
	}}}
	if _, err := store.CreateEnvironment(ctx, "billing", "local", definition, nil, bindings); err != nil {
		t.Fatal(err)
	}
	app := controlplane.New(store, events.NewBroker(), controlplane.Config{DataDirectory: data, InstallationKey: "test-installation"})
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		app.Close(cleanupCtx)
	})
	operation, err := app.Up(ctx, "billing", "local", "test", "", controlplane.UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for operation.State == "running" {
		select {
		case <-ctx.Done():
			t.Fatalf("application startup timed out: %#v", operation)
		case <-ticker.C:
		}
		operation, err = app.Operation(ctx, "billing", "local", operation.Number)
		if err != nil {
			t.Fatal(err)
		}
	}
	if operation.State != "succeeded" {
		t.Fatalf("application startup failed: %#v", operation)
	}
	authManager, err := auth.LoadOrCreate(filepath.Join(data, "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	daemonControl := &fakeDaemonControl{identity: contract.DaemonStatus{InstanceID: "test-daemon"}}
	server, err := New(Dependencies{
		Application: app, Auth: authManager, DaemonControl: daemonControl,
		Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>Portless control UI</html>")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, daemonControl
}
