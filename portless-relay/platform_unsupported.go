//go:build !darwin && !linux

package relay

import (
	"context"
	"errors"
)

func installPlatform(context.Context, SetupRequest) error {
	return errors.New("localhost relay setup is currently supported on macOS and systemd Linux")
}

func restartPlatform(context.Context) error {
	return errors.New("localhost relay restart is currently supported on macOS and systemd Linux")
}

func uninstallPlatform(context.Context, bool) error {
	return errors.New("localhost relay uninstall is currently supported on macOS and systemd Linux")
}

func prepareRelayLoopbackPool(context.Context, bool) error {
	return errors.New("Portless loopback endpoint pools are unsupported on this platform")
}

func relayLoopbackPoolStatus() (bool, string, error) {
	return false, "unsupported platform", nil
}

func currentPlatformInstallation() platformInstallation {
	return platformInstallation{Name: "unsupported"}
}

func platformServiceRunning(context.Context) (bool, error) {
	return false, nil
}

func platformConfigurationOwner(string) (int, int, string, string, error) {
	return 0, 0, "", "", errors.New("unsupported platform")
}
