package installation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
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

func InstallationID(paths Layout) (string, error) {
	key, err := ReadPrivateTextFile(paths.OwnershipKey)
	if err != nil {
		return "", fmt.Errorf("read Portless installation identity: %w", err)
	}
	hash := sha256.Sum256([]byte("portless-installation\x00" + key))
	return hex.EncodeToString(hash[:]), nil
}

func IDFromKey(key string) string {
	hash := sha256.Sum256([]byte("portless-installation\x00" + strings.TrimSpace(key)))
	return hex.EncodeToString(hash[:])
}
