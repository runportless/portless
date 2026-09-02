package controlplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
)

// SetMockScenarioEnabled starts one durable all-service scenario transition.
func (s *Service) SetMockScenarioEnabled(ctx context.Context, project, environment, name string, enabled bool, actor, idempotencyKey string) (model.Operation, error) {
	s.resetGate.RLock()
	defer s.resetGate.RUnlock()
	if s.resetting {
		return model.Operation{}, errors.New("Portless reset preparation is in progress")
	}
	scenario, err := s.database.MockScenario(ctx, project, environment, name)
	if err != nil {
		return model.Operation{}, err
	}
	if enabled {
		if len(scenario.Routes) == 0 {
			return model.Operation{}, errors.New("add at least one route before enabling the mock scenario")
		}
		if scenario.Activation.State == model.MockScenarioDegraded {
			return model.Operation{}, errors.New("disable the partially active mock scenario before enabling it again")
		}
		if _, err := mocks.Compile(scenario); err != nil {
			return model.Operation{}, err
		}
	}
	scope := model.EnvironmentSelector(project, environment)
	operation, err := s.database.CreateOperation(ctx, scope, "set-mock-scenario", actor, idempotencyKey, operationFingerprint("set-mock-scenario", scenario.Name, map[string]bool{"enabled": enabled}))
	if err != nil {
		return model.Operation{}, err
	}
	if operation.Events != nil {
		return operation, nil
	}
	go s.runMockScenarioActivation(scope, operation, scenario.Name, enabled)
	return operation, nil
}

func (s *Service) runMockScenarioActivation(scope string, operation model.Operation, scenarioName string, enabled bool) {
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	project, environmentName := scopeNames(scope)
	scenario, err := s.database.MockScenario(ctx, project, environmentName, scenarioName)
	if err != nil {
		s.failMockScenarioActivation(scope, operation, scenarioName, err)
		return
	}
	if (enabled && scenario.Activation.State == model.MockScenarioEnabled) || (!enabled && scenario.Activation.State == model.MockScenarioDisabled) {
		s.completeOperation(scope, operation, "Mock scenario "+scenario.Name+" is already "+map[bool]string{true: "enabled", false: "disabled"}[enabled])
		return
	}
	if enabled && scenario.Activation.State == model.MockScenarioDegraded {
		s.failMockScenarioActivation(scope, operation, scenarioName, errors.New("disable the partially active mock scenario before enabling it again"))
		return
	}
	if enabled && len(scenario.Routes) == 0 {
		s.failMockScenarioActivation(scope, operation, scenarioName, errors.New("add at least one route before enabling the mock scenario"))
		return
	}
	if _, err := mocks.Compile(scenario); err != nil {
		s.failMockScenarioActivation(scope, operation, scenarioName, err)
		return
	}
	environment, err := s.database.Environment(ctx, project, environmentName)
	if err != nil {
		s.failMockScenarioActivation(scope, operation, scenarioName, err)
		return
	}
	if environment.Status == model.EnvironmentStarting || environment.Status == model.EnvironmentStopping || environment.Status == model.EnvironmentRecovering || environment.Status == model.EnvironmentUnknown {
		s.failMockScenarioActivation(scope, operation, scenarioName, fmt.Errorf("environment is %s; wait for the current lifecycle transition", environment.Status))
		return
	}
	previous := []model.ComponentBinding{}
	desired := []model.ComponentBinding{}
	services := []string{}
	if enabled {
		for _, service := range scenario.Activation.TargetServices {
			if _, err := s.validateMockService(ctx, project, service); err != nil {
				s.failMockScenarioActivation(scope, operation, scenarioName, err)
				return
			}
			owner, active, err := s.database.ActiveMockScenarioForService(ctx, project, environmentName, service)
			if err != nil {
				s.failMockScenarioActivation(scope, operation, scenarioName, err)
				return
			}
			if active {
				s.failMockScenarioActivation(scope, operation, scenarioName, fmt.Errorf("service %s is already controlled by mock scenario %s", service, owner))
				return
			}
			binding := bindingForEnvironment(environment, service)
			if binding.Provider == "" || binding.Provider == model.ProviderMock {
				s.failMockScenarioActivation(scope, operation, scenarioName, fmt.Errorf("service %s has no non-mock provider binding to restore", service))
				return
			}
			previous = append(previous, binding)
			desired = append(desired, model.ComponentBinding{Service: service, Provider: model.ProviderMock, Mock: &model.MockTarget{Scenario: scenario.Name}})
			services = append(services, service)
		}
	} else {
		records, err := s.database.MockScenarioActivations(ctx, project, environmentName, scenario.Name)
		if err != nil {
			s.failMockScenarioActivation(scope, operation, scenarioName, err)
			return
		}
		for _, record := range records {
			previous = append(previous, bindingForEnvironment(environment, record.Service))
			desired = append(desired, record.PreviousBinding)
			services = append(services, record.Service)
		}
	}
	// Validate every requested binding before changing any provider. The saved
	// restoration records remain private and survive a daemon interruption.
	for _, binding := range desired {
		if _, err := s.prepareBindingChange(ctx, project, environmentName, binding.Service, binding); err != nil {
			s.failMockScenarioActivation(scope, operation, scenarioName, err)
			return
		}
		if environment.Status != model.EnvironmentStopped && binding.Provider == model.ProviderRemote {
			probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.proxy.CheckRemoteTarget(probeCtx, *binding.Remote)
			probeCancel()
			if err != nil {
				s.failMockScenarioActivation(scope, operation, scenarioName, fmt.Errorf("%s remote preflight failed: %w", binding.Service, err))
				return
			}
		}
	}
	if enabled {
		if err := s.persistMockScenarioActivation(ctx, project, environmentName, scenario.Name, previous, true); err != nil {
			s.failMockScenarioActivation(scope, operation, scenarioName, err)
			return
		}
	}
	_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{
		Type: "operation.accepted", Subject: scenario.Name,
		Message: fmt.Sprintf("Mock scenario transition accepted for %d services", len(services)),
		Payload: map[string]any{"enabled": enabled, "services": services},
	})
	changed := make([]int, 0, len(desired))
	fail := func(changeErr error) {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer rollbackCancel()
		rollbackErr := s.rollbackMockScenarioBindings(rollbackCtx, scope, operation, previous, changed)
		if rollbackErr == nil && enabled {
			rollbackErr = s.persistMockScenarioActivation(rollbackCtx, project, environmentName, scenario.Name, nil, false)
		}
		if rollbackErr != nil {
			changeErr = fmt.Errorf("%w; rollback failed: %v; disable the scenario to restore its saved providers", changeErr, rollbackErr)
		}
		s.failMockScenarioActivation(scope, operation, scenarioName, changeErr)
	}
	for index, binding := range desired {
		childKey := fmt.Sprintf("scenario-%d-%t-%s", operation.Number, enabled, strings.ToLower(binding.Service))
		if err := s.applyMockScenarioBinding(ctx, scope, operation.Actor, childKey, binding); err != nil {
			// The provider transition rolls itself back. Retrying its original
			// binding also covers a provider-level rollback that failed.
			changed = append(changed, index)
			fail(err)
			return
		}
		changed = append(changed, index)
		_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "scenario.service_changed", Subject: binding.Service, Message: providerChangeSummary(binding)})
	}
	if !enabled {
		if err := s.persistMockScenarioActivation(ctx, project, environmentName, scenario.Name, nil, false); err != nil {
			fail(err)
			return
		}
	}
	action := "disabled"
	if enabled {
		action = "enabled"
	}
	_, _ = s.timeline(ctx, scope, operation.Actor, "mock."+action, scenario.Name, "info", "Mock scenario "+scenario.Name+" "+action, map[string]any{"services": services})
	updated, _ := s.database.MockScenario(ctx, project, environmentName, scenario.Name)
	s.publish(scope, "mock.state", updated)
	snapshot, _ := s.Environment(ctx, project, environmentName)
	s.publish(scope, "environment.state", snapshot)
	s.completeOperation(scope, operation, "Mock scenario "+scenario.Name+" "+action)
}

func (s *Service) persistMockScenarioActivation(ctx context.Context, project, environment, scenario string, previous []model.ComponentBinding, enabled bool) error {
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return err
	}
	definition, err := s.database.EnvironmentModel(ctx, project, environment)
	if err != nil {
		return err
	}
	_, err = s.database.ApplyMockScenarioConfiguration(ctx, project, environment, current.Revision, definition, current.Bindings, scenario, previous, enabled)
	return err
}

func (s *Service) applyMockScenarioBinding(ctx context.Context, scope, actor, idempotencyKey string, binding model.ComponentBinding) error {
	fingerprint := binding
	fingerprint.ModifiedAt = time.Time{}
	child, err := s.database.CreateOperation(ctx, scope, "change-provider", actor, idempotencyKey, operationFingerprint("change-provider", binding.Service, fingerprint))
	if err != nil {
		return err
	}
	childCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	s.runBindingChangeLocked(childCtx, scope, child, binding, true)
	completed, err := s.database.Operation(ctx, scope, child.Number)
	if err != nil {
		return err
	}
	if completed.State != "succeeded" {
		return errors.New(completed.Error)
	}
	return nil
}

func (s *Service) rollbackMockScenarioBindings(ctx context.Context, scope string, parent model.Operation, previous []model.ComponentBinding, changed []int) error {
	var result error
	for cursor := len(changed) - 1; cursor >= 0; cursor-- {
		binding := previous[changed[cursor]]
		childKey := fmt.Sprintf("scenario-%d-rollback-%s", parent.Number, strings.ToLower(binding.Service))
		result = errors.Join(result, s.applyMockScenarioBinding(ctx, scope, parent.Actor, childKey, binding))
	}
	return result
}

func (s *Service) failMockScenarioActivation(scope string, operation model.Operation, scenario string, err error) {
	ctx := context.Background()
	_, _ = s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: "operation.failed", Subject: scenario, Message: err.Error()})
	_ = s.database.CompleteOperation(ctx, scope, operation.Number, "failed", err.Error())
	failed, _ := s.database.Operation(ctx, scope, operation.Number)
	_, _ = s.timeline(ctx, scope, operation.Actor, "mock.activation_failed", scenario, "error", err.Error(), map[string]any{"operation": operation.Number})
	s.publish(scope, "operation.state", failed)
	project, environment := scopeNames(scope)
	updated, _ := s.database.MockScenario(ctx, project, environment, scenario)
	s.publish(scope, "mock.state", updated)
	snapshot, _ := s.Environment(ctx, project, environment)
	s.publish(scope, "environment.state", snapshot)
}
