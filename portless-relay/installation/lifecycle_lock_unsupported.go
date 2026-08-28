//go:build !darwin && !linux

package installation

import "context"

func withRelayLifecycleLock(_ context.Context, _ platformInstallation, operation func() error) error {
	return operation()
}
