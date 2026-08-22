package observe

import (
	"strings"
	"time"

	shared "github.com/runportless/portless/portless-cli/command"
	"github.com/spf13/cobra"
)

func (c *Commands) serviceCommand() *cobra.Command {
	root := shared.CommandGroup("service", "Inspect and manage services")
	listOptions := listOptions{limit: 250}
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List services", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.listServices(cmd.Context(), listOptions.limit)
	}}
	list.Flags().IntVar(&listOptions.limit, "limit", listOptions.limit, "maximum services")
	show := &cobra.Command{Use: "show <service>", Short: "Show service details", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showService(cmd.Context(), args[0])
	}}
	config := &cobra.Command{Use: "config <service>", Short: "Show effective service configuration", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showServiceConfiguration(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionServices)
	config.ValidArgsFunction = c.Complete(shared.CompletionServices)
	root.AddCommand(list, show, config)
	for _, actionName := range []string{"start", "stop", "restart", "debug", "manage"} {
		action := actionName
		options := &serviceActionOptions{wait: true, timeout: 2 * time.Minute}
		noWait := false
		short := strings.ToUpper(action[:1]) + action[1:] + " a service"
		if action == "manage" {
			short = "Restart a service in normal managed mode"
		} else if action == "debug" {
			short = "Restart a service with its debugger enabled"
		}
		child := &cobra.Command{Use: action + " <service>", Short: short, Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
			effective := *options
			if noWait {
				effective.wait = false
			}
			return c.serviceAction(cmd.Context(), action, args[0], effective)
		}}
		child.Flags().BoolVar(&noWait, "no-wait", false, "return after the operation is accepted")
		child.Flags().DurationVar(&options.timeout, "timeout", options.timeout, "time to wait for completion")
		child.ValidArgsFunction = c.Complete(shared.CompletionServices)
		root.AddCommand(child)
	}
	return root
}

func (c *Commands) connectionCommand() *cobra.Command {
	root := shared.CommandGroup("connection", "Inspect effective service connections")
	options := listOptions{limit: 250}
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List effective connections", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.listConnections(cmd.Context(), options.limit)
	}}
	list.Flags().IntVar(&options.limit, "limit", options.limit, "maximum connections")
	show := &cobra.Command{Use: "show <source:target>", Short: "Explain one effective connection", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showConnection(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionConnections)
	root.AddCommand(list, show)
	return root
}

func (c *Commands) timelineCommand() *cobra.Command {
	options := listOptions{limit: 50}
	root := &cobra.Command{Use: "timeline", Short: "Show durable environment history", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.timeline(cmd.Context(), options.limit)
	}}
	root.Flags().IntVar(&options.limit, "limit", options.limit, "maximum timeline events")
	return root
}

func (c *Commands) logsCommand() *cobra.Command {
	options := logsOptions{limit: 500}
	root := &cobra.Command{Use: "logs [service]", Short: "Read logs from every service or one named service", Args: shared.UsageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.logs(cmd.Context(), shared.FirstArg(args), options)
	}}
	root.Flags().BoolVarP(&options.tail, "tail", "t", false, "keep streaming new log lines")
	root.Flags().IntVar(&options.limit, "limit", options.limit, "maximum log entries")
	root.Flags().DurationVar(&options.since, "since", 0, "only show entries this recent, such as 10m")
	root.Flags().BoolVar(&options.timestamps, "timestamps", false, "show timestamps in human-readable output")
	root.ValidArgsFunction = c.Complete(shared.CompletionServices)
	return root
}

// RootCommands returns the observability commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.logsCommand(), c.timelineCommand(), c.serviceCommand(), c.connectionCommand()}
}
