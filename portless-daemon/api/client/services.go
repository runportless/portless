package client

import (
	"context"
	"net/http"
	"strconv"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func (c *Client) ListServices(ctx context.Context, project, environment string, limit int) (contract.ServiceList, error) {
	var result contract.ServiceList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/services?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

func (c *Client) Service(ctx context.Context, project, environment, name string) (contract.Service, error) {
	var result contract.Service
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/services/"+EscapePath(name), nil, &result)
	return result, err
}

func (c *Client) ServiceConfiguration(ctx context.Context, project, environment, name string) (contract.ServiceConfiguration, error) {
	var result contract.ServiceConfiguration
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/services/"+EscapePath(name)+"/configuration", nil, &result)
	return result, err
}

func (c *Client) ServiceAction(ctx context.Context, project, environment, name, action string) (contract.Operation, error) {
	var result contract.Operation
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/services/"+EscapePath(name)+"/"+EscapePath(action), nil, &result)
	return result, err
}

func (c *Client) ListConnections(ctx context.Context, project, environment string, limit int) (contract.ConnectionList, error) {
	var result contract.ConnectionList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/connections?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

func (c *Client) Connection(ctx context.Context, project, environment, source, target string) (contract.EffectiveConnection, error) {
	var result contract.EffectiveConnection
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/connections/"+EscapePath(source, target), nil, &result)
	return result, err
}

func (c *Client) Timeline(ctx context.Context, project, environment string, limit int) (contract.TimelineList, error) {
	var result contract.TimelineList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/timeline?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}
