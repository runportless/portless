package contract

import (
	"fmt"
	"time"
)

type Health struct {
	Ready      bool   `json:"ready"`
	APIVersion string `json:"apiVersion"`
}

type SystemStatus struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	APIVersion string `json:"apiVersion"`
	Telemetry  bool   `json:"telemetry,omitempty"`
}

type DaemonStatus struct {
	State              string    `json:"state"`
	PID                int       `json:"pid"`
	StartedAt          time.Time `json:"startedAt"`
	InstanceID         string    `json:"instanceId"`
	BuildID            string    `json:"buildId"`
	ProtocolVersion    string    `json:"protocolVersion"`
	APIVersion         string    `json:"apiVersion"`
	HandoffReady       bool      `json:"handoffReady"`
	RecoveryProblems   []string  `json:"recoveryProblems"`
	ActiveEnvironments []string  `json:"activeEnvironments"`
}

type DaemonRestart struct {
	Restarting         bool     `json:"restarting"`
	PreviousInstanceID string   `json:"previousInstanceId"`
	Handoff            bool     `json:"handoff"`
	ActiveEnvironments []string `json:"activeEnvironments"`
}

type DaemonRestartRequest struct {
	InstanceID string `json:"instanceId"`
}

type DaemonControlError struct {
	Code               string
	Message            string
	ActiveEnvironments []string
	Problems           []string
}

func (e *DaemonControlError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type RelayStatus struct {
	Platform             string     `json:"platform"`
	Service              string     `json:"service"`
	Installed            bool       `json:"installed"`
	Running              bool       `json:"running"`
	Healthy              bool       `json:"healthy"`
	HTTPHealthy          bool       `json:"httpHealthy"`
	HelperPresent        bool       `json:"helperPresent"`
	ConfigurationPresent bool       `json:"configurationPresent"`
	ReceiptPresent       bool       `json:"receiptPresent"`
	ResolverPresent      bool       `json:"resolverPresent"`
	ResolverHealthy      bool       `json:"resolverHealthy"`
	OwnerUID             int        `json:"ownerUid,omitempty"`
	OwnerGID             int        `json:"ownerGid,omitempty"`
	TargetSocket         string     `json:"targetSocket,omitempty"`
	DNSTargetSocket      string     `json:"dnsTargetSocket,omitempty"`
	DNSListenAddress     string     `json:"dnsListenAddress"`
	HelperPath           string     `json:"helperPath"`
	ConfigurationPath    string     `json:"configurationPath"`
	ReceiptPath          string     `json:"receiptPath"`
	ResolverPath         string     `json:"resolverPath,omitempty"`
	InstalledAt          *time.Time `json:"installedAt,omitempty"`
	HealthError          string     `json:"healthError,omitempty"`
	DNSHealthy           bool       `json:"dnsHealthy"`
	DNSHealthError       string     `json:"dnsHealthError,omitempty"`
	ResolverHealthError  string     `json:"resolverHealthError,omitempty"`
	EndpointPoolReady    bool       `json:"endpointPoolReady"`
	EndpointPoolDetail   string     `json:"endpointPoolDetail,omitempty"`
	Problem              string     `json:"problem,omitempty"`
}
