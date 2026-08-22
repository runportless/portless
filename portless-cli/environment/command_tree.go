package environment

import (
	"strings"
	"time"

	shared "github.com/runportless/portless/portless-cli/command"
	"github.com/spf13/cobra"
)

func (c *Commands) upCommand() *cobra.Command {
	options := upOptions{timeout: 10 * time.Minute, open: true, wait: true}
	noOpen, noWait := false, false
	command := &cobra.Command{
		Use:   "up",
		Short: "Prepare an environment for development",
		Long: "Start the environment and prepare it for the way you are working. " +
			"From a registered service directory, Portless starts that service with its debugger enabled and starts the rest normally. " +
			"From a project directory that does not identify one service, it preserves existing launch modes and starts missing services normally.",
		Example: "  portless up\n" +
			"  portless up --debug checkout\n" +
			"  portless up --managed",
		Args: shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			effective := options
			if noOpen {
				effective.open = false
			}
			if noWait {
				effective.wait = false
			}
			return c.up(cmd.Context(), "", effective)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.name, "name", "", "name used when discovering a new project")
	flags.DurationVar(&options.timeout, "timeout", options.timeout, "startup timeout")
	flags.BoolVar(&options.open, "open", options.open, "open the dashboard")
	flags.BoolVar(&noOpen, "no-open", false, "do not open a browser")
	flags.BoolVar(&noWait, "no-wait", false, "return after the operation is accepted")
	flags.StringVar(&options.debug, "debug", "", "start one service with its discovered debugger enabled")
	flags.BoolVar(&options.managed, "managed", false, "start every service in normal managed mode")
	command.MarkFlagsMutuallyExclusive("debug", "managed")
	_ = command.RegisterFlagCompletionFunc("debug", c.Complete(shared.CompletionServices))
	command.MarkFlagsMutuallyExclusive("open", "no-open")
	return command
}

func (c *Commands) downCommand() *cobra.Command {
	options := downOptions{wait: true, timeout: 3 * time.Minute}
	noWait := false
	command := &cobra.Command{
		Use:   "down",
		Short: "Stop one or all environments",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.all && strings.TrimSpace(c.EnvironmentOverride) != "" {
				return shared.UsageError("--all cannot be combined with --env")
			}
			if options.volumes && !options.yes {
				return shared.UsageError("--volumes permanently deletes managed database/cache data; repeat with --yes")
			}
			effective := options
			if noWait {
				effective.wait = false
			}
			return c.down(cmd.Context(), "", effective)
		},
	}
	command.Flags().BoolVar(&options.all, "all", false, "stop every active environment")
	command.Flags().BoolVar(&options.volumes, "volumes", false, "remove managed data volumes")
	command.Flags().BoolVar(&options.yes, "yes", false, "confirm volume deletion")
	command.Flags().BoolVar(&noWait, "no-wait", false, "return after the operation is accepted")
	command.Flags().DurationVar(&options.timeout, "timeout", options.timeout, "shutdown timeout")
	return command
}

func (c *Commands) statusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Show environment status",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.status(cmd.Context(), "", c.JSONOutput)
		},
	}
	return command
}

func (c *Commands) openCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "open [service]",
		Short: "Open an application service in the browser",
		Args:  shared.UsageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.open(cmd.Context(), shared.FirstArg(args))
		},
	}
	command.ValidArgsFunction = c.Complete(shared.CompletionServices)
	return command
}

func (c *Commands) urlCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "url [service]",
		Short: "Print an application service URL",
		Args:  shared.UsageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.printURL(cmd.Context(), shared.FirstArg(args))
		},
	}
	command.ValidArgsFunction = c.Complete(shared.CompletionServices)
	return command
}

func (c *Commands) uiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the Portless control plane",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.ui(cmd.Context())
		},
	}
}

// RootCommands returns the environment commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.upCommand(), c.downCommand(), c.statusCommand(), c.openCommand(), c.urlCommand(), c.uiCommand()}
}
