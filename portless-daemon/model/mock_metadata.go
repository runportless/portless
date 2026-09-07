package model

import "time"

// MockScenarioMetadata describes a scenario without loading its saved responses.
type MockScenarioMetadata struct {
	Project     string                 `json:"project"`
	Environment string                 `json:"environment"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	ModifiedAt  time.Time              `json:"modifiedAt"`
	Activation  MockScenarioActivation `json:"activation"`
	RouteCount  int                    `json:"routeCount"`
}

// MockRouteMetadata describes matching structure without saved application data.
type MockRouteMetadata struct {
	Name        string            `json:"name"`
	Service     string            `json:"service"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	QueryKinds  map[string]string `json:"queryKinds"`
	HeaderNames []string          `json:"headerNames"`
	Status      int               `json:"status"`
	DelayMS     int64             `json:"delayMs"`
	Enabled     bool              `json:"enabled"`
	BodyBytes   int64             `json:"bodyBytes"`
	CreatedAt   time.Time         `json:"createdAt"`
	ModifiedAt  time.Time         `json:"modifiedAt"`
}
