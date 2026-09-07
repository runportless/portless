package controlplane

import (
	"context"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
	"strings"
)

// PreviewMockDeletion reports exact route names and blockers without changing state.
func (s *Service) PreviewMockDeletion(ctx context.Context, project, environment, scenario, route string) (model.MockDeletionPreview, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	var result model.MockDeletionPreview
	metadata, err := s.database.MockScenarioMetadata(ctx, project, environment, scenario)
	if err != nil {
		return result, err
	}
	env, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return result, err
	}
	routes, err := s.database.MockRouteMetadataPage(ctx, project, environment, scenario, "", 0, 1000)
	if err != nil {
		return result, err
	}
	result = model.MockDeletionPreview{Scenario: metadata, Route: route, Routes: []string{}, Expected: model.ResourceVersion{CreatedAt: metadata.CreatedAt, ModifiedAt: metadata.ModifiedAt, ParentCreatedAt: env.CreatedAt}, Blocked: []string{}, Retained: []string{"Application source files, durable recordings, and live traffic remain."}}
	counts := map[string]int{}
	removedService := ""
	for _, candidate := range routes {
		counts[strings.ToLower(candidate.Service)]++
		if route == "" || strings.EqualFold(candidate.Name, route) {
			result.Routes = append(result.Routes, candidate.Name)
			removedService = candidate.Service
		}
	}
	if route != "" && len(result.Routes) == 0 {
		return result, database.ErrNotFound
	}
	if metadata.Activation.State != model.MockScenarioDisabled {
		if route == "" {
			result.Blocked = append(result.Blocked, "Disable the scenario before deleting it.")
		} else if counts[strings.ToLower(removedService)] == 1 {
			result.Blocked = append(result.Blocked, "Disable the scenario before deleting the final route for a service.")
		}
	}
	return result, nil
}
