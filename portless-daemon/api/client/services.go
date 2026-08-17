package client

import (
	"context"
	"net/http"
	"strconv"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

// ListServices returns services in one environment.
func (c *Client) ListServices(ctx context.Context, project, environment string, limit int) (contract.ServiceList, error) {
	var result contract.ServiceList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/services?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

// Service returns one named environment service.
func (c *Client) Service(ctx context.Context, project, environment, name string) (contract.Service, error) {
	var result contract.Service
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/services/"+EscapePath(name), nil, &result)
	return result, err
}

// ServiceConfiguration returns the effective runtime configuration for a service.
func (c *Client) ServiceConfiguration(ctx context.Context, project, environment, name string) (contract.ServiceConfiguration, error) {
	var result contract.ServiceConfiguration
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/services/"+EscapePath(name)+"/configuration", nil, &result)
	return result, err
}

// ServiceAction starts an asynchronous lifecycle action for one service.
func (c *Client) ServiceAction(ctx context.Context, project, environment, name, action, idempotencyKey string) (contract.Operation, error) {
	var result contract.Operation
	err := c.doWithHeaders(ctx, http.MethodPost, environmentPath(project, environment)+"/services/"+EscapePath(name)+"/"+EscapePath(action), nil, &result, map[string]string{"Idempotency-Key": idempotencyKey})
	return result, err
}

// ListConnections returns effective service connections in one environment.
func (c *Client) ListConnections(ctx context.Context, project, environment string, limit int) (contract.ConnectionList, error) {
	var result contract.ConnectionList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/connections?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

// Connection returns one effective source-to-target service connection.
func (c *Client) Connection(ctx context.Context, project, environment, source, target string) (contract.EffectiveConnection, error) {
	var result contract.EffectiveConnection
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/connections/"+EscapePath(source, target), nil, &result)
	return result, err
}

// Timeline returns recent durable events for an environment.
func (c *Client) Timeline(ctx context.Context, project, environment string, limit int) (contract.TimelineList, error) {
	var result contract.TimelineList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/timeline?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}
