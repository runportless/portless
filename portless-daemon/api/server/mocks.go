package server

import (
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/model"
)

func (s *Server) handleMocks(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
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
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, err := s.app.CreateMockScenario(request.Context(), project, environment, model.MockScenario{Name: input.Name, Description: input.Description}, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(input.Name))
				return
			}
			writeJSON(writer, http.StatusCreated, scenario)
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
			writeJSON(writer, http.StatusOK, scenario)
		case http.MethodDelete:
			if err := s.app.DeleteMockScenario(request.Context(), project, environment, scenarioName, principal.Actor); err != nil {
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
		var input model.MockRequest
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		preview, err := s.app.PreviewMock(request.Context(), project, environment, scenarioName, input)
		if err != nil {
			s.writeError(writer, err, subject(scenarioName))
			return
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
		if err := decodeJSON(request, &input); err != nil {
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
			if err := decodeJSON(request, &route); err != nil {
				writeDecodeError(writer, err)
				return
			}
			route.Name = routeName
			scenario, err := s.app.PutMockRoute(request.Context(), project, environment, scenarioName, route, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, scenario)
		case http.MethodDelete:
			scenario, err := s.app.DeleteMockRoute(request.Context(), project, environment, scenarioName, routeName, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, scenario)
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
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, warnings, err := s.app.ImportMockScenarioRecording(request.Context(), project, environment, scenarioName, input.Recording, input.Services, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, contract.MockScenarioMutation{Scenario: scenario, Warnings: nonNil(warnings)})
		case "openapi":
			if request.Method != http.MethodPost {
				methodNotAllowed(writer, http.MethodPost)
				return
			}
			var input contract.ImportMockOpenAPIRequest
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			scenario, warnings, err := s.app.ImportMockScenarioOpenAPI(request.Context(), project, environment, scenarioName, input.Service, []byte(input.Document), principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(scenarioName))
				return
			}
			writeJSON(writer, http.StatusOK, contract.MockScenarioMutation{Scenario: scenario, Warnings: nonNil(warnings)})
		default:
			writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock import route not found"})
		}
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock route not found"})
}
