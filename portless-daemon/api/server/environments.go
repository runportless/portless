package server

import (
	"net/http"
	"strings"

	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/auth"
	"github.com/portless-run/portless/portless-daemon/controlplane"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (s *Server) handleEnvironments(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodGet:
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			environments, err := s.app.Environments(ctx, request.URL.Query().Get("project"))
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, contract.EnvironmentList{Environments: limited(nonNil(environments), limit), Total: len(environments)})
		case http.MethodPost:
			var input contract.CloneEnvironmentRequest
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			if input.From == "" {
				input.From = "local"
			}
			environment, err := s.app.CloneEnvironment(ctx, input.Project, input.From, input.Name)
			if err != nil {
				s.writeError(writer, err, map[string]any{"project": input.Project, "environment": input.Name})
				return
			}
			writeJSON(writer, http.StatusCreated, environment)
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) == 2 && segments[1] == "resolve" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		path := request.URL.Query().Get("path")
		if path == "" {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "PATH_REQUIRED", Message: "the path query parameter is required"})
			return
		}
		environments, err := s.app.EnvironmentsForPath(ctx, path)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, contract.EnvironmentList{Environments: nonNil(environments)})
		return
	}
	if len(segments) == 2 && segments[1] == "context" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		path := request.URL.Query().Get("path")
		if path == "" {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "PATH_REQUIRED", Message: "the path query parameter is required"})
			return
		}
		resolved, err := s.app.EnvironmentContext(ctx, path)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, contract.EnvironmentContext{Resolution: resolved.Resolution, Environment: resolved.Environment, Candidates: nonNil(resolved.Candidates)})
		return
	}
	if len(segments) == 2 && segments[1] == "select" {
		switch request.Method {
		case http.MethodPut:
			var input contract.SelectEnvironmentRequest
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			if err := s.app.SelectEnvironment(ctx, input.Path, input.Project, input.Environment); err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			path := request.URL.Query().Get("path")
			if path == "" {
				writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "PATH_REQUIRED", Message: "the path query parameter is required"})
				return
			}
			cleared, err := s.app.ClearEnvironmentSelection(ctx, path)
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, contract.ClearEnvironmentSelectionResponse{Cleared: cleared})
		default:
			methodNotAllowed(writer, http.MethodPut, http.MethodDelete)
		}
		return
	}
	if len(segments) < 3 {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "environment route not found"})
		return
	}
	project, environment := segments[1], segments[2]
	if err := model.ValidateProjectName(project); err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_PROJECT_NAME", Message: err.Error()})
		return
	}
	if err := model.ValidateEnvironmentName(environment); err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_ENVIRONMENT_NAME", Message: err.Error()})
		return
	}
	if len(segments) == 3 {
		s.handleEnvironment(writer, request, project, environment)
		return
	}
	switch segments[3] {
	case "rescan":
		s.handleRescan(writer, request, project, environment)
	case "up":
		s.handleUp(writer, request, project, environment, principal)
	case "down":
		s.handleDown(writer, request, project, environment, principal)
	case "bindings":
		s.handleBindings(writer, request, project, environment, segments, principal)
	case "mocks":
		s.handleMocks(writer, request, project, environment, segments, principal)
	case "sources":
		s.handleSources(writer, request, project, environment, segments)
	case "services":
		s.handleServices(writer, request, project, environment, segments, principal)
	case "connections":
		s.handleConnections(writer, request, project, environment, segments)
	case "logs":
		s.handleLogs(writer, request, project, environment)
	case "traffic":
		s.handleTraffic(writer, request, project, environment, segments)
	case "stream":
		s.handleStream(writer, request, project, environment)
	case "recordings":
		s.handleRecordings(writer, request, project, environment, segments, principal)
	case "faults":
		s.handleFaults(writer, request, project, environment, segments, principal)
	case "operations":
		s.handleOperations(writer, request, project, environment, segments)
	case "timeline":
		s.handleTimeline(writer, request, project, environment)
	default:
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "environment route not found"})
	}
}

func (s *Server) handleEnvironment(writer http.ResponseWriter, request *http.Request, project, environment string) {
	switch request.Method {
	case http.MethodGet:
		result, err := s.app.Environment(request.Context(), project, environment)
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodDelete:
		if err := s.app.ForgetEnvironment(request.Context(), project, environment); err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, http.MethodGet, http.MethodDelete)
	}
}

func (s *Server) handleRescan(writer http.ResponseWriter, request *http.Request, project, environment string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	result, warnings, err := s.app.Rescan(request.Context(), project, environment)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writeJSON(writer, http.StatusOK, contract.EnvironmentMutation{Environment: result, Warnings: nonNil(warnings)})
}

func (s *Server) handleUp(writer http.ResponseWriter, request *http.Request, project, environment string, principal auth.Principal) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	var input contract.UpRequest
	if request.Body != nil && request.ContentLength != 0 {
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
	}
	operation, err := s.app.Up(request.Context(), project, environment, principal.Actor, request.Header.Get("Idempotency-Key"), controlplane.UpOptions{DebugServices: input.DebugServices, Managed: input.Managed})
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writeJSON(writer, http.StatusAccepted, operation)
}

func (s *Server) handleDown(writer http.ResponseWriter, request *http.Request, project, environment string, principal auth.Principal) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	var input contract.DownRequest
	if request.Body != nil && request.ContentLength != 0 {
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
	}
	operation, err := s.app.Down(request.Context(), project, environment, principal.Actor, request.Header.Get("Idempotency-Key"), input.RemoveVolumes)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writeJSON(writer, http.StatusAccepted, operation)
}

func (s *Server) handleBindings(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	if len(segments) != 5 || request.Method != http.MethodPut {
		methodNotAllowed(writer, http.MethodPut)
		return
	}
	var binding model.ComponentBinding
	if err := decodeJSON(request, &binding); err != nil {
		writeDecodeError(writer, err)
		return
	}
	result, err := s.app.ChangeBinding(request.Context(), project, environment, segments[4], binding, principal.Actor, request.Header.Get("Idempotency-Key"))
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "service": segments[4]})
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func (s *Server) handleSources(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if len(segments) != 5 || request.Method != http.MethodPut {
		methodNotAllowed(writer, http.MethodPut)
		return
	}
	var input contract.SetSourceRequest
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	if input.Path == "" {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "PATH_REQUIRED", Message: "source path is required"})
		return
	}
	result, warnings, err := s.app.SetSource(request.Context(), project, environment, segments[4], input.Path)
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "source": segments[4]})
		return
	}
	writeJSON(writer, http.StatusOK, contract.EnvironmentMutation{Environment: result, Warnings: nonNil(warnings)})
}

func (s *Server) handleServices(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	current, err := s.app.Environment(request.Context(), project, environment)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 4 && request.Method == http.MethodGet {
		limit, limitErr := queryLimit(request, 250, 1000)
		if limitErr != nil {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, contract.ServiceList{Services: limited(nonNil(current.Services), limit)})
		return
	}
	if len(segments) < 5 {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "service route not found"})
		return
	}
	serviceName := segments[4]
	var selected *model.Service
	for index := range current.Services {
		if strings.EqualFold(current.Services[index].Name, serviceName) {
			selected = &current.Services[index]
			break
		}
	}
	if selected == nil {
		s.writeError(writer, controlplane.ErrNotFound, map[string]any{"project": project, "environment": environment, "service": serviceName})
		return
	}
	if len(segments) == 5 && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, selected)
		return
	}
	if len(segments) == 6 && segments[5] == "configuration" && request.Method == http.MethodGet {
		configuration, err := s.app.ServiceConfiguration(request.Context(), project, environment, serviceName)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "service": serviceName})
			return
		}
		writeJSON(writer, http.StatusOK, configuration)
		return
	}
	if len(segments) == 6 && request.Method == http.MethodPost {
		var operation model.Operation
		var actionErr error
		idempotencyKey := request.Header.Get("Idempotency-Key")
		switch segments[5] {
		case "start":
			operation, actionErr = s.app.StartService(request.Context(), project, environment, serviceName, principal.Actor, idempotencyKey)
		case "restart":
			operation, actionErr = s.app.RestartService(request.Context(), project, environment, serviceName, principal.Actor, idempotencyKey)
		case "stop":
			operation, actionErr = s.app.StopService(request.Context(), project, environment, serviceName, principal.Actor, idempotencyKey)
		case "manage":
			operation, actionErr = s.app.ManageService(request.Context(), project, environment, serviceName, principal.Actor, idempotencyKey)
		case "debug":
			operation, actionErr = s.app.DebugService(request.Context(), project, environment, serviceName, principal.Actor, idempotencyKey)
		default:
			writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "service action not found"})
			return
		}
		if actionErr != nil {
			s.writeError(writer, actionErr, map[string]any{"project": project, "environment": environment, "service": serviceName})
			return
		}
		writeJSON(writer, http.StatusAccepted, operation)
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "service route not found"})
}

func (s *Server) handleConnections(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	connections, err := s.app.Connections(request.Context(), project, environment)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 4 {
		limit, limitErr := queryLimit(request, 250, 1000)
		if limitErr != nil {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, contract.ConnectionList{Connections: limited(nonNil(connections), limit)})
		return
	}
	if len(segments) == 6 {
		for _, connection := range connections {
			if connection.Source == segments[4] && connection.Target == segments[5] {
				writeJSON(writer, http.StatusOK, connection)
				return
			}
		}
		s.writeError(writer, controlplane.ErrNotFound, environmentSubject(project, environment))
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "connection route not found"})
}

func (s *Server) handleLogs(writer http.ResponseWriter, request *http.Request, project, environment string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	limit, err := queryLimit(request, 500, 10_000)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: err.Error()})
		return
	}
	since, err := querySince(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_SINCE", Message: err.Error()})
		return
	}
	service := request.URL.Query().Get("service")
	entries, err := s.app.Logs(request.Context(), project, environment, service, limit, since)
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "service": service})
		return
	}
	writeJSON(writer, http.StatusOK, contract.LogList{Project: project, Environment: environment, Service: service, Entries: nonNil(entries)})
}
