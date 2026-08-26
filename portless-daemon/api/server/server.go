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

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"github.com/runportless/portless/portless-daemon/model"
)

// Server routes authenticated control API, embedded UI, and application-host
// requests for one daemon.
type Server struct {
	app             *controlplane.Service
	auth            *auth.Manager
	daemonControl   DaemonControl
	assets          fs.FS
	files           http.Handler
	indexHTML       []byte
	inspectRelay    func(context.Context) (contract.RelayStatus, error)
	selectDirectory func(context.Context, string, string) (string, bool, error)
	systemVersion   string
}

// DaemonControl exposes process lifecycle operations to the authenticated API.
type DaemonControl interface {
	// Status returns the current shallow daemon identity and recovery state.
	Status(context.Context) (contract.DaemonStatus, error)
	// Diagnostics returns one bounded operational snapshot, optionally including storage inspection.
	Diagnostics(context.Context, bool) (contract.DaemonDiagnostics, error)
	// Logs returns one bounded, safely redacted daemon-log tail.
	Logs(context.Context) (contract.DaemonLogSnapshot, error)
	// HandoffStatus performs a fresh runtime-adoption safety audit.
	HandoffStatus(context.Context) (contract.DaemonHandoffStatus, error)
	// Restart requests replacement of the identified daemon instance.
	Restart(context.Context, string, string) (contract.DaemonRestart, error)
	// CommitRestart begins an accepted replacement after its response has been written.
	CommitRestart(string)
}

// Dependencies contains the control-plane services required by Server.
type Dependencies struct {
	Application     *controlplane.Service
	Auth            *auth.Manager
	Assets          fs.FS
	DaemonControl   DaemonControl
	InspectRelay    func(context.Context) (contract.RelayStatus, error)
	SelectDirectory func(context.Context, string, string) (string, bool, error)
	SystemVersion   string
}

// New validates dependencies and constructs an HTTP server with embedded UI assets.
func New(dependencies Dependencies) (*Server, error) {
	index, err := fs.ReadFile(dependencies.Assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read embedded UI: %w", err)
	}
	systemVersion := dependencies.SystemVersion
	if systemVersion == "" {
		systemVersion = "dev"
	}
	return &Server{
		app: dependencies.Application, auth: dependencies.Auth, daemonControl: dependencies.DaemonControl,
		assets: dependencies.Assets, files: http.FileServer(http.FS(dependencies.Assets)), indexHTML: index,
		inspectRelay: dependencies.InspectRelay, selectDirectory: dependencies.SelectDirectory, systemVersion: systemVersion,
	}, nil
}

// ServeHTTP dispatches control hosts to the UI and API and application hosts to
// the environment ingress proxy.
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
	clientKind := request.Header.Get(contract.ClientKindHeader)
	if principal.Session {
		if clientKind != "" {
			writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "REQUEST_FORBIDDEN", Message: "browser sessions cannot select an API client kind"})
			return
		}
	} else {
		switch contract.ClientKind(clientKind) {
		case "", contract.ClientKindCLI:
			principal.Actor = "CLI"
		case contract.ClientKindMCP:
			principal.Actor = "MCP"
		default:
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_CLIENT_KIND", Message: "Portless-Client-Kind must be cli or mcp"})
			return
		}
	}
	if isMutation(request.Method) {
		if err := s.auth.ValidateMutation(request, principal); err != nil {
			writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "REQUEST_FORBIDDEN", Message: err.Error()})
			return
		}
	}
	segments := splitPath(strings.TrimPrefix(strings.Trim(request.URL.Path, "/"), "api/v1/"))
	if len(segments) == 0 {
		writeJSON(writer, http.StatusOK, contract.SystemStatus{Name: "portless", Version: s.systemVersion, APIVersion: contract.APIVersion})
		return
	}
	switch segments[0] {
	case "system":
		s.handleSystem(writer, request, segments, principal)
	case "daemon":
		s.handleDaemon(writer, request, segments, principal)
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
