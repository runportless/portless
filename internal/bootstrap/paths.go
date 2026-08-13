package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Paths struct {
	Root         string
	Database     string
	Control      string
	DaemonLog    string
	Token        string
	OwnershipKey string
	Lock         string
	InstanceLock string
	Ingress      string
	Logs         string
	Temporary    string
}

type ResetStateResult struct {
	Removed []string `json:"removed"`
}

func ensurePrivateDirectory(path string) error {
	if path == "" || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("refusing to prepare a broad private directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory %s: %w", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private path %s must be a real directory, not a symlink or file", path)
	}
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open private directory %s: %w", path, err)
	}
	defer directory.Close()
	openedInfo, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened private directory %s: %w", path, err)
	}
	if !openedInfo.IsDir() || !os.SameFile(info, openedInfo) {
		return fmt.Errorf("private directory %s changed while it was being inspected", path)
	}
	stat, ok := openedInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private directory ownership is unavailable for %s", path)
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("private directory %s belongs to UID %d, expected UID %d", path, stat.Uid, os.Geteuid())
	}
	if err := directory.Chmod(0o700); err != nil {
		return fmt.Errorf("protect private directory %s: %w", path, err)
	}
	verified, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("verify private directory %s: %w", path, err)
	}
	if verified.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory %s has mode %04o after repair", path, verified.Mode().Perm())
	}
	return nil
}

func ResolvePaths(override string) (Paths, error) {
	root := override
	if root == "" {
		root = os.Getenv("PORTLESS_HOME")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		root = filepath.Join(home, ".portless")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, err
	}
	if root == string(filepath.Separator) || root == "" {
		return Paths{}, errors.New("refusing to use a broad Portless data directory")
	}
	return Paths{
		Root: root, Database: filepath.Join(root, "portless.db"), Control: filepath.Join(root, "control.json"),
		DaemonLog: filepath.Join(root, "daemon.log"), Token: filepath.Join(root, "install.key"),
		OwnershipKey: filepath.Join(root, "ownership.key"), Lock: filepath.Join(root, "daemon.lock"),
		InstanceLock: filepath.Join(root, "daemon.instance.lock"),
		Ingress:      filepath.Join(root, "ingress.sock"), Logs: filepath.Join(root, "logs"), Temporary: filepath.Join(root, "tmp"),
	}, nil
}

// ResetApplicationState removes data produced by projects and environments
// while preserving installation-level state such as authentication,
// ownership, CLI preferences, and the selected container runtime. The caller
// must stop the authenticated daemon and remove managed volumes first.
func ResetApplicationState(paths Paths) (ResetStateResult, error) {
	root, err := filepath.Abs(paths.Root)
	if err != nil {
		return ResetStateResult{}, err
	}
	if root == "" || root == string(filepath.Separator) {
		return ResetStateResult{}, errors.New("refusing to reset a broad Portless data directory")
	}
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return ResetStateResult{Removed: []string{}}, nil
	}
	if err != nil {
		return ResetStateResult{}, fmt.Errorf("inspect Portless data directory %s: %w", root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ResetStateResult{}, fmt.Errorf("Portless data directory %s must be a real directory", root)
	}

	result := ResetStateResult{Removed: []string{}}
	targets := []struct {
		name string
		path string
	}{
		{name: "database", path: paths.Database},
		{name: "database-wal", path: paths.Database + "-wal"},
		{name: "database-shm", path: paths.Database + "-shm"},
		{name: "environment-data", path: filepath.Join(root, "environments")},
		{name: "process-state", path: filepath.Join(root, "runs")},
		{name: "generated-secrets", path: filepath.Join(root, "secrets")},
		{name: "logs", path: paths.Logs},
		{name: "temporary-data", path: paths.Temporary},
		{name: "daemon-log", path: paths.DaemonLog},
	}
	for _, target := range targets {
		if err := validateResetTarget(root, target.path); err != nil {
			return result, err
		}
	}
	for _, target := range targets {
		if err := removeResetTarget(root, target.path); err != nil {
			return result, err
		}
		result.Removed = append(result.Removed, target.name)
	}
	return result, nil
}

func removeResetTarget(root, target string) error {
	if err := validateResetTarget(root, target); err != nil {
		return err
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove reset target %s: %w", target, err)
	}
	return nil
}

func validateResetTarget(root, target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == "." || relative == "" || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove reset target outside %s: %s", root, target)
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect reset target %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove reset target symlink %s", target)
	}
	return nil
}
