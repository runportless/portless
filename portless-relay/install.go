// Package install owns privileged relay installation, inspection, restart,
// ownership validation, and removal.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

type SetupRequest struct {
	Executable      string
	TargetSocket    string
	DNSTargetSocket string
	UID             int
	GID             int
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
}

type UninstallRequest struct {
	Executable string
	UID        int
	Force      bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type RestartRequest struct {
	Executable string
	UID        int
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// PrepareRuntime ensures the machine-level loopback addresses required by the
// privileged relay are present before the relay binds and drops privileges.
func PrepareRuntime(ctx context.Context) error {
	return prepareRelayLoopbackPool(ctx, false)
}

func Install(ctx context.Context, request SetupRequest) error {
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return InstallPrivileged(ctx, request.Executable, request.TargetSocket, request.DNSTargetSocket, request.UID, request.GID)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("Portless setup requires sudo, but sudo was not found")
	}
	command := exec.CommandContext(ctx, sudo,
		request.Executable,
		"__install-relay",
		"--socket", request.TargetSocket,
		"--dns-socket", request.DNSTargetSocket,
		"--uid", strconv.Itoa(request.UID),
		"--gid", strconv.Itoa(request.GID),
	)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("install localhost relay: %w", err)
	}
	return nil
}

func InstallPrivileged(ctx context.Context, sourceExecutable, targetSocket, dnsTargetSocket string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal relay installer must run as root")
	}
	request := SetupRequest{Executable: sourceExecutable, TargetSocket: targetSocket, DNSTargetSocket: dnsTargetSocket, UID: uid, GID: gid}
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	return installPlatform(ctx, request)
}

func Restart(ctx context.Context, request RestartRequest) error {
	if request.UID <= 0 {
		return errors.New("Portless relay restart requires a non-root requesting user ID")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	status, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed")
	}
	if err := validateOwnership(status, request.UID); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return RestartPrivileged(ctx, request.UID)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("Portless relay restart requires sudo, but sudo was not found")
	}
	command := exec.CommandContext(ctx, sudo,
		request.Executable,
		"__restart-relay",
		"--uid", strconv.Itoa(request.UID),
	)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("restart localhost relay: %w", err)
	}
	return nil
}

func RestartPrivileged(ctx context.Context, requestingUID int) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal relay restarter must run as root")
	}
	status, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed")
	}
	if err := validateOwnership(status, requestingUID); err != nil {
		return err
	}
	return restartPlatform(ctx)
}

func Uninstall(ctx context.Context, request UninstallRequest) (bool, error) {
	if request.UID <= 0 && !request.Force {
		return false, errors.New("Portless relay uninstall requires a non-root requesting user ID")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return false, err
	}
	status, err := Inspect(ctx)
	if err != nil {
		return false, err
	}
	if !status.Installed {
		return false, nil
	}
	if err := validateUninstallOwnership(status, request.UID, request.Force); err != nil {
		return false, err
	}
	if os.Geteuid() == 0 {
		return true, UninstallPrivileged(ctx, request.UID, request.Force)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return false, errors.New("Portless relay uninstall requires sudo, but sudo was not found")
	}
	arguments := []string{request.Executable, "__uninstall-relay", "--uid", strconv.Itoa(request.UID)}
	if request.Force {
		arguments = append(arguments, "--force")
	}
	command := exec.CommandContext(ctx, sudo, arguments...)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	if err := command.Run(); err != nil {
		return false, fmt.Errorf("uninstall localhost relay: %w", err)
	}
	return true, nil
}

func UninstallPrivileged(ctx context.Context, requestingUID int, force bool) error {
	if os.Geteuid() != 0 {
		return errors.New("the internal relay uninstaller must run as root")
	}
	status, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return nil
	}
	if err := validateUninstallOwnership(status, requestingUID, force); err != nil {
		return err
	}
	removeLoopbackPool := false
	if receipt, receiptErr := readInstallationReceipt(currentPlatformInstallation()); receiptErr == nil {
		removeLoopbackPool = receipt.SchemaVersion >= 3
	}
	if err := uninstallPlatform(ctx, removeLoopbackPool); err != nil {
		return err
	}
	remaining, err := Inspect(ctx)
	if err != nil {
		return err
	}
	if remaining.Installed {
		return errors.New("localhost relay removal was incomplete; run `portless relay status` for details")
	}
	return nil
}
