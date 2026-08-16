package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	debug   string
	managed bool
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
