package installation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type artifactBackup struct {
	path       string
	backupPath string
	existed    bool
	preserve   bool
}

type artifactTransaction struct {
	backups      []artifactBackup
	rollbackFunc func() error
	commitFunc   func() error
}

func beginArtifactTransaction(paths ...string) (*artifactTransaction, error) {
	transaction := &artifactTransaction{backups: make([]artifactBackup, 0, len(paths))}
	for _, path := range paths {
		backup, err := backupArtifact(path)
		if err != nil {
			return nil, errors.Join(err, transaction.discard())
		}
		transaction.backups = append(transaction.backups, backup)
	}
	return transaction, nil
}

func backupArtifact(path string) (artifactBackup, error) {
	backup := artifactBackup{path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return backup, nil
	}
	if err != nil {
		return backup, fmt.Errorf("inspect relay artifact before replacement %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return backup, fmt.Errorf("refuse to replace relay artifact %s because it is not a regular file", path)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".portless-relay-backup-*")
	if err != nil {
		return backup, fmt.Errorf("create relay artifact backup for %s: %w", path, err)
	}
	backup.backupPath = temporary.Name()
	if closeErr := temporary.Close(); closeErr != nil {
		_ = os.Remove(backup.backupPath)
		return artifactBackup{}, fmt.Errorf("close relay artifact backup for %s: %w", path, closeErr)
	}
	if err := os.Remove(backup.backupPath); err != nil {
		return artifactBackup{}, fmt.Errorf("prepare relay artifact backup for %s: %w", path, err)
	}
	if err := os.Link(path, backup.backupPath); err != nil {
		return artifactBackup{}, fmt.Errorf("snapshot relay artifact %s: %w", path, err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		_ = os.Remove(backup.backupPath)
		return artifactBackup{}, fmt.Errorf("sync relay artifact backup for %s: %w", path, err)
	}
	backup.existed = true
	return backup, nil
}

func (transaction *artifactTransaction) rollback() error {
	if transaction == nil {
		return nil
	}
	if transaction.rollbackFunc != nil {
		return transaction.rollbackFunc()
	}
	var rollbackErr error
	for index := len(transaction.backups) - 1; index >= 0; index-- {
		backup := &transaction.backups[index]
		if backup.existed {
			if err := os.Rename(backup.backupPath, backup.path); err != nil {
				backup.preserve = true
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore relay artifact %s from preserved backup %s: %w", backup.path, backup.backupPath, err))
				continue
			}
			backup.backupPath = ""
			backup.preserve = false
			if err := syncDirectory(filepath.Dir(backup.path)); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("sync restored relay artifact %s: %w", backup.path, err))
				continue
			}
			continue
		}
		if err := removeExactFile(backup.path); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove newly installed relay artifact %s: %w", backup.path, err))
		}
	}
	rollbackErr = errors.Join(rollbackErr, transaction.discard())
	return rollbackErr
}

func (transaction *artifactTransaction) commit() error {
	if transaction == nil {
		return nil
	}
	if transaction.commitFunc != nil {
		return transaction.commitFunc()
	}
	return transaction.discard()
}

func (transaction *artifactTransaction) discard() error {
	var discardErr error
	for index := range transaction.backups {
		backup := &transaction.backups[index]
		if backup.backupPath == "" || backup.preserve {
			continue
		}
		if err := removeExactFile(backup.backupPath); err != nil {
			discardErr = errors.Join(discardErr, fmt.Errorf("remove relay artifact backup %s: %w", backup.backupPath, err))
			continue
		}
		backup.backupPath = ""
	}
	return discardErr
}
