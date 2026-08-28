package installation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/runportless/portless/portless-daemon/system/installation"
	relayhealth "github.com/runportless/portless/portless-relay/health"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
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
	ConfigurationError    string     `json:"configurationError,omitempty"`
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
	EndpointPoolManaged   bool       `json:"endpointPoolManaged"`
	EndpointPoolResidual  bool       `json:"endpointPoolResidual,omitempty"`
	EndpointPoolDetail    string     `json:"endpointPoolDetail,omitempty"`
	EndpointPoolError     string     `json:"endpointPoolError,omitempty"`
	Problem               string     `json:"problem,omitempty"`
}

// State returns the aggregate human-readable relay installation state.
func (status InstallationStatus) State() string {
	switch {
	case !status.Installed && status.EndpointPoolResidual:
		return "not installed; residual endpoint pool"
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
	LifecycleLockPath     string
	ArtifactUID           int
	ArtifactGID           int
}

type inspectionProbes struct {
	http     func(context.Context) error
	dns      func(context.Context) error
	resolver func(context.Context) error
}

func defaultInspectionProbes() inspectionProbes {
	return inspectionProbes{http: relayhealth.Check, dns: relayhealth.CheckDNS, resolver: relayhealth.CheckResolver}
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
		DNSListenAddress: relayruntime.DefaultDNSAddress,
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
	var resolverArtifactErr error
	for _, resolverPath := range resolverPaths {
		present, resolverErr := inspectArtifact(resolverPath, 0o644, details.ArtifactUID, details.ArtifactGID)
		if resolverErr != nil {
			resolverArtifactErr = errors.Join(resolverArtifactErr, resolverErr)
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
	if configurationErr != nil || resolverArtifactErr != nil {
		status.ConfigurationError = errors.Join(configurationErr, resolverArtifactErr).Error()
	}
	status.ReceiptPresent = receiptPresent
	status.ResolverPresent = resolverPresent
	status.Installed = helperPresent || configurationPresent || receiptPresent || resolverArtifactPresent
	receiptValid := false
	if receiptPresent && receiptArtifactErr == nil {
		receipt, receiptErr := readInstallationReceipt(details)
		if receiptErr != nil {
			status.Problem = appendProblem(status.Problem, receiptErr.Error())
		} else {
			receiptValid = true
			status.OwnerUID, status.OwnerGID = receipt.OwnerUID, receipt.OwnerGID
			status.TargetSocket, status.DNSTargetSocket = receipt.TargetSocket, receipt.DNSTargetSocket
			installedAt := receipt.InstalledAt
			status.InstalledAt = &installedAt
			if !receiptUsesCurrentLoopbackPool(receipt) {
				status.Problem = appendProblem(status.Problem, "installation receipt uses an outdated loopback address pool; run `portless setup` to repair it")
			}
			expected, expectedErr := platform.expectedArtifacts(receipt)
			if expectedErr != nil {
				status.ConfigurationError = appendProblem(status.ConfigurationError, expectedErr.Error())
				status.Problem = appendProblem(status.Problem, expectedErr.Error())
			} else if contentErr := verifyExpectedArtifacts(expected); contentErr != nil {
				status.ConfigurationError = appendProblem(status.ConfigurationError, contentErr.Error())
				status.Problem = appendProblem(status.Problem, contentErr.Error())
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
	pool, poolErr := platform.loopbackPoolStatus()
	status.EndpointPoolReady = pool.ready
	status.EndpointPoolDetail = pool.detail
	status.EndpointPoolManaged = pool.managed
	if poolErr != nil {
		status.EndpointPoolError = poolErr.Error()
		status.Problem = appendProblem(status.Problem, "inspect TCP endpoint address pool: "+poolErr.Error())
	}
	status.EndpointPoolResidual = pool.managed && pool.configured && !receiptValid
	if status.EndpointPoolResidual {
		status.Problem = appendProblem(status.Problem, "reserved Portless loopback addresses remain without a valid ownership receipt; Portless will not remove unverified aliases")
	}
	if status.Installed && probes.http != nil && probes.dns != nil && probes.resolver != nil {
		healthContext, cancelHealth := context.WithTimeout(ctx, 1500*time.Millisecond)
		health := relayhealth.Inspect(healthContext, relayhealth.Probes{HTTP: probes.http, DNS: probes.dns, Resolver: probes.resolver})
		cancelHealth()
		httpErr, dnsErr, resolverErr := health.HTTPError, health.DNSError, health.ResolverError
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
		pathsHealthy := httpErr == nil && dnsErr == nil && resolverErr == nil && pool.ready && poolErr == nil
		status.Healthy = status.Running && artifactsHealthy && pathsHealthy
	}
	return status, nil
}

func verifyExpectedArtifacts(artifacts []expectedArtifact) error {
	var resultErr error
	for _, artifact := range artifacts {
		if artifact.path == "" {
			continue
		}
		info, err := os.Lstat(artifact.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("inspect relay artifact content %s: %w", artifact.path, err))
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > 1<<20 {
			resultErr = errors.Join(resultErr, fmt.Errorf("relay artifact %s is unexpectedly larger than 1 MiB", artifact.path))
			continue
		}
		file, err := os.Open(artifact.path)
		if err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("read relay artifact content %s: %w", artifact.path, err))
			continue
		}
		content, readErr := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("read relay artifact content %s: %w", artifact.path, errors.Join(readErr, closeErr)))
			continue
		}
		if !bytes.Equal(content, artifact.content) {
			resultErr = errors.Join(resultErr, fmt.Errorf("relay artifact %s content does not match the ownership receipt", artifact.path))
		}
	}
	return resultErr
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
