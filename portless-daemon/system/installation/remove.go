package installation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// StateStatus describes whether a validated data directory contains Portless
// installation state. An unrelated existing directory is never removable.
type StateStatus struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
}

// StateRemoval describes a stopped installation state removal.
type StateRemoval struct {
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
}

// InspectState validates the configured installation boundary without
// creating, chmodding, or otherwise mutating it.
func InspectState(layout Layout) (StateStatus, error) {
	root, _, exists, err := validateInstallationRoot(layout)
	if err != nil {
		return StateStatus{}, err
	}
	status := StateStatus{Path: root}
	if !exists {
		return status, nil
	}
	status.Present, err = hasInstallationMarker(layout)
	if err != nil {
		return StateStatus{}, err
	}
	if status.Present {
		if err := validateRemovableInstallationRoot(root); err != nil {
			return StateStatus{}, err
		}
	}
	return status, nil
}

// RemoveStoppedState removes a complete, validated installation root. The
// caller must serialize daemon startup and prove the daemon is stopped first.
func RemoveStoppedState(layout Layout) (StateRemoval, error) {
	status, err := InspectState(layout)
	if err != nil {
		return StateRemoval{}, err
	}
	result := StateRemoval{Path: status.Path}
	if !status.Present {
		return result, nil
	}

	root, before, exists, err := validateInstallationRoot(layout)
	if err != nil {
		return result, err
	}
	if !exists {
		result.Removed = true
		return result, nil
	}
	directory, err := os.Open(root)
	if err != nil {
		return result, fmt.Errorf("open Portless data directory before uninstall: %w", err)
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil {
		return result, fmt.Errorf("verify Portless data directory before uninstall: %w", err)
	}
	if !os.SameFile(before, opened) {
		return result, fmt.Errorf("Portless data directory %s changed while it was being inspected", root)
	}

	tombstone := filepath.Join(filepath.Dir(root), fmt.Sprintf(".%s.portless-uninstall-%d-%d", filepath.Base(root), os.Getpid(), time.Now().UnixNano()))
	if _, err := os.Lstat(tombstone); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return result, fmt.Errorf("temporary uninstall path already exists: %s", tombstone)
		}
		return result, fmt.Errorf("inspect temporary uninstall path: %w", err)
	}
	if err := os.Rename(root, tombstone); err != nil {
		return result, fmt.Errorf("detach Portless data directory for uninstall: %w", err)
	}
	detached, err := os.Lstat(tombstone)
	if err != nil || !os.SameFile(opened, detached) {
		if err == nil {
			err = errors.New("detached directory identity does not match")
		}
		return result, fmt.Errorf("verify detached Portless data directory %s: %w", tombstone, err)
	}
	if err := os.RemoveAll(tombstone); err != nil {
		return result, fmt.Errorf("remove detached Portless data directory %s: %w", tombstone, err)
	}
	result.Removed = true
	return result, nil
}

func validateInstallationRoot(layout Layout) (string, os.FileInfo, bool, error) {
	root, err := filepath.Abs(layout.Root)
	if err != nil {
		return "", nil, false, err
	}
	root = filepath.Clean(root)
	if root == "" || root == string(filepath.Separator) {
		return "", nil, false, errors.New("refusing to remove a broad Portless data directory")
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		home, _ = filepath.Abs(home)
		if filepath.Clean(home) == root {
			return "", nil, false, fmt.Errorf("refusing to remove the user home directory as Portless state: %s", root)
		}
	}
	resolved, err := ResolveLayout(root)
	if err != nil {
		return "", nil, false, err
	}
	if !sameLayout(layout, resolved) {
		return "", nil, false, errors.New("Portless installation paths do not share the configured data-directory boundary")
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return root, nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("inspect Portless data directory %s: %w", root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", nil, false, fmt.Errorf("Portless data directory %s must be a real directory", root)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", nil, false, fmt.Errorf("Portless data directory ownership is unavailable for %s", root)
	}
	if int(stat.Uid) != os.Geteuid() {
		return "", nil, false, fmt.Errorf("Portless data directory %s belongs to UID %d, expected UID %d", root, stat.Uid, os.Geteuid())
	}
	return root, info, true, nil
}

func validateRemovableInstallationRoot(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	base := strings.ToLower(filepath.Base(root))
	if !strings.Contains(base, "portless") {
		return fmt.Errorf("refusing to recursively remove ambiguous custom data directory %s; a full-uninstall data root must have portless in its directory name", root)
	}
	for _, broad := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var", "/private/var", "/usr", "/opt", "/Library", "/Applications", "/Users", "/home"} {
		absolute, absoluteErr := filepath.Abs(broad)
		if absoluteErr == nil && filepath.Clean(absolute) == root {
			return fmt.Errorf("refusing to recursively remove broad data directory %s", root)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil {
		return fmt.Errorf("refusing to remove Portless state because %s is also a Git checkout", root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect data directory Git marker: %w", err)
	}
	checkouts := filepath.Join(root, "worktrees")
	if entries, err := os.ReadDir(checkouts); err == nil && len(entries) > 0 {
		return fmt.Errorf("environment checkouts are retained in %s and may contain local edits; move or remove those checkouts before uninstalling Portless", checkouts)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect retained environment checkouts: %w", err)
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		workingDirectory, _ = filepath.Abs(workingDirectory)
		relative, relativeErr := filepath.Rel(root, workingDirectory)
		if relativeErr == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("refusing to remove Portless data directory %s while the current working directory is inside it", root)
		}
	}
	return nil
}

func sameLayout(left, right Layout) bool {
	return filepath.Clean(left.Root) == filepath.Clean(right.Root) &&
		left.Database == right.Database && left.Control == right.Control && left.DaemonLog == right.DaemonLog &&
		left.AuthToken == right.AuthToken && left.OwnershipKey == right.OwnershipKey && left.StartupLock == right.StartupLock &&
		left.InstanceLock == right.InstanceLock && left.IngressSocket == right.IngressSocket && left.DNSSocket == right.DNSSocket &&
		left.Logs == right.Logs && left.Temporary == right.Temporary
}

func hasInstallationMarker(layout Layout) (bool, error) {
	markers := []string{
		layout.Database, layout.Database + "-wal", layout.Database + "-shm", layout.Control, layout.DaemonLog,
		layout.AuthToken, layout.OwnershipKey, layout.StartupLock, layout.InstanceLock, layout.IngressSocket, layout.DNSSocket,
		layout.Logs, layout.Temporary, filepath.Join(layout.Root, "preferences.json"), filepath.Join(layout.Root, "runtime.json"),
		filepath.Join(layout.Root, "browser-sessions.json"), filepath.Join(layout.Root, "environments"),
		filepath.Join(layout.Root, "runs"), filepath.Join(layout.Root, "secrets"),
	}
	for _, marker := range markers {
		if _, err := os.Lstat(marker); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect Portless installation marker %s: %w", marker, err)
		}
	}
	return false, nil
}
