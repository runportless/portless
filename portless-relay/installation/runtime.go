package installation

import (
	"context"

	relayruntime "github.com/runportless/portless/portless-relay/runtime"
)

// AuthorizeRuntime verifies the fixed runtime identity against the root-owned
// installation receipt and prepares any platform-owned loopback resources.
func AuthorizeRuntime(ctx context.Context, identity relayruntime.Identity) error {
	if err := relayruntime.ValidateIdentity(identity); err != nil {
		return err
	}
	return newHostPlatform().prepareRuntime(ctx, identity)
}
