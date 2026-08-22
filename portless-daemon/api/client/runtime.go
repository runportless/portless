package client

import (
	"context"
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

// RuntimeStatus returns container-runtime preference and probe results.
func (c *Client) RuntimeStatus(ctx context.Context) (contract.RuntimeStatus, error) {
	var result contract.RuntimeStatus
	err := c.do(ctx, http.MethodGet, "/api/v1/runtime", nil, &result)
	return result, err
}

// StartRuntime starts the selected container host when supported.
func (c *Client) StartRuntime(ctx context.Context) (contract.RuntimeStatus, error) {
	var result contract.RuntimeStatus
	err := c.do(ctx, http.MethodPost, "/api/v1/runtime/start", nil, &result)
	return result, err
}

// UseRuntime persists a container-runtime preference.
func (c *Client) UseRuntime(ctx context.Context, preference string) (contract.RuntimeStatus, error) {
	var result contract.RuntimeStatus
	err := c.do(ctx, http.MethodPut, "/api/v1/runtime", contract.UseRuntimeRequest{Preference: preference}, &result)
	return result, err
}

// ResetPlan previews the application and runtime state affected by a reset.
func (c *Client) ResetPlan(ctx context.Context) (contract.ResetPlan, error) {
	var result contract.ResetPlan
	err := c.do(ctx, http.MethodGet, "/api/v1/runtime/reset", nil, &result)
	return result, err
}

// PrepareReset stops owned runtime resources before persistent state is removed.
func (c *Client) PrepareReset(ctx context.Context, force bool) (contract.PrepareResetResponse, error) {
	var result contract.PrepareResetResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/runtime/reset", contract.PrepareResetRequest{Force: force}, &result)
	return result, err
}

// CancelReset returns a daemon from reset-draining mode to normal operation.
func (c *Client) CancelReset(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/v1/runtime/reset/cancel", nil, nil)
}
