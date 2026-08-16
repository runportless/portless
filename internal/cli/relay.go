package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/portless-run/portless/internal/relay"
	relayinstall "github.com/portless-run/portless/internal/relay/install"
)

func (c *CLI) installRelay(ctx context.Context, jsonOutput bool) error {
	if _, err := c.daemon.Ensure(ctx); err != nil {
		return err
	}
	uid, gid := c.local.userIDs()
	status, err := c.local.inspectRelay(ctx)
	if err != nil {
		return err
	}
	if status.Installed && status.OwnerUID <= 0 {
		return errors.New("the existing clean-URL relay owner could not be determined; inspect `portless relay status`, then remove it with `portless relay uninstall --force`")
	}
	if status.Installed && status.OwnerUID != uid {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d; remove it with `portless relay uninstall --force` before installing it for this user", status.OwnerUID)
	}
	if status.Healthy && status.TargetSocket == c.paths.IngressSocket && status.DNSTargetSocket == c.paths.DNSSocket && status.ReceiptPresent && status.ResolverPresent {
		if jsonOutput {
			return writeRelayStatusJSON(c.Out, status)
		}
		fmt.Fprintln(c.Out, "Clean local endpoints are already configured.")
		fmt.Fprintln(c.Out, c.accent(c.Out, relay.ControlOrigin))
		fmt.Fprintln(c.Out, c.accent(c.Out, "*.portless.test"))
		return nil
	}
	executable, err := c.local.resolvedExecutable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		if status.Installed {
			fmt.Fprintln(c.Out, "Repairing Portless HTTP ingress and TCP endpoint DNS requires administrator approval.")
		} else {
			fmt.Fprintln(c.Out, "Portless needs administrator approval once to install HTTP ingress and scoped TCP endpoint DNS.")
		}
	}
	installOutput := c.Out
	if jsonOutput {
		installOutput = c.Err
	}
	if err := c.local.installRelay(ctx, relayinstall.SetupRequest{
		Executable: executable, TargetSocket: c.paths.IngressSocket, DNSTargetSocket: c.paths.DNSSocket,
		UID: uid, GID: gid, Stdin: os.Stdin, Stdout: installOutput, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := c.local.waitRelay(ctx, 8*time.Second); err != nil {
		return err
	}
	if jsonOutput {
		ready, err := c.local.inspectRelay(ctx)
		if err != nil {
			return err
		}
		return writeRelayStatusJSON(c.Out, ready)
	}
	fmt.Fprintln(c.Out, "Clean local endpoints are", c.success(c.Out, "ready")+".")
	fmt.Fprintln(c.Out, c.accent(c.Out, relay.ControlOrigin))
	fmt.Fprintln(c.Out, c.accent(c.Out, "*.portless.test"))
	return nil
}

func (c *CLI) relayStatus(ctx context.Context, jsonOutput bool) error {
	status, err := c.local.inspectRelay(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeRelayStatusJSON(c.Out, status)
	}
	fmt.Fprintln(c.Out, c.heading(c.Out, "Portless relay:"), c.state(c.Out, status.State()))
	fmt.Fprintln(c.Out, "Platform:", status.Platform)
	fmt.Fprintln(c.Out, "HTTP listener:", relay.DefaultListenAddress)
	fmt.Fprintln(c.Out, "Control URL:", relay.ControlOrigin)
	fmt.Fprintln(c.Out, "DNS domain:", "portless.test")
	fmt.Fprintln(c.Out, "DNS listener:", relay.DefaultDNSAddress, "(UDP and TCP)")
	if !status.Installed {
		fmt.Fprintln(c.Out, "Run `portless relay install` or `portless setup` to install it.")
		return nil
	}
	fmt.Fprintln(c.Out, "Service:", status.Service)
	if status.OwnerUID > 0 {
		fmt.Fprintf(c.Out, "Owner: UID %d, GID %d\n", status.OwnerUID, status.OwnerGID)
	} else {
		fmt.Fprintln(c.Out, "Owner: unknown")
	}
	if status.TargetSocket != "" {
		fmt.Fprintln(c.Out, "HTTP forwards to:", status.TargetSocket)
	}
	if status.DNSTargetSocket != "" {
		fmt.Fprintln(c.Out, "DNS forwards to:", status.DNSTargetSocket)
	}
	if status.ResolverPath != "" {
		fmt.Fprintln(c.Out, "Resolver:", status.ResolverPath)
	}
	poolState := "not ready"
	if status.EndpointPoolReady {
		poolState = "ready"
	}
	fmt.Fprintln(c.Out, "TCP endpoint pool:", poolState)
	if status.EndpointPoolDetail != "" {
		fmt.Fprintln(c.Out, "  "+status.EndpointPoolDetail)
	}
	if status.InstalledAt != nil {
		fmt.Fprintln(c.Out, "Installed:", status.InstalledAt.Local().Format(time.RFC3339))
	}
	fmt.Fprintln(c.Out, "Helper:", status.HelperPath)
	fmt.Fprintln(c.Out, "Configuration:", status.ConfigurationPath)
	fmt.Fprintln(c.Out, "Receipt:", status.ReceiptPath)
	if status.HealthError != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "HTTP check:"), status.HealthError)
	}
	if status.DNSHealthError != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "DNS check:"), status.DNSHealthError)
	}
	if status.ResolverHealthError != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "Resolver check:"), status.ResolverHealthError)
	}
	if status.Problem != "" {
		fmt.Fprintln(c.Out, c.failure(c.Out, "Problem:"), status.Problem)
	}
	return nil
}

func (c *CLI) restartRelay(ctx context.Context, jsonOutput bool) error {
	status, err := c.local.inspectRelay(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed; run `portless relay install`")
	}
	uid, _ := c.local.userIDs()
	if err := c.local.validateRelayOwner(status, uid); err != nil {
		return err
	}
	if status.TargetSocket != "" && status.TargetSocket != c.paths.IngressSocket {
		return fmt.Errorf("the relay targets %s, but this Portless installation uses %s; run `portless relay install` to repair it", status.TargetSocket, c.paths.IngressSocket)
	}
	if status.DNSTargetSocket != c.paths.DNSSocket || !status.ResolverPresent {
		return errors.New("the relay DNS configuration is stale; run `portless relay install` to repair it")
	}
	if _, err := c.daemon.Ensure(ctx); err != nil {
		return err
	}
	executable, err := c.local.resolvedExecutable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		fmt.Fprintln(c.Out, "Restarting the Portless localhost relay requires administrator approval.")
	}
	restartOutput := c.Out
	if jsonOutput {
		restartOutput = c.Err
	}
	if err := c.local.restartRelay(ctx, relayinstall.RestartRequest{
		Executable: executable, UID: uid, Stdin: os.Stdin, Stdout: restartOutput, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := c.local.waitRelay(ctx, 8*time.Second); err != nil {
		return err
	}
	ready, err := c.local.inspectRelay(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeJSON(c.Out, relayActionOutput{
			Action:            "restart",
			relayStatusOutput: relayStatusOutput{State: ready.State(), InstallationStatus: ready},
		})
	}
	fmt.Fprintln(c.Out, "Clean-URL relay restarted and", c.success(c.Out, "ready")+".")
	fmt.Fprintln(c.Out, c.accent(c.Out, relay.ControlOrigin))
	return nil
}

func (c *CLI) uninstallRelay(ctx context.Context, force, jsonOutput bool) error {
	status, err := c.local.inspectRelay(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		if jsonOutput {
			return writeJSON(c.Out, actionOutput{Action: "uninstall", Status: "not-installed"})
		}
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	uid, _ := c.local.userIDs()
	if err := c.local.validateRelayUninstall(status, uid, force); err != nil {
		return err
	}
	executable, err := c.local.resolvedExecutable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		fmt.Fprintln(c.Out, "Removing the Portless clean-URL relay:")
		fmt.Fprintln(c.Out, "  service:", status.Service)
		fmt.Fprintln(c.Out, "  helper: ", status.HelperPath)
		fmt.Fprintln(c.Out, "Projects, containers, volumes, recordings, and Portless user data will not be removed.")
		fmt.Fprintln(c.Out, "Administrator approval is required to remove the system service.")
	}
	uninstallOutput := c.Out
	if jsonOutput {
		uninstallOutput = c.Err
	}
	removed, err := c.local.uninstallRelay(ctx, relayinstall.UninstallRequest{
		Executable: executable, UID: uid, Force: force, Stdin: os.Stdin, Stdout: uninstallOutput, Stderr: c.Err,
	})
	if err != nil {
		return err
	}
	if !removed {
		if jsonOutput {
			return writeJSON(c.Out, actionOutput{Action: "uninstall", Status: "not-installed"})
		}
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	if jsonOutput {
		return writeJSON(c.Out, actionOutput{Action: "uninstall", Name: status.Service, Status: "removed"})
	}
	fmt.Fprintln(c.Out, "Clean-URL relay removed. Portless no longer owns 127.0.0.1:80, "+relay.DefaultDNSAddress+", its reserved loopback endpoint pool, or the portless.test resolver entry.")
	fmt.Fprintln(c.Out, "Running environments were not stopped, but their clean localhost URLs are unavailable until `portless relay install` or `portless setup` is run.")
	return nil
}

func resolvedExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(executable)
}

func requestingUserIDs() (int, int) {
	uid, gid := os.Getuid(), os.Getgid()
	if os.Geteuid() != 0 {
		return uid, gid
	}
	if sudoUID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && sudoUID > 0 {
		uid = sudoUID
	}
	if sudoGID, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil && sudoGID > 0 {
		gid = sudoGID
	}
	return uid, gid
}
