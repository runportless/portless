package controlplane

import (
	"context"
	"github.com/runportless/portless/portless-daemon/projects/discovery"
)

// DiscoveryPolicy confines new sources and creation to the trusted adapter's startup scope.
type DiscoveryPolicy struct {
	AllowedRoot             string
	RequiredAssociationPath string
	RequiredProject         string
}

func (s *Service) discoverSource(ctx context.Context, path, root string) (discovery.Result, error) {
	if root != "" {
		return s.discoverer.DiscoverWithin(ctx, path, root)
	}
	return s.discoverer.Discover(ctx, path)
}
