package administration

import (
	"fmt"

	"github.com/runportless/portless/portless-cli/command"
)

func (c *Commands) printUninstallPreview(result uninstallOutput) error {
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	fmt.Fprintln(c.Out, c.Heading(c.Out, "Portless uninstall preview"))
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Installation:")
	fmt.Fprintf(c.Out, "  Daemon: %s", result.Daemon.State)
	if result.Daemon.PID > 0 {
		fmt.Fprintf(c.Out, " (PID %d)", result.Daemon.PID)
	}
	fmt.Fprintln(c.Out)
	if result.Daemon.Problem != "" {
		fmt.Fprintln(c.Out, "  Daemon detail: "+result.Daemon.Problem)
	}
	if result.Daemon.InventoryAvailable {
		fmt.Fprintf(c.Out, "  Application state: %d %s, %d %s\n", result.Projects, counted(result.Projects, "project"), result.Environments, counted(result.Environments, "environment"))
		fmt.Fprintf(c.Out, "  Managed container environments: %d\n", result.ManagedVolumeEnvironments)
	} else {
		fmt.Fprintln(c.Out, "  Application state: inventory will be verified before confirmed removal")
		if result.Data.Present && !result.Forced {
			fmt.Fprintln(c.Out, "  Confirmation: requires a current running daemon, or --force to authenticate and purge recoverable runtimes")
		}
	}
	fmt.Fprintf(c.Out, "  Relay: %s", result.Relay.State)
	if result.Relay.Installed && result.Relay.Service != "" {
		fmt.Fprintf(c.Out, " (%s)", result.Relay.Service)
	}
	fmt.Fprintln(c.Out)
	if result.Relay.Installed {
		fmt.Fprintf(c.Out, "  Resolver: %s\n", result.Relay.ResolverPath)
		fmt.Fprintf(c.Out, "  Reserved TCP endpoint pool: %s\n", yesNo(result.Relay.EndpointPoolReady))
		if result.Relay.AdministratorPrompt {
			fmt.Fprintln(c.Out, "  Administrator approval: required during confirmed removal")
		}
	}
	fmt.Fprintf(c.Out, "  Data: %s", result.Data.Path)
	if !result.Data.Present {
		fmt.Fprint(c.Out, " (not installed)")
	}
	fmt.Fprintln(c.Out)
	printLauncherPlan(c, result.Launcher)

	if len(result.Daemon.ActiveEnvironments) > 0 {
		fmt.Fprintln(c.Out)
		label := "Uninstall is currently blocked by active environments:"
		if result.Forced {
			label = "Force uninstall will terminate verified Portless runtimes in these environments:"
		}
		fmt.Fprintln(c.Out, c.Warning(c.Out, label))
		for _, environment := range result.Daemon.ActiveEnvironments {
			fmt.Fprintln(c.Out, "  "+environment)
		}
	}
	if len(result.Blockers) > 0 {
		fmt.Fprintln(c.Out)
		fmt.Fprintln(c.Out, c.Warning(c.Out, "Safety blockers:"))
		for _, blocker := range result.Blockers {
			fmt.Fprintln(c.Out, "  "+blocker)
		}
	}

	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "This will permanently remove:")
	for _, item := range result.WillRemove {
		fmt.Fprintln(c.Out, "  "+item)
	}
	fmt.Fprintln(c.Out)
	command := "portless uninstall --yes"
	if result.Forced || result.Data.Present && !result.Daemon.InventoryAvailable {
		command = "portless uninstall --force --yes"
	}
	fmt.Fprintf(c.Out, "No changes were made. Run `%s` to continue.\n", command)
	return nil
}

func (c *Commands) printUninstallComplete(result uninstallOutput) error {
	if c.JSONOutput {
		return command.WriteJSON(c.Out, result)
	}
	if result.Complete {
		fmt.Fprintln(c.Out, c.Success(c.Out, "Portless uninstall complete."))
	} else if result.Relay.Installed || result.Data.Present {
		fmt.Fprintln(c.Out, c.Warning(c.Out, "Portless uninstall did not complete."))
	} else {
		fmt.Fprintln(c.Out, c.Warning(c.Out, "Portless uninstall needs one manual cleanup step."))
	}
	for _, runtime := range result.RuntimeCleanup {
		fmt.Fprintf(c.Out, "%s cleanup: %d containers, %d volumes, %d networks.\n", runtime.Runtime, runtime.Containers, runtime.Volumes, runtime.Networks)
	}
	if result.ProcessesStopped > 0 {
		fmt.Fprintf(c.Out, "Stopped %d supervised %s.\n", result.ProcessesStopped, counted(result.ProcessesStopped, "process"))
	}
	if result.Relay.Removed {
		fmt.Fprintln(c.Out, "Relay: removed (service, resolver, and loopback endpoint pool)")
	} else if result.Relay.Installed {
		fmt.Fprintln(c.Out, "Relay: still installed")
	} else {
		fmt.Fprintln(c.Out, "Relay: not installed")
	}
	if result.Data.Removed {
		fmt.Fprintln(c.Out, "Daemon and data: removed from "+result.Data.Path)
	} else if result.Data.Present {
		fmt.Fprintln(c.Out, "Daemon and data: still present at "+result.Data.Path)
	} else {
		fmt.Fprintln(c.Out, "Daemon and data: not installed")
	}
	switch {
	case result.Launcher.Removed:
		fmt.Fprintln(c.Out, "CLI launcher: removed from "+result.Launcher.Path)
	case result.Launcher.Action == "remove":
		fmt.Fprintln(c.Out, "CLI launcher: still installed at "+result.Launcher.Path)
	case result.Launcher.Action == "preserve":
		fmt.Fprintln(c.Out, "CLI launcher: preserved ("+result.Launcher.Reason+")")
		if result.Launcher.Path != "" {
			fmt.Fprintln(c.Out, "  "+result.Launcher.Path)
		}
	default:
		fmt.Fprintln(c.Out, "CLI launcher: not installed")
	}
	for _, message := range result.Errors {
		label := "Failure: "
		if !result.Relay.Installed && !result.Data.Present {
			label = "Manual cleanup: "
		}
		fmt.Fprintln(c.Out, label+message)
	}
	return nil
}

func printLauncherPlan(c *Commands, plan launcherPlan) {
	switch plan.Action {
	case "remove":
		fmt.Fprintf(c.Out, "  CLI launcher: remove %s (%s)\n", plan.Path, plan.Kind)
		if plan.Target != "" && plan.Target != plan.Path {
			fmt.Fprintf(c.Out, "  Source/build target: preserve %s\n", plan.Target)
		}
	case "preserve":
		fmt.Fprintf(c.Out, "  CLI launcher: preserve %s (%s)\n", plan.Path, plan.Reason)
	default:
		fmt.Fprintln(c.Out, "  CLI launcher: not found")
	}
}

func yesNo(value bool) string {
	if value {
		return "configured"
	}
	return "not configured"
}
