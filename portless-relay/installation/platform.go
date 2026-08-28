package installation

import (
	"context"
	"os"
	"time"

	relayhealth "github.com/runportless/portless/portless-relay/health"
	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

type relayServiceState uint8

const (
	relayServiceUnknown relayServiceState = iota
	relayServiceStopped
	relayServiceRunning
)

type uninstallSpec struct {
	loopbackAddresses []string
}

type endpointPoolStatus struct {
	ready      bool
	configured bool
	managed    bool
	detail     string
}

type expectedArtifact struct {
	path    string
	content []byte
}

type platformOperations struct {
	beginArtifactTransactionFunc func(...string) (*artifactTransaction, error)
	copyExecutableFunc           func(string, string) error
	writeRootFileFunc            func(string, []byte, os.FileMode) error
	writeReceiptFunc             func(platformInstallation, SetupRequest) error
	readReceiptFunc              func(platformInstallation) (installationReceipt, error)
	pathExistsFunc               func(string) (bool, error)
	removeFileFunc               func(string) error
	waitForAddressesFunc         func(context.Context, time.Duration) error
	waitUntilReadyFunc           func(context.Context, time.Duration) error
}

func (operations platformOperations) beginArtifactTransaction(paths ...string) (*artifactTransaction, error) {
	if operations.beginArtifactTransactionFunc != nil {
		return operations.beginArtifactTransactionFunc(paths...)
	}
	return beginArtifactTransaction(paths...)
}

func (operations platformOperations) copyExecutable(source, destination string) error {
	if operations.copyExecutableFunc != nil {
		return operations.copyExecutableFunc(source, destination)
	}
	return copyExecutableAtomically(source, destination)
}

func (operations platformOperations) writeRootFile(destination string, content []byte, mode os.FileMode) error {
	if operations.writeRootFileFunc != nil {
		return operations.writeRootFileFunc(destination, content, mode)
	}
	return writeRootFileAtomically(destination, content, mode)
}

func (operations platformOperations) writeReceipt(details platformInstallation, request SetupRequest) error {
	if operations.writeReceiptFunc != nil {
		return operations.writeReceiptFunc(details, request)
	}
	return writeInstallationReceipt(details, request)
}

func (operations platformOperations) readReceipt(details platformInstallation) (installationReceipt, error) {
	if operations.readReceiptFunc != nil {
		return operations.readReceiptFunc(details)
	}
	return readInstallationReceipt(details)
}

func (operations platformOperations) pathExists(path string) (bool, error) {
	if operations.pathExistsFunc != nil {
		return operations.pathExistsFunc(path)
	}
	return pathExists(path)
}

func (operations platformOperations) removeFile(path string) error {
	if operations.removeFileFunc != nil {
		return operations.removeFileFunc(path)
	}
	return removeExactFile(path)
}

func (operations platformOperations) waitForAddresses(ctx context.Context, timeout time.Duration) error {
	if operations.waitForAddressesFunc != nil {
		return operations.waitForAddressesFunc(ctx, timeout)
	}
	return waitForRelayAddressesAvailable(ctx, timeout)
}

func (operations platformOperations) waitUntilReady(ctx context.Context, timeout time.Duration) error {
	if operations.waitUntilReadyFunc != nil {
		return operations.waitUntilReadyFunc(ctx, timeout)
	}
	return relayhealth.WaitUntilReady(ctx, timeout)
}

type hostPlatform interface {
	installation() platformInstallation
	install(context.Context, SetupRequest) error
	restart(context.Context) error
	uninstall(context.Context, uninstallSpec) error
	prepareRuntime(context.Context, relayruntime.Identity) error
	expectedArtifacts(installationReceipt) ([]expectedArtifact, error)
	serviceState(context.Context) (relayServiceState, error)
	loopbackPoolStatus() (endpointPoolStatus, error)
}
