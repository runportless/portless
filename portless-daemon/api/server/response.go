package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/runportless/portless/portless-daemon/api/contract"
)

func setSecurityHeaders(headers http.Header) {
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Frame-Options", "DENY")
	headers.Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, apiError contract.APIError) {
	writeJSON(writer, status, contract.ErrorEnvelope{Error: apiError})
}

func writeDecodeError(writer http.ResponseWriter, err error) {
	writeAPIError(writer, http.StatusBadRequest, contract.APIError{Code: "INVALID_JSON", Message: err.Error()})
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}

func methodNotAllowed(writer http.ResponseWriter, methods ...string) {
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(writer, http.StatusMethodNotAllowed, contract.APIError{Code: "METHOD_NOT_ALLOWED", Message: "method not allowed"})
}
