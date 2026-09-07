package server

import (
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/model"
)

func (s *Server) handleMocks(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	if request.Method == http.MethodGet && (request.URL.Query().Get("view") == "metadata" || (len(segments) == 7 && segments[5] == "routes")) {
		s.handleMockMetadata(writer, request, project, environment, segments)
		return
	}
	subject := func(name string) map[string]any {
		return map[string]any{"project": project, "environment": environment, "scenario": name}
	}
	if len(segments) == 4 {
		switch request.Method {
		case http.MethodGet:
			scenarios, err := s.app.MockScenarios(request.Context(), project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, contract.MockScenarioList{Scenarios: nonNil(scenarios)})
		case http.MethodPost:
			var input contract.CreateMockRequest
			if err := decodeMockJSON(writer, request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, err := s.app.CreateMockScenario(request.Context(), project, environment, model.MockScenario{Name: input.Name, Description: input.Description}, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(input.Name))
				return
			}
			writeJSON(writer, http.StatusCreated, mockResponse(request, scenario))
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) < 5 {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock route not found"})
		return
	}
	scenarioName := segments[4]
	if len(segments) == 5 {
		switch request.Method {
		case http.MethodGet:
			scenario, err := s.app.MockScenario(request.Context(), project, environment, scenarioName)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, mockResponse(request, scenario))
		case http.MethodDelete:
			if request.URL.Query().Get("mode") == "preview" {
				s.writeMockDeletionPreview(writer, request, project, environment, scenarioName, "")
				return
			}
			expected, ok := requestResourceVersion(writer, request, principal.Actor == "MCP")
			if !ok {
				return
			}
			if err := s.app.DeleteMockScenario(request.Context(), project, environment, scenarioName, principal.Actor, expected); err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodDelete)
		}
		return
	}
	if len(segments) == 6 && segments[5] == "preview" {
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		var input contract.PreviewMockRequest
		if err := decodeMockJSON(writer, request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		preview, err := s.app.PreviewMock(request.Context(), project, environment, scenarioName, input.Request, input.Draft, input.OriginalRoute)
		if err != nil {
			s.writeError(writer, err, subject(scenarioName))
			return
		}
		if request.Header.Get(contract.MockMetadataHeader) == "1" {
			preview.Headers = nil
			preview.Body = ""
		}
		writeJSON(writer, http.StatusOK, preview)
		return
	}
	if len(segments) == 6 && segments[5] == "activation" {
		if request.Method != http.MethodPut {
			methodNotAllowed(writer, http.MethodPut)
			return
		}
		var input contract.SetMockScenarioActivationRequest
		if err := decodeMockJSON(writer, request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		operation, err := s.app.SetMockScenarioEnabled(request.Context(), project, environment, scenarioName, input.Enabled, principal.Actor, request.Header.Get("Idempotency-Key"))
		if err != nil {
			s.writeError(writer, err, subject(scenarioName))
			return
		}
		writeJSON(writer, http.StatusAccepted, operation)
		return
	}
	if len(segments) == 7 && segments[5] == "routes" {
		routeName := segments[6]
		switch request.Method {
		case http.MethodPut:
			var route model.MockRoute
			if err := decodeMockJSON(writer, request, &route); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, err := s.app.PutMockRoute(request.Context(), project, environment, scenarioName, routeName, route, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, mockResponse(request, scenario))
		case http.MethodDelete:
			if request.URL.Query().Get("mode") == "preview" {
				s.writeMockDeletionPreview(writer, request, project, environment, scenarioName, routeName)
				return
			}
			expected, ok := requestResourceVersion(writer, request, principal.Actor == "MCP")
			if !ok {
				return
			}
			scenario, err := s.app.DeleteMockRoute(request.Context(), project, environment, scenarioName, routeName, principal.Actor, expected)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, mockResponse(request, scenario))
		default:
			methodNotAllowed(writer, http.MethodPut, http.MethodDelete)
		}
		return
	}
	if len(segments) == 7 && segments[5] == "imports" {
		switch segments[6] {
		case "recording":
			if request.Method != http.MethodPost {
				methodNotAllowed(writer, http.MethodPost)
				return
			}
			var input contract.ImportMockRecordingRequest
			if err := decodeMockJSON(writer, request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, warnings, err := s.app.ImportMockScenarioRecording(request.Context(), project, environment, scenarioName, input.Recording, input.Services, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, contract.MockScenarioMutation{Scenario: mockResponse(request, scenario), Warnings: nonNil(warnings)})
		case "openapi":
			if request.Method != http.MethodPost {
				methodNotAllowed(writer, http.MethodPost)
				return
			}
			var input contract.ImportMockOpenAPIRequest
			if err := decodeMockJSON(writer, request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, warnings, err := s.app.ImportMockScenarioOpenAPI(request.Context(), project, environment, scenarioName, input.Service, []byte(input.Document), principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, contract.MockScenarioMutation{Scenario: mockResponse(request, scenario), Warnings: nonNil(warnings)})
		default:
			writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock import route not found"})
		}
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock route not found"})
}

func (s *Server) writeMockDeletionPreview(writer http.ResponseWriter, request *http.Request, project, environment, scenario, route string) {
	preview, err := s.app.PreviewMockDeletion(request.Context(), project, environment, scenario, route)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, 200, preview)
}
