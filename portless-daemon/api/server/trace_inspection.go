package server

import (
	"fmt"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/controlplane"
	"net/http"
	"strconv"
)

func (s *Server) writeTracePage(writer http.ResponseWriter, request *http.Request, project, environment string, number int64) {
	offset, limit, err := queryPage(request, 50, 200)
	if err != nil {
		pageError(writer, err)
		return
	}
	expected, err := strconv.ParseUint(request.URL.Query().Get("expectedRevision"), 10, 64)
	if err != nil && request.URL.Query().Get("expectedRevision") != "" {
		pageError(writer, err)
		return
	}
	if offset > 0 && expected == 0 {
		pageError(writer, fmt.Errorf("expectedRevision is required for continuation"))
		return
	}
	result, err := s.app.TrafficTracePage(request.Context(), project, environment, number, offset, limit)
	if err != nil {
		if controlplane.IsNotFound(err) {
			writeAPIError(writer, 404, contract.APIError{Code: "TRAFFIC_TRACE_NOT_FOUND", Message: "traffic trace is no longer in the live buffer"})
			return
		}
		s.writeError(writer, err, environmentSubject(project, environment))
		return
	}
	if expected != 0 && expected != result.Trace.Revision {
		writeAPIError(writer, 409, contract.APIError{Code: "TRACE_CHANGED", Message: "trace changed; restart inspection from offset zero"})
		return
	}
	writeJSON(writer, 200, result)
}
