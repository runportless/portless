// Package discovery provides the application-facing discovery service and the
// default registry of built-in framework plugins.
package discovery

import (
	"context"
	"sync"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery/builtin"
	"github.com/portless-run/portless/internal/project/discovery/engine"
	"github.com/portless-run/portless/internal/project/discovery/spec"
	resourcebuiltin "github.com/portless-run/portless/internal/resource/builtin"
)

type Result = engine.Result
type Config = engine.Config
type Limits = engine.Limits
type Diagnostic = spec.Diagnostic

type Discoverer interface {
	FindRoot(ctx context.Context, start string) (string, error)
	Discover(ctx context.Context, start string) (Result, error)
}

func New(config Config, detectors []spec.ServiceDetector, analyzers []spec.TopologyAnalyzer) (*engine.Engine, error) {
	return engine.New(config, detectors, analyzers)
}

func NewDefault(config Config) (*engine.Engine, error) {
	if config.Resources == nil {
		config.Resources = resourcebuiltin.Registry()
	}
	return engine.New(config, builtin.Detectors(), builtin.Analyzers())
}

func DefaultLimits() Limits {
	return engine.DefaultLimits()
}

var (
	defaultOnce       sync.Once
	defaultDiscoverer Discoverer
)

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

func FindRoot(ctx context.Context, start string) (string, error) {
	return Default().FindRoot(ctx, start)
}

func Discover(ctx context.Context, start string) (Result, error) {
	return Default().Discover(ctx, start)
}

func Validate(definition model.ProjectModel) error {
	return engine.Validate(definition, resourcebuiltin.Registry())
}
