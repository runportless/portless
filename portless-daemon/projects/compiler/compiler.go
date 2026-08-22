package compiler

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
)

// Result contains an effective environment model, normalized bindings, and configuration issues.
type Result struct {
	Definition model.ProjectModel
	Bindings   []model.ComponentBinding
	Issues     []model.ConfigurationIssue
}

// AddSource merges one newly discovered source into an existing logical
// project. The returned bindings are defaults only for components introduced
// by the source; callers decide which environment receives them.
func AddSource(project model.ProjectModel, projectSources []model.ProjectSource, source model.SourceBinding) (model.ProjectModel, []model.ProjectSource, []model.ComponentBinding, error) {
	if err := model.ValidateSourceName(source.Name); err != nil {
		return model.ProjectModel{}, nil, nil, fmt.Errorf("source %q: %w", source.Name, err)
	}
	for _, existing := range projectSources {
		if strings.EqualFold(existing.Name, source.Name) {
			return model.ProjectModel{}, nil, nil, fmt.Errorf("source %s already exists", source.Name)
		}
	}

	definition := project
	definition.Services = append([]model.ServiceDefinition{}, project.Services...)
	definition.Connections = append([]model.Connection{}, project.Connections...)
	definition.References = append([]model.ConnectionReference{}, project.References...)

	serviceDefinitions := make(map[string]model.ServiceDefinition, len(project.Services))
	serviceOwners := make(map[string]string, len(project.Services))
	for _, service := range project.Services {
		serviceDefinitions[strings.ToLower(service.Name)] = service
	}
	for _, existingSource := range projectSources {
		for _, serviceName := range existingSource.Services {
			serviceOwners[strings.ToLower(serviceName)] = existingSource.Name
		}
	}

	var provided []string
	var defaults []model.ComponentBinding
	for _, service := range source.Definition.Services {
		key := strings.ToLower(service.Name)
		if previous, exists := serviceDefinitions[key]; exists {
			if sameResourceService(service, previous) {
				continue
			}
			owner := serviceOwners[key]
			if owner == "" {
				owner = "the project"
			}
			return model.ProjectModel{}, nil, nil, fmt.Errorf("service %s is already provided by %s", service.Name, owner)
		}

		logical := service
		logical.WorkingDirectory = ""
		logical.ServiceDirectory = ""
		definition.Services = append(definition.Services, logical)
		serviceDefinitions[key] = logical
		if service.Kind == model.ServiceProcess {
			provided = append(provided, service.Name)
			serviceOwners[key] = source.Name
			defaults = append(defaults, model.ComponentBinding{Service: service.Name, Provider: model.ProviderLocal, Source: source.Name})
		} else {
			defaults = append(defaults, model.ComponentBinding{Service: service.Name, Provider: model.ProviderContainer})
		}
	}

	if definition.PrimaryService == "" && source.Definition.PrimaryService != "" {
		definition.PrimaryService = source.Definition.PrimaryService
	}
	definition.Connections = append(definition.Connections, source.Definition.Connections...)
	definition.References = append(definition.References, source.Definition.References...)
	sort.Slice(definition.Services, func(i, j int) bool { return definition.Services[i].Name < definition.Services[j].Name })
	definition.Connections = resolveConnections(definition.Services, definition.Connections, definition.References)
	definition.References = uniqueConnectionReferences(unresolvedReferences(definition.Services, definition.References))
	if definition.PrimaryService == "" && len(definition.Services) > 0 {
		definition.PrimaryService = definition.Services[0].Name
	}

	sort.Strings(provided)
	sources := append([]model.ProjectSource{}, projectSources...)
	sources = append(sources, model.ProjectSource{Name: source.Name, Services: provided})
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	sort.Slice(defaults, func(i, j int) bool { return defaults[i].Service < defaults[j].Service })
	return definition, sources, defaults, nil
}

// RemoveSource removes one logical project source, the process services it
// owns, and resource services that are no longer reachable from a remaining
// process service. It returns every removed service and connection so callers
// can present and persist the topology change explicitly.
func RemoveSource(project model.ProjectModel, projectSources []model.ProjectSource, sourceName string) (model.ProjectModel, []model.ProjectSource, []string, []model.Connection, error) {
	if len(projectSources) <= 1 {
		return model.ProjectModel{}, nil, nil, nil, errors.New("a project must retain at least one source; forget the project instead")
	}
	removedProcesses := map[string]struct{}{}
	remainingSources := make([]model.ProjectSource, 0, len(projectSources)-1)
	found := false
	for _, source := range projectSources {
		if strings.EqualFold(source.Name, sourceName) {
			found = true
			for _, service := range source.Services {
				removedProcesses[strings.ToLower(service)] = struct{}{}
			}
			continue
		}
		remainingSources = append(remainingSources, source)
	}
	if !found {
		return model.ProjectModel{}, nil, nil, nil, fmt.Errorf("source %s does not exist", sourceName)
	}

	candidates := make([]model.ServiceDefinition, 0, len(project.Services))
	candidateNames := make(map[string]model.ServiceDefinition, len(project.Services))
	active := map[string]struct{}{}
	for _, service := range project.Services {
		key := strings.ToLower(service.Name)
		if _, removed := removedProcesses[key]; removed {
			continue
		}
		candidates = append(candidates, service)
		candidateNames[key] = service
		if service.Kind == model.ServiceProcess {
			active[key] = struct{}{}
		}
	}
	candidateConnections := make([]model.Connection, 0, len(project.Connections))
	for _, connection := range project.Connections {
		if connection.Source != "external" {
			if _, ok := candidateNames[strings.ToLower(connection.Source)]; !ok {
				continue
			}
		}
		if _, ok := candidateNames[strings.ToLower(connection.Target)]; !ok {
			continue
		}
		candidateConnections = append(candidateConnections, connection)
	}
	changed := true
	for changed {
		changed = false
		for _, connection := range candidateConnections {
			if connection.Source != "external" {
				if _, ok := active[strings.ToLower(connection.Source)]; !ok {
					continue
				}
			}
			target := strings.ToLower(connection.Target)
			if _, ok := active[target]; !ok {
				active[target] = struct{}{}
				changed = true
			}
		}
	}

	result := project
	result.Services = nil
	retained := map[string]struct{}{}
	var removedServices []string
	for _, service := range candidates {
		key := strings.ToLower(service.Name)
		if service.Kind == model.ServiceResource {
			if _, ok := active[key]; !ok {
				removedServices = append(removedServices, service.Name)
				continue
			}
		}
		result.Services = append(result.Services, service)
		retained[key] = struct{}{}
	}
	for _, service := range project.Services {
		if _, ok := removedProcesses[strings.ToLower(service.Name)]; ok {
			removedServices = append(removedServices, service.Name)
		}
	}
	var removedConnections []model.Connection
	result.Connections = nil
	for _, connection := range project.Connections {
		_, targetRetained := retained[strings.ToLower(connection.Target)]
		_, sourceRetained := retained[strings.ToLower(connection.Source)]
		if connection.Source == "external" {
			sourceRetained = true
		}
		if !sourceRetained || !targetRetained {
			removedConnections = append(removedConnections, connection)
			continue
		}
		result.Connections = append(result.Connections, connection)
	}
	result.References = nil
	for _, reference := range project.References {
		if _, ok := retained[strings.ToLower(reference.Source)]; ok {
			result.References = append(result.References, reference)
		}
	}
	if _, ok := retained[strings.ToLower(result.PrimaryService)]; !ok {
		result.PrimaryService = ""
		for _, service := range result.Services {
			if service.Kind == model.ServiceProcess {
				result.PrimaryService = service.Name
				break
			}
		}
		if result.PrimaryService == "" && len(result.Services) > 0 {
			result.PrimaryService = result.Services[0].Name
		}
	}
	sort.Strings(removedServices)
	return result, remainingSources, removedServices, removedConnections, nil
}

// InitialProject combines discovered sources into reusable topology and default local bindings.
func InitialProject(name string, sources []model.SourceBinding) (model.ProjectModel, []model.ProjectSource, []model.ComponentBinding, error) {
	definition := model.ProjectModel{SuggestedName: name}
	serviceOwner := map[string]string{}
	serviceDefinitions := map[string]model.ServiceDefinition{}
	var projectSources []model.ProjectSource
	for _, source := range sources {
		if err := model.ValidateSourceName(source.Name); err != nil {
			return model.ProjectModel{}, nil, nil, fmt.Errorf("source %q: %w", source.Name, err)
		}
		var provided []string
		for _, service := range source.Definition.Services {
			key := strings.ToLower(service.Name)
			if previous, exists := serviceDefinitions[key]; exists {
				if sameResourceService(service, previous) {
					continue
				}
				return model.ProjectModel{}, nil, nil, fmt.Errorf("service %s is provided by both %s and %s", service.Name, serviceOwner[key], source.Name)
			}
			serviceDefinitions[key] = service
			serviceOwner[key] = source.Name
			if service.Kind == model.ServiceProcess {
				provided = append(provided, service.Name)
			}
		}
		if definition.PrimaryService == "" && source.Definition.PrimaryService != "" {
			definition.PrimaryService = source.Definition.PrimaryService
		}
		definition.Connections = append(definition.Connections, source.Definition.Connections...)
		definition.References = append(definition.References, source.Definition.References...)
		sort.Strings(provided)
		projectSources = append(projectSources, model.ProjectSource{Name: source.Name, Services: provided})
	}
	for _, service := range serviceDefinitions {
		logical := service
		logical.WorkingDirectory = ""
		logical.ServiceDirectory = ""
		definition.Services = append(definition.Services, logical)
	}
	sort.Slice(definition.Services, func(i, j int) bool { return definition.Services[i].Name < definition.Services[j].Name })
	definition.Connections = resolveConnections(definition.Services, definition.Connections, definition.References)
	definition.References = unresolvedReferences(definition.Services, definition.References)
	if definition.PrimaryService == "" && len(definition.Services) > 0 {
		definition.PrimaryService = definition.Services[0].Name
	}
	bindings := make([]model.ComponentBinding, 0, len(definition.Services))
	for _, service := range definition.Services {
		if service.Kind == model.ServiceResource {
			bindings = append(bindings, model.ComponentBinding{Service: service.Name, Provider: model.ProviderContainer})
			continue
		}
		bindings = append(bindings, model.ComponentBinding{Service: service.Name, Provider: model.ProviderLocal, Source: serviceOwner[strings.ToLower(service.Name)]})
	}
	compiled := Compile(definition, sources, bindings)
	if len(compiled.Issues) > 0 {
		return definition, projectSources, bindings, ConfigurationError{Issues: compiled.Issues}
	}
	return definition, projectSources, bindings, nil
}

// Compile resolves a logical project through environment-specific sources and providers.
func Compile(project model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) Result {
	result := Result{Definition: project}
	sourceByName := make(map[string]model.SourceBinding, len(sources))
	for _, source := range sources {
		key := strings.ToLower(source.Name)
		if _, exists := sourceByName[key]; exists {
			result.Issues = append(result.Issues, issue("DUPLICATE_SOURCE", source.Name, "source name is bound more than once", "rename one of the sources"))
			continue
		}
		sourceByName[key] = source
	}
	bindingByService := make(map[string]model.ComponentBinding, len(bindings))
	for _, binding := range bindings {
		key := strings.ToLower(binding.Service)
		if _, exists := bindingByService[key]; exists {
			result.Issues = append(result.Issues, issue("DUPLICATE_BINDING", binding.Service, "component has more than one provider binding", "choose one provider"))
			continue
		}
		bindingByService[key] = binding
	}

	var effective []model.ServiceDefinition
	for _, logical := range project.Services {
		binding, ok := bindingByService[strings.ToLower(logical.Name)]
		if !ok {
			result.Issues = append(result.Issues, issue("MISSING_BINDING", logical.Name, "component has no provider binding", "bind it to a local source, managed resource, remote service, or mock profile"))
			continue
		}
		switch binding.Provider {
		case model.ProviderLocal:
			source, ok := sourceByName[strings.ToLower(binding.Source)]
			if !ok {
				result.Issues = append(result.Issues, issue("MISSING_SOURCE", logical.Name, "local binding references source "+binding.Source+" which is not bound", "add the source to this environment"))
				continue
			}
			implementation, ok := serviceDefinition(source.Definition.Services, logical.Name)
			if !ok || implementation.Kind != model.ServiceProcess {
				result.Issues = append(result.Issues, issue("SERVICE_NOT_IN_SOURCE", logical.Name, "source "+source.Name+" does not provide this service", "rescan the source or choose a different source"))
				continue
			}
			effective = append(effective, implementation)
		case model.ProviderContainer:
			if logical.Kind != model.ServiceResource {
				result.Issues = append(result.Issues, issue("INVALID_RESOURCE_BINDING", logical.Name, "component is not a managed resource", "bind it to its local source or a remote HTTP service"))
				continue
			}
			effective = append(effective, logical)
		case model.ProviderRemote:
			if logical.Kind != model.ServiceProcess {
				result.Issues = append(result.Issues, issue("REMOTE_PROTOCOL_UNSUPPORTED", logical.Name, "only HTTP application services can be remote in this release", "use a local managed resource"))
				continue
			}
			if err := ValidateRemote(binding.Remote); err != nil {
				result.Issues = append(result.Issues, issue("INVALID_REMOTE", logical.Name, err.Error(), "edit the remote provider binding"))
				continue
			}
			effective = append(effective, logical)
		case model.ProviderMock:
			if logical.Kind != model.ServiceProcess {
				result.Issues = append(result.Issues, issue("MOCK_PROTOCOL_UNSUPPORTED", logical.Name, "only HTTP application services can use a mock provider", "use a managed resource provider"))
				continue
			}
			if err := ValidateMock(binding.Mock); err != nil {
				result.Issues = append(result.Issues, issue("INVALID_MOCK", logical.Name, err.Error(), "choose an existing mock profile"))
				continue
			}
			effective = append(effective, logical)
		default:
			result.Issues = append(result.Issues, issue("INVALID_PROVIDER", logical.Name, "unknown provider "+string(binding.Provider), "choose local, container, remote, or mock"))
			continue
		}
		result.Bindings = append(result.Bindings, normalizedBinding(binding))
	}
	connections := append([]model.Connection{}, project.Connections...)
	var references []model.ConnectionReference
	for _, source := range sources {
		connections = append(connections, source.Definition.Connections...)
		references = append(references, source.Definition.References...)
	}
	resolved := resolveConnections(effective, connections, references)
	effective = pruneUnusedResources(effective, resolved, bindingByService)
	result.Definition.Services = effective
	result.Definition.Connections = resolveConnections(effective, connections, references)
	result.Definition.References = unresolvedReferences(effective, references)
	for _, reference := range result.Definition.References {
		if reference.Required {
			result.Issues = append(result.Issues, issue("UNRESOLVED_CONNECTION", reference.Source+":"+reference.TargetHint,
				"dependency reference "+reference.Environment+" does not match a project component", "add or rename the target component"))
		}
	}
	result.Issues = uniqueIssues(result.Issues)
	return result
}

// RefreshDiscoveredTopology rebuilds a logical project's connections from its
// current source snapshots. Connections are discovery-owned in this release;
// provider bindings are the environment-owned configuration that is preserved.
func RefreshDiscoveredTopology(project model.ProjectModel, currentSources []model.SourceBinding) model.ProjectModel {
	currentConnections, currentReferences := sourceTopology(project.Services, currentSources)
	result := project
	result.Connections = currentConnections
	result.References = uniqueConnectionReferences(unresolvedReferences(project.Services, currentReferences))
	return result
}

func sourceTopology(services []model.ServiceDefinition, sources []model.SourceBinding) ([]model.Connection, []model.ConnectionReference) {
	var connections []model.Connection
	var references []model.ConnectionReference
	for _, source := range sources {
		connections = append(connections, source.Definition.Connections...)
		references = append(references, source.Definition.References...)
	}
	return resolveConnections(services, connections, references), references
}

func referenceKey(reference model.ConnectionReference) string {
	return strings.ToLower(reference.Source + "\x00" + reference.TargetHint + "\x00" + reference.Environment + "\x00" + string(reference.Protocol) + "\x00" + reference.Binding)
}

func uniqueConnectionReferences(input []model.ConnectionReference) []model.ConnectionReference {
	seen := make(map[string]struct{}, len(input))
	result := make([]model.ConnectionReference, 0, len(input))
	for _, reference := range input {
		key := referenceKey(reference)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	return result
}

func pruneUnusedResources(services []model.ServiceDefinition, connections []model.Connection, bindings map[string]model.ComponentBinding) []model.ServiceDefinition {
	active := make(map[string]struct{})
	for _, service := range services {
		binding := bindings[strings.ToLower(service.Name)]
		if service.Kind == model.ServiceProcess && binding.Provider == model.ProviderLocal {
			active[strings.ToLower(service.Name)] = struct{}{}
		}
	}
	changed := true
	for changed {
		changed = false
		for _, connection := range connections {
			if _, ok := active[strings.ToLower(connection.Source)]; !ok {
				continue
			}
			target := strings.ToLower(connection.Target)
			if bindings[target].Provider == model.ProviderRemote || bindings[target].Provider == model.ProviderMock {
				continue
			}
			if _, ok := active[target]; !ok {
				active[target] = struct{}{}
				changed = true
			}
		}
	}
	result := make([]model.ServiceDefinition, 0, len(services))
	for _, service := range services {
		if service.Kind == model.ServiceResource {
			if _, ok := active[strings.ToLower(service.Name)]; !ok {
				continue
			}
		}
		result = append(result, service)
	}
	return result
}

// ValidateRemote verifies the URL and explicit safety policy for an external provider.
func ValidateRemote(remote *model.RemoteTarget) error {
	if remote == nil {
		return errors.New("remote target configuration is missing")
	}
	parsed, err := url.Parse(remote.URL)
	if err != nil {
		return fmt.Errorf("remote URL is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("remote URL must use http or https")
	}
	if parsed.Hostname() == "" {
		return errors.New("remote URL must include a host")
	}
	if parsed.User != nil {
		return errors.New("remote URL cannot contain credentials")
	}
	if parsed.Fragment != "" {
		return errors.New("remote URL cannot contain a fragment")
	}
	if parsed.RawQuery != "" {
		return errors.New("remote URL cannot contain a query string")
	}
	switch remote.Classification {
	case model.RemoteDevelopment, model.RemoteQA, model.RemoteStaging, model.RemoteUnknown:
	default:
		return errors.New("remote classification must be development, qa, staging, or unknown")
	}
	switch remote.WritePolicy {
	case model.WriteReadOnly, model.WriteReadWrite:
	default:
		return errors.New("remote write policy must be read-only or read-write")
	}
	if remote.HealthPath != "" && !strings.HasPrefix(remote.HealthPath, "/") {
		return errors.New("remote health path must begin with /")
	}
	return nil
}

// ValidateMock verifies that a provider selects a syntactically valid profile.
func ValidateMock(mock *model.MockTarget) error {
	if mock == nil {
		return errors.New("mock target configuration is missing")
	}
	if err := model.ValidateArtifactName(mock.Profile); err != nil {
		return fmt.Errorf("mock profile is invalid: %w", err)
	}
	return nil
}

// ConfigurationError exposes one or more invalid environment configuration issues.
type ConfigurationError struct {
	Issues []model.ConfigurationIssue
}

// Error summarizes the contained configuration issues.
func (e ConfigurationError) Error() string {
	if len(e.Issues) == 1 {
		return e.Issues[0].Message
	}
	return fmt.Sprintf("environment configuration has %d issues", len(e.Issues))
}

func serviceDefinition(services []model.ServiceDefinition, name string) (model.ServiceDefinition, bool) {
	for _, service := range services {
		if strings.EqualFold(service.Name, name) {
			return service, true
		}
	}
	return model.ServiceDefinition{}, false
}

func sameResourceService(left, right model.ServiceDefinition) bool {
	return left.Kind == model.ServiceResource && right.Kind == model.ServiceResource && left.Resource != nil && right.Resource != nil &&
		*left.Resource == *right.Resource && left.Port == right.Port
}

func resolveConnections(services []model.ServiceDefinition, connections []model.Connection, references []model.ConnectionReference) []model.Connection {
	serviceNames := make(map[string]string, len(services))
	for _, service := range services {
		serviceNames[strings.ToLower(service.Name)] = service.Name
	}
	result := append([]model.Connection{}, connections...)
	for _, reference := range references {
		target, ok := serviceNames[strings.ToLower(reference.TargetHint)]
		if !ok {
			continue
		}
		if _, ok := serviceNames[strings.ToLower(reference.Source)]; !ok {
			continue
		}
		result = append(result, model.Connection{Source: reference.Source, Target: target, Protocol: reference.Protocol, Binding: reference.Binding, Environment: reference.Environment, Required: reference.Required})
	}
	seen := make(map[string]model.Connection)
	for _, connection := range result {
		if connection.Source != "external" {
			if _, ok := serviceNames[strings.ToLower(connection.Source)]; !ok {
				continue
			}
		}
		if _, ok := serviceNames[strings.ToLower(connection.Target)]; !ok {
			continue
		}
		key := strings.ToLower(connection.Source + "\x00" + connection.Target)
		if existing, ok := seen[key]; ok && existing.Environment != "" {
			continue
		}
		seen[key] = connection
	}
	result = result[:0]
	for _, connection := range seen {
		result = append(result, connection)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source == result[j].Source {
			return result[i].Target < result[j].Target
		}
		return result[i].Source < result[j].Source
	})
	return result
}

func unresolvedReferences(services []model.ServiceDefinition, references []model.ConnectionReference) []model.ConnectionReference {
	names := make(map[string]struct{}, len(services))
	for _, service := range services {
		names[strings.ToLower(service.Name)] = struct{}{}
	}
	var result []model.ConnectionReference
	for _, reference := range references {
		if _, ok := names[strings.ToLower(reference.Source)]; !ok {
			continue
		}
		if _, ok := names[strings.ToLower(reference.TargetHint)]; !ok {
			result = append(result, reference)
		}
	}
	return result
}

func normalizedBinding(binding model.ComponentBinding) model.ComponentBinding {
	if binding.Provider == model.ProviderRemote && binding.Remote != nil {
		copy := *binding.Remote
		if copy.Classification == "" {
			copy.Classification = model.RemoteUnknown
		}
		if copy.WritePolicy == "" {
			copy.WritePolicy = model.WriteReadOnly
		}
		binding.Remote = &copy
	}
	return binding
}

func issue(code, subject, message, remediation string) model.ConfigurationIssue {
	return model.ConfigurationIssue{Code: code, Subject: subject, Message: message, Remediation: remediation}
}

func uniqueIssues(input []model.ConfigurationIssue) []model.ConfigurationIssue {
	seen := make(map[string]struct{})
	var result []model.ConfigurationIssue
	for _, issue := range input {
		key := issue.Code + "\x00" + issue.Subject + "\x00" + issue.Message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, issue)
	}
	return result
}
