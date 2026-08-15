package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/networking"
	"github.com/portless-run/portless/internal/runtime/health"
	"github.com/portless-run/portless/internal/runtime/supervisor"
	"github.com/portless-run/portless/internal/store"
)

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
		if service.Status != model.ServiceReady {
			return false
		}
		runtime, err := s.store.ServiceRuntime(ctx, scope, service.Name)
		if err != nil || runtime.OwnerInstanceID != s.daemonInstanceID || !s.proxy.HasTarget(scope, service.Name) {
			return false
		}
	}
	for _, connection := range environment.Connections {
		runtime, err := s.store.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
		if err != nil || runtime.OwnerInstanceID != s.daemonInstanceID || runtime.State != "ready" ||
			!s.proxy.HasEdgeAtAddress(scope, connection.Source, connection.Target, connectionRuntimeAddress(runtime)) {
			return false
		}
	}
	allocations, err := s.store.NetworkAllocations(ctx, scope)
	if err != nil {
		return false
	}
	for _, allocation := range allocations {
		if allocation.Kind == networking.AllocationPublic && !s.proxy.HasEdgeAtAddress(scope, "external", allocation.Target, allocation.Address()) {
			return false
		}
	}
	return true
}

func (s *Service) Reconcile(ctx context.Context) (ReconciliationReport, error) {
	report := ReconciliationReport{Recovered: []string{}, Unverifiable: []string{}}
	if s.daemonInstanceID == "" {
		return report, nil
	}
	if err := s.store.InterruptRunningOperations(ctx, "Portless daemon restarted before the operation completed"); err != nil {
		return report, err
	}
	environments, err := s.store.ListEnvironments(ctx, "")
	if err != nil {
		return report, err
	}
	active := make([]model.Environment, 0, len(environments))
	for _, environment := range environments {
		definition, definitionErr := s.store.EnvironmentModel(ctx, environment.Project, environment.Name)
		if definitionErr != nil {
			return report, definitionErr
		}
		specs, specErr := networking.AllocationSpecs(environment.Project, environment.Name, definition)
		if specErr != nil {
			return report, specErr
		}
		if syncErr := s.store.SyncNetworkAllocations(ctx, model.EnvironmentSelector(environment.Project, environment.Name), specs); syncErr != nil {
			return report, syncErr
		}
		if environment.Status == model.EnvironmentStopped {
			continue
		}
		active = append(active, environment)
		if err := s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentRecovering, "runtime ownership is being verified"); err != nil {
			return report, err
		}
		for _, service := range environment.Services {
			if service.Status != model.ServiceStopped && service.Status != model.ServicePlanned {
				_ = s.store.SetServiceStatus(ctx, model.EnvironmentSelector(environment.Project, environment.Name), service.Name, model.ServiceRecovering, "runtime ownership is being verified")
			}
		}
	}
	for _, environment := range active {
		scope := model.EnvironmentSelector(environment.Project, environment.Name)
		if err := s.reconcileActiveEnvironment(ctx, environment); err != nil {
			report.Unverifiable = append(report.Unverifiable, scope+": "+err.Error())
			continue
		}
		current, currentErr := s.store.Environment(ctx, environment.Project, environment.Name)
		if currentErr != nil {
			return report, currentErr
		}
		if current.Status == model.EnvironmentHealthy {
			report.Recovered = append(report.Recovered, scope)
		} else {
			report.Unverifiable = append(report.Unverifiable, scope+": "+current.Reason)
		}
	}
	return report, nil
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
		if err := s.store.SetEnvironmentStatus(ctx, environment.Project, environment.Name, model.EnvironmentRecovering, "runtime ownership is being verified"); err != nil {
			return err
		}
		for _, service := range environment.Services {
			if service.Status != model.ServiceStopped && service.Status != model.ServicePlanned {
				_ = s.store.SetServiceStatus(ctx, scope, service.Name, model.ServiceRecovering, "runtime ownership is being verified")
			}
		}
	}
	definition, err := s.store.EnvironmentModel(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	privateEnvironmentKey, err := s.store.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return err
	}
	logsRoot := filepath.Join(s.dataDirectory, "environments", privateEnvironmentKey, "logs")

	for _, serviceDefinition := range definition.Services {
		binding := bindingForEnvironment(environment, serviceDefinition.Name)
		if binding.Provider == "" {
			if serviceDefinition.Kind == model.ServiceResource {
				binding.Provider = model.ProviderContainer
			} else {
				binding.Provider = model.ProviderLocal
			}
		}
		runtime, runtimeErr := s.store.ServiceRuntime(ctx, scope, serviceDefinition.Name)
		if runtimeErr != nil {
			return runtimeErr
		}
		if runtime.Generation == 0 && (runtime.Status == model.ServiceStopped || runtime.Status == model.ServicePlanned) {
			continue
		}
		switch binding.Provider {
		case model.ProviderRemote:
			err = s.reconcileRemote(ctx, scope, binding, runtime)
		case model.ProviderContainer:
			err = s.reconcileContainer(ctx, scope, environment, serviceDefinition, runtime, privateEnvironmentKey, logsRoot)
		default:
			err = s.reconcileProcess(ctx, scope, serviceDefinition, runtime)
		}
		if err != nil {
			reason := "runtime could not be recovered: " + err.Error()
			_ = s.store.SetServiceStatus(ctx, scope, serviceDefinition.Name, model.ServiceUnknown, reason)
			s.proxy.RemoveTarget(scope, serviceDefinition.Name)
		}
	}

	current, err := s.store.Environment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if err := s.ensurePublicTCPProxies(ctx, current); err != nil {
		return err
	}
	for _, connection := range current.Connections {
		source := runtimeFor(current, connection.Source)
		if source.Status != model.ServiceReady {
			continue
		}
		if !s.proxy.HasTarget(scope, connection.Target) {
			s.markConnectionRecoveryFailure(ctx, scope, connection, "target runtime is unavailable")
			continue
		}
		persisted, persistedErr := s.store.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
		if persistedErr != nil {
			if errors.Is(persistedErr, store.ErrNotFound) {
				s.markConnectionRecoveryFailure(ctx, scope, connection, "saved proxy port is missing")
				continue
			}
			return persistedErr
		}
		if persisted.SourceGeneration != source.Generation {
			s.markConnectionRecoveryFailure(ctx, scope, connection, "saved proxy generation does not match the running service")
			continue
		}
		listenAddress := net.JoinHostPort(persisted.ListenIP, strconv.Itoa(persisted.ListenPort))
		if persisted.ListenIP == "" {
			listenAddress = net.JoinHostPort("127.0.0.1", strconv.Itoa(persisted.ListenPort))
		}
		if connection.Protocol != model.ProtocolHTTP {
			allocation, allocationErr := s.store.NetworkAllocation(ctx, scope, networking.AllocationConnection, connection.Source, connection.Target, connection.Protocol)
			if allocationErr != nil {
				s.markConnectionRecoveryFailure(ctx, scope, connection, allocationErr.Error())
				continue
			}
			if persisted.ListenIP != allocation.ListenIP || persisted.ListenPort != allocation.ListenPort || persisted.DNSName != allocation.DNSName {
				s.markConnectionRecoveryFailure(ctx, scope, connection, "saved proxy endpoint does not match its stable allocation")
				continue
			}
			listenAddress = allocation.Address()
		}
		_, edgeErr := s.proxy.EnsureEdgeAtAddress(ctx, scope, connection, listenAddress)
		if edgeErr != nil {
			s.markConnectionRecoveryFailure(ctx, scope, connection, edgeErr.Error())
			continue
		}
		now := time.Now().UTC()
		persisted.OwnerInstanceID = s.daemonInstanceID
		persisted.State = "ready"
		persisted.Reason = ""
		persisted.ObservedAt = &now
		if err := s.store.SaveConnectionRuntime(ctx, scope, persisted); err != nil {
			return err
		}
	}
	current, err = s.store.Environment(ctx, environment.Project, environment.Name)
	if err != nil {
		return err
	}
	if err := s.acquireRecoveredSourceLeases(scope, current); err != nil {
		for _, service := range current.Services {
			if bindingForEnvironment(current, service.Name).Provider == model.ProviderLocal && service.Status == model.ServiceReady {
				_ = s.store.SetServiceStatus(ctx, scope, service.Name, model.ServiceUnknown, err.Error())
				s.proxy.RemoveTarget(scope, service.Name)
			}
		}
	}
	s.reconcileEnvironmentStatus(ctx, scope)
	final, finalErr := s.store.Environment(ctx, environment.Project, environment.Name)
	if finalErr != nil {
		return finalErr
	}
	if final.Status != model.EnvironmentHealthy {
		_, _ = s.timeline(ctx, scope, "daemon", "environment.reconciled", scope, "warning", "Runtime recovery completed with unavailable services", map[string]any{"daemonInstance": s.daemonInstanceID})
	}
	return nil
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

func (s *Service) reconcileProcess(ctx context.Context, scope string, definition model.ServiceDefinition, runtime store.ServiceRuntimeRecord) error {
	if runtime.SupervisorSocket == "" || runtime.PrivateRunKey == "" || runtime.Generation <= 0 {
		return errors.New("service was not started by a recoverable Portless supervisor")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var live supervisor.Status
	for {
		var liveErr error
		live, liveErr = supervisor.LiveStatus(probeCtx, runtime.SupervisorSocket, runtime.PrivateRunKey)
		if liveErr == nil {
			break
		}
		persisted, stateErr := supervisor.StatusFor(probeCtx, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
		if stateErr == nil && persisted.Scope == scope && persisted.Service == definition.Name && persisted.Generation == runtime.Generation {
			if status, terminal := recoveredTerminalStatus(persisted.State); terminal {
				reason := persisted.Error
				if reason == "" && status == model.ServiceExited {
					reason = "process exited while the Portless daemon was unavailable"
				}
				now := time.Now().UTC()
				started := runtime.StartedAt
				if started == nil && !persisted.StartedAt.IsZero() {
					started = &persisted.StartedAt
				}
				s.proxy.RemoveTarget(scope, definition.Name)
				return s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
					Status: status, Reason: reason, Generation: runtime.Generation, PID: persisted.PID,
					UpstreamPort: persisted.Port, StartedAt: started, RestartCount: runtime.RestartCount,
					LogPath: persisted.LogDirectory, PrivateRunKey: runtime.PrivateRunKey,
					OwnerInstanceID: s.daemonInstanceID, SupervisorSocket: runtime.SupervisorSocket,
					SupervisorState: runtime.SupervisorState, SupervisorPID: persisted.SupervisorPID, ObservedAt: &now,
				})
			}
		}
		select {
		case <-probeCtx.Done():
			return fmt.Errorf("supervisor did not become available during recovery: %w", liveErr)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if live.Scope != scope || live.Service != definition.Name || live.Generation != runtime.Generation {
		return errors.New("supervisor identity does not match persisted service run")
	}
	result, err := s.processes.Attach(probeCtx, scope, definition.Name, runtime.Generation, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
	if err != nil {
		return err
	}
	if err := health.Wait(probeCtx, result.Port, definition.Health); err != nil {
		return err
	}
	s.proxy.SetTarget(scope, definition.Name, result.Port)
	now := time.Now().UTC()
	started := runtime.StartedAt
	if started == nil {
		started = &result.StartedAt
	}
	return s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: runtime.Generation, PID: result.PID, UpstreamPort: result.Port,
		StartedAt: started, RestartCount: runtime.RestartCount, LogPath: result.LogDirectory,
		PrivateRunKey: runtime.PrivateRunKey, OwnerInstanceID: s.daemonInstanceID,
		SupervisorSocket: result.SupervisorSocket, SupervisorState: result.SupervisorState,
		SupervisorPID: result.SupervisorPID, ObservedAt: &now,
	})
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

func (s *Service) reconcileContainer(ctx context.Context, scope string, environment model.Environment, definition model.ServiceDefinition, runtime store.ServiceRuntimeRecord, privateEnvironmentKey, logsRoot string) error {
	if runtime.Generation <= 0 {
		return errors.New("container generation is missing")
	}
	result, err := s.containers.Adopt(ctx, environment.Project+"-"+environment.Name, privateEnvironmentKey, definition, runtime.Generation, logsRoot)
	if err != nil {
		return err
	}
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
	return s.store.SetServiceRuntime(ctx, scope, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: runtime.Generation, UpstreamPort: result.Port,
		StartedAt: started, RestartCount: runtime.RestartCount, LogPath: result.LogDirectory,
		OwnerInstanceID: s.daemonInstanceID, ContainerName: result.ContainerName, ObservedAt: &now,
	})
}

func (s *Service) reconcileRemote(ctx context.Context, scope string, binding model.ComponentBinding, runtime store.ServiceRuntimeRecord) error {
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
	return s.store.SetServiceRuntime(ctx, scope, binding.Service, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Reason: "remote " + string(binding.Remote.Classification) + " target",
		Generation: runtime.Generation, RestartCount: runtime.RestartCount,
		OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now,
	})
}

func (s *Service) markConnectionRecoveryFailure(ctx context.Context, scope string, connection model.Connection, reason string) {
	message := fmt.Sprintf("dependency proxy %s:%s could not be recovered: %s", connection.Source, connection.Target, reason)
	_ = s.store.SetServiceStatus(ctx, scope, connection.Source, model.ServiceUnknown, message)
	s.proxy.RemoveTarget(scope, connection.Source)
}

func (s *Service) CanHandoff(ctx context.Context) (bool, []string) {
	if s.daemonInstanceID == "" {
		return false, []string{"daemon runtime ownership is not configured"}
	}
	environments, err := s.store.ListEnvironments(ctx, "")
	if err != nil {
		return false, []string{err.Error()}
	}
	var reasons []string
	for _, environment := range environments {
		if environment.Status == model.EnvironmentStopped {
			continue
		}
		scope := model.EnvironmentSelector(environment.Project, environment.Name)
		for _, service := range environment.Services {
			if service.Status == model.ServiceStopped || service.Status == model.ServicePlanned {
				continue
			}
			runtime, runtimeErr := s.store.ServiceRuntime(ctx, scope, service.Name)
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
			switch binding.Provider {
			case model.ProviderLocal:
				if runtime.SupervisorSocket == "" || runtime.PrivateRunKey == "" {
					reasons = append(reasons, scope+"/"+service.Name+": no recoverable supervisor")
					continue
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
					} else if status.Scope != scope || status.Service != service.Name || status.Generation != runtime.Generation {
						detail = "supervisor identity does not match persisted service run"
					}
					reasons = append(reasons, scope+"/"+service.Name+": "+detail)
				}
			case model.ProviderContainer:
				if runtime.ContainerName == "" {
					reasons = append(reasons, scope+"/"+service.Name+": container ownership is missing")
					break
				}
				privateKey, keyErr := s.store.PrivateEnvironmentKeyForSelector(ctx, scope)
				if keyErr != nil {
					reasons = append(reasons, scope+"/"+service.Name+": "+keyErr.Error())
					break
				}
				probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				verifyErr := s.containers.Verify(probeCtx, privateKey, service.ServiceDefinition, runtime.Generation, runtime.ContainerName)
				cancel()
				if verifyErr != nil {
					reasons = append(reasons, scope+"/"+service.Name+": "+verifyErr.Error())
				}
			}
			if runtime.OwnerInstanceID != s.daemonInstanceID {
				reasons = append(reasons, scope+"/"+service.Name+": runtime is not claimed by the current daemon")
			}
			if service.Status == model.ServiceReady && !s.proxy.HasTarget(scope, service.Name) {
				reasons = append(reasons, scope+"/"+service.Name+": ingress target is not installed in the current daemon")
			}
		}
		for _, connection := range environment.Connections {
			source := runtimeFor(environment, connection.Source)
			if source.Status != model.ServiceReady {
				continue
			}
			persisted, runtimeErr := s.store.ConnectionRuntime(ctx, scope, connection.Source, connection.Target)
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
		allocations, allocationErr := s.store.NetworkAllocations(ctx, scope)
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
	return len(reasons) == 0, uniqueStrings(reasons)
}

func connectionRuntimeAddress(runtime store.ConnectionRuntime) string {
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
