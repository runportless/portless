package bootstrap

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/daemon"
)

func TestEnsurePrivateDirectoryCreatesAndRepairsMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("private directory mode = %04o, want 0700", info.Mode().Perm())
	}

	created := filepath.Join(root, "nested", "private")
	if err := ensurePrivateDirectory(created); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(created)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("new private directory mode = %04o, want 0700", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirectoryRejectsSymlinkWithoutChangingTarget(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "private")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(link); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("unexpected symlink error: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target mode changed to %04o", info.Mode().Perm())
	}
}

func TestEnsureDaemonRepairsDataDirectoryBeforeReturningHealthyRecord(t *testing.T) {
	paths, err := ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Token, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("test-ownership-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	installationID, err := InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	buildID, err := CurrentBuildID()
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	identity := daemon.Identity{
		Product: daemon.Product, ProtocolVersion: daemon.ProtocolVersion, APIVersion: api.APIVersion,
		InstallationID: installationID, InstanceID: "test-instance", BuildID: buildID,
		PID: os.Getpid(), StartedAt: startedAt, ActiveEnvironments: []string{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != daemon.IdentityPath || request.Header.Get("Authorization") != "Bearer test-token" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(identity)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	record := ControlRecord{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.Token, StartedAt: identity.StartedAt,
	}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Control, content, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := EnsureDaemon(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Port != port {
		t.Fatalf("EnsureDaemon returned port %d, want %d", actual.Port, port)
	}
	info, err := os.Stat(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("healthy daemon data directory mode = %04o, want 0700", info.Mode().Perm())
	}
}
