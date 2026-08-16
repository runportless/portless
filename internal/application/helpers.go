package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/networking"
	"github.com/portless-run/portless/internal/resource"
)

func (s *Service) decorateProject(project model.Project) model.Project {
	project.DashboardURL = fmt.Sprintf("http://portless.localhost/projects/%s", project.Name)
	for index := range project.Environments {
		project.Environments[index].DashboardURL = fmt.Sprintf("http://portless.localhost/environments/%s/%s", project.Name, project.Environments[index].Name)
	}
	return project
}

func (s *Service) decorateEnvironment(environment model.Environment) model.Environment {
	environment.DashboardURL = fmt.Sprintf("http://portless.localhost/environments/%s/%s", environment.Project, environment.Name)
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	allocations, _ := s.store.NetworkAllocations(context.Background(), scope)
	publicEndpoints := make(map[string][]model.Endpoint)
	for _, allocation := range allocations {
		if allocation.Kind == networking.AllocationPublic {
			publicEndpoints[strings.ToLower(allocation.Target)] = append(publicEndpoints[strings.ToLower(allocation.Target)], allocation.Endpoint(model.EndpointPublic))
		}
	}
	recentTraffic := s.broker.RecentTraffic(model.EnvironmentSelector(environment.Project, environment.Name), 1000)
	cutoff := time.Now().UTC().Add(-30 * time.Second)
	requestDurations := make(map[string][]int64)
	for _, event := range recentTraffic {
		if event.Protocol != model.ProtocolHTTP || event.CompletedAt.Before(cutoff) {
			continue
		}
		requestDurations[event.Target] = append(requestDurations[event.Target], event.DurationMS)
	}
	for index := range environment.Services {
		environment.Services[index].Endpoints = []model.Endpoint{}
		if environment.Services[index].Kind == model.ServiceProcess {
			host := fmt.Sprintf("%s.%s.%s.localhost", environment.Services[index].Name, environment.Name, environment.Project)
			environment.Services[index].Endpoints = append(environment.Services[index].Endpoints, model.Endpoint{
				Kind: model.EndpointPublic, Protocol: model.ProtocolHTTP, Host: host, Port: 80,
				URL: networking.HTTPURL(environment.Services[index].Name, environment.Name, environment.Project),
			})
		}
		environment.Services[index].Endpoints = append(environment.Services[index].Endpoints, publicEndpoints[strings.ToLower(environment.Services[index].Name)]...)
		durations := requestDurations[environment.Services[index].Name]
		environment.Services[index].RecentRequest = int64(len(durations))
		if len(durations) > 0 {
			sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
			percentileIndex := (95*len(durations) + 99) / 100
			environment.Services[index].P95Millis = durations[percentileIndex-1]
		}
	}
	return environment
}

func (s *Service) publish(scope, eventType string, data any) {
	project, environment := scopeNames(scope)
	s.broker.Publish(events.Event{Type: eventType, Project: project, Environment: environment, Data: data})
}

func (s *Service) projectLock(project string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.projectLocks[project]
	if lock == nil {
		lock = &sync.Mutex{}
		s.projectLocks[project] = lock
	}
	return lock
}

func (s *Service) containerEnvironmentFor(project, target string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	input := s.containerEnvironment[targetEnvironmentKey(project, target)]
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (s *Service) acquireSourceLeases(scope string, environment model.Environment) error {
	used := make(map[string]struct{})
	for _, binding := range environment.Bindings {
		if binding.Provider == model.ProviderLocal {
			used[binding.Source] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, source := range environment.Sources {
		if _, ok := used[source.Name]; !ok {
			continue
		}
		if owner := s.sourceLeases[source.Path]; owner != "" && owner != scope {
			return fmt.Errorf("source %s is already running in %s; bind a Git worktree to run both environments concurrently", source.Path, owner)
		}
	}
	for _, source := range environment.Sources {
		if _, ok := used[source.Name]; ok {
			s.sourceLeases[source.Path] = scope
		}
	}
	return nil
}

func (s *Service) releaseSourceLeases(scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, owner := range s.sourceLeases {
		if owner == scope {
			delete(s.sourceLeases, path)
		}
	}
}

func (s *Service) releaseSourceLeasesIfIdle(scope string) {
	environment, err := s.store.EnvironmentBySelector(context.Background(), scope)
	if err != nil {
		return
	}
	for _, service := range environment.Services {
		if service.Kind != model.ServiceProcess || bindingForEnvironment(environment, service.Name).Provider != model.ProviderLocal {
			continue
		}
		switch service.Status {
		case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
			return
		}
	}
	s.releaseSourceLeases(scope)
}

func (s *Service) connectionBinding(target model.ServiceDefinition, connection model.Connection, host string, port int, targetEnvironment map[string]string, active bool) (resource.BindingResult, error) {
	if connection.Environment == "" {
		return resource.BindingResult{}, nil
	}
	if connection.Binding != "" {
		return s.resources.Bind(target, connection, resource.BindingContext{
			Environment: connection.Environment, Host: host, Port: port,
			TargetEnvironment: targetEnvironment, Active: active,
		})
	}
	if !active {
		return resource.BindingResult{SafeValues: map[string]string{connection.Environment: "not active"}}, nil
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	value := address
	if connection.Protocol == model.ProtocolHTTP {
		value = "http://" + address
	}
	return resource.BindingResult{
		Values: map[string]string{connection.Environment: value}, SafeValues: map[string]string{connection.Environment: value},
	}, nil
}

func secretConfigurationKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "API_KEY") || strings.HasSuffix(upper, "_KEY")
}

func shouldMaskConfiguration(key, value string) bool {
	if secretConfigurationKey(key) {
		return true
	}
	parsed, err := url.Parse(strings.TrimPrefix(value, "jdbc:"))
	return err == nil && parsed.User != nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func serviceDefinitionForEnvironment(environment model.Environment, name string) (model.ServiceDefinition, bool) {
	for _, service := range environment.Services {
		if service.Name == name {
			return service.ServiceDefinition, true
		}
	}
	return model.ServiceDefinition{}, false
}

func runtimeFor(environment model.Environment, name string) model.Service {
	for _, service := range environment.Services {
		if service.Name == name {
			return service
		}
	}
	return model.Service{ServiceDefinition: model.ServiceDefinition{Name: name}}
}

func bindingForEnvironment(environment model.Environment, name string) model.ComponentBinding {
	for _, binding := range environment.Bindings {
		if binding.Service == name {
			return binding
		}
	}
	return model.ComponentBinding{Service: name}
}

func bindingByName(bindings []model.ComponentBinding, name string) (model.ComponentBinding, bool) {
	for _, binding := range bindings {
		if strings.EqualFold(binding.Service, name) {
			return binding, true
		}
	}
	return model.ComponentBinding{}, false
}

func configurationCanBeSaved(issues []model.ConfigurationIssue) bool {
	for _, issue := range issues {
		switch issue.Code {
		case "MISSING_BINDING", "MISSING_SOURCE":
		default:
			return false
		}
	}
	return true
}

func scopeNames(scope string) (string, string) {
	project, environment, err := model.ParseEnvironmentSelector(scope)
	if err != nil {
		return scope, "local"
	}
	return project, environment
}

func projectNameSuggestions(name, root string) []string {
	parent := model.NormalizeDNSName(filepath.Base(filepath.Dir(root)))
	var suggestions []string
	if parent != "" && parent != name {
		suggestions = append(suggestions, model.NormalizeDNSName(name+"-"+parent))
	}
	suggestions = append(suggestions, model.NormalizeDNSName(name+"-checkout"), model.NormalizeDNSName(name+"-dev"))
	return suggestions
}

func validateExperimentScope(definition model.ProjectModel, source, target string, allowPartial bool) error {
	if !allowPartial && (source == "" || target == "") {
		return errors.New("fault source and target are required")
	}
	services := make(map[string]model.ServiceDefinition, len(definition.Services))
	for _, service := range definition.Services {
		services[service.Name] = service
	}
	if source != "" && source != "external" {
		if _, ok := services[source]; !ok {
			return fmt.Errorf("source service %s does not exist", source)
		}
	}
	if target != "" {
		if _, ok := services[target]; !ok {
			return fmt.Errorf("target service %s does not exist", target)
		}
	}
	if source == "external" && target != "" && services[target].Kind != model.ServiceProcess {
		return fmt.Errorf("external ingress is only available for process services; %s is a managed container", target)
	}
	if source == "external" && target != "" && target != definition.PrimaryService {
		return invalidExperimentConnection(definition, source, target)
	}
	if source != "" && source != "external" && target != "" {
		for _, connection := range definition.Connections {
			if connection.Source == source && connection.Target == target {
				return nil
			}
		}
		return invalidExperimentConnection(definition, source, target)
	}
	return nil
}

func invalidExperimentConnection(definition model.ProjectModel, source, target string) error {
	available := make([]string, 0, len(definition.Connections)+1)
	if definition.PrimaryService != "" {
		for _, service := range definition.Services {
			if service.Name == definition.PrimaryService && service.Kind == model.ServiceProcess {
				available = append(available, "external → "+definition.PrimaryService)
				break
			}
		}
	}
	for _, connection := range definition.Connections {
		available = append(available, connection.Source+" → "+connection.Target)
	}
	if len(available) == 0 {
		return fmt.Errorf("%s → %s is not a configured connection; this project has no configurable connections", source, target)
	}
	return fmt.Errorf("%s → %s is not a configured connection; choose one of: %s", source, target, strings.Join(available, ", "))
}

func targetEnvironmentKey(project, service string) string { return project + "\x00" + service }

func mustRecordings(recordings []model.Recording, err error) []model.Recording {
	if err != nil {
		return nil
	}
	return recordings
}

func mustLogPath(path string, err error) string {
	if err != nil {
		return ""
	}
	return path
}
