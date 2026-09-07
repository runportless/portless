package portlessmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

type codedError struct {
	code        string
	message     string
	status      int
	subject     map[string]any
	details     map[string]any
	remediation []contract.Remediation
}

// Error returns the stable JSON representation exposed as MCP tool content.
func (e codedError) Error() string {
	message, _ := truncateUTF8(e.message, 4<<10)
	code, _ := truncateUTF8(e.code, 128)
	encoded, _ := json.Marshal(errorEnvelope{Error: errorValue{
		Code: code, Message: message, Status: e.status,
		Subject: safeErrorMap(e.subject), Details: safeErrorMap(e.details),
		Remediation: boundedRemediation(e.remediation),
	}})
	if len(encoded) > 64<<10 {
		encoded, _ = json.Marshal(errorEnvelope{Error: errorValue{Code: code, Message: message, Status: e.status, Details: map[string]any{"truncated": true}, Remediation: boundedRemediation(e.remediation)}})
	}
	return string(encoded)
}

func (r *runtime) toolError(err error) error {
	if err == nil {
		return nil
	}
	var coded codedError
	if errors.As(err, &coded) {
		return coded
	}
	if errors.Is(err, context.Canceled) {
		return codedError{code: "CANCELLED", message: "the MCP tool call was cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return codedError{code: "TIMEOUT", message: "the MCP tool call exceeded its time limit"}
	}
	var clientError *apiclient.ClientError
	if errors.As(err, &clientError) {
		code := clientError.Code
		if code == "" {
			code = "DAEMON_REQUEST_FAILED"
		}
		return codedError{
			code: code, message: clientError.Message, status: clientError.Status,
			subject: clientError.Subject, details: clientError.Details,
			remediation: clientError.Remediation,
		}
	}
	r.logger.Error("MCP tool failed", "errorType", fmt.Sprintf("%T", err))
	return codedError{code: "INTERNAL", message: "the Portless MCP tool failed unexpectedly; inspect MCP stderr for local diagnostics"}
}

type errorEnvelope struct {
	Error errorValue `json:"error"`
}

type errorValue struct {
	Code        string                 `json:"code"`
	Message     string                 `json:"message"`
	Status      int                    `json:"status,omitempty"`
	Subject     map[string]any         `json:"subject,omitempty"`
	Details     map[string]any         `json:"details,omitempty"`
	Remediation []contract.Remediation `json:"remediation"`
}

func safeErrorMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = keys[:8]
	}
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") ||
			strings.Contains(lower, "credential") || strings.Contains(lower, "password") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
			result[key] = "[REDACTED]"
			continue
		}
		result[key] = safeErrorValue(values[key], 0)
	}
	return result
}

func safeErrorValue(value any, depth int) any {
	if depth >= 3 {
		return "[TRUNCATED]"
	}
	switch typed := value.(type) {
	case nil, bool, float64, json.Number:
		return typed
	case string:
		value, truncated := truncateUTF8(typed, 512)
		if truncated {
			return value + "…"
		}
		return value
	case []any:
		maximum := len(typed)
		if maximum > 8 {
			maximum = 8
		}
		result := make([]any, 0, maximum)
		for _, item := range typed[:maximum] {
			result = append(result, safeErrorValue(item, depth+1))
		}
		return result
	case map[string]any:
		return safeErrorMapAtDepth(typed, depth+1)
	default:
		return "[UNAVAILABLE]"
	}
}

func safeErrorMapAtDepth(values map[string]any, depth int) map[string]any {
	if depth >= 3 {
		return map[string]any{"truncated": true}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = keys[:8]
	}
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") ||
			strings.Contains(lower, "credential") || strings.Contains(lower, "password") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
			result[key] = "[REDACTED]"
			continue
		}
		result[key] = safeErrorValue(values[key], depth)
	}
	return result
}

func boundedRemediation(values []contract.Remediation) []contract.Remediation {
	result := []contract.Remediation{}
	for _, value := range values[:min(8, len(values))] {
		value.Label, _ = truncateUTF8(value.Label, 512)
		value.Command, _ = truncateUTF8(value.Command, 1024)
		if parsed, err := url.Parse(value.URL); err == nil {
			parsed.User = nil
			parsed.RawQuery = ""
			parsed.Fragment = ""
			value.URL = parsed.String()
		} else {
			value.URL = ""
		}
		value.URL, _ = truncateUTF8(value.URL, 1024)
		result = append(result, value)
	}
	return result
}
