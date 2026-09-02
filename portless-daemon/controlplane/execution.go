package controlplane

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/networking"
	"github.com/runportless/portless/portless-daemon/runtime/debuglaunch"
	processruntime "github.com/runportless/portless/portless-daemon/runtime/process"
)

func (s *Service) runUp(scope string, operation model.Operation, options UpOptions) {
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	environment, err := s.database.EnvironmentBySelector(ctx, scope)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	changingModes := options.Managed || len(options.DebugServices) > 0
	if !changingModes && environment.Status == model.EnvironmentHealthy && s.environmentRuntimeVerified(ctx, environment) {
		s.completeOperation(scope, operation, "Environment is already ready")
		return
	}
	if environment.Status != model.EnvironmentStopped && !s.environmentRuntimeVerified(ctx, environment) {
		_ = s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentRecovering, "runtime ownership is stale")
		if err := s.reconcileActiveEnvironmentLocked(ctx, environment); err != nil {
			s.failOperation(scope, operation, fmt.Errorf("runtime recovery failed: %w", err))
			return
		}
		if environment, err = s.database.EnvironmentBySelector(ctx, scope); err != nil {
			s.failOperation(scope, operation, err)
			return
		}
		if !changingModes && environment.Status == model.EnvironmentHealthy {
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
	targetModes := make(map[string]model.LaunchMode)
	for _, service := range environment.Services {
		if service.Kind != model.ServiceProcess || bindingForEnvironment(environment, service.Name).Provider != model.ProviderLocal {
			continue
		}
		mode := service.LaunchMode
		if mode == "" || environment.Status == model.EnvironmentStopped {
			mode = model.LaunchManaged
		}
		if options.Managed {
			mode = model.LaunchManaged
		}
		targetModes[strings.ToLower(service.Name)] = mode
	}
	for _, serviceName := range options.DebugServices {
		targetModes[strings.ToLower(serviceName)] = model.LaunchDebug
	}
	if err := s.acquireSourceLeases(scope, environment); err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	_ = s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentStarting, "services are starting")
	s.publish(scope, "environment.state", map[string]any{"status": model.EnvironmentStarting})
	definition, err := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	for _, binding := range environment.Bindings {
		switch binding.Provider {
		case model.ProviderRemote:
			if binding.Remote == nil {
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
					_ = s.database.SetServiceRuntime(context.Background(), scope, binding.Service, database.ServiceRuntimeUpdate{Status: model.ServiceFailed, Reason: "remote health check failed: " + err.Error()})
					s.failOperation(scope, operation, fmt.Errorf("%s remote health check: %w", binding.Service, err))
					return
				}
			}
			now := time.Now().UTC()
			_ = s.database.SetServiceRuntime(ctx, scope, binding.Service, database.ServiceRuntimeUpdate{
				Status: model.ServiceReady, Reason: "remote " + string(binding.Remote.Classification) + " target",
				OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now,
			})
			_ = s.serviceEvent(scope, operation, binding.Service, "ready", binding.Service+" is routed to "+string(binding.Remote.Classification))
		case model.ProviderMock:
			if binding.Mock == nil {
				s.failOperation(scope, operation, fmt.Errorf("%s mock provider has no profile", binding.Service))
				return
			}
			_ = s.serviceEvent(scope, operation, binding.Service, "starting", "Loading mock scenario "+binding.Mock.Scenario)
			if err := s.activateMock(ctx, scope, binding, runtimeFor(environment, binding.Service)); err != nil {
				s.failOperation(scope, operation, fmt.Errorf("%s mock provider: %w", binding.Service, err))
				return
			}
			_ = s.serviceEvent(scope, operation, binding.Service, "ready", binding.Service+" is served by mock scenario "+binding.Mock.Scenario)
		}
	}
	order, err := executionOrder(definition, environment.Bindings)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	for _, serviceName := range order {
		service, _ := serviceDefinition(definition, serviceName)
		current := runtimeFor(environment, serviceName)
		targetMode := targetModes[strings.ToLower(serviceName)]
		if targetMode == "" {
			targetMode = model.LaunchManaged
		}
		modeMatches := service.Kind != model.ServiceProcess || current.LaunchMode == targetMode ||
			(current.LaunchMode == "" && targetMode == model.LaunchManaged)
		if current.Status == model.ServiceReady && s.proxy.HasTarget(scope, serviceName) && modeMatches {
			_ = s.serviceEvent(scope, operation, serviceName, "ready", serviceName+" is already ready")
			continue
		}
		restartIncrement := int64(0)
		if service.Kind == model.ServiceProcess && !modeMatches && s.processes.IsRunning(scope, serviceName) {
			_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Restarting "+serviceName+" in "+string(targetMode)+" mode")
			if err := s.processes.Stop(ctx, scope, serviceName, 10*time.Second); err != nil {
				s.failOperation(scope, operation, err)
				return
			}
			s.proxy.RemoveTarget(scope, serviceName)
			restartIncrement = 1
		}
		_ = s.serviceEvent(scope, operation, serviceName, "starting", "Starting "+serviceName)
		if service.Kind == model.ServiceResource {
			err = s.startContainer(ctx, environment, service, 0)
		} else {
			err = s.startProcess(ctx, environment, service, operation, restartIncrement, targetMode)
		}
		if err != nil {
			_ = s.database.SetServiceStatus(context.Background(), scope, serviceName, model.ServiceFailed, err.Error())
			s.failOperation(scope, operation, fmt.Errorf("%s: %w", serviceName, err))
			return
		}
		_ = s.serviceEvent(scope, operation, serviceName, "ready", serviceName+" is ready")
		environment, _ = s.database.Environment(ctx, environment.Project, environment.Name)
	}
	if err := s.ensurePublicTCPProxies(ctx, environment); err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	s.reconcileEnvironmentStatus(ctx, scope)
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
	environment, err := s.database.EnvironmentBySelector(ctx, scope)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	definition, err := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	order, err := executionOrder(definition, environment.Bindings)
	if err != nil {
		s.failOperation(scope, operation, err)
		return
	}
	_ = s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentStopping, "services are stopping")
	for _, serviceName := range reverse(order) {
		service, _ := serviceDefinition(definition, serviceName)
		if service.Kind != model.ServiceProcess {
			continue
		}
		_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Stopping "+serviceName)
		current := runtimeFor(environment, serviceName)
		if err := s.stopProcessRuntime(ctx, scope, serviceName, current); err != nil {
			s.failOperation(scope, operation, err)
			return
		}
		_ = s.database.SetServiceRuntime(ctx, scope, serviceName, database.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: current.Generation, RestartCount: current.RestartCount, LaunchMode: model.LaunchManaged})
		s.proxy.RemoveTarget(scope, serviceName)
	}
	privateKey, err := s.database.PrivateEnvironmentKeyForSelector(ctx, scope)
	containerDefined, containerMayBeRunning := false, false
	for _, service := range environment.Services {
		if service.Kind != model.ServiceResource {
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
		_ = s.database.SetServiceRuntime(ctx, scope, service.Name, database.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: service.Generation, LaunchMode: model.LaunchManaged})
	}
	_ = s.mocks.RemoveScope(ctx, scope)
	s.proxy.CloseEnvironment(ctx, scope)
	_ = s.database.DeleteConnectionRuntimes(ctx, scope)
	_, _ = s.database.DisableAllFaults(ctx, scope)
	for _, recording := range mustRecordings(s.database.Recordings(ctx, scope)) {
		if recording.Status == "active" {
			_ = s.database.StopRecording(ctx, scope, recording.Name, "stopped")
		}
	}
	_ = s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentStopped, "")
	s.releaseSourceLeases(scope)
	_, _ = s.timeline(ctx, scope, operation.Actor, "environment.stopped", scope, "info", "Environment stopped", map[string]any{"volumesRemoved": removeVolumes})
	s.completeOperation(scope, operation, "Environment stopped")
	snapshot, _ := s.Environment(ctx, environment.Project, environment.Name)
	s.publish(scope, "environment.state", snapshot)
}

func (s *Service) stopProcessRuntime(ctx context.Context, scope, serviceName string, current model.Service) error {
	if current.Status == model.ServiceStopped || current.Status == model.ServicePlanned || current.Generation == 0 {
		return nil
	}
	if s.processes.IsRunning(scope, serviceName) {
		return s.processes.Stop(ctx, scope, serviceName, 10*time.Second)
	}
	runtime, err := s.database.ServiceRuntime(ctx, scope, serviceName)
	if err != nil {
		return err
	}
	inspection, err := s.processes.StopPersistedRun(ctx, persistedProcessRun(scope, runtime), 12*time.Second)
	if err != nil {
		return fmt.Errorf("cannot safely stop %s: %w", serviceName, err)
	}
	if inspection.State != processruntime.RecoveryTerminal && inspection.State != processruntime.RecoveryGone {
		return fmt.Errorf("cannot safely stop %s: persisted runtime is %s", serviceName, inspection.State)
	}
	return nil
}

func (s *Service) startContainer(ctx context.Context, environment model.Environment, definition model.ServiceDefinition, restartIncrement int64) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	privateKey, err := s.database.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return err
	}
	runtime := runtimeFor(environment, definition.Name)
	nextGeneration := runtime.Generation + 1
	observed := time.Now().UTC()
	if err := s.database.SetServiceRuntime(ctx, scope, definition.Name, database.ServiceRuntimeUpdate{
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
	return s.database.SetServiceRuntime(ctx, scope, definition.Name, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: nextGeneration, UpstreamPort: result.Port, StartedAt: &result.StartedAt, LogPath: result.LogDirectory, RestartCount: runtime.RestartCount + restartIncrement,
		OwnerInstanceID: s.daemonInstanceID, ContainerName: result.ContainerName, ObservedAt: &now,
	})
}

func (s *Service) ensurePublicTCPProxies(ctx context.Context, environment model.Environment) error {
	if s.privateTCPIngress {
		return nil
	}
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	allocations, err := s.database.NetworkAllocations(ctx, scope)
	if err != nil {
		return err
	}
	for _, allocation := range allocations {
		if allocation.Kind != networking.AllocationPublic || !s.proxy.HasTarget(scope, allocation.Target) {
			continue
		}
		target, _ := serviceDefinitionForEnvironment(environment, allocation.Target)
		connection := s.connectionApplicationProtocol(model.Connection{
			Source: "external", Target: allocation.Target, Protocol: allocation.Protocol, Required: false,
		}, target)
		if _, err := s.proxy.EnsureEdgeAtAddress(ctx, scope, connection, allocation.Address()); err != nil {
			return fmt.Errorf("publish %s at %s: %w", allocation.Target, allocation.Address(), err)
		}
	}
	return nil
}

func (s *Service) startProcess(ctx context.Context, environment model.Environment, definition model.ServiceDefinition, operation model.Operation, restartIncrement int64, launchMode model.LaunchMode) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	runtime := runtimeFor(environment, definition.Name)
	nextGeneration := runtime.Generation + 1
	if launchMode == "" {
		launchMode = model.LaunchManaged
	}
	if launchMode == model.LaunchManaged {
		_ = s.database.SetServiceLaunch(ctx, scope, definition.Name, launchMode, nil)
	}
	processEnvironment, err := s.prepareProcessEnvironment(ctx, environment, definition, nextGeneration)
	if err != nil {
		return err
	}
	privateKey, err := s.database.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return err
	}
	launchDefinition := definition
	var debugger *model.DebuggerRuntime
	if launchMode == model.LaunchDebug {
		_ = s.database.SetServiceLaunch(ctx, scope, definition.Name, launchMode, nil)
		debugPort, allocateErr := processruntime.AllocatePort()
		if allocateErr != nil {
			return fmt.Errorf("allocate debugger port for %s: %w", definition.Name, allocateErr)
		}
		artifactsRoot := filepath.Join(s.dataDirectory, "environments", privateKey, "runtime", definition.Name, strconv.FormatInt(nextGeneration, 10))
		launch, prepareErr := debuglaunch.Prepare(definition.Debug, debugPort, artifactsRoot)
		if prepareErr != nil {
			return fmt.Errorf("prepare %s debugger: %w", definition.Name, prepareErr)
		}
		launchDefinition.Command = launch.Command
		for name, value := range launch.Environment {
			processEnvironment[name] = value
		}
		value := launch.Debugger
		debugger = &value
		_ = s.database.SetServiceLaunch(ctx, scope, definition.Name, launchMode, cloneDebugger(debugger))
	} else if launchMode != model.LaunchManaged {
		return fmt.Errorf("unsupported launch mode %q", launchMode)
	}
	logsRoot := filepath.Join(s.dataDirectory, "environments", privateKey, "logs")
	result, err := s.processes.StartPrepared(ctx, scope, launchDefinition, nextGeneration, processEnvironment, logsRoot, processruntime.StartOptions{LaunchMode: launchMode, Debugger: debugger}, func(prepared processruntime.StartResult) error {
		now := time.Now().UTC()
		return s.database.SetServiceRuntime(ctx, scope, definition.Name, database.ServiceRuntimeUpdate{
			Status: model.ServiceStarting, Generation: prepared.Generation, UpstreamPort: prepared.Port,
			StartedAt: &prepared.StartedAt, RestartCount: runtime.RestartCount + restartIncrement,
			LogPath: prepared.LogDirectory, PrivateRunKey: prepared.PrivateRunKey,
			OwnerInstanceID: s.daemonInstanceID, SupervisorSocket: prepared.SupervisorSocket,
			SupervisorState: prepared.SupervisorState, SupervisorPID: prepared.SupervisorPID, ObservedAt: &now,
			LaunchMode: launchMode, Debugger: cloneDebugger(debugger),
		})
	})
	if err != nil {
		if debugger != nil {
			debugger.State = "stopped"
		}
		_ = s.database.SetServiceLaunch(context.Background(), scope, definition.Name, launchMode, cloneDebugger(debugger))
		return err
	}
	if debugger != nil {
		debugContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = debuglaunch.Wait(debugContext, *debugger)
		cancel()
		if err != nil {
			_ = s.processes.Stop(context.Background(), scope, definition.Name, 3*time.Second)
			debugger.State = "stopped"
			_ = s.database.SetServiceLaunch(context.Background(), scope, definition.Name, launchMode, cloneDebugger(debugger))
			return err
		}
		debugger.State = "listening"
	}
	s.proxy.SetTarget(scope, definition.Name, result.Port)
	now := time.Now().UTC()
	return s.database.SetServiceRuntime(ctx, scope, definition.Name, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: result.Generation, PID: result.PID, UpstreamPort: result.Port,
		StartedAt: &result.StartedAt, RestartCount: runtime.RestartCount + restartIncrement, LogPath: result.LogDirectory, PrivateRunKey: result.PrivateRunKey,
		OwnerInstanceID: s.daemonInstanceID, SupervisorSocket: result.SupervisorSocket,
		SupervisorState: result.SupervisorState, SupervisorPID: result.SupervisorPID, ObservedAt: &now,
		LaunchMode: launchMode, Debugger: cloneDebugger(debugger),
	})
}

func cloneDebugger(input *model.DebuggerRuntime) *model.DebuggerRuntime {
	if input == nil {
		return nil
	}
	result := *input
	return &result
}

func (s *Service) prepareProcessEnvironment(ctx context.Context, environment model.Environment, definition model.ServiceDefinition, generation int64) (map[string]string, error) {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	processEnvironment := make(map[string]string)
	modelDefinition, err := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		return nil, err
	}
	for _, connection := range modelDefinition.Connections {
		if connection.Source != definition.Name {
			continue
		}
		target, exists := serviceDefinition(modelDefinition, connection.Target)
		if !exists {
			return nil, fmt.Errorf("connection target %s is not defined", connection.Target)
		}
		connection = s.connectionApplicationProtocol(connection, target)
		listenIP, dnsName, port := "127.0.0.1", "", 0
		persisted, persistedErr := s.database.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
		if persistedErr != nil && !errors.Is(persistedErr, database.ErrNotFound) {
			return nil, persistedErr
		}
		if connection.Protocol != model.ProtocolHTTP && !s.privateTCPIngress {
			allocation, allocationErr := s.database.NetworkAllocation(ctx, scope, networking.AllocationConnection, connection.Source, connection.Target, connection.Protocol)
			if allocationErr != nil {
				return nil, fmt.Errorf("load stable endpoint for %s:%s: %w", connection.Source, connection.Target, allocationErr)
			}
			listenIP, dnsName, port = allocation.ListenIP, allocation.DNSName, allocation.ListenPort
			_, err = s.proxy.EnsureEdgeAtAddress(ctx, scope, connection, allocation.Address())
		} else if persistedErr == nil && persisted.SourceGeneration == generation {
			port, err = s.proxy.EnsureEdgeAtPort(ctx, scope, connection, persisted.ListenPort)
		} else {
			port, err = s.proxy.EnsureEdge(ctx, scope, connection)
		}
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		if err := s.database.SaveConnectionRuntime(ctx, scope, database.ConnectionRuntime{
			Source: connection.Source, Target: connection.Target, Protocol: connection.Protocol,
			SourceGeneration: generation, ListenIP: listenIP, DNSName: dnsName, ListenPort: port, OwnerInstanceID: s.daemonInstanceID,
			State: "ready", ObservedAt: &now,
		}); err != nil {
			return nil, err
		}
		host := listenIP
		if dnsName != "" {
			host = dnsName
		}
		binding, bindErr := s.connectionBinding(target, connection, host, port, s.containerEnvironmentFor(scope, connection.Target), true)
		if bindErr != nil {
			return nil, bindErr
		}
		for name, value := range binding.Values {
			processEnvironment[name] = value
		}
	}
	return processEnvironment, nil
}

func (s *Service) processExited(event processruntime.ExitEvent) {
	if event.Expected {
		return
	}
	ctx := context.Background()
	environment, err := s.database.EnvironmentBySelector(ctx, event.Scope)
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
	_ = s.database.SetServiceStatus(ctx, event.Scope, event.Service, status, reason)
	if runtime.LaunchMode == model.LaunchDebug {
		_ = s.database.SetServiceDebuggerState(ctx, event.Scope, event.Service, "stopped")
	}
	s.proxy.RemoveTarget(event.Scope, event.Service)
	s.reconcileEnvironmentStatus(ctx, event.Scope)
	s.releaseSourceLeasesIfIdle(event.Scope)
	_, _ = s.timeline(ctx, event.Scope, "runtime", "service.exited", event.Service, "error", event.Service+" exited: "+reason, nil)
	s.publish(event.Scope, "service.state", map[string]any{"service": event.Service, "state": status, "reason": reason})
}

func (s *Service) completeOperation(scope string, operation model.Operation, message string) {
	ctx := context.Background()
	_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "operation.completed", Message: message})
	_ = s.database.CompleteOperation(ctx, scope, operation.Number, "succeeded", "")
	completed, _ := s.database.Operation(ctx, scope, operation.Number)
	s.publish(scope, "operation.state", completed)
}

func (s *Service) operationServiceTarget(scope string, operation model.Operation, serviceName string) error {
	_, err := s.database.AddOperationEvent(context.Background(), scope, operation.Number, model.OperationEvent{
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
		if binding.Provider == model.ProviderMock {
			if binding.Mock == nil {
				return fmt.Errorf("mock dependency %s has no profile", connection.Target)
			}
			if err := s.activateMock(ctx, scope, binding, runtimeFor(environment, connection.Target)); err != nil {
				return fmt.Errorf("%s mock provider: %w", connection.Target, err)
			}
			_ = s.serviceEvent(scope, operation, connection.Target, "ready", connection.Target+" is served by mock scenario "+binding.Mock.Scenario)
			continue
		}
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
				_ = s.database.SetServiceStatus(context.Background(), scope, connection.Target, model.ServiceFailed, "remote health check failed: "+err.Error())
				return fmt.Errorf("%s remote health check: %w", connection.Target, err)
			}
		}
		remoteRuntime := runtimeFor(environment, connection.Target)
		now := time.Now().UTC()
		if err := s.database.SetServiceRuntime(ctx, scope, connection.Target, database.ServiceRuntimeUpdate{
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
	environment, err := s.database.EnvironmentBySelector(ctx, scope)
	if err != nil {
		return
	}
	definition, err := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		return
	}
	order, _ := executionOrder(definition, environment.Bindings)
	required := make(map[string]struct{}, len(order)+len(environment.Bindings))
	for _, name := range order {
		required[name] = struct{}{}
	}
	for _, binding := range environment.Bindings {
		if binding.Provider == model.ProviderRemote || binding.Provider == model.ProviderMock {
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
	_ = s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, status, reason)
	s.publish(scope, "environment.state", map[string]any{"status": status, "reason": reason})
}

func (s *Service) failOperation(scope string, operation model.Operation, err error) {
	ctx := context.Background()
	_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "operation.failed", Message: err.Error()})
	_ = s.database.CompleteOperation(ctx, scope, operation.Number, "failed", err.Error())
	if environment, lookupErr := s.database.EnvironmentBySelector(ctx, scope); lookupErr == nil {
		_ = s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentFailed, err.Error())
	}
	_, _ = s.timeline(ctx, scope, operation.Actor, "operation.failed", scope, "error", err.Error(), map[string]any{"operation": operation.Number})
	failed, _ := s.database.Operation(ctx, scope, operation.Number)
	s.publish(scope, "operation.state", failed)
}

func (s *Service) serviceEvent(scope string, operation model.Operation, service, state, message string) error {
	ctx := context.Background()
	_, err := s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "service." + state, Subject: service, Message: message})
	s.publish(scope, "service.state", map[string]any{"service": service, "state": state})
	return err
}

func (s *Service) timeline(ctx context.Context, scope, actor, eventType, subject, severity, summary string, details map[string]any) (model.TimelineEvent, error) {
	project, environment := scopeNames(scope)
	event, err := s.database.AddTimelineEvent(ctx, model.TimelineEvent{Project: project, Environment: environment, Actor: actor, Type: eventType, Subject: subject, Severity: severity, Summary: summary, Details: details})
	if err == nil {
		s.broker.Publish(events.Event{Type: "timeline", Project: project, Environment: environment, Data: event})
	}
	return event, err
}
