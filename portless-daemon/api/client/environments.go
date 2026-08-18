package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func environmentPath(project, environment string) string {
	return "/api/v1/environments/" + EscapePath(project, environment)
}

// ListEnvironments returns environments, optionally filtered by project.
func (c *Client) ListEnvironments(ctx context.Context, project string, limit int) (contract.EnvironmentList, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if project != "" {
		query.Set("project", project)
	}
	var result contract.EnvironmentList
	err := c.do(ctx, http.MethodGet, "/api/v1/environments?"+query.Encode(), nil, &result)
	return result, err
}

// CloneEnvironment creates an environment from an existing project environment.
func (c *Client) CloneEnvironment(ctx context.Context, input contract.CloneEnvironmentRequest) (contract.Environment, error) {
	var result contract.Environment
	err := c.do(ctx, http.MethodPost, "/api/v1/environments", input, &result)
	return result, err
}

// EnvironmentsForPath returns every environment associated with a source path.
func (c *Client) EnvironmentsForPath(ctx context.Context, path string) (contract.EnvironmentList, error) {
	var result contract.EnvironmentList
	err := c.do(ctx, http.MethodGet, "/api/v1/environments/resolve?path="+url.QueryEscape(path), nil, &result)
	return result, err
}

// EnvironmentContext resolves the effective environment selection for path.
func (c *Client) EnvironmentContext(ctx context.Context, path string) (contract.EnvironmentContext, error) {
	var result contract.EnvironmentContext
	err := c.do(ctx, http.MethodGet, "/api/v1/environments/context?path="+url.QueryEscape(path), nil, &result)
	return result, err
}

// SelectEnvironment persists an environment selection for a source path.
func (c *Client) SelectEnvironment(ctx context.Context, input contract.SelectEnvironmentRequest) error {
	return c.do(ctx, http.MethodPut, "/api/v1/environments/select", input, nil)
}

// ClearEnvironmentSelection removes the persisted environment selection for path.
func (c *Client) ClearEnvironmentSelection(ctx context.Context, path string) (contract.ClearEnvironmentSelectionResponse, error) {
	var result contract.ClearEnvironmentSelectionResponse
	err := c.do(ctx, http.MethodDelete, "/api/v1/environments/select?path="+url.QueryEscape(path), nil, &result)
	return result, err
}

// Environment returns one named project environment.
func (c *Client) Environment(ctx context.Context, project, environment string) (contract.Environment, error) {
	var result contract.Environment
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment), nil, &result)
	return result, err
}

// ForgetEnvironment removes a stopped environment from Portless state.
func (c *Client) ForgetEnvironment(ctx context.Context, project, environment string) error {
	return c.do(ctx, http.MethodDelete, environmentPath(project, environment), nil, nil)
}

// RescanEnvironment rediscovers an environment's bound project sources.
func (c *Client) RescanEnvironment(ctx context.Context, project, environment string) (contract.EnvironmentMutation, error) {
	var result contract.EnvironmentMutation
	err := c.do(ctx, http.MethodPost, environmentPath(project, environment)+"/rescan", nil, &result)
	return result, err
}

// UpEnvironment starts an environment and returns its asynchronous operation.
func (c *Client) UpEnvironment(ctx context.Context, project, environment string, input contract.UpRequest, idempotencyKey string) (contract.Operation, error) {
	var result contract.Operation
	err := c.doWithHeaders(ctx, http.MethodPost, environmentPath(project, environment)+"/up", input, &result, map[string]string{"Idempotency-Key": idempotencyKey})
	return result, err
}

// DownEnvironment stops an environment and optionally removes managed volumes.
func (c *Client) DownEnvironment(ctx context.Context, project, environment string, removeVolumes bool, idempotencyKey string) (contract.Operation, error) {
	var result contract.Operation
	err := c.doWithHeaders(ctx, http.MethodPost, environmentPath(project, environment)+"/down", contract.DownRequest{RemoveVolumes: removeVolumes}, &result, map[string]string{"Idempotency-Key": idempotencyKey})
	return result, err
}

// ChangeBinding starts a durable service-scoped provider transition.
func (c *Client) ChangeBinding(ctx context.Context, project, environment, service string, binding contract.ComponentBinding, idempotencyKey string) (contract.Operation, error) {
	var result contract.Operation
	err := c.doWithHeaders(ctx, http.MethodPut, environmentPath(project, environment)+"/bindings/"+EscapePath(service), binding, &result, map[string]string{"Idempotency-Key": idempotencyKey})
	return result, err
}

// SetSource changes one environment source path and rediscovers its services.
func (c *Client) SetSource(ctx context.Context, project, environment, source, path string) (contract.EnvironmentMutation, error) {
	var result contract.EnvironmentMutation
	err := c.do(ctx, http.MethodPut, environmentPath(project, environment)+"/sources/"+EscapePath(source), contract.SetSourceRequest{Path: path}, &result)
	return result, err
}

// Operation returns one environment operation by sequence number.
func (c *Client) Operation(ctx context.Context, project, environment string, number int64) (contract.Operation, error) {
	var result contract.Operation
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/operations/"+strconv.FormatInt(number, 10), nil, &result)
	return result, err
}

// ListOperations returns recent durable operations for one environment.
func (c *Client) ListOperations(ctx context.Context, project, environment string, limit int) (contract.OperationList, error) {
	var result contract.OperationList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/operations?limit="+strconv.Itoa(limit), nil, &result)
	return result, err
}

// Logs returns recent environment logs, optionally filtered by service and time.
func (c *Client) Logs(ctx context.Context, project, environment, service string, limit int, since string) (contract.LogList, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if service != "" {
		query.Set("service", service)
	}
	if since != "" {
		query.Set("since", since)
	}
	var result contract.LogList
	err := c.do(ctx, http.MethodGet, environmentPath(project, environment)+"/logs?"+query.Encode(), nil, &result)
	return result, err
}

// WaitOperation polls until operation reaches a terminal state and invokes
// observe once for each newly received operation event.
func (c *Client) WaitOperation(ctx context.Context, operation contract.Operation, interval time.Duration, observe func(contract.Operation)) (contract.Operation, error) {
	seen := 0
	for {
		current, err := c.Operation(ctx, operation.Project, operation.Environment, operation.Number)
		if err != nil {
			return contract.Operation{}, err
		}
		if observe != nil {
			for _, event := range current.Events[seen:] {
				copy := current
				copy.Events = []contract.OperationEvent{event}
				observe(copy)
			}
		}
		seen = len(current.Events)
		if current.State != "running" {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return contract.Operation{}, ctx.Err()
		case <-time.After(interval):
		}
	}
}
