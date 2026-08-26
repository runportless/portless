package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

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
	replacement := make(chan struct{}, 1)
	go watchExecutable(ctx, executable, buildID, func(context.Context) (bool, []string) {
		return true, nil
	}, replacement)

	// Let the watcher capture the original file identity before replacing it.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(executable, []byte("second-build"), 0o700); err != nil {
		t.Fatal(err)
	}
	select {
	case <-replacement:
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
	}

	result, err := control.Diagnostics(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Build.Version != "1.2.3" || result.Build.Distribution != "release" || result.Build.Commit != "1234567890abcdef" || result.Build.OnDiskBuildID != buildID || !result.Build.Current || result.Storage != nil {
		t.Fatalf("current build diagnostics = %#v", result)
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
