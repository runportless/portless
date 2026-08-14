package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	pathmatch "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/compiler"
	"github.com/portless-run/portless/internal/project/discovery"
	"github.com/portless-run/portless/internal/proxy"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/runtime/container/docker"
	"github.com/portless-run/portless/internal/runtime/container/podman"
	"github.com/portless-run/portless/internal/runtime/logstore"
	processruntime "github.com/portless-run/portless/internal/runtime/process"
	"github.com/portless-run/portless/internal/runtime/supervisor"
	"github.com/portless-run/portless/internal/store"
)

type NameConflictError struct {
	Name        string   `json:"name"`
	Suggestions []string `json:"suggestions"`
}

type RuntimeInUseError struct {
	Project string `json:"project"`
}

type ResetActiveEnvironmentsError struct {
	Environments []string `json:"environments"`
}

type ResetRuntimeResult struct {
	Processes int                     `json:"processes"`
	Runtimes  []container.ResetResult `json:"runtimes"`
}

type EnvironmentContext struct {
	Resolution  string              `json:"resolution"`
	Environment *model.Environment  `json:"environment,omitempty"`
	Candidates  []model.Environment `json:"candidates"`
}

func (e RuntimeInUseError) Error() string {
	return "stop project " + e.Project + " before changing the container runtime"
}

func (e ResetActiveEnvironmentsError) Error() string {
	return "all environments must be stopped before Portless application state is reset: " + strings.Join(e.Environments, ", ")
}

func (e NameConflictError) Error() string {
	return "project name " + e.Name + " is already used by another path"
}

type Config struct {
	DataDirectory    string
	InstallationKey  string
	DaemonInstanceID string
	Executable       string
}

type Service struct {
	store                *store.Store
	broker               *events.Broker
	processes            *processruntime.Manager
	containers           *container.Manager
	proxy                *proxy.Manager
	dataDirectory        string
	installationKey      string
	daemonInstanceID     string
	mu                   sync.RWMutex
	resetGate            sync.RWMutex
	resetting            bool
	projectLocks         map[string]*sync.Mutex
	containerEnvironment map[string]map[string]string
	sourceLeases         map[string]string
}

func New(controlStore *store.Store, broker *events.Broker, config Config) *Service {
	service := &Service{
		store: controlStore, broker: broker, dataDirectory: config.DataDirectory,
		installationKey: config.InstallationKey, daemonInstanceID: config.DaemonInstanceID,
		projectLocks: make(map[string]*sync.Mutex), containerEnvironment: make(map[string]map[string]string), sourceLeases: make(map[string]string),
	}
	service.proxy = proxy.NewManager(controlStore, broker)
	temporaryRoot := filepath.Join(config.DataDirectory, "tmp")
	service.containers = container.NewManager(
		filepath.Join(config.DataDirectory, "runtime.json"),
		podman.New(config.InstallationKey, temporaryRoot),
		docker.New(config.InstallationKey, temporaryRoot),
	)
	if config.DaemonInstanceID != "" && config.Executable != "" {
		service.processes = processruntime.NewSupervisedManager(config.Executable, filepath.Join(config.DataDirectory, "runs"), service.processExited)
	} else {
		service.processes = processruntime.NewManager(service.processExited)
	}
	if environments, err := controlStore.ListEnvironments(context.Background(), ""); err == nil {
		for _, environment := range environments {
			scope := model.EnvironmentSelector(environment.Project, environment.Name)
			if sequence, sequenceErr := controlStore.MaxRecordedTrafficSequence(context.Background(), scope); sequenceErr == nil {
				broker.EnsureTrafficSequence(scope, sequence)
			}
		}
	}
	return service
}

func (s *Service) Close(ctx context.Context) {
	s.processes.Close()
	s.containers.Close()
	closeContext := ctx
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		closeContext, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	s.proxy.Close(closeContext)
}

type SourceInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Service) Discover(ctx context.Context, path, requestedName string) (model.Project, model.Environment, []string, error) {
	result, err := discovery.Discover(path)
	if err != nil {
		return model.Project{}, model.Environment{}, nil, err
	}
	if known, err := s.store.EnvironmentsByPath(ctx, result.Root); err == nil && len(known) > 0 {
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
		result, err := discovery.Discover(input.Path)
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
	project, err := s.store.CreateProject(ctx, name, definition, projectSources)
	if errors.Is(err, store.ErrNameTaken) {
		return model.Project{}, model.Environment{}, warnings, NameConflictError{Name: name, Suggestions: projectNameSuggestions(name, sources[0].Path)}
	}
	if err != nil {
		return model.Project{}, model.Environment{}, warnings, err
	}
	compiled := compiler.Compile(definition, sources, bindings)
	if len(compiled.Issues) > 0 {
		_ = s.store.ForgetProject(ctx, name)
		return model.Project{}, model.Environment{}, warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	environment, err := s.store.CreateEnvironment(ctx, name, "local", compiled.Definition, sources, compiled.Bindings)
	if err != nil {
		_ = s.store.ForgetProject(ctx, name)
		return model.Project{}, model.Environment{}, warnings, err
	}
	scope := model.EnvironmentSelector(name, "local")
	_, _ = s.timeline(ctx, scope, "CLI", "environment.discovered", scope, "info", "Discovered local environment", map[string]any{"sources": len(sources)})
	project, _ = s.store.Project(ctx, name)
	return s.decorateProject(project), s.decorateEnvironment(environment), warnings, nil
}

func (s *Service) Rescan(ctx context.Context, projectName, environmentName string) (model.Environment, []string, error) {
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, nil, err
	}
	if environment.Status != model.EnvironmentStopped {
		return model.Environment{}, nil, errors.New("environment must be stopped before rescan")
	}
	var warnings []string
	for index := range environment.Sources {
		result, err := discovery.Discover(environment.Sources[index].Path)
		if err != nil {
			return model.Environment{}, warnings, fmt.Errorf("rescan source %s: %w", environment.Sources[index].Name, err)
		}
		environment.Sources[index].Definition = result.Model
		environment.Sources[index].Warnings = result.Warnings
		environment.Sources[index].ScannedAt = time.Now().UTC()
		warnings = append(warnings, result.Warnings...)
	}
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		return model.Environment{}, warnings, err
	}
	projectDefinition, err := s.store.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Environment{}, warnings, err
	}
	projectDefinition = compiler.RefreshDiscoveredTopology(projectDefinition, environment.Sources)
	compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings)
	if len(compiled.Issues) > 0 {
		environment.Issues = compiled.Issues
		return environment, warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.store.ReplaceProjectAndEnvironmentConfiguration(ctx, projectName, project.Revision, projectDefinition, project.Sources, environmentName, environment.Revision, compiled.Definition, environment.Sources, compiled.Bindings)
	if err != nil {
		return model.Environment{}, warnings, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	_, _ = s.timeline(ctx, scope, "CLI", "environment.rescanned", scope, "info", "Environment sources refreshed", nil)
	return s.decorateEnvironment(updated), warnings, nil
}

func (s *Service) Rename(ctx context.Context, projectName, newName string, revision int64, actor string) (model.Project, error) {
	newName = model.NormalizeDNSName(newName)
	if err := model.ValidateProjectName(newName); err != nil {
		return model.Project{}, err
	}
	project, err := s.store.RenameProject(ctx, projectName, newName, revision)
	if err != nil {
		return model.Project{}, err
	}
	return s.decorateProject(project), nil
}

func (s *Service) Forget(ctx context.Context, projectName string) error {
	return s.store.ForgetProject(ctx, projectName)
}

func (s *Service) Projects(ctx context.Context) ([]model.Project, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		projects[index] = s.decorateProject(projects[index])
	}
	return projects, nil
}

func (s *Service) Project(ctx context.Context, name string) (model.Project, error) {
	project, err := s.store.Project(ctx, name)
	if err != nil {
		return model.Project{}, err
	}
	return s.decorateProject(project), nil
}

func (s *Service) Environments(ctx context.Context, projectName string) ([]model.Environment, error) {
	environments, err := s.store.ListEnvironments(ctx, projectName)
	if err != nil {
		return nil, err
	}
	for index := range environments {
		environments[index] = s.decorateEnvironment(environments[index])
	}
	return environments, nil
}

func (s *Service) Environment(ctx context.Context, projectName, environmentName string) (model.Environment, error) {
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	projectDefinition, definitionErr := s.store.ProjectModel(ctx, projectName)
	if definitionErr == nil {
		environment.Issues = compiler.Compile(projectDefinition, environment.Sources, environment.Bindings).Issues
	}
	return s.decorateEnvironment(environment), nil
}

func (s *Service) CloneEnvironment(ctx context.Context, projectName, from, name string) (model.Environment, error) {
	created, err := s.store.CloneEnvironment(ctx, projectName, from, name)
	if err != nil {
		return model.Environment{}, err
	}
	return s.decorateEnvironment(created), nil
}

func (s *Service) ForgetEnvironment(ctx context.Context, projectName, environmentName string) error {
	return s.store.ForgetEnvironment(ctx, projectName, environmentName)
}

func (s *Service) SetBinding(ctx context.Context, projectName, environmentName, serviceName string, binding model.ComponentBinding) (model.Environment, error) {
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	projectDefinition, err := s.store.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Environment{}, err
	}
	found := false
	for _, service := range projectDefinition.Services {
		if service.Name == serviceName {
			found = true
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
				if service.Kind != model.ServiceContainer {
					return model.Environment{}, errors.New("only managed dependency templates can use the container provider")
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
		return model.Environment{}, store.ErrNotFound
	}
	binding.Service = serviceName
	for index := range environment.Bindings {
		if environment.Bindings[index].Service == serviceName {
			environment.Bindings[index] = binding
			found = true
			break
		}
	}
	compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings)
	if len(compiled.Issues) > 0 {
		return model.Environment{}, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.store.ReplaceEnvironmentConfiguration(ctx, projectName, environmentName, environment.Revision, compiled.Definition, environment.Sources, compiled.Bindings)
	if err != nil {
		return model.Environment{}, err
	}
	return s.decorateEnvironment(updated), nil
}

func (s *Service) SetSource(ctx context.Context, projectName, environmentName, sourceName, path string) (model.Environment, []string, error) {
	if !filepath.IsAbs(path) {
		return model.Environment{}, nil, errors.New("source path must be absolute")
	}
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, nil, err
	}
	if environment.Status != model.EnvironmentStopped {
		return model.Environment{}, nil, errors.New("environment must be stopped before a source changes")
	}
	result, err := discovery.Discover(path)
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
		return model.Environment{}, result.Warnings, store.ErrNotFound
	}
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		return model.Environment{}, result.Warnings, err
	}
	projectDefinition, err := s.store.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Environment{}, result.Warnings, err
	}
	projectDefinition = compiler.RefreshDiscoveredTopology(projectDefinition, environment.Sources)
	compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings)
	if len(compiled.Issues) > 0 {
		return model.Environment{}, result.Warnings, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	updated, err := s.store.ReplaceProjectAndEnvironmentConfiguration(ctx, projectName, project.Revision, projectDefinition, project.Sources, environmentName, environment.Revision, compiled.Definition, environment.Sources, compiled.Bindings)
	if err != nil {
		return model.Environment{}, result.Warnings, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	_, _ = s.timeline(ctx, scope, "CLI", "environment.source_changed", sourceName, "info", "Source "+sourceName+" now uses "+result.Root, nil)
	return s.decorateEnvironment(updated), result.Warnings, nil
}

func (s *Service) SelectEnvironment(ctx context.Context, path, projectName, environmentName string) error {
	return s.store.SetContextSelection(ctx, path, projectName, environmentName)
}

func (s *Service) ClearEnvironmentSelection(ctx context.Context, path string) (bool, error) {
	return s.store.ClearContextSelection(ctx, path)
}

func (s *Service) EnvironmentContext(ctx context.Context, path string) (EnvironmentContext, error) {
	selected, err := s.store.ContextSelection(ctx, path)
	if err == nil {
		selected = s.decorateEnvironment(selected)
		return EnvironmentContext{Resolution: "selected", Environment: &selected, Candidates: []model.Environment{}}, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return EnvironmentContext{}, err
	}
	candidates, err := s.store.EnvironmentsByPath(ctx, path)
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

func (s *Service) ProjectModel(ctx context.Context, name string) (model.ProjectModel, error) {
	return s.store.ProjectModel(ctx, name)
}

func (s *Service) Up(ctx context.Context, projectName, environmentName, actor, idempotencyKey string) (model.Operation, error) {
	s.resetGate.RLock()
	defer s.resetGate.RUnlock()
	if s.resetting {
		return model.Operation{}, errors.New("Portless reset preparation is in progress")
	}
	if _, err := s.store.Environment(ctx, projectName, environmentName); err != nil {
		return model.Operation{}, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.store.CreateOperation(ctx, scope, "up", actor, idempotencyKey)
	if err != nil {
		return model.Operation{}, err
	}
	if operation.State != "running" || len(operation.Events) > 0 {
		return operation, nil
	}
	go s.runUp(scope, operation)
	return operation, nil
}

func (s *Service) Down(ctx context.Context, projectName, environmentName, actor, idempotencyKey string, removeVolumes bool) (model.Operation, error) {
	if _, err := s.store.Environment(ctx, projectName, environmentName); err != nil {
		return model.Operation{}, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.store.CreateOperation(ctx, scope, "down", actor, idempotencyKey)
	if err != nil {
		return model.Operation{}, err
	}
	if operation.State != "running" || len(operation.Events) > 0 {
		return operation, nil
	}
	go s.runDown(scope, operation, removeVolumes)
	return operation, nil
}

func (s *Service) Operation(ctx context.Context, projectName, environmentName string, number int64) (model.Operation, error) {
	return s.store.Operation(ctx, model.EnvironmentSelector(projectName, environmentName), number)
}

func (s *Service) Operations(ctx context.Context, projectName, environmentName string, limit int) ([]model.Operation, error) {
	scope := model.EnvironmentSelector(projectName, environmentName)
	operations, err := s.store.Operations(ctx, scope, limit)
	if err != nil {
		return nil, err
	}
	for index := range operations {
		events, eventErr := s.store.OperationEvents(ctx, scope, operations[index].Number)
		if eventErr != nil {
			return nil, eventErr
		}
		operations[index].Events = events
	}
	return operations, nil
}

func (s *Service) Connections(ctx context.Context, projectName, environmentName string) ([]model.EffectiveConnection, error) {
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return nil, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	result := make([]model.EffectiveConnection, 0, len(environment.Connections))
	for _, connection := range environment.Connections {
		proxyAddress, provider, endpoint := s.proxy.ConnectionRuntime(scope, connection.Source, connection.Target)
		target := runtimeFor(environment, connection.Target)
		if provider == "" {
			provider = bindingForEnvironment(environment, connection.Target).Provider
			if provider == "" {
				provider = model.ProviderLocal
			}
		}
		if endpoint == "" && provider == model.ProviderRemote {
			binding := bindingForEnvironment(environment, connection.Target)
			if binding.Remote != nil {
				endpoint = binding.Remote.URL
			}
		}
		injected := safeInjectedValue(connection, proxyAddress)
		result = append(result, model.EffectiveConnection{
			Connection: connection, TargetProvider: provider, TargetStatus: target.Status,
			ProxyAddress: proxyAddress, TargetEndpoint: endpoint,
			InjectedEnvVar: connection.Environment, InjectedValue: injected,
		})
	}
	return result, nil
}

func (s *Service) ServiceConfiguration(ctx context.Context, projectName, environmentName, serviceName string) (model.ServiceConfiguration, error) {
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.ServiceConfiguration{}, err
	}
	definition, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.ServiceConfiguration{}, store.ErrNotFound
	}
	values := make([]model.ConfigurationValue, 0, len(definition.Environment)+len(environment.Connections))
	for key, value := range definition.Environment {
		classification := "public"
		if shouldMaskConfiguration(key, value) {
			classification, value = "masked", "••••••••"
		}
		values = append(values, model.ConfigurationValue{Key: key, Value: value, Classification: classification, Source: "discovered model"})
	}
	connections, err := s.Connections(ctx, projectName, environmentName)
	if err != nil {
		return model.ServiceConfiguration{}, err
	}
	for _, connection := range connections {
		if connection.Source == serviceName && connection.InjectedEnvVar != "" {
			values = append(values, model.ConfigurationValue{Key: connection.InjectedEnvVar, Value: connection.InjectedValue, Classification: "generated", Source: "Portless connection " + connection.Source + ":" + connection.Target})
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Key < values[j].Key })
	return model.ServiceConfiguration{
		Service: definition.Name, Command: nonNilStrings(definition.Command), WorkingDirectory: definition.WorkingDirectory,
		PortEnvironment: definition.PortEnvironment, Environment: values, Health: definition.Health,
	}, nil
}

func (s *Service) StartService(ctx context.Context, projectName, environmentName, serviceName, actor string) (model.Operation, error) {
	return s.beginServiceStart(ctx, projectName, environmentName, serviceName, actor, false)
}

func (s *Service) RestartService(ctx context.Context, projectName, environmentName, serviceName, actor string) (model.Operation, error) {
	return s.beginServiceStart(ctx, projectName, environmentName, serviceName, actor, true)
}

func (s *Service) beginServiceStart(ctx context.Context, projectName, environmentName, serviceName, actor string, restart bool) (model.Operation, error) {
	s.resetGate.RLock()
	defer s.resetGate.RUnlock()
	if s.resetting {
		return model.Operation{}, errors.New("Portless reset preparation is in progress")
	}
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Operation{}, err
	}
	_, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.Operation{}, store.ErrNotFound
	}
	if bindingForEnvironment(environment, serviceName).Provider == model.ProviderRemote {
		return model.Operation{}, fmt.Errorf("remote service %s is not managed by Portless; change its environment binding before using lifecycle commands", serviceName)
	}
	if !restart {
		for _, connection := range environment.Connections {
			if connection.Source != serviceName || !connection.Required {
				continue
			}
			if bindingForEnvironment(environment, connection.Target).Provider == model.ProviderRemote {
				continue
			}
			dependency := runtimeFor(environment, connection.Target)
			if dependency.Status != model.ServiceReady {
				return model.Operation{}, fmt.Errorf("required dependency %s is %s; start it first or run `portless up`", connection.Target, dependency.Status)
			}
		}
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operationType := "start-service"
	if restart {
		operationType = "restart-service"
	}
	operation, err := s.store.CreateOperation(ctx, scope, operationType, actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	_ = s.operationServiceTarget(scope, operation, serviceName)
	if !restart && runtimeFor(environment, serviceName).Status == model.ServiceReady {
		s.completeOperation(scope, operation, "Service "+serviceName+" is already ready")
		return s.store.Operation(ctx, scope, operation.Number)
	}
	go func() {
		lock := s.projectLock(scope)
		lock.Lock()
		defer lock.Unlock()
		current, currentErr := s.store.Environment(context.Background(), projectName, environmentName)
		if currentErr != nil {
			s.failOperation(scope, operation, currentErr)
			return
		}
		currentDefinition, exists := serviceDefinitionForEnvironment(current, serviceName)
		if !exists {
			s.failOperation(scope, operation, store.ErrNotFound)
			return
		}
		currentRuntime := runtimeFor(current, serviceName)
		if !restart && currentRuntime.Status == model.ServiceReady {
			s.completeOperation(scope, operation, "Service "+serviceName+" is already ready")
			return
		}
		if currentErr = s.prepareServiceDependencies(context.Background(), scope, current, serviceName, operation); currentErr != nil {
			s.failOperation(scope, operation, currentErr)
			return
		}
		if currentDefinition.Kind == model.ServiceProcess {
			if currentErr = s.acquireSourceLeases(scope, current); currentErr != nil {
				s.failOperation(scope, operation, currentErr)
				return
			}
		}
		if restart {
			_ = s.store.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceStopping, "")
			s.reconcileEnvironmentStatus(context.Background(), scope)
			_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Stopping "+serviceName)
			if currentDefinition.Kind == model.ServiceProcess {
				currentErr = s.processes.Stop(context.Background(), scope, serviceName, 10*time.Second)
			} else {
				var privateKey string
				privateKey, currentErr = s.store.PrivateEnvironmentKeyForSelector(context.Background(), scope)
				if currentErr == nil {
					currentErr = s.containers.StopService(context.Background(), privateKey, serviceName)
				}
			}
			if currentErr != nil {
				_ = s.store.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceFailed, currentErr.Error())
				s.failOperation(scope, operation, currentErr)
				return
			}
			s.proxy.RemoveTarget(scope, serviceName)
		}
		_ = s.store.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceStarting, "")
		s.reconcileEnvironmentStatus(context.Background(), scope)
		_ = s.serviceEvent(scope, operation, serviceName, "starting", "Starting "+serviceName)
		increment := int64(0)
		if restart {
			increment = 1
		}
		if currentDefinition.Kind == model.ServiceProcess {
			currentErr = s.startProcess(context.Background(), current, currentDefinition, operation, increment)
		} else {
			currentErr = s.startContainer(context.Background(), current, currentDefinition, increment)
		}
		if currentErr != nil {
			_ = s.store.SetServiceRuntime(context.Background(), scope, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceFailed, Reason: currentErr.Error(), Generation: currentRuntime.Generation, RestartCount: currentRuntime.RestartCount})
			s.failOperation(scope, operation, currentErr)
			s.releaseSourceLeasesIfIdle(scope)
			return
		}
		_ = s.serviceEvent(scope, operation, serviceName, "ready", serviceName+" is ready")
		s.reconcileEnvironmentStatus(context.Background(), scope)
		verb := "started"
		if restart {
			verb = "restarted"
		}
		s.completeOperation(scope, operation, "Service "+serviceName+" "+verb)
	}()
	return operation, nil
}

func (s *Service) StopService(ctx context.Context, projectName, environmentName, serviceName, actor string) (model.Operation, error) {
	environment, err := s.store.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Operation{}, err
	}
	_, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.Operation{}, store.ErrNotFound
	}
	if bindingForEnvironment(environment, serviceName).Provider == model.ProviderRemote {
		return model.Operation{}, fmt.Errorf("remote service %s is not managed by Portless; change its environment binding before using lifecycle commands", serviceName)
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.store.CreateOperation(ctx, scope, "stop-service", actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	_ = s.operationServiceTarget(scope, operation, serviceName)
	runtime := runtimeFor(environment, serviceName)
	if runtime.Status == model.ServiceStopped || runtime.Status == model.ServicePlanned {
		s.completeOperation(scope, operation, "Service "+serviceName+" is already stopped")
		return s.store.Operation(ctx, scope, operation.Number)
	}
	go func() {
		lock := s.projectLock(scope)
		lock.Lock()
		defer lock.Unlock()
		current, currentErr := s.store.Environment(context.Background(), projectName, environmentName)
		if currentErr != nil {
			s.failOperation(scope, operation, currentErr)
			return
		}
		currentDefinition, exists := serviceDefinitionForEnvironment(current, serviceName)
		if !exists {
			s.failOperation(scope, operation, store.ErrNotFound)
			return
		}
		currentRuntime := runtimeFor(current, serviceName)
		if currentRuntime.Status == model.ServiceStopped || currentRuntime.Status == model.ServicePlanned {
			s.completeOperation(scope, operation, "Service "+serviceName+" is already stopped")
			return
		}
		_ = s.store.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceStopping, "")
		s.reconcileEnvironmentStatus(context.Background(), scope)
		_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Stopping service")
		var stopErr error
		if currentDefinition.Kind == model.ServiceProcess {
			stopErr = s.processes.Stop(context.Background(), scope, serviceName, 10*time.Second)
		} else {
			var privateKey string
			privateKey, stopErr = s.store.PrivateEnvironmentKeyForSelector(context.Background(), scope)
			if stopErr == nil {
				stopErr = s.containers.StopService(context.Background(), privateKey, serviceName)
			}
		}
		if stopErr != nil {
			_ = s.store.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceFailed, stopErr.Error())
			s.failOperation(scope, operation, stopErr)
			return
		}
		s.proxy.RemoveTarget(scope, serviceName)
		_ = s.store.SetServiceRuntime(context.Background(), scope, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: currentRuntime.Generation, RestartCount: currentRuntime.RestartCount})
		s.reconcileEnvironmentStatus(context.Background(), scope)
		s.releaseSourceLeasesIfIdle(scope)
		s.completeOperation(scope, operation, "Service "+serviceName+" stopped")
	}()
	return operation, nil
}

func (s *Service) RuntimeStatus(ctx context.Context) container.Status {
	return s.containers.Status(ctx)
}

func (s *Service) StartRuntime(ctx context.Context) container.Status {
	return s.containers.StartHost(ctx)
}

func (s *Service) UseRuntime(ctx context.Context, value string) (container.Status, error) {
	preference, err := container.ParseRuntimeName(value)
	if err != nil {
		return container.Status{}, err
	}
	current := s.containers.Status(ctx)
	if preference != current.Selected {
		environments, err := s.store.ListEnvironments(ctx, "")
		if err != nil {
			return container.Status{}, err
		}
		for _, environment := range environments {
			for _, service := range environment.Services {
				if service.Kind != model.ServiceContainer {
					continue
				}
				switch service.Status {
				case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
					return container.Status{}, RuntimeInUseError{Project: model.EnvironmentSelector(environment.Project, environment.Name)}
				}
			}
		}
	}
	if err := s.containers.SetPreference(preference); err != nil {
		return container.Status{}, err
	}
	return s.containers.Status(ctx), nil
}

func (s *Service) PrepareReset(ctx context.Context) (result ResetRuntimeResult, err error) {
	s.resetGate.Lock()
	defer s.resetGate.Unlock()
	if s.resetting {
		return result, errors.New("Portless reset preparation is already in progress")
	}
	s.resetting = true
	defer func() {
		if err != nil {
			s.resetting = false
		}
	}()
	environments, err := s.store.ListEnvironments(ctx, "")
	if err != nil {
		return result, err
	}
	active := make([]string, 0)
	for _, environment := range environments {
		if environment.Status != model.EnvironmentStopped {
			active = append(active, model.EnvironmentSelector(environment.Project, environment.Name))
		}
	}
	if len(active) > 0 {
		sort.Strings(active)
		return result, ResetActiveEnvironmentsError{Environments: active}
	}
	running, err := s.store.RunningOperationScopes(ctx)
	if err != nil {
		return result, err
	}
	if len(running) > 0 {
		return result, fmt.Errorf("environment operations are still running: %s; wait for them to finish, then retry", strings.Join(running, ", "))
	}
	result.Processes, err = s.stopResetSupervisors(ctx, environments)
	if err != nil {
		return result, err
	}
	result.Runtimes, err = s.containers.ResetInstallations(ctx)
	return result, err
}

func (s *Service) CancelReset() {
	s.resetGate.Lock()
	s.resetting = false
	s.resetGate.Unlock()
}

func (s *Service) stopResetSupervisors(ctx context.Context, environments []model.Environment) (int, error) {
	stopped := 0
	for _, environment := range environments {
		scope := model.EnvironmentSelector(environment.Project, environment.Name)
		for _, service := range environment.Services {
			if service.Kind != model.ServiceProcess {
				continue
			}
			runtime, err := s.store.ServiceRuntime(ctx, scope, service.Name)
			if err != nil {
				return stopped, err
			}
			if runtime.SupervisorSocket == "" && runtime.PrivateRunKey == "" && runtime.SupervisorState == "" {
				continue
			}
			if runtime.SupervisorSocket == "" || runtime.PrivateRunKey == "" || runtime.SupervisorState == "" {
				return stopped, fmt.Errorf("cannot verify previous process runtime %s/%s because its supervisor ownership record is incomplete", scope, service.Name)
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			status, liveErr := supervisor.LiveStatus(probeCtx, runtime.SupervisorSocket, runtime.PrivateRunKey)
			cancel()
			if liveErr != nil {
				status, err = supervisor.StatusFor(ctx, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
				if err != nil {
					return stopped, fmt.Errorf("cannot verify previous process runtime %s/%s: %w", scope, service.Name, liveErr)
				}
				if err := validateResetSupervisor(status, scope, service.Name, runtime.Generation); err != nil {
					return stopped, err
				}
				if !supervisorTerminalState(status.State) {
					return stopped, fmt.Errorf("cannot stop previous process runtime %s/%s because its supervisor is unavailable and persisted state is %s", scope, service.Name, status.State)
				}
				continue
			}
			if err := validateResetSupervisor(status, scope, service.Name, runtime.Generation); err != nil {
				return stopped, err
			}
			if supervisorTerminalState(status.State) {
				continue
			}
			stopCtx, stopCancel := context.WithTimeout(ctx, 12*time.Second)
			status, err = supervisor.Stop(stopCtx, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
			stopCancel()
			if err != nil {
				return stopped, fmt.Errorf("stop previous process runtime %s/%s: %w", scope, service.Name, err)
			}
			if err := validateResetSupervisor(status, scope, service.Name, runtime.Generation); err != nil {
				return stopped, err
			}
			stopped++
		}
	}
	return stopped, nil
}

func validateResetSupervisor(status supervisor.Status, scope, service string, generation int64) error {
	if status.Scope != scope || status.Service != service || status.Generation != generation {
		return fmt.Errorf("previous process supervisor identity does not match %s/%s generation %d", scope, service, generation)
	}
	return nil
}

func (s *Service) Traffic(project, environment string, limit int) []model.TrafficEvent {
	return s.broker.RecentTraffic(model.EnvironmentSelector(project, environment), limit)
}

func (s *Service) TrafficEvent(ctx context.Context, project, environment string, sequence int64) (model.TrafficEvent, error) {
	for _, event := range s.Traffic(project, environment, 1000) {
		if event.Sequence == sequence {
			return event, nil
		}
	}
	recorded, err := s.store.RecordedTraffic(ctx, model.EnvironmentSelector(project, environment), "", 10_000)
	if err != nil {
		return model.TrafficEvent{}, err
	}
	for _, event := range recorded {
		if event.Sequence == sequence {
			return event, nil
		}
	}
	return model.TrafficEvent{}, store.ErrNotFound
}

func (s *Service) RecordedTraffic(ctx context.Context, project, environment, recording string, limit int) ([]model.TrafficEvent, error) {
	return s.store.RecordedTraffic(ctx, model.EnvironmentSelector(project, environment), recording, limit)
}

func (s *Service) Timeline(ctx context.Context, project, environment string, limit int) ([]model.TimelineEvent, error) {
	return s.store.Timeline(ctx, model.EnvironmentSelector(project, environment), limit)
}

func (s *Service) StartRecording(ctx context.Context, recording model.Recording, actor string) (model.Recording, error) {
	if err := model.ValidateArtifactName(recording.Name); err != nil {
		return model.Recording{}, err
	}
	if recording.Project == "" || recording.Environment == "" {
		return model.Recording{}, errors.New("project and environment are required")
	}
	if recording.CaptureBodies {
		return model.Recording{}, errors.New("request and response body capture is not enabled in this build; start a metadata-only recording")
	}
	definition, err := s.store.EnvironmentModel(ctx, recording.Project, recording.Environment)
	if err != nil {
		return model.Recording{}, err
	}
	if err := validateExperimentScope(definition, recording.Source, recording.Target, true); err != nil {
		return model.Recording{}, err
	}
	if recording.MaxEvents < 0 || recording.MaxEvents > 100_000 {
		return model.Recording{}, errors.New("maxEvents must be between 1 and 100000")
	}
	if recording.MaxBodyBytes < 0 || recording.MaxBodyBytes > 1<<20 {
		return model.Recording{}, errors.New("maxBodyBytes must not exceed 1048576")
	}
	if recording.ExpiresAt == nil {
		expires := time.Now().UTC().Add(15 * time.Minute)
		recording.ExpiresAt = &expires
	}
	if !recording.ExpiresAt.After(time.Now()) || recording.ExpiresAt.After(time.Now().Add(time.Hour)) {
		return model.Recording{}, errors.New("recording expiry must be in the future and no more than one hour away")
	}
	created, err := s.store.CreateRecording(ctx, recording)
	if err != nil {
		return model.Recording{}, err
	}
	scope := model.EnvironmentSelector(recording.Project, recording.Environment)
	_, _ = s.timeline(ctx, scope, actor, "recording.started", recording.Name, "info", "Recording "+recording.Name+" started", nil)
	s.broker.Publish(events.Event{Type: "recording.state", Project: recording.Project, Environment: recording.Environment, Data: created})
	return created, nil
}

func (s *Service) StopRecording(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.store.StopRecording(ctx, scope, name, "stopped"); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "recording.stopped", name, "info", "Recording "+name+" stopped", nil)
	s.broker.Publish(events.Event{Type: "recording.state", Project: project, Environment: environment, Data: map[string]any{"name": name, "status": "completed"}})
	return nil
}

func (s *Service) Recordings(ctx context.Context, project, environment string) ([]model.Recording, error) {
	return s.store.Recordings(ctx, model.EnvironmentSelector(project, environment))
}

func (s *Service) Recording(ctx context.Context, project, environment, name string) (model.Recording, error) {
	return s.store.Recording(ctx, model.EnvironmentSelector(project, environment), name)
}

func (s *Service) DeleteRecording(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.store.DeleteRecording(ctx, scope, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "recording.deleted", name, "warning", "Recording "+name+" deleted", nil)
	return nil
}

func (s *Service) CreateFault(ctx context.Context, fault model.FaultRule, actor string) (model.FaultRule, error) {
	if err := model.ValidateArtifactName(fault.Name); err != nil {
		return model.FaultRule{}, err
	}
	if fault.Probability == 0 {
		fault.Probability = 1
	}
	if fault.Probability < 0 || fault.Probability > 1 {
		return model.FaultRule{}, errors.New("probability must be between 0 and 1")
	}
	if fault.Project == "" || fault.Environment == "" {
		return model.FaultRule{}, errors.New("project and environment are required")
	}
	definition, err := s.store.EnvironmentModel(ctx, fault.Project, fault.Environment)
	if err != nil {
		return model.FaultRule{}, err
	}
	if err := validateExperimentScope(definition, fault.Source, fault.Target, false); err != nil {
		return model.FaultRule{}, err
	}
	if fault.LatencyMS < 0 || fault.JitterMS < 0 || fault.LatencyMS+fault.JitterMS > 60_000 {
		return model.FaultRule{}, errors.New("latency plus jitter must be between 0 and 60000 milliseconds")
	}
	if fault.StatusCode != 0 && (fault.StatusCode < 400 || fault.StatusCode > 599) {
		return model.FaultRule{}, errors.New("synthetic status must be between 400 and 599")
	}
	if fault.LatencyMS == 0 && fault.JitterMS == 0 && fault.StatusCode == 0 && !fault.Abort {
		return model.FaultRule{}, errors.New("fault must define latency, jitter, a synthetic status, or an abort")
	}
	if strings.ContainsAny(fault.Method, " \t\r\n") {
		return model.FaultRule{}, errors.New("HTTP method filter must be a single token")
	}
	fault.Method = strings.ToUpper(fault.Method)
	if fault.Path != "" {
		if _, err := pathmatch.Match(fault.Path, "/validation"); err != nil {
			return model.FaultRule{}, fmt.Errorf("invalid path glob: %w", err)
		}
	}
	if fault.ExpiresAt != nil && !fault.ExpiresAt.After(time.Now()) {
		return model.FaultRule{}, errors.New("fault expiry must be in the future")
	}
	created, err := s.store.CreateFault(ctx, fault)
	if err != nil {
		return model.FaultRule{}, err
	}
	scope := model.EnvironmentSelector(fault.Project, fault.Environment)
	_, _ = s.timeline(ctx, scope, actor, "fault.enabled", fault.Name, "warning", created.ScopeSummary, nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: fault.Project, Environment: fault.Environment, Data: created})
	return created, nil
}

func (s *Service) Faults(ctx context.Context, project, environment string) ([]model.FaultRule, error) {
	return s.store.Faults(ctx, model.EnvironmentSelector(project, environment), false)
}

func (s *Service) Fault(ctx context.Context, project, environment, name string) (model.FaultRule, error) {
	return s.store.Fault(ctx, model.EnvironmentSelector(project, environment), name)
}

func (s *Service) EnableFault(ctx context.Context, project, environment, name, actor string) (model.FaultRule, error) {
	scope := model.EnvironmentSelector(project, environment)
	fault, err := s.store.Fault(ctx, scope, name)
	if err != nil {
		return model.FaultRule{}, err
	}
	if fault.ExpiresAt != nil && !fault.ExpiresAt.After(time.Now()) {
		return model.FaultRule{}, fmt.Errorf("fault %s has expired; delete it and create a new rule", name)
	}
	if fault.Enabled {
		return fault, nil
	}
	definition, err := s.store.EnvironmentModel(ctx, project, environment)
	if err != nil {
		return model.FaultRule{}, err
	}
	if err := validateExperimentScope(definition, fault.Source, fault.Target, false); err != nil {
		return model.FaultRule{}, err
	}
	if err := s.store.EnableFault(ctx, scope, name); err != nil {
		return model.FaultRule{}, err
	}
	fault, err = s.store.Fault(ctx, scope, name)
	if err != nil {
		return model.FaultRule{}, err
	}
	_, _ = s.timeline(ctx, scope, actor, "fault.enabled", name, "warning", "Fault "+name+" enabled", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: fault})
	return fault, nil
}

func (s *Service) DisableFault(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.store.DisableFault(ctx, scope, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "fault.disabled", name, "info", "Fault "+name+" disabled", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: map[string]any{"name": name, "enabled": false}})
	return nil
}

func (s *Service) DeleteFault(ctx context.Context, project, environment, name, actor string) error {
	scope := model.EnvironmentSelector(project, environment)
	if err := s.store.DeleteFault(ctx, scope, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, scope, actor, "fault.deleted", name, "warning", "Fault "+name+" deleted", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: map[string]any{"name": name, "enabled": false, "deleted": true}})
	return nil
}

func (s *Service) DisableAllFaults(ctx context.Context, project, environment, actor string) (int64, error) {
	scope := model.EnvironmentSelector(project, environment)
	count, err := s.store.DisableAllFaults(ctx, scope)
	if err != nil {
		return 0, err
	}
	_, _ = s.timeline(ctx, scope, actor, "faults.disabled_all", scope, "info", fmt.Sprintf("Disabled %d active faults", count), nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Environment: environment, Data: map[string]any{"enabled": false, "count": count}})
	return count, nil
}

func (s *Service) Logs(ctx context.Context, project, environment, service string, limit int, since time.Time) ([]model.LogEntry, error) {
	current, err := s.store.Environment(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	services := make([]string, 0, len(current.Services))
	for _, candidate := range current.Services {
		if service == "" || strings.EqualFold(candidate.Name, service) {
			services = append(services, candidate.Name)
		}
	}
	if service != "" && len(services) == 0 {
		return nil, store.ErrNotFound
	}
	privateKey, err := s.store.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(s.dataDirectory, "environments", privateKey, "logs")
	return logstore.Read(root, services, limit, since)
}

func (s *Service) ExportProject(ctx context.Context, project string) ([]byte, error) {
	definition, err := s.store.ProjectModel(ctx, project)
	if err != nil {
		return nil, err
	}
	definition.SuggestedName = project
	return json.MarshalIndent(struct {
		SchemaVersion int `json:"schemaVersion"`
		model.ProjectModel
	}{SchemaVersion: 1, ProjectModel: definition}, "", "  ")
}

func (s *Service) Proxy() *proxy.Manager { return s.proxy }

func (s *Service) Broker() *events.Broker { return s.broker }

func (s *Service) runUp(scope string, operation model.Operation) {
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	environment, err := s.store.EnvironmentBySelector(ctx, scope)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	if environment.Status == model.EnvironmentHealthy && s.environmentRuntimeVerified(ctx, environment) {
		s.completeOperation(scope, operation, "Environment is already healthy")
		return
	}
	if environment.Status != model.EnvironmentStopped && !s.environmentRuntimeVerified(ctx, environment) {
		_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentRecovering, "runtime ownership is stale")
		if err := s.reconcileActiveEnvironmentLocked(ctx, environment); err != nil {
			s.failOperation(scope, operation, fmt.Errorf("runtime recovery failed: %w", err))
			return
		}
		if environment, err = s.store.EnvironmentBySelector(ctx, scope); err != nil {
			s.failOperation(scope, operation, err)
			return
		}
		if environment.Status == model.EnvironmentHealthy {
			s.completeOperation(scope, operation, "Environment runtime was recovered")
			return
		}
		for _, service := range environment.Services {
			if service.Status == model.ServiceUnknown || service.Status == model.ServiceRecovering {
				s.failOperation(scope, operation, fmt.Errorf("runtime recovery is incomplete: %s", environment.Reason))
				return
			}
		}
	}
	if err := s.acquireSourceLeases(scope, environment); err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentStarting, "services are starting")
	s.publish(scope, "environment.state", map[string]any{"status": model.EnvironmentStarting})
	definition, err := s.store.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	for _, binding := range environment.Bindings {
		if binding.Provider != model.ProviderRemote || binding.Remote == nil {
			continue
		}
		_ = s.serviceEvent(scope, operation, binding.Service, "starting", "Connecting "+binding.Service+" to "+string(binding.Remote.Classification))
		if err := s.proxy.SetRemoteTarget(scope, binding.Service, *binding.Remote); err != nil {
			s.failOperation(scope, operation, err)
			return
		}
		if binding.Remote.HealthPath != "" {
			checkCtx, checkCancel := context.WithTimeout(ctx, 15*time.Second)
			err = s.proxy.CheckRemote(checkCtx, scope, binding.Service)
			checkCancel()
			if err != nil {
				_ = s.store.SetServiceRuntime(context.Background(), scope, binding.Service, store.ServiceRuntimeUpdate{Status: model.ServiceFailed, Reason: "remote health check failed: " + err.Error()})
				s.failOperation(scope, operation, fmt.Errorf("%s remote health check: %w", binding.Service, err))
				return
			}
		}
		now := time.Now().UTC()
		_ = s.store.SetServiceRuntime(ctx, scope, binding.Service, store.ServiceRuntimeUpdate{
			Status: model.ServiceReady, Reason: "remote " + string(binding.Remote.Classification) + " target",
			OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now,
		})
		_ = s.serviceEvent(scope, operation, binding.Service, "ready", binding.Service+" is routed to "+string(binding.Remote.Classification))
	}
	order, err := executionOrder(definition, environment.Bindings)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	for _, serviceName := range order {
		if current := runtimeFor(environment, serviceName); current.Status == model.ServiceReady && s.proxy.HasTarget(scope, serviceName) {
			_ = s.serviceEvent(scope, operation, serviceName, "ready", serviceName+" is already ready")
			continue
		}
		service, _ := serviceDefinition(definition, serviceName)
		_ = s.serviceEvent(scope, operation, serviceName, "starting", "Starting "+serviceName)
		if service.Kind == model.ServiceContainer {
			err = s.startContainer(ctx, environment, service, 0)
		} else {
			err = s.startProcess(ctx, environment, service, operation, 0)
		}
		if err != nil {
			runtime := runtimeFor(environment, serviceName)
			_ = s.store.SetServiceRuntime(context.Background(), scope, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceFailed, Reason: err.Error(), Generation: runtime.Generation})
			s.failOperation(scope, operation, fmt.Errorf("%s: %w", serviceName, err))
			return
		}
		_ = s.serviceEvent(scope, operation, serviceName, "ready", serviceName+" is ready")
		environment, _ = s.store.Environment(ctx, environment.Project, environment.Name)
	}
	_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentHealthy, "")
	_, _ = s.timeline(ctx, scope, operation.Actor, "environment.healthy", scope, "info", "All required local and remote services are ready", nil)
	s.completeOperation(scope, operation, "Environment is healthy")
	snapshot, _ := s.Environment(ctx, environment.Project, environment.Name)
	s.publish(scope, "environment.state", snapshot)
}

func (s *Service) runDown(scope string, operation model.Operation, removeVolumes bool) {
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	environment, err := s.store.EnvironmentBySelector(ctx, scope)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	definition, err := s.store.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	order, err := executionOrder(definition, environment.Bindings)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentStopping, "services are stopping")
	for _, serviceName := range reverse(order) {
		service, _ := serviceDefinition(definition, serviceName)
		if service.Kind != model.ServiceProcess {
			continue
		}
		_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Stopping "+serviceName)
		if err := s.processes.Stop(ctx, scope, serviceName, 10*time.Second); err != nil {
			s.failOperation(scope, operation, err)
			return
		}
		runtime := runtimeFor(environment, serviceName)
		_ = s.store.SetServiceRuntime(ctx, scope, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: runtime.Generation, RestartCount: runtime.RestartCount})
		s.proxy.RemoveTarget(scope, serviceName)
	}
	privateKey, err := s.store.PrivateEnvironmentKeyForSelector(ctx, scope)
	containerDefined, containerMayBeRunning := false, false
	for _, service := range environment.Services {
		if service.Kind != model.ServiceContainer {
			continue
		}
		containerDefined = true
		switch service.Status {
		case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
			containerMayBeRunning = true
		}
	}
	if err == nil && containerDefined {
		probe := s.containers.Status(ctx)
		if probe.State != "ready" && containerMayBeRunning {
			s.failOperation(scope, operation, fmt.Errorf("container runtime is unavailable while managed containers may still be running: %s", probe.Reason))
			return
		}
		if probe.State == "ready" {
			if err := s.containers.StopEnvironment(ctx, privateKey, removeVolumes); err != nil {
				s.failOperation(scope, operation, err)
				return
			}
		}
	}
	for _, service := range environment.Services {
		_ = s.store.SetServiceRuntime(ctx, scope, service.Name, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: service.Generation})
	}
	s.proxy.CloseEnvironment(ctx, scope)
	_ = s.store.DeleteConnectionRuntimes(ctx, scope)
	_, _ = s.store.DisableAllFaults(ctx, scope)
	for _, recording := range mustRecordings(s.store.Recordings(ctx, scope)) {
		if recording.Status == "active" {
			_ = s.store.StopRecording(ctx, scope, recording.Name, "stopped")
		}
	}
	_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentStopped, "")
	s.releaseSourceLeases(scope)
	_, _ = s.timeline(ctx, scope, operation.Actor, "environment.stopped", scope, "info", "Environment stopped", map[string]any{"volumesRemoved": removeVolumes})
	s.completeOperation(scope, operation, "Environment stopped")
	snapshot, _ := s.Environment(ctx, environment.Project, environment.Name)
	s.publish(scope, "environment.state", snapshot)
}

func (s *Service) startContainer(ctx context.Context, environment model.Environment, definition model.ServiceDefinition, restartIncrement int64) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	privateKey, err := s.store.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return err
	}
	runtime := runtimeFor(environment, definition.Name)
	nextGeneration := runtime.Generation + 1
	observed := time.Now().UTC()
	if err := s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceStarting, Generation: nextGeneration,
		RestartCount:    runtime.RestartCount + restartIncrement,
		OwnerInstanceID: s.daemonInstanceID, ObservedAt: &observed,
	}); err != nil {
		return err
	}
	logsRoot := filepath.Join(s.dataDirectory, "environments", privateKey, "logs")
	result, err := s.containers.Start(ctx, environment.Project+"-"+environment.Name, privateKey, definition, nextGeneration, logsRoot)
	if err != nil {
		return err
	}
	s.proxy.SetTargetProvider(scope, definition.Name, result.Port, model.ProviderContainer)
	s.mu.Lock()
	s.containerEnvironment[targetEnvironmentKey(scope, definition.Name)] = result.Environment
	s.mu.Unlock()
	now := time.Now().UTC()
	return s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: nextGeneration, UpstreamPort: result.Port, StartedAt: &result.StartedAt, LogPath: result.LogDirectory, RestartCount: runtime.RestartCount + restartIncrement,
		OwnerInstanceID: s.daemonInstanceID, ContainerName: result.ContainerName, ObservedAt: &now,
	})
}

func (s *Service) startProcess(ctx context.Context, environment model.Environment, definition model.ServiceDefinition, operation model.Operation, restartIncrement int64) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	processEnvironment := make(map[string]string)
	modelDefinition, err := s.store.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	runtime := runtimeFor(environment, definition.Name)
	nextGeneration := runtime.Generation + 1
	for _, connection := range modelDefinition.Connections {
		if connection.Source != definition.Name {
			continue
		}
		port := 0
		persisted, persistedErr := s.store.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
		if persistedErr != nil && !errors.Is(persistedErr, store.ErrNotFound) {
			return persistedErr
		}
		if persistedErr == nil && persisted.SourceGeneration == nextGeneration {
			port, err = s.proxy.EnsureEdgeAtPort(ctx, scope, connection, persisted.ListenPort)
		} else {
			port, err = s.proxy.EnsureEdge(ctx, scope, connection)
		}
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := s.store.SaveConnectionRuntime(ctx, scope, store.ConnectionRuntime{
			Source: connection.Source, Target: connection.Target, Protocol: connection.Protocol,
			SourceGeneration: nextGeneration, ListenPort: port, OwnerInstanceID: s.daemonInstanceID,
			State: "ready", ObservedAt: &now,
		}); err != nil {
			return err
		}
		applyBinding(processEnvironment, connection, port, s.containerEnvironmentFor(scope, connection.Target))
	}
	privateKey, err := s.store.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return err
	}
	logsRoot := filepath.Join(s.dataDirectory, "environments", privateKey, "logs")
	result, err := s.processes.StartPrepared(ctx, scope, definition, nextGeneration, processEnvironment, logsRoot, func(prepared processruntime.StartResult) error {
		now := time.Now().UTC()
		return s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
			Status: model.ServiceStarting, Generation: prepared.Generation, UpstreamPort: prepared.Port,
			StartedAt: &prepared.StartedAt, RestartCount: runtime.RestartCount + restartIncrement,
			LogPath: prepared.LogDirectory, PrivateRunKey: prepared.PrivateRunKey,
			OwnerInstanceID: s.daemonInstanceID, SupervisorSocket: prepared.SupervisorSocket,
			SupervisorState: prepared.SupervisorState, SupervisorPID: prepared.SupervisorPID, ObservedAt: &now,
		})
	})
	if err != nil {
		return err
	}
	s.proxy.SetTarget(scope, definition.Name, result.Port)
	now := time.Now().UTC()
	return s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: result.Generation, PID: result.PID, UpstreamPort: result.Port,
		StartedAt: &result.StartedAt, RestartCount: runtime.RestartCount + restartIncrement, LogPath: result.LogDirectory, PrivateRunKey: result.PrivateRunKey,
		OwnerInstanceID: s.daemonInstanceID, SupervisorSocket: result.SupervisorSocket,
		SupervisorState: result.SupervisorState, SupervisorPID: result.SupervisorPID, ObservedAt: &now,
	})
}

func (s *Service) processExited(event processruntime.ExitEvent) {
	if event.Expected {
		return
	}
	ctx := context.Background()
	environment, err := s.store.EnvironmentBySelector(ctx, event.Scope)
	if err != nil {
		return
	}
	runtime := runtimeFor(environment, event.Service)
	if runtime.Generation != event.Generation {
		return
	}
	status := model.ServiceExited
	reason := "process exited"
	if event.Error != nil {
		status = model.ServiceFailed
		reason = event.Error.Error()
	}
	// Preserve the supervisor identity and run metadata. A later daemon must be
	// able to authenticate the durable terminal state instead of degrading a
	// known exit into an unverifiable process.
	_ = s.store.SetServiceStatus(ctx, event.Scope, event.Service, status, reason)
	s.proxy.RemoveTarget(event.Scope, event.Service)
	s.reconcileEnvironmentStatus(ctx, event.Scope)
	s.releaseSourceLeasesIfIdle(event.Scope)
	_, _ = s.timeline(ctx, event.Scope, "runtime", "service.exited", event.Service, "error", event.Service+" exited: "+reason, nil)
	s.publish(event.Scope, "service.state", map[string]any{"service": event.Service, "state": status, "reason": reason})
}

func (s *Service) completeOperation(scope string, operation model.Operation, message string) {
	ctx := context.Background()
	_, _ = s.store.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "operation.completed", Message: message})
	_ = s.store.CompleteOperation(ctx, scope, operation.Number, "succeeded", "")
	completed, _ := s.store.Operation(ctx, scope, operation.Number)
	s.publish(scope, "operation.state", completed)
}

func (s *Service) operationServiceTarget(scope string, operation model.Operation, serviceName string) error {
	_, err := s.store.AddOperationEvent(context.Background(), scope, operation.Number, model.OperationEvent{
		Type: "operation.accepted", Subject: serviceName, Message: "Service operation accepted",
	})
	return err
}

func (s *Service) prepareServiceDependencies(ctx context.Context, scope string, environment model.Environment, serviceName string, operation model.Operation) error {
	for _, connection := range environment.Connections {
		if connection.Source != serviceName {
			continue
		}
		binding := bindingForEnvironment(environment, connection.Target)
		if binding.Provider != model.ProviderRemote {
			if connection.Required {
				dependency := runtimeFor(environment, connection.Target)
				if dependency.Status != model.ServiceReady {
					return fmt.Errorf("required dependency %s is %s; start it first or run `portless up`", connection.Target, dependency.Status)
				}
			}
			continue
		}
		if binding.Remote == nil {
			return fmt.Errorf("remote dependency %s has no target configuration", connection.Target)
		}
		_ = s.serviceEvent(scope, operation, connection.Target, "starting", "Connecting "+connection.Target+" to "+string(binding.Remote.Classification))
		if err := s.proxy.SetRemoteTarget(scope, connection.Target, *binding.Remote); err != nil {
			return err
		}
		if binding.Remote.HealthPath != "" {
			checkContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.proxy.CheckRemote(checkContext, scope, connection.Target)
			cancel()
			if err != nil {
				_ = s.store.SetServiceStatus(context.Background(), scope, connection.Target, model.ServiceFailed, "remote health check failed: "+err.Error())
				return fmt.Errorf("%s remote health check: %w", connection.Target, err)
			}
		}
		remoteRuntime := runtimeFor(environment, connection.Target)
		now := time.Now().UTC()
		if err := s.store.SetServiceRuntime(ctx, scope, connection.Target, store.ServiceRuntimeUpdate{
			Status: model.ServiceReady, Reason: "remote " + string(binding.Remote.Classification) + " target",
			Generation: remoteRuntime.Generation, RestartCount: remoteRuntime.RestartCount,
			OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now,
		}); err != nil {
			return err
		}
		_ = s.serviceEvent(scope, operation, connection.Target, "ready", connection.Target+" is routed to "+string(binding.Remote.Classification))
	}
	return nil
}

func (s *Service) reconcileEnvironmentStatus(ctx context.Context, scope string) {
	environment, err := s.store.EnvironmentBySelector(ctx, scope)
	if err != nil {
		return
	}
	definition, err := s.store.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		return
	}
	order, _ := executionOrder(definition, environment.Bindings)
	required := make(map[string]struct{}, len(order)+len(environment.Bindings))
	for _, name := range order {
		required[name] = struct{}{}
	}
	for _, binding := range environment.Bindings {
		if binding.Provider == model.ProviderRemote {
			required[binding.Service] = struct{}{}
		}
	}
	var services []model.Service
	for _, service := range environment.Services {
		if _, ok := required[service.Name]; ok {
			services = append(services, service)
		}
	}
	status, reason := model.DeriveEnvironmentStatus(services, "")
	_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, status, reason)
	s.publish(scope, "environment.state", map[string]any{"status": status, "reason": reason})
}

func (s *Service) failOperation(scope string, operation model.Operation, err error) {
	ctx := context.Background()
	_, _ = s.store.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "operation.failed", Message: err.Error()})
	_ = s.store.CompleteOperation(ctx, scope, operation.Number, "failed", err.Error())
	if environment, lookupErr := s.store.EnvironmentBySelector(ctx, scope); lookupErr == nil {
		_ = s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentFailed, err.Error())
	}
	_, _ = s.timeline(ctx, scope, operation.Actor, "operation.failed", scope, "error", err.Error(), map[string]any{"operation": operation.Number})
	failed, _ := s.store.Operation(ctx, scope, operation.Number)
	s.publish(scope, "operation.state", failed)
}

func (s *Service) serviceEvent(scope string, operation model.Operation, service, state, message string) error {
	ctx := context.Background()
	_, err := s.store.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "service." + state, Subject: service, Message: message})
	s.publish(scope, "service.state", map[string]any{"service": service, "state": state})
	return err
}

func (s *Service) timeline(ctx context.Context, scope, actor, eventType, subject, severity, summary string, details map[string]any) (model.TimelineEvent, error) {
	project, environment := scopeNames(scope)
	event, err := s.store.AddTimelineEvent(ctx, model.TimelineEvent{Project: project, Environment: environment, Actor: actor, Type: eventType, Subject: subject, Severity: severity, Summary: summary, Details: details})
	if err == nil {
		s.broker.Publish(events.Event{Type: "timeline", Project: project, Environment: environment, Data: event})
	}
	return event, err
}

func (s *Service) decorateProject(project model.Project) model.Project {
	project.DashboardURL = fmt.Sprintf("http://portless.localhost/projects/%s", project.Name)
	for index := range project.Environments {
		project.Environments[index].DashboardURL = fmt.Sprintf("http://portless.localhost/environments/%s/%s", project.Name, project.Environments[index].Name)
	}
	return project
}

func (s *Service) decorateEnvironment(environment model.Environment) model.Environment {
	environment.DashboardURL = fmt.Sprintf("http://portless.localhost/environments/%s/%s", environment.Project, environment.Name)
	recentTraffic := s.broker.RecentTraffic(model.EnvironmentSelector(environment.Project, environment.Name), 1000)
	cutoff := time.Now().UTC().Add(-30 * time.Second)
	requestDurations := make(map[string][]int64)
	for _, event := range recentTraffic {
		if event.Protocol != model.ProtocolHTTP || event.CompletedAt.Before(cutoff) {
			continue
		}
		requestDurations[event.Target] = append(requestDurations[event.Target], event.DurationMS)
	}
	for index := range environment.Services {
		if environment.Services[index].Kind == model.ServiceProcess {
			environment.Services[index].IngressURL = fmt.Sprintf("http://%s.%s.%s.localhost", environment.Services[index].Name, environment.Name, environment.Project)
		}
		durations := requestDurations[environment.Services[index].Name]
		environment.Services[index].RecentRequest = int64(len(durations))
		if len(durations) > 0 {
			sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
			percentileIndex := (95*len(durations) + 99) / 100
			environment.Services[index].P95Millis = durations[percentileIndex-1]
		}
	}
	return environment
}

func (s *Service) publish(scope, eventType string, data any) {
	project, environment := scopeNames(scope)
	s.broker.Publish(events.Event{Type: eventType, Project: project, Environment: environment, Data: data})
}

func (s *Service) projectLock(project string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.projectLocks[project]
	if lock == nil {
		lock = &sync.Mutex{}
		s.projectLocks[project] = lock
	}
	return lock
}

func (s *Service) containerEnvironmentFor(project, target string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	input := s.containerEnvironment[targetEnvironmentKey(project, target)]
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (s *Service) acquireSourceLeases(scope string, environment model.Environment) error {
	used := make(map[string]struct{})
	for _, binding := range environment.Bindings {
		if binding.Provider == model.ProviderLocal {
			used[binding.Source] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range environment.Sources {
		if _, ok := used[source.Name]; !ok {
			continue
		}
		if owner := s.sourceLeases[source.Path]; owner != "" && owner != scope {
			return fmt.Errorf("source %s is already running in %s; bind a Git worktree to run both environments concurrently", source.Path, owner)
		}
	}
	for _, source := range environment.Sources {
		if _, ok := used[source.Name]; ok {
			s.sourceLeases[source.Path] = scope
		}
	}
	return nil
}

func (s *Service) releaseSourceLeases(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, owner := range s.sourceLeases {
		if owner == scope {
			delete(s.sourceLeases, path)
		}
	}
}

func (s *Service) releaseSourceLeasesIfIdle(scope string) {
	environment, err := s.store.EnvironmentBySelector(context.Background(), scope)
	if err != nil {
		return
	}
	for _, service := range environment.Services {
		if service.Kind != model.ServiceProcess || bindingForEnvironment(environment, service.Name).Provider != model.ProviderLocal {
			continue
		}
		switch service.Status {
		case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
			return
		}
	}
	s.releaseSourceLeases(scope)
}

func applyBinding(environment map[string]string, connection model.Connection, port int, targetEnvironment map[string]string) {
	address := "127.0.0.1:" + strconv.Itoa(port)
	switch connection.Protocol {
	case model.ProtocolHTTP:
		environment[connection.Environment] = "http://" + address
	case model.ProtocolPostgres:
		user := targetEnvironment["POSTGRES_USER"]
		password := targetEnvironment["POSTGRES_PASSWORD"]
		database := targetEnvironment["POSTGRES_DB"]
		if user == "" {
			user, database = "portless", "portless"
		}
		if strings.Contains(connection.Environment, "SPRING_DATASOURCE") {
			environment[connection.Environment] = "jdbc:postgresql://" + address + "/" + database
			environment["SPRING_DATASOURCE_USERNAME"] = user
			environment["SPRING_DATASOURCE_PASSWORD"] = password
		} else {
			environment[connection.Environment] = "postgresql://" + url.QueryEscape(user) + ":" + url.QueryEscape(password) + "@" + address + "/" + database
		}
	case model.ProtocolRedis:
		environment[connection.Environment] = "redis://" + address
	}
}

func safeInjectedValue(connection model.Connection, address string) string {
	if address == "" {
		return "not active"
	}
	switch connection.Protocol {
	case model.ProtocolHTTP:
		return "http://" + address
	case model.ProtocolPostgres:
		if strings.Contains(connection.Environment, "SPRING_DATASOURCE") {
			return "jdbc:postgresql://" + address + "/portless"
		}
		return "postgresql://••••@" + address + "/portless"
	case model.ProtocolRedis:
		return "redis://" + address
	default:
		return address
	}
}

func secretConfigurationKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "API_KEY") || strings.HasSuffix(upper, "_KEY")
}

func shouldMaskConfiguration(key, value string) bool {
	if secretConfigurationKey(key) {
		return true
	}
	parsed, err := url.Parse(strings.TrimPrefix(value, "jdbc:"))
	return err == nil && parsed.User != nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func serviceDefinitionForEnvironment(environment model.Environment, name string) (model.ServiceDefinition, bool) {
	for _, service := range environment.Services {
		if service.Name == name {
			return service.ServiceDefinition, true
		}
	}
	return model.ServiceDefinition{}, false
}

func runtimeFor(environment model.Environment, name string) model.Service {
	for _, service := range environment.Services {
		if service.Name == name {
			return service
		}
	}
	return model.Service{ServiceDefinition: model.ServiceDefinition{Name: name}}
}

func bindingForEnvironment(environment model.Environment, name string) model.ComponentBinding {
	for _, binding := range environment.Bindings {
		if binding.Service == name {
			return binding
		}
	}
	return model.ComponentBinding{Service: name}
}

func scopeNames(scope string) (string, string) {
	project, environment, err := model.ParseEnvironmentSelector(scope)
	if err != nil {
		return scope, "local"
	}
	return project, environment
}

func projectNameSuggestions(name, root string) []string {
	parent := model.NormalizeDNSName(filepath.Base(filepath.Dir(root)))
	var suggestions []string
	if parent != "" && parent != name {
		suggestions = append(suggestions, model.NormalizeDNSName(name+"-"+parent))
	}
	suggestions = append(suggestions, model.NormalizeDNSName(name+"-checkout"), model.NormalizeDNSName(name+"-dev"))
	return suggestions
}

func validateExperimentScope(definition model.ProjectModel, source, target string, allowPartial bool) error {
	if !allowPartial && (source == "" || target == "") {
		return errors.New("fault source and target are required")
	}
	services := make(map[string]model.ServiceDefinition, len(definition.Services))
	for _, service := range definition.Services {
		services[service.Name] = service
	}
	if source != "" && source != "external" {
		if _, ok := services[source]; !ok {
			return fmt.Errorf("source service %s does not exist", source)
		}
	}
	if target != "" {
		if _, ok := services[target]; !ok {
			return fmt.Errorf("target service %s does not exist", target)
		}
	}
	if source == "external" && target != "" && services[target].Kind != model.ServiceProcess {
		return fmt.Errorf("external ingress is only available for process services; %s is a managed container", target)
	}
	if source == "external" && target != "" && target != definition.PrimaryService {
		return invalidExperimentConnection(definition, source, target)
	}
	if source != "" && source != "external" && target != "" {
		for _, connection := range definition.Connections {
			if connection.Source == source && connection.Target == target {
				return nil
			}
		}
		return invalidExperimentConnection(definition, source, target)
	}
	return nil
}

func invalidExperimentConnection(definition model.ProjectModel, source, target string) error {
	available := make([]string, 0, len(definition.Connections)+1)
	if definition.PrimaryService != "" {
		for _, service := range definition.Services {
			if service.Name == definition.PrimaryService && service.Kind == model.ServiceProcess {
				available = append(available, "external → "+definition.PrimaryService)
				break
			}
		}
	}
	for _, connection := range definition.Connections {
		available = append(available, connection.Source+" → "+connection.Target)
	}
	if len(available) == 0 {
		return fmt.Errorf("%s → %s is not a configured connection; this project has no configurable connections", source, target)
	}
	return fmt.Errorf("%s → %s is not a configured connection; choose one of: %s", source, target, strings.Join(available, ", "))
}

func targetEnvironmentKey(project, service string) string { return project + "\x00" + service }

func mustRecordings(recordings []model.Recording, err error) []model.Recording {
	if err != nil {
		return nil
	}
	return recordings
}

func mustLogPath(path string, err error) string {
	if err != nil {
		return ""
	}
	return path
}
