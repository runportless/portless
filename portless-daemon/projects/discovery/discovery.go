// Package discovery provides the application-facing discovery service and the
// default registry of built-in framework plugins.
package discovery

import (
	"context"
	"sync"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/builtin"
	"github.com/runportless/portless/portless-daemon/projects/discovery/engine"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
	resourcebuiltin "github.com/runportless/portless/portless-daemon/providers/builtin"
)

// Result is the application-facing discovery result.
type Result = engine.Result

// Config configures discovery limits, timeout, and resource plugins.
type Config = engine.Config

// Limits bounds filesystem work performed during discovery.
type Limits = engine.Limits

// Diagnostic is a structured message emitted by a discovery plugin.
type Diagnostic = spec.Diagnostic

// Discoverer finds a project root and produces its discovered topology.
type Discoverer interface {
	// FindRoot resolves the project root containing start.
	FindRoot(ctx context.Context, start string) (string, error)
	// Discover scans the project containing start and returns its topology.
	Discover(ctx context.Context, start string) (Result, error)
}

// New constructs a discovery engine from explicit detectors and analyzers.
func New(config Config, detectors []spec.ServiceDetector, analyzers []spec.TopologyAnalyzer) (*engine.Engine, error) {
	return engine.New(config, detectors, analyzers)
}

// NewDefault constructs an engine with all built-in discovery and resource plugins.
func NewDefault(config Config) (*engine.Engine, error) {
	if config.Resources == nil {
		config.Resources = resourcebuiltin.Registry()
	}
	return engine.New(config, builtin.Detectors(), builtin.Analyzers())
}

// DefaultLimits returns the standard bounded-workspace limits.
func DefaultLimits() Limits {
	return engine.DefaultLimits()
}

var (
	defaultOnce       sync.Once
	defaultDiscoverer Discoverer
)

// Default returns the process-wide built-in discovery engine.
func Default() Discoverer {
	defaultOnce.Do(func() {
		created, err := NewDefault(Config{})
		if err != nil {
			panic("construct default discovery registry: " + err.Error())
		}
		defaultDiscoverer = created
	})
	return defaultDiscoverer
}

// FindRoot resolves a project root using the default discovery engine.
func FindRoot(ctx context.Context, start string) (string, error) {
	return Default().FindRoot(ctx, start)
}

// Discover scans a project using the default discovery engine.
func Discover(ctx context.Context, start string) (Result, error) {
	return Default().Discover(ctx, start)
}

// Validate checks a discovered model against built-in resource providers.
func Validate(definition model.ProjectModel) error {
	return engine.Validate(definition, resourcebuiltin.Registry())
}
