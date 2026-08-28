// Package installation owns relay installation, inspection, ownership,
// platform service integration, restart, and removal.
package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
)

// SetupRequest contains the executable, daemon sockets, ownership, and streams
// needed to install the privileged relay.
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

// UninstallRequest contains ownership and confirmation details for removing the
// privileged relay.
type UninstallRequest struct {
	Executable string
	UID        int
	Force      bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// RestartRequest contains the executable and requesting-user details needed to
// restart an installed relay.
type RestartRequest struct {
	Executable string
	UID        int
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type privilegedLifecycleDependencies struct {
	effectiveUID func() int
	withLock     func(context.Context, platformInstallation, func() error) error
}

func defaultPrivilegedLifecycleDependencies() privilegedLifecycleDependencies {
	return privilegedLifecycleDependencies{effectiveUID: os.Geteuid, withLock: withRelayLifecycleLock}
}

// Install validates request and installs the relay, elevating through sudo when
// the current process is not already privileged.
func Install(ctx context.Context, request SetupRequest) error {
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	if err := validateInstallOwnership(ctx, newHostPlatform(), request.UID); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		return installPrivileged(ctx, newHostPlatform(), request.Executable, request.TargetSocket, request.DNSTargetSocket, request.UID, request.GID)
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

func installPrivileged(ctx context.Context, platform hostPlatform, sourceExecutable, targetSocket, dnsTargetSocket string, uid, gid int) error {
	return installPrivilegedWithDependencies(ctx, platform, sourceExecutable, targetSocket, dnsTargetSocket, uid, gid, defaultPrivilegedLifecycleDependencies())
}

func installPrivilegedWithDependencies(ctx context.Context, platform hostPlatform, sourceExecutable, targetSocket, dnsTargetSocket string, uid, gid int, dependencies privilegedLifecycleDependencies) error {
	if dependencies.effectiveUID() != 0 {
		return errors.New("the internal relay installer must run as root")
	}
	request := SetupRequest{Executable: sourceExecutable, TargetSocket: targetSocket, DNSTargetSocket: dnsTargetSocket, UID: uid, GID: gid}
	if err := validateSetupRequest(request); err != nil {
		return err
	}
	return dependencies.withLock(ctx, platform.installation(), func() error {
		if err := validateInstallOwnership(ctx, platform, request.UID); err != nil {
			return err
		}
		return platform.install(ctx, request)
	})
}

// Restart verifies relay ownership and restarts the installed service,
// elevating through sudo when necessary.
func Restart(ctx context.Context, request RestartRequest) error {
	if request.UID <= 0 {
		return errors.New("Portless relay restart requires a non-root requesting user ID")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	platform := newHostPlatform()
	status, err := inspectInstallation(ctx, platform)
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
		return restartPrivileged(ctx, platform, request.UID)
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

func restartPrivileged(ctx context.Context, platform hostPlatform, requestingUID int) error {
	return restartPrivilegedWithDependencies(ctx, platform, requestingUID, defaultPrivilegedLifecycleDependencies())
}

func restartPrivilegedWithDependencies(ctx context.Context, platform hostPlatform, requestingUID int, dependencies privilegedLifecycleDependencies) error {
	if dependencies.effectiveUID() != 0 {
		return errors.New("the internal relay restarter must run as root")
	}
	return dependencies.withLock(ctx, platform.installation(), func() error {
		status, err := inspectInstallation(ctx, platform)
		if err != nil {
			return err
		}
		if !status.Installed {
			return errors.New("the Portless clean-URL relay is not installed")
		}
		if err := validateOwnership(status, requestingUID); err != nil {
			return err
		}
		return platform.restart(ctx)
	})
}

// Uninstall verifies relay ownership and removes the installed service. The
// boolean result reports whether an installed relay was found for removal.
func Uninstall(ctx context.Context, request UninstallRequest) (bool, error) {
	if request.UID <= 0 && !request.Force {
		return false, errors.New("Portless relay uninstall requires a non-root requesting user ID")
	}
	if err := validateExecutable(request.Executable); err != nil {
		return false, err
	}
	platform := newHostPlatform()
	status, err := inspectInstallation(ctx, platform)
	if err != nil {
		return false, err
	}
	if !status.Installed {
		if status.EndpointPoolResidual {
			return false, errors.New("the relay is not installed, but unverified reserved loopback aliases remain; run `portless relay status` for safe recovery guidance")
		}
		return false, nil
	}
	if err := validateUninstallOwnership(status, request.UID, request.Force); err != nil {
		return false, err
	}
	if os.Geteuid() == 0 {
		return true, uninstallPrivileged(ctx, platform, request.UID, request.Force)
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

func uninstallPrivileged(ctx context.Context, platform hostPlatform, requestingUID int, force bool) error {
	return uninstallPrivilegedWithDependencies(ctx, platform, requestingUID, force, defaultPrivilegedLifecycleDependencies())
}

func uninstallPrivilegedWithDependencies(ctx context.Context, platform hostPlatform, requestingUID int, force bool, dependencies privilegedLifecycleDependencies) error {
	if dependencies.effectiveUID() != 0 {
		return errors.New("the internal relay uninstaller must run as root")
	}
	return dependencies.withLock(ctx, platform.installation(), func() error {
		status, err := inspectInstallation(ctx, platform)
		if err != nil {
			return err
		}
		if !status.Installed {
			if status.EndpointPoolResidual {
				return errors.New("the relay is not installed, but unverified reserved loopback aliases remain; run `portless relay status` for safe recovery guidance")
			}
			return nil
		}
		if err := validateUninstallOwnership(status, requestingUID, force); err != nil {
			return err
		}
		spec := uninstallSpec{}
		receipt, receiptErr := readInstallationReceipt(platform.installation())
		if receiptErr == nil {
			spec.loopbackAddresses = append([]string(nil), receipt.LoopbackAddresses...)
		}
		if err := platform.uninstall(ctx, spec); err != nil {
			return err
		}
		remaining, err := inspectInstallation(ctx, platform)
		if err != nil {
			return err
		}
		if remaining.Installed {
			return errors.New("localhost relay removal was incomplete; run `portless relay status` for details")
		}
		if remaining.EndpointPoolResidual {
			if receiptErr != nil {
				return fmt.Errorf("relay artifacts were removed, but reserved loopback aliases remain because the ownership receipt could not be verified: %w; run `portless relay status` for safe manual recovery guidance", receiptErr)
			}
			return errors.New("relay artifacts were removed, but reserved loopback aliases remain after verified removal; run `portless relay status` before reinstalling")
		}
		if receiptErr != nil && remaining.EndpointPoolManaged && remaining.EndpointPoolError != "" {
			return fmt.Errorf("relay artifacts were removed, but reserved loopback aliases could not be inspected after the ownership receipt failed validation: %w; inspect the relay status before reinstalling", receiptErr)
		}
		return nil
	})
}

func validateInstallOwnership(ctx context.Context, platform hostPlatform, requestingUID int) error {
	status, err := inspectInstallation(ctx, platform)
	if err != nil {
		return err
	}
	if !status.Installed {
		if status.EndpointPoolResidual {
			return errors.New("refuse to install over unverified reserved loopback aliases; run `portless relay status` for safe recovery guidance")
		}
		return nil
	}
	if err := validateOwnership(status, requestingUID); err != nil {
		return fmt.Errorf("refuse to replace the existing clean-URL relay: %w", err)
	}
	return nil
}
