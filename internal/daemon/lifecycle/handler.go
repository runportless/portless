package lifecycle

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/portless-run/portless/internal/auth"
)

type HandlerConfig struct {
	Next               http.Handler
	Auth               *auth.Manager
	Identity           Identity
	ActiveEnvironments func(context.Context) ([]string, error)
	HandoffStatus      func(context.Context) (bool, []string)
	Shutdown           func()
	Replace            func()
}

type Handler struct {
	next               http.Handler
	auth               *auth.Manager
	identity           Identity
	activeEnvironments func(context.Context) ([]string, error)
	handoffStatus      func(context.Context) (bool, []string)
	shutdown           func()
	replace            func()
}

func NewHandler(config HandlerConfig) *Handler {
	return &Handler{
		next: config.Next, auth: config.Auth, identity: config.Identity,
		activeEnvironments: config.ActiveEnvironments, handoffStatus: config.HandoffStatus,
		shutdown: config.Shutdown, replace: config.Replace,
	}
}

func (h *Handler) SetNext(next http.Handler) { h.next = next }

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != IdentityPath && request.URL.Path != ShutdownPath {
		if h.next == nil {
			http.NotFound(writer, request)
			return
		}
		h.next.ServeHTTP(writer, request)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	if !isDaemonControlHost(request.Host) {
		writeDaemonError(writer, http.StatusMisdirectedRequest, "CONTROL_HOST_REQUIRED", "daemon lifecycle routes are only served on the Portless control host", nil)
		return
	}
	principal, ok := h.auth.Authenticate(request)
	if !ok {
		writeDaemonError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "daemon lifecycle authentication is required", nil)
		return
	}
	if principal.Session {
		writeDaemonError(writer, http.StatusForbidden, "CLI_AUTH_REQUIRED", "only the local Portless CLI may control the daemon lifecycle", nil)
		return
	}

	switch request.URL.Path {
	case IdentityPath:
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeDaemonError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
			return
		}
		writeDaemonJSON(writer, http.StatusOK, h.Identity(request.Context()))
	case ShutdownPath:
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			writeDaemonError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
			return
		}
		var input ShutdownRequest
		decoder := json.NewDecoder(io.LimitReader(request.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeDaemonError(writer, http.StatusBadRequest, "INVALID_SHUTDOWN_REQUEST", "shutdown request is malformed", nil)
			return
		}
		if input.InstanceID == "" || input.InstanceID != h.identity.InstanceID {
			writeDaemonError(writer, http.StatusConflict, "DAEMON_INSTANCE_CHANGED", "daemon instance does not match the authenticated request", nil)
			return
		}
		active, err := h.currentActiveEnvironments(request.Context())
		if err != nil {
			if !input.Force {
				writeDaemonError(writer, http.StatusInternalServerError, "DAEMON_STATE_UNAVAILABLE", err.Error(), nil)
				return
			}
			active = []string{}
		}
		if len(active) > 0 && !input.Force {
			if !input.Handoff {
				writeDaemonError(writer, http.StatusConflict, "ACTIVE_ENVIRONMENTS", "daemon is managing active environments", active, nil)
				return
			}
			ready, problems := false, []string{"runtime handoff is not configured"}
			if h.handoffStatus != nil {
				ready, problems = h.handoffStatus(request.Context())
			}
			if !ready {
				writeDaemonError(writer, http.StatusConflict, "HANDOFF_UNAVAILABLE", "active environments cannot be safely handed off", active, problems)
				return
			}
		}
		writeDaemonJSON(writer, http.StatusAccepted, ShutdownResponse{Stopping: true, Handoff: input.Handoff, InstanceID: h.identity.InstanceID, ActiveEnvironments: active})
		h.shutdown()
	}
}

// Identity always returns the authenticated process identity, even when the
// application inventory is unreadable. Lifecycle authentication must remain
// available for guarded recovery; ordinary shutdown and browser restart still
// use Status and fail closed when active-environment safety cannot be proven.
func (h *Handler) Identity(ctx context.Context) Identity {
	identity, err := h.Status(ctx)
	if err == nil {
		return identity
	}
	identity = h.identity
	identity.ActiveEnvironments = []string{}
	identity.HandoffReady = false
	identity.RecoveryProblems = append([]string(nil), h.identity.RecoveryProblems...)
	identity.RecoveryProblems = append(identity.RecoveryProblems, "active environment inventory is unavailable: "+err.Error())
	return identity
}

func (h *Handler) Status(ctx context.Context) (Identity, error) {
	active, err := h.currentActiveEnvironments(ctx)
	if err != nil {
		return Identity{}, err
	}
	identity := h.identity
	identity.ActiveEnvironments = active
	identity.RecoveryProblems = append([]string(nil), h.identity.RecoveryProblems...)
	if h.handoffStatus != nil {
		ready, problems := h.handoffStatus(ctx)
		identity.HandoffReady = ready
		identity.RecoveryProblems = append(identity.RecoveryProblems, problems...)
	}
	if identity.RecoveryProblems == nil {
		identity.RecoveryProblems = []string{}
	}
	return identity, nil
}

func (h *Handler) Restart(ctx context.Context, instanceID string) (ShutdownResponse, error) {
	identity, err := h.Status(ctx)
	if err != nil {
		return ShutdownResponse{}, err
	}
	if instanceID == "" || instanceID != identity.InstanceID {
		return ShutdownResponse{}, &LifecycleError{
			Code: "DAEMON_INSTANCE_CHANGED", Message: "daemon instance changed; refresh its status before restarting",
			ActiveEnvironments: identity.ActiveEnvironments,
		}
	}
	if len(identity.ActiveEnvironments) > 0 && !identity.HandoffReady {
		return ShutdownResponse{}, &LifecycleError{
			Code: "HANDOFF_UNAVAILABLE", Message: "active environments cannot be safely handed off",
			ActiveEnvironments: identity.ActiveEnvironments, Problems: identity.RecoveryProblems,
		}
	}
	if h.replace == nil {
		return ShutdownResponse{}, &LifecycleError{
			Code: "DAEMON_RESTART_UNAVAILABLE", Message: "daemon replacement is not configured",
			ActiveEnvironments: identity.ActiveEnvironments,
		}
	}
	result := ShutdownResponse{
		Stopping: true, Handoff: true, InstanceID: identity.InstanceID,
		ActiveEnvironments: append([]string(nil), identity.ActiveEnvironments...),
	}
	h.replace()
	return result, nil
}

func (h *Handler) currentActiveEnvironments(ctx context.Context) ([]string, error) {
	if h.activeEnvironments == nil {
		return []string{}, nil
	}
	active, err := h.activeEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	active = append([]string(nil), active...)
	sort.Strings(active)
	if active == nil {
		active = []string{}
	}
	return active, nil
}

func isDaemonControlHost(value string) bool {
	host := strings.ToLower(strings.TrimSpace(value))
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "portless.localhost"
}

func writeDaemonError(writer http.ResponseWriter, status int, code, message string, active []string, problems ...[]string) {
	details := []string(nil)
	if len(problems) > 0 {
		details = problems[0]
	}
	writeDaemonJSON(writer, status, ErrorResponse{Error: Error{Code: code, Message: message, ActiveEnvironments: active, Problems: details}})
}

func writeDaemonJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
