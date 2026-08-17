package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/api/contract"
	"github.com/portless-run/portless/portless-daemon/auth"
	"github.com/portless-run/portless/portless-daemon/controlplane"
	"github.com/portless-run/portless/portless-daemon/model"
)

func (s *Server) handleTraffic(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if len(segments) == 4 {
		if request.Method != http.MethodDelete {
			methodNotAllowed(writer, http.MethodDelete)
			return
		}
		if _, err := s.app.Environment(request.Context(), project, environment); err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		cleared, throughSequence := s.app.ClearTraffic(project, environment)
		writeJSON(writer, http.StatusOK, contract.TrafficClearResponse{Cleared: cleared, ThroughSequence: throughSequence})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if len(segments) < 5 || len(segments) > 6 {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "traffic route not found"})
		return
	}
	if _, err := s.app.Environment(request.Context(), project, environment); err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	switch segments[4] {
	case "exchanges":
		s.handleTrafficExchanges(writer, request, project, environment, segments)
	case "traces":
		s.handleTrafficTraces(writer, request, project, environment, segments)
	default:
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "traffic route not found"})
	}
}

func (s *Server) handleTrafficExchanges(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if len(segments) == 6 {
		sequence, err := positiveTrafficNumber(segments[5], "exchange")
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, *err)
			return
		}
		exchange, findErr := s.app.TrafficExchange(request.Context(), project, environment, sequence)
		if findErr != nil {
			if controlplane.IsNotFound(findErr) {
				writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "TRAFFIC_EXCHANGE_NOT_FOUND", Message: "traffic exchange is no longer in the live buffer or a retained recording", Remediation: []contract.Remediation{{Label: "Capture durable traffic", Command: "portless record start debug"}}})
				return
			}
			s.writeError(writer, findErr, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, exchange)
		return
	}
	protocol := request.URL.Query().Get("protocol")
	if protocol == "" {
		protocol = "all"
	}
	if protocol != "all" && protocol != string(model.ProtocolHTTP) && protocol != string(model.ProtocolTCP) {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_TRAFFIC_PROTOCOL", Message: "protocol must be all, http, or tcp"})
		return
	}
	limit, err := queryLimit(request, 250, 1000)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: err.Error()})
		return
	}
	all := s.app.TrafficExchanges(project, environment, 5000)
	filtered := make([]model.TrafficExchange, 0, len(all))
	service := request.URL.Query().Get("service")
	source := request.URL.Query().Get("source")
	target := request.URL.Query().Get("target")
	if edge := request.URL.Query().Get("edge"); edge != "" {
		var found bool
		source, target, found = strings.Cut(edge, ":")
		if !found || source == "" || target == "" || strings.Contains(target, ":") {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_EDGE", Message: "edge must use source:target"})
			return
		}
	}
	after, err := queryNonNegativeInt64(request, "after")
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_AFTER", Message: err.Error()})
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
	writeJSON(writer, http.StatusOK, contract.TrafficExchangeList{Exchanges: filtered})
}

func (s *Server) handleTrafficTraces(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if len(segments) == 6 {
		number, err := positiveTrafficNumber(segments[5], "trace")
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, *err)
			return
		}
		trace, findErr := s.app.TrafficTrace(project, environment, number)
		if findErr != nil {
			if controlplane.IsNotFound(findErr) {
				writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "TRAFFIC_TRACE_NOT_FOUND", Message: "traffic trace is no longer in the live buffer"})
				return
			}
			s.writeError(writer, findErr, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, trace)
		return
	}
	limit, err := queryLimit(request, 100, 1000)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: err.Error()})
		return
	}
	source, target, edgeErr := trafficEdge(request.URL.Query().Get("edge"))
	if edgeErr != nil {
		writeAPIError(writer, http.StatusBadRequest, *edgeErr)
		return
	}
	service := request.URL.Query().Get("service")
	includeBackground := request.URL.Query().Get("background") == "include"
	filtered := make([]model.TrafficTrace, 0, limit)
	for _, trace := range s.app.TrafficTraces(project, environment, 5000) {
		if trace.Background && !includeBackground {
			continue
		}
		if !traceMatches(trace, service, source, target) {
			continue
		}
		trace.Spans = nil
		filtered = append(filtered, trace)
		if len(filtered) == limit {
			break
		}
	}
	writeJSON(writer, http.StatusOK, contract.TrafficTraceList{Traces: filtered})
}

func positiveTrafficNumber(value, kind string) (int64, *contract.APIError) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err == nil && number > 0 {
		return number, nil
	}
	apiError := contract.APIError{Code: "INVALID_TRAFFIC_NUMBER", Message: "traffic " + kind + " number must be a positive integer"}
	return 0, &apiError
}

func trafficEdge(value string) (string, string, *contract.APIError) {
	if value == "" {
		return "", "", nil
	}
	source, target, found := strings.Cut(value, ":")
	if !found || source == "" || target == "" || strings.Contains(target, ":") {
		apiError := contract.APIError{Code: "INVALID_EDGE", Message: "edge must use source:target"}
		return "", "", &apiError
	}
	return source, target, nil
}

func traceMatches(trace model.TrafficTrace, service, source, target string) bool {
	if service == "" && source == "" && target == "" {
		return true
	}
	for _, span := range trace.Spans {
		exchange := span.Exchange
		if service != "" && exchange.Source != service && exchange.Target != service {
			continue
		}
		if source != "" && exchange.Source != source {
			continue
		}
		if target != "" && exchange.Target != target {
			continue
		}
		return true
	}
	return false
}

func (s *Server) handleStream(writer http.ResponseWriter, request *http.Request, project, environment string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, contract.APIError{Code: "STREAM_UNAVAILABLE", Message: "streaming is unavailable"})
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
	subscription := s.app.Subscribe(request.Context(), scope, topics)
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
			if event.Type == "traffic.exchange" {
				if exchange, ok := event.Data.(model.TrafficExchange); ok {
					data = trafficSummary(exchange)
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
				writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			recordings, err := s.app.Recordings(ctx, project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, contract.RecordingList{Recordings: limited(nonNil(recordings), limit)})
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
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "recording route not found"})
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
		exchanges, err := s.app.RecordedTraffic(ctx, project, environment, name, 10_000)
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, name))
		writeJSON(writer, http.StatusOK, contract.RecordingExport{SchemaVersion: 2, Project: project, Environment: environment, Recording: name, Exchanges: exchanges})
		return
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "recording route not found"})
}

func (s *Server) handleFaults(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string, principal auth.Principal) {
	ctx := request.Context()
	if len(segments) == 4 {
		switch request.Method {
		case http.MethodGet:
			limit, limitErr := queryLimit(request, 100, 1000)
			if limitErr != nil {
				writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
				return
			}
			faults, err := s.app.Faults(ctx, project, environment)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, contract.FaultList{Faults: limited(nonNil(faults), limit)})
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
		writeJSON(writer, http.StatusOK, contract.DisableFaultsResponse{Disabled: count})
		return
	}
	if len(segments) == 6 && request.Method == http.MethodPost {
		name := segments[4]
		switch segments[5] {
		case "enable":
			fault, err := s.app.EnableFault(ctx, project, environment, name, principal.Actor)
			if err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writeJSON(writer, http.StatusOK, fault)
			return
		case "disable":
			if err := s.app.DisableFault(ctx, project, environment, name, principal.Actor); err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
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
			if err := s.app.DeleteFault(ctx, project, environment, name, principal.Actor); err != nil {
				s.writeError(writer, err, environmentSubject(project, environment))
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "fault route not found"})
}

func (s *Server) handleOperations(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	if request.Method == http.MethodGet && len(segments) == 4 {
		limit, limitErr := queryLimit(request, 100, 500)
		if limitErr != nil {
			writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
			return
		}
		operations, err := s.app.Operations(request.Context(), project, environment, limit)
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, http.StatusOK, contract.OperationList{Operations: nonNil(operations)})
		return
	}
	if request.Method != http.MethodGet || len(segments) < 5 {
		writeAPIError(writer, http.StatusNotImplemented, contract.APIError{Code: "OPERATION_CANCEL_UNAVAILABLE", Message: "operation cancellation is not available after execution has begun"})
		return
	}
	if len(segments) != 5 && !(len(segments) == 6 && segments[5] == "events") {
		writeAPIError(writer, http.StatusNotFound, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "operation route not found"})
		return
	}
	number, err := strconv.ParseInt(segments[4], 10, 64)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_OPERATION_NUMBER", Message: "operation number must be an integer"})
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
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_LIMIT", Message: limitErr.Error()})
		return
	}
	events, err := s.app.Timeline(request.Context(), project, environment, limit)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	writeJSON(writer, http.StatusOK, contract.TimelineList{Timeline: nonNil(events)})
}
