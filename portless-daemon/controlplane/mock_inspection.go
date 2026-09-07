package controlplane

import (
	"context"
	"github.com/runportless/portless/portless-daemon/model"
)

// MockScenarioMetadataList returns scenario summaries without loading saved bodies.
func (s *Service) MockScenarioMetadataList(ctx context.Context, project, environment string, offset, limit int) ([]model.MockScenarioMetadata, int, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	return s.database.MockScenarioMetadataList(ctx, project, environment, offset, limit)
}

// InspectMockScenario reads scenario identity and a route page under the mutation lock.
// A named route's saved payload is loaded only when includePayloads is true.
func (s *Service) InspectMockScenario(ctx context.Context, project, environment, scenario, route string, offset, limit int, includePayloads bool) (model.MockScenarioMetadata, []model.MockRouteMetadata, *model.MockRoute, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	metadata, err := s.database.MockScenarioMetadata(ctx, project, environment, scenario)
	if err != nil {
		return metadata, nil, nil, err
	}
	routes, err := s.database.MockRouteMetadataPage(ctx, project, environment, scenario, route, offset, limit)
	if err != nil {
		return metadata, nil, nil, err
	}
	if route != "" && includePayloads {
		value, err := s.database.MockRoute(ctx, project, environment, scenario, route)
		return metadata, routes, &value, err
	}
	return metadata, routes, nil, nil
}
