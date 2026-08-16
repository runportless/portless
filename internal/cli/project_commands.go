package cli

import (
	"github.com/portless-run/portless/internal/model"
	"github.com/spf13/cobra"
)

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
