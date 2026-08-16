package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func (c *CLI) upCommand() *cobra.Command {
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
		Args: usageArgs(cobra.NoArgs),
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
	_ = command.RegisterFlagCompletionFunc("debug", c.complete(completionServices))
	command.MarkFlagsMutuallyExclusive("open", "no-open")
	return command
}

func (c *CLI) downCommand() *cobra.Command {
	options := downOptions{wait: true, timeout: 3 * time.Minute}
	noWait := false
	command := &cobra.Command{
		Use:   "down",
		Short: "Stop one or all environments",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.all && strings.TrimSpace(c.environmentOverride) != "" {
				return usageError("--all cannot be combined with --env")
			}
			if options.volumes && !options.yes {
				return usageError("--volumes permanently deletes managed database/cache data; repeat with --yes")
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

func (c *CLI) resetCommand() *cobra.Command {
	options := resetOptions{}
	command := &cobra.Command{
		Use:   "reset",
		Short: "Erase all projects and local environment data",
		Long:  "Reset Portless to an empty application state. The command preserves CLI preferences, runtime selection, installation identity, authentication, and the localhost relay installation.",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.reset(cmd.Context(), options)
		},
	}
	command.Flags().BoolVar(&options.yes, "yes", false, "confirm permanent deletion")
	command.Flags().BoolVar(&options.force, "force", false, "terminate verified Portless runtimes even when environments are active or unknown")
	return command
}

func (c *CLI) uninstallCommand() *cobra.Command {
	options := uninstallOptions{}
	command := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Portless and all locally managed resources",
		Long:  "Uninstall Portless completely. Without --yes, the command only previews the daemon, relay, resolver, managed runtimes, data, and CLI launcher that would be removed.",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.uninstall(cmd.Context(), options)
		},
	}
	command.Flags().BoolVar(&options.yes, "yes", false, "confirm permanent removal")
	command.Flags().BoolVar(&options.force, "force", false, "terminate verified Portless runtimes even when environments are active or unknown")
	return command
}

func (c *CLI) statusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "status",
		Short: "Show environment status",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.status(cmd.Context(), "", c.jsonOutput)
		},
	}
	return command
}

func (c *CLI) openCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "open [service]",
		Short: "Open an application service in the browser",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.open(cmd.Context(), firstArg(args))
		},
	}
	command.ValidArgsFunction = c.complete(completionServices)
	return command
}

func (c *CLI) urlCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "url [service]",
		Short: "Print an application service URL",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.printURL(cmd.Context(), firstArg(args))
		},
	}
	command.ValidArgsFunction = c.complete(completionServices)
	return command
}

func (c *CLI) uiCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the Portless control plane",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.ui(cmd.Context())
		},
	}
}

func (c *CLI) logsCommand() *cobra.Command {
	options := logsOptions{limit: 500}
	command := &cobra.Command{
		Use:   "logs [service]",
		Short: "Read logs from every service or one named service",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.logs(cmd.Context(), firstArg(args), options)
		},
	}
	command.Flags().BoolVarP(&options.tail, "tail", "t", false, "keep streaming new log lines")
	command.Flags().IntVar(&options.limit, "limit", options.limit, "maximum log entries")
	command.Flags().DurationVar(&options.since, "since", 0, "only show entries this recent, such as 10m")
	command.Flags().BoolVar(&options.timestamps, "timestamps", false, "show timestamps in human-readable output")
	command.ValidArgsFunction = c.complete(completionServices)
	return command
}

func (c *CLI) trafficCommand() *cobra.Command {
	command := commandGroup("traffic", "Inspect local application traffic")
	options := trafficOptions{protocol: "http", limit: 250}
	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List captured application traffic",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.traffic(cmd.Context(), options)
		},
	}
	list.Flags().BoolVarP(&options.tail, "tail", "t", false, "stream live traffic")
	list.Flags().StringVar(&options.protocol, "protocol", options.protocol, "protocol family: http or tcp")
	list.Flags().IntVar(&options.limit, "limit", options.limit, "maximum traffic events")
	list.Flags().StringVar(&options.service, "service", "", "match traffic where the service is either endpoint")
	list.Flags().StringVar(&options.edge, "edge", "", "match one directed source:target edge")
	list.MarkFlagsMutuallyExclusive("service", "edge")
	_ = list.RegisterFlagCompletionFunc("service", c.complete(completionServices))
	_ = list.RegisterFlagCompletionFunc("edge", c.complete(completionConnections))
	show := &cobra.Command{
		Use:   "show <sequence>",
		Short: "Show one captured traffic event",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE:  func(cmd *cobra.Command, args []string) error { return c.showTraffic(cmd.Context(), args[0]) },
	}
	show.ValidArgsFunction = c.complete(completionTraffic)
	command.AddCommand(list, show)
	return command
}
