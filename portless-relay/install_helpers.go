package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func removeExactFile(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func removeDirectoryIfEmpty(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func relayArgumentValues(arguments []string) (int, int, string, string, error) {
	values := map[string]string{}
	for index := 0; index+1 < len(arguments); index++ {
		switch arguments[index] {
		case "--socket", "--dns-socket", "--uid", "--gid":
			values[arguments[index]] = arguments[index+1]
			index++
		}
	}
	uid, uidErr := strconv.Atoi(values["--uid"])
	gid, gidErr := strconv.Atoi(values["--gid"])
	socket := values["--socket"]
	dnsSocket := values["--dns-socket"]
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 || !filepath.IsAbs(socket) || filepath.Base(filepath.Clean(socket)) != "ingress.sock" {
		return 0, 0, "", "", errors.New("service configuration does not contain valid relay ownership arguments")
	}
	if !filepath.IsAbs(dnsSocket) || filepath.Base(filepath.Clean(dnsSocket)) != "dns.sock" {
		return 0, 0, "", "", errors.New("service configuration does not contain a valid DNS relay target")
	}
	return uid, gid, socket, dnsSocket, nil
}

func validateSetupRequest(request SetupRequest) error {
	if request.UID <= 0 || request.GID <= 0 {
		return errors.New("the localhost relay must run as a non-root user and group")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	if !filepath.IsAbs(request.TargetSocket) {
		return errors.New("ingress target socket must be an absolute path")
	}
	cleanSocket := filepath.Clean(request.TargetSocket)
	if cleanSocket == string(filepath.Separator) || filepath.Base(cleanSocket) != "ingress.sock" {
		return errors.New("ingress target must be a private ingress.sock path")
	}
	if strings.ContainsRune(request.TargetSocket, '\x00') {
		return errors.New("ingress target socket contains an invalid character")
	}
	if !filepath.IsAbs(request.DNSTargetSocket) || filepath.Base(filepath.Clean(request.DNSTargetSocket)) != "dns.sock" || strings.ContainsRune(request.DNSTargetSocket, '\x00') {
		return errors.New("DNS target must be a private dns.sock path")
	}
	return nil
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
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
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
	if err := temporary.Sync(); err != nil {
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
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func writeRootFileAtomically(destination string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
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
	if err := temporary.Sync(); err != nil {
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
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func runCommand(ctx context.Context, executable string, arguments ...string) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	output, err := command.CombinedOutput()
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
	return addressAvailable(DefaultListenAddress, "port 80 on 127.0.0.1")
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
		if err := dnsAddressAvailable(DefaultDNSAddress); err == nil {
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
