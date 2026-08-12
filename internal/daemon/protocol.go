package daemon

import "time"

const (
	Product         = "portless"
	ProtocolVersion = "2"
	IdentityPath    = "/_portless/daemon/v1/identity"
	ShutdownPath    = "/_portless/daemon/v1/shutdown"
)

type Identity struct {
	Product            string    `json:"product"`
	ProtocolVersion    string    `json:"protocolVersion"`
	APIVersion         string    `json:"apiVersion"`
	InstallationID     string    `json:"installationId"`
	InstanceID         string    `json:"instanceId"`
	BuildID            string    `json:"buildId"`
	PID                int       `json:"pid"`
	StartedAt          time.Time `json:"startedAt"`
	State              string    `json:"state"`
	HandoffReady       bool      `json:"handoffReady"`
	RecoveryProblems   []string  `json:"recoveryProblems"`
	ActiveEnvironments []string  `json:"activeEnvironments"`
}

type ShutdownRequest struct {
	InstanceID string `json:"instanceId"`
	Force      bool   `json:"force"`
	Handoff    bool   `json:"handoff,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ShutdownResponse struct {
	Stopping           bool     `json:"stopping"`
	Handoff            bool     `json:"handoff,omitempty"`
	InstanceID         string   `json:"instanceId"`
	ActiveEnvironments []string `json:"activeEnvironments"`
}

type ErrorResponse struct {
	Error Error `json:"error"`
}

type Error struct {
	Code               string   `json:"code"`
	Message            string   `json:"message"`
	ActiveEnvironments []string `json:"activeEnvironments,omitempty"`
	Problems           []string `json:"problems,omitempty"`
}

type LifecycleError struct {
	Code               string
	Message            string
	ActiveEnvironments []string
	Problems           []string
}

func (e *LifecycleError) Error() string {
	return e.Message
}
