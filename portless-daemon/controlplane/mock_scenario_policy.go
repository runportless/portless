package controlplane

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

// SetMockScenarioPolicy starts a durable full/partial mode change, preserving
// routes and enabled state through the existing provider restoration workflow.
func (s *Service) SetMockScenarioPolicy(ctx context.Context, project, environment, name string, policy model.MockUnmatchedRequests, actor, idempotencyKey string) (model.Operation, error) {
	if err := policy.Validate(); err != nil {
		return model.Operation{}, err
	}
	s.resetGate.RLock()
	defer s.resetGate.RUnlock()
	if s.resetting {
		return model.Operation{}, errors.New("Portless reset preparation is in progress")
	}
	scope := model.EnvironmentSelector(project, environment)
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	scenario, err := s.MockScenario(ctx, project, environment, name)
	if err != nil {
		return model.Operation{}, err
	}
	operation, err := s.database.CreateOperation(ctx, scope, "set-mock-policy", actor, idempotencyKey, operationFingerprint("set-mock-policy", scenario.Name, policy))
	if err != nil || operation.Events != nil {
		return operation, err
	}
	go s.runMockScenarioPolicy(scope, operation, scenario.Name, policy)
	return operation, nil
}

func (s *Service) runMockScenarioPolicy(scope string, operation model.Operation, name string, policy model.MockUnmatchedRequests) {
	lock := s.projectLock(scope)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	project, environment := scopeNames(scope)
	fail := func(err error) { s.failMockScenarioActivation(scope, operation, name, err) }
	scenario, err := s.MockScenario(ctx, project, environment, name)
	if err != nil {
		fail(err)
		return
	}
	if scenario.UnmatchedRequests == policy {
		s.completeOperation(scope, operation, "Mock scenario "+name+" already uses "+string(policy))
		return
	}
	if scenario.Activation.State == model.MockScenarioDegraded {
		fail(fmt.Errorf("%w: disable the partially active scenario before changing its mock type", errMockScenarioConflict))
		return
	}
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		fail(err)
		return
	}
	if current.Status == model.EnvironmentStarting || current.Status == model.EnvironmentStopping || current.Status == model.EnvironmentRecovering || current.Status == model.EnvironmentUnknown {
		fail(fmt.Errorf("environment is %s; wait for the current lifecycle transition", current.Status))
		return
	}
	active := scenario.Activation.State == model.MockScenarioEnabled
	if active {
		if err := s.applyMockPolicyActivation(ctx, scope, operation, name, "disable", false); err != nil {
			fail(err)
			return
		}
	}
	_, err = s.database.SetMockScenarioPolicy(ctx, project, environment, name, policy)
	if err == nil && active {
		err = s.applyMockPolicyActivation(ctx, scope, operation, name, "enable", true)
	}
	if err != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer rollbackCancel()
		rollbackErr := s.restoreMockScenarioPolicy(rollbackCtx, scope, operation, scenario)
		if rollbackErr != nil {
			err = fmt.Errorf("%w; restoring the previous mock type failed: %v; inspect the scenario and disable it to release any remaining service ownership", err, rollbackErr)
		}
		fail(err)
		return
	}
	updated, err := s.MockScenario(ctx, project, environment, name)
	if err != nil {
		fail(err)
		return
	}
	_, _ = s.timeline(ctx, scope, operation.Actor, "mock.updated", name, "info", "Changed mock scenario "+name+" to "+string(policy), map[string]any{"unmatchedRequests": policy})
	s.publish(scope, "mock.state", updated)
	s.completeOperation(scope, operation, "Mock scenario "+name+" now uses "+string(policy))
}

func (s *Service) applyMockPolicyActivation(ctx context.Context, scope string, parent model.Operation, name, phase string, enabled bool) error {
	key := fmt.Sprintf("mock-policy-%d-%s", parent.Number, phase)
	child, err := s.database.CreateOperation(ctx, scope, "set-mock-scenario", parent.Actor, key, operationFingerprint("set-mock-scenario", name, map[string]bool{"enabled": enabled}))
	if err != nil {
		return err
	}
	s.runMockScenarioActivationLocked(ctx, scope, child, name, enabled)
	completed, err := s.database.Operation(ctx, scope, child.Number)
	if err != nil {
		return err
	}
	if completed.State != "succeeded" {
		return errors.New(completed.Error)
	}
	return nil
}

func (s *Service) restoreMockScenarioPolicy(ctx context.Context, scope string, operation model.Operation, original model.MockScenario) error {
	current, err := s.MockScenario(ctx, original.Project, original.Environment, original.Name)
	if err != nil {
		return err
	}
	if current.Activation.State != model.MockScenarioDisabled {
		if err := s.applyMockPolicyActivation(ctx, scope, operation, original.Name, "rollback-disable", false); err != nil {
			return err
		}
	}
	if _, err := s.database.SetMockScenarioPolicy(ctx, original.Project, original.Environment, original.Name, original.UnmatchedRequests); err != nil {
		return err
	}
	if original.Activation.State == model.MockScenarioEnabled {
		return s.applyMockPolicyActivation(ctx, scope, operation, original.Name, "rollback-enable", true)
	}
	return nil
}
