package contract

// EnvironmentList is a bounded collection of environments and its total size.
type EnvironmentList struct {
	Environments []Environment `json:"environments"`
	Total        int           `json:"total,omitempty"`
}

// CloneEnvironmentRequest identifies a project environment to clone and the
// new environment name.
type CloneEnvironmentRequest struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	From    string `json:"from"`
}

// EnvironmentContext describes the environment resolved for a filesystem path.
type EnvironmentContext struct {
	Resolution  string        `json:"resolution"`
	Environment *Environment  `json:"environment,omitempty"`
	Candidates  []Environment `json:"candidates"`
}

// SelectEnvironmentRequest binds a filesystem path to an explicit environment.
type SelectEnvironmentRequest struct {
	Path        string `json:"path"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
}

// ClearEnvironmentSelectionResponse reports whether a saved selection existed.
type ClearEnvironmentSelectionResponse struct {
	Cleared bool `json:"cleared"`
}

// UpRequest configures debug ownership when starting an environment.
type UpRequest struct {
	DebugServices []string `json:"debugServices,omitempty"`
	Managed       bool     `json:"managed,omitempty"`
}

// DownRequest controls whether shutdown also removes managed volumes.
type DownRequest struct {
	RemoveVolumes bool `json:"removeVolumes"`
}

// EnvironmentMutation returns an updated environment and discovery warnings.
type EnvironmentMutation struct {
	Environment Environment `json:"environment"`
	Warnings    []string    `json:"warnings"`
}

// SetSourceCheckoutRequest replaces the filesystem path for one project source
// in a single environment.
type SetSourceCheckoutRequest struct {
	Path string `json:"path"`
}

// ServiceList is a collection of environment services.
type ServiceList struct {
	Services []Service `json:"services"`
}

// ConnectionList is a collection of effective service connections.
type ConnectionList struct {
	Connections []EffectiveConnection `json:"connections"`
}

// LogList contains filtered runtime log entries and their effective scope.
type LogList struct {
	Project     string     `json:"project,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Service     string     `json:"service,omitempty"`
	Entries     []LogEntry `json:"entries"`
}

// TimelineList is a collection of durable environment timeline events.
type TimelineList struct {
	Timeline []TimelineEvent `json:"timeline"`
}

// OperationList is a collection of asynchronous environment operations.
type OperationList struct {
	Operations []Operation `json:"operations"`
}

// MockProfileList is a collection of environment-scoped mock profiles.
type MockProfileList struct {
	Mocks []MockProfile `json:"mocks"`
}

// CreateMockRequest creates a profile, optionally deriving routes from a recording.
type CreateMockRequest struct {
	Name            string `json:"name"`
	Service         string `json:"service"`
	Description     string `json:"description,omitempty"`
	FromRecording   string `json:"fromRecording,omitempty"`
	OpenAPIDocument string `json:"openapiDocument,omitempty"`
}

// MockMutation returns an updated profile and non-fatal import warnings.
type MockMutation struct {
	Mock     MockProfile `json:"mock"`
	Warnings []string    `json:"warnings"`
}
