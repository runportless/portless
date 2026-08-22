package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/compiler"
)

type bindingChangePlan struct {
	environment model.Environment
	binding     model.ComponentBinding
	bindings    []model.ComponentBinding
	definition  model.ProjectModel
}

// ChangeBinding starts a durable provider transition for one service. Stopped
// environments only update configuration; active environments hand off the
// selected service while keeping unrelated runtimes and proxy listeners intact.
func (s *Service) ChangeBinding(ctx context.Context, projectName, environmentName, serviceName string, binding model.ComponentBinding, actor, idempotencyKey string) (model.Operation, error) {
	s.resetGate.RLock()
	defer s.resetGate.RUnlock()
	if s.resetting {
		return model.Operation{}, errors.New("Portless reset preparation is in progress")
	}
	plan, err := s.prepareBindingChange(ctx, projectName, environmentName, serviceName, binding)
	if err != nil {
		return model.Operation{}, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	fingerprintBinding := plan.binding
	fingerprintBinding.ModifiedAt = time.Time{}
	operation, err := s.database.CreateOperation(ctx, scope, "change-provider", actor, idempotencyKey, operationFingerprint("change-provider", plan.binding.Service, fingerprintBinding))
	if err != nil {
		return model.Operation{}, err
	}
	if operation.Events != nil {
		return operation, nil
	}
	go s.runBindingChange(scope, operation, plan.binding)
	return operation, nil
}

func (s *Service) prepareBindingChange(ctx context.Context, projectName, environmentName, serviceName string, binding model.ComponentBinding) (bindingChangePlan, error) {
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return bindingChangePlan{}, err
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return bindingChangePlan{}, err
	}
	var declared model.ServiceDefinition
	found := false
	for _, service := range projectDefinition.Services {
		if strings.EqualFold(service.Name, serviceName) {
			declared, serviceName, found = service, service.Name, true
			break
		}
	}
	if !found {
		return bindingChangePlan{}, database.ErrNotFound
	}
	switch binding.Provider {
	case model.ProviderLocal:
		if declared.Kind != model.ServiceProcess {
			return bindingChangePlan{}, errors.New("only application services can use a local checkout provider")
		}
		if binding.Source == "" {
			return bindingChangePlan{}, errors.New("a local provider requires a source checkout")
		}
		sourceFound := false
		for _, source := range environment.Sources {
			if strings.EqualFold(source.Name, binding.Source) {
				binding.Source, sourceFound = source.Name, true
				break
			}
		}
		if !sourceFound {
			return bindingChangePlan{}, fmt.Errorf("source checkout %s is not configured in %s", binding.Source, model.EnvironmentSelector(projectName, environmentName))
		}
		binding.Remote, binding.Mock = nil, nil
	case model.ProviderContainer:
		if declared.Kind != model.ServiceResource {
			return bindingChangePlan{}, errors.New("only managed resources can use the container provider")
		}
		binding.Source, binding.Remote, binding.Mock = "", nil, nil
	case model.ProviderRemote:
		if declared.Kind != model.ServiceProcess {
			return bindingChangePlan{}, errors.New("only HTTP application services can use a remote provider")
		}
		if err := compiler.ValidateRemote(binding.Remote); err != nil {
			return bindingChangePlan{}, err
		}
		binding.Source, binding.Mock = "", nil
	case model.ProviderMock:
		if declared.Kind != model.ServiceProcess {
			return bindingChangePlan{}, errors.New("only HTTP application services can use a mock provider")
		}
		if err := compiler.ValidateMock(binding.Mock); err != nil {
			return bindingChangePlan{}, err
		}
		profile, err := s.database.MockProfile(ctx, projectName, environmentName, binding.Mock.Profile)
		if err != nil {
			return bindingChangePlan{}, fmt.Errorf("mock profile %s does not exist: %w", binding.Mock.Profile, err)
		}
		if !strings.EqualFold(profile.Service, serviceName) {
			return bindingChangePlan{}, fmt.Errorf("mock profile %s belongs to service %s", profile.Name, profile.Service)
		}
		binding.Mock.Profile = profile.Name
		binding.Source, binding.Remote = "", nil
	default:
		return bindingChangePlan{}, errors.New("provider must be local, container, remote, or mock")
	}
	binding.Service = serviceName
	binding.ModifiedAt = time.Now().UTC()
	bindings := replaceBinding(environment.Bindings, binding)
	compiled := compiler.Compile(projectDefinition, environment.Sources, bindings)
	if !configurationCanBeSaved(compiled.Issues) {
		return bindingChangePlan{}, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	return bindingChangePlan{environment: environment, binding: binding, bindings: bindings, definition: compiled.Definition}, nil
}

func (s *Service) runBindingChange(scope string, operation model.Operation, requested model.ComponentBinding) {
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	projectName, environmentName := scopeNames(scope)
	plan, err := s.prepareBindingChange(ctx, projectName, environmentName, requested.Service, requested)
	if err != nil {
		s.failBindingChange(scope, operation, err)
		return
	}
	if err := s.operationServiceTarget(scope, operation, plan.binding.Service); err != nil {
		s.failBindingChange(scope, operation, err)
		return
	}
	currentBinding := bindingForEnvironment(plan.environment, plan.binding.Service)
	if sameProviderBinding(currentBinding, plan.binding) {
		s.completeOperation(scope, operation, "Provider for "+plan.binding.Service+" is already configured")
		return
	}
	if plan.environment.Status == model.EnvironmentStopped {
		updated, replaceErr := s.database.ReplaceEnvironmentConfiguration(ctx, projectName, environmentName, plan.environment.Revision, plan.definition, plan.environment.Sources, plan.bindings)
		if replaceErr != nil {
			s.failBindingChange(scope, operation, replaceErr)
			return
		}
		_, _ = s.timeline(ctx, scope, operation.Actor, "service.provider_changed", plan.binding.Service, "info", providerChangeSummary(plan.binding), map[string]any{"provider": plan.binding.Provider, "environmentStatus": updated.Status})
		s.completeOperation(scope, operation, "Provider for "+plan.binding.Service+" updated")
		snapshot, _ := s.Environment(ctx, projectName, environmentName)
		s.publish(scope, "environment.state", snapshot)
		return
	}
	if plan.environment.Status == model.EnvironmentStarting || plan.environment.Status == model.EnvironmentStopping || plan.environment.Status == model.EnvironmentRecovering || plan.environment.Status == model.EnvironmentUnknown {
		s.failBindingChange(scope, operation, fmt.Errorf("environment is %s; wait for the current lifecycle transition before changing a provider", plan.environment.Status))
		return
	}
	if plan.binding.Provider == model.ProviderRemote {
		probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
		err = s.proxy.CheckRemoteTarget(probeCtx, *plan.binding.Remote)
		probeCancel()
		if err != nil {
			s.failBindingChange(scope, operation, fmt.Errorf("%s remote preflight failed: %w", plan.binding.Service, err))
			return
		}
		_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "provider.preflight", Subject: plan.binding.Service, Message: "Remote target passed preflight"})
	}
	oldDefinition, err := s.database.EnvironmentModel(ctx, projectName, environmentName)
	if err != nil {
		s.failBindingChange(scope, operation, err)
		return
	}
	oldService, ok := serviceDefinition(oldDefinition, plan.binding.Service)
	if !ok {
		s.failBindingChange(scope, operation, database.ErrNotFound)
		return
	}
	oldRuntime := runtimeFor(plan.environment, plan.binding.Service)
	oldBinding := currentBinding
	if oldBinding.Provider == "" {
		oldBinding.Service = oldService.Name
		if oldService.Kind == model.ServiceResource {
			oldBinding.Provider = model.ProviderContainer
		} else {
			oldBinding.Provider = model.ProviderLocal
		}
	}
	activeDefinition := mergeActiveDefinition(oldDefinition, plan.definition, plan.binding.Service)
	candidate := plan.environment
	candidate.Bindings = replaceBinding(candidate.Bindings, plan.binding)
	candidate.Connections = activeDefinition.Connections
	if plan.binding.Provider == model.ProviderLocal {
		if err := s.acquireSourceLeases(scope, candidate); err != nil {
			s.failBindingChange(scope, operation, err)
			return
		}
	}
	stoppedOld := false
	applied := false
	fail := func(changeErr error) {
		rollbackContext, rollbackCancel := context.WithTimeout(context.Background(), time.Minute)
		defer rollbackCancel()
		if applied && plan.binding.Provider == model.ProviderLocal {
			_ = s.processes.Stop(rollbackContext, scope, plan.binding.Service, 10*time.Second)
			s.proxy.RemoveTarget(scope, plan.binding.Service)
		}
		if applied && plan.binding.Provider == model.ProviderContainer {
			if privateKey, keyErr := s.database.PrivateEnvironmentKeyForSelector(rollbackContext, scope); keyErr == nil {
				_ = s.containers.StopService(rollbackContext, privateKey, plan.binding.Service)
			}
			s.proxy.RemoveTarget(scope, plan.binding.Service)
		}
		if applied && plan.binding.Provider == model.ProviderMock {
			_ = s.mocks.Remove(rollbackContext, scope, plan.binding.Service)
			s.proxy.RemoveTarget(scope, plan.binding.Service)
		}
		rollbackErr := s.rollbackBindingChange(rollbackContext, scope, operation, oldDefinition, oldService, oldBinding, oldRuntime, stoppedOld, applied)
		if rollbackErr != nil {
			changeErr = fmt.Errorf("%w; rollback failed: %v", changeErr, rollbackErr)
			_ = s.database.SetServiceStatus(context.Background(), scope, plan.binding.Service, model.ServiceFailed, changeErr.Error())
		}
		s.releaseUnusedSourceLeases(scope)
		s.reconcileEnvironmentStatus(context.Background(), scope)
		s.failBindingChange(scope, operation, changeErr)
	}
	if oldBinding.Provider == model.ProviderLocal && serviceRuntimeActive(oldRuntime.Status) {
		_ = s.database.SetServiceStatus(ctx, scope, plan.binding.Service, model.ServiceStopping, "provider is changing")
		s.reconcileEnvironmentStatus(ctx, scope)
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "stopping", "Stopping only "+plan.binding.Service)
		if err := s.processes.Stop(ctx, scope, plan.binding.Service, 10*time.Second); err != nil {
			fail(err)
			return
		}
		s.proxy.RemoveTarget(scope, plan.binding.Service)
		stoppedOld = true
		if err := s.database.SetServiceRuntime(ctx, scope, plan.binding.Service, database.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: oldRuntime.Generation, RestartCount: oldRuntime.RestartCount, LaunchMode: oldRuntime.LaunchMode}); err != nil {
			fail(err)
			return
		}
	}
	if oldBinding.Provider == model.ProviderMock && serviceRuntimeActive(oldRuntime.Status) {
		_ = s.database.SetServiceStatus(ctx, scope, plan.binding.Service, model.ServiceStopping, "provider is changing")
		s.reconcileEnvironmentStatus(ctx, scope)
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "stopping", "Stopping only "+plan.binding.Service)
		if err := s.mocks.Remove(ctx, scope, plan.binding.Service); err != nil {
			fail(err)
			return
		}
		s.proxy.RemoveTarget(scope, plan.binding.Service)
		stoppedOld = true
		if err := s.database.SetServiceRuntime(ctx, scope, plan.binding.Service, database.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: oldRuntime.Generation, RestartCount: oldRuntime.RestartCount, LaunchMode: model.LaunchManaged}); err != nil {
			fail(err)
			return
		}
	}
	updated, err := s.database.ApplyActiveBindingConfiguration(ctx, projectName, environmentName, plan.environment.Revision, activeDefinition, plan.binding)
	if err != nil {
		fail(err)
		return
	}
	applied = true
	switch plan.binding.Provider {
	case model.ProviderRemote:
		if err := s.proxy.SetRemoteTarget(scope, plan.binding.Service, *plan.binding.Remote); err != nil {
			fail(err)
			return
		}
		now := time.Now().UTC()
		if err := s.database.SetServiceRuntime(ctx, scope, plan.binding.Service, database.ServiceRuntimeUpdate{
			Status: model.ServiceReady, Reason: "remote " + string(plan.binding.Remote.Classification) + " target",
			Generation: oldRuntime.Generation, RestartCount: oldRuntime.RestartCount,
			OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now, LaunchMode: model.LaunchManaged,
		}); err != nil {
			fail(err)
			return
		}
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "ready", plan.binding.Service+" is routed to "+string(plan.binding.Remote.Classification))
	case model.ProviderLocal:
		definition, exists := serviceDefinition(activeDefinition, plan.binding.Service)
		if !exists {
			fail(database.ErrNotFound)
			return
		}
		_ = s.database.SetServiceStatus(ctx, scope, plan.binding.Service, model.ServiceStarting, "provider is changing")
		s.reconcileEnvironmentStatus(ctx, scope)
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "starting", "Starting "+plan.binding.Service+" from "+plan.binding.Source)
		if err := s.prepareServiceDependencies(ctx, scope, updated, plan.binding.Service, operation); err != nil {
			fail(err)
			return
		}
		launchMode := oldRuntime.LaunchMode
		if launchMode == "" || oldBinding.Provider != model.ProviderLocal {
			launchMode = model.LaunchManaged
		}
		if err := s.startProcess(ctx, updated, definition, operation, boolInt64(stoppedOld), launchMode); err != nil {
			fail(err)
			return
		}
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "ready", plan.binding.Service+" is ready")
	case model.ProviderContainer:
		definition, exists := serviceDefinition(activeDefinition, plan.binding.Service)
		if !exists {
			fail(database.ErrNotFound)
			return
		}
		_ = s.database.SetServiceStatus(ctx, scope, plan.binding.Service, model.ServiceStarting, "provider is changing")
		s.reconcileEnvironmentStatus(ctx, scope)
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "starting", "Starting managed "+plan.binding.Service)
		if err := s.startContainer(ctx, updated, definition, 0); err != nil {
			fail(err)
			return
		}
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "ready", plan.binding.Service+" is ready")
	case model.ProviderMock:
		if err := s.activateMock(ctx, scope, plan.binding, oldRuntime); err != nil {
			fail(err)
			return
		}
		_ = s.serviceEvent(scope, operation, plan.binding.Service, "ready", plan.binding.Service+" is served by mock profile "+plan.binding.Mock.Profile)
	}
	latest, _ := s.database.Environment(ctx, projectName, environmentName)
	if err := s.ensurePublicTCPProxies(ctx, latest); err != nil {
		fail(err)
		return
	}
	s.releaseUnusedSourceLeases(scope)
	s.reconcileEnvironmentStatus(ctx, scope)
	_, _ = s.timeline(ctx, scope, operation.Actor, "service.provider_changed", plan.binding.Service, "info", providerChangeSummary(plan.binding), map[string]any{"provider": plan.binding.Provider})
	s.completeOperation(scope, operation, "Provider for "+plan.binding.Service+" updated")
	snapshot, _ := s.Environment(ctx, projectName, environmentName)
	s.publish(scope, "environment.state", snapshot)
}

func (s *Service) rollbackBindingChange(ctx context.Context, scope string, operation model.Operation, definition model.ProjectModel, service model.ServiceDefinition, binding model.ComponentBinding, runtime model.Service, stoppedOld, applied bool) error {
	projectName, environmentName := scopeNames(scope)
	if applied {
		current, err := s.database.Environment(ctx, projectName, environmentName)
		if err != nil {
			return err
		}
		binding.ModifiedAt = time.Now().UTC()
		if _, err := s.database.ApplyActiveBindingConfiguration(ctx, projectName, environmentName, current.Revision, definition, binding); err != nil {
			return err
		}
	}
	if binding.Provider == model.ProviderRemote && binding.Remote != nil {
		if err := s.proxy.SetRemoteTarget(scope, binding.Service, *binding.Remote); err != nil {
			return err
		}
		now := time.Now().UTC()
		return s.database.SetServiceRuntime(ctx, scope, binding.Service, database.ServiceRuntimeUpdate{
			Status: model.ServiceReady, Reason: "remote " + string(binding.Remote.Classification) + " target",
			Generation: runtime.Generation, RestartCount: runtime.RestartCount,
			OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now, LaunchMode: model.LaunchManaged,
		})
	}
	if binding.Provider == model.ProviderMock && binding.Mock != nil {
		return s.activateMock(ctx, scope, binding, runtime)
	}
	if !stoppedOld {
		return s.database.SetServiceStatus(ctx, scope, service.Name, runtime.Status, runtime.Reason)
	}
	_ = s.serviceEvent(scope, operation, service.Name, "starting", "Restoring "+service.Name+" after provider change failed")
	restored, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return err
	}
	launchMode := runtime.LaunchMode
	if launchMode == "" {
		launchMode = model.LaunchManaged
	}
	return s.startProcess(ctx, restored, service, operation, 1, launchMode)
}

func (s *Service) failBindingChange(scope string, operation model.Operation, err error) {
	ctx := context.Background()
	_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "operation.failed", Message: err.Error()})
	_ = s.database.CompleteOperation(ctx, scope, operation.Number, "failed", err.Error())
	failed, _ := s.database.Operation(ctx, scope, operation.Number)
	subject := ""
	for _, event := range failed.Events {
		if event.Subject != "" {
			subject = event.Subject
			break
		}
	}
	_, _ = s.timeline(ctx, scope, operation.Actor, "service.provider_change_failed", subject, "error", err.Error(), map[string]any{"operation": operation.Number})
	s.publish(scope, "operation.state", failed)
}

func (s *Service) releaseUnusedSourceLeases(scope string) {
	environment, err := s.database.EnvironmentBySelector(context.Background(), scope)
	if err != nil {
		return
	}
	usedPaths := make(map[string]struct{})
	for _, service := range environment.Services {
		binding := bindingForEnvironment(environment, service.Name)
		if binding.Provider != model.ProviderLocal || !serviceRuntimeActive(service.Status) {
			continue
		}
		for _, source := range environment.Sources {
			if strings.EqualFold(source.Name, binding.Source) {
				usedPaths[source.Path] = struct{}{}
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, owner := range s.sourceLeases {
		if owner == scope {
			if _, used := usedPaths[path]; !used {
				delete(s.sourceLeases, path)
			}
		}
	}
}

func replaceBinding(bindings []model.ComponentBinding, binding model.ComponentBinding) []model.ComponentBinding {
	result := append([]model.ComponentBinding(nil), bindings...)
	for index := range result {
		if strings.EqualFold(result[index].Service, binding.Service) {
			result[index] = binding
			return result
		}
	}
	return append(result, binding)
}

func sameProviderBinding(left, right model.ComponentBinding) bool {
	if left.Provider != right.Provider {
		return false
	}
	switch right.Provider {
	case model.ProviderLocal:
		return strings.EqualFold(left.Source, right.Source)
	case model.ProviderContainer:
		return true
	case model.ProviderRemote:
		return left.Remote != nil && right.Remote != nil && *left.Remote == *right.Remote
	case model.ProviderMock:
		return left.Mock != nil && right.Mock != nil && strings.EqualFold(left.Mock.Profile, right.Mock.Profile)
	default:
		return false
	}
}

func mergeActiveDefinition(current, proposed model.ProjectModel, changedService string) model.ProjectModel {
	result := current
	serviceIndexes := make(map[string]int, len(result.Services))
	for index, service := range result.Services {
		serviceIndexes[strings.ToLower(service.Name)] = index
	}
	for _, service := range proposed.Services {
		key := strings.ToLower(service.Name)
		if index, exists := serviceIndexes[key]; exists {
			if strings.EqualFold(service.Name, changedService) {
				result.Services[index] = service
			}
			continue
		}
		serviceIndexes[key] = len(result.Services)
		result.Services = append(result.Services, service)
	}
	connections := make(map[string]struct{}, len(result.Connections))
	for _, connection := range result.Connections {
		connections[connectionIdentity(connection)] = struct{}{}
	}
	for _, connection := range proposed.Connections {
		key := connectionIdentity(connection)
		if _, exists := connections[key]; !exists {
			connections[key] = struct{}{}
			result.Connections = append(result.Connections, connection)
		}
	}
	return result
}

func connectionIdentity(connection model.Connection) string {
	return strings.ToLower(connection.Source) + "\x00" + strings.ToLower(connection.Target) + "\x00" + string(connection.Protocol) + "\x00" + connection.Binding + "\x00" + connection.Environment
}

func serviceRuntimeActive(status model.ServiceStatus) bool {
	switch status {
	case model.ServiceReady, model.ServiceStarting, model.ServiceRecovering, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
		return true
	default:
		return false
	}
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func providerChangeSummary(binding model.ComponentBinding) string {
	if binding.Provider == model.ProviderRemote && binding.Remote != nil {
		return binding.Service + " now routes to " + string(binding.Remote.Classification)
	}
	if binding.Provider == model.ProviderLocal {
		return binding.Service + " now runs from source " + binding.Source
	}
	if binding.Provider == model.ProviderMock && binding.Mock != nil {
		return binding.Service + " now uses mock profile " + binding.Mock.Profile
	}
	return binding.Service + " now uses the managed container provider"
}
