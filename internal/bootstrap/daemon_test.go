package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
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
