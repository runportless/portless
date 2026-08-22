package mocks

import (
	shared "github.com/runportless/portless/portless-cli/command"
	"github.com/spf13/cobra"
)

func (c *Commands) mockCommand() *cobra.Command {
	root := shared.CommandGroup("mock", "Serve deterministic HTTP responses for a service")
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List mock profiles", Args: shared.UsageArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, _ []string) error {
		return c.list(cmd.Context())
	}}
	root.AddCommand(list)

	show := &cobra.Command{Use: "show <profile>", Short: "Show a mock profile and its routes", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.show(cmd.Context(), args[0])
	}}
	show.ValidArgsFunction = c.Complete(shared.CompletionMocks)
	root.AddCommand(show)

	service := ""
	description := ""
	fromRecording := ""
	fromOpenAPI := ""
	create := &cobra.Command{Use: "create <profile>", Short: "Create or import a mock profile for a service", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.create(cmd.Context(), args[0], service, description, fromRecording, fromOpenAPI)
	}}
	create.Flags().StringVar(&service, "service", "", "service this profile can replace")
	create.Flags().StringVar(&description, "description", "", "optional profile description")
	create.Flags().StringVar(&fromRecording, "from-recording", "", "derive exact routes from a retained recording")
	create.Flags().StringVar(&fromOpenAPI, "from-openapi", "", "derive routes from a local OpenAPI 3.0 or 3.1 file")
	create.MarkFlagsMutuallyExclusive("from-recording", "from-openapi")
	_ = create.MarkFlagRequired("service")
	_ = create.RegisterFlagCompletionFunc("service", c.Complete(shared.CompletionServices))
	root.AddCommand(create)

	deleteYes := false
	deleteCommand := &cobra.Command{Use: "delete <profile>", Short: "Delete an unbound mock profile", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if !deleteYes {
			return shared.UsageError("mock profile deletion is permanent; repeat with --yes")
		}
		return c.delete(cmd.Context(), args[0])
	}}
	deleteCommand.Flags().BoolVar(&deleteYes, "yes", false, "confirm mock profile deletion")
	deleteCommand.ValidArgsFunction = c.Complete(shared.CompletionMocks)
	root.AddCommand(deleteCommand)

	route := shared.CommandGroup("route", "Manage deterministic routes in a mock profile")
	options := routeOptions{method: "GET", path: "/", status: 200}
	set := &cobra.Command{Use: "set <profile> <route>", Short: "Create or replace a mock route", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.setRoute(cmd.Context(), args[0], args[1], options)
	}}
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
	set.ValidArgsFunction = completeProfileFirst(c)
	route.AddCommand(set)

	routeDeleteYes := false
	routeDelete := &cobra.Command{Use: "delete <profile> <route>", Short: "Delete a route from a mock profile", Args: shared.UsageArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if !routeDeleteYes {
			return shared.UsageError("mock route deletion is permanent; repeat with --yes")
		}
		return c.deleteRoute(cmd.Context(), args[0], args[1])
	}}
	routeDelete.Flags().BoolVar(&routeDeleteYes, "yes", false, "confirm mock route deletion")
	routeDelete.ValidArgsFunction = completeProfileFirst(c)
	route.AddCommand(routeDelete)
	root.AddCommand(route)

	preview := previewOptions{method: "GET", path: "/"}
	previewCommand := &cobra.Command{Use: "preview <profile>", Short: "Show which route would match a request", Args: shared.UsageArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		return c.preview(cmd.Context(), args[0], preview)
	}}
	previewCommand.Flags().StringVar(&preview.method, "method", preview.method, "HTTP method")
	previewCommand.Flags().StringVar(&preview.path, "path", preview.path, "request path")
	previewCommand.Flags().StringArrayVar(&preview.query, "query", nil, "query value as name=value (repeatable)")
	previewCommand.Flags().StringArrayVar(&preview.header, "header", nil, "request header as name=value (repeatable)")
	previewCommand.Flags().StringVar(&preview.body, "body", "", "request body")
	previewCommand.Flags().StringVar(&preview.bodyFile, "body-file", "", "read the request body from a file")
	previewCommand.MarkFlagsMutuallyExclusive("body", "body-file")
	previewCommand.ValidArgsFunction = c.Complete(shared.CompletionMocks)
	root.AddCommand(previewCommand)
	return root
}

func completeProfileFirst(c *Commands) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.Complete(shared.CompletionMocks)(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

// RootCommands returns the mock commands mounted directly under portless.
func (c *Commands) RootCommands() []*cobra.Command {
	return []*cobra.Command{c.mockCommand()}
}
