package cli

import (
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func (c *CLI) serviceCommand() *cobra.Command {
	command := commandGroup("service", "Inspect and manage services")
	listOptions := listOptions{limit: 250}
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List services", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return c.listServices(cmd.Context(), listOptions.limit) }}
	list.Flags().IntVar(&listOptions.limit, "limit", listOptions.limit, "maximum services")
	show := &cobra.Command{Use: "show <service>", Short: "Show service details", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error { return c.showService(cmd.Context(), args[0]) }}
	config := &cobra.Command{Use: "config <service>", Short: "Show effective service configuration", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showServiceConfiguration(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.complete(completionServices)
	config.ValidArgsFunction = c.complete(completionServices)
	command.AddCommand(list, show, config)
	for _, action := range []string{"start", "stop", "restart", "debug", "manage"} {
		action := action
		options := &serviceActionOptions{wait: true, timeout: 2 * time.Minute}
		noWait := false
		short := strings.ToUpper(action[:1]) + action[1:] + " a service"
		if action == "manage" {
			short = "Restart a service in normal managed mode"
		} else if action == "debug" {
			short = "Restart a service with its debugger enabled"
		}
		child := &cobra.Command{Use: action + " <service>", Short: short, Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			effective := *options
			if noWait {
				effective.wait = false
			}
			return c.serviceAction(cmd.Context(), action, args[0], effective)
		}}
		child.Flags().BoolVar(&noWait, "no-wait", false, "return after the operation is accepted")
		child.Flags().DurationVar(&options.timeout, "timeout", options.timeout, "time to wait for completion")
		child.ValidArgsFunction = c.complete(completionServices)
		command.AddCommand(child)
	}
	return command
}

func (c *CLI) connectionCommand() *cobra.Command {
	command := commandGroup("connection", "Inspect effective service connections")
	options := listOptions{limit: 250}
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List effective connections", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return c.listConnections(cmd.Context(), options.limit) }}
	list.Flags().IntVar(&options.limit, "limit", options.limit, "maximum connections")
	show := &cobra.Command{Use: "show <source:target>", Short: "Explain one effective connection", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error { return c.showConnection(cmd.Context(), args[0]) }}
	show.ValidArgsFunction = c.complete(completionConnections)
	command.AddCommand(list, show)
	return command
}

func (c *CLI) timelineCommand() *cobra.Command {
	options := listOptions{limit: 50}
	command := &cobra.Command{Use: "timeline", Short: "Show durable environment history", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error { return c.timeline(cmd.Context(), options.limit) }}
	command.Flags().IntVar(&options.limit, "limit", options.limit, "maximum timeline events")
	return command
}

func (c *CLI) recordCommand() *cobra.Command {
	command := commandGroup("record", "Capture bounded local traffic recordings")

	recordListOptions := listOptions{limit: 100}
	recordList := &cobra.Command{
		Use:   "list",
		Short: "List recordings",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.listRecordings(cmd.Context(), recordListOptions.limit)
		},
	}
	recordList.Flags().IntVar(&recordListOptions.limit, "limit", recordListOptions.limit, "maximum recordings")
	command.AddCommand(recordList)

	options := recordingOptions{duration: 15 * time.Minute, maxEvents: 10000}
	start := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a bounded recording",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.startRecording(cmd.Context(), args[0], options)
		},
	}
	start.Flags().StringVar(&options.edge, "edge", "", "source:target scope")
	start.Flags().DurationVar(&options.duration, "duration", options.duration, "automatic stop time")
	start.Flags().Int64Var(&options.maxEvents, "max-events", options.maxEvents, "maximum retained events")
	_ = start.RegisterFlagCompletionFunc("edge", c.complete(completionConnections))
	command.AddCommand(start)

	stop := &cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a recording, or the active recording when unnamed",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.stopRecording(cmd.Context(), firstArg(args))
		},
	}
	stop.ValidArgsFunction = c.complete(completionRecordings)
	command.AddCommand(stop)
	show := &cobra.Command{
		Use:   "show <name>",
		Short: "Show recording details",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE:  func(cmd *cobra.Command, args []string) error { return c.showRecording(cmd.Context(), args[0]) },
	}
	show.ValidArgsFunction = c.complete(completionRecordings)
	command.AddCommand(show)
	exportOptions := exportOptions{output: "-"}
	export := &cobra.Command{
		Use:   "export <name>",
		Short: "Export a recording as JSON",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.exportRecording(cmd.Context(), args[0], exportOptions)
		},
	}
	export.Flags().StringVarP(&exportOptions.output, "output", "o", exportOptions.output, "output path, or - for stdout")
	export.Flags().BoolVar(&exportOptions.force, "force", false, "overwrite an existing output file")
	export.ValidArgsFunction = c.complete(completionRecordings)
	command.AddCommand(export)
	deleteYes := false
	deleteCommand := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a recording",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !deleteYes {
				return usageError("recording deletion is permanent; repeat with --yes")
			}
			return c.deleteRecording(cmd.Context(), args[0])
		},
	}
	deleteCommand.Flags().BoolVar(&deleteYes, "yes", false, "confirm recording deletion")
	deleteCommand.ValidArgsFunction = c.complete(completionRecordings)
	command.AddCommand(deleteCommand)
	return command
}

func (c *CLI) faultCommand() *cobra.Command {
	command := commandGroup("fault", "Introduce scoped failures into local traffic")
	faultListOptions := listOptions{limit: 100}
	faultList := &cobra.Command{
		Use:   "list",
		Short: "List fault rules",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.listFaults(cmd.Context(), faultListOptions.limit)
		},
	}
	faultList.Flags().IntVar(&faultListOptions.limit, "limit", faultListOptions.limit, "maximum fault rules")
	command.AddCommand(faultList)

	options := faultOptions{probability: 1}
	add := &cobra.Command{
		Use:   "add <name> <source:target>",
		Short: "Add a fault rule",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.addFault(cmd.Context(), args[0], args[1], options)
		},
	}
	add.Flags().Int64Var(&options.latency, "latency", 0, "latency in milliseconds")
	add.Flags().Int64Var(&options.jitter, "jitter", 0, "maximum jitter in milliseconds")
	add.Flags().IntVar(&options.status, "status", 0, "synthetic HTTP status")
	add.Flags().BoolVar(&options.abort, "abort", false, "abort matching connections")
	add.Flags().Float64Var(&options.probability, "probability", options.probability, "match probability from 0 to 1")
	add.Flags().StringVar(&options.method, "method", "", "HTTP method filter")
	add.Flags().StringVar(&options.path, "path", "", "HTTP path glob")
	add.Flags().DurationVar(&options.duration, "duration", options.duration, "automatically disable after this duration")
	add.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 1 {
			return c.complete(completionConnections)(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	command.AddCommand(add)

	show := &cobra.Command{
		Use:   "show <name>",
		Short: "Show fault rule details",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE:  func(cmd *cobra.Command, args []string) error { return c.showFault(cmd.Context(), args[0]) },
	}
	show.ValidArgsFunction = c.complete(completionFaults)
	command.AddCommand(show)
	enable := &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a saved fault rule",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.enableFault(cmd.Context(), args[0])
		},
	}
	enable.ValidArgsFunction = c.complete(completionFaults)
	command.AddCommand(enable)
	disable := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a fault rule",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.disableFault(cmd.Context(), args[0])
		},
	}
	disable.ValidArgsFunction = c.complete(completionFaults)
	command.AddCommand(disable)
	deleteYes := false
	deleteCommand := &cobra.Command{
		Use:   "delete <name>",
		Short: "Permanently delete a fault rule",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !deleteYes {
				return usageError("fault deletion is permanent; repeat with --yes")
			}
			return c.deleteFault(cmd.Context(), args[0])
		},
	}
	deleteCommand.Flags().BoolVar(&deleteYes, "yes", false, "confirm fault deletion")
	deleteCommand.ValidArgsFunction = c.complete(completionFaults)
	command.AddCommand(deleteCommand)
	clear := &cobra.Command{
		Use:   "clear",
		Short: "Disable all active fault rules",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.clearFaults(cmd.Context())
		},
	}
	command.AddCommand(clear)
	return command
}
