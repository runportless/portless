package model

// ConfigurationPreview describes metadata affected by a guarded configuration change.
type ConfigurationPreview struct {
	Project            string                            `json:"project"`
	Environment        string                            `json:"environment,omitempty"`
	Source             string                            `json:"source,omitempty"`
	Expected           ResourceVersion                   `json:"expected"`
	Environments       []ConfigurationEnvironmentPreview `json:"environments"`
	RemovedServices    []string                          `json:"removedServices,omitempty"`
	RemovedConnections []Connection                      `json:"removedConnections,omitempty"`
	Blocked            []string                          `json:"blocked"`
	Retained           []string                          `json:"retained"`
}

// ConfigurationEnvironmentPreview lists public identities and retained data counts.
type ConfigurationEnvironmentPreview struct {
	Name           string            `json:"name"`
	Status         EnvironmentStatus `json:"status"`
	Revision       int64             `json:"revision"`
	Sources        []string          `json:"sources"`
	Services       []string          `json:"services"`
	Recordings     []string          `json:"recordings"`
	Faults         []string          `json:"faults"`
	MockScenarios  []string          `json:"mockScenarios"`
	MockRoutes     int               `json:"mockRoutes"`
	RecordedEvents int               `json:"recordedEvents"`
	Operations     int               `json:"operations"`
	TimelineEvents int               `json:"timelineEvents"`
}
