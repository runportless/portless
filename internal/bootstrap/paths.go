package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
