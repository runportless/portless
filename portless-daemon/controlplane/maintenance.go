package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/runtime/container"
	processruntime "github.com/runportless/portless/portless-daemon/runtime/process"
)

// RuntimeStatus reports the configured and currently selected container runtime.
func (s *Service) RuntimeStatus(ctx context.Context) RuntimeStatus {
	return applicationRuntimeStatus(s.containers.Status(ctx))
}

// StartRuntime attempts to make the preferred container runtime available.
func (s *Service) StartRuntime(ctx context.Context) RuntimeStatus {
	return applicationRuntimeStatus(s.containers.StartHost(ctx))
}

// UseRuntime changes the preferred container runtime when no managed resources are active.
func (s *Service) UseRuntime(ctx context.Context, value string) (RuntimeStatus, error) {
	preference, err := container.ParseRuntimeName(value)
	if err != nil {
		return RuntimeStatus{}, err
	}
	current := s.containers.Status(ctx)
	if preference != current.Selected {
		environments, err := s.database.ListEnvironments(ctx, "")
		if err != nil {
			return RuntimeStatus{}, err
		}
		for _, environment := range environments {
			for _, service := range environment.Services {
				if service.Kind != model.ServiceResource {
					continue
				}
				switch service.Status {
				case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
					return RuntimeStatus{}, RuntimeInUseError{Project: model.EnvironmentSelector(environment.Project, environment.Name)}
				}
			}
		}
	}
	if err := s.containers.SetPreference(preference); err != nil {
		return RuntimeStatus{}, err
	}
	return applicationRuntimeStatus(s.containers.Status(ctx)), nil
}

// PrepareReset verifies ownership and stops managed runtimes before application state is removed.
func (s *Service) PrepareReset(ctx context.Context, force bool) (result ResetRuntimeResult, err error) {
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
	inventory, err := s.database.RuntimeInventory(ctx)
	if err != nil {
		return result, err
	}
	active := activeRuntimeEnvironments(inventory)
	if len(active) > 0 && !force {
		return result, ResetActiveEnvironmentsError{Environments: active}
	}
	running, err := s.database.RunningOperationScopes(ctx)
	if err != nil {
		return result, err
	}
	if len(running) > 0 {
		return result, fmt.Errorf("environment operations are still running: %s; wait for them to finish, then retry", strings.Join(running, ", "))
	}
	result.Processes, err = s.stopResetSupervisors(ctx, inventory)
	if err != nil {
		return result, err
	}
	runtimeResults, resetErr := s.containers.ResetInstallations(ctx)
	result.Runtimes = applicationRuntimeResetResults(runtimeResults)
	err = resetErr
	return result, err
}

// ActiveEnvironments returns sorted selectors for environments with retained runtime activity.
func (s *Service) ActiveEnvironments(ctx context.Context) ([]string, error) {
	inventory, err := s.database.RuntimeInventory(ctx)
	if err != nil {
		return nil, err
	}
	return activeRuntimeEnvironments(inventory), nil
}

// ResetPlan summarizes the application and runtime state that a reset would remove.
func (s *Service) ResetPlan(ctx context.Context) (ResetPlan, error) {
	inventory, err := s.database.RuntimeInventory(ctx)
	if err != nil {
		return ResetPlan{}, err
	}
	projects := make(map[string]struct{})
	managed := make(map[string]struct{})
	for _, environment := range inventory {
		projects[environment.Project] = struct{}{}
		selector := model.EnvironmentSelector(environment.Project, environment.Environment)
		for _, runtime := range environment.Services {
			if runtime.ContainerName != "" {
				managed[selector] = struct{}{}
			}
		}
	}
	compatible := true
	environments, modelErr := s.database.ListEnvironments(ctx, "")
	if modelErr == nil {
		_, modelErr = s.database.ListProjects(ctx)
	}
	switch {
	case modelErr == nil:
		for _, environment := range environments {
			selector := model.EnvironmentSelector(environment.Project, environment.Name)
			for _, service := range environment.Services {
				if service.Kind == model.ServiceResource {
					managed[selector] = struct{}{}
					break
				}
			}
		}
	case errors.Is(modelErr, database.ErrIncompatibleState):
		compatible = false
	default:
		return ResetPlan{}, modelErr
	}
	return ResetPlan{
		Projects: len(projects), Environments: len(inventory), ManagedVolumeEnvironments: len(managed),
		ActiveEnvironments: activeRuntimeEnvironments(inventory), TopologyIncompatible: !compatible,
	}, nil
}

func activeRuntimeEnvironments(inventory []database.EnvironmentRuntimeInventory) []string {
	active := make([]string, 0, len(inventory))
	for _, environment := range inventory {
		if environment.Status != model.EnvironmentStopped {
			active = append(active, model.EnvironmentSelector(environment.Project, environment.Environment))
		}
	}
	sort.Strings(active)
	return active
}

// CancelReset releases the reset gate when reset preparation is not committed.
func (s *Service) CancelReset() {
	s.resetGate.Lock()
	s.resetting = false
	s.resetGate.Unlock()
}

func (s *Service) stopResetSupervisors(ctx context.Context, environments []database.EnvironmentRuntimeInventory) (int, error) {
	type action struct {
		scope   string
		service string
		run     processruntime.PersistedRun
	}
	var actions []action
	for _, environment := range environments {
		scope := model.EnvironmentSelector(environment.Project, environment.Environment)
		for _, runtime := range environment.Services {
			serviceName := runtime.ServiceName
			if runtime.SupervisorSocket == "" && runtime.PrivateRunKey == "" && runtime.SupervisorState == "" && runtime.SupervisorPID == 0 && runtime.PID == 0 {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			inspection := s.processes.InspectPersistedRun(probeCtx, persistedProcessRun(scope, runtime))
			cancel()
			switch inspection.State {
			case processruntime.RecoveryTerminal, processruntime.RecoveryGone:
				continue
			case processruntime.RecoveryLive:
				actions = append(actions, action{scope: scope, service: serviceName, run: persistedProcessRun(scope, runtime)})
			case processruntime.RecoveryUnverifiable:
				detail := inspection.Err
				if detail == nil {
					detail = errors.New("persisted process state cannot be verified")
				}
				return 0, fmt.Errorf("cannot verify previous process runtime %s/%s: %w", scope, serviceName, detail)
			default:
				return 0, fmt.Errorf("cannot verify previous process runtime %s/%s: invalid recovery state", scope, serviceName)
			}
		}
	}
	for index, item := range actions {
		stopCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		_, stopErr := s.processes.StopPersistedRun(stopCtx, item.run, 0)
		cancel()
		if stopErr != nil {
			return index, fmt.Errorf("stop previous process runtime %s/%s: %w", item.scope, item.service, stopErr)
		}
	}
	return len(actions), nil
}
