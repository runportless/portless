package mocks

import (
	shared "github.com/runportless/portless/portless-cli/command"
	"github.com/spf13/cobra"
)

func (c *Commands) mockCommand() *cobra.Command {
	root := shared.CommandGroup("mock", "Build and activate multi-service HTTP scenarios")
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List mock scenarios", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.list(cmd.Context())
	}}
	root.AddCommand(list)

	show := &cobra.Command{Use: "show <scenario>", Short: "Show a mock scenario and its routes", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.show(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionMockScenarios)
	root.AddCommand(show)

	description := ""
	create := &cobra.Command{Use: "create <scenario>", Short: "Create an empty mock scenario", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.create(cmd.Context(), args[0], description)
	}}
	create.Flags().StringVar(&description, "description", "", "optional scenario description")
	root.AddCommand(create)

	root.AddCommand(c.activationCommand("enable", true), c.activationCommand("disable", false))

	deleteYes := false
	deleteCommand := &cobra.Command{Use: "delete <scenario>", Short: "Delete a disabled mock scenario", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if !deleteYes {
			return shared.UsageError("mock scenario deletion is permanent; repeat with --yes")
		}
		return c.delete(cmd.Context(), args[0])
	}}
	deleteCommand.Flags().BoolVar(&deleteYes, "yes", false, "confirm mock scenario deletion")
	deleteCommand.ValidArgsFunction = c.Complete(shared.CompletionMockScenarios)
	root.AddCommand(deleteCommand)

	route := shared.CommandGroup("route", "Manage deterministic routes in a mock scenario")
	options := routeOptions{method: "GET", path: "/", status: 200}
	set := &cobra.Command{Use: "set <scenario> <route>", Short: "Create or replace a mock route", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.setRoute(cmd.Context(), args[0], args[1], options)
	}}
	set.Flags().StringVar(&options.service, "service", "", "service this route mocks")
	set.Flags().StringVar(&options.method, "method", options.method, "HTTP method to match")
	set.Flags().StringVar(&options.path, "path", options.path, "exact or parameterized path to match")
	set.Flags().StringArrayVar(&options.query, "query", nil, "required query matcher as name=value (repeatable)")
	set.Flags().IntVar(&options.status, "status", options.status, "fixed HTTP response status")
	set.Flags().StringArrayVar(&options.header, "header", nil, "response header as name=value (repeatable)")
	set.Flags().StringVar(&options.body, "body", "", "fixed response body")
	set.Flags().StringVar(&options.bodyFile, "body-file", "", "read the fixed response body from a file")
	set.Flags().Int64Var(&options.delay, "delay", 0, "fixed response delay in milliseconds")
	set.Flags().BoolVar(&options.disabled, "disabled", false, "save the route without matching it")
	set.MarkFlagsMutuallyExclusive("body", "body-file")
	_ = set.MarkFlagRequired("service")
	_ = set.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	set.ValidArgsFunction = completeScenarioFirst(c)
	route.AddCommand(set)

	routeDeleteYes := false
	routeDelete := &cobra.Command{Use: "delete <scenario> <route>", Short: "Delete a route from a mock scenario", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if !routeDeleteYes {
			return shared.UsageError("mock route deletion is permanent; repeat with --yes")
		}
		return c.deleteRoute(cmd.Context(), args[0], args[1])
	}}
	routeDelete.Flags().BoolVar(&routeDeleteYes, "yes", false, "confirm mock route deletion")
	routeDelete.ValidArgsFunction = completeScenarioFirst(c)
	route.AddCommand(routeDelete)
	root.AddCommand(route)

	preview := previewOptions{method: "GET", path: "/"}
	previewCommand := &cobra.Command{Use: "preview <scenario>", Short: "Show which service route would match a request", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.preview(cmd.Context(), args[0], preview)
	}}
	previewCommand.Flags().StringVar(&preview.service, "service", "", "service receiving the request")
	previewCommand.Flags().StringVar(&preview.method, "method", preview.method, "HTTP method")
	previewCommand.Flags().StringVar(&preview.path, "path", preview.path, "request path")
	previewCommand.Flags().StringArrayVar(&preview.query, "query", nil, "query value as name=value (repeatable)")
	previewCommand.Flags().StringArrayVar(&preview.header, "header", nil, "request header as name=value (repeatable)")
	previewCommand.Flags().StringVar(&preview.body, "body", "", "request body")
	previewCommand.Flags().StringVar(&preview.bodyFile, "body-file", "", "read the request body from a file")
	previewCommand.MarkFlagsMutuallyExclusive("body", "body-file")
	_ = previewCommand.MarkFlagRequired("service")
	_ = previewCommand.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	previewCommand.ValidArgsFunction = c.Complete(shared.CompletionMockScenarios)
	root.AddCommand(previewCommand)

	root.AddCommand(c.importCommand())
	return root
}

func (c *Commands) activationCommand(action string, enabled bool) *cobra.Command {
	command := &cobra.Command{Use: action + " <scenario>", Short: action + " every service covered by a mock scenario", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.setEnabled(cmd.Context(), args[0], enabled)
	}}
	command.ValidArgsFunction = c.Complete(shared.CompletionMockScenarios)
	return command
}

func (c *Commands) importCommand() *cobra.Command {
	root := shared.CommandGroup("import", "Import routes into a disabled mock scenario")
	services := []string{}
	recording := &cobra.Command{Use: "recording <scenario> <recording>", Short: "Import retained HTTP traffic", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.importRecording(cmd.Context(), args[0], args[1], services)
	}}
	recording.Flags().StringSliceVar(&services, "service", nil, "limit imported traffic to a service (repeatable)")
	_ = recording.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	recording.ValidArgsFunction = completeScenarioThenRecording(c)
	root.AddCommand(recording)

	service := ""
	openAPI := &cobra.Command{Use: "openapi <scenario> <file>", Short: "Import routes for one service from OpenAPI", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.importOpenAPI(cmd.Context(), args[0], service, args[1])
	}}
	openAPI.Flags().StringVar(&service, "service", "", "service described by the document")
	_ = openAPI.MarkFlagRequired("service")
	_ = openAPI.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	openAPI.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.Complete(shared.CompletionMockScenarios)(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveDefault
	}
	root.AddCommand(openAPI)
	return root
}

func completeScenarioFirst(c *Commands) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.Complete(shared.CompletionMockScenarios)(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeScenarioThenRecording(c *Commands) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return c.Complete(shared.CompletionMockScenarios)(cmd, args, toComplete)
		case 1:
			return c.Complete(shared.CompletionRecordings)(cmd, args, toComplete)
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

// RootCommands returns the mock commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.mockCommand()}
}
