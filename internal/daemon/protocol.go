package daemon

import "time"

const (
	Product         = "portless"
	ProtocolVersion = "1"
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
	ActiveEnvironments []string  `json:"activeEnvironments"`
}

type ShutdownRequest struct {
	InstanceID string `json:"instanceId"`
	Force      bool   `json:"force"`
	Reason     string `json:"reason,omitempty"`
}

type ShutdownResponse struct {
	Stopping           bool     `json:"stopping"`
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
}
