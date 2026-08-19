package client

import (
	"context"
	"net/http"

	"github.com/portless-run/portless/portless-daemon/api/contract"
)

func mocksPath(project, environment string) string {
	return environmentPath(project, environment) + "/mocks"
}

// ListMocks returns every mock profile configured for an environment.
func (c *Client) ListMocks(ctx context.Context, project, environment string) (contract.MockProfileList, error) {
	var result contract.MockProfileList
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment), nil, &result)
	return result, err
}

// Mock returns one named environment mock profile.
func (c *Client) Mock(ctx context.Context, project, environment, name string) (contract.MockProfile, error) {
	var result contract.MockProfile
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment)+"/"+EscapePath(name), nil, &result)
	return result, err
}

// CreateMock creates an empty mock profile attached to one service.
func (c *Client) CreateMock(ctx context.Context, project, environment string, input contract.CreateMockRequest) (contract.MockMutation, error) {
	var result contract.MockMutation
	err := c.do(ctx, http.MethodPost, mocksPath(project, environment), input, &result)
	return result, err
}

// DeleteMock removes an unbound mock profile and all of its routes.
func (c *Client) DeleteMock(ctx context.Context, project, environment, name string) error {
	return c.do(ctx, http.MethodDelete, mocksPath(project, environment)+"/"+EscapePath(name), nil, nil)
}

// PutMockRoute creates or replaces one named route.
func (c *Client) PutMockRoute(ctx context.Context, project, environment, profile, route string, input contract.MockRoute) (contract.MockProfile, error) {
	var result contract.MockProfile
	err := c.do(ctx, http.MethodPut, mocksPath(project, environment)+"/"+EscapePath(profile)+"/routes/"+EscapePath(route), input, &result)
	return result, err
}

// DeleteMockRoute removes one named route from a profile.
func (c *Client) DeleteMockRoute(ctx context.Context, project, environment, profile, route string) (contract.MockProfile, error) {
	var result contract.MockProfile
	err := c.do(ctx, http.MethodDelete, mocksPath(project, environment)+"/"+EscapePath(profile)+"/routes/"+EscapePath(route), nil, &result)
	return result, err
}

// PreviewMock evaluates a request without generating a traffic exchange.
func (c *Client) PreviewMock(ctx context.Context, project, environment, profile string, input contract.MockRequest) (contract.MockPreview, error) {
	var result contract.MockPreview
	err := c.do(ctx, http.MethodPost, mocksPath(project, environment)+"/"+EscapePath(profile)+"/preview", input, &result)
	return result, err
}
