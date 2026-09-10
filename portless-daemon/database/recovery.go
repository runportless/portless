package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// SnapshotForReplacement makes a consistent SQLite snapshot including any WAL
// contents, without migrating the source. The caller holds the daemon process
// lease and has closed the old control plane before calling it.
func SnapshotForReplacement(ctx context.Context, path, snapshot string) error {
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return fmt.Errorf("snapshot daemon state: %w", err)
	}
	file, err := os.OpenFile(snapshot, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	return file.Sync()
}

// RestoreAfterReplacement restores a snapshot only after the trial process has
// been reaped and all SQLite handles are closed. Application data and runtime
// ownership evidence outside the daemon database are never changed.
func RestoreAfterReplacement(path, snapshot string) error {
	info, err := os.Lstat(snapshot)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("invalid daemon recovery snapshot")
	}
	// Reject unexpected objects before removing any SQLite sidecars.
	for _, target := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("daemon database recovery encountered a non-regular file")
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(snapshot, path); err != nil {
		return fmt.Errorf("restore previous daemon state: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
