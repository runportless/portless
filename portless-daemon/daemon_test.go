package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

func TestShutdownHTTPServersCancelsLongLivedRequestsWithinDrainBudget(t *testing.T) {
	requestStarted := make(chan struct{})
	requestStopped := make(chan struct{})
	serverContext, stopServing := context.WithCancel(context.Background())
	server := &http.Server{
		BaseContext: func(net.Listener) context.Context { return serverContext },
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			close(requestStarted)
			<-request.Context().Done()
			close(requestStopped)
		}),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	response, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("long-lived request did not start")
	}

	startedAt := time.Now()
	if err := shutdownHTTPServers(stopServing, 250*time.Millisecond, server); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(startedAt); elapsed >= 250*time.Millisecond {
		t.Fatalf("long-lived request consumed the full drain budget: %s", elapsed)
	}
	select {
	case <-requestStopped:
	case <-time.After(time.Second):
		t.Fatal("server shutdown did not cancel the request context")
	}
	if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve returned %v, want http.ErrServerClosed", err)
	}
}

func TestReplacementCoordinatorCoalescesAndCommitsOnce(t *testing.T) {
	coordinator := newReplacementCoordinator()
	acceptedAt := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	first, err := coordinator.prepare("cli", "old-instance", "new-build", acceptedAt, []string{"store/local"}, ErrRestartRequested)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.prepare("browser", "different-instance", "different-build", acceptedAt.Add(time.Second), nil, ErrExecutableChanged)
	if err != nil {
		t.Fatal(err)
	}
	if second.RestartID != first.RestartID || second.Reason != first.Reason || second.PreviousInstanceID != first.PreviousInstanceID {
		t.Fatalf("concurrent prepare did not reuse receipt: first=%#v second=%#v", first, second)
	}
	if !first.DeadlineAt.Equal(first.AcceptedAt.Add(contract.DaemonRestartSLA)) {
		t.Fatalf("restart deadline = %s, want accepted + %s", first.DeadlineAt, contract.DaemonRestartSLA)
	}
	if coordinator.commit("wrong-restart") {
		t.Fatal("coordinator committed a different restart")
	}
	if !coordinator.commit(first.RestartID) || coordinator.commit(first.RestartID) {
		t.Fatal("coordinator did not commit exactly once")
	}
	select {
	case request := <-coordinator.requests:
		if request.receipt.RestartID != first.RestartID || !errors.Is(request.cause, ErrRestartRequested) {
			t.Fatalf("replacement request = %#v, %v", request.receipt, request.cause)
		}
	case <-time.After(time.Second):
		t.Fatal("committed replacement was not delivered")
	}
}

func TestRestartReceiptEnvironmentRoundTrip(t *testing.T) {
	acceptedAt := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	receipt := contract.DaemonRestart{
		Restarting: true, RestartID: "restart-id", Reason: "cli",
		PreviousInstanceID: "old-instance", TargetBuildID: "new-build",
		AcceptedAt: acceptedAt, DeadlineAt: acceptedAt.Add(contract.DaemonRestartSLA),
		Handoff: true, ActiveEnvironments: []string{"store/local"},
	}
	environment, err := environmentWithRestartReceipt([]string{"PATH=/bin", daemonRestartReceiptEnvironment + "=stale"}, receipt)
	if err != nil {
		t.Fatal(err)
	}
	encoded := ""
	for _, value := range environment {
		if strings.HasPrefix(value, daemonRestartReceiptEnvironment+"=") {
			if encoded != "" {
				t.Fatal("restart receipt environment was duplicated")
			}
			encoded = strings.TrimPrefix(value, daemonRestartReceiptEnvironment+"=")
		}
	}
	if encoded == "" {
		t.Fatal("restart receipt environment is missing")
	}
	t.Setenv(daemonRestartReceiptEnvironment, encoded)
	decoded, err := restartReceiptFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RestartID != receipt.RestartID || decoded.Reason != receipt.Reason || len(decoded.ActiveEnvironments) != 1 {
		t.Fatalf("decoded restart receipt = %#v", decoded)
	}
}

func TestListenIngressCreatesPrivateUnixSocket(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "portless-daemon-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "ingress.sock")
	listener, err := listenIngress(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("ingress mode is %v, expected a mode-0600 socket", info.Mode())
	}
}

func TestListenIngressRefusesToReplaceFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingress.sock")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenIngress(path); err == nil {
		t.Fatal("listenIngress replaced a non-socket path")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "keep me" {
		t.Fatalf("existing file changed: content=%q err=%v", content, err)
	}
}

func TestExecutableWatcherRequestsSafeReplacement(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "portless")
	if err := os.WriteFile(executable, []byte("first-build"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildID, err := installation.BuildIDForPath(executable)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacement := make(chan string, 1)
	go watchExecutable(ctx, executable, buildID, func(context.Context) (bool, []string) {
		return true, nil
	}, func(targetBuildID string) { replacement <- targetBuildID })

	// Let the watcher capture the original file identity before replacing it.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(executable, []byte("second-build"), 0o700); err != nil {
		t.Fatal(err)
	}
	select {
	case targetBuildID := <-replacement:
		if targetBuildID == "" || targetBuildID == buildID {
			t.Fatalf("replacement build ID = %q, want updated build", targetBuildID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("updated executable did not request a safe daemon replacement")
	}
}

func TestDaemonDiagnosticsReportsLinkedBuildAndExecutableCurrency(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	app := controlplane.New(controlStore, events.NewBroker(), controlplane.Config{DataDirectory: data, InstallationKey: "test"})
	defer app.Close(ctx)
	executable := filepath.Join(data, "portless")
	if err := os.WriteFile(executable, []byte("first-build"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildID, err := installation.BuildIDForPath(executable)
	if err != nil {
		t.Fatal(err)
	}
	control := lifecycleAPIControl{
		app: app, executable: executable, runningBuildID: buildID,
		build: BuildInfo{Version: "1.2.3", Distribution: "release", Commit: "1234567890abcdef"},
		lastRestart: &contract.DaemonRestartStatus{
			RestartID: "restart-id", Reason: "cli", PreviousInstanceID: "previous-instance",
			InstanceID: "current-instance", TargetBuildID: buildID, DurationMS: 731, WithinSLA: true,
		},
	}

	result, err := control.Diagnostics(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Build.Version != "1.2.3" || result.Build.Distribution != "release" || result.Build.Commit != "1234567890abcdef" || result.Build.OnDiskBuildID != buildID || !result.Build.Current || result.Storage != nil || result.LastRestart == nil || result.LastRestart.DurationMS != 731 {
		t.Fatalf("current build diagnostics = %#v", result)
	}
	result.LastRestart.DurationMS = 999
	unchanged, err := control.Diagnostics(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.LastRestart == nil || unchanged.LastRestart.DurationMS != 731 {
		t.Fatalf("diagnostics exposed mutable restart status: %#v", unchanged.LastRestart)
	}
	if err := os.WriteFile(executable, []byte("replacement-build"), 0o700); err != nil {
		t.Fatal(err)
	}
	replaced, err := control.Diagnostics(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Build.Current || replaced.Build.OnDiskBuildID == buildID || replaced.Storage == nil {
		t.Fatalf("replacement build diagnostics = %#v", replaced)
	}
}
