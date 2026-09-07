package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type cleanupInput struct {
	Mode     string                    `json:"mode,omitempty" jsonschema:"preview (default) or apply"`
	Confirm  bool                      `json:"confirm,omitempty" jsonschema:"apply requires explicit confirmation of the previewed action"`
	Expected *contract.ResourceVersion `json:"expected,omitempty" jsonschema:"complete resource identity returned by preview"`
}

func (input cleanupInput) apply() (bool, error) {
	switch input.Mode {
	case "", "preview":
		return false, nil
	case "apply":
		if !input.Confirm || input.Expected == nil || input.Expected.CreatedAt.IsZero() {
			return false, codedError{code: "PREVIEW_REQUIRED", message: "apply requires confirm:true and the complete expected identity from a fresh preview"}
		}
		return true, nil
	default:
		return false, codedError{code: "INVALID_ARGUMENT", message: "mode must be preview or apply"}
	}
}

type deleteMockScenarioInput struct {
	mockInput
	cleanupInput
}
type deleteMockRouteInput struct {
	mockInput
	cleanupInput
	Route string `json:"route"`
}
type mockDeletionView struct {
	Preview  *contract.MockDeletionPreview `json:"preview,omitempty"`
	Applied  bool                          `json:"applied"`
	Scenario string                        `json:"scenario"`
	Route    string                        `json:"route,omitempty"`
}

func (r *runtime) registerMockDeletionTools(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_delete_mock_scenario", "Preview deletion of a disabled mock scenario and its routes. Apply requires confirm:true and the unchanged identity returned by preview; services are never stopped implicitly.", true), r.deleteMockScenario)
	registerTool(r, server, mutationTool("portless_delete_mock_route", "Preview removal of one saved mock route. Apply requires confirm:true and unchanged scenario and environment identities. Deleting final service coverage requires disabling the scenario first.", true), r.deleteMockRoute)
}

func (r *runtime) deleteMock(ctx context.Context, input mockInput, route string, cleanup cleanupInput) (*mcp.CallToolResult, scopedResult[mockDeletionView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (mockDeletionView, error) {
		result := mockDeletionView{Scenario: input.Scenario, Route: route}
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return result, err
		}
		if route != "" {
			if err := validateArtifactName(route, "route"); err != nil {
				return result, err
			}
		}
		apply, err := cleanup.apply()
		if err != nil {
			return result, err
		}
		if !apply {
			preview, err := selected.client.PreviewMockDeletion(ctx, selected.project, selected.environment, input.Scenario, route)
			if err != nil {
				return result, err
			}
			result.Preview = &preview
			return result, nil
		}
		api := selected.client.WithResourceVersion(*cleanup.Expected).WithMockMetadata()
		if route == "" {
			err = api.DeleteMockScenario(ctx, selected.project, selected.environment, input.Scenario)
		} else {
			_, err = api.DeleteMockRoute(ctx, selected.project, selected.environment, input.Scenario, route)
		}
		result.Applied = err == nil
		return result, err
	})
}

func (r *runtime) deleteMockScenario(ctx context.Context, _ *mcp.CallToolRequest, input deleteMockScenarioInput) (*mcp.CallToolResult, scopedResult[mockDeletionView], error) {
	return r.deleteMock(ctx, input.mockInput, "", input.cleanupInput)
}
func (r *runtime) deleteMockRoute(ctx context.Context, _ *mcp.CallToolRequest, input deleteMockRouteInput) (*mcp.CallToolResult, scopedResult[mockDeletionView], error) {
	if err := validateArtifactName(input.Route, "route"); err != nil {
		return nil, scopedResult[mockDeletionView]{}, r.toolError(err)
	}
	return r.deleteMock(ctx, input.mockInput, input.Route, input.cleanupInput)
}
