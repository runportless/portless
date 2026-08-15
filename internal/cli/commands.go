package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/bootstrap"
	"github.com/portless-run/portless/internal/diagnostics"
	"github.com/portless-run/portless/internal/model"
	"github.com/spf13/cobra"
)

type commandUsageError struct {
	err     error
	command *cobra.Command
}

func (e *commandUsageError) Error() string { return e.err.Error() }
func (e *commandUsageError) Unwrap() error { return e.err }

type commandHelpError struct {
	command *cobra.Command
}

func (e *commandHelpError) Error() string { return "required arguments were omitted" }

type reportedCommandError struct{}

func (*reportedCommandError) Error() string { return "command reported failures" }

const (
	rootGroupRun       = "run"
	rootGroupInspect   = "inspect"
	rootGroupConfigure = "configure"
	rootGroupTest      = "test"
	rootGroupSystem    = "system"
	rootGroupOther     = "other"
)

func usageError(message string, arguments ...any) error {
	return &commandUsageError{err: fmt.Errorf(message, arguments...)}
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < requiredArgumentCount(cmd.Use) {
			return &commandHelpError{command: cmd}
		}
		if err := validate(cmd, args); err != nil {
			return &commandUsageError{err: err, command: cmd}
		}
		return nil
	}
}

type upOptions struct {
	name    string
	timeout time.Duration
	open    bool
	wait    bool
}

type downOptions struct {
	all     bool
	volumes bool
	yes     bool
	wait    bool
	timeout time.Duration
}

type resetOptions struct {
	yes   bool
	force bool
}

type uninstallOptions struct {
	yes   bool
	force bool
}

type logsOptions struct {
	tail       bool
	limit      int
	since      time.Duration
	timestamps bool
}

type trafficOptions struct {
	tail     bool
	protocol string
	limit    int
	service  string
	edge     string
}

type listOptions struct {
	limit int
}

type serviceActionOptions struct {
	wait    bool
	timeout time.Duration
}

type exportOptions struct {
	output string
	force  bool
}

type recordingOptions struct {
	edge      string
	duration  time.Duration
	maxEvents int64
}

type faultOptions struct {
	latency     int64
	jitter      int64
	status      int
	abort       bool
	probability float64
	method      string
	path        string
	duration    time.Duration
}

type bindingOptions struct {
	provider       model.ProviderKind
	source         string
	remoteURL      string
	classification model.RemoteClassification
	writePolicy    model.WritePolicy
	healthPath     string
}

func (c *CLI) Run(ctx context.Context, args []string) int {
	c.completionCache = nil
	c.jsonOutput = jsonFlagRequested(args)
	c.noColor = boolFlagRequested(args, "no-color")
	c.completionOutput = isCompletionRequest(args)
	if err := c.loadPreferences(); err != nil {
		if !isConfigResetRequest(args) {
			c.printError(err)
			return 1
		}
		c.colorPreference = colorAuto
		c.colorSource = "default"
	}
	root := c.rootCommand()
	if c.jsonOutput {
		encoded, _ := json.MarshalIndent(map[string]string{"version": Version}, "", "  ")
		root.SetVersionTemplate(string(encoded) + "\n")
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		var help *commandHelpError
		if errors.As(err, &help) {
			if helpErr := help.command.Help(); helpErr != nil {
				c.printError(helpErr)
				return 1
			}
			return 0
		}
		var reported *reportedCommandError
		if errors.As(err, &reported) {
			return 1
		}
		var usage *commandUsageError
		if errors.As(err, &usage) || isCobraSyntaxError(err) {
			c.printError(err)
			if !c.jsonOutput {
				command := commandForUsage(root, args)
				if usage != nil && usage.command != nil {
					command = usage.command
				}
				fmt.Fprintln(c.Err)
				fmt.Fprint(c.Err, command.UsageString())
			}
			return 2
		}
		c.printError(err)
		return 1
	}
	return 0
}

func commandForUsage(root *cobra.Command, args []string) *cobra.Command {
	command, _, _ := root.Find(args)
	if command == nil {
		return root
	}
	return command
}

func requiredArgumentCount(use string) int {
	count := 0
	for _, field := range strings.Fields(use) {
		if strings.HasPrefix(field, "<") {
			count++
		}
	}
	return count
}

func isCobraSyntaxError(err error) bool {
	message := err.Error()
	return strings.HasPrefix(message, "unknown command ") ||
		strings.HasPrefix(message, "unknown flag: ") ||
		strings.HasPrefix(message, "required flag(s) ") ||
		strings.HasPrefix(message, "at least one of the flags in the group ") ||
		strings.Contains(message, " if any flags in the group ") ||
		strings.Contains(message, " were all set")
}

func (c *CLI) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "portless",
		Short:         "Run complete local application environments without port conflicts",
		Long:          "Portless is a local application-environment control plane that discovers, runs, observes, and modifies complete environments without requiring a project file.",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetOut(c.Out)
	root.SetErr(c.Err)
	root.SetVersionTemplate("portless {{.Version}}\n")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &commandUsageError{err: err, command: cmd}
	})
	root.SetUsageTemplate(c.usageTemplate())
	root.PersistentFlags().BoolVar(&c.jsonOutput, "json", c.jsonOutput, "emit JSON (JSON Lines for streaming commands)")
	root.PersistentFlags().BoolVar(&c.noColor, "no-color", c.noColor, "disable color for this invocation")
	root.PersistentFlags().StringVar(&c.environmentOverride, "env", "", "use project/environment for this invocation without changing the checkout selection")
	_ = root.RegisterFlagCompletionFunc("env", c.complete(completionEnvironments))
	root.CompletionOptions.DisableDescriptions = true
	root.AddGroup(
		&cobra.Group{ID: rootGroupRun, Title: c.heading(c.Out, "Environment:")},
		&cobra.Group{ID: rootGroupInspect, Title: c.heading(c.Out, "Observe:")},
		&cobra.Group{ID: rootGroupConfigure, Title: c.heading(c.Out, "Projects:")},
		&cobra.Group{ID: rootGroupTest, Title: c.heading(c.Out, "Traffic:")},
		&cobra.Group{ID: rootGroupSystem, Title: c.heading(c.Out, "Administration:")},
		&cobra.Group{ID: rootGroupOther, Title: c.heading(c.Out, "Help:")},
	)
	root.SetHelpCommandGroupID(rootGroupOther)
	root.SetCompletionCommandGroupID(rootGroupOther)
	root.AddCommand(
		inRootGroup(rootGroupRun, c.upCommand()),
		inRootGroup(rootGroupRun, c.downCommand()),
		inRootGroup(rootGroupRun, c.statusCommand()),
		inRootGroup(rootGroupRun, c.openCommand()),
		inRootGroup(rootGroupRun, c.urlCommand()),
		inRootGroup(rootGroupRun, c.uiCommand()),
		inRootGroup(rootGroupInspect, c.logsCommand()),
		inRootGroup(rootGroupInspect, c.trafficCommand()),
		inRootGroup(rootGroupInspect, c.timelineCommand()),
		inRootGroup(rootGroupInspect, c.serviceCommand()),
		inRootGroup(rootGroupInspect, c.connectionCommand()),
		inRootGroup(rootGroupConfigure, c.projectCommand()),
		inRootGroup(rootGroupConfigure, c.environmentCommand()),
		inRootGroup(rootGroupTest, c.recordCommand()),
		inRootGroup(rootGroupTest, c.faultCommand()),
		inRootGroup(rootGroupSystem, c.runtimeCommand()),
		inRootGroup(rootGroupSystem, c.setupCommand()),
		inRootGroup(rootGroupSystem, c.relayCommand()),
		inRootGroup(rootGroupSystem, c.daemonCommand()),
		inRootGroup(rootGroupSystem, c.doctorCommand()),
		inRootGroup(rootGroupSystem, c.configCommand()),
		inRootGroup(rootGroupSystem, c.resetCommand()),
		inRootGroup(rootGroupSystem, c.uninstallCommand()),
	)
	return root
}

func inRootGroup(groupID string, command *cobra.Command) *cobra.Command {
	command.GroupID = groupID
	return command
}

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

	stopOptions := bootstrap.StopOptions{Timeout: 15 * time.Second}
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

	restartOptions := bootstrap.StopOptions{Timeout: 15 * time.Second}
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

func (c *CLI) upCommand() *cobra.Command {
	options := upOptions{timeout: 10 * time.Minute, open: true, wait: true}
	noOpen, noWait := false, false
	command := &cobra.Command{
		Use:   "up",
		Short: "Start an environment",
		Args:  usageArgs(cobra.NoArgs),
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
	for _, action := range []string{"start", "stop", "restart"} {
		action := action
		options := &serviceActionOptions{wait: true, timeout: 2 * time.Minute}
		noWait := false
		child := &cobra.Command{Use: action + " <service>", Short: strings.ToUpper(action[:1]) + action[1:] + " a service", Args: usageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
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

func (c *CLI) projectCommand() *cobra.Command {
	command := commandGroup("project", "Manage logical projects")
	projectListOptions := listOptions{limit: 100}
	projectList := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List projects", Args: usageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.listProjects(cmd.Context(), projectListOptions.limit)
	}}
	projectList.Flags().IntVar(&projectListOptions.limit, "limit", projectListOptions.limit, "maximum projects")
	projectShow := &cobra.Command{Use: "show [project]", Short: "Show project sources, environments, services, and connections", Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error { return c.showProject(cmd.Context(), firstArg(args)) }}
	projectShow.ValidArgsFunction = c.complete(completionProjects)
	command.AddCommand(projectList, projectShow)

	var sources []string
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project from one or more source checkouts",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.createProject(cmd.Context(), args[0], sources)
		},
	}
	create.Flags().StringArrayVar(&sources, "source", nil, "source checkout as name=path (repeatable)")
	_ = create.MarkFlagRequired("source")
	command.AddCommand(create)

	sourceGroup := commandGroup("source", "Manage project sources")
	addPath := ""
	add := &cobra.Command{
		Use:     "add <name>",
		Short:   "Add a source checkout to the current project",
		Long:    "Discover a checkout and add its services to the logical project. All project environments must be stopped. The path is bound only to the selected environment; configure every other environment explicitly.",
		Example: "  portless project source add inventory --path ../inventory\n  portless --env store/local project source add inventory --path ../inventory",
		Args:    usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.addProjectSource(cmd.Context(), args[0], addPath)
		},
	}
	add.Flags().StringVar(&addPath, "path", "", "source checkout path")
	_ = add.MarkFlagRequired("path")
	sourceGroup.AddCommand(add)
	command.AddCommand(sourceGroup)

	output := "portless.project.json"
	export := &cobra.Command{
		Use:   "export",
		Short: "Export the current project declaration",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.exportProject(cmd.Context(), output)
		},
	}
	export.Flags().StringVarP(&output, "output", "o", output, "output path, or - for stdout")
	command.AddCommand(export)
	command.AddCommand(&cobra.Command{
		Use:   "rename <new-name>",
		Short: "Rename the current project",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.renameProject(cmd.Context(), args[0])
		},
	})
	yes := false
	forget := &cobra.Command{
		Use:   "forget",
		Short: "Remove the current project and all of its metadata",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return usageError("forget removes all project environments and metadata; repeat with --yes")
			}
			return c.forgetProject(cmd.Context())
		},
	}
	forget.Flags().BoolVar(&yes, "yes", false, "confirm project removal")
	command.AddCommand(forget)
	return command
}

func (c *CLI) environmentCommand() *cobra.Command {
	command := commandGroup("env", "Manage project environments")
	command.Aliases = []string{"environment"}

	selectCommand := &cobra.Command{
		Use:               "select <project/environment>",
		Short:             "Select an environment for the current checkout",
		Example:           "  portless env select billing/local",
		Args:              usageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: c.complete(completionEnvironments),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.selectEnvironment(cmd.Context(), args[0])
		},
	}
	command.AddCommand(selectCommand)
	command.AddCommand(&cobra.Command{
		Use:   "current",
		Short: "Show the effective environment and how it was resolved",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.showEnvironmentContext(cmd.Context())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear the saved environment selection for the current checkout",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.clearEnvironmentSelection(cmd.Context())
		},
	})

	environmentListOptions := listOptions{limit: 100}
	environmentList := &cobra.Command{
		Use:   "list [project]",
		Short: "List environments",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.listEnvironments(cmd.Context(), firstArg(args), environmentListOptions.limit)
		},
	}
	environmentList.Flags().IntVar(&environmentListOptions.limit, "limit", environmentListOptions.limit, "maximum environments")
	environmentList.ValidArgsFunction = c.complete(completionProjects)
	command.AddCommand(environmentList)

	from := ""
	clone := &cobra.Command{
		Use:   "clone <name>",
		Short: "Clone environment configuration",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.cloneEnvironment(cmd.Context(), args[0], from)
		},
	}
	clone.Flags().StringVar(&from, "from", "", "source environment (defaults to the selected environment)")
	_ = clone.RegisterFlagCompletionFunc("from", c.completeEnvironmentNames())
	command.AddCommand(clone)

	options := bindingOptions{}
	local, remote, classification, writePolicy, container := "", "", string(model.RemoteUnknown), string(model.WriteReadOnly), false
	bind := &cobra.Command{
		Use:   "bind <service>",
		Short: "Choose a local, container, or remote provider",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			options.classification = model.RemoteClassification(classification)
			options.writePolicy = model.WritePolicy(writePolicy)
			switch {
			case cmd.Flags().Changed("local"):
				options.provider, options.source = model.ProviderLocal, local
			case container:
				options.provider = model.ProviderContainer
			case cmd.Flags().Changed("remote"):
				options.provider, options.remoteURL = model.ProviderRemote, remote
			}
			if options.provider != model.ProviderRemote && (cmd.Flags().Changed("classification") || cmd.Flags().Changed("write-policy") || cmd.Flags().Changed("health-path")) {
				return usageError("--classification, --write-policy, and --health-path require --remote")
			}
			if options.provider == model.ProviderRemote {
				if !validRemoteClassification(options.classification) {
					return usageError("classification must be development, qa, staging, or unknown")
				}
				if options.writePolicy != model.WriteReadOnly && options.writePolicy != model.WriteReadWrite {
					return usageError("write policy must be read-only or read-write")
				}
			}
			return c.bindProvider(cmd.Context(), args[0], options)
		},
	}
	bind.Flags().StringVar(&local, "local", "", "run the service from the named source")
	bind.Flags().BoolVar(&container, "container", false, "use a Portless-managed container")
	bind.Flags().StringVar(&remote, "remote", "", "route the service to an HTTP(S) URL")
	bind.Flags().StringVar(&classification, "classification", classification, "remote class: development, qa, staging, or unknown")
	bind.Flags().StringVar(&writePolicy, "write-policy", writePolicy, "remote policy: read-only or read-write")
	bind.Flags().StringVar(&options.healthPath, "health-path", "", "remote readiness path")
	bind.MarkFlagsOneRequired("local", "container", "remote")
	bind.MarkFlagsMutuallyExclusive("local", "container", "remote")
	_ = bind.RegisterFlagCompletionFunc("classification", fixedCompletions("development", "qa", "staging", "unknown"))
	_ = bind.RegisterFlagCompletionFunc("write-policy", fixedCompletions("read-only", "read-write"))
	_ = bind.RegisterFlagCompletionFunc("local", c.complete(completionSources))
	bind.ValidArgsFunction = c.complete(completionServices)
	command.AddCommand(bind)

	sourcePath := ""
	source := &cobra.Command{
		Use:   "source <source>",
		Short: "Point an environment source at another checkout or worktree",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.bindSource(cmd.Context(), args[0], sourcePath)
		},
	}
	source.Flags().StringVar(&sourcePath, "path", "", "source checkout path")
	_ = source.MarkFlagRequired("path")
	source.ValidArgsFunction = c.complete(completionSources)
	command.AddCommand(source)

	command.AddCommand(&cobra.Command{
		Use:   "rescan",
		Short: "Rediscover every source in the selected environment",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.rescanEnvironment(cmd.Context())
		},
	})
	environmentYes := false
	forget := &cobra.Command{
		Use:   "forget",
		Short: "Remove the selected environment and its metadata",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !environmentYes {
				return usageError("forget removes the selected environment and metadata; repeat with --yes")
			}
			return c.forgetEnvironment(cmd.Context())
		},
	}
	forget.Flags().BoolVar(&environmentYes, "yes", false, "confirm environment removal")
	command.AddCommand(forget)
	return command
}

func (c *CLI) runtimeCommand() *cobra.Command {
	command := commandGroup("runtime", "Manage the Docker or Podman container runtime")
	command.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show container runtime status",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runtimeStatus(cmd.Context(), c.jsonOutput)
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the configured container runtime",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.startRuntime(cmd.Context(), c.jsonOutput)
		},
	})
	use := &cobra.Command{
		Use:       "use <auto|docker|podman>",
		Short:     "Select the container runtime",
		Args:      usageArgs(cobra.ExactArgs(1)),
		ValidArgs: []string{"auto", "docker", "podman"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "auto" && args[0] != "docker" && args[0] != "podman" {
				return usageError("runtime must be auto, docker, or podman")
			}
			return c.useRuntime(cmd.Context(), args[0], c.jsonOutput)
		},
	}
	command.AddCommand(use)
	return command
}

func jsonFlagRequested(args []string) bool {
	return boolFlagRequested(args, "json")
}

func commandGroup(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
}

func fixedCompletions(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

func validRemoteClassification(value model.RemoteClassification) bool {
	return value == model.RemoteDevelopment || value == model.RemoteQA || value == model.RemoteStaging || value == model.RemoteUnknown
}
