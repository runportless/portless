package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/traffic/replay"
)

func (s *Server) handleTrafficReplay(w http.ResponseWriter, r *http.Request, project, environment string, segments []string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get(contract.ClientKindHeader) == string(contract.ClientKindMCP) {
		writeAPIError(w, http.StatusForbidden, contract.APIError{Code: "REPLAY_CAPABILITY_REQUIRED", Message: "replay workspaces are not available through the current MCP capability contract"})
		return
	}
	if len(segments) < 5 || len(segments) > 7 {
		writeAPIError(w, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "replay route not found"})
		return
	}
	if len(segments) == 5 {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var input contract.PrepareTrafficReplayRequest
		if !decodeReplayJSON(w, r, &input) {
			return
		}
		result, err := s.app.PrepareTrafficReplay(r.Context(), project, environment, input)
		if err != nil {
			s.writeReplayError(w, err, project, environment)
			return
		}
		writeJSON(w, http.StatusCreated, result)
		return
	}
	number, numberErr := positiveTrafficNumber(segments[5], "replay")
	if numberErr != nil {
		writeAPIError(w, http.StatusBadRequest, *numberErr)
		return
	}
	if len(segments) == 6 {
		switch r.Method {
		case http.MethodGet:
			expected, err := replayQueryIdentity(r)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, contract.APIError{Code: "INVALID_REPLAY_IDENTITY", Message: "expected identity must contain both valid timestamps"})
				return
			}
			include := r.URL.Query().Get("include")
			if include != "" && include != "result" {
				writeAPIError(w, http.StatusBadRequest, contract.APIError{Code: "INVALID_REPLAY_QUERY", Message: "include must be result"})
				return
			}
			result, err := s.app.TrafficReplay(project, environment, number, expected, include == "result")
			if err != nil {
				s.writeReplayError(w, err, project, environment)
				return
			}
			writeJSON(w, http.StatusOK, result)
		case http.MethodDelete:
			var expected contract.TrafficReplayIdentity
			if !decodeReplayJSON(w, r, &expected) {
				return
			}
			if err := s.app.DeleteTrafficReplay(project, environment, number, expected); err != nil {
				s.writeReplayError(w, err, project, environment)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			methodNotAllowed(w, http.MethodGet, http.MethodDelete)
		}
		return
	}
	switch segments[6] {
	case "activity":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var expected contract.TrafficReplayIdentity
		if !decodeReplayJSON(w, r, &expected) {
			return
		}
		if err := s.app.TouchTrafficReplay(project, environment, number, expected); err != nil {
			s.writeReplayError(w, err, project, environment)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "draft":
		if r.Method != http.MethodPut {
			methodNotAllowed(w, http.MethodPut)
			return
		}
		var input contract.UpdateTrafficReplayDraftRequest
		if !decodeReplayJSON(w, r, &input) {
			return
		}
		result, err := s.app.UpdateTrafficReplay(r.Context(), project, environment, number, input)
		if err != nil {
			s.writeReplayError(w, err, project, environment)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "runs":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		var input contract.RunTrafficReplayRequest
		if !decodeReplayJSON(w, r, &input) {
			return
		}
		result, err := s.app.RunTrafficReplay(r.Context(), project, environment, number, input)
		if err != nil {
			s.writeReplayError(w, err, project, environment)
			return
		}
		writeJSON(w, http.StatusAccepted, result)
	default:
		writeAPIError(w, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "replay route not found"})
	}
}

func decodeReplayJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	limit := int64(1 << 20)
	if _, draft := target.(*contract.UpdateTrafficReplayDraftRequest); draft {
		// A body byte can occupy six JSON bytes (for example, \u0000).
		limit += 6 * contract.TrafficReplayMaxBodyBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	err := decodeJSONReader(r.Body, target)
	if err == nil {
		return true
	}
	status, code, message := http.StatusBadRequest, "INVALID_REPLAY_JSON", "provide one valid replay request with supported fields"
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status, code, message = http.StatusRequestEntityTooLarge, "REPLAY_INPUT_TOO_LARGE", "replay request exceeds the input limit"
	}
	writeAPIError(w, status, contract.APIError{Code: code, Message: message})
	return false
}

func replayQueryIdentity(r *http.Request) (contract.TrafficReplayIdentity, error) {
	var identity contract.TrafficReplayIdentity
	created, daemon := r.URL.Query().Get("expectedCreatedAt"), r.URL.Query().Get("expectedDaemonStartedAt")
	if created == "" && daemon == "" {
		return identity, nil
	}
	var err error
	identity.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return identity, err
	}
	identity.DaemonStartedAt, err = time.Parse(time.RFC3339Nano, daemon)
	return identity, err
}

func (s *Server) writeReplayError(w http.ResponseWriter, err error, project, environment string) {
	var failure *replay.Error
	if errors.As(err, &failure) {
		writeAPIError(w, failure.Status, contract.APIError{Code: failure.Code, Message: failure.Message, Subject: environmentSubject(project, environment)})
		return
	}
	s.writeError(w, err, environmentSubject(project, environment))
}
