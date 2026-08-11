//go:build !darwin && !linux

package ingress

import (
	"context"
	"errors"
)

func installPlatform(context.Context, SetupRequest) error {
	return errors.New("clean localhost ingress setup is currently supported on macOS and systemd Linux")
}
