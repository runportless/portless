// Package server implements the daemon's authenticated HTTP control API and
// application-host routing boundary.
package server

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/portless-run/portless/internal/api/contract"
	"github.com/portless-run/portless/internal/application"
	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/model"
)

type Server struct {
	app           *application.Service
	auth          *auth.Manager
	daemonControl DaemonControl
	assets        fs.FS
	files         http.Handler
	indexHTML     []byte
	inspectRelay  func(context.Context) (contract.RelayStatus, error)
}

type DaemonControl interface {
	Status(context.Context) (contract.DaemonStatus, error)
	Restart(context.Context, string) (contract.DaemonRestart, error)
}

type Dependencies struct {
	Application   *application.Service
	Auth          *auth.Manager
	Assets        fs.FS
	DaemonControl DaemonControl
	InspectRelay  func(context.Context) (contract.RelayStatus, error)
}

func New(dependencies Dependencies) (*Server, error) {
	index, err := fs.ReadFile(dependencies.Assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded UI: %w", err)
	}
	return &Server{
		app: dependencies.Application, auth: dependencies.Auth, daemonControl: dependencies.DaemonControl,
		assets: dependencies.Assets, files: http.FileServer(http.FS(dependencies.Assets)), indexHTML: index,
		inspectRelay: dependencies.InspectRelay,
	}, nil
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(writer.Header())
	host := normalizedHost(request.Host)
	if service, environment, project, ok := applicationHost(host); ok {
		if strings.HasPrefix(request.URL.Path, "/api/") || strings.HasPrefix(request.URL.Path, "/auth/") {
			writeAPIError(writer, http.StatusMisdirectedRequest, contract.APIError{Code: "CONTROL_HOST_REQUIRED", Message: "control routes are not served on application hosts"})
			return
		}
		s.app.ServeIngress(writer, request, model.EnvironmentSelector(project, environment), service)
		return
	}
	if !isControlHost(host) {
		writeAPIError(writer, http.StatusMisdirectedRequest, contract.APIError{Code: "UNKNOWN_HOST", Message: "request host is not a Portless control or application host"})
		return
	}
	if request.URL.Path == "/api/v1/health" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		writeJSON(writer, http.StatusOK, contract.Health{Ready: true, APIVersion: contract.APIVersion})
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
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_BROWSER_CLAIM", Message: "browser claim is malformed"})
		return
	}
	token, _, next, expiresAt, err := s.auth.ConsumeClaim(code)
	if err != nil {
		writeAPIError(writer, http.StatusUnauthorized, contract.APIError{Code: "INVALID_BROWSER_CLAIM", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Open the UI again", Command: "portless ui"}}})
		return
	}
	http.SetCookie(writer, &http.Cookie{Name: auth.SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expiresAt})
	http.Redirect(writer, request, next, http.StatusSeeOther)
}

func (s *Server) handleAPI(writer http.ResponseWriter, request *http.Request) {
	principal, ok := s.auth.Authenticate(request)
	if !ok {
		writeAPIError(writer, http.StatusUnauthorized, contract.APIError{Code: "AUTHENTICATION_REQUIRED", Message: "authenticate with the Portless CLI or open a browser session with `portless ui`"})
		return
	}
	if isMutation(request.Method) {
		if err := s.auth.ValidateMutation(request, principal); err != nil {
			writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "REQUEST_FORBIDDEN", Message: err.Error()})
			return
		}
	}
	segments := splitPath(strings.TrimPrefix(strings.Trim(request.URL.Path, "/"), "api/v1/"))
	if len(segments) == 0 {
		writeJSON(writer, http.StatusOK, contract.SystemStatus{Name: "portless", APIVersion: contract.APIVersion})
		return
	}
	switch segments[0] {
	case "system":
		s.handleSystem(writer, request)
	case "daemon":
		s.handleDaemon(writer, request, segments)
	case "relay":
		s.handleRelay(writer, request, segments)
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
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "API route not found"})
	}
}
