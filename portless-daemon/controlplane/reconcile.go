package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/networking"
	"github.com/runportless/portless/portless-daemon/runtime/container"
	"github.com/runportless/portless/portless-daemon/runtime/debuglaunch"
	"github.com/runportless/portless/portless-daemon/runtime/health"
	processruntime "github.com/runportless/portless/portless-daemon/runtime/process"
	"github.com/runportless/portless/portless-daemon/runtime/supervisor"
)

// ReconciliationReport summarizes runtimes recovered or left unverifiable at daemon startup.
type ReconciliationReport struct {
	Recovered    []string
	Unverifiable []string
}

func (s *Service) environmentRuntimeVerified(ctx context.Context, environment model.Environment) bool {
	if s.daemonInstanceID == "" {
		return environment.Status == model.EnvironmentHealthy
	}
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	for _, service := range environment.Services {
		runtime, err := s.database.ServiceRuntime(ctx, scope, service.Name)
		if err != nil || runtime.OwnerInstanceID != s.daemonInstanceID {
			return false
		}
		if service.Status != model.ServiceReady || !s.proxy.HasTarget(scope, service.Name) {
			return false
		}
	}
	for _, connection := range environment.Connections {
		runtime, err := s.database.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
		if err != nil || runtime.OwnerInstanceID != s.daemonInstanceID || runtime.State != "ready" ||
			!s.proxy.HasEdgeAtAddress(scope, connection.Source, connection.Target, connectionRuntimeAddress(runtime)) {
			return false
		}
	}
	if !s.privateTCPIngress {
		allocations, err := s.database.NetworkAllocations(ctx, scope)
		if err != nil {
			return false
		}
		for _, allocation := range allocations {
			if allocation.Kind == networking.AllocationPublic && !s.proxy.HasEdgeAtAddress(scope, "external", allocation.Target, allocation.Address()) {
				return false
			}
		}
	}
	return true
}

// Reconcile verifies persisted runtime ownership and reconstructs proxy routes after startup.
func (s *Service) Reconcile(ctx context.Context) (report ReconciliationReport, resultErr error) {
	startedAt := time.Now().UTC()
	report = ReconciliationReport{Recovered: []string{}, Unverifiable: []string{}}
	defer func() { s.recordReconciliation(startedAt, report, resultErr) }()
	if s.daemonInstanceID == "" {
		return report, nil
	}
	if err := s.database.InterruptRunningOperations(ctx, "Portless daemon restarted before the operation completed"); err != nil {
		return report, err
	}
	environments, err := s.database.ListEnvironments(ctx, "")
	if err != nil {
		return report, err
	}
	active := make([]model.Environment, 0, len(environments))
	for _, environment := range environments {
		definition, definitionErr := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
		if definitionErr != nil {
			return report, definitionErr
		}
		specs, specErr := networking.AllocationSpecs(environment.Project, environment.Name, definition)
		if specErr != nil {
			return report, specErr
		}
		if syncErr := s.database.SyncNetworkAllocations(ctx, model.EnvironmentSelector(environment.Project, environment.Name), specs); syncErr != nil {
			return report, syncErr
		}
		if environment.Status == model.EnvironmentStopped {
			continue
		}
		active = append(active, environment)
		if err := s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentRecovering, "runtime ownership is being verified"); err != nil {
			return report, err
		}
		for _, service := range environment.Services {
			if service.Status != model.ServiceStopped && service.Status != model.ServicePlanned {
				_ = s.database.SetServiceStatus(ctx, model.EnvironmentSelector(environment.Project, environment.Name), service.Name, model.ServiceRecovering, "runtime ownership is being verified")
			}
		}
	}
	for _, environment := range active {
		scope := model.EnvironmentSelector(environment.Project, environment.Name)
		if err := s.reconcileActiveEnvironment(ctx, environment); err != nil {
			report.Unverifiable = append(report.Unverifiable, scope+": "+err.Error())
			continue
		}
		current, currentErr := s.database.Environment(ctx, environment.Project, environment.Name)
		if currentErr != nil {
			return report, currentErr
		}
		if current.Status == model.EnvironmentHealthy {
			report.Recovered = append(report.Recovered, scope)
		} else if environmentRecoveryUnverifiable(current) {
			report.Unverifiable = append(report.Unverifiable, scope+": "+current.Reason)
		}
	}
	return report, nil
}

func environmentRecoveryUnverifiable(environment model.Environment) bool {
	if environment.Status == model.EnvironmentUnknown || environment.Status == model.EnvironmentRecovering {
		return true
	}
	for _, service := range environment.Services {
		if service.Status == model.ServiceUnknown || service.Status == model.ServiceRecovering {
			return true
		}
	}
	return false
}

func (s *Service) reconcileActiveEnvironment(ctx context.Context, environment model.Environment) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	return s.reconcileActiveEnvironmentLocked(ctx, environment)
}

func (s *Service) reconcileActiveEnvironmentLocked(ctx context.Context, environment model.Environment) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	if environment.Status != model.EnvironmentRecovering {
		if err := s.database.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentRecovering, "runtime ownership is being verified"); err != nil {
			return err
		}
		for _, service := range environment.Services {
			if service.Status != model.ServiceStopped && service.Status != model.ServicePlanned {
				_ = s.database.SetServiceStatus(ctx, scope, service.Name, model.ServiceRecovering, "runtime ownership is being verified")
			}
		}
	}
	definition, err := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	privateEnvironmentKey, err := s.database.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return err
	}
	logsRoot := filepath.Join(s.dataDirectory, "environments", privateEnvironmentKey, "logs")

	effectiveBindings := make([]model.ComponentBinding, 0, len(definition.Services))
	bindingsByService := make(map[string]model.ComponentBinding, len(definition.Services))
	for _, serviceDefinition := range definition.Services {
		binding := bindingForEnvironment(environment, serviceDefinition.Name)
		if binding.Provider == "" {
			if serviceDefinition.Kind == model.ServiceResource {
				binding.Provider = model.ProviderContainer
			} else {
				binding.Provider = model.ProviderLocal
			}
		}
		effectiveBindings = append(effectiveBindings, binding)
		bindingsByService[serviceDefinition.Name] = binding
	}
	layers, err := executionLayers(definition, effectiveBindings)
	if err != nil {
		return err
	}
	runtimesByService := make(map[string]database.ServiceRuntimeRecord, len(definition.Services))
	for _, serviceDefinition := range definition.Services {
		runtime, runtimeErr := s.database.ServiceRuntime(ctx, scope, serviceDefinition.Name)
		if runtimeErr != nil {
			return runtimeErr
		}
		runtimesByService[serviceDefinition.Name] = runtime
	}
	// Recover remote and mock targets first, then local/container services in
	// dependency-target-first layers. Independent services within one layer are
	// adopted concurrently with a fixed bound. A live process health check may
	// require its saved dependency listener, so that listener is restored after
	// all targets in earlier layers are adopted and immediately before the probe.
	seen := make(map[string]struct{}, len(definition.Services))
	reconcileService := func(serviceDefinition model.ServiceDefinition) {
		binding := bindingsByService[serviceDefinition.Name]
		runtime := runtimesByService[serviceDefinition.Name]
		if binding.Provider != model.ProviderRemote && binding.Provider != model.ProviderMock && runtime.Generation == 0 && (runtime.Status == model.ServiceStopped || runtime.Status == model.ServicePlanned) {
			return
		}
		var reconcileErr error
		switch binding.Provider {
		case model.ProviderRemote:
			reconcileErr = s.reconcileRemote(ctx, scope, binding, runtime)
		case model.ProviderMock:
			reconcileErr = s.activateMock(ctx, scope, binding, model.Service{Generation: runtime.Generation, RestartCount: runtime.RestartCount})
		case model.ProviderContainer:
			reconcileErr = s.reconcileContainer(ctx, scope, serviceDefinition, runtime, privateEnvironmentKey, logsRoot)
		default:
			reconcileErr = s.reconcileProcess(ctx, scope, serviceDefinition, runtime, func() error {
				return s.restoreDependencyProxiesForSource(ctx, scope, definition, serviceDefinition.Name, runtime.Generation)
			})
		}
		if reconcileErr != nil {
			reason := "runtime could not be recovered: " + reconcileErr.Error()
			_ = s.database.SetServiceStatus(ctx, scope, serviceDefinition.Name, model.ServiceUnknown, reason)
			s.proxy.RemoveTarget(scope, serviceDefinition.Name)
		}
	}
	for _, serviceDefinition := range definition.Services {
		provider := bindingsByService[serviceDefinition.Name].Provider
		if provider != model.ProviderRemote && provider != model.ProviderMock {
			continue
		}
		reconcileService(serviceDefinition)
		seen[serviceDefinition.Name] = struct{}{}
	}
	for _, layer := range layers {
		layerDefinitions := make([]model.ServiceDefinition, 0, len(layer))
		for _, serviceName := range layer {
			orderedService, exists := serviceDefinition(definition, serviceName)
			if !exists {
				continue
			}
			layerDefinitions = append(layerDefinitions, orderedService)
			seen[serviceName] = struct{}{}
		}
		runBoundedServiceRecoveries(layerDefinitions, 4, reconcileService)
	}
	for _, serviceDefinition := range definition.Services {
		if _, exists := seen[serviceDefinition.Name]; exists {
			continue
		}
		reconcileService(serviceDefinition)
	}
	current, err := s.database.Environment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if err := s.ensurePublicTCPProxies(ctx, current); err != nil {
		return err
	}
	for _, connection := range current.Connections {
		targetDefinition, _ := serviceDefinitionForEnvironment(current, connection.Target)
		source := runtimeFor(current, connection.Source)
		if source.Status != model.ServiceReady {
			continue
		}
		if err := s.restoreDependencyProxy(ctx, scope, connection, targetDefinition, source.Generation); err != nil {
			s.markConnectionRecoveryFailure(ctx, scope, connection, err.Error())
		}
	}
	current, err = s.database.Environment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if err := s.acquireRecoveredSourceLeases(scope, current); err != nil {
		for _, service := range current.Services {
			if bindingForEnvironment(current, service.Name).Provider == model.ProviderLocal && service.Status == model.ServiceReady {
				_ = s.database.SetServiceStatus(ctx, scope, service.Name, model.ServiceUnknown, err.Error())
				s.proxy.RemoveTarget(scope, service.Name)
			}
		}
	}
	s.reconcileEnvironmentStatus(ctx, scope)
	final, finalErr := s.database.Environment(ctx, environment.Project, environment.Name)
	if finalErr != nil {
		return finalErr
	}
	if final.Status != model.EnvironmentHealthy {
		_, _ = s.timeline(ctx, scope, "daemon", "environment.reconciled", scope, "warning", "Runtime recovery completed with unavailable services", map[string]any{"daemonInstance": s.daemonInstanceID})
	}
	return nil
}

func runBoundedServiceRecoveries(definitions []model.ServiceDefinition, limit int, recoverService func(model.ServiceDefinition)) {
	if limit < 1 {
		limit = 1
	}
	semaphore := make(chan struct{}, limit)
	var wait sync.WaitGroup
	for _, definition := range definitions {
		definition := definition
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			recoverService(definition)
		}()
	}
	wait.Wait()
}

func (s *Service) restoreDependencyProxiesForSource(ctx context.Context, scope string, definition model.ProjectModel, source string, generation int64) error {
	for _, connection := range definition.Connections {
		if connection.Source != source {
			continue
		}
		targetDefinition, exists := serviceDefinition(definition, connection.Target)
		if !exists {
			return fmt.Errorf("dependency proxy %s:%s target is not defined", connection.Source, connection.Target)
		}
		if err := s.restoreDependencyProxy(ctx, scope, connection, targetDefinition, generation); err != nil {
			return fmt.Errorf("dependency proxy %s:%s could not be recovered: %w", connection.Source, connection.Target, err)
		}
	}
	return nil
}

func (s *Service) restoreDependencyProxy(ctx context.Context, scope string, connection model.Connection, targetDefinition model.ServiceDefinition, sourceGeneration int64) error {
	connection = s.connectionApplicationProtocol(connection, targetDefinition)
	if !s.proxy.HasTarget(scope, connection.Target) {
		return errors.New("target runtime is unavailable")
	}
	persisted, err := s.database.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return errors.New("saved proxy port is missing")
		}
		return err
	}
	if persisted.SourceGeneration != sourceGeneration {
		return errors.New("saved proxy generation does not match the running service")
	}
	listenAddress := net.JoinHostPort(persisted.ListenIP, strconv.Itoa(persisted.ListenPort))
	if persisted.ListenIP == "" {
		listenAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(persisted.ListenPort))
	}
	if connection.Protocol != model.ProtocolHTTP && !s.privateTCPIngress {
		allocation, allocationErr := s.database.NetworkAllocation(ctx, scope, networking.AllocationConnection, connection.Source, connection.Target, connection.Protocol)
		if allocationErr != nil {
			return allocationErr
		}
		if persisted.ListenIP != allocation.ListenIP || persisted.ListenPort != allocation.ListenPort || persisted.DNSName != allocation.DNSName {
			return errors.New("saved proxy endpoint does not match its stable allocation")
		}
		listenAddress = allocation.Address()
	}
	if _, err := s.proxy.EnsureEdgeAtAddress(ctx, scope, connection, listenAddress); err != nil {
		return err
	}
	now := time.Now().UTC()
	persisted.Protocol = connection.Protocol
	persisted.OwnerInstanceID = s.daemonInstanceID
	persisted.State = "ready"
	persisted.Reason = ""
	persisted.ObservedAt = &now
	return s.database.SaveConnectionRuntime(ctx, scope, persisted)
}

func (s *Service) acquireRecoveredSourceLeases(scope string, environment model.Environment) error {
	activeSources := make(map[string]struct{})
	for _, service := range environment.Services {
		binding := bindingForEnvironment(environment, service.Name)
		if binding.Provider != model.ProviderLocal || binding.Source == "" {
			continue
		}
		switch service.Status {
		case model.ServiceReady, model.ServiceStarting, model.ServiceRecovering, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
			activeSources[binding.Source] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range environment.Sources {
		if _, used := activeSources[source.Name]; !used {
			continue
		}
		if owner := s.sourceLeases[source.Path]; owner != "" && owner != scope {
			return fmt.Errorf("source %s is already running in %s; bind a Git worktree to run both environments concurrently", source.Path, owner)
		}
	}
	for _, source := range environment.Sources {
		if _, used := activeSources[source.Name]; used {
			s.sourceLeases[source.Path] = scope
		}
	}
	return nil
}

func (s *Service) reconcileProcess(ctx context.Context, scope string, definition model.ServiceDefinition, runtime database.ServiceRuntimeRecord, prepareDependencies func() error) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	expected := persistedProcessRun(scope, runtime)
	var inspection processruntime.RecoveryInspection
	for {
		inspection = s.processes.InspectPersistedRun(probeCtx, expected)
		if inspection.State != processruntime.RecoveryUnverifiable {
			break
		}
		select {
		case <-probeCtx.Done():
			if inspection.Err == nil {
				inspection.Err = errors.New("persisted process state cannot be verified")
			}
			return fmt.Errorf("supervisor did not become available during recovery: %w", inspection.Err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	switch inspection.State {
	case processruntime.RecoveryTerminal:
		return s.restoreTerminalProcess(ctx, scope, definition.Name, runtime, inspection.Status)
	case processruntime.RecoveryGone:
		return s.markRecoveredRuntimeStopped(ctx, scope, definition.Name, runtime, "previous process runtime is no longer running", "")
	case processruntime.RecoveryLive:
		// Continue with authenticated attachment below.
	default:
		if inspection.Err != nil {
			return inspection.Err
		}
		return errors.New("persisted process state cannot be verified")
	}
	if prepareDependencies != nil {
		if err := prepareDependencies(); err != nil {
			return err
		}
	}
	result, err := s.processes.Attach(probeCtx, scope, definition.Name, runtime.Generation, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
	cancel()
	if err != nil {
		return err
	}
	if err := health.Wait(ctx, result.Port, definition.Health); err != nil {
		return err
	}
	debugger := cloneDebugger(result.Debugger)
	if debugger != nil {
		debugCtx, cancelDebug := context.WithTimeout(ctx, 5*time.Second)
		err := debuglaunch.Wait(debugCtx, *debugger)
		cancelDebug()
		if err != nil {
			return err
		}
		debugger.State = "listening"
	}
	s.proxy.SetTarget(scope, definition.Name, result.Port)
	now := time.Now().UTC()
	started := runtime.StartedAt
	if started == nil {
		started = &result.StartedAt
	}
	return s.database.SetServiceRuntime(ctx, scope, definition.Name, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: runtime.Generation, PID: result.PID, UpstreamPort: result.Port,
		StartedAt: started, RestartCount: runtime.RestartCount, LogPath: result.LogDirectory,
		PrivateRunKey: runtime.PrivateRunKey, OwnerInstanceID: s.daemonInstanceID,
		SupervisorSocket: result.SupervisorSocket, SupervisorState: result.SupervisorState,
		SupervisorPID: result.SupervisorPID, ObservedAt: &now, LaunchMode: result.LaunchMode, Debugger: debugger,
	})
}

func (s *Service) restoreTerminalProcess(ctx context.Context, scope, serviceName string, runtime database.ServiceRuntimeRecord, persisted supervisor.Status) error {
	status, terminal := recoveredTerminalStatus(persisted.State)
	if !terminal {
		return fmt.Errorf("supervisor returned non-terminal state %s", persisted.State)
	}
	if status == model.ServiceStopped {
		return s.markRecoveredRuntimeStopped(ctx, scope, serviceName, runtime, persisted.Error, "")
	}
	reason := persisted.Error
	if reason == "" && status == model.ServiceExited {
		reason = "process exited while the Portless daemon was unavailable"
	}
	now := time.Now().UTC()
	started := runtime.StartedAt
	if started == nil && !persisted.StartedAt.IsZero() {
		started = &persisted.StartedAt
	}
	debugger := cloneDebugger(runtime.Debugger)
	if debugger != nil {
		debugger.State = "stopped"
	}
	s.proxy.RemoveTarget(scope, serviceName)
	return s.database.SetServiceRuntime(ctx, scope, serviceName, database.ServiceRuntimeUpdate{
		Status: status, Reason: reason, Generation: runtime.Generation, PID: persisted.PID,
		UpstreamPort: persisted.Port, StartedAt: started, RestartCount: runtime.RestartCount,
		LogPath: persisted.LogDirectory, PrivateRunKey: runtime.PrivateRunKey,
		OwnerInstanceID: s.daemonInstanceID, SupervisorSocket: runtime.SupervisorSocket,
		SupervisorState: runtime.SupervisorState, SupervisorPID: persisted.SupervisorPID, ObservedAt: &now,
		LaunchMode: runtime.LaunchMode, Debugger: debugger,
	})
}

func (s *Service) markRecoveredRuntimeStopped(ctx context.Context, scope, serviceName string, runtime database.ServiceRuntimeRecord, reason, containerName string) error {
	now := time.Now().UTC()
	debugger := cloneDebugger(runtime.Debugger)
	if debugger != nil {
		debugger.State = "stopped"
	}
	s.proxy.RemoveTarget(scope, serviceName)
	s.mu.Lock()
	delete(s.containerEnvironment, targetEnvironmentKey(scope, serviceName))
	s.mu.Unlock()
	return s.database.SetServiceRuntime(ctx, scope, serviceName, database.ServiceRuntimeUpdate{
		Status: model.ServiceStopped, Reason: reason, Generation: runtime.Generation,
		StartedAt: runtime.StartedAt, RestartCount: runtime.RestartCount, LogPath: runtime.LogPath,
		ContainerName: containerName, ObservedAt: &now, LaunchMode: runtime.LaunchMode, Debugger: debugger,
	})
}

func debuggersEqual(left, right *model.DebuggerRuntime) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Adapter == right.Adapter && left.Host == right.Host && left.Port == right.Port
}

func recoveredTerminalStatus(state string) (model.ServiceStatus, bool) {
	switch state {
	case "stopped":
		return model.ServiceStopped, true
	case "exited":
		return model.ServiceExited, true
	case "failed":
		return model.ServiceFailed, true
	default:
		return "", false
	}
}

func (s *Service) reconcileContainer(ctx context.Context, scope string, definition model.ServiceDefinition, runtime database.ServiceRuntimeRecord, privateEnvironmentKey, logsRoot string) error {
	if runtime.Generation <= 0 {
		return errors.New("container generation is missing")
	}
	recovery, err := s.containers.Recover(ctx, privateEnvironmentKey, definition, runtime.Generation, runtime.ContainerName, logsRoot)
	if err != nil {
		return err
	}
	inspection := recovery.Inspection
	switch inspection.State {
	case container.RecoveryStopped:
		return s.markRecoveredRuntimeStopped(ctx, scope, definition.Name, runtime, "previous managed container is stopped", inspection.ContainerName)
	case container.RecoveryMissing:
		return s.markRecoveredRuntimeStopped(ctx, scope, definition.Name, runtime, "previous managed container is no longer present", "")
	case container.RecoveryRunning:
		// Continue with adoption below.
	default:
		return errors.New("managed container recovery returned an invalid state")
	}
	if recovery.Start == nil {
		return errors.New("managed running container recovery returned no adopted runtime")
	}
	result := *recovery.Start
	if runtime.ContainerName != "" && result.ContainerName != runtime.ContainerName {
		return fmt.Errorf("managed container %s does not match persisted container %s", result.ContainerName, runtime.ContainerName)
	}
	s.proxy.SetTargetProvider(scope, definition.Name, result.Port, model.ProviderContainer)
	s.mu.Lock()
	s.containerEnvironment[targetEnvironmentKey(scope, definition.Name)] = result.Environment
	s.mu.Unlock()
	now := time.Now().UTC()
	started := runtime.StartedAt
	if started == nil {
		started = &result.StartedAt
	}
	return s.database.SetServiceRuntime(ctx, scope, definition.Name, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: runtime.Generation, UpstreamPort: result.Port,
		StartedAt: started, RestartCount: runtime.RestartCount, LogPath: result.LogDirectory,
		OwnerInstanceID: s.daemonInstanceID, ContainerName: result.ContainerName, ObservedAt: &now,
	})
}

func (s *Service) reconcileRemote(ctx context.Context, scope string, binding model.ComponentBinding, runtime database.ServiceRuntimeRecord) error {
	if binding.Remote == nil {
		return errors.New("remote target configuration is missing")
	}
	if err := s.proxy.SetRemoteTarget(scope, binding.Service, *binding.Remote); err != nil {
		return err
	}
	if binding.Remote.HealthPath != "" {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.proxy.CheckRemote(probeCtx, scope, binding.Service)
		cancel()
		if err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	return s.database.SetServiceRuntime(ctx, scope, binding.Service, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Reason: "remote " + string(binding.Remote.Classification) + " target",
		Generation: runtime.Generation, RestartCount: runtime.RestartCount,
		OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now,
	})
}

func (s *Service) markConnectionRecoveryFailure(ctx context.Context, scope string, connection model.Connection, reason string) {
	message := fmt.Sprintf("dependency proxy %s:%s could not be recovered: %s", connection.Source, connection.Target, reason)
	_ = s.database.SetServiceStatus(ctx, scope, connection.Source, model.ServiceUnknown, message)
	s.proxy.RemoveTarget(scope, connection.Source)
}

// CanHandoff reports whether a replacement daemon can safely adopt all active runtimes.
func (s *Service) CanHandoff(ctx context.Context) (bool, []string) {
	if s.daemonInstanceID == "" {
		return false, []string{"daemon runtime ownership is not configured"}
	}
	environments, err := s.database.ListEnvironments(ctx, "")
	if err != nil {
		return false, []string{err.Error()}
	}
	var reasons []string
	var serviceChecks []func() []string
	for _, environment := range environments {
		if environment.Status == model.EnvironmentStopped {
			continue
		}
		scope := model.EnvironmentSelector(environment.Project, environment.Name)
		privateKey, privateKeyErr := s.database.PrivateEnvironmentKeyForSelector(ctx, scope)
		for _, service := range environment.Services {
			if service.Status == model.ServiceStopped || service.Status == model.ServicePlanned {
				continue
			}
			runtime, runtimeErr := s.database.ServiceRuntime(ctx, scope, service.Name)
			if runtimeErr != nil {
				reasons = append(reasons, scope+"/"+service.Name+": "+runtimeErr.Error())
				continue
			}
			binding := bindingForEnvironment(environment, service.Name)
			if binding.Provider == "" {
				if service.Kind == model.ServiceResource {
					binding.Provider = model.ProviderContainer
				} else {
					binding.Provider = model.ProviderLocal
				}
			}
			service := service
			serviceChecks = append(serviceChecks, func() []string {
				return s.handoffServiceProblems(ctx, scope, privateKey, privateKeyErr, service, binding, runtime)
			})
		}
		for _, connection := range environment.Connections {
			source := runtimeFor(environment, connection.Source)
			if source.Status != model.ServiceReady {
				continue
			}
			persisted, runtimeErr := s.database.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
			if runtimeErr != nil {
				reasons = append(reasons, scope+"/"+connection.Source+":"+connection.Target+": saved proxy ownership is missing")
				continue
			}
			if persisted.OwnerInstanceID != s.daemonInstanceID || persisted.SourceGeneration != source.Generation || persisted.State != "ready" {
				reasons = append(reasons, scope+"/"+connection.Source+":"+connection.Target+": saved proxy ownership is stale")
				continue
			}
			if !s.proxy.HasEdgeAtAddress(scope, connection.Source, connection.Target, connectionRuntimeAddress(persisted)) {
				reasons = append(reasons, scope+"/"+connection.Source+":"+connection.Target+": dependency proxy is not listening on its saved endpoint")
			}
		}
		if !s.privateTCPIngress {
			allocations, allocationErr := s.database.NetworkAllocations(ctx, scope)
			if allocationErr != nil {
				reasons = append(reasons, scope+": stable endpoint allocations are unavailable")
			} else {
				for _, allocation := range allocations {
					if allocation.Kind == networking.AllocationPublic && !s.proxy.HasEdgeAtAddress(scope, "external", allocation.Target, allocation.Address()) {
						reasons = append(reasons, scope+"/external:"+allocation.Target+": public TCP endpoint is not listening")
					}
				}
			}
		}
	}
	for _, result := range runBoundedHandoffChecks(serviceChecks, 4) {
		reasons = append(reasons, result...)
	}
	reasons = uniqueStrings(reasons)
	sort.Strings(reasons)
	return len(reasons) == 0, reasons
}

func (s *Service) handoffServiceProblems(ctx context.Context, scope, privateKey string, privateKeyErr error, service model.Service, binding model.ComponentBinding, runtime database.ServiceRuntimeRecord) []string {
	var reasons []string
	prefix := scope + "/" + service.Name + ": "
	switch binding.Provider {
	case model.ProviderLocal:
		if runtime.SupervisorSocket == "" || runtime.PrivateRunKey == "" {
			reasons = append(reasons, prefix+"no recoverable supervisor")
			break
		}
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		status, probeErr := supervisor.LiveStatus(probeCtx, runtime.SupervisorSocket, runtime.PrivateRunKey)
		if service.Status == model.ServiceExited || service.Status == model.ServiceFailed {
			status, probeErr = supervisor.StatusFor(probeCtx, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
			if probeErr == nil && !supervisorTerminalState(status.State) {
				probeErr = fmt.Errorf("supervisor state is %s, expected a terminal state", status.State)
			}
		}
		cancel()
		if probeErr != nil || status.Scope != scope || status.Service != service.Name || status.Generation != runtime.Generation {
			detail := "supervisor is unavailable"
			if probeErr != nil {
				detail = probeErr.Error()
			} else {
				detail = "supervisor identity does not match persisted service run"
			}
			reasons = append(reasons, prefix+detail)
		}
		if probeErr == nil && (status.LaunchMode != runtime.LaunchMode || !debuggersEqual(status.Debugger, runtime.Debugger)) {
			reasons = append(reasons, prefix+"supervisor launch mode does not match persisted service run")
		}
	case model.ProviderContainer:
		switch {
		case runtime.ContainerName == "":
			reasons = append(reasons, prefix+"container ownership is missing")
		case privateKeyErr != nil:
			reasons = append(reasons, prefix+privateKeyErr.Error())
		default:
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			verifyErr := s.containers.Verify(probeCtx, privateKey, service.ServiceDefinition, runtime.Generation, runtime.ContainerName)
			cancel()
			if verifyErr != nil {
				reasons = append(reasons, prefix+verifyErr.Error())
			}
		}
	}
	if runtime.OwnerInstanceID != s.daemonInstanceID {
		reasons = append(reasons, prefix+"runtime is not claimed by the current daemon")
	}
	if service.Status == model.ServiceReady && !s.proxy.HasTarget(scope, service.Name) {
		reasons = append(reasons, prefix+"ingress target is not installed in the current daemon")
	}
	return reasons
}

func runBoundedHandoffChecks(checks []func() []string, limit int) [][]string {
	results := make([][]string, len(checks))
	if len(checks) == 0 {
		return results
	}
	if limit < 1 || limit > len(checks) {
		limit = len(checks)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(limit)
	for range limit {
		go func() {
			defer workers.Done()
			for index := range jobs {
				results[index] = checks[index]()
			}
		}()
	}
	for index := range checks {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return results
}

func connectionRuntimeAddress(runtime database.ConnectionRuntime) string {
	host := runtime.ListenIP
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(runtime.ListenPort))
}

func supervisorTerminalState(state string) bool {
	_, terminal := recoveredTerminalStatus(state)
	return terminal
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
