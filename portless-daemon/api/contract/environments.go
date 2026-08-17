package contract

type EnvironmentList struct {
	Environments []Environment `json:"environments"`
	Total        int           `json:"total,omitempty"`
}

type CloneEnvironmentRequest struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	From    string `json:"from"`
}

type EnvironmentContext struct {
	Resolution  string        `json:"resolution"`
	Environment *Environment  `json:"environment,omitempty"`
	Candidates  []Environment `json:"candidates"`
}

type SelectEnvironmentRequest struct {
	Path        string `json:"path"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
}

type ClearEnvironmentSelectionResponse struct {
	Cleared bool `json:"cleared"`
}

type UpRequest struct {
	DebugServices []string `json:"debugServices,omitempty"`
	Managed       bool     `json:"managed,omitempty"`
}

type DownRequest struct {
	RemoveVolumes bool `json:"removeVolumes"`
}

type EnvironmentMutation struct {
	Environment Environment `json:"environment"`
	Warnings    []string    `json:"warnings"`
}

type SetSourceRequest struct {
	Path string `json:"path"`
}

type ServiceList struct {
	Services []Service `json:"services"`
}

type ConnectionList struct {
	Connections []EffectiveConnection `json:"connections"`
}

type LogList struct {
	Project     string     `json:"project,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Service     string     `json:"service,omitempty"`
	Entries     []LogEntry `json:"entries"`
}

type TimelineList struct {
	Timeline []TimelineEvent `json:"timeline"`
}

type OperationList struct {
	Operations []Operation `json:"operations"`
}
