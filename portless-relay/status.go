package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/system/installation"
)

// InstallationStatus is the complete inspectable state of the platform relay,
// including ownership, installed artifacts, and end-to-end health.
type InstallationStatus struct {
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
	Name                  string
	Service               string
	HelperPath            string
	ConfigurationPath     string
	ReceiptPath           string
	ResolverPath          string
	LocalhostResolverPath string
	ArtifactUID           int
	ArtifactGID           int
}

type inspectionProbes struct {
	http     func(context.Context) error
	dns      func(context.Context) error
	resolver func(context.Context) error
}

func defaultInspectionProbes() inspectionProbes {
	return inspectionProbes{http: Check, dns: CheckDNS, resolver: CheckResolver}
}

// Inspect discovers installed relay artifacts, ownership, service state,
// endpoint-pool readiness, and HTTP and DNS health without changing the host.
func Inspect(ctx context.Context) (InstallationStatus, error) {
	return inspect(ctx, newHostPlatform(), defaultInspectionProbes())
}

func inspectInstallation(ctx context.Context, platform hostPlatform) (InstallationStatus, error) {
	return inspect(ctx, platform, inspectionProbes{})
}

func inspect(ctx context.Context, platform hostPlatform, probes inspectionProbes) (InstallationStatus, error) {
	details := platform.installation()
	status := InstallationStatus{
		Platform: details.Name, Service: details.Service, HelperPath: details.HelperPath,
		ConfigurationPath: details.ConfigurationPath, ReceiptPath: details.ReceiptPath,
		ResolverPath: details.ResolverPath, LocalhostResolverPath: details.LocalhostResolverPath,
		DNSListenAddress: DefaultDNSAddress,
	}
	helperPresent, helperErr := inspectArtifact(details.HelperPath, 0o755, details.ArtifactUID, details.ArtifactGID)
	configurationPresent, configurationErr := inspectArtifact(details.ConfigurationPath, 0o644, details.ArtifactUID, details.ArtifactGID)
	receiptPresent, receiptArtifactErr := inspectArtifact(details.ReceiptPath, 0o644, details.ArtifactUID, details.ArtifactGID)
	for _, artifactErr := range []error{helperErr, configurationErr, receiptArtifactErr} {
		if artifactErr != nil {
			status.Problem = appendProblem(status.Problem, artifactErr.Error())
		}
	}
	resolverPaths := details.resolverPaths()
	resolverPresent := len(resolverPaths) > 0
	resolverArtifactPresent := false
	for _, resolverPath := range resolverPaths {
		present, resolverErr := inspectArtifact(resolverPath, 0o644, details.ArtifactUID, details.ArtifactGID)
		if resolverErr != nil {
			status.Problem = appendProblem(status.Problem, resolverErr.Error())
		}
		resolverPresent = resolverPresent && present
		resolverArtifactPresent = resolverArtifactPresent || present
	}
	status.HelperPresent = helperPresent
	if helperPresent && helperErr == nil {
		helperBuildID, currentBuildID, current, buildErr := inspectHelperBuild(details.HelperPath)
		status.HelperBuildID = helperBuildID
		status.CurrentBuildID = currentBuildID
		status.HelperCurrent = current
		if buildErr != nil {
			status.Problem = appendProblem(status.Problem, buildErr.Error())
		}
	}
	status.ConfigurationPresent = configurationPresent
	status.ReceiptPresent = receiptPresent
	status.ResolverPresent = resolverPresent
	status.Installed = helperPresent || configurationPresent || receiptPresent || resolverArtifactPresent
	if receiptPresent && receiptArtifactErr == nil {
		receipt, receiptErr := readInstallationReceipt(details)
		if receiptErr != nil {
			status.Problem = appendProblem(status.Problem, receiptErr.Error())
		} else {
			status.OwnerUID, status.OwnerGID = receipt.OwnerUID, receipt.OwnerGID
			status.TargetSocket, status.DNSTargetSocket = receipt.TargetSocket, receipt.DNSTargetSocket
			installedAt := receipt.InstalledAt
			status.InstalledAt = &installedAt
			if !receiptUsesCurrentLoopbackPool(receipt) {
				status.Problem = appendProblem(status.Problem, "installation receipt uses an outdated loopback address pool; run `portless setup` to repair it")
			}
		}
	}
	serviceState, serviceErr := platform.serviceState(ctx)
	if serviceErr != nil {
		return status, serviceErr
	}
	if serviceState == relayServiceUnknown {
		return status, errors.New("relay service state is unknown")
	}
	status.Running = serviceState == relayServiceRunning
	status.Installed = status.Installed || status.Running
	if status.Installed && !receiptPresent {
		status.Problem = appendProblem(status.Problem, "installation receipt is missing; relay ownership cannot be verified")
	}
	poolReady, poolDetail, poolErr := platform.loopbackPoolStatus()
	status.EndpointPoolReady = poolReady
	status.EndpointPoolDetail = poolDetail
	if poolErr != nil {
		status.Problem = appendProblem(status.Problem, "inspect TCP endpoint address pool: "+poolErr.Error())
	}
	if status.Installed && probes.http != nil && probes.dns != nil && probes.resolver != nil {
		healthContext, cancelHealth := context.WithTimeout(ctx, 1500*time.Millisecond)
		health := inspectRelayHealth(healthContext, probes)
		cancelHealth()
		httpErr, dnsErr, resolverErr := health.httpErr, health.dnsErr, health.resolverErr
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
		artifactsHealthy := helperPresent && configurationPresent && receiptPresent && status.OwnerUID > 0 && resolverPresent && status.Problem == ""
		pathsHealthy := httpErr == nil && dnsErr == nil && resolverErr == nil && poolReady && poolErr == nil
		status.Healthy = status.Running && artifactsHealthy && pathsHealthy
	}
	return status, nil
}

func inspectArtifact(path string, expectedMode os.FileMode, expectedUID, expectedGID int) (bool, error) {
	if path == "" {
		return false, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect relay artifact %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return true, fmt.Errorf("relay artifact %s must be a regular file, not a symlink", path)
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return true, fmt.Errorf("relay artifact %s has unsafe special permission bits", path)
	}
	ownerUID, ownerGID, ok := artifactOwner(info)
	if !ok {
		return true, fmt.Errorf("relay artifact ownership is unavailable for %s", path)
	}
	if ownerUID != expectedUID || ownerGID != expectedGID {
		return true, fmt.Errorf("relay artifact %s belongs to UID %d and GID %d, expected UID %d and GID %d", path, ownerUID, ownerGID, expectedUID, expectedGID)
	}
	if info.Mode().Perm() != expectedMode.Perm() {
		return true, fmt.Errorf("relay artifact %s has mode %04o, expected %04o", path, info.Mode().Perm(), expectedMode.Perm())
	}
	return true, nil
}

func artifactOwner(info os.FileInfo) (int, int, bool) {
	metadata, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(metadata.Uid), int(metadata.Gid), true
}

func (details platformInstallation) resolverPaths() []string {
	paths := make([]string, 0, 2)
	if details.ResolverPath != "" {
		paths = append(paths, details.ResolverPath)
	}
	if details.LocalhostResolverPath != "" {
		paths = append(paths, details.LocalhostResolverPath)
	}
	return paths
}

func inspectHelperBuild(helperPath string) (helperBuildID, currentBuildID string, current bool, err error) {
	helperBuildID, err = installation.BuildIDForPath(helperPath)
	if err != nil {
		return "", "", false, fmt.Errorf("fingerprint installed relay helper: %w", err)
	}
	currentBuildID, err = installation.CurrentBuildID()
	if err != nil {
		return helperBuildID, "", false, fmt.Errorf("fingerprint current Portless executable: %w", err)
	}
	return helperBuildID, currentBuildID, helperBuildID == currentBuildID, nil
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
