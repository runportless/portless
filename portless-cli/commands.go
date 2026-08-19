package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	shared "github.com/portless-run/portless/portless-cli/command"
	"github.com/spf13/cobra"
)

const (
	rootGroupRun       = "run"
	rootGroupInspect   = "inspect"
	rootGroupConfigure = "configure"
	rootGroupTest      = "test"
	rootGroupSystem    = "system"
	rootGroupOther     = "other"
)

// Run executes one CLI invocation and returns its process exit code without
// terminating the calling process.
func (c *CLI) Run(ctx context.Context, args []string) int {
	c.context.CompletionCache = nil
	c.context.JSONOutput = shared.BoolFlagRequested(args, "json")
	c.context.NoColor = shared.BoolFlagRequested(args, "no-color")
	c.context.CompletionOutput = shared.IsCompletionRequest(args)
	if err := c.context.LoadPreferences(); err != nil {
		if !shared.IsConfigResetRequest(args) {
			c.context.PrintError(err)
			return 1
		}
		c.context.ColorPreference = shared.ColorAuto
		c.context.ColorSource = "default"
	}
	root := c.rootCommand()
	if c.context.JSONOutput {
		encoded, _ := json.MarshalIndent(map[string]string{"version": Version}, "", "  ")
		root.SetVersionTemplate(string(encoded) + "\n")
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		var help *shared.HelpError
		if errors.As(err, &help) {
			if helpErr := help.Command.Help(); helpErr != nil {
				c.context.PrintError(helpErr)
				return 1
			}
			return 0
		}
		var reported *shared.ReportedError
		if errors.As(err, &reported) {
			return 1
		}
		var usage *shared.UsageFailure
		if errors.As(err, &usage) || shared.IsCobraSyntaxError(err) {
			c.context.PrintError(err)
			if !c.context.JSONOutput {
				selected := commandForUsage(root, args)
				if usage != nil && usage.Command != nil {
					selected = usage.Command
				}
				fmt.Fprintln(c.Err)
				fmt.Fprint(c.Err, selected.UsageString())
			}
			return 2
		}
		c.context.PrintError(err)
		return 1
	}
	return 0
}

func commandForUsage(root *cobra.Command, args []string) *cobra.Command {
	selected, _, _ := root.Find(args)
	if selected == nil {
		return root
	}
	return selected
}

func (c *CLI) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use: "portless", Short: "Run complete local application environments without port conflicts",
		Long:    "Portless is a local application-environment control plane that discovers, runs, observes, and modifies complete environments without requiring a project file.",
		Version: Version, SilenceErrors: true, SilenceUsage: true, Args: shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	root.SetOut(c.Out)
	root.SetErr(c.Err)
	root.SetVersionTemplate("portless {{.Version}}\n")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &shared.UsageFailure{Err: err, Command: cmd}
	})
	root.SetUsageTemplate(c.context.UsageTemplate())
	root.PersistentFlags().BoolVar(&c.context.JSONOutput, "json", c.context.JSONOutput, "emit JSON (JSON Lines for streaming commands)")
	root.PersistentFlags().BoolVar(&c.context.NoColor, "no-color", c.context.NoColor, "disable color for this invocation")
	root.PersistentFlags().StringVar(&c.context.EnvironmentOverride, "env", "", "use project/environment for this invocation without changing the checkout selection")
	_ = root.RegisterFlagCompletionFunc("env", c.context.Complete(shared.CompletionEnvironments))
	root.CompletionOptions.DisableDescriptions = true
	root.AddGroup(
		&cobra.Group{ID: rootGroupRun, Title: c.context.Heading(c.Out, "Environment:")},
		&cobra.Group{ID: rootGroupInspect, Title: c.context.Heading(c.Out, "Observe:")},
		&cobra.Group{ID: rootGroupConfigure, Title: c.context.Heading(c.Out, "Projects:")},
		&cobra.Group{ID: rootGroupTest, Title: c.context.Heading(c.Out, "Traffic:")},
		&cobra.Group{ID: rootGroupSystem, Title: c.context.Heading(c.Out, "Administration:")},
		&cobra.Group{ID: rootGroupOther, Title: c.context.Heading(c.Out, "Help:")},
	)
	root.SetHelpCommandGroupID(rootGroupOther)
	root.SetCompletionCommandGroupID(rootGroupOther)
	addRootCommands(root, rootGroupRun, c.environment.RootCommands())
	addRootCommands(root, rootGroupInspect, c.observe.RootCommands())
	addRootCommands(root, rootGroupConfigure, c.projects.RootCommands())
	for _, child := range c.traffic.RootCommands() {
		group := rootGroupTest
		if child.Name() == "traffic" {
			group = rootGroupInspect
		}
		root.AddCommand(inRootGroup(group, child))
	}
	addRootCommands(root, rootGroupTest, c.mocks.RootCommands())
	addRootCommands(root, rootGroupSystem, c.administration.RootCommands())
	return root
}

func addRootCommands(root *cobra.Command, groupID string, commands []*cobra.Command) {
	for _, child := range commands {
		root.AddCommand(inRootGroup(groupID, child))
	}
}

func inRootGroup(groupID string, command *cobra.Command) *cobra.Command {
	command.GroupID = groupID
	return command
}
