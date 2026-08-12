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
	volumes bool
	yes     bool
	wait    bool
}

type streamOptions struct {
	tail bool
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
	_ = root.RegisterFlagCompletionFunc("env", cobra.NoFileCompletions)
	root.CompletionOptions.DisableDescriptions = true
	root.AddCommand(
		c.configCommand(),
		c.setupCommand(),
		c.daemonCommand(),
		c.doctorCommand(),
		c.upCommand(),
		c.downCommand(),
		c.statusCommand(),
		c.openCommand(),
		c.uiCommand(),
		c.logsCommand(),
		c.trafficCommand(),
		c.recordCommand(),
		c.faultCommand(),
		c.projectCommand(),
		c.environmentCommand(),
		c.runtimeCommand(),
		c.versionCommand(),
	)
	return root
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
	command := &cobra.Command{
		Use:   "setup",
		Short: "Install, inspect, or remove the localhost port-80 relay",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.setup(cmd.Context(), c.jsonOutput)
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show clean-URL relay installation and health",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.setupStatus(cmd.Context(), c.jsonOutput)
		},
	}
	command.AddCommand(status)

	force := false
	uninstall := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"remove"},
		Short:   "Remove only the privileged clean-URL relay",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.uninstallSetup(cmd.Context(), force, c.jsonOutput)
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
	flags.BoolVar(&options.wait, "wait", options.wait, "wait for readiness")
	flags.BoolVar(&noWait, "no-wait", false, "return after the operation is accepted")
	command.MarkFlagsMutuallyExclusive("open", "no-open")
	command.MarkFlagsMutuallyExclusive("wait", "no-wait")
	return command
}

func (c *CLI) downCommand() *cobra.Command {
	options := downOptions{wait: true}
	command := &cobra.Command{
		Use:   "down",
		Short: "Stop an environment",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.volumes && !options.yes {
				return usageError("--volumes permanently deletes managed database/cache data; repeat with --yes")
			}
			return c.down(cmd.Context(), "", options)
		},
	}
	command.Flags().BoolVar(&options.volumes, "volumes", false, "remove managed data volumes")
	command.Flags().BoolVar(&options.yes, "yes", false, "confirm volume deletion")
	command.Flags().BoolVar(&options.wait, "wait", true, "wait for shutdown")
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
	return &cobra.Command{
		Use:   "open [service]",
		Short: "Open an application service in the browser",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.open(cmd.Context(), firstArg(args))
		},
	}
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
	options := streamOptions{}
	command := &cobra.Command{
		Use:   "logs [service]",
		Short: "Read logs from every service or one named service",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.logs(cmd.Context(), firstArg(args), options)
		},
	}
	command.Flags().BoolVarP(&options.tail, "tail", "t", false, "keep streaming new log lines")
	return command
}

func (c *CLI) trafficCommand() *cobra.Command {
	command := commandGroup("traffic", "Inspect HTTP traffic")
	options := streamOptions{}
	list := &cobra.Command{
		Use:     "list [service|source:target]",
		Aliases: []string{"ls"},
		Short:   "List captured HTTP traffic",
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.traffic(cmd.Context(), firstArg(args), options)
		},
	}
	list.Flags().BoolVarP(&options.tail, "tail", "t", false, "stream live traffic")
	command.AddCommand(list)
	return command
}

func (c *CLI) recordCommand() *cobra.Command {
	command := commandGroup("record", "Capture bounded local traffic recordings")

	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List recordings",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.listRecordings(cmd.Context())
		},
	})

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
	command.AddCommand(start)

	command.AddCommand(&cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a recording, or the active recording when unnamed",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.stopRecording(cmd.Context(), firstArg(args))
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "export <name>",
		Short: "Export a recording as JSON",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.exportRecording(cmd.Context(), args[0])
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a recording",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.deleteRecording(cmd.Context(), args[0])
		},
	})
	return command
}

func (c *CLI) faultCommand() *cobra.Command {
	command := commandGroup("fault", "Introduce scoped failures into local traffic")
	command.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List fault rules",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.listFaults(cmd.Context())
		},
	})

	options := faultOptions{probability: 1, duration: 10 * time.Minute}
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
	add.Flags().DurationVar(&options.duration, "duration", options.duration, "automatic expiry")
	command.AddCommand(add)

	command.AddCommand(&cobra.Command{
		Use:   "disable <name>",
		Short: "Disable a fault rule",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.disableFault(cmd.Context(), args[0])
		},
	})
	clear := &cobra.Command{
		Use:   "clear",
		Short: "Disable all active fault rules",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.clearFaults(cmd.Context())
		},
	}
	clear.Flags().Bool("all", false, "disable all active fault rules")
	command.AddCommand(clear)
	return command
}

func (c *CLI) projectCommand() *cobra.Command {
	command := commandGroup("project", "Manage logical projects")

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

	command.AddCommand(&cobra.Command{
		Use:               "select <project/environment>",
		Short:             "Select an environment for the current checkout",
		Example:           "  portless env select billing/local",
		Args:              usageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.selectEnvironment(cmd.Context(), args[0])
		},
	})
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

	command.AddCommand(&cobra.Command{
		Use:   "list [project]",
		Short: "List environments",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.listEnvironments(cmd.Context(), firstArg(args))
		},
	})

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

func (c *CLI) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Portless version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			if c.jsonOutput {
				return writeJSON(c.Out, map[string]string{"version": Version})
			}
			fmt.Fprintln(c.Out, "portless "+Version)
			return nil
		},
	}
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
