package replay

import (
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/model"
)

const (
	maxBodyBytes           = 64 << 10
	maxHeaderBytes         = 32 << 10
	maxResponseHeaderBytes = 64 << 10
	maxHeaderRows          = 128
	maxPathBytes           = 8 << 10
	redacted               = "[REDACTED]"
)

// Error classifies a replay failure without exposing request contents or runtime addresses.
type Error struct {
	Code    string
	Message string
	Status  int
}

// Error returns the fixed, safe replay failure message.
func (e *Error) Error() string { return e.Message }

func failure(code, message string, status int) error {
	return &Error{Code: code, Message: message, Status: status}
}

func validMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	}
	return false
}

func mutatingMethod(method string) bool {
	return method != "GET" && method != "HEAD" && method != "OPTIONS"
}

func mediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

func streamingMedia(value string) bool {
	value = mediaType(value)
	return value == "text/event-stream" || strings.HasPrefix(value, "application/grpc")
}

func textMedia(value string) bool {
	value = mediaType(value)
	return value == "" || strings.HasPrefix(value, "text/") || strings.Contains(value, "json") || strings.Contains(value, "xml") || value == "application/x-www-form-urlencoded" || value == "application/graphql" || value == "application/javascript"
}

func streamingHeaders(headers map[string][]string) bool {
	for name, values := range headers {
		for _, value := range values {
			if strings.EqualFold(name, "content-type") && streamingMedia(value) {
				return true
			}
			if strings.EqualFold(name, "accept") {
				for part := range strings.SplitSeq(value, ",") {
					if streamingMedia(part) {
						return true
					}
				}
			}
		}
	}
	return false
}

func validRequestTarget(target string) bool {
	if target == "" || len(target) > maxPathBytes || !utf8.ValidString(target) || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.ContainsAny(target, "#\\") {
		return false
	}
	for _, c := range target {
		if c <= ' ' || c == 127 {
			return false
		}
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	// ParseRequestURI leaves query escaping untouched; validate it independently.
	if _, err = url.QueryUnescape(parsed.RawQuery); err != nil {
		return false
	}
	return true
}

func token(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			continue
		}
		return false
	}
	return true
}

func forbiddenHeader(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "x-portless-") || strings.HasPrefix(name, "portless-") || strings.HasPrefix(name, "sec-") || strings.HasPrefix(name, "x-b3-") || strings.HasPrefix(name, "x-datadog-") {
		return true
	}
	switch name {
	case "host", "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "expect", "content-length", "forwarded", "traceparent", "tracestate", "baggage", "b3", "x-real-ip", "x-csrf-token", "x-xsrf-token":
		return true
	}
	return false
}

func sensitiveHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "x-auth-token", "sec-websocket-protocol":
		return true
	}
	return false
}

func headerBudget(headers map[string][]string, limit int, rowLimit bool) bool {
	rows, size := 0, 0
	for key, values := range headers {
		size += len(key)
		rows += max(1, len(values))
		for _, value := range values {
			size += len(value) + 4
		}
		if size > limit || rowLimit && rows > maxHeaderRows {
			return false
		}
	}
	return true
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	result := make(map[string][]string, len(headers))
	for key, values := range headers {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func cloneDraft(draft contract.TrafficReplayDraft) contract.TrafficReplayDraft {
	draft.Headers = cloneHeaders(draft.Headers)
	draft.OmittedHeaders = append([]string(nil), draft.OmittedHeaders...)
	return draft
}

func safeDraft(draft contract.TrafficReplayDraft) contract.TrafficReplayDraft {
	return scrubDraft(draft, draftSecrets(draft))
}

func scrubDraft(draft contract.TrafficReplayDraft, secrets []string) contract.TrafficReplayDraft {
	draft = cloneDraft(draft)
	for key, values := range draft.Headers {
		if sensitiveHeader(key) {
			draft.Headers[key] = []string{redacted}
			continue
		}
		for i := range values {
			values[i] = redactValues(values[i], secrets)
		}
	}
	draft.Body = redactValues(draft.Body, secrets)
	draft.RequestTarget = redactValues(draft.RequestTarget, secrets)
	return draft
}

func draftSecrets(draft contract.TrafficReplayDraft) []string {
	var secrets []string
	for key, values := range draft.Headers {
		if !sensitiveHeader(key) {
			continue
		}
		for _, value := range values {
			if value != "" && value != redacted {
				secrets = append(secrets, value)
			}
			if strings.EqualFold(key, "authorization") {
				if _, credential, ok := strings.Cut(value, " "); ok && credential != "" {
					secrets = append(secrets, credential)
				}
			}
			if strings.EqualFold(key, "cookie") {
				for cookie := range strings.SplitSeq(value, ";") {
					if _, credential, ok := strings.Cut(cookie, "="); ok && strings.TrimSpace(credential) != "" {
						secrets = append(secrets, strings.TrimSpace(credential))
					}
				}
			}
		}
	}
	// Replace longer credentials before their shorter substrings.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

func redactValues(value string, secrets []string) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, redacted)
	}
	return value
}

func completeBody(capture *model.HTTPCapture, body string) bool {
	return capture != nil && capture.Exact && capture.CapturedBytes == int64(len(body)) && capture.ObservedBytes == capture.CapturedBytes && (capture.State == "empty" && body == "" || capture.State == "complete" && (capture.Encoding == "" || strings.EqualFold(capture.Encoding, "identity"))) && utf8.ValidString(body)
}

func initialDraft(baseline model.TrafficExchange) (contract.TrafficReplayDraft, []contract.TrafficReplayLimitation) {
	draft := contract.TrafficReplayDraft{Environment: baseline.Environment, Method: baseline.Method, RequestTarget: baseline.RequestTarget, Headers: map[string][]string{}, BodyMode: "captured"}
	if draft.RequestTarget == "" {
		draft.RequestTarget = baseline.Path
	}
	var limitations []contract.TrafficReplayLimitation
	nominated := map[string]bool{}
	for key, values := range baseline.RequestHeaders {
		if strings.EqualFold(key, "connection") {
			for _, value := range values {
				for name := range strings.SplitSeq(value, ",") {
					nominated[strings.ToLower(strings.TrimSpace(name))] = true
				}
			}
		}
	}
	for key, values := range baseline.RequestHeaders {
		lower := strings.ToLower(key)
		if forbiddenHeader(key) || nominated[lower] || lower == "origin" || lower == "referer" || lower == "accept-encoding" {
			limitations = append(limitations, contract.TrafficReplayLimitation{Field: "headers", Code: "normalized-header", Message: "Captured routing, framing, trace and browser metadata headers are excluded or regenerated."})
			continue
		}
		name := textproto.CanonicalMIMEHeaderKey(key)
		draft.Headers[name] = append(draft.Headers[name], values...)
		if sensitiveHeader(name) || containsRedacted(values) {
			draft.Headers[name] = []string{redacted}
		}
	}
	draft.Headers["Accept-Encoding"] = []string{"identity"}
	if completeBody(baseline.RequestCapture, baseline.RequestBody) && len(baseline.RequestBody) <= maxBodyBytes {
		draft.Body = baseline.RequestBody
		if baseline.RequestCapture.State == "empty" {
			draft.BodyMode = "empty"
		}
	} else {
		limitations = append(limitations, bodyUnresolved())
	}
	limitations = append(limitations, unresolvedHeaders(baseline, draft)...)
	return draft, uniqueLimitations(limitations)
}

func containsRedacted(values []string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToUpper(value), redacted) {
			return true
		}
	}
	return false
}

func bodyUnresolved() contract.TrafficReplayLimitation {
	return contract.TrafficReplayLimitation{Field: "body", Code: "body-unresolved", Message: "The captured body is not complete; provide replacement text or explicitly choose an empty body."}
}

func unresolvedHeaders(baseline model.TrafficExchange, draft contract.TrafficReplayDraft) []contract.TrafficReplayLimitation {
	omitted := map[string]bool{}
	for _, name := range draft.OmittedHeaders {
		omitted[strings.ToLower(name)] = true
	}
	var result []contract.TrafficReplayLimitation
	for key, values := range baseline.RequestHeaders {
		if forbiddenHeader(key) || !sensitiveHeader(key) && !containsRedacted(values) {
			continue
		}
		if omitted[strings.ToLower(key)] {
			continue
		}
		provided := false
		for current, values := range draft.Headers {
			if strings.EqualFold(current, key) && len(values) > 0 && !containsRedacted(values) {
				provided = true
			}
		}
		if !provided {
			result = append(result, contract.TrafficReplayLimitation{Field: "headers", Code: "header-unresolved", Message: "A redacted captured header needs a supplied value or explicit omission."})
		}
	}
	return result
}

func validateDraft(input contract.TrafficReplayDraft, baseline model.TrafficExchange) (contract.TrafficReplayDraft, error) {
	if !validMethod(input.Method) {
		return input, failure("replay_invalid_method", "Method is not supported for request replay.", 400)
	}
	if !validRequestTarget(input.RequestTarget) {
		return input, failure("replay_invalid_path", "Path must be a valid bounded origin-form path and query.", 400)
	}
	if strings.Contains(strings.ToUpper(input.RequestTarget), redacted) {
		return input, failure("replay_unresolved_path", "Replace redacted path or query values before sending this request.", 400)
	}
	if input.Environment == "" || len(input.Environment) > 128 || strings.ContainsAny(input.Environment, "/\\\x00") {
		return input, failure("replay_invalid_environment", "Destination must name an environment in the origin project.", 400)
	}
	if len(input.Body) > contract.TrafficReplayMaxBodyBytes {
		return input, failure("replay_input_limit", "The request body is too large. Reduce its size and try again.", 413)
	}
	if !headerBudget(input.Headers, maxHeaderBytes, true) || len(input.OmittedHeaders) > maxHeaderRows {
		return input, failure("replay_input_limit", "Request headers exceed the replay input limit.", 413)
	}
	if !utf8.ValidString(input.Body) {
		return input, failure("replay_invalid_body", "Body must contain valid UTF-8 text.", 400)
	}
	if streamingHeaders(input.Headers) {
		return input, failure("replay_unsupported_stream", "gRPC and explicitly requested event streams are not supported for request replay.", 400)
	}
	draft := cloneDraft(input)
	draft.Headers = make(map[string][]string, len(input.Headers))
	omitted := map[string]bool{}
	omissionBytes := 0
	for _, name := range input.OmittedHeaders {
		omissionBytes += len(name)
		if omissionBytes > maxHeaderBytes {
			return input, failure("replay_input_limit", "Omitted header names exceed the replay input limit.", 413)
		}
		if !token(name) || len(name) > maxHeaderBytes {
			return input, failure("replay_invalid_header", "Omitted header names must be valid HTTP tokens.", 400)
		}
		omitted[strings.ToLower(name)] = true
	}
	for name, values := range input.Headers {
		if !token(name) || forbiddenHeader(name) || len(values) == 0 {
			return input, failure("replay_invalid_header", "Headers contain an invalid or reserved name.", 400)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if _, exists := draft.Headers[canonical]; exists {
			return input, failure("replay_duplicate_header", "Repeated header values must use one canonical name with an ordered value array.", 400)
		}
		if omitted[strings.ToLower(name)] {
			return input, failure("replay_conflicting_header", "A header cannot be both supplied and explicitly omitted.", 400)
		}
		for _, value := range values {
			if !utf8.ValidString(value) || containsRedacted([]string{value}) {
				return input, failure("replay_unresolved_header", "Header values must be supplied explicitly; redaction placeholders cannot be sent.", 400)
			}
			for _, c := range value {
				if c < 32 && c != '\t' || c == 127 {
					return input, failure("replay_invalid_header", "Header values cannot contain control characters.", 400)
				}
			}
			if strings.EqualFold(name, "cookie") {
				for cookie := range strings.SplitSeq(value, ";") {
					if key, _, _ := strings.Cut(strings.TrimSpace(cookie), "="); key == "portless_session" {
						return input, failure("replay_control_credential", "Portless control cookies cannot be replayed.", 400)
					}
				}
			}
			if strings.EqualFold(name, "accept-encoding") && !strings.EqualFold(strings.TrimSpace(value), "identity") {
				return input, failure("replay_invalid_encoding", "Replay response negotiation requires identity encoding.", 400)
			}
			if strings.EqualFold(name, "content-encoding") && !strings.EqualFold(strings.TrimSpace(value), "identity") {
				return input, failure("replay_invalid_encoding", "Replay request bodies require identity encoding; remove the captured content encoding when replacing a body.", 400)
			}
			if strings.EqualFold(name, "content-type") && input.BodyMode != "empty" {
				if !textMedia(value) {
					return input, failure("replay_unsupported_body", "Binary and multipart request bodies are not supported; provide text with an appropriate content type.", 400)
				}
			}
		}
		draft.Headers[canonical] = append([]string(nil), values...)
	}
	draft.Headers["Accept-Encoding"] = []string{"identity"}
	if len(unresolvedHeaders(baseline, draft)) > 0 {
		return input, failure("replay_unresolved_header", "Resolve each redacted captured header by supplying a value or explicitly omitting it.", 400)
	}
	switch input.BodyMode {
	case "captured":
		if !completeBody(baseline.RequestCapture, baseline.RequestBody) {
			return input, failure("replay_unresolved_body", bodyUnresolved().Message, 400)
		}
		if input.Body != baseline.RequestBody {
			return input, failure("replay_body_mode", "Edited body text requires replacement body mode.", 400)
		}
	case "replacement":
	case "empty":
		if input.Body != "" {
			return input, failure("replay_body_mode", "Empty body mode cannot include body text.", 400)
		}
	default:
		return input, failure("replay_body_mode", "Body mode must be captured, replacement, or empty.", 400)
	}
	if strings.Contains(strings.ToUpper(draft.Body), redacted) {
		return input, failure("replay_redacted_body", "A redaction placeholder cannot be replayed as body content.", 400)
	}
	return draft, nil
}

func uniqueLimitations(input []contract.TrafficReplayLimitation) []contract.TrafficReplayLimitation {
	seen := map[string]bool{}
	var result []contract.TrafficReplayLimitation
	for _, value := range input {
		if !seen[value.Field+value.Code] {
			seen[value.Field+value.Code] = true
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Field+result[i].Code < result[j].Field+result[j].Code })
	return result
}
