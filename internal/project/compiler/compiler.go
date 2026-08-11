package compiler

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/portless-run/portless/internal/model"
)

type Result struct {
	Definition model.ProjectModel
	Bindings   []model.ComponentBinding
	Issues     []model.ConfigurationIssue
}

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
				if service.Kind == model.ServiceContainer && previous.Kind == model.ServiceContainer && previous.Template == service.Template {
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
		if service.Kind == model.ServiceContainer {
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
			result.Issues = append(result.Issues, issue("MISSING_BINDING", logical.Name, "component has no provider binding", "bind it to a local source, managed container, or remote service"))
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
			if logical.Kind != model.ServiceContainer {
				result.Issues = append(result.Issues, issue("INVALID_CONTAINER_BINDING", logical.Name, "component is not a managed container template", "bind it to its local source or a remote HTTP service"))
				continue
			}
			effective = append(effective, logical)
		case model.ProviderRemote:
			if logical.Kind != model.ServiceProcess {
				result.Issues = append(result.Issues, issue("REMOTE_PROTOCOL_UNSUPPORTED", logical.Name, "only HTTP application services can be remote in this release", "use a local managed dependency"))
				continue
			}
			if err := ValidateRemote(binding.Remote); err != nil {
				result.Issues = append(result.Issues, issue("INVALID_REMOTE", logical.Name, err.Error(), "edit the remote provider binding"))
				continue
			}
			effective = append(effective, logical)
		default:
			result.Issues = append(result.Issues, issue("INVALID_PROVIDER", logical.Name, "unknown provider "+string(binding.Provider), "choose local, container, or remote"))
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
	effective = pruneUnusedContainers(effective, resolved, bindingByService)
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

func pruneUnusedContainers(services []model.ServiceDefinition, connections []model.Connection, bindings map[string]model.ComponentBinding) []model.ServiceDefinition {
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
			if bindings[target].Provider == model.ProviderRemote {
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
		if service.Kind == model.ServiceContainer {
			if _, ok := active[strings.ToLower(service.Name)]; !ok {
				continue
			}
		}
		result = append(result, service)
	}
	return result
}

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

type ConfigurationError struct {
	Issues []model.ConfigurationIssue
}

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
		result = append(result, model.Connection{Source: reference.Source, Target: target, Protocol: reference.Protocol, Environment: reference.Environment, Required: reference.Required})
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
