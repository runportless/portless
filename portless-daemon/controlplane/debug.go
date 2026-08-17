package controlplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/projects/compiler"
)

// DebugService restarts a local process under the Portless supervisor with its
// discovered debugger enabled. The application and debugger ports remain
// private implementation details exposed through the environment status.
func (s *Service) DebugService(ctx context.Context, project, environment, service, actor string) (model.Operation, error) {
	return s.beginServiceModeChange(ctx, project, environment, service, actor, model.LaunchDebug)
}

// ManageService restarts a debug process with its normal discovered command.
func (s *Service) ManageService(ctx context.Context, project, environment, service, actor string) (model.Operation, error) {
	return s.beginServiceModeChange(ctx, project, environment, service, actor, model.LaunchManaged)
}

func (s *Service) beginServiceModeChange(ctx context.Context, projectName, environmentName, serviceName, actor string, targetMode model.LaunchMode) (model.Operation, error) {
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
	definition, ok := serviceDefinitionForEnvironment(environment, serviceName)
	if !ok {
		return model.Operation{}, database.ErrNotFound
	}
	if definition.Kind != model.ServiceProcess || bindingForEnvironment(environment, serviceName).Provider != model.ProviderLocal {
		return model.Operation{}, fmt.Errorf("service %s is not a local process", serviceName)
	}
	if targetMode == model.LaunchDebug && definition.Debug == nil {
		return model.Operation{}, fmt.Errorf("service %s has no supported debug launcher; use its normal managed mode", serviceName)
	}
	operationType, verb := "manage-service", "managed"
	if targetMode == model.LaunchDebug {
		operationType, verb = "debug-service", "debug"
	}
	scope := model.EnvironmentSelector(projectName, environmentName)
	operation, err := s.database.CreateOperation(ctx, scope, operationType, actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	_ = s.operationServiceTarget(scope, operation, serviceName)
	current := runtimeFor(environment, serviceName)
	if current.Status == model.ServiceReady && current.LaunchMode == targetMode && s.environmentRuntimeVerified(ctx, environment) {
		s.completeOperation(scope, operation, "Service "+serviceName+" is already running in "+verb+" mode")
		return s.database.Operation(ctx, scope, operation.Number)
	}

	go func() {
		lock := s.projectLock(scope)
		lock.Lock()
		defer lock.Unlock()
		background := context.Background()
		latest, modeErr := s.database.Environment(background, projectName, environmentName)
		if modeErr != nil {
			s.failOperation(scope, operation, modeErr)
			return
		}
		latestDefinition, exists := serviceDefinitionForEnvironment(latest, serviceName)
		if !exists {
			s.failOperation(scope, operation, database.ErrNotFound)
			return
		}
		latestRuntime := runtimeFor(latest, serviceName)
		if latestRuntime.Status == model.ServiceReady && latestRuntime.LaunchMode == targetMode && s.proxy.HasTarget(scope, serviceName) {
			s.completeOperation(scope, operation, "Service "+serviceName+" is already running in "+verb+" mode")
			return
		}
		if modeErr = s.prepareServiceDependencies(background, scope, latest, serviceName, operation); modeErr != nil {
			s.failOperation(scope, operation, modeErr)
			return
		}
		if modeErr = s.acquireSourceLeases(scope, latest); modeErr != nil {
			s.failOperation(scope, operation, modeErr)
			return
		}
		restartIncrement := int64(0)
		if latestRuntime.Generation > 0 && latestRuntime.Status != model.ServiceStopped && latestRuntime.Status != model.ServicePlanned {
			restartIncrement = 1
		}
		if s.processes.IsRunning(scope, serviceName) {
			_ = s.database.SetServiceStatus(background, scope, serviceName, model.ServiceStopping, "")
			s.reconcileEnvironmentStatus(background, scope)
			_ = s.serviceEvent(scope, operation, serviceName, "stopping", "Restarting "+serviceName+" in "+verb+" mode")
			if modeErr = s.processes.Stop(background, scope, serviceName, 10*time.Second); modeErr != nil {
				_ = s.database.SetServiceStatus(background, scope, serviceName, model.ServiceFailed, modeErr.Error())
				s.failOperation(scope, operation, modeErr)
				return
			}
		}
		s.proxy.RemoveTarget(scope, serviceName)
		_ = s.database.SetServiceStatus(background, scope, serviceName, model.ServiceStarting, "")
		s.reconcileEnvironmentStatus(background, scope)
		_ = s.serviceEvent(scope, operation, serviceName, "starting", "Starting "+serviceName+" in "+verb+" mode")
		if modeErr = s.startProcess(background, latest, latestDefinition, operation, restartIncrement, targetMode); modeErr != nil {
			_ = s.database.SetServiceStatus(background, scope, serviceName, model.ServiceFailed, modeErr.Error())
			s.failOperation(scope, operation, modeErr)
			s.releaseSourceLeasesIfIdle(scope)
			return
		}
		if currentEnvironment, lookupErr := s.database.Environment(background, projectName, environmentName); lookupErr == nil {
			if modeErr = s.ensurePublicTCPProxies(background, currentEnvironment); modeErr != nil {
				s.failOperation(scope, operation, modeErr)
				return
			}
		}
		_ = s.serviceEvent(scope, operation, serviceName, "ready", serviceName+" is ready in "+verb+" mode")
		s.reconcileEnvironmentStatus(background, scope)
		_, _ = s.timeline(background, scope, actor, "service.mode_changed", serviceName, "info", serviceName+" is running in "+verb+" mode", map[string]any{"mode": targetMode})
		s.completeOperation(scope, operation, "Service "+serviceName+" is running in "+verb+" mode")
		snapshot, _ := s.Environment(background, projectName, environmentName)
		s.publish(scope, "environment.state", snapshot)
	}()
	return operation, nil
}
