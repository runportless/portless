package api

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/daemon"
	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/logstore"
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
	daemonControl := &fakeDaemonControl{identity: daemon.Identity{
		Product: daemon.Product, State: "ready", PID: 33083, StartedAt: time.Now().UTC().Add(-time.Minute),
		InstanceID: "instance-current", BuildID: "build-current", ProtocolVersion: "2", APIVersion: APIVersion,
		HandoffReady: true, RecoveryProblems: []string{}, ActiveEnvironments: []string{"billing/local"},
	}}
	server, err := New(app, authManager, assets, daemonControl)
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := request(server, authManager, http.MethodGet, "/api/v1/projects", "", false)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control request returned %d", unauthenticated.Code)
	}
	projects := request(server, authManager, http.MethodGet, "/api/v1/projects", "", true)
	if projects.Code != http.StatusOK || !strings.Contains(projects.Body.String(), `"name":"billing"`) || !strings.Contains(projects.Body.String(), `"environments":[`) || !strings.Contains(projects.Body.String(), `"total":1`) {
		t.Fatalf("projects response code=%d body=%s", projects.Code, projects.Body.String())
	}
	environment := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local", "", true)
	if environment.Code != http.StatusOK || !strings.Contains(environment.Body.String(), `"project":"billing"`) || !strings.Contains(environment.Body.String(), `"name":"local"`) ||
		!strings.Contains(environment.Body.String(), `"dashboardUrl":"http://portless.localhost/environments/billing/local"`) ||
		!strings.Contains(environment.Body.String(), `"ingressUrl":"http://checkout.local.billing.localhost"`) || strings.Contains(environment.Body.String(), ".localhost:7331") {
		t.Fatalf("environment API did not use clean scoped URLs: %s", environment.Body.String())
	}

	started := time.Now().UTC().Add(-time.Second)
	httpEvent := app.Broker().AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolHTTP, Source: "checkout", Target: "orders", StartedAt: started, CompletedAt: started.Add(12 * time.Millisecond), Method: "GET", Path: "/orders", Status: 200, RequestHeaders: map[string]string{"Accept": "application/json"}, ResponseHeaders: map[string]string{"Content-Type": "application/json"}})
	app.Broker().AddTraffic(model.TrafficEvent{Project: "billing", Environment: "local", Protocol: model.ProtocolPostgres, Source: "checkout", Target: "postgres", StartedAt: started, CompletedAt: started.Add(2 * time.Millisecond)})
	httpTraffic := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic?protocol=http&service=checkout&limit=10", "", true)
	if httpTraffic.Code != http.StatusOK || !strings.Contains(httpTraffic.Body.String(), `"target":"orders"`) || strings.Contains(httpTraffic.Body.String(), `"target":"postgres"`) || strings.Contains(httpTraffic.Body.String(), `"requestHeaders"`) {
		t.Fatalf("filtered HTTP traffic response code=%d body=%s", httpTraffic.Code, httpTraffic.Body.String())
	}
	tcpTraffic := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic?protocol=tcp&edge=checkout:postgres", "", true)
	if tcpTraffic.Code != http.StatusOK || !strings.Contains(tcpTraffic.Body.String(), `"protocol":"postgres"`) {
		t.Fatalf("filtered TCP traffic response code=%d body=%s", tcpTraffic.Code, tcpTraffic.Body.String())
	}
	trafficDetail := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic/"+strconv.FormatInt(httpEvent.Sequence, 10), "", true)
	if trafficDetail.Code != http.StatusOK || !strings.Contains(trafficDetail.Body.String(), `"requestHeaders":{"Accept":"application/json"}`) || !strings.Contains(trafficDetail.Body.String(), `"responseHeaders":{"Content-Type":"application/json"}`) {
		t.Fatalf("traffic detail response code=%d body=%s", trafficDetail.Code, trafficDetail.Body.String())
	}
	invalidTraffic := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/traffic?protocol=udp", "", true)
	if invalidTraffic.Code != http.StatusBadRequest || !strings.Contains(invalidTraffic.Body.String(), `"code":"INVALID_TRAFFIC_PROTOCOL"`) {
		t.Fatalf("invalid traffic protocol response code=%d body=%s", invalidTraffic.Code, invalidTraffic.Body.String())
	}
	invalidLimit := request(server, authManager, http.MethodGet, "/api/v1/projects?limit=0", "", true)
	if invalidLimit.Code != http.StatusBadRequest || !strings.Contains(invalidLimit.Body.String(), `"code":"INVALID_LIMIT"`) {
		t.Fatalf("invalid limit response code=%d body=%s", invalidLimit.Code, invalidLimit.Body.String())
	}
	daemonStatus := request(server, authManager, http.MethodGet, "/api/v1/daemon", "", true)
	if daemonStatus.Code != http.StatusOK || !strings.Contains(daemonStatus.Body.String(), `"instanceId":"instance-current"`) || !strings.Contains(daemonStatus.Body.String(), `"protocolVersion":"2"`) || strings.Contains(daemonStatus.Body.String(), "installationId") {
		t.Fatalf("daemon status response code=%d body=%s", daemonStatus.Code, daemonStatus.Body.String())
	}
	daemonRestart := request(server, authManager, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"instance-current"}`, true)
	if daemonRestart.Code != http.StatusAccepted || !strings.Contains(daemonRestart.Body.String(), `"restarting":true`) || daemonControl.restartedInstance != "instance-current" {
		t.Fatalf("daemon restart response code=%d body=%s restarted=%q", daemonRestart.Code, daemonRestart.Body.String(), daemonControl.restartedInstance)
	}
	staleRestart := request(server, authManager, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"stale"}`, true)
	if staleRestart.Code != http.StatusConflict || !strings.Contains(staleRestart.Body.String(), `"code":"DAEMON_INSTANCE_CHANGED"`) {
		t.Fatalf("stale daemon restart response code=%d body=%s", staleRestart.Code, staleRestart.Body.String())
	}
	claim, _, err := authManager.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, csrf, _, _, err := authManager.ConsumeClaim(claim)
	if err != nil {
		t.Fatal(err)
	}
	missingCSRF := requestBrowser(server, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"instance-current"}`, sessionToken, "")
	if missingCSRF.Code != http.StatusForbidden {
		t.Fatalf("browser restart without CSRF returned %d body=%s", missingCSRF.Code, missingCSRF.Body.String())
	}
	browserRestart := requestBrowser(server, http.MethodPost, "/api/v1/daemon/restart", `{"instanceId":"instance-current"}`, sessionToken, csrf)
	if browserRestart.Code != http.StatusAccepted {
		t.Fatalf("browser restart with CSRF returned %d body=%s", browserRestart.Code, browserRestart.Body.String())
	}
	browserReset := requestBrowser(server, http.MethodPost, "/api/v1/runtime/reset", "", sessionToken, csrf)
	if browserReset.Code != http.StatusForbidden || !strings.Contains(browserReset.Body.String(), `"code":"CLI_AUTH_REQUIRED"`) {
		t.Fatalf("browser runtime reset returned %d body=%s", browserReset.Code, browserReset.Body.String())
	}

	privateKey, err := controlStore.PrivateEnvironmentKey(context.Background(), "billing", "local")
	if err != nil {
		t.Fatal(err)
	}
	logDirectory := filepath.Join(data, "environments", privateKey, "logs", "checkout", "1")
	sink, err := logstore.OpenSink(logDirectory, "checkout", "stdout", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Write([]byte("ready on an allocated port\n")); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	logs := request(server, authManager, http.MethodGet, "/api/v1/environments/billing/local/logs?limit=10&since=1h", "", true)
	if logs.Code != http.StatusOK || !strings.Contains(logs.Body.String(), `"service":"checkout"`) || !strings.Contains(logs.Body.String(), `"stream":"stdout"`) || !strings.Contains(logs.Body.String(), `"message":"ready on an allocated port"`) {
		t.Fatalf("structured logs response code=%d body=%s", logs.Code, logs.Body.String())
	}

	binding := request(server, authManager, http.MethodPut, "/api/v1/environments/billing/local/bindings/checkout", `{"provider":"remote","remote":{"url":"https://checkout.qa.example.test","classification":"qa","writePolicy":"read-only","healthPath":"/health"}}`, true)
	if binding.Code != http.StatusOK || !strings.Contains(binding.Body.String(), `"provider":"remote"`) || !strings.Contains(binding.Body.String(), `"writePolicy":"read-only"`) {
		t.Fatalf("remote binding response code=%d body=%s", binding.Code, binding.Body.String())
	}
	if err := controlStore.SetEnvironmentStatus(context.Background(), "billing", "local", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	blockedReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset", "", true)
	if blockedReset.Code != http.StatusConflict || !strings.Contains(blockedReset.Body.String(), `"code":"ACTIVE_ENVIRONMENTS"`) {
		t.Fatalf("active environment reset returned %d body=%s", blockedReset.Code, blockedReset.Body.String())
	}
	if err := controlStore.SetEnvironmentStatus(context.Background(), "billing", "local", model.EnvironmentStopped, ""); err != nil {
		t.Fatal(err)
	}
	preparedReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset", "", true)
	if preparedReset.Code != http.StatusOK || !strings.Contains(preparedReset.Body.String(), `"processes":0`) || !strings.Contains(preparedReset.Body.String(), `"runtimes":[]`) {
		t.Fatalf("runtime reset preparation returned %d body=%s", preparedReset.Code, preparedReset.Body.String())
	}
	canceledReset := request(server, authManager, http.MethodPost, "/api/v1/runtime/reset/cancel", "", true)
	if canceledReset.Code != http.StatusNoContent {
		t.Fatalf("runtime reset cancellation returned %d body=%s", canceledReset.Code, canceledReset.Body.String())
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

type fakeDaemonControl struct {
	identity          daemon.Identity
	restartedInstance string
}

func (f *fakeDaemonControl) Status(context.Context) (daemon.Identity, error) {
	return f.identity, nil
}

func (f *fakeDaemonControl) Restart(_ context.Context, instanceID string) (daemon.ShutdownResponse, error) {
	if instanceID != f.identity.InstanceID {
		return daemon.ShutdownResponse{}, &daemon.LifecycleError{Code: "DAEMON_INSTANCE_CHANGED", Message: "daemon instance changed"}
	}
	f.restartedInstance = instanceID
	return daemon.ShutdownResponse{Stopping: true, Handoff: true, InstanceID: instanceID, ActiveEnvironments: append([]string(nil), f.identity.ActiveEnvironments...)}, nil
}

func TestApplicationHostRequiresServiceEnvironmentProject(t *testing.T) {
	if service, environment, project, ok := applicationHost("checkout.local.billing.localhost"); !ok || service != "checkout" || environment != "local" || project != "billing" {
		t.Fatalf("canonical host parsed as %q %q %q %v", service, environment, project, ok)
	}
	if _, _, _, ok := applicationHost("checkout.billing.localhost"); ok {
		t.Fatal("two-label application host should not be accepted")
	}
}

func TestTrafficSummaryOmitsHeadersWithoutMutatingDetail(t *testing.T) {
	detail := model.TrafficEvent{RequestHeaders: map[string]string{"Authorization": "[REDACTED]"}, ResponseHeaders: map[string]string{"Set-Cookie": "[REDACTED]"}}
	summary := trafficSummary(detail)
	if summary.RequestHeaders != nil || summary.ResponseHeaders != nil {
		t.Fatalf("summary retained headers: %#v", summary)
	}
	if len(detail.RequestHeaders) != 1 || len(detail.ResponseHeaders) != 1 {
		t.Fatalf("summary mutated detail: %#v", detail)
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
	server, err := New(app, authManager, assets, nil)
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

func requestBrowser(server *Server, method, path, body, sessionToken, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://portless.localhost"+path, strings.NewReader(body))
	request.Host = "portless.localhost"
	request.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: sessionToken})
	request.Header.Set("Origin", "http://portless.localhost")
	if csrf != "" {
		request.Header.Set("X-Portless-CSRF", csrf)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}
