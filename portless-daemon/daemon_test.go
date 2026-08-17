package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/system/installation"
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
