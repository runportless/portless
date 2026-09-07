package controlplane

import (
	"context"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/compiler"
	"strings"
)

// PreviewConfiguration reports metadata removal effects without changing topology or files.
func (s *Service) PreviewConfiguration(ctx context.Context, project, environment, source string) (model.ConfigurationPreview, error) {
	// Projection reads may cross transactions. Verify the same snapshot after they
	// complete; apply validates it once more inside the committing transaction.
	result, err := s.database.PreviewConfiguration(ctx, project, environment)
	if err != nil {
		return result, err
	}
	if source != "" {
		if err := model.ValidateSourceName(source); err != nil {
			return result, err
		}
		result.Source = source
		if environment == "" {
			p, err := s.database.Project(ctx, project)
			if err != nil {
				return result, err
			}
			definition, err := s.database.ProjectModel(ctx, project)
			if err != nil {
				return result, err
			}
			_, _, result.RemovedServices, result.RemovedConnections, err = compiler.RemoveSource(definition, p.Sources, source)
			if err != nil {
				return result, err
			}
			result.Retained = []string{"filesystem checkouts", "environments", "recordings", "unrelated services and sources"}
		} else {
			env, err := s.database.Environment(ctx, project, environment)
			if err != nil {
				return result, err
			}
			found := false
			for _, item := range env.Sources {
				found = found || strings.EqualFold(item.Name, source)
			}
			if !found {
				return result, database.ErrNotFound
			}
			for _, binding := range env.Bindings {
				if binding.Provider == model.ProviderLocal && strings.EqualFold(binding.Source, source) {
					result.Blocked = append(result.Blocked, "checkout is used by local service "+binding.Service)
				}
			}
			result.Retained = []string{"filesystem checkout", "logical project source", "other environments", "recordings, faults, and mocks"}
		}
	}
	latest, err := s.database.PreviewConfiguration(ctx, project, environment)
	if err != nil {
		return result, err
	}
	if latest.Expected != result.Expected {
		return result, database.ErrConflict
	}
	return result, nil
}
