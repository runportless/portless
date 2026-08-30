package client

import (
	"context"
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

// DaemonStatus returns the daemon's shallow authenticated process status.
func (c *Client) DaemonStatus(ctx context.Context) (contract.DaemonStatus, error) {
	var result contract.DaemonStatus
	err := c.do(ctx, http.MethodGet, "/api/v1/daemon", nil, &result)
	return result, err
}

// DaemonDiagnostics returns bounded operational metadata and optionally
// includes the more expensive storage inspection.
func (c *Client) DaemonDiagnostics(ctx context.Context, includeStorage bool) (contract.DaemonDiagnostics, error) {
	path := "/api/v1/daemon/diagnostics"
	if includeStorage {
		path += "?include=storage"
	}
	var result contract.DaemonDiagnostics
	err := c.do(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

// DaemonLogs returns one bounded, safely redacted tail of the daemon log.
func (c *Client) DaemonLogs(ctx context.Context) (contract.DaemonLogSnapshot, error) {
	var result contract.DaemonLogSnapshot
	err := c.do(ctx, http.MethodGet, "/api/v1/daemon/logs", nil, &result)
	return result, err
}

// DaemonHandoffStatus performs and returns a fresh runtime-adoption audit.
func (c *Client) DaemonHandoffStatus(ctx context.Context) (contract.DaemonHandoffStatus, error) {
	var result contract.DaemonHandoffStatus
	err := c.do(ctx, http.MethodGet, "/api/v1/daemon/handoff", nil, &result)
	return result, err
}

// RestartDaemon requests guarded replacement of one daemon instance. Browser
// force recovery is intentionally outside the CLI-authenticated client.
func (c *Client) RestartDaemon(ctx context.Context, instanceID string) (contract.DaemonRestart, error) {
	var result contract.DaemonRestart
	err := c.do(ctx, http.MethodPost, "/api/v1/daemon/restart", contract.DaemonRestartRequest{InstanceID: instanceID}, &result)
	return result, err
}
