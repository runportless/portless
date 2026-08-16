package control

import (
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
	"syscall"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/daemon/instance"
	"github.com/portless-run/portless/internal/daemon/lifecycle"
	"github.com/portless-run/portless/internal/installation"
)

func TestEnsurePrivateDirectoryCreatesAndRepairsMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installation.EnsurePrivateDirectory(root); err != nil {
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
	if err := installation.EnsurePrivateDirectory(created); err != nil {
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
	if err := installation.EnsurePrivateDirectory(link); err == nil || !strings.Contains(err.Error(), "not a symlink") {
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
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.AuthToken, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OwnershipKey, []byte("test-ownership-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	installationID, err := installation.InstallationID(paths)
	if err != nil {
		t.Fatal(err)
	}
	buildID, err := installation.CurrentBuildID()
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	identity := lifecycle.Identity{
		Product: lifecycle.Product, ProtocolVersion: lifecycle.ProtocolVersion, APIVersion: contract.APIVersion,
		InstallationID: installationID, InstanceID: "test-instance", BuildID: buildID,
		PID: os.Getpid(), StartedAt: startedAt, ActiveEnvironments: []string{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != lifecycle.IdentityPath || request.Header.Get("Authorization") != "Bearer test-token" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(identity)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	record := instance.Record{
		PID: identity.PID, Port: port, ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.AuthToken, StartedAt: identity.StartedAt,
	}
	content, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Control, content, 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := New(paths).ensureDaemon(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if actual.Port != port {
		t.Fatalf("ensureDaemon returned port %d, want %d", actual.Port, port)
	}
	info, err := os.Stat(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("healthy daemon data directory mode = %04o, want 0700", info.Mode().Perm())
	}
}

func TestResetApplicationStateRemovesProjectDataAndPreservesInstallationState(t *testing.T) {
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removed := []string{
		paths.Database, paths.Database + "-wal", paths.Database + "-shm", paths.DaemonLog,
		filepath.Join(paths.Root, "environments", "private", "logs", "checkout.log"),
		filepath.Join(paths.Root, "runs", "private", "state.json"),
		filepath.Join(paths.Root, "secrets", "private", "postgres.env"),
		filepath.Join(paths.Logs, "lifecycle.jsonl"), filepath.Join(paths.Temporary, "request.json"),
	}
	for _, path := range removed {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("application-state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	preserved := []string{
		paths.AuthToken, paths.OwnershipKey, filepath.Join(paths.Root, "preferences.json"),
		filepath.Join(paths.Root, "runtime.json"), filepath.Join(paths.Root, "browser-sessions.json"),
	}
	for _, path := range preserved {
		if err := os.WriteFile(path, []byte("installation-state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result, err := installation.ResetApplicationState(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) == 0 {
		t.Fatal("reset did not report removed state categories")
	}
	for _, path := range removed {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("reset target still exists: %s (%v)", path, err)
		}
	}
	for _, path := range preserved {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("preserved path is missing: %s (%v)", path, err)
			continue
		}
		if string(content) != "installation-state" {
			t.Errorf("preserved path changed: %s", path)
		}
	}
}

func TestResetApplicationStateRejectsSymlinkBeforeRemovingAnything(t *testing.T) {
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Database, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	marker := filepath.Join(target, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(paths.Root, "environments")); err != nil {
		t.Fatal(err)
	}
	if _, err := installation.ResetApplicationState(paths); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("unexpected reset symlink error: %v", err)
	}
	if content, err := os.ReadFile(paths.Database); err != nil || string(content) != "database" {
		t.Fatalf("database was removed before reset preflight completed: content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("symlink target changed: content=%q err=%v", content, err)
	}
}

func TestResetRefusesMissingControlRecordWhileDaemonInstanceLockIsHeld(t *testing.T) {
	paths, err := installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(paths.InstanceLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := New(paths).prepareStoppedDaemonForReset(context.Background(), os.ErrNotExist); err == nil || !strings.Contains(err.Error(), "instance lock is still held") {
		t.Fatalf("unexpected missing-control reset error: %v", err)
	}
}

func TestForcedResetPropagatesForceToActiveDaemonShutdown(t *testing.T) {
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
	buildID, err := installation.CurrentBuildID()
	if err != nil {
		t.Fatal(err)
	}
	identity := lifecycle.Identity{
		Product: lifecycle.Product, ProtocolVersion: lifecycle.ProtocolVersion, APIVersion: contract.APIVersion,
		InstallationID: installationID, InstanceID: "active-reset-instance", BuildID: buildID,
		PID: os.Getpid(), StartedAt: time.Now().UTC(),
	}
	var shutdowns atomic.Int32
	handler := lifecycle.NewHandler(lifecycle.HandlerConfig{
		Next: http.NotFoundHandler(), Auth: authManager, Identity: identity,
		ActiveEnvironments: func(context.Context) ([]string, error) { return []string{"store/local"}, nil },
		Shutdown: func() {
			shutdowns.Add(1)
			_ = os.Remove(paths.Control)
		},
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	record := instance.Record{
		PID: identity.PID, Port: server.Listener.Addr().(*net.TCPAddr).Port,
		ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
		InstallationID: identity.InstallationID, InstanceID: identity.InstanceID, BuildID: identity.BuildID,
		TokenPath: paths.AuthToken, StartedAt: identity.StartedAt,
	}
	if err := instance.Write(paths, record); err != nil {
		t.Fatal(err)
	}
	// Stop after the authenticated shutdown. The reset preflight error keeps this
	// test from spawning a replacement daemon while still proving that an active
	// daemon accepted the forced shutdown request.
	if err := os.Symlink(t.TempDir(), filepath.Join(paths.Root, "environments")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := New(paths).resetApplicationState(context.Background(), true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("forced reset did not reach application-state preflight: %v", err)
	}
	if shutdowns.Load() != 1 {
		t.Fatalf("active daemon shutdowns = %d, want 1", shutdowns.Load())
	}
}

func TestInspectInstallationStateDoesNotTreatAnEmptyDirectoryAsPortlessState(t *testing.T) {
	root := t.TempDir()
	paths, err := installation.ResolveLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := installation.InspectState(paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.Present {
		t.Fatalf("empty directory was classified as Portless state: %#v", status)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("inspection changed the directory: %v", err)
	}
}

func TestRemoveInstallationStateDeletesCompleteRootWithoutRestartingDaemon(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".portless-test")
	paths, err := installation.ResolveLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		paths.AuthToken:                         "token",
		paths.OwnershipKey:                      "ownership",
		filepath.Join(root, "preferences.json"): `{"color":"never"}`,
		filepath.Join(root, "logs", "app.log"):  "log",
		filepath.Join(root, "future-state"):     "also-owned-by-the-dedicated-root",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result, err := New(paths).RemoveInstallationState(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed || result.Path != root {
		t.Fatalf("unexpected uninstall result: %#v", result)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installation root still exists: %v", err)
	}
	if _, err := os.Lstat(paths.Control); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall started a replacement daemon: %v", err)
	}
}

func TestRemoveInstallationStateIsIdempotentWhenRootIsAbsent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	paths, err := installation.ResolveLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := New(paths).RemoveInstallationState(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed || result.Path != root {
		t.Fatalf("unexpected absent uninstall result: %#v", result)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent uninstall created state: %v", err)
	}
}

func TestInspectInstallationStateRejectsSymlinkAndMismatchedPaths(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, ".portless")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	paths, err := installation.ResolveLayout(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installation.InspectState(paths); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("unexpected symlink error: %v", err)
	}

	paths, err = installation.ResolveLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths.Database = filepath.Join(t.TempDir(), "outside.db")
	if _, err := installation.InspectState(paths); err == nil || !strings.Contains(err.Error(), "do not share") {
		t.Fatalf("unexpected mismatched path error: %v", err)
	}
}

func TestInspectInstallationStateRejectsHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	paths, err := installation.ResolveLayout(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installation.InspectState(paths); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("unexpected home-directory safety error: %v", err)
	}
}

func TestRemoveInstallationStateRejectsAmbiguousCustomRootAndGitCheckout(t *testing.T) {
	for _, test := range []struct {
		name     string
		rootName string
		git      bool
		want     string
	}{
		{name: "ambiguous", rootName: "application-data", want: "must have portless"},
		{name: "git checkout", rootName: "portless-source", git: true, want: "Git checkout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), test.rootName)
			paths, err := installation.ResolveLayout(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.AuthToken, []byte("marker"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.git {
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := New(paths).RemoveInstallationState(context.Background(), true); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("unexpected destructive-root safety error: %v", err)
			}
			if content, err := os.ReadFile(paths.AuthToken); err != nil || string(content) != "marker" {
				t.Fatalf("state changed before destructive-root rejection: content=%q err=%v", content, err)
			}
		})
	}
}
