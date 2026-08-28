//go:build !darwin && !linux

package installation

import (
	"context"
	"errors"

	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

type unsupportedPlatform struct{}

func newHostPlatform() hostPlatform { return unsupportedPlatform{} }

func (unsupportedPlatform) install(context.Context, SetupRequest) error {
	return errors.New("localhost relay setup is currently supported on macOS and systemd Linux")
}

func (unsupportedPlatform) restart(context.Context) error {
	return errors.New("localhost relay restart is currently supported on macOS and systemd Linux")
}

func (unsupportedPlatform) uninstall(context.Context, uninstallSpec) error {
	return errors.New("localhost relay uninstall is currently supported on macOS and systemd Linux")
}

func (unsupportedPlatform) prepareRuntime(context.Context, relayruntime.Identity) error {
	return errors.New("Portless loopback endpoint pools are unsupported on this platform")
}

func (unsupportedPlatform) expectedArtifacts(installationReceipt) ([]expectedArtifact, error) {
	return nil, nil
}

func (unsupportedPlatform) loopbackPoolStatus() (endpointPoolStatus, error) {
	return endpointPoolStatus{detail: "unsupported platform"}, nil
}

func (unsupportedPlatform) installation() platformInstallation {
	return platformInstallation{Name: "unsupported"}
}

func (unsupportedPlatform) serviceState(context.Context) (relayServiceState, error) {
	return relayServiceStopped, nil
}
