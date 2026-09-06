package mocks

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/runportless/portless/portless-daemon/model"
)

// response constructs the fixed response shared by the private HTTP runtime
// and preview. The runtime leaves body suppression to net/http; preview applies
// those rules without opening a listener or waiting for the configured delay.
func (c *CompiledScenario) response(service, method, path string, query url.Values, requestTarget string) model.MockPreview {
	result := model.MockPreview{Service: service, Headers: map[string]string{ScenarioHeader: c.scenario.Name}}
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
	result.Matched, result.Route, result.Status = true, route.Name, route.Status
	result.Body, result.DelayMS = route.Body, route.DelayMS
	for name, value := range route.Headers {
		result.Headers[http.CanonicalHeaderKey(name)] = value
	}
	result.Headers[ScenarioHeader] = c.scenario.Name
	result.Headers[RouteHeader] = route.Name
	return result
}
