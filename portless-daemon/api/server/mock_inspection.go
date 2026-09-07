package server

import (
	"encoding/json"
	"fmt"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

func queryPage(request *http.Request, defaultLimit, maximum int) (int, int, error) {
	values := request.URL.Query()
	offset := 0
	limit := defaultLimit
	var err error
	if values.Has("offset") {
		offset, err = strconv.Atoi(values.Get("offset"))
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
	}
	if values.Has("limit") && values.Get("limit") != "0" {
		limit, err = strconv.Atoi(values.Get("limit"))
		if err != nil || limit < 1 || limit > maximum {
			return 0, 0, fmt.Errorf("limit must be from 1 through %d", maximum)
		}
	}
	return offset, limit, nil
}

func pageError(writer http.ResponseWriter, err error) {
	writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_PAGE", Message: err.Error()})
}

func requireModifiedAt(writer http.ResponseWriter, request *http.Request, actual time.Time, continuation bool) bool {
	value := request.URL.Query().Get("expectedModifiedAt")
	if value == "" && !continuation {
		return true
	}
	expected, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		pageError(writer, fmt.Errorf("expectedModifiedAt is required for continuation and must be an RFC3339 timestamp"))
		return false
	}
	if !expected.Equal(actual) {
		writeAPIError(writer, http.StatusConflict, contract.APIError{Code: "RESOURCE_CHANGED", Message: "saved content changed; restart inspection from offset zero"})
		return false
	}
	return true
}

func savedTextRange(content string, offset, limit int) (contract.TextRange, error) {
	if offset < 0 || offset > len(content) || (offset < len(content) && !utf8.RuneStart(content[offset])) {
		return contract.TextRange{}, fmt.Errorf("offset must identify a UTF-8 boundary inside the saved document")
	}
	end := min(len(content), offset+limit)
	for end < len(content) && end > offset && !utf8.RuneStart(content[end]) {
		end--
	}
	result := contract.TextRange{Content: content[offset:end], MediaType: "application/json", Offset: offset, TotalBytes: len(content)}
	if end < len(content) {
		result.NextOffset = end
	}
	return result, nil
}

func (s *Server) handleMockMetadata(writer http.ResponseWriter, request *http.Request, project, environment string, segments []string) {
	writer.Header().Set("Cache-Control", "no-store")
	if len(segments) != 4 && len(segments) != 5 && !(len(segments) == 7 && segments[5] == "routes") {
		writeAPIError(writer, 404, contract.APIError{Code: "ROUTE_NOT_FOUND", Message: "mock inspection route not found"})
		return
	}
	defaultLimit, maximum := 100, 500
	if len(segments) == 7 {
		defaultLimit, maximum = 32<<10, 128<<10
	}
	offset, limit, err := queryPage(request, defaultLimit, maximum)
	if err != nil {
		pageError(writer, err)
		return
	}
	if len(segments) == 4 {
		items, total, err := s.app.MockScenarioMetadataList(request.Context(), project, environment, offset, limit)
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		result := contract.MockScenarioMetadataList{Scenarios: items, Total: total}
		if offset+len(items) < total {
			result.NextOffset = offset + len(items)
		}
		writeJSON(writer, 200, result)
		return
	}
	name := segments[4]
	route := ""
	routeOffset, routeLimit := offset, limit
	includePayloads := false
	if len(segments) == 7 {
		route = segments[6]
		routeOffset, routeLimit = 0, 1
		if request.URL.Query().Has("includePayloads") {
			includePayloads, err = strconv.ParseBool(request.URL.Query().Get("includePayloads"))
			if err != nil {
				pageError(writer, err)
				return
			}
		}
	}
	scenario, routes, payload, err := s.app.InspectMockScenario(request.Context(), project, environment, name, route, routeOffset, routeLimit, includePayloads)
	if err != nil {
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if !requireModifiedAt(writer, request, scenario.ModifiedAt, offset > 0) {
		return
	}
	if route == "" {
		result := contract.MockScenarioMetadataPage{Scenario: scenario, Routes: routes}
		if offset+len(routes) < scenario.RouteCount {
			result.NextOffset = offset + len(routes)
		}
		writeJSON(writer, 200, result)
		return
	}
	if len(routes) == 0 {
		writeAPIError(writer, 404, contract.APIError{Code: "MOCK_ROUTE_NOT_FOUND", Message: "mock route does not exist"})
		return
	}
	result := contract.MockRouteDetail{Scenario: scenario, Route: routes[0]}
	if payload != nil {
		encoded, err := json.Marshal(struct {
			Query   map[string]contract.MockQueryMatcher `json:"query"`
			Headers map[string]string                    `json:"headers"`
			Body    string                               `json:"body"`
		}{payload.Query, payload.Headers, payload.Body})
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		text, err := savedTextRange(string(encoded), offset, limit)
		if err != nil {
			pageError(writer, err)
			return
		}
		result.Payload = &text
	}
	writeJSON(writer, 200, result)
}

func mockResponse(request *http.Request, scenario contract.MockScenario) contract.MockScenario {
	if request.Header.Get(contract.MockMetadataHeader) == "1" {
		scenario.RouteCount = len(scenario.Routes)
		scenario.Routes = nil
		scenario.PayloadsOmitted = true
	}
	return scenario
}

func decodeMockJSON(writer http.ResponseWriter, request *http.Request, output any) error {
	return decodeJSONReader(http.MaxBytesReader(writer, request.Body, 7<<20), output)
}
