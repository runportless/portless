// Package lifecycle owns the authenticated daemon identity and shutdown
// protocol shared by the daemon process, control clients, and API adapters.
package lifecycle

import "time"

const (
	// Product is the authenticated lifecycle product identifier.
	Product = "portless"
	// ProtocolVersion is the semantic version of the daemon lifecycle protocol.
	ProtocolVersion = "3.0.0"
	// IdentityPath is the private authenticated daemon identity route.
	IdentityPath = "/_portless/daemon/v1/identity"
	// ShutdownPath is the private authenticated daemon shutdown route.
	ShutdownPath = "/_portless/daemon/v1/shutdown"
)

// Identity proves the running daemon's installation, process, build,
// compatibility, recovery, and handoff state.
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

// ShutdownRequest identifies the intended daemon instance and shutdown policy.
type ShutdownRequest struct {
	InstanceID string `json:"instanceId"`
	Force      bool   `json:"force"`
	Handoff    bool   `json:"handoff,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ShutdownResponse acknowledges an accepted shutdown and its active environments.
type ShutdownResponse struct {
	Stopping           bool     `json:"stopping"`
	Handoff            bool     `json:"handoff,omitempty"`
	InstanceID         string   `json:"instanceId"`
	ActiveEnvironments []string `json:"activeEnvironments"`
}

// ErrorResponse is the lifecycle protocol's structured error envelope.
type ErrorResponse struct {
	Error Error `json:"error"`
}

// Error describes a lifecycle protocol failure and relevant active state.
type Error struct {
	Code               string   `json:"code"`
	Message            string   `json:"message"`
	ActiveEnvironments []string `json:"activeEnvironments,omitempty"`
	Problems           []string `json:"problems,omitempty"`
}

// LifecycleError is the in-process form of a structured lifecycle refusal.
type LifecycleError struct {
	Code               string
	Message            string
	ActiveEnvironments []string
	Problems           []string
}

// Error returns the lifecycle error message.
func (e *LifecycleError) Error() string {
	return e.Message
}
