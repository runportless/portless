package container

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/providers"
)

// RuntimeName identifies a supported container engine or automatic selection.
type RuntimeName string

const (
	// RuntimeAuto selects the first ready runtime according to configured order.
	RuntimeAuto RuntimeName = "auto"
	// RuntimeDocker selects Docker explicitly.
	RuntimeDocker RuntimeName = "docker"
	// RuntimePodman selects Podman explicitly.
	RuntimePodman RuntimeName = "podman"
)

// ParseRuntimeName normalizes and validates a runtime preference.
func ParseRuntimeName(value string) (RuntimeName, error) {
	name := RuntimeName(strings.ToLower(strings.TrimSpace(value)))
	switch name {
	case RuntimeAuto, RuntimeDocker, RuntimePodman:
		return name, nil
	default:
		return "", fmt.Errorf("container runtime must be auto, docker, or podman")
	}
}

// ProbeResult reports availability and version details for one runtime.
type ProbeResult struct {
	Name    RuntimeName `json:"name"`
	State   string      `json:"state"`
	Version string      `json:"version,omitempty"`
	Reason  string      `json:"reason,omitempty"`
}

// Status describes runtime preference, selection, and candidate probes.
type Status struct {
	Preference RuntimeName   `json:"preference"`
	Selected   RuntimeName   `json:"selected,omitempty"`
	State      string        `json:"state"`
	Version    string        `json:"version,omitempty"`
	Reason     string        `json:"reason,omitempty"`
	Candidates []ProbeResult `json:"candidates"`
}

// StartResult contains the adopted or newly started resource container state.
type StartResult struct {
	ContainerName string
	Port          int
	Environment   map[string]string
	StartedAt     time.Time
	LogDirectory  string
}

// RecoveryState classifies the persisted state of one managed resource container.
type RecoveryState string

const (
	// RecoveryRunning means the expected owned container is running and can be adopted.
	RecoveryRunning RecoveryState = "running"
	// RecoveryStopped means the expected owned container exists with matching identity but is stopped.
	RecoveryStopped RecoveryState = "stopped"
	// RecoveryMissing means the expected container is conclusively absent from the reachable persisted runtime.
	RecoveryMissing RecoveryState = "missing"
)

// RecoveryInspection describes a verified running, stopped, or missing managed container.
type RecoveryInspection struct {
	State         RecoveryState
	ContainerName string
	Port          int
}

// RecoveryResult contains one verified recovery classification and, when the
// container is running, the adopted runtime state produced from that same
// inspection.
type RecoveryResult struct {
	Inspection RecoveryInspection
	Start      *StartResult
}

// ResetResult counts installation-owned artifacts removed from a runtime.
type ResetResult struct {
	Runtime    RuntimeName `json:"runtime"`
	Containers int         `json:"containers"`
	Volumes    int         `json:"volumes"`
	Networks   int         `json:"networks"`
}

// Runtime manages Portless-owned resource containers in one container engine.
type Runtime interface {
	// Name returns the runtime's canonical name.
	Name() RuntimeName
	// Probe reports whether the runtime can currently accept commands.
	Probe(context.Context) ProbeResult
	// StartHost attempts to launch or activate the runtime host.
	StartHost(context.Context) ProbeResult
	// Start creates or adopts a managed resource container.
	Start(context.Context, string, string, model.ServiceDefinition, providers.ContainerPlan, int64, string) (StartResult, error)
	// StopEnvironment removes owned containers and optionally persistent volumes.
	StopEnvironment(context.Context, string, bool) error
	// StopService removes the owned container for one service.
	StopService(context.Context, string, string) error
	// ResetInstallation removes all resources owned by this Portless installation.
	ResetInstallation(context.Context) (ResetResult, error)
}

// Recoverer verifies and recovers a previously started managed container after
// daemon replacement without repeating its ownership inspection.
type Recoverer interface {
	// Recover classifies the persisted container and resumes observation when it is running.
	Recover(context.Context, string, model.ServiceDefinition, providers.ContainerPlan, int64, string, string) (RecoveryResult, error)
}

// Verifier checks that a persisted container still matches expected ownership and identity.
type Verifier interface {
	// Verify validates an existing managed container without adopting it.
	Verify(context.Context, string, model.ServiceDefinition, providers.ContainerPlan, int64, string) error
}

// Closer releases runtime-specific background resources.
type Closer interface {
	// Close stops background work owned by the runtime.
	Close()
}

// ErrRuntimeUnavailable indicates that no selected container engine is ready.
var ErrRuntimeUnavailable = errors.New("container runtime is unavailable")
