package installation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ResetStateResult struct {
	Removed []string `json:"removed"`
}

// ResetApplicationState removes data produced by projects and environments
// while preserving installation-level state such as authentication,
// ownership, CLI preferences, and the selected container runtime. The caller
// must stop the authenticated daemon and remove managed volumes first.
func ResetApplicationState(paths Layout) (ResetStateResult, error) {
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
