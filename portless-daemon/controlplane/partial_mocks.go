package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/traffic/proxy"
)

func (s *Service) runPartialMockActivation(ctx context.Context, scope string, operation model.Operation, scenario model.MockScenario, environment model.Environment, enabled bool) {
	fail := func(err error) { s.failMockScenarioActivation(scope, operation, scenario.Name, err) }
	previous := []model.ComponentBinding{}
	var compiled *mocks.CompiledScenario
	if enabled {
		var err error
		compiled, err = mocks.Compile(scenario)
		if err != nil {
			fail(err)
			return
		}
		for _, service := range scenario.Activation.TargetServices {
			if _, err := s.validateMockService(ctx, scenario.Project, service); err != nil {
				fail(err)
				return
			}
			owner, active, err := s.database.ActiveMockScenarioForService(ctx, scenario.Project, scenario.Environment, service)
			if err != nil {
				fail(err)
				return
			}
			if active {
				fail(fmt.Errorf("service %s is already controlled by mock scenario %s", service, owner))
				return
			}
			binding := bindingForEnvironment(environment, service)
			if binding.Provider != model.ProviderLocal && binding.Provider != model.ProviderRemote {
				fail(fmt.Errorf("service %s needs a local or remote HTTP provider", service))
				return
			}
			if binding.Provider == model.ProviderRemote && binding.Remote == nil {
				fail(errors.New("remote target configuration is missing"))
				return
			}
			if environment.Status != model.EnvironmentStopped {
				if _, err := s.proxy.ReplayTarget(scope, service, binding, "GET", "/"); err != nil {
					fail(fmt.Errorf("service %s is unavailable or its provider changed; start it before enabling partial mocks", service))
					return
				}
			}
			previous = append(previous, binding)
		}
	}
	if err := s.database.SetPartialMockActivation(ctx, scenario.Project, scenario.Environment, scenario.Name, previous, enabled); err != nil {
		fail(err)
		return
	}
	if enabled {
		if err := s.proxy.SetPartialMock(scope, scenario.Name, scenario.Activation.TargetServices, compiled); err != nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rollbackErr := s.database.SetPartialMockActivation(rollbackCtx, scenario.Project, scenario.Environment, scenario.Name, nil, false)
			fail(errors.Join(err, rollbackErr))
			return
		}
	} else {
		s.proxy.RemovePartialMock(scope, scenario.Name)
	}
	action := map[bool]string{true: "enabled", false: "disabled"}[enabled]
	_, _ = s.timeline(ctx, scope, operation.Actor, "mock."+action, scenario.Name, "info", "Mock scenario "+scenario.Name+" "+action, map[string]any{"unmatchedRequests": scenario.UnmatchedRequests})
	updated, _ := s.MockScenario(ctx, scenario.Project, scenario.Environment, scenario.Name)
	s.publish(scope, "mock.state", updated)
	snapshot, _ := s.Environment(ctx, scenario.Project, scenario.Environment)
	s.publish(scope, "environment.state", snapshot)
	s.completeOperation(scope, operation, "Mock scenario "+scenario.Name+" "+action)
}

// restorePartialMocks installs ownership guards before reading any saved responses.
func (s *Service) restorePartialMocks(ctx context.Context, environment model.Environment) error {
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	metadata, _, err := s.database.MockScenarioMetadataList(ctx, environment.Project, environment.Name, 0, -1)
	if err != nil {
		return err
	}
	pending := make([]model.MockScenarioMetadata, 0, len(metadata))
	for _, item := range metadata {
		if item.UnmatchedRequests != model.MockUnmatchedForward || item.Activation.State == model.MockScenarioDisabled {
			continue
		}
		applied := true
		for _, target := range item.Activation.TargetServices {
			applied = applied && s.proxy.PartialMockApplied(scope, target, item.Name)
		}
		if applied {
			continue
		}
		pending = append(pending, item)
		if err := s.proxy.SetPartialMock(scope, item.Name, item.Activation.TargetServices, nil); err != nil {
			return err
		}
	}
	var result error
	for _, item := range pending {
		if item.UnmatchedRequests != model.MockUnmatchedForward || item.Activation.State == model.MockScenarioDisabled {
			continue
		}
		scenario, err := s.database.MockScenario(ctx, environment.Project, environment.Name, item.Name)
		if err == nil && scenario.Activation.State != model.MockScenarioEnabled {
			err = errors.New("partial mock ownership disagrees with provider configuration")
		}
		var compiled *mocks.CompiledScenario
		if err == nil {
			compiled, err = mocks.Compile(scenario)
		}
		if err == nil {
			err = s.proxy.SetPartialMock(scope, item.Name, item.Activation.TargetServices, compiled)
		}
		for _, target := range item.Activation.TargetServices {
			runtime := runtimeFor(environment, target)
			if runtime.Status == model.ServiceFailed || runtime.Status == model.ServiceExited || runtime.Status == model.ServiceUnhealthy || err != nil && runtime.Status == model.ServiceReady {
				s.proxy.ResumePartialMockAdmission(scope, target)
			}
		}
		if err != nil {
			result = errors.Join(result, fmt.Errorf("restore partial mock %s: %w", item.Name, err))
		}
	}
	return result
}

func (s *Service) projectMockActivation(scope, name string, policy model.MockUnmatchedRequests, status model.EnvironmentStatus, activation model.MockScenarioActivation) model.MockScenarioActivation {
	if policy != model.MockUnmatchedForward || activation.State == model.MockScenarioDisabled || status == model.EnvironmentStopped {
		return activation
	}
	active := []string{}
	for _, service := range activation.ActiveServices {
		if s.proxy.PartialMockApplied(scope, service, name) {
			active = append(active, service)
		}
	}
	activation.ActiveServices = active
	if len(active) != len(activation.TargetServices) {
		activation.State = model.MockScenarioDegraded
	}
	return activation
}

func (s *Service) mockPreviewDestination(ctx context.Context, project, environment, scenario string, request model.MockRequest, preview model.MockPreview) (model.MockPreview, error) {
	sample, err := http.NewRequestWithContext(ctx, request.Method, "http://preview.localhost"+request.Path, strings.NewReader(request.Body))
	if err != nil {
		return model.MockPreview{}, err
	}
	for name, values := range request.Headers {
		for _, value := range values {
			sample.Header.Add(name, value)
		}
	}
	upgrade, err := proxy.InspectMockPreviewUpgrade(sample)
	if err != nil {
		return model.MockPreview{Service: request.Service, Outcome: "blocked", Reason: &model.MockBlockReason{Code: "INVALID_UPGRADE", Message: err.Error()}}, nil
	}
	if upgrade && preview.Outcome != "forward" {
		saved, err := s.database.MockScenarioMetadata(ctx, project, environment, scenario)
		if err != nil {
			return model.MockPreview{}, err
		}
		if saved.UnmatchedRequests == model.MockUnmatchedReject {
			return model.MockPreview{Service: request.Service, Outcome: "blocked", Reason: &model.MockBlockReason{Code: "MOCK_UPGRADE_UNSUPPORTED", Message: "Strict mocks do not support WebSocket upgrades"}}, nil
		}
		preview = model.MockPreview{Service: request.Service, Outcome: "forward"}
	}
	if preview.Outcome != "forward" {
		return preview, nil
	}
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return model.MockPreview{}, err
	}
	binding := bindingForEnvironment(current, request.Service)
	if binding.Provider == model.ProviderMock {
		records, err := s.database.MockScenarioActivations(ctx, project, environment, scenario)
		if err != nil {
			return model.MockPreview{}, err
		}
		for _, record := range records {
			if strings.EqualFold(record.Service, request.Service) {
				binding = record.PreviousBinding
			}
		}
	}
	block := func(code, message string) (model.MockPreview, error) {
		return model.MockPreview{Service: request.Service, Outcome: "blocked", Reason: &model.MockBlockReason{Code: code, Message: message}}, nil
	}
	if binding.Provider != model.ProviderLocal && binding.Provider != model.ProviderRemote {
		return block("MOCK_FORWARD_UNAVAILABLE", "Service has no supported real provider")
	}
	destination := &model.MockDestination{Provider: binding.Provider, URL: "http://" + request.Service + "." + environment + "." + project + ".localhost"}
	if binding.Provider == model.ProviderRemote {
		if binding.Remote == nil {
			return block("MOCK_FORWARD_UNAVAILABLE", "Remote target configuration is missing")
		}
		if binding.Remote.WritePolicy == model.WriteReadOnly && (upgrade || strings.ToUpper(request.Method) != http.MethodGet && strings.ToUpper(request.Method) != http.MethodHead && strings.ToUpper(request.Method) != http.MethodOptions && strings.ToUpper(request.Method) != http.MethodTrace) {
			return block("REMOTE_READ_ONLY", "Remote target is read-only")
		}
		destination.Classification, destination.WritePolicy = binding.Remote.Classification, binding.Remote.WritePolicy
	}
	preview.Destination = destination
	return preview, nil
}
