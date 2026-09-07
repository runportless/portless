package controlplane

import (
	"context"
	"fmt"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
)

// PreviewTrafficClear reports the current live watermark and environment/daemon identity.
func (s *Service) PreviewTrafficClear(ctx context.Context, project, environment string) (contract.TrafficClearPreview, error) {
	var result contract.TrafficClearPreview
	env, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return result, err
	}
	count, sequence := s.traffic.ClearWatermark(project, environment)
	return contract.TrafficClearPreview{Count: count, ThroughSequence: sequence, Expected: model.ResourceVersion{CreatedAt: env.CreatedAt, Revision: env.Revision, ParentCreatedAt: s.replays.StartedAt()}, Retained: []string{"Exchanges arriving after this watermark, active requests, durable recordings, and admitted replay receipts remain."}}, nil
}

// ClearTrafficThrough clears only reviewed history while excluding environment replacement.
func (s *Service) ClearTrafficThrough(ctx context.Context, project, environment string, through int64, expected model.ResourceVersion) (contract.TrafficClearResponse, error) {
	var result contract.TrafficClearResponse
	if through < 0 {
		return result, fmt.Errorf("traffic watermark must be non-negative")
	}
	if !expected.ParentCreatedAt.Equal(s.replays.StartedAt()) {
		return result, fmt.Errorf("daemon changed since the clear preview: %w", database.ErrConflict)
	}
	expected.ParentCreatedAt = model.ResourceVersion{}.ParentCreatedAt
	err := s.database.WithLiveTrafficIdentity(ctx, project, environment, expected, func() {
		s.replays.ClearThrough(project, environment, through)
		result.Cleared, result.ThroughSequence, result.Revision = s.traffic.ClearThrough(project, environment, through)
	})
	return result, err
}
