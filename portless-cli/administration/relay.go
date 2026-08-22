package administration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-relay"
)

type relayStatusOutput struct {
	State string `json:"state"`
	relay.InstallationStatus
}

type relayActionOutput struct {
	Action string `json:"action"`
	relayStatusOutput
}

func (c *Commands) installRelay(ctx context.Context, jsonOutput bool) error {
	if _, err := c.Daemon.Ensure(ctx); err != nil {
		return err
	}
	uid, gid := c.Local.UserIDs()
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return err
	}
	if status.Installed && status.OwnerUID <= 0 {
		return errors.New("the existing clean-URL relay owner could not be determined; inspect `portless relay status`, then remove it with `portless relay uninstall --force`")
	}
	if status.Installed && status.OwnerUID != uid {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d; remove it with `portless relay uninstall --force` before installing it for this user", status.OwnerUID)
	}
	if status.Healthy && status.HelperCurrent && status.TargetSocket == c.Paths.IngressSocket && status.DNSTargetSocket == c.Paths.DNSSocket && status.ReceiptPresent && status.ResolverPresent {
		if jsonOutput {
			return command.WriteRelayStatusJSON(c.Out, status)
		}
		fmt.Fprintln(c.Out, "Clean local endpoints are already configured.")
		fmt.Fprintln(c.Out, c.Accent(c.Out, relay.ControlOrigin))
		fmt.Fprintln(c.Out, c.Accent(c.Out, "*.portless.test"))
		return nil
	}
	executable, err := c.Local.ResolvedExecutable()
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
	if err := c.Local.InstallRelay(ctx, relay.SetupRequest{
		Executable: executable, TargetSocket: c.Paths.IngressSocket, DNSTargetSocket: c.Paths.DNSSocket,
		UID: uid, GID: gid, Stdin: os.Stdin, Stdout: installOutput, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := c.Local.WaitRelay(ctx, 8*time.Second); err != nil {
		return err
	}
	if jsonOutput {
		ready, err := c.Local.InspectRelay(ctx)
		if err != nil {
			return err
		}
		return command.WriteRelayStatusJSON(c.Out, ready)
	}
	fmt.Fprintln(c.Out, "Clean local endpoints are", c.Success(c.Out, "ready")+".")
	fmt.Fprintln(c.Out, c.Accent(c.Out, relay.ControlOrigin))
	fmt.Fprintln(c.Out, c.Accent(c.Out, "*.portless.test"))
	return nil
}

func (c *Commands) relayStatus(ctx context.Context, jsonOutput bool) error {
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return command.WriteRelayStatusJSON(c.Out, status)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Portless relay:"), c.State(c.Out, status.State()))
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
	helperBuildState := "unknown"
	if status.HelperCurrent {
		helperBuildState = "current"
	} else if status.HelperBuildID != "" && status.CurrentBuildID != "" {
		helperBuildState = "outdated; run `portless setup` to refresh it"
	}
	fmt.Fprintln(c.Out, "Helper build:", helperBuildState)
	fmt.Fprintln(c.Out, "Configuration:", status.ConfigurationPath)
	fmt.Fprintln(c.Out, "Receipt:", status.ReceiptPath)
	if status.HealthError != "" {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "HTTP check:"), status.HealthError)
	}
	if status.DNSHealthError != "" {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "DNS check:"), status.DNSHealthError)
	}
	if status.ResolverHealthError != "" {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "Resolver check:"), status.ResolverHealthError)
	}
	if status.Problem != "" {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "Problem:"), status.Problem)
	}
	return nil
}

func (c *Commands) restartRelay(ctx context.Context, jsonOutput bool) error {
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		return errors.New("the Portless clean-URL relay is not installed; run `portless relay install`")
	}
	uid, _ := c.Local.UserIDs()
	if err := c.Local.ValidateRelayOwner(status, uid); err != nil {
		return err
	}
	if status.TargetSocket != "" && status.TargetSocket != c.Paths.IngressSocket {
		return fmt.Errorf("the relay targets %s, but this Portless installation uses %s; run `portless relay install` to repair it", status.TargetSocket, c.Paths.IngressSocket)
	}
	if status.DNSTargetSocket != c.Paths.DNSSocket || !status.ResolverPresent {
		return errors.New("the relay DNS configuration is stale; run `portless relay install` to repair it")
	}
	if _, err := c.Daemon.Ensure(ctx); err != nil {
		return err
	}
	executable, err := c.Local.ResolvedExecutable()
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
	if err := c.Local.RestartRelay(ctx, relay.RestartRequest{
		Executable: executable, UID: uid, Stdin: os.Stdin, Stdout: restartOutput, Stderr: c.Err,
	}); err != nil {
		return err
	}
	if err := c.Local.WaitRelay(ctx, 8*time.Second); err != nil {
		return err
	}
	ready, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return err
	}
	if jsonOutput {
		return command.WriteJSON(c.Out, relayActionOutput{
			Action:            "restart",
			relayStatusOutput: relayStatusOutput{State: ready.State(), InstallationStatus: ready},
		})
	}
	fmt.Fprintln(c.Out, "Clean-URL relay restarted and", c.Success(c.Out, "ready")+".")
	fmt.Fprintln(c.Out, c.Accent(c.Out, relay.ControlOrigin))
	return nil
}

func (c *Commands) uninstallRelay(ctx context.Context, force, jsonOutput bool) error {
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		if jsonOutput {
			return command.WriteJSON(c.Out, command.ActionOutput{Action: "uninstall", Status: "not-installed"})
		}
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	uid, _ := c.Local.UserIDs()
	if err := c.Local.ValidateRelayUninstall(status, uid, force); err != nil {
		return err
	}
	executable, err := c.Local.ResolvedExecutable()
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
	removed, err := c.Local.UninstallRelay(ctx, relay.UninstallRequest{
		Executable: executable, UID: uid, Force: force, Stdin: os.Stdin, Stdout: uninstallOutput, Stderr: c.Err,
	})
	if err != nil {
		return err
	}
	if !removed {
		if jsonOutput {
			return command.WriteJSON(c.Out, command.ActionOutput{Action: "uninstall", Status: "not-installed"})
		}
		fmt.Fprintln(c.Out, "The Portless clean-URL relay is not installed.")
		return nil
	}
	if jsonOutput {
		return command.WriteJSON(c.Out, command.ActionOutput{Action: "uninstall", Name: status.Service, Status: "removed"})
	}
	fmt.Fprintln(c.Out, "Clean-URL relay removed. Portless no longer owns 127.0.0.1:80, "+relay.DefaultDNSAddress+", its reserved loopback endpoint pool, or the portless.test resolver entry.")
	fmt.Fprintln(c.Out, "Running environments were not stopped, but their clean localhost URLs are unavailable until `portless relay install` or `portless setup` is run.")
	return nil
}
