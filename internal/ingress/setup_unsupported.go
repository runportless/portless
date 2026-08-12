//go:build !darwin && !linux

package ingress

import (
	"context"
	"errors"
)

func installPlatform(context.Context, SetupRequest) error {
	return errors.New("clean localhost ingress setup is currently supported on macOS and systemd Linux")
}

func restartPlatform(context.Context) error {
	return errors.New("clean localhost ingress restart is currently supported on macOS and systemd Linux")
}

func uninstallPlatform(context.Context) error {
	return errors.New("clean localhost ingress uninstall is currently supported on macOS and systemd Linux")
}

func currentPlatformInstallation() platformInstallation {
	return platformInstallation{Name: "unsupported"}
}

func platformServiceRunning(context.Context) (bool, error) {
	return false, nil
}

func platformConfigurationOwner(string) (int, int, string, error) {
	return 0, 0, "", errors.New("unsupported platform")
}
