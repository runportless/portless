// Package mocks compiles and serves deterministic environment-scoped HTTP mocks.
package mocks

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
)

var pathParameter = regexp.MustCompile(`^\{[A-Za-z_][A-Za-z0-9_]*\}$`)

const (
	// MaxRoutesPerScenario bounds matcher compilation and persisted scenario size.
	MaxRoutesPerScenario = 1000
	// MaxResponseBodyBytes bounds one fixed mock response.
	MaxResponseBodyBytes = 1 << 20
	// MaxScenarioBodyBytes bounds all fixed response bodies retained by one scenario.
	MaxScenarioBodyBytes = 8 << 20
	// MaxPreviewRequestBodyBytes bounds a side-effect-free preview request body.
	MaxPreviewRequestBodyBytes = 256 << 10
	// MaxQueryPatternBytes bounds one query regular expression before compilation.
	MaxQueryPatternBytes = 4096
	// MaxScenarioQueryPatternBytes bounds total query regex compilation per scenario.
	MaxScenarioQueryPatternBytes = 256 << 10
)

// ErrNoMatch indicates that no enabled route accepts a request.
var ErrNoMatch = errors.New("no mock route matched the request")

type compiledRoute struct {
	route            model.MockRoute
	segments         []string
	literalCount     int
	queryCount       int
	queryEqualsCount int
	queryRegexCount  int
	queryPatterns    map[string]*regexp.Regexp
}

// CompiledScenario is an immutable, validated matcher ready for concurrent use.
type CompiledScenario struct {
	scenario model.MockScenario
	routes   []compiledRoute
}

// Compile validates a scenario and orders its routes by deterministic specificity.
func Compile(scenario model.MockScenario) (*CompiledScenario, error) {
	if err := model.ValidateArtifactName(scenario.Name); err != nil {
		return nil, fmt.Errorf("invalid mock scenario name: %w", err)
	}
	if len(scenario.Routes) > MaxRoutesPerScenario {
		return nil, fmt.Errorf("mock scenarios cannot contain more than %d routes", MaxRoutesPerScenario)
	}
	scenario = cloneScenario(scenario)
	result := &CompiledScenario{scenario: scenario}
	seen := map[string]struct{}{}
	totalBodyBytes := 0
	totalPatternBytes := 0
	for _, route := range scenario.Routes {
		if err := model.ValidateServiceName(route.Service); err != nil {
			return nil, fmt.Errorf("route %s has invalid service: %w", route.Name, err)
		}
		if len(route.Body) > MaxResponseBodyBytes {
			return nil, fmt.Errorf("route %s response body exceeds %d bytes", route.Name, MaxResponseBodyBytes)
		}
		totalBodyBytes += len(route.Body)
		if totalBodyBytes > MaxScenarioBodyBytes {
			return nil, fmt.Errorf("mock scenario response bodies exceed %d bytes", MaxScenarioBodyBytes)
		}
		for _, matcher := range route.Query {
			if matcher.Match == "regex" {
				totalPatternBytes += len(matcher.Value)
			}
		}
		if totalPatternBytes > MaxScenarioQueryPatternBytes {
			return nil, fmt.Errorf("mock scenario query regex patterns exceed %d bytes", MaxScenarioQueryPatternBytes)
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
			if strings.EqualFold(result.routes[left].route.Service, result.routes[right].route.Service) && routesAreAmbiguous(result.routes[left], result.routes[right]) {
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
		if left.queryEqualsCount != right.queryEqualsCount {
			return left.queryEqualsCount > right.queryEqualsCount
		}
		if left.queryRegexCount != right.queryRegexCount {
			return left.queryRegexCount > right.queryRegexCount
		}
		if len(left.segments) != len(right.segments) {
			return len(left.segments) > len(right.segments)
		}
		return strings.ToLower(left.route.Name) < strings.ToLower(right.route.Name)
	})
	return result, nil
}

// Scenario returns a copy of the scenario represented by the matcher.
func (c *CompiledScenario) Scenario() model.MockScenario {
	return cloneScenario(c.scenario)
}

// Match returns the most specific route that accepts the request.
func (c *CompiledScenario) Match(service, method, path string, query url.Values) (model.MockRoute, error) {
	method = strings.ToUpper(method)
	segments := splitPath(path)
	for _, candidate := range c.routes {
		if !strings.EqualFold(candidate.route.Service, service) || !candidate.route.Enabled || candidate.route.Method != method || len(candidate.segments) != len(segments) {
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
		if !matched || !candidate.matchesQuery(query) {
			continue
		}
		return cloneRoute(candidate.route), nil
	}
	return model.MockRoute{}, ErrNoMatch
}

// Preview evaluates a request without traffic or delay, returning the mock-owned
// response headers and body after HTTP body suppression. Transport-added headers
// such as Date and Content-Length are not synthesized.
func (c *CompiledScenario) Preview(request model.MockRequest) (model.MockPreview, error) {
	if err := validatePreviewRequest(request); err != nil {
		return model.MockPreview{}, err
	}
	query := make(url.Values, len(request.Query))
	for key, values := range request.Query {
		query[key] = append([]string{}, values...)
	}
	target := (&url.URL{Path: request.Path, RawQuery: query.Encode()}).RequestURI()
	result := c.response(request.Service, request.Method, request.Path, query, target)
	if strings.EqualFold(request.Method, http.MethodHead) || result.Status == http.StatusNoContent || result.Status == http.StatusNotModified {
		result.Body = ""
	}
	if result.Status == http.StatusNotModified {
		// net/http removes Content-Type when writing a 304 response.
		delete(result.Headers, "Content-Type")
	}
	return result, nil
}

func validatePreviewRequest(request model.MockRequest) error {
	if err := model.ValidateServiceName(request.Service); err != nil {
		return fmt.Errorf("preview service is invalid: %w", err)
	}
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
	compiled := compiledRoute{route: route, segments: segments, literalCount: literals, queryCount: len(route.Query), queryPatterns: map[string]*regexp.Regexp{}}
	for key, matcher := range route.Query {
		if strings.TrimSpace(key) == "" {
			return compiledRoute{}, errors.New("query matcher names cannot be empty")
		}
		switch matcher.Match {
		case "exists":
			if matcher.Value != "" {
				return compiledRoute{}, fmt.Errorf("query parameter %s: Exists cannot have a value", key)
			}
		case "equals":
			if matcher.Value == "" {
				return compiledRoute{}, fmt.Errorf("query parameter %s: enter a value or choose Exists", key)
			}
			compiled.queryEqualsCount++
		case "regex":
			if matcher.Value == "" || len(matcher.Value) > MaxQueryPatternBytes {
				return compiledRoute{}, fmt.Errorf("query parameter %s: regex must contain 1 to %d bytes", key, MaxQueryPatternBytes)
			}
			if _, err := syntax.Parse(matcher.Value, syntax.Perl); err != nil {
				return compiledRoute{}, fmt.Errorf("query parameter %s has an invalid regex: %w", key, err)
			}
			pattern, err := regexp.Compile(`\A(?:` + matcher.Value + `)\z`)
			if err != nil {
				return compiledRoute{}, fmt.Errorf("query parameter %s has an invalid regex: %w", key, err)
			}
			compiled.queryPatterns[key] = pattern
			compiled.queryRegexCount++
		default:
			return compiledRoute{}, fmt.Errorf("query parameter %s: match must be equals, exists, or regex", key)
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
	return compiled, nil
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

func (route compiledRoute) matchesQuery(actual url.Values) bool {
	for key, expected := range route.route.Query {
		values, exists := actual[key]
		if !exists {
			return false
		}
		if expected.Match == "exists" {
			continue
		}
		found := false
		for _, value := range values {
			if (expected.Match == "equals" && value == expected.Value) || (expected.Match == "regex" && route.queryPatterns[key].MatchString(value)) {
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
		left.literalCount != right.literalCount || left.queryCount != right.queryCount ||
		left.queryEqualsCount != right.queryEqualsCount || left.queryRegexCount != right.queryRegexCount {
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
		if rightValue, exists := right.route.Query[key]; exists && leftValue.Match == "equals" && rightValue.Match == "equals" && leftValue.Value != rightValue.Value {
			return false
		}
	}
	return true
}

func cloneScenario(scenario model.MockScenario) model.MockScenario {
	scenario.Routes = append([]model.MockRoute(nil), scenario.Routes...)
	scenario.Activation.TargetServices = append([]string(nil), scenario.Activation.TargetServices...)
	scenario.Activation.ActiveServices = append([]string(nil), scenario.Activation.ActiveServices...)
	for index := range scenario.Routes {
		scenario.Routes[index] = cloneRoute(scenario.Routes[index])
	}
	return scenario
}

func cloneRoute(route model.MockRoute) model.MockRoute {
	route.Query = maps.Clone(route.Query)
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
