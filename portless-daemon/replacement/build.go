// Package replacement owns bounded daemon replacement trials, retained build
// images, and the exclusive process lease shared across a trial. It does not
// compose the daemon, access its database, or control application runtimes.
package replacement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/system/installation"
)

// SourceExecutable returns the invocation path without resolving a package
// manager's symlink, so later upgrades at that path remain visible.
func SourceExecutable() (string, error) {
	path, err := exec.LookPath(os.Args[0])
	if err != nil {
		path, err = os.Executable()
	}
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// BuildPath locates one private content-addressed executable image.
func BuildPath(layout installation.Layout, buildID string) (string, error) {
	decoded, err := hex.DecodeString(buildID)
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != buildID {
		return "", errors.New("invalid retained daemon build identity")
	}
	return filepath.Join(layout.Root, "daemon-builds", buildID, "portless"), nil
}

// StageBuild copies and verifies an executable before it can be used by a
// daemon or a trial. Published build images are never overwritten.
func StageBuild(ctx context.Context, layout installation.Layout, source, expected string) (string, error) {
	destination, err := BuildPath(layout, expected)
	if err != nil {
		return "", err
	}
	for _, dir := range []string{layout.Root, filepath.Dir(filepath.Dir(destination)), filepath.Dir(destination)} {
		if err := installation.EnsurePrivateDirectory(dir); err != nil {
			return "", err
		}
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := verifyBuild(destination, expected); err != nil {
			return "", err
		}
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open replacement build: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("replacement build is not a regular executable")
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".stage-")
	if err != nil {
		return "", err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			if _, err := io.MultiWriter(output, hash).Write(buffer[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return "", errors.New("replacement executable changed while it was staged")
	}
	if err := output.Chmod(0o700); err != nil {
		return "", err
	}
	if err := output.Sync(); err != nil {
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	// A concurrent launcher may stage the same image. Linking cannot overwrite it.
	if err := os.Link(output.Name(), destination); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	if err := verifyBuild(destination, expected); err != nil {
		return "", err
	}
	return destination, syncDirectory(filepath.Dir(destination))
}

func verifyBuild(path, expected string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 {
		return errors.New("retained daemon image must be a private executable file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("retained daemon image ownership could not be verified")
	}
	actual, err := installation.BuildIDForPath(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("retained daemon image failed its integrity check")
	}
	return nil
}

// PruneBuilds retains only the working and trial images. Only verified private
// build directories with the exact owned file layout are eligible for removal.
func PruneBuilds(ctx context.Context, layout installation.Layout, keep ...string) error {
	root := filepath.Join(layout.Root, "daemon-builds")
	if err := installation.EnsurePrivateDirectory(root); err != nil {
		return err
	}
	directory, err := os.Open(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	for {
		entries, readErr := directory.ReadDir(64)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			retained := false
			for _, id := range keep {
				if id == entry.Name() {
					retained = true
				}
			}
			if retained || !entry.IsDir() {
				continue
			}
			path, err := BuildPath(layout, entry.Name())
			if err != nil {
				continue
			}
			if err := verifyBuild(path, entry.Name()); err != nil {
				continue
			}
			contents, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(contents) != 1 || contents[0].Name() != "portless" {
				continue
			}
			info, err := os.Lstat(filepath.Dir(path))
			if err != nil {
				continue
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0o700 {
				continue
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			if err := os.Remove(filepath.Dir(path)); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return syncDirectory(root)
		}
		if readErr != nil {
			return readErr
		}
	}
}

// Recovery records the rejected build and the previous working image. Paths,
// credentials, and application ownership keys are never part of this record.
type Recovery struct {
	BuildID string                       `json:"buildId"`
	Restart contract.DaemonRestartStatus `json:"restart"`
}

// ReadRecovery reads a private, validated failed-build quarantine record.
func ReadRecovery(layout installation.Layout) (*Recovery, error) {
	content, err := installation.ReadPrivateTextFile(filepath.Join(layout.Root, "daemon-recovery.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recovery Recovery
	if err := json.Unmarshal([]byte(content), &recovery); err != nil {
		return nil, err
	}
	if _, err := BuildPath(layout, recovery.BuildID); err != nil {
		return nil, err
	}
	if _, err := BuildPath(layout, recovery.Restart.TargetBuildID); err != nil {
		return nil, err
	}
	if recovery.Restart.Outcome != "rolled-back" || recovery.Restart.RestartID == "" {
		return nil, errors.New("invalid daemon recovery record")
	}
	return &recovery, nil
}

// WriteRecovery atomically persists a failed-build quarantine before recovery.
func WriteRecovery(layout installation.Layout, recovery Recovery) error {
	data, err := json.Marshal(recovery)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(layout.Root, ".daemon-recovery-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), filepath.Join(layout.Root, "daemon-recovery.json")); err != nil {
		return err
	}
	return syncDirectory(layout.Root)
}

// ClearRecovery removes the previous quarantine after a committed replacement.
func ClearRecovery(layout installation.Layout) error {
	if err := os.Remove(filepath.Join(layout.Root, "daemon-recovery.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(layout.Root)
}

// LaunchBuild chooses a verified working image when this exact installed build
// previously failed. A different installed build is eligible for a new trial.
func LaunchBuild(ctx context.Context, layout installation.Layout, source, buildID string) (string, error) {
	recovery, err := ReadRecovery(layout)
	if err != nil {
		return "", fmt.Errorf("read daemon recovery state: %w", err)
	}
	if recovery != nil && recovery.Restart.TargetBuildID == buildID {
		path, err := BuildPath(layout, recovery.BuildID)
		if err != nil {
			return "", err
		}
		return path, verifyBuild(path, recovery.BuildID)
	}
	return StageBuild(ctx, layout, source, buildID)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
