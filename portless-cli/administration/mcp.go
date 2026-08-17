package administration

import (
	"context"

	shared "github.com/portless-run/portless/portless-cli/command"
	apiclient "github.com/portless-run/portless/portless-daemon/api/client"
	portlessmcp "github.com/portless-run/portless/portless-mcp"
	"github.com/spf13/cobra"
)

type mcpOptions struct {
	allEnvironments       bool
	allowLifecycle        bool
	allowTrafficControl   bool
	allowSensitiveTraffic bool
}

type mcpConnector struct {
	daemon shared.DaemonController
}

// Connect adapts the CLI daemon lifecycle boundary to the MCP product facade.
func (c mcpConnector) Connect(ctx context.Context) (*apiclient.Client, portlessmcp.DaemonIdentity, error) {
	client, identity, err := c.daemon.Connect(ctx)
	if err != nil {
		return nil, portlessmcp.DaemonIdentity{}, err
	}
	return client, portlessmcp.DaemonIdentity{InstanceID: identity.InstanceID}, nil
}

func (c *Commands) mcpCommand() *cobra.Command {
	root := &cobra.Command{
		Use: "mcp", Short: "Expose the local Portless control plane to MCP clients",
		Args: shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	options := mcpOptions{}
	serve := &cobra.Command{
		Use: "serve", Short: "Serve scoped Portless tools over stdin and stdout",
		Args: shared.UsageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if c.JSONOutput {
				return shared.UsageError("--json cannot be used with mcp serve because stdout carries MCP JSON-RPC")
			}
			if options.allEnvironments && c.EnvironmentOverride != "" {
				return shared.UsageError("--all-environments cannot be combined with --env")
			}
			config := portlessmcp.Config{
				Environment: c.EnvironmentOverride, AllEnvironments: options.allEnvironments,
				AllowLifecycle: options.allowLifecycle, AllowTrafficControl: options.allowTrafficControl,
				AllowSensitiveTraffic: options.allowSensitiveTraffic, Version: cmd.Root().Version,
			}
			if config.Environment == "" && !config.AllEnvironments {
				root, err := c.CurrentSourceRoot(cmd.Context())
				if err != nil {
					return err
				}
				config.WorkspaceRoot = root
			}
			return portlessmcp.Serve(cmd.Context(), config, mcpConnector{daemon: c.Daemon}, portlessmcp.Streams{
				In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(),
			})
		},
	}
	serve.Flags().BoolVar(&options.allEnvironments, "all-environments", false, "permit inspection of every environment in this Portless installation")
	serve.Flags().BoolVar(&options.allowLifecycle, "allow-lifecycle", false, "enable environment and service lifecycle tools")
	serve.Flags().BoolVar(&options.allowTrafficControl, "allow-traffic-control", false, "enable bounded recording and fault-control tools")
	serve.Flags().BoolVar(&options.allowSensitiveTraffic, "allow-sensitive-traffic", false, "enable detailed traffic access that may contain sensitive application data")
	root.AddCommand(serve)
	return root
}
