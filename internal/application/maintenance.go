package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/runtime/supervisor"
	"github.com/portless-run/portless/internal/store"
)

func (s *Service) RuntimeStatus(ctx context.Context) RuntimeStatus {
	return applicationRuntimeStatus(s.containers.Status(ctx))
}

func (s *Service) StartRuntime(ctx context.Context) RuntimeStatus {
	return applicationRuntimeStatus(s.containers.StartHost(ctx))
}

func (s *Service) UseRuntime(ctx context.Context, value string) (RuntimeStatus, error) {
	preference, err := container.ParseRuntimeName(value)
	if err != nil {
		return RuntimeStatus{}, err
	}
	current := s.containers.Status(ctx)
	if preference != current.Selected {
		environments, err := s.store.ListEnvironments(ctx, "")
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
	inventory, err := s.store.RuntimeInventory(ctx)
	if err != nil {
		return result, err
	}
	active := activeRuntimeEnvironments(inventory)
	if len(active) > 0 && !force {
		return result, ResetActiveEnvironmentsError{Environments: active}
	}
	running, err := s.store.RunningOperationScopes(ctx)
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

func (s *Service) ActiveEnvironments(ctx context.Context) ([]string, error) {
	inventory, err := s.store.RuntimeInventory(ctx)
	if err != nil {
		return nil, err
	}
	return activeRuntimeEnvironments(inventory), nil
}

func (s *Service) ResetPlan(ctx context.Context) (ResetPlan, error) {
	inventory, err := s.store.RuntimeInventory(ctx)
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
	environments, modelErr := s.store.ListEnvironments(ctx, "")
	if modelErr == nil {
		_, modelErr = s.store.ListProjects(ctx)
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
	case errors.Is(modelErr, store.ErrIncompatibleState):
		compatible = false
	default:
		return ResetPlan{}, modelErr
	}
	return ResetPlan{
		Projects: len(projects), Environments: len(inventory), ManagedVolumeEnvironments: len(managed),
		ActiveEnvironments: activeRuntimeEnvironments(inventory), TopologyIncompatible: !compatible,
	}, nil
}

func activeRuntimeEnvironments(inventory []store.EnvironmentRuntimeInventory) []string {
	active := make([]string, 0, len(inventory))
	for _, environment := range inventory {
		if environment.Status != model.EnvironmentStopped {
			active = append(active, model.EnvironmentSelector(environment.Project, environment.Environment))
		}
	}
	sort.Strings(active)
	return active
}

func (s *Service) CancelReset() {
	s.resetGate.Lock()
	s.resetting = false
	s.resetGate.Unlock()
}

func (s *Service) stopResetSupervisors(ctx context.Context, environments []store.EnvironmentRuntimeInventory) (int, error) {
	stopped := 0
	for _, environment := range environments {
		scope := model.EnvironmentSelector(environment.Project, environment.Environment)
		for _, runtime := range environment.Services {
			serviceName := runtime.ServiceName
			if runtime.SupervisorSocket == "" && runtime.PrivateRunKey == "" && runtime.SupervisorState == "" {
				continue
			}
			if runtime.SupervisorSocket == "" || runtime.PrivateRunKey == "" || runtime.SupervisorState == "" {
				return stopped, fmt.Errorf("cannot verify previous process runtime %s/%s because its supervisor ownership record is incomplete", scope, serviceName)
			}
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			status, liveErr := supervisor.LiveStatus(probeCtx, runtime.SupervisorSocket, runtime.PrivateRunKey)
			cancel()
			if liveErr != nil {
				var statusErr error
				status, statusErr = supervisor.StatusFor(ctx, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
				if statusErr != nil {
					return stopped, fmt.Errorf("cannot verify previous process runtime %s/%s: %w", scope, serviceName, liveErr)
				}
				if err := validateResetSupervisor(status, scope, serviceName, runtime.Generation); err != nil {
					return stopped, err
				}
				if !supervisorTerminalState(status.State) {
					return stopped, fmt.Errorf("cannot stop previous process runtime %s/%s because its supervisor is unavailable and persisted state is %s", scope, serviceName, status.State)
				}
				continue
			}
			if err := validateResetSupervisor(status, scope, serviceName, runtime.Generation); err != nil {
				return stopped, err
			}
			if supervisorTerminalState(status.State) {
				continue
			}
			stopCtx, stopCancel := context.WithTimeout(ctx, 12*time.Second)
			status, stopErr := supervisor.Stop(stopCtx, runtime.SupervisorSocket, runtime.SupervisorState, runtime.PrivateRunKey)
			stopCancel()
			if stopErr != nil {
				return stopped, fmt.Errorf("stop previous process runtime %s/%s: %w", scope, serviceName, stopErr)
			}
			if err := validateResetSupervisor(status, scope, serviceName, runtime.Generation); err != nil {
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
