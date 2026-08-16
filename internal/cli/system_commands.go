package cli

import (
	"time"

	"github.com/portless-run/portless/internal/daemon/control"
	"github.com/portless-run/portless/internal/diagnostics"
	"github.com/spf13/cobra"
)

func (c *CLI) configCommand() *cobra.Command {
	command := commandGroup("config", "Manage CLI preferences")
	color := &cobra.Command{
		Use:               "color [auto|always|never]",
		Short:             "Show or save the color preference",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgs:         []string{string(colorAuto), string(colorAlways), string(colorNever)},
		ValidArgsFunction: fixedCompletions(string(colorAuto), string(colorAlways), string(colorNever)),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				preference, err := parseColorPreference(args[0])
				if err != nil {
					return err
				}
				if err := c.saveColorPreference(preference); err != nil {
					return err
				}
			}
			return c.printColorConfig()
		},
	}
	reset := &cobra.Command{
		Use:   "reset",
		Short: "Reset all CLI preferences to built-in defaults",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			return c.resetPreferences()
		},
	}
	command.AddCommand(color, reset)
	return command
}

func (c *CLI) daemonCommand() *cobra.Command {
	command := commandGroup("daemon", "Inspect, stop, or restart the local Portless daemon")

	status := &cobra.Command{
		Use:   "status",
		Short: "Authenticate the daemon and show its identity",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.daemonStatus(cmd.Context(), c.jsonOutput)
		},
	}
	command.AddCommand(status)

	stopOptions := control.StopOptions{Timeout: 15 * time.Second}
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Gracefully stop the authenticated daemon",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.stopDaemon(cmd.Context(), stopOptions, c.jsonOutput)
		},
	}
	stop.Flags().BoolVar(&stopOptions.Force, "force", false, "stop despite active environments or use the guarded legacy fallback")
	stop.Flags().DurationVar(&stopOptions.Timeout, "timeout", stopOptions.Timeout, "time to wait for graceful shutdown")
	command.AddCommand(stop)

	restartOptions := control.StopOptions{Timeout: 15 * time.Second}
	restart := &cobra.Command{
		Use:   "restart",
		Short: "Stop the authenticated daemon and start the current build",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.restartDaemon(cmd.Context(), restartOptions, c.jsonOutput)
		},
	}
	restart.Flags().BoolVar(&restartOptions.Force, "force", false, "restart despite active environments or replace a guarded legacy daemon")
	restart.Flags().DurationVar(&restartOptions.Timeout, "timeout", restartOptions.Timeout, "time to wait for graceful shutdown")
	command.AddCommand(restart)

	return command
}

func (c *CLI) doctorCommand() *cobra.Command {
	command := &cobra.Command{
		Use:               "doctor [daemon|relay|runtime]",
		Short:             "Diagnose the local Portless installation",
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgs:         []string{"daemon", "relay", "runtime"},
		ValidArgsFunction: fixedCompletions("daemon", "relay", "runtime"),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := diagnostics.ParseScope(firstArg(args))
			if err != nil {
				return usageError("%v", err)
			}
			return c.doctor(cmd.Context(), scope, c.jsonOutput)
		},
	}
	return command
}

func (c *CLI) setupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Configure clean HTTP URLs and TCP endpoint DNS",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.installRelay(cmd.Context(), c.jsonOutput)
		},
	}
}

func (c *CLI) relayCommand() *cobra.Command {
	command := commandGroup("relay", "Manage clean local endpoint networking")
	install := &cobra.Command{
		Use:   "install",
		Short: "Install or repair HTTP ingress and TCP endpoint DNS",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.installRelay(cmd.Context(), c.jsonOutput)
		},
	}
	command.AddCommand(install)

	status := &cobra.Command{
		Use:   "status",
		Short: "Show local HTTP and DNS relay health",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.relayStatus(cmd.Context(), c.jsonOutput)
		},
	}
	command.AddCommand(status)

	restart := &cobra.Command{
		Use:   "restart",
		Short: "Restart the installed HTTP and DNS relay",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.restartRelay(cmd.Context(), c.jsonOutput)
		},
	}
	command.AddCommand(restart)

	force := false
	uninstall := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"remove"},
		Short:   "Remove only the privileged HTTP and DNS relay",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.uninstallRelay(cmd.Context(), force, c.jsonOutput)
		},
	}
	uninstall.Flags().BoolVar(&force, "force", false, "remove an installation owned by another or unknown user")
	command.AddCommand(uninstall)
	return command
}
