package server

import (
	"github.com/runportless/portless/portless-daemon/api/contract"
	"mime"
	"net/http"
	"strconv"
)

type recordingExportWriter struct {
	http.ResponseWriter
	started bool
}

// Write records whether streaming began so a later failure aborts the response.
func (w *recordingExportWriter) Write(value []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(value)
}

func (s *Server) handleRecordingExport(writer http.ResponseWriter, request *http.Request, project, environment, name string, chunked bool) {
	writer.Header().Set("Cache-Control", "no-store")
	if chunked {
		maxBytes := 0
		var err error
		if value := request.URL.Query().Get("maxBytes"); value != "" {
			maxBytes, err = strconv.Atoi(value)
			if err != nil {
				pageError(writer, err)
				return
			}
		}
		result, err := s.app.RecordingExportChunk(request.Context(), project, environment, name, contract.RecordingExportChunkQuery{Cursor: request.URL.Query().Get("cursor"), MaxBytes: maxBytes})
		if err != nil {
			s.writeError(writer, err, environmentSubject(project, environment))
			return
		}
		writeJSON(writer, 200, result)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".json"}))
	stream := &recordingExportWriter{ResponseWriter: writer}
	if err := s.app.WriteRecordingExport(request.Context(), project, environment, name, stream); err != nil {
		if stream.started {
			panic(http.ErrAbortHandler)
		}
		writer.Header().Del("Content-Disposition")
		s.writeError(writer, err, environmentSubject(project, environment))
	}
}
