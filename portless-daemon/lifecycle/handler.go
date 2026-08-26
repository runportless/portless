package lifecycle

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/auth"
)

// HandlerConfig supplies daemon identity, authentication, active-state probes,
// and shutdown hooks to a lifecycle handler.
type HandlerConfig struct {
	Next               http.Handler
	Auth               *auth.Manager
	Identity           Identity
	ActiveEnvironments func(context.Context) ([]string, error)
	HandoffStatus      func(context.Context) (bool, []string)
	Shutdown           func()
}

// Handler serves authenticated identity and shutdown routes before delegating
// ordinary requests to the next HTTP handler.
type Handler struct {
	next               http.Handler
	auth               *auth.Manager
	identity           Identity
	activeEnvironments func(context.Context) ([]string, error)
	handoffStatus      func(context.Context) (bool, []string)
	shutdown           func()
}

// NewHandler constructs a lifecycle handler from config.
func NewHandler(config HandlerConfig) *Handler {
	return &Handler{
		next: config.Next, auth: config.Auth, identity: config.Identity,
		activeEnvironments: config.ActiveEnvironments, handoffStatus: config.HandoffStatus,
		shutdown: config.Shutdown,
	}
}

// SetNext replaces the handler used for non-lifecycle requests.
func (h *Handler) SetNext(next http.Handler) { h.next = next }

// ServeHTTP authenticates lifecycle requests and delegates every other route.
func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != IdentityPath && request.URL.Path != HandoffPath && request.URL.Path != ShutdownPath {
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
	case HandoffPath:
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeDaemonError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
			return
		}
		status, err := h.VerifyHandoff(request.Context())
		if err != nil {
			writeDaemonError(writer, http.StatusInternalServerError, "DAEMON_STATE_UNAVAILABLE", err.Error(), nil)
			return
		}
		writeDaemonJSON(writer, http.StatusOK, status)
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
		if !input.Force && input.Handoff {
			verification, verifyErr := h.VerifyHandoff(request.Context())
			if verifyErr != nil {
				writeDaemonError(writer, http.StatusInternalServerError, "DAEMON_STATE_UNAVAILABLE", verifyErr.Error(), nil)
				return
			}
			active = verification.ActiveEnvironments
			if len(active) > 0 && verification.State != HandoffReady {
				writeDaemonError(writer, http.StatusConflict, "HANDOFF_UNAVAILABLE", "active environments cannot be safely handed off", active, verification.Problems)
				return
			}
		}
		if len(active) > 0 && !input.Force && !input.Handoff {
			writeDaemonError(writer, http.StatusConflict, "ACTIVE_ENVIRONMENTS", "daemon is managing active environments", active, nil)
			return
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
	identity.RecoveryProblems = append([]string(nil), h.identity.RecoveryProblems...)
	identity.RecoveryProblems = append(identity.RecoveryProblems, "active environment inventory is unavailable: "+err.Error())
	return identity
}

// Status returns live identity and active environments without probing runtime
// handoff safety.
func (h *Handler) Status(ctx context.Context) (Identity, error) {
	active, err := h.currentActiveEnvironments(ctx)
	if err != nil {
		return Identity{}, err
	}
	identity := h.identity
	identity.ActiveEnvironments = active
	identity.RecoveryProblems = append([]string(nil), h.identity.RecoveryProblems...)
	if identity.RecoveryProblems == nil {
		identity.RecoveryProblems = []string{}
	}
	return identity, nil
}

// VerifyHandoff performs a fresh fail-closed audit of active runtime ownership
// and adoption safety.
func (h *Handler) VerifyHandoff(ctx context.Context) (HandoffStatus, error) {
	active, err := h.currentActiveEnvironments(ctx)
	if err != nil {
		return HandoffStatus{}, err
	}
	return h.verifyHandoff(ctx, active), nil
}

func (h *Handler) verifyHandoff(ctx context.Context, active []string) HandoffStatus {
	ready := false
	problems := []string{"runtime handoff is not configured"}
	if h.handoffStatus != nil {
		ready, problems = h.handoffStatus(ctx)
	}
	if problems == nil {
		problems = []string{}
	}
	state := HandoffBlocked
	if ready {
		state = HandoffReady
	}
	return HandoffStatus{
		State: state, VerifiedAt: time.Now().UTC(), Problems: append([]string(nil), problems...),
		ActiveEnvironments: append([]string(nil), active...),
	}
}

// Restart validates replacement of instanceID after proving that active
// runtime state can be handed off safely.
func (h *Handler) Restart(ctx context.Context, instanceID string) (ShutdownResponse, error) {
	active, err := h.currentActiveEnvironments(ctx)
	if err != nil {
		return ShutdownResponse{}, err
	}
	if instanceID == "" || instanceID != h.identity.InstanceID {
		return ShutdownResponse{}, &LifecycleError{
			Code: "DAEMON_INSTANCE_CHANGED", Message: "daemon instance changed; refresh its status before restarting",
			ActiveEnvironments: active,
		}
	}
	verification := h.verifyHandoff(ctx, active)
	if len(verification.ActiveEnvironments) > 0 && verification.State != HandoffReady {
		return ShutdownResponse{}, &LifecycleError{
			Code: "HANDOFF_UNAVAILABLE", Message: "active environments cannot be safely handed off",
			ActiveEnvironments: verification.ActiveEnvironments, Problems: verification.Problems,
		}
	}
	return ShutdownResponse{
		Stopping: true, Handoff: true, InstanceID: h.identity.InstanceID,
		ActiveEnvironments: append([]string(nil), verification.ActiveEnvironments...),
	}, nil
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
