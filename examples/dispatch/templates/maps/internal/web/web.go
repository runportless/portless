// Package web contains the small HTTP conventions shared by the Go services.
package web

import (
	"encoding/json"
	"net/http"
)

// Error is the common structured error envelope.
type Error struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail identifies one failed request.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// JSON writes a JSON response.
func JSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// WriteError writes the common error envelope.
func WriteError(writer http.ResponseWriter, status int, code, message string) {
	JSON(writer, status, Error{Error: ErrorDetail{Code: code, Message: message}})
}

// TraceHeaders copies W3C trace propagation headers from an inbound request.
func TraceHeaders(request *http.Request) http.Header {
	result := make(http.Header)
	for _, name := range []string{"traceparent", "tracestate"} {
		if value := request.Header.Get(name); value != "" {
			result.Set(name, value)
		}
	}
	return result
}
