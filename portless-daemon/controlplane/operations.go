package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/networking"
	"github.com/portless-run/portless/portless-daemon/projects/compiler"
)

func (s *Service) Up(ctx context.Context, projectName, environmentName, actor, idempotencyKey string, options UpOptions) (model.Operation, error) {
	s.resetGate.RLock()
	defer s.resetGate.RUnlock()
	if s.resetting {
		return model.Operation{}, errors.New("Portless reset preparation is in progress")
	}
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Operation{}, err
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Operation{}, err
	}
	if compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings); len(compiled.Issues) > 0 {
		return model.Operation{}, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	if options.Managed && len(options.DebugServices) > 0 {
		return model.Operation{}, errors.New("managed startup cannot also select debug services")
	}
	for index, requested := range options.DebugServices {
		definition, exists := serviceDefinitionForEnvironment(environment, requested)
		if !exists {
			return model.Operation{}, fmt.Errorf("service %s was not found in %s", requested, model.EnvironmentSelector(projectName, environmentName))
		}
		binding := bindingForEnvironment(environment, definition.Name)
		if definition.Kind != model.ServiceProcess || binding.Provider != model.ProviderLocal {
			return model.Operation{}, fmt.Errorf("service %s cannot run in debug mode because its provider is %s", definition.Name, binding.Provider)
		}
		if definition.Debug == nil {
			return model.Operation{}, fmt.Errorf("service %s can run normally, but no safe debug launcher was discovered", definition.Name)
		}
		options.DebugServices[index] = definition.Name
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.database.CreateOperation(ctx, scope, "up", actor, idempotencyKey)
	if err != nil {
		return model.Operation{}, err
	}
	if operation.State != "running" || len(operation.Events) > 0 {
		return operation, nil
	}
	go s.runUp(scope, operation, options)
	return operation, nil
}

func (s *Service) Down(ctx context.Context, projectName, environmentName, actor, idempotencyKey string, removeVolumes bool) (model.Operation, error) {
	if _, err := s.database.Environment(ctx, projectName, environmentName); err != nil {
		return model.Operation{}, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.database.CreateOperation(ctx, scope, "down", actor, idempotencyKey)
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
	return s.database.Operation(ctx, model.EnvironmentSelector(projectName, environmentName), number)
}

func (s *Service) Operations(ctx context.Context, projectName, environmentName string, limit int) ([]model.Operation, error) {
	scope := model.EnvironmentSelector(projectName, environmentName)
	operations, err := s.database.Operations(ctx, scope, limit)
	if err != nil {
		return nil, err
	}
	for index := range operations {
		events, eventErr := s.database.OperationEvents(ctx, scope, operations[index].Number)
		if eventErr != nil {
			return nil, eventErr
		}
		operations[index].Events = events
	}
	return operations, nil
}

func (s *Service) Connections(ctx context.Context, projectName, environmentName string) ([]model.EffectiveConnection, error) {
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return nil, err
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	result := make([]model.EffectiveConnection, 0, len(environment.Connections))
	for _, connection := range environment.Connections {
		proxyAddress, provider, runtimeTarget := s.proxy.ConnectionRuntime(scope, connection.Source, connection.Target)
		target := runtimeFor(environment, connection.Target)
		if provider == "" {
			provider = bindingForEnvironment(environment, connection.Target).Provider
			if provider == "" {
				if target.Kind == model.ServiceResource {
					provider = model.ProviderContainer
				} else {
					provider = model.ProviderLocal
				}
			}
		}
		if runtimeTarget == "" && provider == model.ProviderRemote {
			binding := bindingForEnvironment(environment, connection.Target)
			if binding.Remote != nil {
				runtimeTarget = binding.Remote.URL
			}
		}
		var endpoint *model.Endpoint
		if connection.Protocol == model.ProtocolHTTP || s.privateTCPIngress {
			if proxyAddress != "" {
				host, encodedPort, splitErr := net.SplitHostPort(proxyAddress)
				port, portErr := strconv.Atoi(encodedPort)
				if splitErr == nil && portErr == nil {
					value := model.Endpoint{Kind: model.EndpointConnection, Protocol: connection.Protocol, Host: host, Port: port, URL: networking.EndpointURL(connection.Protocol, host, port), Address: proxyAddress}
					endpoint = &value
				}
			}
		} else if allocation, allocationErr := s.database.NetworkAllocation(ctx, scope, networking.AllocationConnection, connection.Source, connection.Target, connection.Protocol); allocationErr == nil {
			value := allocation.Endpoint(model.EndpointConnection)
			endpoint = &value
		}
		targetDefinition, targetExists := serviceDefinitionForEnvironment(environment, connection.Target)
		injected := map[string]string{}
		if targetExists && connection.Environment != "" {
			host, port := "", 0
			if endpoint != nil {
				host, port = endpoint.Host, endpoint.Port
			}
			binding, bindingErr := s.connectionBinding(targetDefinition, connection, host, port, s.containerEnvironmentFor(scope, connection.Target), proxyAddress != "" && endpoint != nil)
			if bindingErr != nil {
				return nil, bindingErr
			}
			injected = binding.SafeValues
		}
		result = append(result, model.EffectiveConnection{
			Connection: connection, TargetProvider: provider, TargetStatus: target.Status,
			Endpoint: endpoint, RuntimeTarget: runtimeTarget,
			InjectedEnvironment: injected,
		})
	}
	return result, nil
}

func (s *Service) ServiceConfiguration(ctx context.Context, projectName, environmentName, serviceName string) (model.ServiceConfiguration, error) {
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.ServiceConfiguration{}, err
	}
	definition, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.ServiceConfiguration{}, database.ErrNotFound
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
		if connection.Source != serviceName {
			continue
		}
		for key, value := range connection.InjectedEnvironment {
			values = append(values, model.ConfigurationValue{Key: key, Value: value, Classification: "generated", Source: "Portless connection " + connection.Source + ":" + connection.Target})
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
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Operation{}, err
	}
	projectDefinition, err := s.database.ProjectModel(ctx, projectName)
	if err != nil {
		return model.Operation{}, err
	}
	if compiled := compiler.Compile(projectDefinition, environment.Sources, environment.Bindings); len(compiled.Issues) > 0 {
		return model.Operation{}, compiler.ConfigurationError{Issues: compiled.Issues}
	}
	_, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.Operation{}, database.ErrNotFound
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
	operation, err := s.database.CreateOperation(ctx, scope, operationType, actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	_ = s.operationServiceTarget(scope, operation, serviceName)
	if !restart && runtimeFor(environment, serviceName).Status == model.ServiceReady {
		s.completeOperation(scope, operation, "Service "+serviceName+" is already ready")
		return s.database.Operation(ctx, scope, operation.Number)
	}
	go func() {
		lock := s.projectLock(scope)
		lock.Lock()
		defer lock.Unlock()
		current, currentErr := s.database.Environment(context.Background(), projectName, environmentName)
		if currentErr != nil {
			s.failOperation(scope, operation, currentErr)
			return
		}
		currentDefinition, exists := serviceDefinitionForEnvironment(current, serviceName)
		if !exists {
			s.failOperation(scope, operation, database.ErrNotFound)
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
			_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceStopping, "")
			s.reconcileEnvironmentStatus(context.Background(), scope)
			_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Stopping "+serviceName)
			if currentDefinition.Kind == model.ServiceProcess {
				currentErr = s.processes.Stop(context.Background(), scope, serviceName, 10*time.Second)
			} else {
				var privateKey string
				privateKey, currentErr = s.database.PrivateEnvironmentKeyForSelector(context.Background(), scope)
				if currentErr == nil {
					currentErr = s.containers.StopService(context.Background(), privateKey, serviceName)
				}
			}
			if currentErr != nil {
				_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceFailed, currentErr.Error())
				s.failOperation(scope, operation, currentErr)
				return
			}
			s.proxy.RemoveTarget(scope, serviceName)
		}
		_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceStarting, "")
		s.reconcileEnvironmentStatus(context.Background(), scope)
		_ = s.serviceEvent(scope, operation, serviceName, "starting", "Starting "+serviceName)
		increment := int64(0)
		if restart {
			increment = 1
		}
		if currentDefinition.Kind == model.ServiceProcess {
			launchMode := currentRuntime.LaunchMode
			if launchMode == "" {
				launchMode = model.LaunchManaged
			}
			currentErr = s.startProcess(context.Background(), current, currentDefinition, operation, increment, launchMode)
		} else {
			currentErr = s.startContainer(context.Background(), current, currentDefinition, increment)
		}
		if currentErr != nil {
			_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceFailed, currentErr.Error())
			s.failOperation(scope, operation, currentErr)
			s.releaseSourceLeasesIfIdle(scope)
			return
		}
		if latest, latestErr := s.database.Environment(context.Background(), projectName, environmentName); latestErr == nil {
			if currentErr = s.ensurePublicTCPProxies(context.Background(), latest); currentErr != nil {
				s.failOperation(scope, operation, currentErr)
				return
			}
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
	environment, err := s.database.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Operation{}, err
	}
	_, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.Operation{}, database.ErrNotFound
	}
	if bindingForEnvironment(environment, serviceName).Provider == model.ProviderRemote {
		return model.Operation{}, fmt.Errorf("remote service %s is not managed by Portless; change its environment binding before using lifecycle commands", serviceName)
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.database.CreateOperation(ctx, scope, "stop-service", actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	_ = s.operationServiceTarget(scope, operation, serviceName)
	runtime := runtimeFor(environment, serviceName)
	if runtime.Status == model.ServiceStopped || runtime.Status == model.ServicePlanned {
		s.completeOperation(scope, operation, "Service "+serviceName+" is already stopped")
		return s.database.Operation(ctx, scope, operation.Number)
	}
	go func() {
		lock := s.projectLock(scope)
		lock.Lock()
		defer lock.Unlock()
		current, currentErr := s.database.Environment(context.Background(), projectName, environmentName)
		if currentErr != nil {
			s.failOperation(scope, operation, currentErr)
			return
		}
		currentDefinition, exists := serviceDefinitionForEnvironment(current, serviceName)
		if !exists {
			s.failOperation(scope, operation, database.ErrNotFound)
			return
		}
		currentRuntime := runtimeFor(current, serviceName)
		if currentRuntime.Status == model.ServiceStopped || currentRuntime.Status == model.ServicePlanned {
			s.completeOperation(scope, operation, "Service "+serviceName+" is already stopped")
			return
		}
		_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceStopping, "")
		s.reconcileEnvironmentStatus(context.Background(), scope)
		_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Stopping service")
		var stopErr error
		if currentDefinition.Kind == model.ServiceProcess {
			stopErr = s.processes.Stop(context.Background(), scope, serviceName, 10*time.Second)
		} else {
			var privateKey string
			privateKey, stopErr = s.database.PrivateEnvironmentKeyForSelector(context.Background(), scope)
			if stopErr == nil {
				stopErr = s.containers.StopService(context.Background(), privateKey, serviceName)
			}
		}
		if stopErr != nil {
			_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceFailed, stopErr.Error())
			s.failOperation(scope, operation, stopErr)
			return
		}
		s.proxy.RemoveTarget(scope, serviceName)
		launchMode := currentRuntime.LaunchMode
		if launchMode == "" {
			launchMode = model.LaunchManaged
		}
		_ = s.database.SetServiceRuntime(context.Background(), scope, serviceName, database.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: currentRuntime.Generation, RestartCount: currentRuntime.RestartCount, LaunchMode: launchMode})
		s.reconcileEnvironmentStatus(context.Background(), scope)
		s.releaseSourceLeasesIfIdle(scope)
		s.completeOperation(scope, operation, "Service "+serviceName+" stopped")
	}()
	return operation, nil
}
