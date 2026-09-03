package controlplane

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/worktrees"
)

// prepareSourceCheckouts runs with the environment lock held. Serializing
// preparation and reservation prevents simultaneous starts from both choosing
// the original checkout, without holding the general state lock during I/O.
// bindings may describe a pending provider handoff; only source paths change.
func (s *Service) prepareSourceCheckouts(ctx context.Context, environment model.Environment, bindings []model.ComponentBinding, operation model.Operation) (model.Environment, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	s.sourcePreparation.Lock()
	defer s.sourcePreparation.Unlock()
	if err := ctx.Err(); err != nil {
		return environment, err
	}
	scope := model.EnvironmentSelector(environment.Project, environment.Name)
	used := make(map[string]bool)
	for _, binding := range bindings {
		if binding.Provider == model.ProviderLocal {
			used[strings.ToLower(binding.Source)] = true
		}
	}
	type checkoutPlan struct {
		repository worktrees.Repository
		source     string
	}
	var plans []checkoutPlan
	for _, source := range environment.Sources {
		if !used[strings.ToLower(source.Name)] {
			continue
		}
		s.mu.RLock()
		owner := s.sourceLeaseOwner(scope, source.Path)
		s.mu.RUnlock()
		if owner == "" {
			continue
		}
		covered := slices.ContainsFunc(plans, func(plan checkoutPlan) bool { return pathInCheckout(plan.repository.Root, source.Path) })
		if covered {
			continue
		}
		repository, err := worktrees.Inspect(ctx, source.Path)
		if err != nil {
			return environment, fmt.Errorf("source %s is already running in %s; could not prepare an independent checkout: %w; stop %s or bind a separate checkout", source.Name, owner, err, owner)
		}
		// An existing runtime must keep its original working directory. Recovery
		// never relocates an unverified or running process to another checkout.
		for _, peer := range environment.Sources {
			if !pathInCheckout(repository.Root, peer.Path) {
				continue
			}
			for _, service := range environment.Services {
				binding := bindingForEnvironment(environment, service.Name)
				if binding.Provider == model.ProviderLocal && strings.EqualFold(binding.Source, peer.Name) &&
					(serviceRuntimeActive(service.Status) || s.processes.IsRunning(scope, service.Name)) {
					return environment, fmt.Errorf("cannot prepare an independent checkout while %s is active; stop %s and start it again", service.Name, scope)
				}
			}
		}
		plans = append(plans, checkoutPlan{repository: repository, source: source.Name})
	}
	if len(plans) > 0 {
		definition, err := s.database.EnvironmentModel(ctx, environment.Project, environment.Name)
		if err != nil {
			return environment, err
		}
		sources := slices.Clone(environment.Sources)
		var changed []model.SourceBinding
		for _, plan := range plans {
			message := "Preparing an independent checkout for " + plan.source
			if err := s.sourcePreparationEvent(ctx, scope, operation, "source.preparing", plan.source, message); err != nil {
				return environment, err
			}
			checkout, err := worktrees.Create(ctx, filepath.Join(s.dataDirectory, "worktrees"), environment.Project+"-"+environment.Name, plan.repository)
			if err != nil {
				return environment, fmt.Errorf("prepare source %s for %s: %w", plan.source, scope, err)
			}
			for index, source := range sources {
				if !pathInCheckout(plan.repository.Root, source.Path) {
					continue
				}
				source.Path = relocateCheckoutPath(source.Path, plan.repository.Root, checkout)
				source.Definition = relocateCheckoutDefinition(source.Definition, plan.repository.Root, checkout)
				sources[index] = source
				changed = append(changed, source)
			}
			definition = relocateCheckoutDefinition(definition, plan.repository.Root, checkout)
		}
		// Persist before launching anything, including after a failed earlier
		// startup. Runtime generations, proxies, provider bindings and the user's
		// explicit CLI selection survive this source-only configuration change.
		environment, err = s.database.RelocateEnvironmentSources(ctx, environment.Project, environment.Name, environment.Revision, definition, changed)
		if err != nil {
			return environment, fmt.Errorf("save independent environment checkouts: %w", err)
		}
		for _, source := range changed {
			message := "Independent checkout for " + source.Name + " is ready"
			_ = s.sourcePreparationEvent(ctx, scope, operation, "source.prepared", source.Name, message)
			_, _ = s.timeline(ctx, scope, operation.Actor, "environment.checkout_prepared", source.Name, "info", message, map[string]any{"path": source.Path})
		}
		s.publish(scope, "environment.state", s.decorateEnvironment(environment))
	}
	candidate := environment
	candidate.Bindings = bindings
	return environment, s.acquireSourceLeases(scope, candidate)
}

func (s *Service) sourcePreparationEvent(ctx context.Context, scope string, operation model.Operation, eventType, source, message string) error {
	if _, err := s.database.AddOperationEvent(ctx, scope, operation.Number, model.OperationEvent{Type: eventType, Subject: source, Message: message}); err != nil {
		return err
	}
	current, err := s.database.Operation(ctx, scope, operation.Number)
	if err == nil {
		s.publish(scope, "operation.state", current)
	}
	return err
}

// sourceLeaseOwner requires mu and recognizes overlapping source directories,
// so a nested source cannot bypass its containing checkout's reservation.
func (s *Service) sourceLeaseOwner(scope, path string) string {
	for leased, owner := range s.sourceLeases {
		if owner != scope && (pathInCheckout(leased, path) || pathInCheckout(path, leased)) {
			return owner
		}
	}
	return ""
}

func pathInCheckout(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func relocateCheckoutPath(value, root, checkout string) string {
	if !filepath.IsAbs(value) || !pathInCheckout(root, value) {
		return value
	}
	relative, _ := filepath.Rel(root, value)
	return filepath.Join(checkout, relative)
}

func relocateCheckoutCommand(command []string, root, checkout string) []string {
	result := slices.Clone(command)
	for index, value := range result {
		if flag, path, ok := strings.Cut(value, "="); ok && strings.HasPrefix(flag, "-") {
			result[index] = flag + "=" + relocateCheckoutPath(path, root, checkout)
		} else {
			result[index] = relocateCheckoutPath(value, root, checkout)
		}
	}
	return result
}

func relocateCheckoutDefinition(definition model.ProjectModel, root, checkout string) model.ProjectModel {
	definition.Services = slices.Clone(definition.Services)
	for index, service := range definition.Services {
		service.WorkingDirectory = relocateCheckoutPath(service.WorkingDirectory, root, checkout)
		service.ServiceDirectory = relocateCheckoutPath(service.ServiceDirectory, root, checkout)
		service.Command = relocateCheckoutCommand(service.Command, root, checkout)
		service.Environment = maps.Clone(service.Environment)
		for key, value := range service.Environment {
			service.Environment[key] = relocateCheckoutPath(value, root, checkout)
		}
		if service.Debug != nil {
			debug := *service.Debug
			debug.Command = relocateCheckoutCommand(debug.Command, root, checkout)
			service.Debug = &debug
		}
		definition.Services[index] = service
	}
	return definition
}
