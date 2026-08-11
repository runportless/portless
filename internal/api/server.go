package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/store"
)

const APIVersion = "1"

type Server struct {
	app       *application.Service
	auth      *auth.Manager
	assets    fs.FS
	files     http.Handler
	indexHTML []byte
}

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Subject     map[string]any `json:"subject,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	Remediation []Remediation  `json:"remediation,omitempty"`
}

type Remediation struct {
	Label   string `json:"label"`
	Command string `json:"command,omitempty"`
	URL     string `json:"url,omitempty"`
}

func New(app *application.Service, authManager *auth.Manager, assets fs.FS) (*Server, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded UI: %w", err)
	}
	return &Server{app: app, auth: authManager, assets: assets, files: http.FileServer(http.FS(assets)), indexHTML: index}, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(writer.Header())
	host := normalizedHost(request.Host)
	if service, environment, project, ok := applicationHost(host); ok {
		if strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/auth/") {
			writeAPIError(writer, http.StatusMisdirectedRequest, APIError{Code: "CONTROL_HOST_REQUIRED", Message: "control routes are not served on application hosts"})
			return
		}
		s.app.Proxy().ServeIngress(writer, request, model.EnvironmentSelector(project, environment), service)
		return
	}
	if !isControlHost(host) {
		writeAPIError(writer, http.StatusMisdirectedRequest, APIError{Code: "UNKNOWN_HOST", Message: "request host is not a Portless control or application host"})
		return
	}
	if request.URL.Path == "/api/v1/health" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ready": true, "apiVersion": APIVersion})
		return
	}
	if strings.HasPrefix(request.URL.Path, "/auth/claim/") {
		s.handleClaim(writer, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/v1/") {
		s.handleAPI(writer, request)
		return
	}
	s.serveUI(writer, request)
}

func (s *Server) handleClaim(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	code, err := url.PathUnescape(strings.TrimPrefix(request.URL.EscapedPath(), "/auth/claim/"))
	if err != nil || code == "" || strings.Contains(code, "/") {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_BROWSER_CLAIM", Message: "browser claim is malformed"})
		return
	}
	token, _, next, expiresAt, err := s.auth.ConsumeClaim(code)
	if err != nil {
		writeAPIError(writer, http.StatusUnauthorized, APIError{Code: "INVALID_BROWSER_CLAIM", Message: err.Error(), Remediation: []Remediation{{Label: "Open the UI again", Command: "portless ui"}}})
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: auth.SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expiresAt})
	http.Redirect(writer, request, next, http.StatusSeeOther)
}

func (s *Server) handleAPI(writer http.ResponseWriter, request *http.Request) {
	principal, ok := s.auth.Authenticate(request)
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, APIError{Code: "AUTHENTICATION_REQUIRED", Message: "authenticate with the Portless CLI or open a browser session with `portless ui`"})
		return
	}
	if isMutation(request.Method) {
		if err := s.auth.ValidateMutation(request, principal); err != nil {
			writeAPIError(writer, http.StatusForbidden, APIError{Code: "REQUEST_FORBIDDEN", Message: err.Error()})
			return
		}
	}
	segments := splitPath(strings.TrimPrefix(strings.Trim(request.URL.Path, "/"), "api/v1/"))
	if len(segments) == 0 {
		writeJSON(writer, http.StatusOK, map[string]any{"name": "portless", "apiVersion": APIVersion})
		return
	}
	switch segments[0] {
	case "system":
		s.handleSystem(writer, request)
	case "runtime":
		s.handleRuntime(writer, request, segments)
	case "session":
		s.handleSession(writer, request, segments, principal)
	case "browser-claims":
		s.handleBrowserClaims(writer, request, principal)
	case "projects":
		s.handleProjects(writer, request, segments, principal)
	case "environments":
		s.handleEnvironments(writer, request, segments, principal)
	default:
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "API route not found"})
	}
}

func (s *Server) handleSystem(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"name": "portless", "version": "dev", "apiVersion": APIVersion, "telemetry": false})
}

func (s *Server) handleRuntime(writer http.ResponseWriter, request *http.Request, segments []string) {
	if len(segments) == 1 && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, s.app.RuntimeStatus(request.Context()))
		return
	}
	if len(segments) == 1 && request.Method == http.MethodPut {
		var input struct {
			Preference string `json:"preference"`
		}
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		result, err := s.app.UseRuntime(request.Context(), input.Preference)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if len(segments) == 2 && segments[1] == "start" && request.Method == http.MethodPost {
		result := s.app.StartRuntime(request.Context())
		if result.State != "ready" {
			writeAPIError(writer, http.StatusServiceUnavailable, APIError{Code: "CONTAINER_RUNTIME_UNAVAILABLE", Message: result.Reason, Remediation: []Remediation{{Label: "Inspect Docker and Podman", Command: "portless runtime status"}}})
			return
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	methodNotAllowed(writer, http.MethodGet, http.MethodPut, http.MethodPost)
}

func (s *Server) handleSession(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	if len(segments) == 1 && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, map[string]any{"actor": principal.Actor, "browser": principal.Session, "csrf": principal.CSRF})
		return
	}
	if len(segments) == 2 && segments[1] == "logout" && request.Method == http.MethodPost {
		s.auth.Logout(request)
		http.SetCookie(writer, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "session route not found"})
}

func (s *Server) handleBrowserClaims(writer http.ResponseWriter, request *http.Request, principal auth.Principal) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if principal.Session {
		writeAPIError(writer, http.StatusForbidden, APIError{Code: "CLI_AUTH_REQUIRED", Message: "only the local CLI may create browser claims"})
		return
	}
	var input struct {
		Next string `json:"next"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	code, expiresAt, err := s.auth.IssueClaim(input.Next)
	if err != nil {
		s.writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"url": "http://portless.localhost/auth/claim/" + code, "expiresAt": expiresAt})
}

func (s *Server) handleProjects(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodGet:
			projects, err := s.app.Projects(ctx)
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"projects": nonNil(projects)})
		case http.MethodPost:
			var input struct {
				Name    string                    `json:"name"`
				Sources []application.SourceInput `json:"sources"`
			}
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
			project, environment, warnings, err := s.app.CreateProject(ctx, input.Name, input.Sources)
			if err != nil {
				s.writeError(writer, err, map[string]any{"project": input.Name})
				return
			}
			writeJSON(writer, http.StatusCreated, map[string]any{"project": project, "environment": environment, "warnings": nonNil(warnings)})
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) == 2 && segments[1] == "discover" {
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		if principal.Session {
			writeAPIError(writer, http.StatusForbidden, APIError{Code: "CLI_AUTH_REQUIRED", Message: "source discovery may only be requested by the local CLI"})
			return
		}
		var input struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		project, environment, warnings, err := s.app.Discover(ctx, input.Path, input.Name)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"project": project, "environment": environment, "warnings": nonNil(warnings)})
		return
	}
	project := segments[1]
	if err := model.ValidateProjectName(project); err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_PROJECT_NAME", Message: err.Error(), Subject: map[string]any{"project": project}})
		return
	}
	if len(segments) == 2 {
		s.handleProject(writer, request, project, principal)
		return
	}
	if len(segments) == 3 && segments[2] == "declaration" && request.Method == http.MethodGet {
		content, err := s.app.ExportProject(ctx, project)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Disposition", `attachment; filename="portless.project.json"`)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(content)
		return
	}
	writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "project route not found"})
}

func (s *Server) handleProject(writer http.ResponseWriter, request *http.Request, project string, principal auth.Principal) {
	switch request.Method {
	case http.MethodGet:
		result, err := s.app.Project(request.Context(), project)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodPatch:
		var input struct {
			Name     string `json:"name"`
			Revision int64  `json:"revision"`
		}
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		result, err := s.app.Rename(request.Context(), project, input.Name, input.Revision, principal.Actor)
		if err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodDelete:
		if err := s.app.Forget(request.Context(), project); err != nil {
			s.writeError(writer, err, map[string]any{"project": project})
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(writer, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleEnvironments(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 1 {
		switch request.Method {
		case http.MethodGet:
			environments, err := s.app.Environments(ctx, request.URL.Query().Get("project"))
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"environments": nonNil(environments)})
		case http.MethodPost:
			var input struct {
				Project string `json:"project"`
				Name    string `json:"name"`
				From    string `json:"from"`
			}
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
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "PATH_REQUIRED", Message: "the path query parameter is required"})
			return
		}
		environments, err := s.app.EnvironmentsForPath(ctx, path)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"environments": nonNil(environments)})
		return
	}
	if len(segments) == 2 && segments[1] == "select" {
		if request.Method != http.MethodPut {
			methodNotAllowed(writer, http.MethodPut)
			return
		}
		var input struct {
			Path        string `json:"path"`
			Project     string `json:"project"`
			Environment string `json:"environment"`
		}
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		if err := s.app.SelectEnvironment(ctx, input.Path, input.Project, input.Environment); err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if len(segments) < 3 {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "environment route not found"})
		return
	}
	project, environment := segments[1], segments[2]
	if err := model.ValidateProjectName(project); err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_PROJECT_NAME", Message: err.Error()})
		return
	}
	if err := model.ValidateEnvironmentName(environment); err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_ENVIRONMENT_NAME", Message: err.Error()})
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
		s.handleBindings(writer, request, project, environment, segments)
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
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "environment route not found"})
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
	writeJSON(writer, http.StatusOK, map[string]any{"environment": result, "warnings": nonNil(warnings)})
}

func (s *Server) handleUp(writer http.ResponseWriter, request *http.Request, project, environment string, principal auth.Principal) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	operation, err := s.app.Up(request.Context(), project, environment, principal.Actor, request.Header.Get("Idempotency-Key"))
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
	var input struct {
		RemoveVolumes bool `json:"removeVolumes"`
	}
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

func (s *Server) handleBindings(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if len(segments) != 5 || request.Method != http.MethodPut {
		methodNotAllowed(writer, http.MethodPut)
		return
	}
	var binding model.ComponentBinding
	if err := decodeJSON(request, &binding); err != nil {
		writeDecodeError(writer, err)
		return
	}
	result, err := s.app.SetBinding(request.Context(), project, environment, segments[4], binding)
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "service": segments[4]})
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) handleSources(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if len(segments) != 5 || request.Method != http.MethodPut {
		methodNotAllowed(writer, http.MethodPut)
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	if input.Path == "" {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "PATH_REQUIRED", Message: "source path is required"})
		return
	}
	result, warnings, err := s.app.SetSource(request.Context(), project, environment, segments[4], input.Path)
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "source": segments[4]})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"environment": result, "warnings": nonNil(warnings)})
}

func (s *Server) handleServices(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	current, err := s.app.Environment(request.Context(), project, environment)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 4 && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, map[string]any{"services": nonNil(current.Services)})
		return
	}
	if len(segments) < 5 {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "service route not found"})
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
		s.writeError(writer, store.ErrNotFound, map[string]any{"project": project, "environment": environment, "service": serviceName})
		return
	}
	if len(segments) == 5 && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, selected)
		return
	}
	if len(segments) == 6 && segments[5] == "configuration" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, maskedConfiguration(selected.ServiceDefinition))
		return
	}
	if len(segments) == 6 && request.Method == http.MethodPost {
		var operation model.Operation
		var actionErr error
		switch segments[5] {
		case "start", "restart":
			operation, actionErr = s.app.RestartService(request.Context(), project, environment, serviceName, principal.Actor)
		case "stop":
			operation, actionErr = s.app.StopService(request.Context(), project, environment, serviceName, principal.Actor)
		default:
			writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "service action not found"})
			return
		}
		if actionErr != nil {
			s.writeError(writer, actionErr, map[string]any{"project": project, "environment": environment, "service": serviceName})
			return
		}
		writeJSON(writer, http.StatusAccepted, operation)
		return
	}
	writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "service route not found"})
}

func (s *Server) handleConnections(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	current, err := s.app.Environment(request.Context(), project, environment)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 4 {
		writeJSON(writer, http.StatusOK, map[string]any{"connections": nonNil(current.Connections)})
		return
	}
	if len(segments) == 6 {
		for _, connection := range current.Connections {
			if connection.Source == segments[4] && connection.Target == segments[5] {
				writeJSON(writer, http.StatusOK, connection)
				return
			}
		}
		s.writeError(writer, store.ErrNotFound, environmentSubject(project, environment))
		return
	}
	writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "connection route not found"})
}

func (s *Server) handleLogs(writer http.ResponseWriter, request *http.Request, project, environment string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	service := request.URL.Query().Get("service")
	if service == "" {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "SERVICE_REQUIRED", Message: "the service query parameter is required"})
		return
	}
	lines, err := s.app.Logs(request.Context(), project, environment, service, queryInt(request, "limit", 500))
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "service": service})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"project": project, "environment": environment, "service": service, "lines": nonNil(lines)})
}

func (s *Server) handleTraffic(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if len(segments) < 5 || (segments[4] != "http" && segments[4] != "tcp") {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "traffic route not found"})
		return
	}
	protocol := model.ProtocolHTTP
	if segments[4] == "tcp" {
		protocol = model.ProtocolTCP
	}
	all := s.app.Traffic(project, environment, queryInt(request, "limit", 250))
	filtered := make([]model.TrafficEvent, 0, len(all))
	for _, event := range all {
		isHTTP := event.Protocol == model.ProtocolHTTP
		if (protocol == model.ProtocolHTTP && isHTTP) || (protocol == model.ProtocolTCP && !isHTTP) {
			filtered = append(filtered, event)
		}
	}
	if len(segments) == 6 {
		sequence, err := strconv.ParseInt(segments[5], 10, 64)
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_TRAFFIC_SEQUENCE", Message: "traffic sequence must be an integer"})
			return
		}
		for _, event := range filtered {
			if event.Sequence == sequence {
				writeJSON(writer, http.StatusOK, event)
				return
			}
		}
		s.writeError(writer, store.ErrNotFound, environmentSubject(project, environment))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"traffic": filtered})
}

func (s *Server) handleStream(writer http.ResponseWriter, request *http.Request, project, environment string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, APIError{Code: "STREAM_UNAVAILABLE", Message: "streaming is unavailable"})
		return
	}
	if _, err := s.app.Environment(request.Context(), project, environment); err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	topics := request.URL.Query()["topic"]
	scope := model.EnvironmentSelector(project, environment)
	subscription := s.app.Broker().Subscribe(request.Context(), scope, topics)
	defer subscription.Close()
	_, _ = io.WriteString(writer, "event: stream.ready\ndata: {\"ready\":true}\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-keepalive.C:
			_, _ = io.WriteString(writer, ": keepalive\n\n")
			flusher.Flush()
		case event, open := <-subscription.C:
			if !open {
				return
			}
			payload, _ := json.Marshal(event.Data)
			_, _ = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)
			flusher.Flush()
		}
	}
}

func (s *Server) handleRecordings(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 4 {
		switch request.Method {
		case http.MethodGet:
			recordings, err := s.app.Recordings(ctx, project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"recordings": nonNil(recordings)})
		case http.MethodPost:
			var recording model.Recording
			if err := decodeJSON(request, &recording); err != nil {
				writeDecodeError(writer, err)
				return
			}
			recording.Project, recording.Environment = project, environment
			created, err := s.app.StartRecording(ctx, recording, principal.Actor)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusCreated, created)
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) < 5 {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "recording route not found"})
		return
	}
	name := segments[4]
	if len(segments) == 5 {
		switch request.Method {
		case http.MethodGet:
			recording, err := findRecording(ctx, s.app, project, environment, name)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, recording)
		case http.MethodDelete:
			if err := s.app.DeleteRecording(ctx, project, environment, name, principal.Actor); err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodDelete)
		}
		return
	}
	if len(segments) == 6 && segments[5] == "stop" && request.Method == http.MethodPost {
		if err := s.app.StopRecording(ctx, project, environment, name, principal.Actor); err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		recording, _ := findRecording(ctx, s.app, project, environment, name)
		writeJSON(writer, http.StatusOK, recording)
		return
	}
	if len(segments) == 6 && segments[5] == "export" && request.Method == http.MethodGet {
		traffic, err := s.app.RecordedTraffic(ctx, project, environment, name, 10_000)
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))
		writeJSON(writer, http.StatusOK, map[string]any{"schemaVersion": 1, "project": project, "environment": environment, "recording": name, "traffic": traffic})
		return
	}
	writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "recording route not found"})
}

func (s *Server) handleFaults(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 4 {
		switch request.Method {
		case http.MethodGet:
			faults, err := s.app.Faults(ctx, project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"faults": nonNil(faults)})
		case http.MethodPost:
			var fault model.FaultRule
			if err := decodeJSON(request, &fault); err != nil {
				writeDecodeError(writer, err)
				return
			}
			fault.Project, fault.Environment = project, environment
			created, err := s.app.CreateFault(ctx, fault, principal.Actor)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusCreated, created)
		default:
			methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		}
		return
	}
	if len(segments) == 5 && segments[4] == "disable-all" && request.Method == http.MethodPost {
		count, err := s.app.DisableAllFaults(ctx, project, environment, principal.Actor)
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"disabled": count})
		return
	}
	if len(segments) == 5 {
		name := segments[4]
		if request.Method == http.MethodGet {
			fault, err := findFault(ctx, s.app, project, environment, name)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, fault)
			return
		}
		if request.Method == http.MethodDelete {
			if err := s.app.DisableFault(ctx, project, environment, name, principal.Actor); err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "fault route not found"})
}

func (s *Server) handleOperations(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if request.Method == http.MethodGet && len(segments) == 4 {
		operations, err := s.app.Operations(request.Context(), project, environment, queryInt(request, "limit", 100))
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"operations": nonNil(operations)})
		return
	}
	if request.Method != http.MethodGet || len(segments) < 5 {
		writeAPIError(writer, http.StatusNotImplemented, APIError{Code: "OPERATION_CANCEL_UNAVAILABLE", Message: "operation cancellation is not available after execution has begun"})
		return
	}
	number, err := strconv.ParseInt(segments[4], 10, 64)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_OPERATION_NUMBER", Message: "operation number must be an integer"})
		return
	}
	operation, err := s.app.Operation(request.Context(), project, environment, number)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 6 && segments[5] == "events" {
		writeJSON(writer, http.StatusOK, map[string]any{"events": nonNil(operation.Events)})
		return
	}
	writeJSON(writer, http.StatusOK, operation)
}

func (s *Server) handleTimeline(writer http.ResponseWriter, request *http.Request, project, environment string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	events, err := s.app.Timeline(request.Context(), project, environment, queryInt(request, "limit", 250))
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"timeline": nonNil(events)})
}

func (s *Server) serveUI(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet, http.MethodHead)
		return
	}
	clean := strings.TrimPrefix(request.URL.Path, "/")
	if clean != "" {
		if info, err := fs.Stat(s.assets, clean); err == nil && !info.IsDir() {
			s.files.ServeHTTP(writer, request)
			return
		}
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(s.indexHTML)
	}
}

func (s *Server) writeError(writer http.ResponseWriter, err error, subject map[string]any) {
	status := http.StatusBadRequest
	apiError := APIError{Code: "REQUEST_FAILED", Message: err.Error(), Subject: subject}
	var conflict application.NameConflictError
	switch {
	case errors.As(err, &conflict):
		status = http.StatusConflict
		apiError.Code = "PROJECT_NAME_TAKEN"
		apiError.Details = map[string]any{"suggestions": conflict.Suggestions}
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
		apiError.Code = "RESOURCE_NOT_FOUND"
	case errors.Is(err, store.ErrConflict):
		status = http.StatusConflict
		apiError.Code = "REVISION_CONFLICT"
	case errors.Is(err, store.ErrNameTaken):
		status = http.StatusConflict
		apiError.Code = "PROJECT_NAME_TAKEN"
	case errors.Is(err, store.ErrAlreadyExists):
		status = http.StatusConflict
		apiError.Code = "RESOURCE_ALREADY_EXISTS"
	case errors.As(err, &application.RuntimeInUseError{}):
		status = http.StatusConflict
		apiError.Code = "CONTAINER_RUNTIME_IN_USE"
	case container.IsUnavailable(err):
		status = http.StatusServiceUnavailable
		apiError.Code = "CONTAINER_RUNTIME_UNAVAILABLE"
	}
	writeAPIError(writer, status, apiError)
}

func setSecurityHeaders(headers http.Header) {
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Frame-Options", "DENY")
	headers.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, apiError APIError) {
	writeJSON(writer, status, ErrorEnvelope{Error: apiError})
}

func writeDecodeError(writer http.ResponseWriter, err error) {
	writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_JSON", Message: err.Error()})
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}

func methodNotAllowed(writer http.ResponseWriter, methods ...string) {
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(writer, http.StatusMethodNotAllowed, APIError{Code: "METHOD_NOT_ALLOWED", Message: "method not allowed"})
}

func normalizedHost(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return strings.Trim(strings.ToLower(host), "[]")
	}
	return strings.Trim(strings.ToLower(hostPort), "[]")
}

func isControlHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "portless.localhost"
}

func applicationHost(host string) (service, environment, project string, ok bool) {
	if !strings.HasSuffix(host, ".localhost") || host == "portless.localhost" {
		return "", "", "", false
	}
	labels := strings.Split(strings.TrimSuffix(host, ".localhost"), ".")
	if len(labels) != 3 || model.ValidateServiceName(labels[0]) != nil || model.ValidateEnvironmentName(labels[1]) != nil || model.ValidateProjectName(labels[2]) != nil {
		return "", "", "", false
	}
	return labels[0], labels[1], labels[2], true
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || strings.Contains(decoded, "/") {
			return []string{"__invalid__"}
		}
		result = append(result, decoded)
	}
	return result
}

func isMutation(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func queryInt(request *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(request.URL.Query().Get(key))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func findRecording(ctx context.Context, app *application.Service, project, environment, name string) (model.Recording, error) {
	items, err := app.Recordings(ctx, project, environment)
	if err != nil {
		return model.Recording{}, err
	}
	for _, item := range items {
		if strings.EqualFold(item.Name, name) {
			return item, nil
		}
	}
	return model.Recording{}, store.ErrNotFound
}

func findFault(ctx context.Context, app *application.Service, project, environment, name string) (model.FaultRule, error) {
	items, err := app.Faults(ctx, project, environment)
	if err != nil {
		return model.FaultRule{}, err
	}
	for _, item := range items {
		if strings.EqualFold(item.Name, name) {
			return item, nil
		}
	}
	return model.FaultRule{}, store.ErrNotFound
}

func environmentSubject(project, environment string) map[string]any {
	return map[string]any{"project": project, "environment": environment}
}

func maskedConfiguration(definition model.ServiceDefinition) map[string]any {
	environment := make([]map[string]any, 0, len(definition.Environment))
	for name := range definition.Environment {
		classification := "public"
		value := definition.Environment[name]
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "KEY") {
			classification = "masked"
			value = "••••••••"
		}
		environment = append(environment, map[string]any{"key": name, "value": value, "classification": classification, "source": "discovered model"})
	}
	return map[string]any{"service": definition.Name, "command": definition.Command, "workingDirectory": definition.WorkingDirectory, "portEnvironment": definition.PortEnvironment, "environment": environment, "health": definition.Health}
}
