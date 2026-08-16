package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func normalizedHost(hostPort string) string {
	if host, _, err := net.SplitHostPort(hostPort); err == nil {
		return strings.Trim(strings.ToLower(host), "[]")
	}
	return strings.Trim(strings.ToLower(hostPort), "[]")
}

func isControlHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "portless.localhost"
}

func applicationHost(host string) (service, environment, project string, ok bool) {
	if !strings.HasSuffix(host, ".localhost") || host == "portless.localhost" {
		return "", "", "", false
	}
	labels := strings.Split(strings.TrimSuffix(host, ".localhost"), ".")
	if len(labels) != 3 || model.ValidateServiceName(labels[0]) != nil || model.ValidateEnvironmentName(labels[1]) != nil || model.ValidateProjectName(labels[2]) != nil {
		return "", "", "", false
	}
	return labels[0], labels[1], labels[2], true
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || strings.Contains(decoded, "/") {
			return []string{"__invalid__"}
		}
		result = append(result, decoded)
	}
	return result
}

func isMutation(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func queryLimit(request *http.Request, fallback, maximum int) (int, error) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > maximum {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	return parsed, nil
}

func queryNonNegativeInt64(request *http.Request, key string) (int64, error) {
	value := request.URL.Query().Get(key)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return parsed, nil
}

func querySince(request *http.Request) (time.Time, error) {
	value := request.URL.Query().Get("since")
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil && duration >= 0 {
		return time.Now().UTC().Add(-duration), nil
	}
	if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return timestamp, nil
	}
	return time.Time{}, errors.New("since must be a duration such as 10m or an RFC3339 timestamp")
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func limited[T any](items []T, limit int) []T {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}
