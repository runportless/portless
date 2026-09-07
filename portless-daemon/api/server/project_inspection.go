package server

import (
	"encoding/json"
	"fmt"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleProjectMetadata(writer http.ResponseWriter, request *http.Request, project string) {
	if project != "" {
		value, err := s.app.ProjectMetadata(request.Context(), project)
		if err != nil {
			s.writeError(writer, err, nil)
			return
		}
		writeJSON(writer, 200, value)
		return
	}
	offset, limit, err := queryPage(request, 100, 500)
	if err != nil {
		pageError(writer, err)
		return
	}
	items, total, err := s.app.ProjectMetadataList(request.Context(), offset, limit)
	if err != nil {
		s.writeError(writer, err, nil)
		return
	}
	result := contract.ProjectMetadataList{Projects: items, Total: total}
	if offset+len(items) < total {
		result.NextOffset = offset + len(items)
	}
	writeJSON(writer, 200, result)
}

func (s *Server) handleProjectDeclarationChunk(writer http.ResponseWriter, request *http.Request, project string) {
	offset, limit, err := queryPage(request, 32<<10, 128<<10)
	if err != nil {
		pageError(writer, err)
		return
	}
	value, err := s.app.ProjectDeclaration(request.Context(), project)
	if err != nil {
		s.writeError(writer, err, nil)
		return
	}
	expected, err := strconv.ParseInt(request.URL.Query().Get("expectedRevision"), 10, 64)
	if err != nil && request.URL.Query().Get("expectedRevision") != "" {
		pageError(writer, err)
		return
	}
	created := request.URL.Query().Get("expectedCreatedAt")
	if offset > 0 && (expected < 1 || created == "") {
		pageError(writer, fmt.Errorf("continuation requires expectedRevision and expectedCreatedAt"))
		return
	}
	if expected != 0 && expected != value.Revision {
		writeAPIError(writer, 409, contract.APIError{Code: "RESOURCE_CHANGED", Message: "project declaration changed; restart inspection"})
		return
	}
	if created != "" {
		identity, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			pageError(writer, err)
			return
		}
		if !identity.Equal(value.CreatedAt) {
			writeAPIError(writer, 409, contract.APIError{Code: "RESOURCE_CHANGED", Message: "project identity changed; restart inspection"})
			return
		}
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		s.writeError(writer, err, nil)
		return
	}
	text, err := savedTextRange(string(content), offset, limit)
	if err != nil {
		pageError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, 200, contract.ProjectDeclarationChunk{Revision: value.Revision, CreatedAt: value.CreatedAt, Redacted: len(value.Redactions) > 0, Content: text})
}
