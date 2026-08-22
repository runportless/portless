package projects

import (
	shared "github.com/runportless/portless/portless-cli/command"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/spf13/cobra"
)

func (c *Commands) projectCommand() *cobra.Command {
	command := shared.CommandGroup("project", "Manage logical projects")
	projectListOptions := listOptions{limit: 100}
	projectList := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List projects", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.listProjects(cmd.Context(), projectListOptions.limit)
	}}
	projectList.Flags().IntVar(&projectListOptions.limit, "limit", projectListOptions.limit, "maximum projects")
	projectShow := &cobra.Command{Use: "show [project]", Short: "Show project sources, environments, services, and connections", Args: shared.UsageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.showProject(cmd.Context(), shared.FirstArg(args))
	}}
	projectShow.ValidArgsFunction = c.Complete(shared.CompletionProjects)
	command.AddCommand(projectList, projectShow)

	var sources []string
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project from one or more source checkouts",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.createProject(cmd.Context(), args[0], sources)
		},
	}
	create.Flags().StringArrayVar(&sources, "source", nil, "source checkout as name=path (repeatable)")
	_ = create.MarkFlagRequired("source")
	command.AddCommand(create)

	sourceGroup := shared.CommandGroup("source", "Manage project sources")
	addPath := ""
	add := &cobra.Command{
		Use:     "add <name>",
		Short:   "Add a source to the current project and configure its initial checkout",
		Long:    "Discover a checkout and add its services to the logical project. All project environments must be stopped. The source belongs to the project; its path is configured only for the selected environment. Configure a checkout separately in every other environment that runs services from this source.",
		Example: "  portless project source add inventory --path ../inventory\n  portless --env store/local project source add inventory --path ../inventory",
		Args:    shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.addProjectSource(cmd.Context(), args[0], addPath)
		},
	}
	add.Flags().StringVar(&addPath, "path", "", "source checkout path")
	_ = add.MarkFlagRequired("path")
	deleteYes := false
	deleteSource := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a source and the topology it owns from the current project",
		Long:  "Delete a logical source from the current project. Every project environment must be stopped. Services owned by the source and resources used only by those services are removed from every environment.",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !deleteYes {
				return shared.UsageError("delete removes the source and its owned topology from every project environment; repeat with --yes")
			}
			return c.deleteProjectSource(cmd.Context(), args[0])
		},
	}
	deleteSource.Flags().BoolVar(&deleteYes, "yes", false, "confirm project source removal")
	deleteSource.ValidArgsFunction = c.Complete(shared.CompletionSources)
	sourceGroup.AddCommand(add, deleteSource)
	command.AddCommand(sourceGroup)

	output := "portless.project.json"
	export := &cobra.Command{
		Use:   "export",
		Short: "Export the current project declaration",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.exportProject(cmd.Context(), output)
		},
	}
	export.Flags().StringVarP(&output, "output", "o", output, "output path, or - for stdout")
	command.AddCommand(export)
	command.AddCommand(&cobra.Command{
		Use:   "rename <new-name>",
		Short: "Rename the current project",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.renameProject(cmd.Context(), args[0])
		},
	})
	yes := false
	forget := &cobra.Command{
		Use:   "forget",
		Short: "Remove the current project and all of its metadata",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return shared.UsageError("forget removes all project environments and metadata; repeat with --yes")
			}
			return c.forgetProject(cmd.Context())
		},
	}
	forget.Flags().BoolVar(&yes, "yes", false, "confirm project removal")
	command.AddCommand(forget)
	return command
}

func (c *Commands) environmentCommand() *cobra.Command {
	command := shared.CommandGroup("env", "Manage project environments")
	command.Aliases = []string{"environment"}

	selectCommand := &cobra.Command{
		Use:               "select <project/environment>",
		Short:             "Select an environment for the current checkout",
		Example:           "  portless env select billing/local",
		Args:              shared.UsageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: c.Complete(shared.CompletionEnvironments),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.selectEnvironment(cmd.Context(), args[0])
		},
	}
	command.AddCommand(selectCommand)
	command.AddCommand(&cobra.Command{
		Use:   "current",
		Short: "Show the effective environment and how it was resolved",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.showEnvironmentContext(cmd.Context())
		},
	})
	command.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear the saved environment selection for the current checkout",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.clearEnvironmentSelection(cmd.Context())
		},
	})

	environmentListOptions := listOptions{limit: 100}
	environmentList := &cobra.Command{
		Use:   "list [project]",
		Short: "List environments",
		Args:  shared.UsageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.listEnvironments(cmd.Context(), shared.FirstArg(args), environmentListOptions.limit)
		},
	}
	environmentList.Flags().IntVar(&environmentListOptions.limit, "limit", environmentListOptions.limit, "maximum environments")
	environmentList.ValidArgsFunction = c.Complete(shared.CompletionProjects)
	command.AddCommand(environmentList)

	from := ""
	clone := &cobra.Command{
		Use:   "clone <name>",
		Short: "Clone environment configuration",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.cloneEnvironment(cmd.Context(), args[0], from)
		},
	}
	clone.Flags().StringVar(&from, "from", "", "source environment (defaults to the selected environment)")
	_ = clone.RegisterFlagCompletionFunc("from", c.CompleteEnvironmentNames())
	command.AddCommand(clone)

	options := bindingOptions{}
	local, remote, mockProfile, classification, writePolicy, container := "", "", "", string(model.RemoteUnknown), string(model.WriteReadOnly), false
	bind := &cobra.Command{
		Use:   "bind <service>",
		Short: "Choose a local, container, remote, or mock provider",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
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
			case cmd.Flags().Changed("mock"):
				options.provider, options.mockProfile = model.ProviderMock, mockProfile
			}
			if options.provider != model.ProviderRemote && (cmd.Flags().Changed("classification") || cmd.Flags().Changed("write-policy") || cmd.Flags().Changed("health-path")) {
				return shared.UsageError("--classification, --write-policy, and --health-path require --remote")
			}
			if options.provider == model.ProviderRemote {
				if !validRemoteClassification(options.classification) {
					return shared.UsageError("classification must be development, qa, staging, or unknown")
				}
				if options.writePolicy != model.WriteReadOnly && options.writePolicy != model.WriteReadWrite {
					return shared.UsageError("write policy must be read-only or read-write")
				}
			}
			return c.bindProvider(cmd.Context(), args[0], options)
		},
	}
	bind.Flags().StringVar(&local, "local", "", "run the service from the named source")
	bind.Flags().BoolVar(&container, "container", false, "use a Portless-managed container")
	bind.Flags().StringVar(&remote, "remote", "", "route the service to an HTTP(S) URL")
	bind.Flags().StringVar(&mockProfile, "mock", "", "serve the service from a mock profile")
	bind.Flags().StringVar(&classification, "classification", classification, "remote class: development, qa, staging, or unknown")
	bind.Flags().StringVar(&writePolicy, "write-policy", writePolicy, "remote policy: read-only or read-write")
	bind.Flags().StringVar(&options.healthPath, "health-path", "", "remote readiness path")
	bind.MarkFlagsOneRequired("local", "container", "remote", "mock")
	bind.MarkFlagsMutuallyExclusive("local", "container", "remote", "mock")
	_ = bind.RegisterFlagCompletionFunc("classification", shared.FixedCompletions("development", "qa", "staging", "unknown"))
	_ = bind.RegisterFlagCompletionFunc("write-policy", shared.FixedCompletions("read-only", "read-write"))
	_ = bind.RegisterFlagCompletionFunc("local", c.Complete(shared.CompletionCheckouts))
	_ = bind.RegisterFlagCompletionFunc("mock", c.Complete(shared.CompletionMocks))
	bind.ValidArgsFunction = c.Complete(shared.CompletionServices)
	command.AddCommand(bind)

	checkoutGroup := shared.CommandGroup("checkout", "Manage source checkouts for the selected environment")
	checkoutGroup.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List source checkouts configured for the selected environment",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.listCheckouts(cmd.Context())
		},
	})

	checkoutPath := ""
	setCheckout := &cobra.Command{
		Use:   "set <source>",
		Short: "Configure a source checkout for the selected environment",
		Long:  "Configure a source checkout for the selected environment. Portless rediscovers the selected filesystem path and recompiles the environment, so the environment must be stopped.",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.setCheckout(cmd.Context(), args[0], checkoutPath)
		},
	}
	setCheckout.Flags().StringVar(&checkoutPath, "path", "", "source checkout path")
	_ = setCheckout.MarkFlagRequired("path")
	setCheckout.ValidArgsFunction = c.Complete(shared.CompletionSources)

	removeCheckoutYes := false
	removeCheckout := &cobra.Command{
		Use:   "remove <source>",
		Short: "Remove a source checkout from the selected environment",
		Long:  "Remove only this environment's checkout path. The project source and checkouts configured in other environments remain intact. The environment must be stopped, and no local provider may still use the checkout.",
		Args:  shared.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !removeCheckoutYes {
				return shared.UsageError("remove deletes only the selected environment's checkout path; repeat with --yes")
			}
			return c.removeCheckout(cmd.Context(), args[0])
		},
	}
	removeCheckout.Flags().BoolVar(&removeCheckoutYes, "yes", false, "confirm environment checkout removal")
	removeCheckout.ValidArgsFunction = c.Complete(shared.CompletionCheckouts)
	checkoutGroup.AddCommand(setCheckout, removeCheckout)
	command.AddCommand(checkoutGroup)

	command.AddCommand(&cobra.Command{
		Use:   "rescan",
		Short: "Rediscover every source in the selected environment",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.rescanEnvironment(cmd.Context())
		},
	})
	environmentYes := false
	forget := &cobra.Command{
		Use:   "forget",
		Short: "Remove the selected environment and its metadata",
		Args:  shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !environmentYes {
				return shared.UsageError("forget removes the selected environment and metadata; repeat with --yes")
			}
			return c.forgetEnvironment(cmd.Context())
		},
	}
	forget.Flags().BoolVar(&environmentYes, "yes", false, "confirm environment removal")
	command.AddCommand(forget)
	return command
}

// RootCommands returns the project commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.projectCommand(), c.environmentCommand()}
}

func validRemoteClassification(value model.RemoteClassification) bool {
	return value == model.RemoteDevelopment || value == model.RemoteQA || value == model.RemoteStaging || value == model.RemoteUnknown
}
