package server

import (
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
)

func (s *Server) configurationCondition(writer http.ResponseWriter, request *http.Request, project, environment, source string) (*contract.ResourceVersion, bool) {
	if request.URL.Query().Get("mode") == "preview" {
		result, err := s.app.PreviewConfiguration(request.Context(), project, environment, source)
		if err != nil {
			s.writeError(writer, err, nil)
		} else {
			writeJSON(writer, http.StatusOK, result)
		}
		return nil, false
	}
	return requestResourceVersion(writer, request, request.Header.Get(contract.ClientKindHeader) == string(contract.ClientKindMCP))
}

func requireSourceRoot(writer http.ResponseWriter, request *http.Request, root string) bool {
	if request.Header.Get(contract.ClientKindHeader) == string(contract.ClientKindMCP) && root == "" {
		writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "SOURCE_ROOT_REQUIRED", Message: "MCP source changes require an authorized source root"})
		return false
	}
	return true
}
