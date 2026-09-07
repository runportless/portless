package portlessmcp

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"time"
)

type mockListInput struct {
	Environment string `json:"environment"`
	Offset      int    `json:"offset,omitempty"`
	Limit       int    `json:"limit,omitempty" jsonschema:"default 100, maximum 500"`
}

type mockInput struct {
	Environment string `json:"environment"`
	Scenario    string `json:"scenario"`
}

type mockScenarioInput struct {
	mockInput
	Offset             int       `json:"offset,omitempty"`
	Limit              int       `json:"limit,omitempty" jsonschema:"default 100, maximum 500"`
	ExpectedModifiedAt time.Time `json:"expectedModifiedAt,omitempty"`
}

type mockRouteInput struct {
	mockInput
	Route              string    `json:"route"`
	IncludePayloads    bool      `json:"includePayloads,omitempty"`
	Offset             int       `json:"offset,omitempty" jsonschema:"UTF-8 byte offset in saved payload JSON; reassemble all chunks before editing"`
	Limit              int       `json:"limit,omitempty" jsonschema:"payload bytes, default 32768, maximum 32768"`
	ExpectedModifiedAt time.Time `json:"expectedModifiedAt,omitempty"`
}

type previewMockInput struct {
	mockInput
	Request         contract.MockRequest `json:"request"`
	Draft           *mockRouteDraft      `json:"draft,omitempty"`
	OriginalRoute   string               `json:"originalRoute,omitempty"`
	IncludePayloads bool                 `json:"includePayloads,omitempty"`
}

type mockPreviewView struct {
	Preview          contract.MockPreview `json:"preview"`
	PayloadsOmitted  bool                 `json:"payloadsOmitted"`
	DisplayTruncated bool                 `json:"displayTruncated"`
	Continuation     string               `json:"continuation,omitempty"`
}

func (r *runtime) registerMockInspectionTools(server *mcp.Server) {
	registerTool(r, server, readTool("portless_list_mock_scenarios", "List paged mock metadata, route counts, and provider activation without loading saved response bodies."), r.listMockScenarios)
	registerTool(r, server, readTool("portless_get_mock_scenario", "Inspect scenario identity and a revision-bound page of route metadata. Saved query values, headers, and bodies are excluded."), r.getMockScenario)
	registerTool(r, server, readTool("portless_get_mock_route", "Read named-route metadata and optional saved payload JSON chunks. includePayloads requires sensitive-traffic permission; use returned modification identity for continuation."), r.getMockRoute)
	registerTool(r, server, readTool("portless_preview_mock", "Evaluate saved routes and an optional draft without persisting changes or sending traffic. Response payloads require includePayloads and sensitive-traffic permission."), r.previewMock)
}

func mockPage(offset, limit int, modified time.Time, continuation bool) (int, error) {
	if offset < 0 {
		return 0, codedError{code: "INVALID_ARGUMENT", message: "offset must be non-negative"}
	}
	if continuation && offset > 0 && modified.IsZero() {
		return 0, codedError{code: "INVALID_ARGUMENT", message: "expectedModifiedAt is required for continuation"}
	}
	return bounded(limit, 100, 500, "limit")
}

func (r *runtime) listMockScenarios(ctx context.Context, _ *mcp.CallToolRequest, input mockListInput) (*mcp.CallToolResult, scopedResult[contract.MockScenarioMetadataList], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (contract.MockScenarioMetadataList, error) {
		var result contract.MockScenarioMetadataList
		limit, err := mockPage(input.Offset, input.Limit, time.Time{}, false)
		if err != nil {
			return result, err
		}
		result, err = selected.client.ListMockScenarioMetadata(ctx, selected.project, selected.environment, contract.MockMetadataQuery{Offset: input.Offset, Limit: limit})
		for len(result.Scenarios) > 1 && r.checkOutput(scopedResult[contract.MockScenarioMetadataList]{Result: result}) != nil {
			result.Scenarios = result.Scenarios[:len(result.Scenarios)/2]
			result.NextOffset = input.Offset + len(result.Scenarios)
		}
		return result, err
	})
}

func (r *runtime) getMockScenario(ctx context.Context, _ *mcp.CallToolRequest, input mockScenarioInput) (*mcp.CallToolResult, scopedResult[contract.MockScenarioMetadataPage], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (contract.MockScenarioMetadataPage, error) {
		var result contract.MockScenarioMetadataPage
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return result, err
		}
		limit, err := mockPage(input.Offset, input.Limit, input.ExpectedModifiedAt, true)
		if err != nil {
			return result, err
		}
		result, err = selected.client.MockScenarioMetadata(ctx, selected.project, selected.environment, input.Scenario, contract.MockMetadataQuery{Offset: input.Offset, Limit: limit, ExpectedModifiedAt: input.ExpectedModifiedAt})
		for len(result.Routes) > 1 && r.checkOutput(scopedResult[contract.MockScenarioMetadataPage]{Result: result}) != nil {
			result.Routes = result.Routes[:len(result.Routes)/2]
			result.NextOffset = input.Offset + len(result.Routes)
		}
		return result, err
	})
}

func (r *runtime) getMockRoute(ctx context.Context, _ *mcp.CallToolRequest, input mockRouteInput) (*mcp.CallToolResult, scopedResult[contract.MockRouteDetail], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (contract.MockRouteDetail, error) {
		var result contract.MockRouteDetail
		if err := requireSensitive(r, input.IncludePayloads); err != nil {
			return result, err
		}
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return result, err
		}
		if err := validateArtifactName(input.Route, "route"); err != nil {
			return result, err
		}
		if _, err := mockPage(input.Offset, 1, input.ExpectedModifiedAt, true); err != nil {
			return result, err
		}
		limit, err := bounded(input.Limit, 32<<10, 32<<10, "limit")
		if err != nil {
			return result, err
		}
		return selected.client.MockRoute(ctx, selected.project, selected.environment, input.Scenario, input.Route, contract.MockRouteQuery{IncludePayloads: input.IncludePayloads, Offset: input.Offset, Limit: limit, ExpectedModifiedAt: input.ExpectedModifiedAt})
	})
}

func (r *runtime) previewMock(ctx context.Context, _ *mcp.CallToolRequest, input previewMockInput) (*mcp.CallToolResult, scopedResult[mockPreviewView], error) {
	return environmentCall(ctx, r, input.Environment, false, func(ctx context.Context, selected selectedEnvironment) (mockPreviewView, error) {
		result := mockPreviewView{PayloadsOmitted: !input.IncludePayloads}
		if err := requireSensitive(r, input.IncludePayloads); err != nil {
			return result, err
		}
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return result, err
		}
		api := selected.client
		if !input.IncludePayloads {
			api = api.WithMockMetadata()
		}
		preview, err := api.PreviewMock(ctx, selected.project, selected.environment, input.Scenario, contract.PreviewMockRequest{Request: input.Request, Draft: input.Draft.contract(), OriginalRoute: input.OriginalRoute})
		if err != nil {
			return result, err
		}
		if !input.IncludePayloads {
			preview.Headers = nil
			preview.Body = ""
		} else {
			preview.Body, result.DisplayTruncated = truncateUTF8(preview.Body, 16<<10)
			headers := map[string][]string{}
			for key, value := range preview.Headers {
				headers[key] = []string{value}
			}
			bounded, cut := capHeaders(headers, 4<<10)
			preview.Headers = map[string]string{}
			for key, values := range bounded {
				if len(values) > 0 {
					preview.Headers[key] = values[0]
				}
			}
			result.DisplayTruncated = result.DisplayTruncated || cut
			if result.DisplayTruncated {
				result.Continuation = "Use portless_get_mock_route for complete saved payload JSON; a supplied draft remains available in your input."
			}
		}
		result.Preview = preview
		return result, nil
	})
}
