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
	"github.com/portless-run/portless/internal/daemon"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/store"
)

const APIVersion = "3"

type Server struct {
	app           *application.Service
	auth          *auth.Manager
	daemonControl DaemonControl
	assets        fs.FS
	files         http.Handler
	indexHTML     []byte
}

type DaemonControl interface {
	Status(context.Context) (daemon.Identity, error)
	Restart(context.Context, string) (daemon.ShutdownResponse, error)
}

type daemonStatusResponse struct {
	State              string    `json:"state"`
	PID                int       `json:"pid"`
	StartedAt          time.Time `json:"startedAt"`
	InstanceID         string    `json:"instanceId"`
	BuildID            string    `json:"buildId"`
	ProtocolVersion    string    `json:"protocolVersion"`
	APIVersion         string    `json:"apiVersion"`
	HandoffReady       bool      `json:"handoffReady"`
	RecoveryProblems   []string  `json:"recoveryProblems"`
	ActiveEnvironments []string  `json:"activeEnvironments"`
}

type daemonRestartResponse struct {
	Restarting         bool     `json:"restarting"`
	PreviousInstanceID string   `json:"previousInstanceId"`
	Handoff            bool     `json:"handoff"`
	ActiveEnvironments []string `json:"activeEnvironments"`
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

func New(app *application.Service, authManager *auth.Manager, assets fs.FS, daemonControl DaemonControl) (*Server, error) {
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded UI: %w", err)
	}
	return &Server{app: app, auth: authManager, daemonControl: daemonControl, assets: assets, files: http.FileServer(http.FS(assets)), indexHTML: index}, nil
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
	case "daemon":
		s.handleDaemon(writer, request, segments)
	case "runtime":
		s.handleRuntime(writer, request, segments, principal)
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

func (s *Server) handleDaemon(writer http.ResponseWriter, request *http.Request, segments []string) {
	if s.daemonControl == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, APIError{Code: "DAEMON_CONTROL_UNAVAILABLE", Message: "daemon lifecycle information is unavailable", Remediation: []Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
		return
	}
	if len(segments) == 1 {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		identity, err := s.daemonControl.Status(request.Context())
		if err != nil {
			writeAPIError(writer, http.StatusServiceUnavailable, APIError{Code: "DAEMON_STATE_UNAVAILABLE", Message: err.Error(), Remediation: []Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
			return
		}
		writeJSON(writer, http.StatusOK, daemonStatusResponse{
			State: identity.State, PID: identity.PID, StartedAt: identity.StartedAt,
			InstanceID: identity.InstanceID, BuildID: identity.BuildID,
			ProtocolVersion: identity.ProtocolVersion, APIVersion: identity.APIVersion,
			HandoffReady:       identity.HandoffReady,
			RecoveryProblems:   nonNil(append([]string(nil), identity.RecoveryProblems...)),
			ActiveEnvironments: nonNil(append([]string(nil), identity.ActiveEnvironments...)),
		})
		return
	}
	if len(segments) != 2 || segments[1] != "restart" {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "daemon route not found"})
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	var input struct {
		InstanceID string `json:"instanceId"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	if strings.TrimSpace(input.InstanceID) == "" {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "DAEMON_INSTANCE_REQUIRED", Message: "instanceId is required"})
		return
	}
	result, err := s.daemonControl.Restart(request.Context(), input.InstanceID)
	if err != nil {
		var lifecycleError *daemon.LifecycleError
		if errors.As(err, &lifecycleError) {
			details := map[string]any{
				"activeEnvironments": nonNil(append([]string(nil), lifecycleError.ActiveEnvironments...)),
				"problems":           nonNil(append([]string(nil), lifecycleError.Problems...)),
			}
			writeAPIError(writer, http.StatusConflict, APIError{Code: lifecycleError.Code, Message: lifecycleError.Message, Details: details, Remediation: []Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
			return
		}
		writeAPIError(writer, http.StatusInternalServerError, APIError{Code: "DAEMON_RESTART_FAILED", Message: err.Error(), Remediation: []Remediation{{Label: "Restart from the CLI", Command: "portless daemon restart"}}})
		return
	}
	writeJSON(writer, http.StatusAccepted, daemonRestartResponse{
		Restarting: true, PreviousInstanceID: result.InstanceID, Handoff: result.Handoff,
		ActiveEnvironments: nonNil(append([]string(nil), result.ActiveEnvironments...)),
	})
}

func (s *Server) handleSystem(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"name": "portless", "version": "dev", "apiVersion": APIVersion, "telemetry": false})
}

func (s *Server) handleRuntime(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
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
	if len(segments) == 2 && segments[1] == "reset" && request.Method == http.MethodPost {
		if principal.Session {
			writeAPIError(writer, http.StatusForbidden, APIError{Code: "CLI_AUTH_REQUIRED", Message: "runtime reset preparation may only be requested by the local CLI"})
			return
		}
		result, err := s.app.PrepareReset(request.Context())
		if err != nil {
			var active application.ResetActiveEnvironmentsError
			if errors.As(err, &active) {
				writeAPIError(writer, http.StatusConflict, APIError{
					Code: "ACTIVE_ENVIRONMENTS", Message: err.Error(),
					Details: map[string]any{"activeEnvironments": nonNil(active.Environments)},
				})
				return
			}
			s.writeError(writer, err, nil)
			return
		}
		if result.Runtimes == nil {
			result.Runtimes = []container.ResetResult{}
		}
		writeJSON(writer, http.StatusOK, result)
		return
	}
	if len(segments) == 3 && segments[1] == "reset" && segments[2] == "cancel" && request.Method == http.MethodPost {
		if principal.Session {
			writeAPIError(writer, http.StatusForbidden, APIError{Code: "CLI_AUTH_REQUIRED", Message: "runtime reset cancellation may only be requested by the local CLI"})
			return
		}
		s.app.CancelReset()
		writer.WriteHeader(http.StatusNoContent)
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
		if err := s.auth.Logout(request); err != nil {
			writeAPIError(writer, http.StatusInternalServerError, APIError{Code: "SESSION_LOGOUT_FAILED", Message: err.Error()})
			return
		}
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
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			projects, err := s.app.Projects(ctx)
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"projects": limited(nonNil(projects), limit), "total": len(projects)})
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
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			environments, err := s.app.Environments(ctx, request.URL.Query().Get("project"))
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"environments": limited(nonNil(environments), limit), "total": len(environments)})
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
	if len(segments) == 2 && segments[1] == "context" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		path := request.URL.Query().Get("path")
		if path == "" {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "PATH_REQUIRED", Message: "the path query parameter is required"})
			return
		}
		resolved, err := s.app.EnvironmentContext(ctx, path)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, resolved)
		return
	}
	if len(segments) == 2 && segments[1] == "select" {
		switch request.Method {
		case http.MethodPut:
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
		case http.MethodDelete:
			path := request.URL.Query().Get("path")
			if path == "" {
				writeAPIError(writer, http.StatusBadRequest, APIError{Code: "PATH_REQUIRED", Message: "the path query parameter is required"})
				return
			}
			cleared, err := s.app.ClearEnvironmentSelection(ctx, path)
			if err != nil {
				s.writeError(writer, err, nil)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"cleared": cleared})
		default:
			methodNotAllowed(writer, http.MethodPut, http.MethodDelete)
		}
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
		limit, limitErr := queryLimit(request, 250, 1000)
		if limitErr != nil {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"services": limited(nonNil(current.Services), limit)})
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
		switch segments[5] {
		case "start":
			operation, actionErr = s.app.StartService(request.Context(), project, environment, serviceName, principal.Actor)
		case "restart":
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
	connections, err := s.app.Connections(request.Context(), project, environment)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 4 {
		limit, limitErr := queryLimit(request, 250, 1000)
		if limitErr != nil {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"connections": limited(nonNil(connections), limit)})
		return
	}
	if len(segments) == 6 {
		for _, connection := range connections {
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
	limit, err := queryLimit(request, 500, 10_000)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: err.Error()})
		return
	}
	since, err := querySince(request)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_SINCE", Message: err.Error()})
		return
	}
	service := request.URL.Query().Get("service")
	entries, err := s.app.Logs(request.Context(), project, environment, service, limit, since)
	if err != nil {
		s.writeError(writer, err, map[string]any{"project": project, "environment": environment, "service": service})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"project": project, "environment": environment, "service": service, "entries": nonNil(entries)})
}

func (s *Server) handleTraffic(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if len(segments) != 4 && len(segments) != 5 {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "traffic route not found"})
		return
	}
	if _, err := s.app.Environment(request.Context(), project, environment); err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if len(segments) == 5 {
		sequence, err := strconv.ParseInt(segments[4], 10, 64)
		if err != nil || sequence <= 0 {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_TRAFFIC_SEQUENCE", Message: "traffic sequence must be a positive integer"})
			return
		}
		event, err := s.app.TrafficEvent(request.Context(), project, environment, sequence)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeAPIError(writer, http.StatusNotFound, APIError{Code: "TRAFFIC_NOT_FOUND", Message: "traffic event is no longer in the live buffer or a retained recording", Remediation: []Remediation{{Label: "Capture durable traffic", Command: "portless record start debug"}}})
				return
			}
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, event)
		return
	}
	protocol := request.URL.Query().Get("protocol")
	if protocol == "" {
		protocol = string(model.ProtocolHTTP)
	}
	if protocol != string(model.ProtocolHTTP) && protocol != string(model.ProtocolTCP) {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_TRAFFIC_PROTOCOL", Message: "protocol must be http or tcp"})
		return
	}
	limit, err := queryLimit(request, 250, 1000)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: err.Error()})
		return
	}
	all := s.app.Traffic(project, environment, 1000)
	filtered := make([]model.TrafficEvent, 0, len(all))
	service := request.URL.Query().Get("service")
	source := request.URL.Query().Get("source")
	target := request.URL.Query().Get("target")
	if edge := request.URL.Query().Get("edge"); edge != "" {
		var found bool
		source, target, found = strings.Cut(edge, ":")
		if !found || source == "" || target == "" || strings.Contains(target, ":") {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_EDGE", Message: "edge must use source:target"})
			return
		}
	}
	after, err := queryNonNegativeInt64(request, "after")
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_AFTER", Message: err.Error()})
		return
	}
	for _, event := range all {
		isHTTP := event.Protocol == model.ProtocolHTTP
		if (protocol == string(model.ProtocolHTTP) && !isHTTP) || (protocol == string(model.ProtocolTCP) && isHTTP) {
			continue
		}
		if service != "" && event.Source != service && event.Target != service {
			continue
		}
		if (source != "" && event.Source != source) || (target != "" && event.Target != target) || event.Sequence <= after {
			continue
		}
		filtered = append(filtered, trafficSummary(event))
		if len(filtered) == limit {
			break
		}
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
			data := event.Data
			if event.Type == "traffic.http" || event.Type == "traffic.tcp" {
				if traffic, ok := event.Data.(model.TrafficEvent); ok {
					data = trafficSummary(traffic)
				}
			}
			payload, _ := json.Marshal(data)
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
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			recordings, err := s.app.Recordings(ctx, project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"recordings": limited(nonNil(recordings), limit)})
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
			recording, err := s.app.Recording(ctx, project, environment, name)
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
		recording, _ := s.app.Recording(ctx, project, environment, name)
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
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			faults, err := s.app.Faults(ctx, project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"faults": limited(nonNil(faults), limit)})
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
			fault, err := s.app.Fault(ctx, project, environment, name)
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
		limit, limitErr := queryLimit(request, 100, 500)
		if limitErr != nil {
			writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
			return
		}
		operations, err := s.app.Operations(request.Context(), project, environment, limit)
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
	if len(segments) != 5 && !(len(segments) == 6 && segments[5] == "events") {
		writeAPIError(writer, http.StatusNotFound, APIError{Code: "ROUTE_NOT_FOUND", Message: "operation route not found"})
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
	limit, limitErr := queryLimit(request, 250, 1000)
	if limitErr != nil {
		writeAPIError(writer, http.StatusBadRequest, APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
		return
	}
	events, err := s.app.Timeline(request.Context(), project, environment, limit)
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

func queryLimit(request *http.Request, fallback, maximum int) (int, error) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > maximum {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	return parsed, nil
}

func queryNonNegativeInt64(request *http.Request, key string) (int64, error) {
	value := request.URL.Query().Get(key)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return parsed, nil
}

func querySince(request *http.Request) (time.Time, error) {
	value := request.URL.Query().Get("since")
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil && duration >= 0 {
		return time.Now().UTC().Add(-duration), nil
	}
	if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return timestamp, nil
	}
	return time.Time{}, errors.New("since must be a duration such as 10m or an RFC3339 timestamp")
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func limited[T any](items []T, limit int) []T {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func trafficSummary(event model.TrafficEvent) model.TrafficEvent {
	event.RequestHeaders = nil
	event.ResponseHeaders = nil
	return event
}

func environmentSubject(project, environment string) map[string]any {
	return map[string]any{"project": project, "environment": environment}
}
