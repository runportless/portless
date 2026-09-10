package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReplacementSnapshotRestoresSchemaAndCommittedWAL(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path, snapshot := filepath.Join(directory, "state.db"), filepath.Join(directory, "backup.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(ctx, "CREATE TABLE recovery_evidence(value TEXT); INSERT INTO recovery_evidence VALUES('preserve me')"); err != nil {
		t.Fatal(err)
	}
	// The writer is deliberately open: a file-only copy would omit WAL data.
	if err := SnapshotForReplacement(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "DROP TABLE recovery_evidence; CREATE TABLE candidate_only(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreAfterReplacement(path, snapshot); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.DB().QueryRowContext(ctx, "SELECT value FROM recovery_evidence").Scan(&value); err != nil || value != "preserve me" {
		t.Fatalf("restored evidence = %q, %v", value, err)
	}
	if _, err := restored.DB().ExecContext(ctx, "SELECT * FROM candidate_only"); err == nil {
		t.Fatal("candidate schema survived rollback")
	}
}

func TestReplacementRestoreRefusesUnexpectedSidecar(t *testing.T) {
	directory := t.TempDir()
	path, snapshot := filepath.Join(directory, "state.db"), filepath.Join(directory, "backup.db")
	if err := os.WriteFile(snapshot, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "unowned")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path+"-wal"); err != nil {
		t.Fatal(err)
	}
	if err := RestoreAfterReplacement(path, snapshot); err == nil {
		t.Fatal("unexpected sidecar accepted")
	}
	content, err := os.ReadFile(victim)
	if err != nil || string(content) != "keep" {
		t.Fatalf("unowned file changed: %q %v", content, err)
	}
}
