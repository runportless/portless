package mocks

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/runportless/portless/portless-daemon/model"
)

// Response holds a selected fixed response and trusted route attribution.
type Response struct {
	model.MockResponse
	Scenario string
	Route    string
	Matched  bool
}

// MatchResponse selects a fixed response, returning ErrNoMatch for an unmatched request.
func (c *CompiledScenario) MatchResponse(service, method, path string, query url.Values) (Response, error) {
	route, err := c.Match(service, method, path, query)
	if err != nil {
		return Response{}, err
	}
	return c.routeResponse(route), nil
}

func (c *CompiledScenario) routeResponse(route model.MockRoute) Response {
	result := Response{Scenario: c.scenario.Name, Route: route.Name, Matched: true, MockResponse: model.MockResponse{Status: route.Status, Body: route.Body, DelayMS: route.DelayMS, Headers: map[string]string{}}}
	for name, value := range route.Headers {
		result.Headers[http.CanonicalHeaderKey(name)] = value
	}
	result.Headers[ScenarioHeader] = c.scenario.Name
	result.Headers[RouteHeader] = route.Name
	return result
}

// response constructs the fixed response shared by the private HTTP runtime
// and preview. The runtime leaves body suppression to net/http; preview applies
// those rules without opening a listener or waiting for the configured delay.
func (c *CompiledScenario) response(service, method, path string, query url.Values, requestTarget string) Response {
	result := Response{Scenario: c.scenario.Name, MockResponse: model.MockResponse{Headers: map[string]string{ScenarioHeader: c.scenario.Name}}}
	route, err := c.Match(service, method, path, query)
	if err != nil {
		result.Status = http.StatusNotImplemented
		result.Headers["Content-Type"] = "application/json"
		body, _ := json.Marshal(map[string]any{
			"error": map[string]string{
				"code":    "MOCK_ROUTE_NOT_MATCHED",
				"message": "No enabled route in mock scenario " + c.scenario.Name + " for " + service + " matched " + method + " " + requestTarget,
			},
		})
		result.Body = string(body) + "\n"
		return result
	}
	return c.routeResponse(route)
}
