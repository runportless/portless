package server

import (
	"net/http"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/model"
)

func (s *Server) handleMocks(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	subject := func(name string) map[string]any {
		return map[string]any{"project": project, "environment": environment, "mock": name}
	}
	if len(segments) == 4 {
		switch request.Method {
		case http.MethodGet:
			profiles, err := s.app.MockProfiles(request.Context(), project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, contract.MockProfileList{Mocks: nonNil(profiles)})
		case http.MethodPost:
			var input contract.CreateMockRequest
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			profile := model.MockProfile{Name: input.Name, Service: input.Service, Description: input.Description}
			created, warnings, err := s.app.CreateMockProfileFromSources(request.Context(), project, environment, profile, input.FromRecording, []byte(input.OpenAPIDocument), principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(profile.Name))
				return
			}
			writeJSON(writer, http.StatusCreated, contract.MockMutation{Mock: created, Warnings: nonNil(warnings)})
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) < 5 {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock route not found"})
		return
	}
	profileName := segments[4]
	if len(segments) == 5 {
		switch request.Method {
		case http.MethodGet:
			profile, err := s.app.MockProfile(request.Context(), project, environment, profileName)
			if err != nil {
				s.writeError(writer, err, subject(profileName))
				return
			}
			writeJSON(writer, http.StatusOK, profile)
		case http.MethodDelete:
			if err := s.app.DeleteMockProfile(request.Context(), project, environment, profileName, principal.Actor); err != nil {
				s.writeError(writer, err, subject(profileName))
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
		preview, err := s.app.PreviewMock(request.Context(), project, environment, profileName, input)
		if err != nil {
			s.writeError(writer, err, subject(profileName))
			return
		}
		writeJSON(writer, http.StatusOK, preview)
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
			profile, err := s.app.PutMockRoute(request.Context(), project, environment, profileName, route, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(profileName))
				return
			}
			writeJSON(writer, http.StatusOK, profile)
		case http.MethodDelete:
			profile, err := s.app.DeleteMockRoute(request.Context(), project, environment, profileName, routeName, principal.Actor)
			if err != nil {
				s.writeError(writer, err, subject(profileName))
				return
			}
			writeJSON(writer, http.StatusOK, profile)
		default:
			methodNotAllowed(writer, http.MethodPut, http.MethodDelete)
		}
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock route not found"})
}
