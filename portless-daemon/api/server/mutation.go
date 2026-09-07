package server

import (
	"bytes"
	"encoding/base64"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"strings"
)

func requestResourceVersion(writer http.ResponseWriter, request *http.Request, required bool) (*contract.ResourceVersion, bool) {
	if mode := request.URL.Query().Get("mode"); mode != "" && mode != "apply" && mode != "preview" {
		writeAPIError(writer, 400, contract.APIError{Code: "INVALID_ARGUMENT", Message: "mode must be preview or apply"})
		return nil, false
	}
	value := request.Header.Get("If-Match")
	if value == "" && !required {
		return nil, true
	}
	if len(value) > 2048 || len(value) < 3 || !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `"`) {
		writeAPIError(writer, http.StatusPreconditionRequired, contract.APIError{Code: "PRECONDITION_REQUIRED", Message: "apply requires the resource identity returned by a fresh preview"})
		return nil, false
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value[1 : len(value)-1])
	if err != nil {
		pageError(writer, err)
		return nil, false
	}
	var version contract.ResourceVersion
	if err := decodeJSONReader(bytes.NewReader(encoded), &version); err != nil {
		writeDecodeError(writer, err)
		return nil, false
	}
	if version.CreatedAt.IsZero() {
		writeAPIError(writer, 428, contract.APIError{Code: "PRECONDITION_REQUIRED", Message: "expected creation identity is required"})
		return nil, false
	}
	return &version, true
}
