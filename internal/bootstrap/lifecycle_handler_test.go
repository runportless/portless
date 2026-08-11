package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/daemon"
)

func TestLifecycleHandlerRequiresCLIAuthenticationAndControlHost(t *testing.T) {
	authManager, err := auth.LoadOrCreate(filepath.Join(t.TempDir(), "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	handler := &lifecycleHandler{
		next: http.NotFoundHandler(), auth: authManager,
		identity: daemon.Identity{Product: daemon.Product, InstanceID: "instance"},
		activeEnvironments: func(context.Context) ([]string, error) {
			return []string{"shop/local", "billing/qa"}, nil
		},
		shutdown: func() {},
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+daemon.IdentityPath, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated identity returned %d", unauthenticated.Code)
	}

	wrongHostRequest := httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost"+daemon.IdentityPath, nil)
	wrongHostRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
	wrongHost := httptest.NewRecorder()
	handler.ServeHTTP(wrongHost, wrongHostRequest)
	if wrongHost.Code != http.StatusMisdirectedRequest {
		t.Fatalf("application host identity returned %d", wrongHost.Code)
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+daemon.IdentityPath, nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
	authenticated := httptest.NewRecorder()
	handler.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated identity returned %d: %s", authenticated.Code, authenticated.Body.String())
	}
	var identity daemon.Identity
	if err := json.Unmarshal(authenticated.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.ActiveEnvironments, ",") != "billing/qa,shop/local" {
		t.Fatalf("active environments = %#v", identity.ActiveEnvironments)
	}
}

func TestLifecycleHandlerRefusesActiveShutdownUnlessForced(t *testing.T) {
	authManager, err := auth.LoadOrCreate(filepath.Join(t.TempDir(), "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	var shutdowns atomic.Int32
	handler := &lifecycleHandler{
		next: http.NotFoundHandler(), auth: authManager,
		identity: daemon.Identity{Product: daemon.Product, InstanceID: "instance"},
		activeEnvironments: func(context.Context) ([]string, error) {
			return []string{"billing/local"}, nil
		},
		shutdown: func() { shutdowns.Add(1) },
	}

	request := func(instance string, force bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(daemon.ShutdownRequest{InstanceID: instance, Force: force})
		httpRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+daemon.ShutdownPath, bytes.NewReader(body))
		httpRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httpRequest)
		return response
	}

	if response := request("other-instance", true); response.Code != http.StatusConflict || shutdowns.Load() != 0 {
		t.Fatalf("instance mismatch returned %d and %d shutdowns", response.Code, shutdowns.Load())
	}
	if response := request("instance", false); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "billing/local") || shutdowns.Load() != 0 {
		t.Fatalf("active shutdown returned %d, body %s, shutdowns %d", response.Code, response.Body.String(), shutdowns.Load())
	}
	if response := request("instance", true); response.Code != http.StatusAccepted || shutdowns.Load() != 1 {
		t.Fatalf("forced shutdown returned %d, body %s, shutdowns %d", response.Code, response.Body.String(), shutdowns.Load())
	}
}

func TestInspectDaemonAuthenticatesIdentityAndDetectsBuildMismatch(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("installation-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(paths.Token)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	identity := daemon.Identity{
		Product: daemon.Product, ProtocolVersion: daemon.ProtocolVersion, APIVersion: api.APIVersion,
		InstallationID: installationID, InstanceID: "instance", BuildID: "older-build",
		PID: os.Getpid(), StartedAt: startedAt, ActiveEnvironments: []string{},
	}
	handler := &lifecycleHandler{next: http.NotFoundHandler(), auth: authManager, identity: identity, shutdown: func() {}}
	server := httptest.NewServer(handler)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	record := ControlRecord{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.Token, StartedAt: identity.StartedAt,
	}
	if err := writeControl(paths, record); err != nil {
		t.Fatal(err)
	}

	inspection, err := InspectDaemon(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Compatible || inspection.CurrentBuild || len(inspection.Problems) != 1 || !strings.Contains(inspection.Problems[0], "executable differs") {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}

	record.InstanceID = "tampered-instance"
	if err := writeControl(paths, record); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDaemon(context.Background(), paths); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func TestEnsureDaemonKeepsCompatibleOutdatedBuildWhileEnvironmentIsActive(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("installation-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(paths.Token)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	identity := daemon.Identity{
		Product: daemon.Product, ProtocolVersion: daemon.ProtocolVersion, APIVersion: api.APIVersion,
		InstallationID: installationID, InstanceID: "active-instance", BuildID: "older-build",
		PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	var shutdowns atomic.Int32
	handler := &lifecycleHandler{
		next: http.NotFoundHandler(), auth: authManager, identity: identity,
		activeEnvironments: func(context.Context) ([]string, error) { return []string{"billing/local"}, nil },
		shutdown:           func() { shutdowns.Add(1) },
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	record := ControlRecord{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.Token, StartedAt: identity.StartedAt,
	}
	if err := writeControl(paths, record); err != nil {
		t.Fatal(err)
	}

	actual, err := EnsureDaemon(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if actual.InstanceID != record.InstanceID || shutdowns.Load() != 0 {
		t.Fatalf("EnsureDaemon replaced an active compatible daemon: record=%#v shutdowns=%d", actual, shutdowns.Load())
	}
}
