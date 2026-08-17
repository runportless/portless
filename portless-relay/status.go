package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// InstallationStatus is the complete inspectable state of the platform relay,
// including ownership, installed artifacts, and end-to-end health.
type InstallationStatus struct {
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

// State returns the aggregate human-readable relay installation state.
func (status InstallationStatus) State() string {
	switch {
	case !status.Installed:
		return "not installed"
	case status.Healthy:
		return "ready"
	case status.Running:
		return "running; not ready"
	default:
		return "installed; service stopped"
	}
}

type platformInstallation struct {
	Name              string
	Service           string
	HelperPath        string
	ConfigurationPath string
	ReceiptPath       string
	ResolverPath      string
}

// Inspect discovers installed relay artifacts, ownership, service state,
// endpoint-pool readiness, and HTTP and DNS health without changing the host.
func Inspect(ctx context.Context) (InstallationStatus, error) {
	details := currentPlatformInstallation()
	status := InstallationStatus{
		Platform: details.Name, Service: details.Service, HelperPath: details.HelperPath,
		ConfigurationPath: details.ConfigurationPath, ReceiptPath: details.ReceiptPath,
		ResolverPath: details.ResolverPath, DNSListenAddress: DefaultDNSAddress,
	}
	helperPresent, err := pathExists(details.HelperPath)
	if err != nil {
		return status, fmt.Errorf("inspect relay helper: %w", err)
	}
	configurationPresent, err := pathExists(details.ConfigurationPath)
	if err != nil {
		return status, fmt.Errorf("inspect relay service configuration: %w", err)
	}
	receiptPresent, err := pathExists(details.ReceiptPath)
	if err != nil {
		return status, fmt.Errorf("inspect relay installation receipt: %w", err)
	}
	resolverPresent, err := pathExists(details.ResolverPath)
	if err != nil {
		return status, fmt.Errorf("inspect Portless resolver configuration: %w", err)
	}
	status.HelperPresent = helperPresent
	status.ConfigurationPresent = configurationPresent
	status.ReceiptPresent = receiptPresent
	status.ResolverPresent = resolverPresent
	status.Installed = helperPresent || configurationPresent || receiptPresent || resolverPresent
	if receiptPresent {
		receipt, receiptErr := readInstallationReceipt(details)
		if receiptErr != nil {
			status.Problem = receiptErr.Error()
		} else {
			status.OwnerUID, status.OwnerGID = receipt.OwnerUID, receipt.OwnerGID
			status.TargetSocket, status.DNSTargetSocket = receipt.TargetSocket, receipt.DNSTargetSocket
			installedAt := receipt.InstalledAt
			status.InstalledAt = &installedAt
		}
	} else if configurationPresent {
		uid, gid, socket, dnsSocket, fallbackErr := platformConfigurationOwner(details.ConfigurationPath)
		if fallbackErr != nil {
			status.Problem = "installation receipt is missing and the service owner could not be determined: " + fallbackErr.Error()
		} else {
			status.OwnerUID, status.OwnerGID, status.TargetSocket, status.DNSTargetSocket = uid, gid, socket, dnsSocket
		}
	}
	running, runningErr := platformServiceRunning(ctx)
	if runningErr != nil {
		status.Problem = appendProblem(status.Problem, runningErr.Error())
	}
	status.Running = running
	status.Installed = status.Installed || running
	poolReady, poolDetail, poolErr := relayLoopbackPoolStatus()
	status.EndpointPoolReady = poolReady
	status.EndpointPoolDetail = poolDetail
	if poolErr != nil {
		status.Problem = appendProblem(status.Problem, "inspect TCP endpoint address pool: "+poolErr.Error())
	}
	if status.Installed {
		httpErr := Check(ctx)
		dnsErr := CheckDNS(ctx)
		resolverContext, cancelResolver := context.WithTimeout(ctx, 1500*time.Millisecond)
		resolverErr := CheckResolver(resolverContext)
		cancelResolver()
		if httpErr != nil {
			status.HealthError = httpErr.Error()
		} else {
			status.HTTPHealthy = true
		}
		if dnsErr == nil {
			status.DNSHealthy = true
		} else {
			status.DNSHealthError = dnsErr.Error()
		}
		if resolverErr == nil {
			status.ResolverHealthy = true
		} else {
			status.ResolverHealthError = resolverErr.Error()
		}
		status.Healthy = httpErr == nil && dnsErr == nil && resolverErr == nil && resolverPresent && poolReady && poolErr == nil
	}
	return status, nil
}

func pathExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func appendProblem(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	return current + "; " + next
}
