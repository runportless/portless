package installation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// EnsurePrivateDirectory securely creates or validates a real, user-owned
// directory and enforces mode 0700.
func EnsurePrivateDirectory(path string) error {
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

// ReadPrivateTextFile reads a nonempty, user-owned regular file that grants no
// group or other access.
func ReadPrivateTextFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("permissions %04o allow group or other access", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("file ownership is unavailable")
	}
	if int(stat.Uid) != os.Geteuid() {
		return "", fmt.Errorf("file belongs to UID %d, expected UID %d", stat.Uid, os.Geteuid())
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", errors.New("file changed while it was being inspected")
	}
	content, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(content))
	if value == "" {
		return "", errors.New("file is empty")
	}
	return value, nil
}
