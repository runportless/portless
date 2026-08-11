package application

import (
	"fmt"
	"sort"

	"github.com/portless-run/portless/internal/model"
)

func startOrder(definition model.ProjectModel) ([]string, error) {
	services := make(map[string]struct{}, len(definition.Services))
	indegree := make(map[string]int, len(definition.Services))
	dependents := make(map[string][]string)
	for _, service := range definition.Services {
		services[service.Name] = struct{}{}
		indegree[service.Name] = 0
	}
	for _, connection := range definition.Connections {
		if connection.Source == "external" {
			continue
		}
		if _, ok := services[connection.Source]; !ok {
			return nil, fmt.Errorf("unknown source service %s", connection.Source)
		}
		if _, ok := services[connection.Target]; !ok {
			return nil, fmt.Errorf("unknown target service %s", connection.Target)
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
