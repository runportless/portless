package administration

import (
	"time"

	shared "github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-cli/doctor"
	"github.com/runportless/portless/portless-daemon/control"
	"github.com/spf13/cobra"
)

func (c *Commands) configCommand() *cobra.Command {
	root := shared.CommandGroup("config", "Manage CLI preferences")
	color := &cobra.Command{
		Use:               "color [auto|always|never]",
		Short:             "Show or save the color preference",
		Args:              shared.UsageArgs(cobra.MaximumNArgs(1)),
		ValidArgs:         []string{string(shared.ColorAuto), string(shared.ColorAlways), string(shared.ColorNever)},
		ValidArgsFunction: shared.FixedCompletions(string(shared.ColorAuto), string(shared.ColorAlways), string(shared.ColorNever)),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				preference, err := shared.ParseColorPreference(args[0])
				if err != nil {
					return err
				}
				if err := c.SaveColorPreference(preference); err != nil {
					return err
				}
			}
			return c.PrintColorConfig()
		},
	}
	reset := &cobra.Command{Use: "reset", Short: "Reset all CLI preferences to built-in defaults", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(_ *cobra.Command, _ []string) error {
		return c.ResetPreferences()
	}}
	root.AddCommand(color, reset)
	return root
}

func (c *Commands) daemonCommand() *cobra.Command {
	root := shared.CommandGroup("daemon", "Inspect, stop, or restart the local Portless daemon")
	status := &cobra.Command{Use: "status", Short: "Authenticate the daemon and show its identity", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.daemonStatus(cmd.Context(), c.JSONOutput)
	}}
	root.AddCommand(status)

	stopOptions := control.StopOptions{Timeout: 15 * time.Second}
	stop := &cobra.Command{Use: "stop", Short: "Gracefully stop the authenticated daemon", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.stopDaemon(cmd.Context(), stopOptions, c.JSONOutput)
	}}
	stop.Flags().BoolVar(&stopOptions.Force, "force", false, "stop despite active environments or use the guarded legacy fallback")
	stop.Flags().DurationVar(&stopOptions.Timeout, "timeout", stopOptions.Timeout, "time to wait for graceful shutdown")
	root.AddCommand(stop)

	restartForce := false
	restart := &cobra.Command{Use: "restart", Short: "Replace the authenticated daemon within the readiness SLA", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.restartDaemon(cmd.Context(), restartForce, c.JSONOutput)
	}}
	restart.Flags().BoolVar(&restartForce, "force", false, "bypass handoff safety or replace a guarded legacy daemon outside the normal restart SLA")
	root.AddCommand(restart)
	return root
}

func (c *Commands) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "doctor [daemon|relay|runtime]",
		Short:             "Diagnose the local Portless installation",
		Args:              shared.UsageArgs(cobra.MaximumNArgs(1)),
		ValidArgs:         []string{"daemon", "relay", "runtime"},
		ValidArgsFunction: shared.FixedCompletions("daemon", "relay", "runtime"),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := doctor.ParseScope(shared.FirstArg(args))
			if err != nil {
				return shared.UsageError("%v", err)
			}
			return c.doctor(cmd.Context(), scope, c.JSONOutput)
		},
	}
}

func (c *Commands) setupCommand() *cobra.Command {
	return &cobra.Command{Use: "setup", Short: "Configure clean HTTP URLs and endpoint DNS", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.installRelay(cmd.Context(), c.JSONOutput)
	}}
}

func (c *Commands) relayCommand() *cobra.Command {
	root := shared.CommandGroup("relay", "Manage clean local endpoint networking")
	install := &cobra.Command{Use: "install", Short: "Install or repair HTTP ingress and endpoint DNS", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.installRelay(cmd.Context(), c.JSONOutput)
	}}
	status := &cobra.Command{Use: "status", Short: "Show local HTTP and DNS relay health", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.relayStatus(cmd.Context(), c.JSONOutput)
	}}
	restart := &cobra.Command{Use: "restart", Short: "Restart the installed HTTP and DNS relay", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.restartRelay(cmd.Context(), c.JSONOutput)
	}}
	force := false
	uninstall := &cobra.Command{Use: "uninstall", Aliases: []string{"remove"}, Short: "Remove only the privileged HTTP and DNS relay", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.uninstallRelay(cmd.Context(), force, c.JSONOutput)
	}}
	uninstall.Flags().BoolVar(&force, "force", false, "remove an installation owned by another or unknown user")
	root.AddCommand(install, status, restart, uninstall)
	return root
}

func (c *Commands) runtimeCommand() *cobra.Command {
	root := shared.CommandGroup("runtime", "Manage the Docker or Podman container runtime")
	root.AddCommand(&cobra.Command{Use: "status", Short: "Show container runtime status", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.runtimeStatus(cmd.Context(), c.JSONOutput)
	}})
	root.AddCommand(&cobra.Command{Use: "start", Short: "Start the configured container runtime", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.startRuntime(cmd.Context(), c.JSONOutput)
	}})
	use := &cobra.Command{Use: "use <auto|docker|podman>", Short: "Select the container runtime", Args: shared.UsageArgs(cobra.ExactArgs(1)), ValidArgs: []string{"auto", "docker", "podman"}, RunE: func(cmd *cobra.Command, args []string) error {
		if args[0] != "auto" && args[0] != "docker" && args[0] != "podman" {
			return shared.UsageError("runtime must be auto, docker, or podman")
		}
		return c.useRuntime(cmd.Context(), args[0], c.JSONOutput)
	}}
	root.AddCommand(use)
	return root
}

func (c *Commands) resetCommand() *cobra.Command {
	options := resetOptions{}
	root := &cobra.Command{Use: "reset", Short: "Erase all projects and local environment data", Long: "Reset Portless to an empty application state. The command preserves CLI preferences, runtime selection, installation identity, authentication, and the localhost relay installation.", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.reset(cmd.Context(), options)
	}}
	root.Flags().BoolVar(&options.yes, "yes", false, "confirm permanent deletion")
	root.Flags().BoolVar(&options.force, "force", false, "terminate verified Portless runtimes even when environments are active or unknown")
	return root
}

func (c *Commands) uninstallCommand() *cobra.Command {
	options := uninstallOptions{}
	root := &cobra.Command{Use: "uninstall", Short: "Remove Portless and all locally managed resources", Long: "Uninstall Portless completely. Without --yes, the command only previews the daemon, relay, resolver, managed runtimes, data, and CLI launcher that would be removed.", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.uninstall(cmd.Context(), options)
	}}
	root.Flags().BoolVar(&options.yes, "yes", false, "confirm permanent removal")
	root.Flags().BoolVar(&options.force, "force", false, "terminate verified Portless runtimes even when environments are active or unknown")
	return root
}

// RootCommands returns the administration commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.runtimeCommand(), c.setupCommand(), c.relayCommand(), c.daemonCommand(), c.mcpCommand(), c.doctorCommand(), c.configCommand(), c.resetCommand(), c.uninstallCommand()}
}
