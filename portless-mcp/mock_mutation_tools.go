package portlessmcp

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"strings"
	"time"
)

type createMockInput struct {
	mockInput
	Description string `json:"description,omitempty"`
}
type putMockRouteInput struct {
	mockInput
	Route string         `json:"route"`
	Draft mockRouteDraft `json:"draft"`
}
type importMockOpenAPIInput struct {
	mockInput
	Service  string `json:"service"`
	Document string `json:"document" jsonschema:"supplied OpenAPI document, maximum 1 MiB; no file paths or fetched URLs"`
}
type importMockRecordingInput struct {
	mockInput
	Recording string   `json:"recording"`
	Services  []string `json:"services,omitempty"`
}
type mockMutationView struct {
	Scenario          contract.MockScenarioMetadata `json:"scenario"`
	Warnings          []string                      `json:"warnings"`
	WarningsTruncated bool                          `json:"warningsTruncated"`
}
type mockActivationInput struct {
	mockInput
	Enabled        bool   `json:"enabled"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	WaitSeconds    *int   `json:"waitSeconds,omitempty"`
}
type disableAllMocksInput struct {
	Environment    string `json:"environment"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	WaitSeconds    *int   `json:"waitSeconds,omitempty"`
}
type mockActivationView struct {
	Scenario         string                         `json:"scenario"`
	Operation        contract.Operation             `json:"operation"`
	IdempotencyKey   string                         `json:"idempotencyKey"`
	TimedOutWaiting  bool                           `json:"timedOutWaiting"`
	Activation       *contract.MockScenarioMetadata `json:"activation,omitempty"`
	AdmissionUnknown bool                           `json:"admissionUnknown"`
	Warning          string                         `json:"warning,omitempty"`
}
type disableAllMocksView struct {
	IdempotencyKey string               `json:"idempotencyKey"`
	Operations     []mockActivationView `json:"operations"`
	Completed      []string             `json:"completed"`
	Pending        []string             `json:"pending"`
	Failed         []string             `json:"failed"`
	PartialFailure bool                 `json:"partialFailure"`
	Warning        string               `json:"warning,omitempty"`
}

func (r *runtime) registerMockMutationTools(server *mcp.Server) {
	registerTool(r, server, mutationTool("portless_create_mock_scenario", "Create an empty disabled scenario. This does not activate providers. After an uncertain response, inspect the named scenario before retrying.", false), r.createMockScenario)
	registerTool(r, server, mutationTool("portless_put_mock_route", "Save a complete route draft, or rename the addressed route by changing draft.name. Live routes can affect application traffic; daemon service-coverage rules still apply. Returns metadata only.", true), r.putMockRoute)
	registerTool(r, server, mutationTool("portless_import_mock_openapi", "Append routes from supplied OpenAPI content to a disabled scenario. This import is not safely retryable; inspect the scenario after an uncertain response. No URLs are fetched.", false), r.importMockOpenAPI)
	if r.config.AllowSensitiveTraffic {
		registerTool(r, server, mutationTool("portless_import_mock_recording", "Append routes from a stopped recording in this same environment to a disabled scenario. This import is not safely retryable. Returns metadata and bounded import warnings.", false), r.importMockRecording)
	}
	if r.config.AllowLifecycle {
		registerTool(r, server, mutationTool("portless_set_mock_scenario_enabled", "Enable or disable a scenario through a durable provider operation. Enabling may stop services; disabling restores saved providers, including configured remote services. Reuse the returned idempotency key for retries.", true), r.setMockScenarioEnabled)
		registerTool(r, server, mutationTool("portless_disable_all_mock_scenarios", "Snapshot active and degraded scenarios and restore providers sequentially. Returns admitted operation receipts and completed, pending, or failed names; zero wait admits at most one pending operation.", true), r.disableAllMockScenarios)
	}
}

func mockMetadata(value contract.MockScenario) contract.MockScenarioMetadata {
	count := len(value.Routes)
	if value.PayloadsOmitted {
		count = value.RouteCount
	}
	return contract.MockScenarioMetadata{Project: value.Project, Environment: value.Environment, Name: value.Name, Description: value.Description, CreatedAt: value.CreatedAt, ModifiedAt: value.ModifiedAt, Activation: value.Activation, RouteCount: count}
}

func mockMutationResult(value contract.MockScenario, warnings []string) mockMutationView {
	result := mockMutationView{Scenario: mockMetadata(value), Warnings: []string{}}
	remaining := 16 << 10
	for _, warning := range warnings {
		if remaining == 0 || len(result.Warnings) >= 100 {
			result.WarningsTruncated = true
			break
		}
		bounded, cut := truncateUTF8(warning, min(remaining, 2048))
		remaining -= len(bounded)
		result.Warnings = append(result.Warnings, bounded)
		result.WarningsTruncated = result.WarningsTruncated || cut
	}
	return result
}

func (r *runtime) createMockScenario(ctx context.Context, _ *mcp.CallToolRequest, input createMockInput) (*mcp.CallToolResult, scopedResult[mockMutationView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (mockMutationView, error) {
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return mockMutationView{}, err
		}
		if len(input.Description) > 4096 {
			return mockMutationView{}, codedError{code: "INVALID_ARGUMENT", message: "description must not exceed 4096 UTF-8 bytes"}
		}
		result, err := selected.client.WithMockMetadata().CreateMockScenario(ctx, selected.project, selected.environment, contract.CreateMockRequest{Name: input.Scenario, Description: input.Description})
		return mockMutationResult(result, nil), err
	})
}

func (r *runtime) putMockRoute(ctx context.Context, _ *mcp.CallToolRequest, input putMockRouteInput) (*mcp.CallToolResult, scopedResult[mockMutationView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (mockMutationView, error) {
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return mockMutationView{}, err
		}
		if err := validateArtifactName(input.Route, "route"); err != nil {
			return mockMutationView{}, err
		}
		if len(input.Draft.Body) > 1<<20 {
			return mockMutationView{}, codedError{code: "INVALID_ARGUMENT", message: "route response body must not exceed 1 MiB"}
		}
		result, err := selected.client.WithMockMetadata().PutMockRoute(ctx, selected.project, selected.environment, input.Scenario, input.Route, *input.Draft.contract())
		return mockMutationResult(result, nil), err
	})
}

func (r *runtime) importMockOpenAPI(ctx context.Context, _ *mcp.CallToolRequest, input importMockOpenAPIInput) (*mcp.CallToolResult, scopedResult[mockMutationView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (mockMutationView, error) {
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return mockMutationView{}, err
		}
		if err := validateServiceName(input.Service); err != nil {
			return mockMutationView{}, err
		}
		if len(input.Document) > 1<<20 {
			return mockMutationView{}, codedError{code: "INVALID_ARGUMENT", message: "OpenAPI document must not exceed 1 MiB"}
		}
		result, err := selected.client.WithMockMetadata().ImportMockOpenAPI(ctx, selected.project, selected.environment, input.Scenario, contract.ImportMockOpenAPIRequest{Service: input.Service, Document: input.Document})
		return mockMutationResult(result.Scenario, result.Warnings), err
	})
}

func (r *runtime) importMockRecording(ctx context.Context, _ *mcp.CallToolRequest, input importMockRecordingInput) (*mcp.CallToolResult, scopedResult[mockMutationView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (mockMutationView, error) {
		if err := requireSensitive(r, true); err != nil {
			return mockMutationView{}, err
		}
		if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
			return mockMutationView{}, err
		}
		if err := validateArtifactName(input.Recording, "recording"); err != nil {
			return mockMutationView{}, err
		}
		for _, service := range input.Services {
			if err := validateServiceName(service); err != nil {
				return mockMutationView{}, err
			}
		}
		result, err := selected.client.WithMockMetadata().ImportMockRecording(ctx, selected.project, selected.environment, input.Scenario, contract.ImportMockRecordingRequest{Recording: input.Recording, Services: input.Services})
		return mockMutationResult(result.Scenario, result.Warnings), err
	})
}

func (r *runtime) admitMockActivation(ctx context.Context, selected selectedEnvironment, input mockActivationInput, wait time.Duration) (mockActivationView, error) {
	var result mockActivationView
	if err := validateArtifactName(input.Scenario, "scenario"); err != nil {
		return result, err
	}
	key, persisted, err := prepareIdempotency("set-mock-scenario", input.Environment+"/"+input.Scenario, input.IdempotencyKey)
	if err != nil {
		return result, err
	}
	result = mockActivationView{Scenario: input.Scenario, IdempotencyKey: key}
	operation, err := selected.client.SetMockScenarioEnabled(ctx, selected.project, selected.environment, input.Scenario, contract.SetMockScenarioActivationRequest{Enabled: input.Enabled}, persisted)
	if err != nil {
		if uncertainMutation(err) {
			result.AdmissionUnknown = true
			result.Warning = "Admission response was lost; inspect scenario operations before retrying with this key."
			return result, nil
		}
		return result, err
	}
	result = mockActivationView{Scenario: input.Scenario, Operation: operation, IdempotencyKey: key}
	current, timedOut, waitErr := waitForOperation(ctx, selected.client, operation, wait)
	if waitErr != nil {
		result.TimedOutWaiting = true
		result.Warning = "Waiting ended after admission; inspect the returned operation. Cancellation does not cancel the provider change."
		return result, nil
	}
	result.Operation = current
	result.TimedOutWaiting = timedOut
	if current.State != "running" {
		page, err := selected.client.MockScenarioMetadata(ctx, selected.project, selected.environment, input.Scenario, contract.MockMetadataQuery{Limit: 1})
		if err == nil {
			result.Activation = &page.Scenario
		}
	}
	return result, nil
}

func (r *runtime) setMockScenarioEnabled(ctx context.Context, _ *mcp.CallToolRequest, input mockActivationInput) (*mcp.CallToolResult, scopedResult[mockActivationView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (mockActivationView, error) {
		wait, err := waitDuration(input.WaitSeconds)
		if err != nil {
			return mockActivationView{}, err
		}
		return r.admitMockActivation(ctx, selected, input, wait)
	})
}

func (r *runtime) disableAllMockScenarios(ctx context.Context, _ *mcp.CallToolRequest, input disableAllMocksInput) (*mcp.CallToolResult, scopedResult[disableAllMocksView], error) {
	return environmentCall(ctx, r, input.Environment, true, func(ctx context.Context, selected selectedEnvironment) (disableAllMocksView, error) {
		result := disableAllMocksView{Operations: []mockActivationView{}, Completed: []string{}, Pending: []string{}, Failed: []string{}}
		wait, err := waitDuration(input.WaitSeconds)
		if err != nil {
			return result, err
		}
		key, _, err := prepareIdempotency("disable-all-mocks", input.Environment, input.IdempotencyKey)
		if err != nil {
			return result, err
		}
		result.IdempotencyKey = key
		names := []string{}
		offset := 0
		for {
			page, err := selected.client.ListMockScenarioMetadata(ctx, selected.project, selected.environment, contract.MockMetadataQuery{Offset: offset, Limit: 500})
			if err != nil {
				return result, err
			}
			for _, scenario := range page.Scenarios {
				if string(scenario.Activation.State) != "disabled" {
					names = append(names, scenario.Name)
				}
			}
			if page.NextOffset == 0 {
				break
			}
			offset = page.NextOffset
		}
		if len(names) > 100 {
			return result, codedError{code: "RESULT_TOO_LARGE", message: "more than 100 active scenarios; disable named scenarios individually"}
		}
		deadline := time.Now().Add(wait)
		for i, name := range names {
			remaining := max(time.Duration(0), time.Until(deadline))
			receipt, err := r.admitMockActivation(ctx, selected, mockActivationInput{mockInput: mockInput{Environment: input.Environment, Scenario: name}, Enabled: false, IdempotencyKey: key}, remaining)
			if err != nil {
				result.Failed = append(result.Failed, name)
				result.Pending = append(result.Pending, names[i+1:]...)
				result.PartialFailure = true
				result.Warning = fmt.Sprintf("Scenario %s was not confirmed disabled. Inspect its state before retrying: %s", name, r.toolError(err))
				break
			}
			receipt.Activation = nil
			result.Operations = append(result.Operations, receipt)
			switch strings.ToLower(string(receipt.Operation.State)) {
			case "succeeded":
				result.Completed = append(result.Completed, name)
			case "running":
				result.Pending = append(result.Pending, names[i:]...)
				return result, nil
			default:
				result.Failed = append(result.Failed, name)
				result.Pending = append(result.Pending, names[i+1:]...)
				result.PartialFailure = true
				return result, nil
			}
		}
		return result, nil
	})
}
