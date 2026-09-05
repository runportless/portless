package installation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLayoutKeepsEveryPathUnderRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "portless-state")
	layout, err := ResolveLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"database": layout.Database, "control": layout.Control, "token": layout.AuthToken,
		"startup lock": layout.StartupLock, "instance lock": layout.InstanceLock,
		"ingress socket": layout.IngressSocket, "DNS socket": layout.DNSSocket,
		"logs": layout.Logs, "temporary": layout.Temporary,
	} {
		relative, err := filepath.Rel(layout.Root, path)
		if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("%s path %q escapes root %q", name, path, layout.Root)
		}
	}
}

func TestEnsurePrivateDirectoryRejectsSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "portless-private")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDirectory(link); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("unexpected symlink error: %v", err)
	}
}

func TestResetApplicationStatePreservesInstallationIdentity(t *testing.T) {
	layout, err := ResolveLayout(filepath.Join(t.TempDir(), "portless-reset"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Logs, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		layout.Database: "database", layout.AuthToken: "token", layout.OwnershipKey: "identity",
		filepath.Join(layout.Root, "preferences.json"): "preferences", filepath.Join(layout.Root, "runtime.json"): "runtime",
		filepath.Join(layout.Logs, "service.log"): "log",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResetApplicationState(layout); err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{layout.AuthToken, layout.OwnershipKey, filepath.Join(layout.Root, "preferences.json"), filepath.Join(layout.Root, "runtime.json")} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("preserved file %s: %v", preserved, err)
		}
	}
	for _, removed := range []string{layout.Database, layout.Logs} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reset target %s remains: %v", removed, err)
		}
	}
}

func TestRemoveStoppedStateRequiresAndRemovesVerifiedInstallation(t *testing.T) {
	layout, err := ResolveLayout(filepath.Join(t.TempDir(), "portless-remove"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.AuthToken, []byte("marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := InspectState(layout)
	if err != nil || !status.Present {
		t.Fatalf("status = %#v, err = %v", status, err)
	}
	result, err := RemoveStoppedState(layout)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Removed {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Lstat(layout.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installation root remains: %v", err)
	}
}

func TestInspectStateRejectsAmbiguousRemovalRoot(t *testing.T) {
	layout, err := ResolveLayout(filepath.Join(t.TempDir(), "application-data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.AuthToken, []byte("marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectState(layout); err == nil || !strings.Contains(err.Error(), "must have portless") {
		t.Fatalf("unexpected ambiguous-root error: %v", err)
	}
}

func TestEnvironmentCheckoutsSurviveResetAndBlockRecursiveUninstall(t *testing.T) {
	layout, err := ResolveLayout(filepath.Join(t.TempDir(), "portless-state"))
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(layout.Root, "worktrees", "store-qa", "local-edit.txt")
	if err := os.MkdirAll(filepath.Dir(checkout), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{layout.AuthToken, checkout} {
		if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResetApplicationState(layout); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveStoppedState(layout); err == nil || !strings.Contains(err.Error(), "environment checkouts are retained") {
		t.Fatalf("uninstall would erase environment checkouts: %v", err)
	}
	if content, err := os.ReadFile(checkout); err != nil || string(content) != "preserve" {
		t.Fatalf("checkout edits were lost: %q, %v", content, err)
	}
}
