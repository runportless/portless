package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ControlOrigin = "http://portless.localhost"

type SetupRequest struct {
	Executable   string
	TargetSocket string
	UID          int
	GID          int
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
}

func Check(ctx context.Context) error {
	return checkAt(ctx, DefaultListenAddress, ControlOrigin)
}

func WaitUntilReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastError error
	for {
		if err := Check(ctx); err == nil {
			return nil
		} else {
			lastError = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("clean localhost ingress did not become ready: %w", lastError)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func checkAt(ctx context.Context, address, origin string) error {
	dialer := &net.Dialer{Timeout: 500 * time.Millisecond}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/api/v1/health", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", origin, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", origin, response.Status)
	}
	var health struct {
		Ready      bool   `json:"ready"`
		APIVersion string `json:"apiVersion"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&health); err != nil {
		return fmt.Errorf("decode %s health response: %w", origin, err)
	}
	if !health.Ready || health.APIVersion == "" {
		return errors.New("portless ingress health response is incompatible")
	}
	return nil
}

func Install(ctx context.Context, request SetupRequest) error {
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return InstallPrivileged(ctx, request.Executable, request.TargetSocket, request.UID, request.GID)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("Portless setup requires sudo, but sudo was not found")
	}
	command := exec.CommandContext(ctx, sudo,
		request.Executable,
		"__install-ingress",
		"--socket", request.TargetSocket,
		"--uid", strconv.Itoa(request.UID),
		"--gid", strconv.Itoa(request.GID),
	)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("install clean localhost ingress: %w", err)
	}
	return nil
}

func InstallPrivileged(ctx context.Context, sourceExecutable, targetSocket string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal ingress installer must run as root")
	}
	request := SetupRequest{Executable: sourceExecutable, TargetSocket: targetSocket, UID: uid, GID: gid}
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	return installPlatform(ctx, request)
}

func validateSetupRequest(request SetupRequest) error {
	if request.UID <= 0 || request.GID <= 0 {
		return errors.New("clean localhost ingress must run as a non-root user and group")
	}
	if !filepath.IsAbs(request.Executable) {
		return errors.New("Portless executable path must be absolute")
	}
	info, err := os.Stat(request.Executable)
	if err != nil {
		return fmt.Errorf("inspect Portless executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return errors.New("Portless executable must be a regular executable file")
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
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".portless-ingress-*")
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
	listener, err := net.Listen("tcp", DefaultListenAddress)
	if err != nil {
		return fmt.Errorf("port 80 on 127.0.0.1 is already in use: %w", err)
	}
	return listener.Close()
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
