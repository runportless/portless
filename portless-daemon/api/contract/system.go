package contract

import (
	"fmt"
	"time"
)

// Health is the unauthenticated compatibility response used by relay checks.
type Health struct {
	Ready      bool   `json:"ready"`
	APIVersion string `json:"apiVersion"`
}

// SystemStatus identifies the running control plane and API version.
type SystemStatus struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	APIVersion string `json:"apiVersion"`
	Telemetry  bool   `json:"telemetry,omitempty"`
}

// DirectorySelectionRequest supplies an optional directory where the native
// operating-system chooser should begin.
type DirectorySelectionRequest struct {
	InitialPath string `json:"initialPath,omitempty"`
}

// DirectorySelection is the absolute directory selected through the native
// operating-system chooser.
type DirectorySelection struct {
	Path string `json:"path"`
}

// DaemonStatus describes the authenticated daemon process, compatibility,
// recovery, and active environments without performing runtime handoff probes.
type DaemonStatus struct {
	State              string    `json:"state"`
	PID                int       `json:"pid"`
	StartedAt          time.Time `json:"startedAt"`
	InstanceID         string    `json:"instanceId"`
	BuildID            string    `json:"buildId"`
	ProtocolVersion    string    `json:"protocolVersion"`
	APIVersion         string    `json:"apiVersion"`
	RecoveryProblems   []string  `json:"recoveryProblems"`
	ActiveEnvironments []string  `json:"activeEnvironments"`
}

// DaemonDiagnostics is one bounded operational snapshot collected for the
// authenticated daemon drawer.
type DaemonDiagnostics struct {
	CollectedAt time.Time              `json:"collectedAt"`
	Inventory   DaemonManagedInventory `json:"inventory"`
	Recovery    DaemonRecoveryStatus   `json:"recovery"`
	Build       DaemonBuildProvenance  `json:"build"`
	Storage     *DaemonStorageStatus   `json:"storage,omitempty"`
}

// DaemonManagedInventory counts active ownership-proven runtime resources.
type DaemonManagedInventory struct {
	Processes          int      `json:"processes"`
	Containers         int      `json:"containers"`
	ProxyListeners     int      `json:"proxyListeners"`
	ActiveEnvironments int      `json:"activeEnvironments"`
	Problems           []string `json:"problems"`
}

// DaemonRecoveryStatus describes the last startup reconciliation completed by
// the running daemon.
type DaemonRecoveryStatus struct {
	Result      string     `json:"result"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	DurationMS  int64      `json:"durationMs"`
	Recovered   int        `json:"recovered"`
	Problems    []string   `json:"problems"`
}

// DaemonBuildProvenance identifies the linked build and whether the running
// process matches its executable currently present on disk.
type DaemonBuildProvenance struct {
	Version        string `json:"version"`
	Distribution   string `json:"distribution"`
	Commit         string `json:"commit"`
	RunningBuildID string `json:"runningBuildId"`
	OnDiskBuildID  string `json:"onDiskBuildId,omitempty"`
	Current        bool   `json:"current"`
	Problem        string `json:"problem,omitempty"`
}

// DaemonStorageStatus summarizes retained data, fixed limits, and actual
// automatic-pruning timestamps without exposing filesystem paths.
type DaemonStorageStatus struct {
	DatabaseBytes                      int64      `json:"databaseBytes"`
	RecordingCount                     int64      `json:"recordingCount"`
	RecordedEventCount                 int64      `json:"recordedEventCount"`
	RecordedBytes                      int64      `json:"recordedBytes"`
	LiveTrafficExchanges               int        `json:"liveTrafficExchanges"`
	LiveTrafficBytes                   int64      `json:"liveTrafficBytes"`
	ServiceLogBytes                    int64      `json:"serviceLogBytes"`
	DaemonLogBytes                     int64      `json:"daemonLogBytes"`
	TrafficExchangeLimitPerEnvironment int        `json:"trafficExchangeLimitPerEnvironment"`
	TrafficPayloadLimitPerEnvironment  int64      `json:"trafficPayloadLimitPerEnvironment"`
	RecordingDefaultEventLimit         int64      `json:"recordingDefaultEventLimit"`
	RecordingMaximumEventLimit         int64      `json:"recordingMaximumEventLimit"`
	RecordingDefaultPayloadLimit       int64      `json:"recordingDefaultPayloadLimit"`
	RecordingMaximumPayloadLimit       int64      `json:"recordingMaximumPayloadLimit"`
	ServiceLogGenerationLimit          int        `json:"serviceLogGenerationLimit"`
	ServiceLogStreamLimitBytes         int64      `json:"serviceLogStreamLimitBytes"`
	TrafficPrunedAt                    *time.Time `json:"trafficPrunedAt,omitempty"`
	ServiceLogsPrunedAt                *time.Time `json:"serviceLogsPrunedAt,omitempty"`
	Problems                           []string   `json:"problems"`
}

// DaemonLogSnapshot is one bounded, safely redacted tail of the fixed daemon log.
type DaemonLogSnapshot struct {
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// DaemonHandoffStatus reports one completed live runtime-adoption audit.
type DaemonHandoffStatus struct {
	State              string    `json:"state"`
	VerifiedAt         time.Time `json:"verifiedAt"`
	Problems           []string  `json:"problems"`
	ActiveEnvironments []string  `json:"activeEnvironments"`
}

// DaemonRestart reports an accepted daemon restart and handoff strategy.
type DaemonRestart struct {
	Restarting         bool     `json:"restarting"`
	PreviousInstanceID string   `json:"previousInstanceId"`
	Handoff            bool     `json:"handoff"`
	ActiveEnvironments []string `json:"activeEnvironments"`
}

// DaemonRestartRequest identifies the daemon instance the caller intends to restart.
type DaemonRestartRequest struct {
	InstanceID string `json:"instanceId"`
}

// DaemonControlError is a structured refusal from a daemon lifecycle action.
type DaemonControlError struct {
	Code               string
	Message            string
	ActiveEnvironments []string
	Problems           []string
}

// Error returns the control error code and message.
func (e *DaemonControlError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// RelayStatus is the daemon API representation of relay installation and health.
type RelayStatus struct {
	Platform              string     `json:"platform"`
	Service               string     `json:"service"`
	Installed             bool       `json:"installed"`
	Running               bool       `json:"running"`
	Healthy               bool       `json:"healthy"`
	HTTPHealthy           bool       `json:"httpHealthy"`
	HelperPresent         bool       `json:"helperPresent"`
	HelperCurrent         bool       `json:"helperCurrent"`
	HelperBuildID         string     `json:"helperBuildId,omitempty"`
	CurrentBuildID        string     `json:"currentBuildId,omitempty"`
	ConfigurationPresent  bool       `json:"configurationPresent"`
	ReceiptPresent        bool       `json:"receiptPresent"`
	ResolverPresent       bool       `json:"resolverPresent"`
	ResolverHealthy       bool       `json:"resolverHealthy"`
	OwnerUID              int        `json:"ownerUid,omitempty"`
	OwnerGID              int        `json:"ownerGid,omitempty"`
	TargetSocket          string     `json:"targetSocket,omitempty"`
	DNSTargetSocket       string     `json:"dnsTargetSocket,omitempty"`
	DNSListenAddress      string     `json:"dnsListenAddress"`
	HelperPath            string     `json:"helperPath"`
	ConfigurationPath     string     `json:"configurationPath"`
	ReceiptPath           string     `json:"receiptPath"`
	ResolverPath          string     `json:"resolverPath,omitempty"`
	LocalhostResolverPath string     `json:"localhostResolverPath,omitempty"`
	InstalledAt           *time.Time `json:"installedAt,omitempty"`
	HealthError           string     `json:"healthError,omitempty"`
	DNSHealthy            bool       `json:"dnsHealthy"`
	DNSHealthError        string     `json:"dnsHealthError,omitempty"`
	ResolverHealthError   string     `json:"resolverHealthError,omitempty"`
	EndpointPoolReady     bool       `json:"endpointPoolReady"`
	EndpointPoolDetail    string     `json:"endpointPoolDetail,omitempty"`
	Problem               string     `json:"problem,omitempty"`
}
