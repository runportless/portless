package client

import (
	"context"
	"net/http"

	"github.com/portless-run/portless/internal/api/contract"
)

func (c *Client) RuntimeStatus(ctx context.Context) (contract.RuntimeStatus, error) {
	var result contract.RuntimeStatus
	err := c.do(ctx, http.MethodGet, "/api/v1/runtime", nil, &result)
	return result, err
}

func (c *Client) StartRuntime(ctx context.Context) (contract.RuntimeStatus, error) {
	var result contract.RuntimeStatus
	err := c.do(ctx, http.MethodPost, "/api/v1/runtime/start", nil, &result)
	return result, err
}

func (c *Client) UseRuntime(ctx context.Context, preference string) (contract.RuntimeStatus, error) {
	var result contract.RuntimeStatus
	err := c.do(ctx, http.MethodPut, "/api/v1/runtime", contract.UseRuntimeRequest{Preference: preference}, &result)
	return result, err
}

func (c *Client) ResetPlan(ctx context.Context) (contract.ResetPlan, error) {
	var result contract.ResetPlan
	err := c.do(ctx, http.MethodGet, "/api/v1/runtime/reset", nil, &result)
	return result, err
}

func (c *Client) PrepareReset(ctx context.Context, force bool) (contract.PrepareResetResponse, error) {
	var result contract.PrepareResetResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/runtime/reset", contract.PrepareResetRequest{Force: force}, &result)
	return result, err
}

func (c *Client) CancelReset(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/v1/runtime/reset/cancel", nil, nil)
}
