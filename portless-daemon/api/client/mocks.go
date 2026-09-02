package client

import (
	"context"
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func mocksPath(project, environment string) string {
	return environmentPath(project, environment) + "/mocks"
}

// ListMockScenarios returns every mock scenario configured for an environment.
func (c *Client) ListMockScenarios(ctx context.Context, project, environment string) (contract.MockScenarioList, error) {
	var result contract.MockScenarioList
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment), nil, &result)
	return result, err
}

// MockScenario returns one named environment mock scenario.
func (c *Client) MockScenario(ctx context.Context, project, environment, name string) (contract.MockScenario, error) {
	var result contract.MockScenario
	err := c.do(ctx, http.MethodGet, mocksPath(project, environment)+"/"+EscapePath(name), nil, &result)
	return result, err
}

// CreateMockScenario creates an empty mock scenario.
func (c *Client) CreateMockScenario(ctx context.Context, project, environment string, input contract.CreateMockRequest) (contract.MockScenario, error) {
	var result contract.MockScenario
	err := c.do(ctx, http.MethodPost, mocksPath(project, environment), input, &result)
	return result, err
}

// DeleteMockScenario removes a disabled mock scenario and all of its routes.
func (c *Client) DeleteMockScenario(ctx context.Context, project, environment, name string) error {
	return c.do(ctx, http.MethodDelete, mocksPath(project, environment)+"/"+EscapePath(name), nil, nil)
}

// PutMockRoute creates or replaces one named scenario route.
func (c *Client) PutMockRoute(ctx context.Context, project, environment, scenario, route string, input contract.MockRoute) (contract.MockScenario, error) {
	var result contract.MockScenario
	err := c.do(ctx, http.MethodPut, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/routes/"+EscapePath(route), input, &result)
	return result, err
}

// DeleteMockRoute removes one named route from a scenario.
func (c *Client) DeleteMockRoute(ctx context.Context, project, environment, scenario, route string) (contract.MockScenario, error) {
	var result contract.MockScenario
	err := c.do(ctx, http.MethodDelete, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/routes/"+EscapePath(route), nil, &result)
	return result, err
}

// PreviewMock evaluates one scenario request without generating traffic.
func (c *Client) PreviewMock(ctx context.Context, project, environment, scenario string, input contract.MockRequest) (contract.MockPreview, error) {
	var result contract.MockPreview
	err := c.do(ctx, http.MethodPost, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/preview", input, &result)
	return result, err
}

// SetMockScenarioEnabled enables or disables one complete scenario.
func (c *Client) SetMockScenarioEnabled(ctx context.Context, project, environment, scenario string, input contract.SetMockScenarioActivationRequest, idempotencyKey string) (contract.Operation, error) {
	var result contract.Operation
	err := c.doWithHeaders(ctx, http.MethodPut, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/activation", input, &result, map[string]string{"Idempotency-Key": idempotencyKey})
	return result, err
}

// ImportMockRecording imports retained traffic into a scenario.
func (c *Client) ImportMockRecording(ctx context.Context, project, environment, scenario string, input contract.ImportMockRecordingRequest) (contract.MockScenarioMutation, error) {
	var result contract.MockScenarioMutation
	err := c.do(ctx, http.MethodPost, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/imports/recording", input, &result)
	return result, err
}

// ImportMockOpenAPI imports one local OpenAPI document for a scenario service.
func (c *Client) ImportMockOpenAPI(ctx context.Context, project, environment, scenario string, input contract.ImportMockOpenAPIRequest) (contract.MockScenarioMutation, error) {
	var result contract.MockScenarioMutation
	err := c.do(ctx, http.MethodPost, mocksPath(project, environment)+"/"+EscapePath(scenario)+"/imports/openapi", input, &result)
	return result, err
}
