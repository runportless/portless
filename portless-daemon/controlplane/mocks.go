package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/database"
	"github.com/portless-run/portless/portless-daemon/mocks"
	"github.com/portless-run/portless/portless-daemon/model"
)

// MockProfiles lists the mock profiles owned by one environment.
func (s *Service) MockProfiles(ctx context.Context, project, environment string) ([]model.MockProfile, error) {
	return s.database.MockProfiles(ctx, project, environment)
}

// MockProfile returns one environment-scoped mock profile.
func (s *Service) MockProfile(ctx context.Context, project, environment, name string) (model.MockProfile, error) {
	return s.database.MockProfile(ctx, project, environment, name)
}

// CreateMockProfile creates an empty HTTP mock profile for a declared application service.
func (s *Service) CreateMockProfile(ctx context.Context, project, environment string, profile model.MockProfile, actor string) (model.MockProfile, error) {
	definition, err := s.database.ProjectModel(ctx, project)
	if err != nil {
		return model.MockProfile{}, err
	}
	service, found := serviceDefinition(definition, profile.Service)
	if !found {
		return model.MockProfile{}, database.ErrNotFound
	}
	if service.Kind != model.ServiceProcess {
		return model.MockProfile{}, errors.New("mock profiles can only be attached to HTTP application services")
	}
	for _, connection := range definition.Connections {
		if strings.EqualFold(connection.Target, service.Name) && connection.Protocol != model.ProtocolHTTP {
			return model.MockProfile{}, fmt.Errorf("service %s has a %s connection and cannot use an HTTP mock", service.Name, connection.Protocol)
		}
	}
	profile.Service = service.Name
	profile.Routes = nil
	created, err := s.database.CreateMockProfile(ctx, project, environment, profile)
	if err != nil {
		return model.MockProfile{}, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.created", created.Name, "info", "Created mock profile "+created.Name+" for "+created.Service, nil)
	s.publish(scope, "mock.state", created)
	return created, nil
}

// CreateMockProfileFromSources creates a profile and optionally derives routes
// from one retained recording or one local OpenAPI document supplied by a client.
func (s *Service) CreateMockProfileFromSources(ctx context.Context, project, environment string, profile model.MockProfile, recordingName string, openAPIDocument []byte, actor string) (model.MockProfile, []string, error) {
	if recordingName != "" && len(openAPIDocument) > 0 {
		return model.MockProfile{}, nil, errors.New("choose either a recording or an OpenAPI document, not both")
	}
	if recordingName == "" && len(openAPIDocument) == 0 {
		created, err := s.CreateMockProfile(ctx, project, environment, profile, actor)
		return created, nil, err
	}
	if len(openAPIDocument) > 1<<20 {
		return model.MockProfile{}, nil, errors.New("OpenAPI document must not exceed 1048576 bytes")
	}
	if len(openAPIDocument) > 0 {
		routes, warnings, err := mocks.RoutesFromOpenAPI(openAPIDocument)
		if err != nil {
			return model.MockProfile{}, warnings, err
		}
		return s.createMockProfileWithRoutes(ctx, project, environment, profile, routes, warnings, "OpenAPI document", actor)
	}
	recording, err := s.database.Recording(ctx, model.EnvironmentSelector(project, environment), recordingName)
	if err != nil {
		return model.MockProfile{}, nil, err
	}
	if recording.Status == "active" {
		return model.MockProfile{}, nil, fmt.Errorf("recording %s is still active; stop it before creating a mock", recording.Name)
	}
	exchanges, err := s.database.RecordedTraffic(ctx, model.EnvironmentSelector(project, environment), recording.Name, 10_000)
	if err != nil {
		return model.MockProfile{}, nil, err
	}
	routes, warnings := mockRoutesFromRecording(profile.Service, exchanges)
	return s.createMockProfileWithRoutes(ctx, project, environment, profile, routes, warnings, "recording "+recording.Name, actor)
}

func (s *Service) createMockProfileWithRoutes(ctx context.Context, project, environment string, profile model.MockProfile, routes []model.MockRoute, warnings []string, source, actor string) (model.MockProfile, []string, error) {
	profile.Routes = routes
	if _, err := mocks.Compile(profile); err != nil {
		return model.MockProfile{}, warnings, err
	}
	created, err := s.CreateMockProfile(ctx, project, environment, profile, actor)
	if err != nil {
		return model.MockProfile{}, warnings, err
	}
	for _, route := range routes {
		if _, err := s.database.PutMockRoute(ctx, project, environment, created.Name, route); err != nil {
			_ = s.database.DeleteMockProfile(context.Background(), project, environment, created.Name)
			return model.MockProfile{}, warnings, err
		}
	}
	created, err = s.database.MockProfile(ctx, project, environment, created.Name)
	if err != nil {
		return model.MockProfile{}, warnings, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.imported", created.Name, "info", fmt.Sprintf("Created %d mock routes from %s", len(created.Routes), source), map[string]any{"source": source, "routes": len(created.Routes)})
	s.publish(scope, "mock.state", created)
	return created, warnings, nil
}

// DeleteMockProfile removes an unbound mock profile and its routes.
func (s *Service) DeleteMockProfile(ctx context.Context, project, environment, name, actor string) error {
	current, err := s.database.Environment(ctx, project, environment)
	if err != nil {
		return err
	}
	profile, err := s.database.MockProfile(ctx, project, environment, name)
	if err != nil {
		return err
	}
	for _, binding := range current.Bindings {
		if binding.Provider == model.ProviderMock && binding.Mock != nil && strings.EqualFold(binding.Mock.Profile, profile.Name) {
			return fmt.Errorf("mock profile %s is bound to service %s; change that provider before deleting it", profile.Name, binding.Service)
		}
	}
	if err := s.database.DeleteMockProfile(ctx, project, environment, profile.Name); err != nil {
		return err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.deleted", profile.Name, "info", "Deleted mock profile "+profile.Name, nil)
	s.publish(scope, "mock.state", map[string]any{"name": profile.Name, "deleted": true})
	return nil
}

// PutMockRoute validates and creates or replaces one deterministic route.
func (s *Service) PutMockRoute(ctx context.Context, project, environment, profileName string, route model.MockRoute, actor string) (model.MockProfile, error) {
	profile, err := s.database.MockProfile(ctx, project, environment, profileName)
	if err != nil {
		return model.MockProfile{}, err
	}
	replaced := false
	for index := range profile.Routes {
		if strings.EqualFold(profile.Routes[index].Name, route.Name) {
			route.Name = profile.Routes[index].Name
			route.CreatedAt = profile.Routes[index].CreatedAt
			profile.Routes[index] = route
			replaced = true
			break
		}
	}
	if !replaced {
		profile.Routes = append(profile.Routes, route)
	}
	if _, err := mocks.Compile(profile); err != nil {
		return model.MockProfile{}, err
	}
	updated, err := s.database.PutMockRoute(ctx, project, environment, profile.Name, route)
	if err != nil {
		return model.MockProfile{}, err
	}
	if err := s.refreshActiveMock(ctx, updated); err != nil {
		return model.MockProfile{}, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.route_changed", updated.Name, "info", "Updated route "+route.Name+" in mock profile "+updated.Name, map[string]any{"route": route.Name})
	s.publish(scope, "mock.state", updated)
	return updated, nil
}

// DeleteMockRoute removes one route and atomically reloads an active profile.
func (s *Service) DeleteMockRoute(ctx context.Context, project, environment, profileName, routeName, actor string) (model.MockProfile, error) {
	updated, err := s.database.DeleteMockRoute(ctx, project, environment, profileName, routeName)
	if err != nil {
		return model.MockProfile{}, err
	}
	if err := s.refreshActiveMock(ctx, updated); err != nil {
		return model.MockProfile{}, err
	}
	scope := model.EnvironmentSelector(project, environment)
	_, _ = s.timeline(ctx, scope, actor, "mock.route_deleted", updated.Name, "info", "Deleted route "+routeName+" from mock profile "+updated.Name, map[string]any{"route": routeName})
	s.publish(scope, "mock.state", updated)
	return updated, nil
}

// PreviewMock evaluates one request against a profile without emitting traffic.
func (s *Service) PreviewMock(ctx context.Context, project, environment, profileName string, request model.MockRequest) (model.MockPreview, error) {
	profile, err := s.database.MockProfile(ctx, project, environment, profileName)
	if err != nil {
		return model.MockPreview{}, err
	}
	compiled, err := mocks.Compile(profile)
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
	profile, err := s.database.MockProfile(ctx, project, environment, binding.Mock.Profile)
	if err != nil {
		return fmt.Errorf("load mock profile %s: %w", binding.Mock.Profile, err)
	}
	if !strings.EqualFold(profile.Service, binding.Service) {
		return fmt.Errorf("mock profile %s belongs to %s, not %s", profile.Name, profile.Service, binding.Service)
	}
	port, err := s.mocks.Set(scope, binding.Service, profile)
	if err != nil {
		return err
	}
	s.proxy.SetTargetProvider(scope, binding.Service, port, model.ProviderMock)
	now := time.Now().UTC()
	return s.database.SetServiceRuntime(ctx, scope, binding.Service, database.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Reason: "mock profile " + profile.Name,
		Generation: runtime.Generation, RestartCount: runtime.RestartCount,
		OwnerInstanceID: s.daemonInstanceID, ObservedAt: &now, LaunchMode: model.LaunchManaged,
	})
}

func (s *Service) refreshActiveMock(ctx context.Context, profile model.MockProfile) error {
	environment, err := s.database.Environment(ctx, profile.Project, profile.Environment)
	if err != nil {
		return err
	}
	if environment.Status == model.EnvironmentStopped {
		return nil
	}
	for _, binding := range environment.Bindings {
		if binding.Provider == model.ProviderMock && binding.Mock != nil && strings.EqualFold(binding.Mock.Profile, profile.Name) {
			runtime := runtimeFor(environment, binding.Service)
			if !serviceRuntimeActive(runtime.Status) {
				return nil
			}
			return s.activateMock(ctx, model.EnvironmentSelector(profile.Project, profile.Environment), binding, runtime)
		}
	}
	return nil
}

func mockRoutesFromRecording(service string, exchanges []model.TrafficExchange) ([]model.MockRoute, []string) {
	seen := map[string]struct{}{}
	names := map[string]int{}
	routes := []model.MockRoute{}
	duplicateCount, missingBodyCount, omittedCount := 0, 0, 0
	for _, exchange := range exchanges {
		if exchange.Protocol != model.ProtocolHTTP || !strings.EqualFold(exchange.Target, service) || exchange.Method == "" || exchange.Status < 100 {
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
		identity := strings.ToUpper(exchange.Method) + "\x00" + requestURL.Path + "\x00" + normalizedQuery.Encode()
		if _, exists := seen[identity]; exists {
			duplicateCount++
			continue
		}
		if len(routes) == mocks.MaxRoutesPerProfile {
			omittedCount++
			continue
		}
		seen[identity] = struct{}{}
		baseName := model.NormalizeDNSName(strings.ToLower(exchange.Method) + "-" + strings.Trim(strings.ReplaceAll(requestURL.Path, "/", "-"), "-"))
		if baseName == "" {
			baseName = "route"
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
			Name: name, Method: exchange.Method, Path: requestURL.Path, Query: query,
			Status: exchange.Status, Headers: headers, Body: exchange.ResponseBody, Enabled: true,
		})
	}
	warnings := []string{}
	if len(routes) == 0 {
		warnings = append(warnings, "The recording contained no HTTP exchanges targeting service "+service+".")
	}
	if duplicateCount > 0 {
		warnings = append(warnings, fmt.Sprintf("Ignored %d older duplicate exchanges; the newest response for each exact request was used.", duplicateCount))
	}
	if missingBodyCount > 0 {
		warnings = append(warnings, fmt.Sprintf("%d responses had no captured body; those mock routes return an empty body.", missingBodyCount))
	}
	if omittedCount > 0 {
		warnings = append(warnings, fmt.Sprintf("Ignored %d older request variants after reaching the %d-route profile limit.", omittedCount, mocks.MaxRoutesPerProfile))
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
