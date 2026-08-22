// Package portlessmcp exposes Portless control-plane capabilities over the
// Model Context Protocol without widening the daemon's HTTP trust boundary.
package portlessmcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
)

// Config fixes the MCP server's environment scope and optional capabilities
// for the lifetime of one stdio session.
type Config struct {
	WorkspaceRoot         string
	Environment           string
	AllEnvironments       bool
	AllowLifecycle        bool
	AllowTrafficControl   bool
	AllowSensitiveTraffic bool
	Version               string
}

// DaemonIdentity contains the minimum daemon identity needed to detect a
// control-plane handoff without exposing its private discovery record.
type DaemonIdentity struct {
	InstanceID string
}

// Connector supplies an authenticated typed daemon client on demand.
type Connector interface {
	// Connect starts or reconnects to a compatible daemon and returns its
	// current stable instance identity.
	Connect(context.Context) (*apiclient.Client, DaemonIdentity, error)
}

// Streams contains the MCP transport streams. Protocol traffic uses In and
// Out exclusively; operational diagnostics use Err.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Serve runs one local MCP stdio session until client EOF or cancellation.
func Serve(ctx context.Context, config Config, connector Connector, streams Streams) error {
	if connector == nil {
		return errors.New("MCP daemon connector is required")
	}
	if streams.In == nil || streams.Out == nil || streams.Err == nil {
		return errors.New("MCP input, output, and error streams are required")
	}
	if config.Environment != "" && config.AllEnvironments {
		return errors.New("an MCP server cannot combine a pinned environment with all-environment scope")
	}
	if config.Environment != "" {
		if _, _, err := parseEnvironmentSelector(config.Environment); err != nil {
			return fmt.Errorf("invalid pinned MCP environment: %w", err)
		}
	}
	if config.Environment == "" && !config.AllEnvironments && config.WorkspaceRoot == "" {
		return errors.New("workspace-scoped MCP access requires a source root")
	}
	if config.Version == "" {
		config.Version = "dev"
	}

	logger := slog.New(slog.NewTextHandler(streams.Err, &slog.HandlerOptions{Level: slog.LevelError}))
	runtime := newRuntime(config, connector, logger)
	server := runtime.server()
	transport := &mcp.IOTransport{
		Reader: io.NopCloser(streams.In),
		Writer: nopWriteCloser{Writer: streams.Out},
	}
	if err := server.Run(ctx, transport); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("serve MCP stdio session: %w", err)
	}
	return nil
}

type nopWriteCloser struct {
	io.Writer
}

// Close leaves the CLI-owned output stream open after the MCP session ends.
func (nopWriteCloser) Close() error { return nil }
