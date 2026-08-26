package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	daemonidentity "github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/lifecycle"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func TestLifecycleHandlerRequiresCLIAuthenticationAndControlHost(t *testing.T) {
	authManager, err := auth.LoadOrCreate(filepath.Join(t.TempDir(), "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	var handoffChecks atomic.Int32
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Next: http.NotFoundHandler(), Auth: authManager,
		Identity: lifecycle.Identity{Product: lifecycle.Product, InstanceID: "instance"},
		ActiveEnvironments: func(context.Context) ([]string, error) {
			return []string{"shop/local", "billing/qa"}, nil
		},
		HandoffStatus: func(context.Context) (bool, []string) {
			handoffChecks.Add(1)
			return true, nil
		},
		Shutdown: func() {},
	})

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+lifecycle.IdentityPath, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated identity returned %d", unauthenticated.Code)
	}

	wrongHostRequest := httptest.NewRequest(http.MethodGet, "http://checkout.local.billing.localhost"+lifecycle.IdentityPath, nil)
	wrongHostRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
	wrongHost := httptest.NewRecorder()
	handler.ServeHTTP(wrongHost, wrongHostRequest)
	if wrongHost.Code != http.StatusMisdirectedRequest {
		t.Fatalf("application host identity returned %d", wrongHost.Code)
	}

	authenticatedRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+lifecycle.IdentityPath, nil)
	authenticatedRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
	authenticated := httptest.NewRecorder()
	handler.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated identity returned %d: %s", authenticated.Code, authenticated.Body.String())
	}
	var identity lifecycle.Identity
	if err := json.Unmarshal(authenticated.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if strings.Join(identity.ActiveEnvironments, ",") != "billing/qa,shop/local" {
		t.Fatalf("active environments = %#v", identity.ActiveEnvironments)
	}
	if handoffChecks.Load() != 0 {
		t.Fatalf("identity performed %d handoff checks, want none", handoffChecks.Load())
	}

	handoffRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+lifecycle.HandoffPath, nil)
	handoffRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
	handoffResponse := httptest.NewRecorder()
	handler.ServeHTTP(handoffResponse, handoffRequest)
	if handoffResponse.Code != http.StatusOK {
		t.Fatalf("handoff verification returned %d: %s", handoffResponse.Code, handoffResponse.Body.String())
	}
	var handoff lifecycle.HandoffStatus
	if err := json.Unmarshal(handoffResponse.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	if handoff.State != lifecycle.HandoffReady || handoff.VerifiedAt.IsZero() || strings.Join(handoff.ActiveEnvironments, ",") != "billing/qa,shop/local" || handoffChecks.Load() != 1 {
		t.Fatalf("unexpected handoff verification: status=%#v checks=%d", handoff, handoffChecks.Load())
	}
}

func TestLifecycleHandlerRefusesActiveShutdownUnlessForced(t *testing.T) {
	authManager, err := auth.LoadOrCreate(filepath.Join(t.TempDir(), "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	var shutdowns atomic.Int32
	var handoffReady atomic.Bool
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Next: http.NotFoundHandler(), Auth: authManager,
		Identity: lifecycle.Identity{Product: lifecycle.Product, InstanceID: "instance"},
		ActiveEnvironments: func(context.Context) ([]string, error) {
			return []string{"billing/local"}, nil
		},
		HandoffStatus: func(context.Context) (bool, []string) {
			if handoffReady.Load() {
				return true, nil
			}
			return false, []string{"checkout has no recoverable supervisor"}
		},
		Shutdown: func() { shutdowns.Add(1) },
	})

	request := func(instance string, force, handoff bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(lifecycle.ShutdownRequest{InstanceID: instance, Force: force, Handoff: handoff})
		httpRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+lifecycle.ShutdownPath, bytes.NewReader(body))
		httpRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httpRequest)
		return response
	}

	if response := request("other-instance", true, false); response.Code != http.StatusConflict || shutdowns.Load() != 0 {
		t.Fatalf("instance mismatch returned %d and %d shutdowns", response.Code, shutdowns.Load())
	}
	if response := request("instance", false, false); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "billing/local") || shutdowns.Load() != 0 {
		t.Fatalf("active shutdown returned %d, body %s, shutdowns %d", response.Code, response.Body.String(), shutdowns.Load())
	}
	if response := request("instance", false, true); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "recoverable supervisor") || shutdowns.Load() != 0 {
		t.Fatalf("unsafe handoff returned %d, body %s, shutdowns %d", response.Code, response.Body.String(), shutdowns.Load())
	}
	handoffReady.Store(true)
	if response := request("instance", false, true); response.Code != http.StatusAccepted || shutdowns.Load() != 1 {
		t.Fatalf("safe handoff returned %d, body %s, shutdowns %d", response.Code, response.Body.String(), shutdowns.Load())
	}
	if response := request("instance", true, false); response.Code != http.StatusAccepted || shutdowns.Load() != 2 {
		t.Fatalf("forced shutdown returned %d, body %s, shutdowns %d", response.Code, response.Body.String(), shutdowns.Load())
	}
}

func TestLifecycleIdentityRemainsAvailableWhenApplicationInventoryFails(t *testing.T) {
	authManager, err := auth.LoadOrCreate(filepath.Join(t.TempDir(), "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	var shutdowns atomic.Int32
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Next: http.NotFoundHandler(), Auth: authManager,
		Identity: lifecycle.Identity{Product: lifecycle.Product, InstanceID: "instance", State: "ready", RecoveryProblems: []string{"stored topology is incompatible"}},
		ActiveEnvironments: func(context.Context) ([]string, error) {
			return nil, errors.New("decode environment store/local")
		},
		Shutdown: func() { shutdowns.Add(1) },
	})

	identityRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+lifecycle.IdentityPath, nil)
	identityRequest.Header.Set("Authorization", "Bearer "+authManager.Token())
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identityRequest)
	if identityResponse.Code != http.StatusOK {
		t.Fatalf("identity returned %d: %s", identityResponse.Code, identityResponse.Body.String())
	}
	var identity lifecycle.Identity
	if err := json.Unmarshal(identityResponse.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if len(identity.ActiveEnvironments) != 0 || !strings.Contains(strings.Join(identity.RecoveryProblems, "; "), "active environment inventory is unavailable") {
		t.Fatalf("unexpected degraded identity: %#v", identity)
	}

	shutdown := func(force bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(lifecycle.ShutdownRequest{InstanceID: "instance", Force: force})
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+lifecycle.ShutdownPath, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+authManager.Token())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := shutdown(false); response.Code != http.StatusInternalServerError || shutdowns.Load() != 0 {
		t.Fatalf("ordinary shutdown returned %d and scheduled %d shutdowns: %s", response.Code, shutdowns.Load(), response.Body.String())
	}
	if response := shutdown(true); response.Code != http.StatusAccepted || shutdowns.Load() != 1 {
		t.Fatalf("forced shutdown returned %d and scheduled %d shutdowns: %s", response.Code, shutdowns.Load(), response.Body.String())
	}
	if _, err := handler.Restart(context.Background(), "instance"); err == nil {
		t.Fatal("browser restart accepted unavailable application inventory")
	}
}

func TestLifecycleHandlerGuardsBrowserRestart(t *testing.T) {
	handoffReady := false
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Identity: lifecycle.Identity{Product: lifecycle.Product, InstanceID: "instance", State: "ready", RecoveryProblems: []string{"reconciliation warning"}},
		ActiveEnvironments: func(context.Context) ([]string, error) {
			return []string{"billing/local"}, nil
		},
		HandoffStatus: func(context.Context) (bool, []string) {
			if handoffReady {
				return true, nil
			}
			return false, []string{"checkout has no recoverable supervisor"}
		},
	})

	if _, err := handler.Restart(context.Background(), "other-instance"); err == nil {
		t.Fatal("restart accepted a stale daemon instance")
	} else {
		var lifecycleError *lifecycle.LifecycleError
		if !errors.As(err, &lifecycleError) || lifecycleError.Code != "DAEMON_INSTANCE_CHANGED" {
			t.Fatalf("stale restart error = %#v, want DAEMON_INSTANCE_CHANGED", err)
		}
	}
	if _, err := handler.Restart(context.Background(), "instance"); err == nil {
		t.Fatal("restart accepted an unsafe active handoff")
	} else {
		var lifecycleError *lifecycle.LifecycleError
		if !errors.As(err, &lifecycleError) || lifecycleError.Code != "HANDOFF_UNAVAILABLE" || len(lifecycleError.Problems) != 1 {
			t.Fatalf("unsafe restart error = %#v", err)
		}
	}
	handoffReady = true
	result, err := handler.Restart(context.Background(), "instance")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Stopping || !result.Handoff || result.InstanceID != "instance" || strings.Join(result.ActiveEnvironments, ",") != "billing/local" {
		t.Fatalf("unexpected restart result: %#v", result)
	}
}

func TestInspectDaemonAuthenticatesIdentityAndDetectsBuildMismatch(t *testing.T) {
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("installation-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(paths.AuthToken)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := installation.InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	identity := lifecycle.Identity{
		Product: lifecycle.Product, ProtocolVersion: lifecycle.ProtocolVersion, APIVersion: contract.APIVersion,
		InstallationID: installationID, InstanceID: "instance", BuildID: "older-build",
		PID: os.Getpid(), StartedAt: startedAt, ActiveEnvironments: []string{},
	}
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{Next: http.NotFoundHandler(), Auth: authManager, Identity: identity, Shutdown: func() {}})
	server := httptest.NewServer(handler)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	record := daemonidentity.Record{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.AuthToken, StartedAt: identity.StartedAt,
	}
	if err := daemonidentity.Write(paths, record); err != nil {
		t.Fatal(err)
	}

	inspection, err := New(paths).inspectDaemon(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.Compatible || inspection.CurrentBuild || len(inspection.Problems) != 1 || !strings.Contains(inspection.Problems[0], "executable differs") {
		t.Fatalf("unexpected inspection: %#v", inspection)
	}

	record.InstanceID = "tampered-instance"
	if err := daemonidentity.Write(paths, record); err != nil {
		t.Fatal(err)
	}
	if _, err := New(paths).inspectDaemon(context.Background()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("identity mismatch error = %v", err)
	}
}

func TestEnsureDaemonKeepsCompatibleOutdatedBuildWhileEnvironmentIsActive(t *testing.T) {
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("installation-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	authManager, err := auth.LoadOrCreate(paths.AuthToken)
	if err != nil {
		t.Fatal(err)
	}
	installationID, err := installation.InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	identity := lifecycle.Identity{
		Product: lifecycle.Product, ProtocolVersion: lifecycle.ProtocolVersion, APIVersion: contract.APIVersion,
		InstallationID: installationID, InstanceID: "active-instance", BuildID: "older-build",
		PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	var shutdowns atomic.Int32
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Next: http.NotFoundHandler(), Auth: authManager, Identity: identity,
		ActiveEnvironments: func(context.Context) ([]string, error) { return []string{"billing/local"}, nil },
		Shutdown:           func() { shutdowns.Add(1) },
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	record := daemonidentity.Record{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.AuthToken, StartedAt: identity.StartedAt,
	}
	if err := daemonidentity.Write(paths, record); err != nil {
		t.Fatal(err)
	}

	actual, err := New(paths).ensureDaemon(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if actual.InstanceID != record.InstanceID || shutdowns.Load() != 0 {
		t.Fatalf("ensureDaemon replaced an active compatible daemon: record=%#v shutdowns=%d", actual, shutdowns.Load())
	}
}
