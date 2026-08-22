package controlplane

import (
	"fmt"
	"sort"

	"github.com/runportless/portless/portless-daemon/model"
)

func startOrder(definition model.ProjectModel) ([]string, error) {
	bindings := make([]model.ComponentBinding, 0, len(definition.Services))
	for _, service := range definition.Services {
		provider := model.ProviderLocal
		if service.Kind == model.ServiceResource {
			provider = model.ProviderContainer
		}
		bindings = append(bindings, model.ComponentBinding{Service: service.Name, Provider: provider})
	}
	return executionOrder(definition, bindings)
}

func executionOrder(definition model.ProjectModel, bindings []model.ComponentBinding) ([]string, error) {
	providers := make(map[string]model.ProviderKind, len(bindings))
	for _, binding := range bindings {
		providers[binding.Service] = binding.Provider
	}
	active := make(map[string]struct{})
	for _, service := range definition.Services {
		if providers[service.Name] == model.ProviderLocal {
			active[service.Name] = struct{}{}
		}
	}
	changed := true
	for changed {
		changed = false
		for _, connection := range definition.Connections {
			if _, ok := active[connection.Source]; !ok {
				continue
			}
			provider := providers[connection.Target]
			if provider == model.ProviderRemote || provider == model.ProviderMock {
				continue
			}
			if _, ok := active[connection.Target]; !ok {
				active[connection.Target] = struct{}{}
				changed = true
			}
		}
	}
	services := make(map[string]struct{}, len(definition.Services))
	indegree := make(map[string]int, len(definition.Services))
	dependents := make(map[string][]string)
	for _, service := range definition.Services {
		if _, ok := active[service.Name]; !ok {
			continue
		}
		if providers[service.Name] == model.ProviderRemote || providers[service.Name] == model.ProviderMock {
			continue
		}
		services[service.Name] = struct{}{}
		indegree[service.Name] = 0
	}
	for _, connection := range definition.Connections {
		if connection.Source == "external" {
			continue
		}
		if _, ok := services[connection.Source]; !ok {
			continue
		}
		if _, ok := services[connection.Target]; !ok {
			continue
		}
		// Source depends on target, so target is emitted first.
		indegree[connection.Source]++
		dependents[connection.Target] = append(dependents[connection.Target], connection.Source)
	}
	var ready []string
	for service, degree := range indegree {
		if degree == 0 {
			ready = append(ready, service)
		}
	}
	sort.Strings(ready)
	var result []string
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		result = append(result, current)
		for _, dependent := range dependents[current] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.Strings(ready)
			}
		}
	}
	if len(result) != len(services) {
		return nil, fmt.Errorf("service dependency graph contains a cycle")
	}
	return result, nil
}

func serviceDefinition(definition model.ProjectModel, name string) (model.ServiceDefinition, bool) {
	for _, service := range definition.Services {
		if service.Name == name {
			return service, true
		}
	}
	return model.ServiceDefinition{}, false
}

func reverse(values []string) []string {
	result := append([]string{}, values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}
