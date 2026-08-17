package server

import (
	"io/fs"
	"net/http"
	"strings"
)

func (s *Server) serveUI(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet, http.MethodHead)
		return
	}
	clean := strings.TrimPrefix(request.URL.Path, "/")
	if clean != "" {
		if info, err := fs.Stat(s.assets, clean); err == nil && !info.IsDir() {
			s.files.ServeHTTP(writer, request)
			return
		}
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(s.indexHTML)
	}
}
