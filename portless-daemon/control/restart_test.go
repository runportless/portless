package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	daemonidentity "github.com/runportless/portless/portless-daemon/identity"
	"github.com/runportless/portless/portless-daemon/lifecycle"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func TestRestartDaemonUsesReceiptAndWaitsForReadyReplacement(t *testing.T) {
	paths, oldIdentity, oldRecord, currentBuildID := restartFixture(t)
	newIdentity := oldIdentity
	newIdentity.InstanceID = "replacement-instance"
	newIdentity.PID++
	newIdentity.StartedAt = oldIdentity.StartedAt.Add(time.Second)
	newRecord := oldRecord
	newRecord.InstanceID = newIdentity.InstanceID
	newRecord.PID = newIdentity.PID
	newRecord.StartedAt = newIdentity.StartedAt
	restartID := "restart-id"
	currentIdentity := oldIdentity
	var clientKind string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == lifecycle.IdentityPath:
			return jsonHTTPResponse(http.StatusOK, currentIdentity), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/daemon/restart":
			clientKind = request.Header.Get(contract.ClientKindHeader)
			acceptedAt := time.Now().UTC()
			response := contract.DaemonRestart{
				Restarting: true, RestartID: restartID, Reason: "cli",
				PreviousInstanceID: oldIdentity.InstanceID, TargetBuildID: currentBuildID,
				AcceptedAt: acceptedAt, DeadlineAt: acceptedAt.Add(contract.DaemonRestartSLA),
				Handoff: true, ActiveEnvironments: []string{"store/local"},
			}
			currentIdentity = newIdentity
			if err := daemonidentity.Write(paths, newRecord); err != nil {
				t.Fatal(err)
			}
			return jsonHTTPResponse(http.StatusAccepted, response), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	manager := NewWithHooks(paths, Hooks{HTTPClient: func(time.Duration) *http.Client {
		return &http.Client{Transport: transport}
	}})

	result, err := manager.Restart(context.Background(), RestartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Restart.RestartID != restartID || result.Restart.PreviousInstanceID != oldIdentity.InstanceID || result.Daemon.InstanceID != newIdentity.InstanceID || result.Forced {
		t.Fatalf("restart result = %#v", result)
	}
	if clientKind != string(contract.ClientKindCLI) {
		t.Fatalf("restart client kind = %q, want cli", clientKind)
	}
}

func TestEnsureUsesCoordinatedRestartForOutdatedDaemon(t *testing.T) {
	paths, oldIdentity, oldRecord, currentBuildID := restartFixture(t)
	oldIdentity.BuildID = "outdated-build"
	oldIdentity.ActiveEnvironments = []string{}
	oldRecord.BuildID = oldIdentity.BuildID
	if err := daemonidentity.Write(paths, oldRecord); err != nil {
		t.Fatal(err)
	}
	newIdentity := oldIdentity
	newIdentity.InstanceID = "replacement-instance"
	newIdentity.BuildID = currentBuildID
	newIdentity.PID++
	newIdentity.StartedAt = oldIdentity.StartedAt.Add(time.Second)
	newIdentity.State = "ready"
	newRecord := oldRecord
	newRecord.InstanceID = newIdentity.InstanceID
	newRecord.BuildID = newIdentity.BuildID
	newRecord.PID = newIdentity.PID
	newRecord.StartedAt = newIdentity.StartedAt
	newRecord.State = newIdentity.State
	currentIdentity := oldIdentity
	restartRequests := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == lifecycle.IdentityPath:
			return jsonHTTPResponse(http.StatusOK, currentIdentity), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/daemon/restart":
			restartRequests++
			acceptedAt := time.Now().UTC()
			currentIdentity = newIdentity
			if err := daemonidentity.Write(paths, newRecord); err != nil {
				t.Fatal(err)
			}
			return jsonHTTPResponse(http.StatusAccepted, contract.DaemonRestart{
				Restarting: true, RestartID: "automatic-restart", Reason: "cli",
				PreviousInstanceID: oldIdentity.InstanceID, TargetBuildID: currentBuildID,
				AcceptedAt: acceptedAt, DeadlineAt: acceptedAt.Add(contract.DaemonRestartSLA),
				Handoff: true, ActiveEnvironments: []string{},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	starts := 0
	manager := NewWithHooks(paths, Hooks{
		HTTPClient: func(time.Duration) *http.Client { return &http.Client{Transport: transport} },
		StartDaemon: func(installation.Layout) error {
			starts++
			return nil
		},
	})

	record, err := manager.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.InstanceID != newIdentity.InstanceID || restartRequests != 1 || starts != 0 {
		t.Fatalf("ensure result=%#v restartRequests=%d starts=%d", record, restartRequests, starts)
	}
}

func TestEnsureWaitsWhenAutomaticReplacementClosesTheHandoffProbe(t *testing.T) {
	paths, oldIdentity, oldRecord, currentBuildID := restartFixture(t)
	oldIdentity.BuildID = "outdated-build"
	oldRecord.BuildID = oldIdentity.BuildID
	if err := daemonidentity.Write(paths, oldRecord); err != nil {
		t.Fatal(err)
	}
	newIdentity := oldIdentity
	newIdentity.InstanceID = "automatic-replacement-instance"
	newIdentity.BuildID = currentBuildID
	newIdentity.PID++
	newIdentity.StartedAt = oldIdentity.StartedAt.Add(time.Second)
	newRecord := oldRecord
	newRecord.InstanceID = newIdentity.InstanceID
	newRecord.BuildID = newIdentity.BuildID
	newRecord.PID = newIdentity.PID
	newRecord.StartedAt = newIdentity.StartedAt
	currentIdentity := oldIdentity
	restartRequests := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == lifecycle.IdentityPath:
			return jsonHTTPResponse(http.StatusOK, currentIdentity), nil
		case request.Method == http.MethodGet && request.URL.Path == lifecycle.HandoffPath:
			currentIdentity = newIdentity
			if err := daemonidentity.Write(paths, newRecord); err != nil {
				t.Fatal(err)
			}
			return nil, io.EOF
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/daemon/restart":
			restartRequests++
			return nil, io.EOF
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	manager := NewWithHooks(paths, Hooks{HTTPClient: func(time.Duration) *http.Client {
		return &http.Client{Transport: transport}
	}})

	record, err := manager.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if record.InstanceID != newIdentity.InstanceID || restartRequests != 1 {
		t.Fatalf("ensure result=%#v restartRequests=%d", record, restartRequests)
	}
}

func TestRestartDaemonFailsAtSharedReadinessDeadline(t *testing.T) {
	paths, oldIdentity, _, currentBuildID := restartFixture(t)
	now := time.Now()
	waits := 0
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case lifecycle.IdentityPath:
			return jsonHTTPResponse(http.StatusOK, oldIdentity), nil
		case "/api/v1/daemon/restart":
			acceptedAt := now.UTC()
			return jsonHTTPResponse(http.StatusAccepted, contract.DaemonRestart{
				Restarting: true, RestartID: "stuck-restart", Reason: "cli",
				PreviousInstanceID: oldIdentity.InstanceID, TargetBuildID: currentBuildID,
				AcceptedAt: acceptedAt, DeadlineAt: acceptedAt.Add(contract.DaemonRestartSLA),
				Handoff: true, ActiveEnvironments: []string{},
			}), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	manager := NewWithHooks(paths, Hooks{
		Now: func() time.Time { return now },
		Wait: func(context.Context, time.Duration) error {
			waits++
			if waits >= int(contract.DaemonRestartSLA/(100*time.Millisecond)) {
				return context.DeadlineExceeded
			}
			return nil
		},
		HTTPClient: func(time.Duration) *http.Client { return &http.Client{Transport: transport} },
	})

	_, err := manager.Restart(context.Background(), RestartOptions{})
	if err == nil || !strings.Contains(err.Error(), "stuck-restart") || !strings.Contains(err.Error(), "5s readiness SLA") || !strings.Contains(err.Error(), paths.DaemonLog) {
		t.Fatalf("restart deadline error = %v", err)
	}
}

func restartFixture(t *testing.T) (installation.Layout, lifecycle.Identity, daemonidentity.Record, string) {
	t.Helper()
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("installation-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.AuthToken, []byte("auth-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	installationID, err := installation.InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	currentBuildID, err := installation.CurrentBuildID()
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	identity := lifecycle.Identity{
		Product: lifecycle.Product, ProtocolVersion: lifecycle.ProtocolVersion, APIVersion: contract.APIVersion,
		InstallationID: installationID, InstanceID: "original-instance", BuildID: currentBuildID,
		PID: os.Getpid(), StartedAt: startedAt, State: "ready",
		RecoveryProblems: []string{}, ActiveEnvironments: []string{"store/local"},
	}
	record := daemonidentity.Record{
		PID: identity.PID, Port: 43210, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		State: identity.State, RecoveryProblems: []string{}, TokenPath: paths.AuthToken, StartedAt: identity.StartedAt,
	}
	if err := daemonidentity.Write(paths, record); err != nil {
		t.Fatal(err)
	}
	return paths, identity, record, currentBuildID
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonHTTPResponse(status int, value any) *http.Response {
	content, _ := json.Marshal(value)
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(content))),
	}
}
