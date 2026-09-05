package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
)

var errMockScenarioConflict = errors.New("mock scenario state conflict")

// MockScenarios lists the mock scenarios owned by one environment.
func (s *Service) MockScenarios(ctx context.Context, project, environment string) ([]model.MockScenario, error) {
	return s.database.MockScenarios(ctx, project, environment)
}

// MockScenario returns one environment-scoped mock scenario.
func (s *Service) MockScenario(ctx context.Context, project, environment, name string) (model.MockScenario, error) {
	return s.database.MockScenario(ctx, project, environment, name)
}

// CreateMockScenario creates an empty environment-scoped HTTP mock scenario.
func (s *Service) CreateMockScenario(ctx context.Context, project, environment string, scenario model.MockScenario, actor string) (model.MockScenario, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	scenario.Routes = nil
	scenario.Activation = model.MockScenarioActivation{}
	created, err := s.database.CreateMockScenario(ctx, project, environment, scenario)
	if err != nil {
		return model.MockScenario{}, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.created", created.Name, "info", "Created mock scenario "+created.Name, nil)
	s.publish(scope, "mock.state", created)
	return created, nil
}

// ImportMockScenarioOpenAPI adds routes for one service from a local OpenAPI document.
func (s *Service) ImportMockScenarioOpenAPI(ctx context.Context, project, environment, scenarioName, service string, document []byte, actor string) (model.MockScenario, []string, error) {
	if len(document) > 1<<20 {
		return model.MockScenario{}, nil, errors.New("OpenAPI document must not exceed 1048576 bytes")
	}
	canonical, err := s.validateMockService(ctx, project, service)
	if err != nil {
		return model.MockScenario{}, nil, err
	}
	routes, warnings, err := mocks.RoutesFromOpenAPI(document)
	if err != nil {
		return model.MockScenario{}, warnings, err
	}
	for index := range routes {
		routes[index].Service = canonical
	}
	return s.importMockRoutes(ctx, project, environment, scenarioName, routes, warnings, "OpenAPI document", actor)
}

// ImportMockScenarioRecording adds routes derived from retained HTTP traffic.
func (s *Service) ImportMockScenarioRecording(ctx context.Context, project, environment, scenarioName, recordingName string, services []string, actor string) (model.MockScenario, []string, error) {
	recording, err := s.database.Recording(ctx, model.EnvironmentSelector(project, environment), recordingName)
	if err != nil {
		return model.MockScenario{}, nil, err
	}
	if recording.Status == "active" {
		return model.MockScenario{}, nil, fmt.Errorf("recording %s is still active; stop it before importing routes", recording.Name)
	}
	definition, err := s.database.ProjectModel(ctx, project)
	if err != nil {
		return model.MockScenario{}, nil, err
	}
	allowed := map[string]string{}
	if len(services) == 0 {
		for _, service := range definition.Services {
			eligible := service.Kind == model.ServiceProcess
			for _, connection := range definition.Connections {
				if strings.EqualFold(connection.Target, service.Name) && connection.Protocol != model.ProtocolHTTP {
					eligible = false
					break
				}
			}
			if eligible {
				allowed[strings.ToLower(service.Name)] = service.Name
			}
		}
	} else {
		for _, service := range services {
			canonical, validateErr := s.validateMockService(ctx, project, service)
			if validateErr != nil {
				return model.MockScenario{}, nil, validateErr
			}
			allowed[strings.ToLower(canonical)] = canonical
		}
	}
	exchanges, err := s.database.RecordedTraffic(ctx, model.EnvironmentSelector(project, environment), recording.Name, 10_000)
	if err != nil {
		return model.MockScenario{}, nil, err
	}
	routes, warnings := mockRoutesFromRecording(allowed, exchanges)
	return s.importMockRoutes(ctx, project, environment, scenarioName, routes, warnings, "recording "+recording.Name, actor)
}

func (s *Service) importMockRoutes(ctx context.Context, project, environment, scenarioName string, routes []model.MockRoute, warnings []string, source, actor string) (model.MockScenario, []string, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	scenario, err := s.database.MockScenario(ctx, project, environment, scenarioName)
	if err != nil {
		return model.MockScenario{}, warnings, err
	}
	if scenario.Activation.State != model.MockScenarioDisabled {
		return model.MockScenario{}, warnings, fmt.Errorf("%w: disable the mock scenario before importing routes that can change its service coverage", errMockScenarioConflict)
	}
	used := map[string]struct{}{}
	for _, route := range scenario.Routes {
		used[strings.ToLower(route.Name)] = struct{}{}
	}
	for index := range routes {
		routes[index].Name = availableMockRouteName(routes[index].Name, used)
		used[strings.ToLower(routes[index].Name)] = struct{}{}
		scenario.Routes = append(scenario.Routes, routes[index])
	}
	if _, err := mocks.Compile(scenario); err != nil {
		return model.MockScenario{}, warnings, err
	}
	updated, err := s.database.PutMockRoutes(ctx, project, environment, scenario.Name, routes)
	if err != nil {
		return model.MockScenario{}, warnings, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.imported", updated.Name, "info", fmt.Sprintf("Imported %d mock routes from %s", len(routes), source), map[string]any{"source": source, "routes": len(routes)})
	s.publish(scope, "mock.state", updated)
	return updated, warnings, nil
}

// DeleteMockScenario removes a disabled mock scenario and its routes.
func (s *Service) DeleteMockScenario(ctx context.Context, project, environment, name, actor string) error {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	scenario, err := s.database.MockScenario(ctx, project, environment, name)
	if err != nil {
		return err
	}
	if scenario.Activation.State != model.MockScenarioDisabled {
		return fmt.Errorf("%w: mock scenario %s is %s; disable it before deleting it", errMockScenarioConflict, scenario.Name, scenario.Activation.State)
	}
	if err := s.database.DeleteMockScenario(ctx, project, environment, scenario.Name); err != nil {
		return err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.deleted", scenario.Name, "info", "Deleted mock scenario "+scenario.Name, nil)
	s.publish(scope, "mock.state", map[string]any{"name": scenario.Name, "deleted": true})
	return nil
}

// PutMockRoute validates and creates or replaces one deterministic route.
func (s *Service) PutMockRoute(ctx context.Context, project, environment, scenarioName string, route model.MockRoute, actor string) (model.MockScenario, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	scenario, err := s.database.MockScenario(ctx, project, environment, scenarioName)
	if err != nil {
		return model.MockScenario{}, err
	}
	service, err := s.validateMockService(ctx, project, route.Service)
	if err != nil {
		return model.MockScenario{}, err
	}
	route.Service = service
	beforeServices := mockScenarioServices(scenario.Routes)
	replaced := false
	for index := range scenario.Routes {
		if strings.EqualFold(scenario.Routes[index].Name, route.Name) {
			route.Name = scenario.Routes[index].Name
			route.CreatedAt = scenario.Routes[index].CreatedAt
			scenario.Routes[index] = route
			replaced = true
			break
		}
	}
	if !replaced {
		scenario.Routes = append(scenario.Routes, route)
	}
	if scenario.Activation.State != model.MockScenarioDisabled && !sameMockServices(beforeServices, mockScenarioServices(scenario.Routes)) {
		return model.MockScenario{}, fmt.Errorf("%w: disable the mock scenario before changing which services it covers", errMockScenarioConflict)
	}
	if _, err := mocks.Compile(scenario); err != nil {
		return model.MockScenario{}, err
	}
	updated, err := s.database.PutMockRoute(ctx, project, environment, scenario.Name, route)
	if err != nil {
		return model.MockScenario{}, err
	}
	if err := s.refreshActiveMockScenario(ctx, updated); err != nil {
		return model.MockScenario{}, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.route_changed", updated.Name, "info", "Updated route "+route.Name+" in mock scenario "+updated.Name, map[string]any{"route": route.Name, "service": route.Service})
	s.publish(scope, "mock.state", updated)
	return updated, nil
}

// DeleteMockRoute removes one route and reloads every active service matcher.
func (s *Service) DeleteMockRoute(ctx context.Context, project, environment, scenarioName, routeName, actor string) (model.MockScenario, error) {
	lock := s.projectLock(model.EnvironmentSelector(project, environment))
	lock.Lock()
	defer lock.Unlock()
	scenario, err := s.database.MockScenario(ctx, project, environment, scenarioName)
	if err != nil {
		return model.MockScenario{}, err
	}
	beforeServices := mockScenarioServices(scenario.Routes)
	found := false
	remaining := make([]model.MockRoute, 0, len(scenario.Routes))
	for _, route := range scenario.Routes {
		if strings.EqualFold(route.Name, routeName) {
			found = true
			continue
		}
		remaining = append(remaining, route)
	}
	if !found {
		return model.MockScenario{}, database.ErrNotFound
	}
	if scenario.Activation.State != model.MockScenarioDisabled && !sameMockServices(beforeServices, mockScenarioServices(remaining)) {
		return model.MockScenario{}, fmt.Errorf("%w: disable the mock scenario before deleting the final route for a service", errMockScenarioConflict)
	}
	updated, err := s.database.DeleteMockRoute(ctx, project, environment, scenario.Name, routeName)
	if err != nil {
		return model.MockScenario{}, err
	}
	if err := s.refreshActiveMockScenario(ctx, updated); err != nil {
		return model.MockScenario{}, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.route_deleted", updated.Name, "info", "Deleted route "+routeName+" from mock scenario "+updated.Name, map[string]any{"route": routeName})
	s.publish(scope, "mock.state", updated)
	return updated, nil
}

// PreviewMock evaluates one request against one service in a scenario.
func (s *Service) PreviewMock(ctx context.Context, project, environment, scenarioName string, request model.MockRequest) (model.MockPreview, error) {
	scenario, err := s.database.MockScenario(ctx, project, environment, scenarioName)
	if err != nil {
		return model.MockPreview{}, err
	}
	service, err := s.validateMockService(ctx, project, request.Service)
	if err != nil {
		return model.MockPreview{}, err
	}
	request.Service = service
	compiled, err := mocks.Compile(scenario)
	if err != nil {
		return model.MockPreview{}, err
	}
	return compiled.Preview(request)
}

func (s *Service) activateMock(ctx context.Context, scope string, binding model.ComponentBinding, runtime model.Service) error {
	if binding.Mock == nil {
		return errors.New("mock target configuration is missing")
	}
	project, environment := scopeNames(scope)
	scenario, err := s.database.MockScenario(ctx, project, environment, binding.Mock.Scenario)
	if err != nil {
		return fmt.Errorf("load mock scenario %s: %w", binding.Mock.Scenario, err)
	}
	if !containsMockService(scenario.Routes, binding.Service) {
		return fmt.Errorf("mock scenario %s has no routes for %s", scenario.Name, binding.Service)
	}
	port, err := s.mocks.Set(scope, binding.Service, scenario)
	if err != nil {
		return err
	}
	s.proxy.SetTargetProvider(scope, binding.Service, port, model.ProviderMock)
	now := time.Now().UTC()
	return s.database.SetServiceRuntime(ctx, scope, binding.Service, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Reason: "mock scenario " + scenario.Name,
		Generation: runtime.Generation, RestartCount: runtime.RestartCount,
		OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now, LaunchMode: model.LaunchManaged,
	})
}

func (s *Service) refreshActiveMockScenario(ctx context.Context, scenario model.MockScenario) error {
	environment, err := s.database.Environment(ctx, scenario.Project, scenario.Environment)
	if err != nil {
		return err
	}
	if environment.Status == model.EnvironmentStopped {
		return nil
	}
	var result error
	for _, binding := range environment.Bindings {
		if binding.Provider != model.ProviderMock || binding.Mock == nil || !strings.EqualFold(binding.Mock.Scenario, scenario.Name) {
			continue
		}
		runtime := runtimeFor(environment, binding.Service)
		if serviceRuntimeActive(runtime.Status) {
			result = errors.Join(result, s.activateMock(ctx, model.EnvironmentSelector(scenario.Project, scenario.Environment), binding, runtime))
		}
	}
	return result
}

func (s *Service) validateMockService(ctx context.Context, project, requested string) (string, error) {
	if err := model.ValidateServiceName(requested); err != nil {
		return "", fmt.Errorf("mock route service: %w", err)
	}
	definition, err := s.database.ProjectModel(ctx, project)
	if err != nil {
		return "", err
	}
	service, found := serviceDefinition(definition, requested)
	if !found {
		return "", database.ErrNotFound
	}
	if service.Kind != model.ServiceProcess {
		return "", errors.New("mock routes can only target HTTP application services")
	}
	for _, connection := range definition.Connections {
		if strings.EqualFold(connection.Target, service.Name) && connection.Protocol != model.ProtocolHTTP {
			return "", fmt.Errorf("service %s has a %s connection and cannot use an HTTP mock", service.Name, connection.Protocol)
		}
	}
	return service.Name, nil
}

func mockScenarioServices(routes []model.MockRoute) []string {
	seen := map[string]string{}
	for _, route := range routes {
		seen[strings.ToLower(route.Service)] = route.Service
	}
	result := make([]string, 0, len(seen))
	for _, service := range seen {
		result = append(result, service)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func sameMockServices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(left[index], right[index]) {
			return false
		}
	}
	return true
}

func containsMockService(routes []model.MockRoute, service string) bool {
	for _, route := range routes {
		if strings.EqualFold(route.Service, service) {
			return true
		}
	}
	return false
}

func mockRoutesFromRecording(allowed map[string]string, exchanges []model.TrafficExchange) ([]model.MockRoute, []string) {
	seen := map[string]struct{}{}
	names := map[string]int{}
	routes := []model.MockRoute{}
	duplicateCount, missingBodyCount, omittedCount := 0, 0, 0
	for _, exchange := range exchanges {
		service, allowedService := allowed[strings.ToLower(exchange.Target)]
		if exchange.Protocol != model.ProtocolHTTP || !allowedService || exchange.Method == "" || exchange.Status < 100 {
			continue
		}
		requestURL, err := url.ParseRequestURI(exchange.RequestTarget)
		if err != nil || requestURL.Path == "" {
			requestURL = &url.URL{Path: exchange.Path}
		}
		query := map[string]string{}
		for key, values := range requestURL.Query() {
			if len(values) > 0 {
				query[key] = values[0]
			}
		}
		normalizedQuery := url.Values{}
		for key, value := range query {
			normalizedQuery.Set(key, value)
		}
		identity := strings.ToLower(service) + "\x00" + strings.ToUpper(exchange.Method) + "\x00" + requestURL.Path + "\x00" + normalizedQuery.Encode()
		if _, exists := seen[identity]; exists {
			duplicateCount++
			continue
		}
		if len(routes) == mocks.MaxRoutesPerScenario {
			omittedCount++
			continue
		}
		seen[identity] = struct{}{}
		baseName := model.NormalizeDNSName(strings.ToLower(service) + "-" + strings.ToLower(exchange.Method) + "-" + strings.Trim(strings.ReplaceAll(requestURL.Path, "/", "-"), "-"))
		if baseName == "" {
			baseName = model.NormalizeDNSName(service + "-route")
		}
		names[baseName]++
		name := baseName
		if names[baseName] > 1 {
			name = uniqueMockRouteName(baseName, names[baseName])
		}
		headers := safeMockResponseHeaders(exchange.ResponseHeaders)
		if exchange.ResponseBytes > 0 && exchange.ResponseBody == "" {
			missingBodyCount++
		}
		routes = append(routes, model.MockRoute{
			Name: name, Service: service, Method: exchange.Method, Path: requestURL.Path, Query: query,
			Status: exchange.Status, Headers: headers, Body: exchange.ResponseBody, Enabled: true,
		})
	}
	warnings := []string{}
	if len(routes) == 0 {
		warnings = append(warnings, "The recording contained no eligible HTTP exchanges for the selected services.")
	}
	if duplicateCount > 0 {
		warnings = append(warnings, fmt.Sprintf("Ignored %d older duplicate exchanges; the newest response for each exact request was used.", duplicateCount))
	}
	if missingBodyCount > 0 {
		warnings = append(warnings, fmt.Sprintf("%d responses had no captured body; those mock routes return an empty body.", missingBodyCount))
	}
	if omittedCount > 0 {
		warnings = append(warnings, fmt.Sprintf("Ignored %d older request variants after reaching the %d-route scenario limit.", omittedCount, mocks.MaxRoutesPerScenario))
	}
	return routes, warnings
}

func uniqueMockRouteName(base string, number int) string {
	suffix := fmt.Sprintf("-%d", number)
	if len(base)+len(suffix) > 64 {
		base = strings.TrimRight(base[:64-len(suffix)], "-._")
	}
	return base + suffix
}

func availableMockRouteName(base string, used map[string]struct{}) string {
	if _, exists := used[strings.ToLower(base)]; !exists {
		return base
	}
	for number := 2; ; number++ {
		candidate := uniqueMockRouteName(base, number)
		if _, exists := used[strings.ToLower(candidate)]; !exists {
			return candidate
		}
	}
}

func safeMockResponseHeaders(headers map[string][]string) map[string]string {
	allowed := map[string]struct{}{"content-type": {}, "cache-control": {}, "etag": {}, "location": {}, "content-language": {}}
	result := map[string]string{}
	for name, values := range headers {
		if _, ok := allowed[strings.ToLower(name)]; ok && len(values) > 0 && values[0] != "[REDACTED]" {
			result[name] = values[0]
		}
	}
	return result
}
