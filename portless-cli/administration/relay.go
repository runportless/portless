package administration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/runportless/portless/portless-cli/command"
	relayinstallation "github.com/runportless/portless/portless-relay/installation"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

type relayStatusOutput struct {
	State string `json:"state"`
	relayinstallation.InstallationStatus
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
	if !status.Installed && status.EndpointPoolResidual {
		return errors.New("unverified reserved Portless loopback aliases remain from an incomplete installation; run `portless relay status` for safe manual recovery guidance")
	}
	if status.Installed && status.OwnerUID <= 0 {
		return errors.New("the existing clean-URL relay owner could not be determined; inspect `portless relay status`, then use `portless relay uninstall --force` to remove fixed artifacts; unverified loopback aliases require separate manual verification")
	}
	if status.Installed && status.OwnerUID != uid {
		return fmt.Errorf("the clean-URL relay belongs to user ID %d; remove it with `portless relay uninstall --force` before installing it for this user", status.OwnerUID)
	}
	if status.Healthy && status.HelperVerified && status.HelperCompatible && status.TargetSocket == c.Paths.IngressSocket && status.DNSTargetSocket == c.Paths.DNSSocket && status.ReceiptPresent && status.ResolverPresent {
		if jsonOutput {
			return command.WriteRelayStatusJSON(c.Out, status)
		}
		fmt.Fprintln(c.Out, "Clean local endpoints are already configured.")
		fmt.Fprintln(c.Out, c.Accent(c.Out, relayruntime.ControlOrigin))
		fmt.Fprintln(c.Out, c.Accent(c.Out, "*.localhost"))
		fmt.Fprintln(c.Out, c.Accent(c.Out, "*.portless.test"))
		return nil
	}
	executable, err := c.Local.ResolvedExecutable()
	if err != nil {
		return err
	}
	if !jsonOutput {
		if status.Installed {
			fmt.Fprintln(c.Out, "Repairing Portless HTTP ingress and endpoint DNS requires administrator approval.")
		} else {
			fmt.Fprintln(c.Out, "Portless needs administrator approval once to install HTTP ingress and scoped endpoint DNS.")
		}
	}
	installOutput := c.Out
	if jsonOutput {
		installOutput = c.Err
	}
	if err := c.Local.InstallRelay(ctx, relayinstallation.SetupRequest{
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
	fmt.Fprintln(c.Out, c.Accent(c.Out, relayruntime.ControlOrigin))
	fmt.Fprintln(c.Out, c.Accent(c.Out, "*.localhost"))
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
	fmt.Fprintln(c.Out, "HTTP listener:", relayruntime.DefaultListenAddress)
	fmt.Fprintln(c.Out, "Control URL:", relayruntime.ControlOrigin)
	fmt.Fprintln(c.Out, "DNS domains:", "localhost, portless.test")
	fmt.Fprintln(c.Out, "DNS listener:", relayruntime.DefaultDNSAddress, "(UDP and TCP)")
	if !status.Installed {
		if status.EndpointPoolResidual {
			fmt.Fprintln(c.Out, c.Failure(c.Out, "Reserved endpoint pool:"), "present without a valid ownership receipt")
			if status.EndpointPoolDetail != "" {
				fmt.Fprintln(c.Out, "  "+status.EndpointPoolDetail)
			}
			if status.Problem != "" {
				fmt.Fprintln(c.Out, c.Failure(c.Out, "Problem:"), status.Problem)
			}
			fmt.Fprintln(c.Out, "Review `ifconfig lo0`; remove only aliases you can independently verify as Portless, then rerun `portless relay install`.")
			return nil
		}
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
		fmt.Fprintln(c.Out, "TCP resolver:", status.ResolverPath)
	}
	if status.LocalhostResolverPath != "" {
		fmt.Fprintln(c.Out, "HTTP resolver:", status.LocalhostResolverPath)
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
	helperBuildState := "not verified"
	if status.HelperVerified {
		helperBuildState = "verified"
	}
	if status.HelperBuildID != "" {
		helperBuildState += " (" + shortFingerprint(status.HelperBuildID) + ")"
	}
	fmt.Fprintln(c.Out, "Helper build:", helperBuildState)
	helperVersionState := "unknown"
	if status.HelperCompatible {
		helperVersionState = "compatible (" + status.HelperVersion + ")"
	} else if status.HelperVersion != "" {
		helperVersionState = fmt.Sprintf("update required (installed %s, required %s)", status.HelperVersion, status.RequiredHelperVersion)
	} else if status.RequiredHelperVersion != "" {
		helperVersionState = "unavailable (required " + status.RequiredHelperVersion + ")"
	}
	fmt.Fprintln(c.Out, "Helper version:", helperVersionState)
	configurationState := status.ConfigurationPath
	if status.ConfigurationError != "" {
		configurationState += " (drifted)"
	}
	fmt.Fprintln(c.Out, "Configuration:", configurationState)
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
	currentUID, _ := c.Local.UserIDs()
	repairGuidance := relayRepairGuidance(status, currentUID)
	if repairGuidance != "" {
		fmt.Fprintln(c.Out, c.Failure(c.Out, "Action required:"), repairGuidance)
		fmt.Fprintln(c.Out, "Run `portless relay install` to update the privileged helper and repair the system service and DNS configuration.")
		fmt.Fprintln(c.Out, "The repair may request administrator approval and does not stop running environments.")
	}
	if status.Problem != "" {
		label := "Problem:"
		if repairGuidance != "" {
			label = "Details:"
		}
		fmt.Fprintln(c.Out, c.Failure(c.Out, label), status.Problem)
	}
	return nil
}

func relayRepairGuidance(status relayinstallation.InstallationStatus, currentUID int) string {
	if !status.ReceiptPresent || status.OwnerUID <= 0 || status.OwnerUID != currentUID {
		return ""
	}
	configurationDrifted := status.ConfigurationError != ""
	helperUnverified := status.HelperPresent && !status.HelperVerified
	helperIncompatible := status.HelperPresent && status.HelperVerified && !status.HelperCompatible
	switch {
	case helperUnverified && configurationDrifted:
		return "The installed privileged helper cannot be verified against its ownership receipt, and its system configuration has drifted."
	case helperUnverified:
		return "The installed privileged helper must be reinstalled to establish receipt-bound integrity."
	case helperIncompatible && configurationDrifted:
		return fmt.Sprintf("The installed privileged helper version %s does not match required version %s, and its system configuration has drifted.", status.HelperVersion, status.RequiredHelperVersion)
	case helperIncompatible:
		return fmt.Sprintf("The installed privileged helper version %s does not match required version %s.", status.HelperVersion, status.RequiredHelperVersion)
	case configurationDrifted:
		return "The relay service or DNS configuration no longer matches the current Portless installation."
	default:
		return ""
	}
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
	if err := c.Local.RestartRelay(ctx, relayinstallation.RestartRequest{
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
	fmt.Fprintln(c.Out, c.Accent(c.Out, relayruntime.ControlOrigin))
	return nil
}

func (c *Commands) uninstallRelay(ctx context.Context, force, jsonOutput bool) error {
	status, err := c.Local.InspectRelay(ctx)
	if err != nil {
		return err
	}
	if !status.Installed {
		if status.EndpointPoolResidual {
			return errors.New("the relay is not installed, but unverified reserved loopback aliases remain; run `portless relay status` for safe manual recovery guidance")
		}
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
		if status.ResolverPath != "" {
			fmt.Fprintln(c.Out, "  TCP resolver:", status.ResolverPath)
		}
		if status.LocalhostResolverPath != "" {
			fmt.Fprintln(c.Out, "  HTTP resolver:", status.LocalhostResolverPath)
		}
		fmt.Fprintln(c.Out, "Projects, containers, volumes, recordings, and Portless user data will not be removed.")
		fmt.Fprintln(c.Out, "Administrator approval is required to remove the system service.")
	}
	uninstallOutput := c.Out
	if jsonOutput {
		uninstallOutput = c.Err
	}
	removed, err := c.Local.UninstallRelay(ctx, relayinstallation.UninstallRequest{
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
	fmt.Fprintln(c.Out, "Clean-URL relay removed. Portless no longer owns 127.0.0.1:80, "+relayruntime.DefaultDNSAddress+", its reserved loopback endpoint pool, or its scoped resolver entries.")
	fmt.Fprintln(c.Out, "Running environments were not stopped, but their clean localhost URLs are unavailable until `portless relay install` or `portless setup` is run.")
	return nil
}
