package daemonlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReaderReturnsBoundedCompleteLinesAndRedactsInstallationSecrets(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "daemon.log")
	authToken := "auth-token-value"
	ownershipKey := "ownership-key-value"
	tail := "keep " + authToken + "\nlast " + ownershipKey + "\n"
	content := strings.Repeat("x", 32) + "\n" + tail
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	reader := newReader(path, int64(len(tail)+4), authToken, ownershipKey)

	snapshot, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Truncated {
		t.Fatal("snapshot was not marked truncated")
	}
	if snapshot.Content != "keep [REDACTED]\nlast [REDACTED]\n" {
		t.Fatalf("content = %q", snapshot.Content)
	}
	if strings.Contains(snapshot.Content, authToken) || strings.Contains(snapshot.Content, ownershipKey) {
		t.Fatalf("snapshot exposed an installation secret: %q", snapshot.Content)
	}
}

func TestReaderTreatsAMissingLogAsEmpty(t *testing.T) {
	reader := NewReader(filepath.Join(t.TempDir(), "daemon.log"))
	snapshot, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "" || snapshot.Truncated {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestReaderRejectsUnsafeFilesAndCanceledReads(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.log")
	if err := os.WriteFile(target, []byte("private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "daemon.log")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewReader(symlink).Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewReader(target).Snapshot(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
