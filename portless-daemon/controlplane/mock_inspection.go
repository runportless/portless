package controlplane

import (
	"context"
	"fmt"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
)

func (s *Service) populateEnvironmentMocks(ctx context.Context, environment *model.Environment) error {
	metadata, _, err := s.database.MockScenarioMetadataList(ctx, environment.Project, environment.Name, 0, -1)
	if err != nil {
		return fmt.Errorf("inspect mock activity for %s/%s: %w", environment.Project, environment.Name, err)
	}
	for _, item := range metadata {
		if item.Activation.State == model.MockScenarioDisabled {
			continue
		}
		activation := s.projectMockActivation(model.EnvironmentSelector(environment.Project, environment.Name), item.Name, item.UnmatchedRequests, environment.Status, item.Activation)
		for i := range environment.Services {
			for _, target := range activation.TargetServices {
				if strings.EqualFold(environment.Services[i].Name, target) {
					environment.Services[i].Mock = &model.ServiceMock{Scenario: item.Name, UnmatchedRequests: item.UnmatchedRequests, State: activation.State}
				}
			}
		}
	}
	return nil
}

// MockScenarioMetadataList returns scenario summaries without loading saved bodies.
func (s *Service) MockScenarioMetadataList(ctx context.Context, project, environment string, offset, limit int) ([]model.MockScenarioMetadata, int, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	items, total, err := s.database.MockScenarioMetadataList(ctx, project, environment, offset, limit)
	if err != nil {
		return items, total, err
	}
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		items[i].Activation = s.projectMockActivation(model.EnvironmentSelector(project, environment), items[i].Name, items[i].UnmatchedRequests, current.Status, items[i].Activation)
	}
	return items, total, nil
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
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return metadata, nil, nil, err
	}
	metadata.Activation = s.projectMockActivation(model.EnvironmentSelector(project, environment), metadata.Name, metadata.UnmatchedRequests, current.Status, metadata.Activation)
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
