package application

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery"
	"github.com/portless-run/portless/internal/proxy"
	"github.com/portless-run/portless/internal/resource"
	resourcebuiltin "github.com/portless-run/portless/internal/resource/builtin"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/runtime/container/docker"
	"github.com/portless-run/portless/internal/runtime/container/podman"
	processruntime "github.com/portless-run/portless/internal/runtime/process"
	"github.com/portless-run/portless/internal/store"
)

type NameConflictError struct {
	Name        string   `json:"name"`
	Suggestions []string `json:"suggestions"`
}

type RuntimeInUseError struct {
	Project string `json:"project"`
}

type ResetActiveEnvironmentsError struct {
	Environments []string `json:"environments"`
}

type ResetRuntimeResult struct {
	Processes int                  `json:"processes"`
	Runtimes  []RuntimeResetResult `json:"runtimes"`
}

type ResetPlan struct {
	Projects                  int      `json:"projects"`
	Environments              int      `json:"environments"`
	ManagedVolumeEnvironments int      `json:"managedVolumeEnvironments"`
	ActiveEnvironments        []string `json:"activeEnvironments"`
	TopologyIncompatible      bool     `json:"topologyIncompatible"`
}

type EnvironmentContext struct {
	Resolution  string              `json:"resolution"`
	Environment *model.Environment  `json:"environment,omitempty"`
	Candidates  []model.Environment `json:"candidates"`
}

type UpOptions struct {
	DebugServices []string `json:"debugServices,omitempty"`
	Managed       bool     `json:"managed,omitempty"`
}

func (e RuntimeInUseError) Error() string {
	return "stop project " + e.Project + " before changing the container runtime"
}

func (e ResetActiveEnvironmentsError) Error() string {
	return "all environments must be stopped before Portless application state is reset: " + strings.Join(e.Environments, ", ")
}

func (e NameConflictError) Error() string {
	return "project name " + e.Name + " is already used by another path"
}

type Config struct {
	DataDirectory    string
	InstallationKey  string
	DaemonInstanceID string
	Executable       string
	Discoverer       discovery.Discoverer
	Resources        *resource.Registry
}

type Service struct {
	store                *store.Store
	broker               *events.Broker
	processes            *processruntime.Manager
	containers           *container.Manager
	proxy                *proxy.Manager
	dataDirectory        string
	installationKey      string
	daemonInstanceID     string
	mu                   sync.RWMutex
	resetGate            sync.RWMutex
	resetting            bool
	projectLocks         map[string]*sync.Mutex
	containerEnvironment map[string]map[string]string
	sourceLeases         map[string]string
	discoverer           discovery.Discoverer
	resources            *resource.Registry
}

func New(controlStore *store.Store, broker *events.Broker, config Config) *Service {
	resources := config.Resources
	if resources == nil {
		resources = resourcebuiltin.Registry()
	}
	discoverer := config.Discoverer
	if discoverer == nil {
		created, err := discovery.NewDefault(discovery.Config{Resources: resources})
		if err != nil {
			panic("construct default discovery engine: " + err.Error())
		}
		discoverer = created
	}
	service := &Service{
		store: controlStore, broker: broker, dataDirectory: config.DataDirectory,
		installationKey: config.InstallationKey, daemonInstanceID: config.DaemonInstanceID,
		projectLocks: make(map[string]*sync.Mutex), containerEnvironment: make(map[string]map[string]string), sourceLeases: make(map[string]string),
		discoverer: discoverer, resources: resources,
	}
	service.proxy = proxy.NewManager(controlStore, broker)
	temporaryRoot := filepath.Join(config.DataDirectory, "tmp")
	service.containers = container.NewManager(
		filepath.Join(config.DataDirectory, "runtime.json"),
		resources,
		podman.New(config.InstallationKey, temporaryRoot),
		docker.New(config.InstallationKey, temporaryRoot),
	)
	if config.DaemonInstanceID != "" && config.Executable != "" {
		service.processes = processruntime.NewSupervisedManager(config.Executable, filepath.Join(config.DataDirectory, "runs"), service.processExited)
	} else {
		service.processes = processruntime.NewManager(service.processExited)
	}
	if environments, err := controlStore.ListEnvironments(context.Background(), ""); err == nil {
		for _, environment := range environments {
			scope := model.EnvironmentSelector(environment.Project, environment.Name)
			if sequence, sequenceErr := controlStore.MaxRecordedTrafficSequence(context.Background(), scope); sequenceErr == nil {
				broker.EnsureTrafficSequence(scope, sequence)
			}
		}
	}
	return service
}

func (s *Service) Close(ctx context.Context) {
	s.processes.Close()
	s.containers.Close()
	closeContext := ctx
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		closeContext, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	s.proxy.Close(closeContext)
}
