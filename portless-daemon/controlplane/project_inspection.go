package controlplane

import (
	"context"
	"github.com/runportless/portless/portless-daemon/model"
	"strings"
)

// ProjectMetadataList returns bounded safe project summaries.
func (s *Service) ProjectMetadataList(ctx context.Context, offset, limit int) ([]model.ProjectMetadata, int, error) {
	return s.database.ProjectMetadataList(ctx, offset, limit)
}

// ProjectMetadata returns safe logical topology without private runtime values.
func (s *Service) ProjectMetadata(ctx context.Context, name string) (model.ProjectMetadata, error) {
	return s.database.ProjectMetadata(ctx, name, true)
}

// ProjectDeclaration returns a stable safe declaration and reports all omitted configuration.
func (s *Service) ProjectDeclaration(ctx context.Context, name string) (model.ProjectDeclaration, error) {
	result, err := s.database.ProjectDeclaration(ctx, name)
	if err != nil {
		return result, err
	}
	result.SchemaVersion = 2
	result.Redactions = []string{}
	result.SuggestedName = result.Project
	if len(result.References) > 0 {
		result.Redactions = append(result.Redactions, "references: unresolved discovery hints omitted")
		result.References = nil
	}
	for i := range result.Services {
		service := &result.Services[i]
		prefix := "services." + service.Name + "."
		if len(service.Environment) > 0 {
			result.Redactions = append(result.Redactions, prefix+"environment: discovered values omitted")
			service.Environment = nil
		}
		if len(service.Command) > 0 {
			result.Redactions = append(result.Redactions, prefix+"command: executable arguments may contain credentials")
			service.Command = nil
		}
		if service.Debug != nil {
			result.Redactions = append(result.Redactions, prefix+"debug: executable arguments omitted")
			service.Debug = nil
		}
		if len(service.Evidence) > 0 {
			result.Redactions = append(result.Redactions, prefix+"evidence: discovery text omitted")
			service.Evidence = nil
		}
		if service.WorkingDirectory != "" || service.ServiceDirectory != "" {
			result.Redactions = append(result.Redactions, prefix+"directories: checkout locations omitted")
			service.WorkingDirectory = ""
			service.ServiceDirectory = ""
		}
		path, _, query := strings.Cut(service.Health.Path, "?")
		if query {
			result.Redactions = append(result.Redactions, prefix+"health.path: query values omitted")
			service.Health.Path = path
		}
	}
	return result, nil
}
