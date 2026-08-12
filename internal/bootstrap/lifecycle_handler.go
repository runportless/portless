package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"

	"github.com/portless-run/portless/internal/auth"
	"github.com/portless-run/portless/internal/daemon"
)

type lifecycleHandler struct {
	next               http.Handler
	auth               *auth.Manager
	identity           daemon.Identity
	activeEnvironments func(context.Context) ([]string, error)
	handoffStatus      func(context.Context) (bool, []string)
	shutdown           func()
	replace            func()
}

func (h *lifecycleHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != daemon.IdentityPath && request.URL.Path != daemon.ShutdownPath {
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
	case daemon.IdentityPath:
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeDaemonError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
			return
		}
		identity, err := h.Status(request.Context())
		if err != nil {
			writeDaemonError(writer, http.StatusInternalServerError, "DAEMON_STATE_UNAVAILABLE", err.Error(), nil)
			return
		}
		writeDaemonJSON(writer, http.StatusOK, identity)
	case daemon.ShutdownPath:
		if request.Method != http.MethodPost {
			writer.Header().Set("Allow", http.MethodPost)
			writeDaemonError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed", nil)
			return
		}
		var input daemon.ShutdownRequest
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
			writeDaemonError(writer, http.StatusInternalServerError, "DAEMON_STATE_UNAVAILABLE", err.Error(), nil)
			return
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
		writeDaemonJSON(writer, http.StatusAccepted, daemon.ShutdownResponse{Stopping: true, Handoff: input.Handoff, InstanceID: h.identity.InstanceID, ActiveEnvironments: active})
		h.shutdown()
	}
}

func (h *lifecycleHandler) Status(ctx context.Context) (daemon.Identity, error) {
	active, err := h.currentActiveEnvironments(ctx)
	if err != nil {
		return daemon.Identity{}, err
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

func (h *lifecycleHandler) Restart(ctx context.Context, instanceID string) (daemon.ShutdownResponse, error) {
	identity, err := h.Status(ctx)
	if err != nil {
		return daemon.ShutdownResponse{}, err
	}
	if instanceID == "" || instanceID != identity.InstanceID {
		return daemon.ShutdownResponse{}, &daemon.LifecycleError{
			Code: "DAEMON_INSTANCE_CHANGED", Message: "daemon instance changed; refresh its status before restarting",
			ActiveEnvironments: identity.ActiveEnvironments,
		}
	}
	if len(identity.ActiveEnvironments) > 0 && !identity.HandoffReady {
		return daemon.ShutdownResponse{}, &daemon.LifecycleError{
			Code: "HANDOFF_UNAVAILABLE", Message: "active environments cannot be safely handed off",
			ActiveEnvironments: identity.ActiveEnvironments, Problems: identity.RecoveryProblems,
		}
	}
	if h.replace == nil {
		return daemon.ShutdownResponse{}, &daemon.LifecycleError{
			Code: "DAEMON_RESTART_UNAVAILABLE", Message: "daemon replacement is not configured",
			ActiveEnvironments: identity.ActiveEnvironments,
		}
	}
	result := daemon.ShutdownResponse{
		Stopping: true, Handoff: true, InstanceID: identity.InstanceID,
		ActiveEnvironments: append([]string(nil), identity.ActiveEnvironments...),
	}
	h.replace()
	return result, nil
}

func (h *lifecycleHandler) currentActiveEnvironments(ctx context.Context) ([]string, error) {
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
	writeDaemonJSON(writer, status, daemon.ErrorResponse{Error: daemon.Error{Code: code, Message: message, ActiveEnvironments: active, Problems: details}})
}

func writeDaemonJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
