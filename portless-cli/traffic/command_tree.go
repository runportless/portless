package traffic

import (
	"time"

	shared "github.com/portless-run/portless/portless-cli/command"
	"github.com/spf13/cobra"
)

func (c *Commands) trafficCommand() *cobra.Command {
	root := shared.CommandGroup("traffic", "Inspect local application traffic")
	options := trafficOptions{protocol: "all", limit: 250}
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List captured application traffic", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.traffic(cmd.Context(), options)
	}}
	list.Flags().BoolVarP(&options.tail, "tail", "t", false, "stream live traffic")
	list.Flags().StringVar(&options.protocol, "protocol", options.protocol, "protocol family: all, http, or tcp")
	list.Flags().IntVar(&options.limit, "limit", options.limit, "maximum traffic exchanges")
	list.Flags().StringVar(&options.service, "service", "", "match traffic where the service is either endpoint")
	list.Flags().StringVar(&options.edge, "edge", "", "match one directed source:target edge")
	list.MarkFlagsMutuallyExclusive("service", "edge")
	_ = list.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	_ = list.RegisterFlagCompletionFunc("edge", c.Complete(shared.CompletionConnections))
	show := &cobra.Command{Use: "show <sequence>", Short: "Show one captured traffic exchange", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showTraffic(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionTraffic)
	traceOptions := traceOptions{limit: 100}
	traces := &cobra.Command{Use: "traces", Short: "List correlated traffic traces", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.traces(cmd.Context(), traceOptions)
	}}
	traces.Flags().IntVar(&traceOptions.limit, "limit", traceOptions.limit, "maximum traces")
	traces.Flags().StringVar(&traceOptions.service, "service", "", "match traces containing a service")
	traces.Flags().StringVar(&traceOptions.edge, "edge", "", "match traces containing a source:target edge")
	traces.Flags().BoolVar(&traceOptions.includeBackground, "include-background", false, "include browser background activity")
	traces.MarkFlagsMutuallyExclusive("service", "edge")
	_ = traces.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	_ = traces.RegisterFlagCompletionFunc("edge", c.Complete(shared.CompletionConnections))
	trace := &cobra.Command{Use: "trace <number>", Short: "Show one correlated traffic trace", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showTrace(cmd.Context(), args[0])
	}}
	trace.ValidArgsFunction = c.Complete(shared.CompletionTraces)
	root.AddCommand(list, show, traces, trace)
	return root
}

func (c *Commands) recordCommand() *cobra.Command {
	root := shared.CommandGroup("record", "Capture bounded local traffic recordings")
	listOptions := listOptions{limit: 100}
	list := &cobra.Command{Use: "list", Short: "List recordings", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.listRecordings(cmd.Context(), listOptions.limit)
	}}
	list.Flags().IntVar(&listOptions.limit, "limit", listOptions.limit, "maximum recordings")
	root.AddCommand(list)

	options := recordingOptions{duration: 15 * time.Minute, maxEvents: 10000}
	start := &cobra.Command{Use: "start <name>", Short: "Start a bounded recording", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.startRecording(cmd.Context(), args[0], options)
	}}
	start.Flags().StringVar(&options.edge, "edge", "", "source:target scope")
	start.Flags().DurationVar(&options.duration, "duration", options.duration, "automatic stop time")
	start.Flags().Int64Var(&options.maxEvents, "max-events", options.maxEvents, "maximum retained events")
	_ = start.RegisterFlagCompletionFunc("edge", c.Complete(shared.CompletionConnections))
	root.AddCommand(start)

	stop := &cobra.Command{Use: "stop [name]", Short: "Stop a recording, or the active recording when unnamed", Args: shared.UsageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.stopRecording(cmd.Context(), shared.FirstArg(args))
	}}
	stop.ValidArgsFunction = c.Complete(shared.CompletionRecordings)
	root.AddCommand(stop)
	show := &cobra.Command{Use: "show <name>", Short: "Show recording details", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showRecording(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionRecordings)
	root.AddCommand(show)

	exportOptions := exportOptions{output: "-"}
	export := &cobra.Command{Use: "export <name>", Short: "Export a recording as JSON", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.exportRecording(cmd.Context(), args[0], exportOptions)
	}}
	export.Flags().StringVarP(&exportOptions.output, "output", "o", exportOptions.output, "output path, or - for stdout")
	export.Flags().BoolVar(&exportOptions.force, "force", false, "overwrite an existing output file")
	export.ValidArgsFunction = c.Complete(shared.CompletionRecordings)
	root.AddCommand(export)

	deleteYes := false
	deleteCommand := &cobra.Command{Use: "delete <name>", Short: "Delete a recording", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if !deleteYes {
			return shared.UsageError("recording deletion is permanent; repeat with --yes")
		}
		return c.deleteRecording(cmd.Context(), args[0])
	}}
	deleteCommand.Flags().BoolVar(&deleteYes, "yes", false, "confirm recording deletion")
	deleteCommand.ValidArgsFunction = c.Complete(shared.CompletionRecordings)
	root.AddCommand(deleteCommand)
	return root
}

func (c *Commands) faultCommand() *cobra.Command {
	root := shared.CommandGroup("fault", "Introduce scoped failures into local traffic")
	listOptions := listOptions{limit: 100}
	list := &cobra.Command{Use: "list", Short: "List fault rules", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.listFaults(cmd.Context(), listOptions.limit)
	}}
	list.Flags().IntVar(&listOptions.limit, "limit", listOptions.limit, "maximum fault rules")
	root.AddCommand(list)

	options := faultOptions{probability: 1}
	add := &cobra.Command{Use: "add <name> <source:target>", Short: "Add a fault rule", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.addFault(cmd.Context(), args[0], args[1], options)
	}}
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
			return c.Complete(shared.CompletionConnections)(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	root.AddCommand(add)

	show := &cobra.Command{Use: "show <name>", Short: "Show fault rule details", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showFault(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionFaults)
	root.AddCommand(show)
	enable := &cobra.Command{Use: "enable <name>", Short: "Enable a saved fault rule", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.enableFault(cmd.Context(), args[0])
	}}
	enable.ValidArgsFunction = c.Complete(shared.CompletionFaults)
	root.AddCommand(enable)
	disable := &cobra.Command{Use: "disable <name>", Short: "Disable a fault rule", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.disableFault(cmd.Context(), args[0])
	}}
	disable.ValidArgsFunction = c.Complete(shared.CompletionFaults)
	root.AddCommand(disable)

	deleteYes := false
	deleteCommand := &cobra.Command{Use: "delete <name>", Short: "Permanently delete a fault rule", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if !deleteYes {
			return shared.UsageError("fault deletion is permanent; repeat with --yes")
		}
		return c.deleteFault(cmd.Context(), args[0])
	}}
	deleteCommand.Flags().BoolVar(&deleteYes, "yes", false, "confirm fault deletion")
	deleteCommand.ValidArgsFunction = c.Complete(shared.CompletionFaults)
	root.AddCommand(deleteCommand)
	clear := &cobra.Command{Use: "clear", Short: "Disable all active fault rules", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.clearFaults(cmd.Context())
	}}
	root.AddCommand(clear)
	return root
}

// RootCommands returns the traffic commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.trafficCommand(), c.recordCommand(), c.faultCommand()}
}
