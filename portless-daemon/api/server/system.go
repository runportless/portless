package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/auth"
	"github.com/runportless/portless/portless-daemon/controlplane"
)

func (s *Server) handleRelay(writer http.ResponseWriter, request *http.Request, segments []string) {
	if len(segments) != 1 {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "relay route not found"})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if s.inspectRelay == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "RELAY_STATE_UNAVAILABLE", Message: "relay inspection is unavailable", Remediation: []contract.Remediation{{Label: "Diagnose the relay", Command: "portless doctor relay"}}})
		return
	}
	status, err := s.inspectRelay(request.Context())
	if err != nil {
		writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "RELAY_STATE_UNAVAILABLE", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Diagnose the relay", Command: "portless doctor relay"}}})
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (s *Server) handleDaemon(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	if s.daemonControl == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DAEMON_CONTROL_UNAVAILABLE", Message: "daemon lifecycle information is unavailable", Remediation: []contract.Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
		return
	}
	if len(segments) == 1 {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		identity, err := s.daemonControl.Status(request.Context())
		if err != nil {
			writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DAEMON_STATE_UNAVAILABLE", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
			return
		}
		identity.RecoveryProblems = nonNil(append([]string(nil), identity.RecoveryProblems...))
		identity.ActiveEnvironments = nonNil(append([]string(nil), identity.ActiveEnvironments...))
		writeJSON(writer, http.StatusOK, identity)
		return
	}
	if len(segments) == 2 && segments[1] == "logs" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		snapshot, err := s.daemonControl.Logs(request.Context())
		if err != nil {
			writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DAEMON_LOG_UNAVAILABLE", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
			return
		}
		writeJSON(writer, http.StatusOK, snapshot)
		return
	}
	if len(segments) == 2 && segments[1] == "diagnostics" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		includeStorage := request.URL.Query().Get("include") == "storage"
		status, err := s.daemonControl.Diagnostics(request.Context(), includeStorage)
		if err != nil {
			writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DAEMON_DIAGNOSTICS_UNAVAILABLE", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
			return
		}
		status.Inventory.Problems = nonNil(append([]string(nil), status.Inventory.Problems...))
		status.Recovery.Problems = nonNil(append([]string(nil), status.Recovery.Problems...))
		if status.Storage != nil {
			status.Storage.Problems = nonNil(append([]string(nil), status.Storage.Problems...))
		}
		writeJSON(writer, http.StatusOK, status)
		return
	}
	if len(segments) == 2 && segments[1] == "handoff" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		status, err := s.daemonControl.HandoffStatus(request.Context())
		if err != nil {
			writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DAEMON_HANDOFF_STATE_UNAVAILABLE", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}})
			return
		}
		status.Problems = nonNil(append([]string(nil), status.Problems...))
		status.ActiveEnvironments = nonNil(append([]string(nil), status.ActiveEnvironments...))
		writeJSON(writer, http.StatusOK, status)
		return
	}
	if len(segments) != 2 || segments[1] != "restart" {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "daemon route not found"})
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	var input contract.DaemonRestartRequest
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	if strings.TrimSpace(input.InstanceID) == "" {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "DAEMON_INSTANCE_REQUIRED", Message: "instanceId is required"})
		return
	}
	if input.Force && !principal.Session {
		writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "BROWSER_SESSION_REQUIRED", Message: "forced daemon restart is available only from the authenticated Portless control plane"})
		return
	}
	reason := "cli"
	if principal.Session {
		reason = "browser"
	} else if principal.Actor == "MCP" {
		reason = "mcp"
	}
	result, err := s.daemonControl.Restart(request.Context(), input.InstanceID, reason, input.Force)
	if err != nil {
		var lifecycleError *contract.DaemonControlError
		if errors.As(err, &lifecycleError) {
			writeAPIError(writer, http.StatusConflict, daemonRestartAPIError(lifecycleError))
			return
		}
		writeAPIError(writer, http.StatusInternalServerError, contract.APIError{Code: "DAEMON_RESTART_FAILED", Message: err.Error(), Remediation: []contract.Remediation{{Label: "Restart from the CLI", Command: "portless daemon restart"}}})
		return
	}
	result.ActiveEnvironments = nonNil(append([]string(nil), result.ActiveEnvironments...))
	writeJSON(writer, http.StatusAccepted, result)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	s.daemonControl.CommitRestart(result.RestartID)
}

func daemonRestartAPIError(input *contract.DaemonControlError) contract.APIError {
	active := append([]string(nil), input.ActiveEnvironments...)
	problems := append([]string(nil), input.Problems...)
	details := map[string]any{
		"activeEnvironments": nonNil(active),
		"problems":           nonNil(problems),
	}
	message := input.Message
	remediation := []contract.Remediation{{Label: "Diagnose Portless", Command: "portless doctor"}}
	if input.Code == "HANDOFF_UNAVAILABLE" && len(active) > 0 {
		blocking := blockingHandoffEnvironments(active, problems)
		details["blockingEnvironments"] = nonNil(append([]string(nil), blocking...))
		message = blockedDaemonRestartMessage(blocking)
		remediation = blockedDaemonRestartRemediation(blocking)
	}
	return contract.APIError{Code: input.Code, Message: message, Details: details, Remediation: remediation}
}

func blockingHandoffEnvironments(active, problems []string) []string {
	matched := make(map[string]bool, len(active))
	unscopedProblem := false
	for _, problem := range problems {
		problemMatched := false
		for _, environment := range active {
			if strings.HasPrefix(problem, environment+"/") || strings.HasPrefix(problem, environment+":") {
				matched[environment] = true
				problemMatched = true
			}
		}
		if !problemMatched {
			unscopedProblem = true
		}
	}
	if unscopedProblem || len(matched) == 0 {
		return append([]string(nil), active...)
	}
	blocking := make([]string, 0, len(matched))
	for _, environment := range active {
		if matched[environment] {
			blocking = append(blocking, environment)
		}
	}
	return blocking
}

func blockedDaemonRestartMessage(environments []string) string {
	if len(environments) == 1 {
		return fmt.Sprintf("Safe daemon restart is blocked by environment %s. Stop it, then retry.", environments[0])
	}
	return fmt.Sprintf("Safe daemon restart is blocked by these environments: %s. Stop them, then retry.", strings.Join(environments, ", "))
}

func blockedDaemonRestartRemediation(environments []string) []contract.Remediation {
	remediation := make([]contract.Remediation, 0, len(environments)+2)
	for _, environment := range environments {
		remediation = append(remediation, contract.Remediation{Label: "Stop " + environment, Command: "portless down --env " + environment})
	}
	return append(remediation,
		contract.Remediation{Label: "Retry daemon restart", Command: "portless daemon restart"},
		contract.Remediation{Label: "Diagnose Portless", Command: "portless doctor"},
	)
}

func (s *Server) handleSystem(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	if len(segments) == 1 {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		writeJSON(writer, http.StatusOK, contract.SystemStatus{Name: "portless", Version: s.systemVersion, APIVersion: contract.APIVersion, Telemetry: false})
		return
	}
	if len(segments) != 3 || segments[1] != "directories" || segments[2] != "select" {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "system route not found"})
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if !principal.Session {
		writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "BROWSER_SESSION_REQUIRED", Message: "native directory selection is available only to the authenticated Portless control plane"})
		return
	}
	if s.selectDirectory == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DIRECTORY_PICKER_UNAVAILABLE", Message: "native directory selection is unavailable"})
		return
	}
	var input contract.DirectorySelectionRequest
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	path, canceled, err := s.selectDirectory(request.Context(), "Choose a Portless source directory", input.InitialPath)
	if err != nil {
		writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "DIRECTORY_PICKER_FAILED", Message: err.Error()})
		return
	}
	if canceled {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(writer, http.StatusOK, contract.DirectorySelection{Path: path})
}

func (s *Server) handleRuntime(writer http.ResponseWriter, request *http.Request, segments []string, principal auth.Principal) {
	if len(segments) == 1 && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, runtimeStatusContract(s.app.RuntimeStatus(request.Context())))
		return
	}
	if len(segments) == 1 && request.Method == http.MethodPut {
		var input contract.UseRuntimeRequest
		if err := decodeJSON(request, &input); err != nil {
			writeDecodeError(writer, err)
			return
		}
		result, err := s.app.UseRuntime(request.Context(), input.Preference)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, runtimeStatusContract(result))
		return
	}
	if len(segments) == 2 && segments[1] == "start" && request.Method == http.MethodPost {
		result := s.app.StartRuntime(request.Context())
		if result.State != "ready" {
			writeAPIError(writer, http.StatusServiceUnavailable, contract.APIError{Code: "CONTAINER_RUNTIME_UNAVAILABLE", Message: result.Reason, Remediation: []contract.Remediation{{Label: "Inspect Docker and Podman", Command: "portless runtime status"}}})
			return
		}
		writeJSON(writer, http.StatusOK, runtimeStatusContract(result))
		return
	}
	if len(segments) == 2 && segments[1] == "reset" && request.Method == http.MethodGet {
		plan, err := s.app.ResetPlan(request.Context())
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, resetPlanContract(plan))
		return
	}
	if len(segments) == 2 && segments[1] == "reset" && request.Method == http.MethodPost {
		if principal.Session {
			writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "CLI_AUTH_REQUIRED", Message: "runtime reset preparation may only be requested by the local CLI"})
			return
		}
		var input contract.PrepareResetRequest
		if request.ContentLength != 0 {
			if err := decodeJSON(request, &input); err != nil {
				writeDecodeError(writer, err)
				return
			}
		}
		result, err := s.app.PrepareReset(request.Context(), input.Force)
		if err != nil {
			var active controlplane.ResetActiveEnvironmentsError
			if errors.As(err, &active) {
				writeAPIError(writer, http.StatusConflict, contract.APIError{
					Code: "ACTIVE_ENVIRONMENTS", Message: err.Error(),
					Details: map[string]any{"activeEnvironments": nonNil(active.Environments)},
				})
				return
			}
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, http.StatusOK, prepareResetContract(result))
		return
	}
	if len(segments) == 3 && segments[1] == "reset" && segments[2] == "cancel" && request.Method == http.MethodPost {
		if principal.Session {
			writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "CLI_AUTH_REQUIRED", Message: "runtime reset cancellation may only be requested by the local CLI"})
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
		writeJSON(writer, http.StatusOK, contract.Session{Actor: principal.Actor, Browser: principal.Session, CSRF: principal.CSRF})
		return
	}
	if len(segments) == 2 && segments[1] == "logout" && request.Method == http.MethodPost {
		if err := s.auth.Logout(request); err != nil {
			writeAPIError(writer, http.StatusInternalServerError, contract.APIError{Code: "SESSION_LOGOUT_FAILED", Message: err.Error()})
			return
		}
		http.SetCookie(writer, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "session route not found"})
}

func (s *Server) handleBrowserClaims(writer http.ResponseWriter, request *http.Request, principal auth.Principal) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if principal.Session {
		writeAPIError(writer, http.StatusForbidden, contract.APIError{Code: "CLI_AUTH_REQUIRED", Message: "only the local CLI may create browser claims"})
		return
	}
	var input contract.BrowserClaimRequest
	if err := decodeJSON(request, &input); err != nil {
		writeDecodeError(writer, err)
		return
	}
	code, expiresAt, err := s.auth.IssueClaim(input.Next)
	if err != nil {
		s.writeError(writer, err, nil)
		return
	}
	writeJSON(writer, http.StatusCreated, contract.BrowserClaimResponse{URL: "http://portless.localhost/auth/claim/" + code, ExpiresAt: expiresAt})
}
