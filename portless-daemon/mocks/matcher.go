// Package mocks compiles and serves deterministic environment-scoped HTTP mocks.
package mocks

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
)

var pathParameter = regexp.MustCompile(`^\{[A-Za-z_][A-Za-z0-9_]*\}$`)

const (
	// MaxRoutesPerProfile bounds matcher compilation and persisted profile size.
	MaxRoutesPerProfile = 1000
	// MaxResponseBodyBytes bounds one fixed mock response.
	MaxResponseBodyBytes = 1 << 20
	// MaxProfileBodyBytes bounds all fixed response bodies retained by one profile.
	MaxProfileBodyBytes = 8 << 20
	// MaxPreviewRequestBodyBytes bounds a side-effect-free preview request body.
	MaxPreviewRequestBodyBytes = 256 << 10
)

// ErrNoMatch indicates that no enabled route accepts a request.
var ErrNoMatch = errors.New("no mock route matched the request")

type compiledRoute struct {
	route        model.MockRoute
	segments     []string
	literalCount int
	queryCount   int
}

// CompiledProfile is an immutable, validated matcher ready for concurrent use.
type CompiledProfile struct {
	profile model.MockProfile
	routes  []compiledRoute
}

// Compile validates a profile and orders its routes by deterministic specificity.
func Compile(profile model.MockProfile) (*CompiledProfile, error) {
	if err := model.ValidateArtifactName(profile.Name); err != nil {
		return nil, fmt.Errorf("invalid mock profile name: %w", err)
	}
	if err := model.ValidateServiceName(profile.Service); err != nil {
		return nil, fmt.Errorf("invalid mock service: %w", err)
	}
	if len(profile.Routes) > MaxRoutesPerProfile {
		return nil, fmt.Errorf("mock profiles cannot contain more than %d routes", MaxRoutesPerProfile)
	}
	profile = cloneProfile(profile)
	result := &CompiledProfile{profile: profile}
	seen := map[string]struct{}{}
	totalBodyBytes := 0
	for _, route := range profile.Routes {
		if len(route.Body) > MaxResponseBodyBytes {
			return nil, fmt.Errorf("route %s response body exceeds %d bytes", route.Name, MaxResponseBodyBytes)
		}
		totalBodyBytes += len(route.Body)
		if totalBodyBytes > MaxProfileBodyBytes {
			return nil, fmt.Errorf("mock profile response bodies exceed %d bytes", MaxProfileBodyBytes)
		}
		compiled, err := compileRoute(route)
		if err != nil {
			return nil, fmt.Errorf("route %s: %w", route.Name, err)
		}
		key := strings.ToLower(route.Name)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("route name %s is duplicated", route.Name)
		}
		seen[key] = struct{}{}
		result.routes = append(result.routes, compiled)
	}
	for left := 0; left < len(result.routes); left++ {
		if !result.routes[left].route.Enabled {
			continue
		}
		for right := left + 1; right < len(result.routes); right++ {
			if !result.routes[right].route.Enabled {
				continue
			}
			if routesAreAmbiguous(result.routes[left], result.routes[right]) {
				return nil, fmt.Errorf("routes %s and %s are ambiguous; make one path or query matcher more specific",
					result.routes[left].route.Name, result.routes[right].route.Name)
			}
		}
	}
	sort.SliceStable(result.routes, func(i, j int) bool {
		left, right := result.routes[i], result.routes[j]
		if left.literalCount != right.literalCount {
			return left.literalCount > right.literalCount
		}
		if left.queryCount != right.queryCount {
			return left.queryCount > right.queryCount
		}
		if len(left.segments) != len(right.segments) {
			return len(left.segments) > len(right.segments)
		}
		return strings.ToLower(left.route.Name) < strings.ToLower(right.route.Name)
	})
	return result, nil
}

// Profile returns a copy of the profile represented by the matcher.
func (c *CompiledProfile) Profile() model.MockProfile {
	return cloneProfile(c.profile)
}

// Match returns the most specific route that accepts the request.
func (c *CompiledProfile) Match(method, path string, query url.Values) (model.MockRoute, error) {
	method = strings.ToUpper(method)
	segments := splitPath(path)
	for _, candidate := range c.routes {
		if !candidate.route.Enabled || candidate.route.Method != method || len(candidate.segments) != len(segments) {
			continue
		}
		matched := true
		for index, expected := range candidate.segments {
			if pathParameter.MatchString(expected) {
				if segments[index] == "" {
					matched = false
					break
				}
				continue
			}
			if expected != segments[index] {
				matched = false
				break
			}
		}
		if !matched || !matchesQuery(candidate.route.Query, query) {
			continue
		}
		return cloneRoute(candidate.route), nil
	}
	return model.MockRoute{}, ErrNoMatch
}

// Preview validates and evaluates a request and returns the response that would be served.
func (c *CompiledProfile) Preview(request model.MockRequest) (model.MockPreview, error) {
	if err := validatePreviewRequest(request); err != nil {
		return model.MockPreview{}, err
	}
	query := make(url.Values, len(request.Query))
	for key, values := range request.Query {
		query[key] = append([]string{}, values...)
	}
	route, err := c.Match(request.Method, request.Path, query)
	if err != nil {
		return model.MockPreview{Status: http.StatusNotImplemented}, nil
	}
	return model.MockPreview{Matched: true, Route: route.Name, Status: route.Status, Headers: cloneStringMap(route.Headers), Body: route.Body, DelayMS: route.DelayMS}, nil
}

func validatePreviewRequest(request model.MockRequest) error {
	method := strings.TrimSpace(request.Method)
	if method == "" || !validHTTPToken(method) {
		return errors.New("preview method must be an HTTP token")
	}
	if request.Path == "" || !strings.HasPrefix(request.Path, "/") || strings.ContainsAny(request.Path, "?#") {
		return errors.New("preview path must be an absolute URL path without a query or fragment")
	}
	for name := range request.Query {
		if strings.TrimSpace(name) == "" {
			return errors.New("preview query names cannot be empty")
		}
	}
	headerNames := map[string]struct{}{}
	for name, values := range request.Headers {
		if strings.TrimSpace(name) != name || !validHTTPToken(name) {
			return errors.New("preview header names must be valid HTTP field names")
		}
		canonicalName := strings.ToLower(name)
		if _, exists := headerNames[canonicalName]; exists {
			return fmt.Errorf("preview header %s is duplicated with different casing", name)
		}
		headerNames[canonicalName] = struct{}{}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("preview header %s contains a line break", name)
			}
		}
	}
	if len(request.Body) > MaxPreviewRequestBodyBytes {
		return fmt.Errorf("preview request body exceeds %d bytes", MaxPreviewRequestBodyBytes)
	}
	return nil
}

func compileRoute(route model.MockRoute) (compiledRoute, error) {
	if err := model.ValidateArtifactName(route.Name); err != nil {
		return compiledRoute{}, fmt.Errorf("invalid name: %w", err)
	}
	route.Method = strings.ToUpper(strings.TrimSpace(route.Method))
	if route.Method == "" || !validHTTPToken(route.Method) {
		return compiledRoute{}, errors.New("method must be an HTTP token")
	}
	if route.Path == "" || !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?#") {
		return compiledRoute{}, errors.New("path must be an absolute URL path without a query or fragment")
	}
	if route.Status < 200 || http.StatusText(route.Status) == "" {
		return compiledRoute{}, errors.New("status must be a registered final HTTP response status")
	}
	if route.DelayMS < 0 || route.DelayMS > 300_000 {
		return compiledRoute{}, errors.New("delayMs must be between 0 and 300000")
	}
	segments := splitPath(route.Path)
	literals := 0
	parameters := map[string]struct{}{}
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") || strings.HasSuffix(segment, "}") {
			if !pathParameter.MatchString(segment) {
				return compiledRoute{}, fmt.Errorf("path segment %q is not a valid {parameter}", segment)
			}
			name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}"))
			if _, exists := parameters[name]; exists {
				return compiledRoute{}, fmt.Errorf("path parameter %s is duplicated", name)
			}
			parameters[name] = struct{}{}
			continue
		}
		literals++
	}
	for key := range route.Query {
		if strings.TrimSpace(key) == "" {
			return compiledRoute{}, errors.New("query matcher names cannot be empty")
		}
	}
	headerNames := map[string]struct{}{}
	for key, value := range route.Headers {
		if strings.TrimSpace(key) != key || !validHTTPToken(key) {
			return compiledRoute{}, errors.New("response header names must be valid HTTP field names")
		}
		canonicalName := strings.ToLower(key)
		if _, exists := headerNames[canonicalName]; exists {
			return compiledRoute{}, fmt.Errorf("response header %s is duplicated with different casing", key)
		}
		headerNames[canonicalName] = struct{}{}
		switch canonicalName {
		case "connection", "content-length", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
			return compiledRoute{}, fmt.Errorf("response header %s is managed by the HTTP transport", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return compiledRoute{}, fmt.Errorf("response header %s contains a line break", key)
		}
	}
	return compiledRoute{route: route, segments: segments, literalCount: literals, queryCount: len(route.Query)}, nil
}

func validHTTPToken(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= '0' && character <= '9') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			strings.IndexByte("!#$%&'*+-.^_`|~", character) >= 0 {
			continue
		}
		return false
	}
	return true
}

func splitPath(path string) []string {
	if path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}

func matchesQuery(required map[string]string, actual url.Values) bool {
	for key, expected := range required {
		values, exists := actual[key]
		if !exists {
			return false
		}
		if expected == "" {
			continue
		}
		found := false
		for _, value := range values {
			if value == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func routesAreAmbiguous(left, right compiledRoute) bool {
	if left.route.Method != right.route.Method || len(left.segments) != len(right.segments) ||
		left.literalCount != right.literalCount || left.queryCount != right.queryCount {
		return false
	}
	for index := range left.segments {
		leftParameter := pathParameter.MatchString(left.segments[index])
		rightParameter := pathParameter.MatchString(right.segments[index])
		if !leftParameter && !rightParameter && left.segments[index] != right.segments[index] {
			return false
		}
	}
	for key, leftValue := range left.route.Query {
		if rightValue, exists := right.route.Query[key]; exists && leftValue != "" && rightValue != "" && leftValue != rightValue {
			return false
		}
	}
	return true
}

func cloneProfile(profile model.MockProfile) model.MockProfile {
	profile.Routes = append([]model.MockRoute(nil), profile.Routes...)
	for index := range profile.Routes {
		profile.Routes[index] = cloneRoute(profile.Routes[index])
	}
	return profile
}

func cloneRoute(route model.MockRoute) model.MockRoute {
	route.Query = cloneStringMap(route.Query)
	route.Headers = cloneStringMap(route.Headers)
	return route
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}
