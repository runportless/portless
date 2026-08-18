package controlplane

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/projects/compiler"
)

// SourceInput identifies a named filesystem source used to create a project.
type SourceInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Discover resolves a path to an existing environment or creates a single-source project.
func (s *Service) Discover(ctx context.Context, path, requestedName string) (model.Project, model.Environment, []string, error) {
	result, err := s.discoverer.Discover(ctx, path)
	if err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	if known, err := s.database.EnvironmentsByPath(ctx, result.Root); err == nil && len(known) > 0 {
		environment := known[0]
		project, projectErr := s.Project(ctx, environment.Project)
		return project, s.decorateEnvironment(environment), result.Warnings, projectErr
	} else if err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	name := requestedName
	if name == "" {
		name = result.Model.SuggestedName
	}
	name = model.NormalizeDNSName(name)
	if err := model.ValidateProjectName(name); err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	sourceName := model.NormalizeDNSName(filepath.Base(result.Root))
	if model.ValidateSourceName(sourceName) != nil {
		sourceName = name
	}
	return s.CreateProject(ctx, name, []SourceInput{{Name: sourceName, Path: result.Root}})
}

// CreateProject discovers the supplied sources and creates a project with its local environment.
func (s *Service) CreateProject(ctx context.Context, name string, inputs []SourceInput) (model.Project, model.Environment, []string, error) {
	name = model.NormalizeDNSName(name)
	if err := model.ValidateProjectName(name); err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	if len(inputs) == 0 {
		return model.Project{}, model.Environment{}, nil, errors.New("at least one source is required")
	}
	var sources []model.SourceBinding
	var warnings []string
	for _, input := range inputs {
		input.Name = model.NormalizeDNSName(input.Name)
		if err := model.ValidateSourceName(input.Name); err != nil {
			return model.Project{}, model.Environment{}, warnings, err
		}
		if !filepath.IsAbs(input.Path) {
			return model.Project{}, model.Environment{}, warnings, fmt.Errorf("source %s path must be absolute", input.Name)
		}
		result, err := s.discoverer.Discover(ctx, input.Path)
		if err != nil {
			return model.Project{}, model.Environment{}, warnings, fmt.Errorf("discover source %s: %w", input.Name, err)
		}
		warnings = append(warnings, result.Warnings...)
		sources = append(sources, model.SourceBinding{Name: input.Name, Path: result.Root, Status: "ready", Warnings: result.Warnings, ScannedAt: time.Now().UTC(), Definition: result.Model})
	}
	definition, projectSources, bindings, err := compiler.InitialProject(name, sources)
	if err != nil {
		return model.Project{}, model.Environment{}, warnings, err
	}
	project, err := s.database.CreateProject(ctx, name, definition, projectSources)
	if errors.Is(err, database.ErrNameTaken) {
		return model.Project{}, model.Environment{}, warnings, NameConflictError{Name: name, Suggestions: projectNameSuggestions(name, sources[0].Path)}
	}
	if err != nil {
		return model.Project{}, model.Environment{}, warnings, err
	}
	compiled := compiler.Compile(definition, sources, bindings)
	if len(compiled.Issues) > 0 {
		_ = s.database.ForgetProject(ctx, name)
		return model.Project{}, model.Environment{}, warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	environment, err := s.database.CreateEnvironment(ctx, name, "local", compiled.Definition, sources, compiled.Bindings)
	if err != nil {
		_ = s.database.ForgetProject(ctx, name)
		return model.Project{}, model.Environment{}, warnings, err
	}
	scope := model.EnvironmentSelector(name, "local")
	_, _ = s.timeline(ctx, scope, "CLI", "environment.discovered", scope, "info", "Discovered local environment", map[string]any{"sources": len(sources)})
	project, _ = s.database.Project(ctx, name)
	return s.decorateProject(project), s.decorateEnvironment(environment), warnings, nil
}

// AddProjectSource discovers and atomically adds a source to a project and one environment.
func (s *Service) AddProjectSource(ctx context.Context, projectName, environmentName, sourceName, path string) (model.Project, model.Environment, []string, error) {
	sourceName = model.NormalizeDNSName(sourceName)
	if err := model.ValidateSourceName(sourceName); err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	if !filepath.IsAbs(path) {
		return model.Project{}, model.Environment{}, nil, errors.New("source path must be absolute")
	}
	project, err := s.database.Project(ctx, projectName)
	if err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}

	result, err := s.discoverer.Discover(ctx, path)
	if err != nil {
		return model.Project{}, model.Environment{}, nil, fmt.Errorf("discover source %s: %w", sourceName, err)
	}
	source := model.SourceBinding{
		Name: sourceName, Path: result.Root, Status: "ready", Warnings: result.Warnings,
		ScannedAt: time.Now().UTC(), Definition: result.Model,
	}
	projectDefinition, projectSources, defaults, err := compiler.AddSource(projectDefinition, project.Sources, source)
	if err != nil {
		return model.Project{}, model.Environment{}, result.Warnings, err
	}
	sources := append([]model.SourceBinding{}, environment.Sources...)
	sources = append(sources, source)
	bindings := append([]model.ComponentBinding{}, environment.Bindings...)
	bindings = append(bindings, defaults...)
	compiled := compiler.Compile(projectDefinition, sources, bindings)
	if len(compiled.Issues) > 0 {
		return model.Project{}, model.Environment{}, result.Warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.database.ReplaceProjectAndEnvironmentConfiguration(
		ctx, projectName, project.Revision, projectDefinition, projectSources,
		environmentName, environment.Revision, compiled.Definition, sources, compiled.Bindings,
	)
	if err != nil {
		return model.Project{}, model.Environment{}, result.Warnings, err
	}
	project, err = s.database.Project(ctx, projectName)
	if err != nil {
		return model.Project{}, model.Environment{}, result.Warnings, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	_, _ = s.timeline(ctx, scope, "CLI", "project.source_added", sourceName, "info", "Added project source "+sourceName, map[string]any{"path": result.Root})
	return s.decorateProject(project), s.decorateEnvironment(updated), result.Warnings, nil
}

// Rescan refreshes a stopped environment from its currently bound filesystem sources.
func (s *Service) Rescan(ctx context.Context, projectName, environmentName string) (model.Environment, []string, error) {
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, nil, err
	}
	if environment.Status != model.EnvironmentStopped {
		return model.Environment{}, nil, errors.New("environment must be stopped before rescan")
	}
	var warnings []string
	for index := range environment.Sources {
		result, err := s.discoverer.Discover(ctx, environment.Sources[index].Path)
		if err != nil {
			return model.Environment{}, warnings, fmt.Errorf("rescan source %s: %w", environment.Sources[index].Name, err)
		}
		environment.Sources[index].Definition = result.Model
		environment.Sources[index].Warnings = result.Warnings
		environment.Sources[index].ScannedAt = time.Now().UTC()
		warnings = append(warnings, result.Warnings...)
	}
	project, err := s.database.Project(ctx, projectName)
	if err != nil {
		return model.Environment{}, warnings, err
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Environment{}, warnings, err
	}
	projectDefinition = compiler.RefreshDiscoveredTopology(projectDefinition, environment.Sources)
	compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings)
	if len(compiled.Issues) > 0 {
		environment.Issues = compiled.Issues
		return environment, warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.database.ReplaceProjectAndEnvironmentConfiguration(ctx, projectName, project.Revision, projectDefinition, project.Sources, environmentName, environment.Revision, compiled.Definition, environment.Sources, compiled.Bindings)
	if err != nil {
		return model.Environment{}, warnings, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	_, _ = s.timeline(ctx, scope, "CLI", "environment.rescanned", scope, "info", "Environment sources refreshed", nil)
	return s.decorateEnvironment(updated), warnings, nil
}

// Rename changes a project name using optimistic concurrency.
func (s *Service) Rename(ctx context.Context, projectName, newName string, revision int64, actor string) (model.Project, error) {
	newName = model.NormalizeDNSName(newName)
	if err := model.ValidateProjectName(newName); err != nil {
		return model.Project{}, err
	}
	project, err := s.database.RenameProject(ctx, projectName, newName, revision)
	if err != nil {
		return model.Project{}, err
	}
	return s.decorateProject(project), nil
}

// Forget removes a stopped project and all of its application state.
func (s *Service) Forget(ctx context.Context, projectName string) error {
	return s.database.ForgetProject(ctx, projectName)
}

// Projects lists all known projects with dashboard links and environment summaries.
func (s *Service) Projects(ctx context.Context) ([]model.Project, error) {
	projects, err := s.database.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		projects[index] = s.decorateProject(projects[index])
	}
	return projects, nil
}

// Project returns one project with its decorated public representation.
func (s *Service) Project(ctx context.Context, name string) (model.Project, error) {
	project, err := s.database.Project(ctx, name)
	if err != nil {
		return model.Project{}, err
	}
	return s.decorateProject(project), nil
}

// Environments lists environments, optionally restricted to one project.
func (s *Service) Environments(ctx context.Context, projectName string) ([]model.Environment, error) {
	environments, err := s.database.ListEnvironments(ctx, projectName)
	if err != nil {
		return nil, err
	}
	definitions := make(map[string]model.ProjectModel)
	for index := range environments {
		definition, ok := definitions[environments[index].Project]
		if !ok {
			definition, err = s.database.ProjectModel(ctx, environments[index].Project)
			if err != nil {
				return nil, err
			}
			definitions[environments[index].Project] = definition
		}
		environments[index].Issues = compiler.Compile(definition, environments[index].Sources, environments[index].Bindings).Issues
		environments[index] = s.decorateEnvironment(environments[index])
	}
	return environments, nil
}

// Environment returns one environment with current configuration issues and endpoints.
func (s *Service) Environment(ctx context.Context, projectName, environmentName string) (model.Environment, error) {
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	projectDefinition, definitionErr := s.database.ProjectModel(ctx, projectName)
	if definitionErr == nil {
		environment.Issues = compiler.Compile(projectDefinition, environment.Sources, environment.Bindings).Issues
	}
	return s.decorateEnvironment(environment), nil
}

// CloneEnvironment creates a stopped environment from another environment's bindings.
func (s *Service) CloneEnvironment(ctx context.Context, projectName, from, name string) (model.Environment, error) {
	created, err := s.database.CloneEnvironment(ctx, projectName, from, name)
	if err != nil {
		return model.Environment{}, err
	}
	return s.decorateEnvironment(created), nil
}

// ForgetEnvironment removes a stopped environment and its retained application state.
func (s *Service) ForgetEnvironment(ctx context.Context, projectName, environmentName string) error {
	return s.database.ForgetEnvironment(ctx, projectName, environmentName)
}

// SetBinding selects the local, container, or remote provider for an environment service.
func (s *Service) SetBinding(ctx context.Context, projectName, environmentName, serviceName string, binding model.ComponentBinding) (model.Environment, error) {
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Environment{}, err
	}
	found := false
	for _, service := range projectDefinition.Services {
		if strings.EqualFold(service.Name, serviceName) {
			found = true
			serviceName = service.Name
			switch binding.Provider {
			case model.ProviderLocal:
				if service.Kind != model.ServiceProcess {
					return model.Environment{}, errors.New("only application services can use a local source provider")
				}
				if binding.Source == "" {
					return model.Environment{}, errors.New("a local provider requires a source name")
				}
				binding.Remote = nil
			case model.ProviderContainer:
				if service.Kind != model.ServiceResource {
					return model.Environment{}, errors.New("only managed resources can use the container provider")
				}
				binding.Source, binding.Remote = "", nil
			case model.ProviderRemote:
				if service.Kind != model.ServiceProcess {
					return model.Environment{}, errors.New("only HTTP application services can use a remote provider")
				}
				if err := compiler.ValidateRemote(binding.Remote); err != nil {
					return model.Environment{}, err
				}
				binding.Source = ""
			default:
				return model.Environment{}, errors.New("provider must be local, container, or remote")
			}
		}
	}
	if !found {
		return model.Environment{}, database.ErrNotFound
	}
	binding.Service = serviceName
	binding.ModifiedAt = time.Now().UTC()
	replaced := false
	for index := range environment.Bindings {
		if strings.EqualFold(environment.Bindings[index].Service, serviceName) {
			environment.Bindings[index] = binding
			replaced = true
			break
		}
	}
	if !replaced {
		environment.Bindings = append(environment.Bindings, binding)
	}
	compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings)
	if !configurationCanBeSaved(compiled.Issues) {
		return model.Environment{}, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.database.ReplaceEnvironmentConfiguration(ctx, projectName, environmentName, environment.Revision, compiled.Definition, environment.Sources, environment.Bindings)
	if err != nil {
		return model.Environment{}, err
	}
	updated.Issues = compiled.Issues
	return s.decorateEnvironment(updated), nil
}

// SetSource rebinds one environment source to a newly discovered filesystem path.
func (s *Service) SetSource(ctx context.Context, projectName, environmentName, sourceName, path string) (model.Environment, []string, error) {
	if !filepath.IsAbs(path) {
		return model.Environment{}, nil, errors.New("source path must be absolute")
	}
	project, err := s.database.Project(ctx, projectName)
	if err != nil {
		return model.Environment{}, nil, err
	}
	var declared *model.ProjectSource
	for index := range project.Sources {
		if strings.EqualFold(project.Sources[index].Name, sourceName) {
			declared = &project.Sources[index]
			sourceName = project.Sources[index].Name
			break
		}
	}
	if declared == nil {
		return model.Environment{}, nil, database.ErrNotFound
	}
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, nil, err
	}
	if environment.Status != model.EnvironmentStopped {
		return model.Environment{}, nil, errors.New("environment must be stopped before a source changes")
	}
	result, err := s.discoverer.Discover(ctx, path)
	if err != nil {
		return model.Environment{}, nil, fmt.Errorf("discover source %s: %w", sourceName, err)
	}
	found := false
	for index := range environment.Sources {
		if strings.EqualFold(environment.Sources[index].Name, sourceName) {
			environment.Sources[index] = model.SourceBinding{
				Name: sourceName, Path: result.Root, Status: "ready", Warnings: result.Warnings,
				ScannedAt: time.Now().UTC(), Definition: result.Model,
			}
			found = true
			break
		}
	}
	if !found {
		environment.Sources = append(environment.Sources, model.SourceBinding{
			Name: sourceName, Path: result.Root, Status: "ready", Warnings: result.Warnings,
			ScannedAt: time.Now().UTC(), Definition: result.Model,
		})
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Environment{}, result.Warnings, err
	}
	for _, serviceName := range declared.Services {
		if _, exists := bindingByName(environment.Bindings, serviceName); !exists {
			environment.Bindings = append(environment.Bindings, model.ComponentBinding{Service: serviceName, Provider: model.ProviderLocal, Source: sourceName})
		}
	}
	for _, service := range projectDefinition.Services {
		if service.Kind != model.ServiceResource {
			continue
		}
		if _, exists := bindingByName(environment.Bindings, service.Name); !exists {
			environment.Bindings = append(environment.Bindings, model.ComponentBinding{Service: service.Name, Provider: model.ProviderContainer})
		}
	}
	compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings)
	if !configurationCanBeSaved(compiled.Issues) {
		return model.Environment{}, result.Warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.database.ReplaceEnvironmentConfiguration(ctx, projectName, environmentName, environment.Revision, compiled.Definition, environment.Sources, environment.Bindings)
	if err != nil {
		return model.Environment{}, result.Warnings, err
	}
	updated.Issues = compiled.Issues
	scope := model.EnvironmentSelector(projectName, environmentName)
	_, _ = s.timeline(ctx, scope, "CLI", "environment.source_changed", sourceName, "info", "Source "+sourceName+" now uses "+result.Root, nil)
	return s.decorateEnvironment(updated), result.Warnings, nil
}

// SelectEnvironment persists the environment explicitly selected for a checkout path.
func (s *Service) SelectEnvironment(ctx context.Context, path, projectName, environmentName string) error {
	return s.database.SetContextSelection(ctx, path, projectName, environmentName)
}

// ClearEnvironmentSelection removes the explicit environment selection for a checkout path.
func (s *Service) ClearEnvironmentSelection(ctx context.Context, path string) (bool, error) {
	return s.database.ClearContextSelection(ctx, path)
}

// EnvironmentContext resolves a path through explicit selection or source-path inference.
func (s *Service) EnvironmentContext(ctx context.Context, path string) (EnvironmentContext, error) {
	selected, err := s.database.ContextSelection(ctx, path)
	if err == nil {
		selected = s.decorateEnvironment(selected)
		return EnvironmentContext{Resolution: "selected", Environment: &selected, Candidates: []model.Environment{}}, nil
	}
	if !errors.Is(err, database.ErrNotFound) {
		return EnvironmentContext{}, err
	}
	candidates, err := s.database.EnvironmentsByPath(ctx, path)
	if err != nil {
		return EnvironmentContext{}, err
	}
	for index := range candidates {
		candidates[index] = s.decorateEnvironment(candidates[index])
	}
	result := EnvironmentContext{Resolution: "none", Candidates: candidates}
	switch len(candidates) {
	case 1:
		result.Resolution = "inferred"
		result.Environment = &candidates[0]
	case 0:
		result.Candidates = []model.Environment{}
	default:
		result.Resolution = "ambiguous"
	}
	return result, nil
}

// EnvironmentsForPath returns the selected environment or all inferred candidates for a path.
func (s *Service) EnvironmentsForPath(ctx context.Context, path string) ([]model.Environment, error) {
	resolved, err := s.EnvironmentContext(ctx, path)
	if err != nil {
		return nil, err
	}
	if resolved.Environment != nil {
		return []model.Environment{*resolved.Environment}, nil
	}
	return resolved.Candidates, nil
}

// ProjectModel returns the reusable discovered topology for a project.
func (s *Service) ProjectModel(ctx context.Context, name string) (model.ProjectModel, error) {
	return s.database.ProjectModel(ctx, name)
}
