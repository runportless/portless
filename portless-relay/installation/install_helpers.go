package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

type commandRunner interface {
	combinedOutput(context.Context, string, ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) combinedOutput(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, arguments...).CombinedOutput()
}

type commandExitCoder interface {
	ExitCode() int
}

func commandExitCode(err error) (int, bool) {
	var exitError commandExitCoder
	if !errors.As(err, &exitError) {
		return 0, false
	}
	return exitError.ExitCode(), true
}

func removeExactFile(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func removeDirectoryIfEmpty(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err == nil {
		_ = syncDirectory(filepath.Dir(path))
	}
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validateSetupRequest(request SetupRequest) error {
	if request.UID <= 0 || request.GID <= 0 {
		return errors.New("the localhost relay must run as a non-root user and group")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	return relayruntime.ValidateIdentity(relayruntime.Identity{
		TargetSocket: request.TargetSocket, DNSTargetSocket: request.DNSTargetSocket,
		UID: request.UID, GID: request.GID,
	})
}

func validateExecutable(executable string) error {
	if !filepath.IsAbs(executable) {
		return errors.New("Portless executable path must be absolute")
	}
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("inspect Portless executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("Portless executable must be a regular executable file")
	}
	return nil
}

func copyExecutableAtomically(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Portless executable source is not a regular file")
	}
	if info.Size() > 512<<20 {
		return errors.New("Portless executable is unexpectedly larger than 512 MiB")
	}
	if err := ensureRootArtifactDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".portless-relay-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o755); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chown(0, 0); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func writeRootFileAtomically(destination string, content []byte, mode os.FileMode) error {
	if err := ensureRootArtifactDirectory(filepath.Dir(destination)); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".portless-config-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chown(0, 0); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}

func ensureRootArtifactDirectory(path string) error {
	return ensureArtifactDirectory(path, 0, 0)
}

func ensureArtifactDirectory(path string, expectedUID, expectedGID int) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("relay artifact directory %s must be a real directory", path)
	}
	uid, gid, ok := artifactOwner(info)
	if !ok {
		return fmt.Errorf("relay artifact directory ownership is unavailable for %s", path)
	}
	if uid != expectedUID || gid != expectedGID {
		return fmt.Errorf("relay artifact directory %s belongs to UID %d and GID %d, expected UID %d and GID %d", path, uid, gid, expectedUID, expectedGID)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("relay artifact directory %s is writable by group or other users", path)
	}
	return nil
}

func runHostCommand(ctx context.Context, runner commandRunner, executable string, arguments ...string) error {
	output, err := runner.combinedOutput(ctx, executable, arguments...)
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s failed: %w", filepath.Base(executable), err)
	}
	return fmt.Errorf("%s failed: %w: %s", filepath.Base(executable), err, detail)
}

func portAvailable() error {
	return addressAvailable(relayruntime.DefaultListenAddress, "port 80 on 127.0.0.1")
}

func addressAvailable(address, label string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("%s is already in use: %w", label, err)
	}
	return listener.Close()
}

func dnsAddressAvailable(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("TCP DNS address %s is already in use: %w", address, err)
	}
	defer listener.Close()
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		return fmt.Errorf("UDP DNS address %s is already in use: %w", address, err)
	}
	return packet.Close()
}

func waitForPortAvailable(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		if err := portAvailable(); err == nil {
			return nil
		} else {
			lastError = err
		}
		if time.Now().After(deadline) {
			return lastError
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitForRelayAddressesAvailable(ctx context.Context, timeout time.Duration) error {
	if err := waitForPortAvailable(ctx, timeout); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		if err := dnsAddressAvailable(relayruntime.DefaultDNSAddress); err == nil {
			return nil
		} else {
			lastError = err
		}
		if time.Now().After(deadline) {
			return lastError
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
