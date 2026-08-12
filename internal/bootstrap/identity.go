package bootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
)

var (
	buildIDOnce sync.Once
	buildID     string
	buildIDErr  error
)

func CurrentBuildID() (string, error) {
	buildIDOnce.Do(func() {
		executable, err := os.Executable()
		if err != nil {
			buildIDErr = fmt.Errorf("locate current Portless executable: %w", err)
			return
		}
		buildID, buildIDErr = BuildIDForPath(executable)
	})
	return buildID, buildIDErr
}

// BuildIDForPath fingerprints the executable currently present at path. It is
// intentionally uncached so a running daemon can notice an atomic rebuild or
// upgrade that replaced its executable on disk.
func BuildIDForPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open Portless executable: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("fingerprint Portless executable: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func InstallationID(paths Paths) (string, error) {
	key, err := readPrivateTextFile(paths.OwnershipKey)
	if err != nil {
		return "", fmt.Errorf("read Portless installation identity: %w", err)
	}
	hash := sha256.Sum256([]byte("portless-installation\x00" + key))
	return hex.EncodeToString(hash[:]), nil
}

func installationIDFromKey(key string) string {
	hash := sha256.Sum256([]byte("portless-installation\x00" + strings.TrimSpace(key)))
	return hex.EncodeToString(hash[:])
}

func newInstanceID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create daemon instance identity: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func readPrivateTextFile(path string) (string, error) {
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
