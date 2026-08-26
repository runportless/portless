package controlplane

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/mocks"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery"
	"github.com/runportless/portless/portless-daemon/providers"
	resourcebuiltin "github.com/runportless/portless/portless-daemon/providers/builtin"
	"github.com/runportless/portless/portless-daemon/runtime/container"
	"github.com/runportless/portless/portless-daemon/runtime/container/docker"
	"github.com/runportless/portless/portless-daemon/runtime/container/podman"
	processruntime "github.com/runportless/portless/portless-daemon/runtime/process"
	"github.com/runportless/portless/portless-daemon/traffic"
	"github.com/runportless/portless/portless-daemon/traffic/proxy"
)

// NameConflictError reports a project-name collision and safe alternative names.
type NameConflictError struct {
	Name        string   `json:"name"`
	Suggestions []string `json:"suggestions"`
}

// RuntimeInUseError identifies the active environment blocking a runtime change.
type RuntimeInUseError struct {
	Project string `json:"project"`
}

// CheckoutInUseError identifies services whose checkout provider prevents an
// environment checkout from being removed.
type CheckoutInUseError struct {
	Source   string   `json:"source"`
	Services []string `json:"services"`
}

// ResetActiveEnvironmentsError lists environments that require an explicit forced reset.
type ResetActiveEnvironmentsError struct {
	Environments []string `json:"environments"`
}

// ResetRuntimeResult summarizes process and container cleanup performed before reset.
type ResetRuntimeResult struct {
	Processes int                  `json:"processes"`
	Runtimes  []RuntimeResetResult `json:"runtimes"`
}

// ResetPlan summarizes application state and managed resources that reset would remove.
type ResetPlan struct {
	Projects                  int      `json:"projects"`
	Environments              int      `json:"environments"`
	ManagedVolumeEnvironments int      `json:"managedVolumeEnvironments"`
	ActiveEnvironments        []string `json:"activeEnvironments"`
	TopologyIncompatible      bool     `json:"topologyIncompatible"`
}

// EnvironmentContext explains how a checkout path resolves to an environment.
type EnvironmentContext struct {
	Resolution  string              `json:"resolution"`
	Environment *model.Environment  `json:"environment,omitempty"`
	Candidates  []model.Environment `json:"candidates"`
}

// UpOptions controls managed and debugger-aware environment startup.
type UpOptions struct {
	DebugServices []string `json:"debugServices,omitempty"`
	Managed       bool     `json:"managed,omitempty"`
}

// Error describes why the selected container runtime cannot be changed.
func (e RuntimeInUseError) Error() string {
	return "stop project " + e.Project + " before changing the container runtime"
}

// Error describes the services that must switch providers before checkout removal.
func (e CheckoutInUseError) Error() string {
	return "source checkout " + e.Source + " is used by checkout providers for: " + strings.Join(e.Services, ", ")
}

// Error describes the active environments preventing an unforced reset.
func (e ResetActiveEnvironmentsError) Error() string {
	return "all environments must be stopped before Portless application state is reset: " + strings.Join(e.Environments, ", ")
}

// Error describes the conflicting project name.
func (e NameConflictError) Error() string {
	return "project name " + e.Name + " is already used by another path"
}

// Config supplies runtime ownership, storage, discovery, and provider dependencies.
type Config struct {
	DataDirectory     string
	InstallationKey   string
	DaemonInstanceID  string
	Executable        string
	PrivateTCPIngress bool
	Discoverer        discovery.Discoverer
	Resources         *providers.Registry
}

// Service coordinates project state, runtimes, proxies, and observability operations.
type Service struct {
	database             *database.Store
	broker               *events.Broker
	traffic              *traffic.Store
	processes            *processruntime.Manager
	containers           *container.Manager
	proxy                *proxy.Manager
	mocks                *mocks.Manager
	dataDirectory        string
	installationKey      string
	daemonInstanceID     string
	privateTCPIngress    bool
	mu                   sync.RWMutex
	recoveryMu           sync.RWMutex
	recovery             ReconciliationStatus
	resetGate            sync.RWMutex
	resetting            bool
	projectLocks         map[string]*sync.Mutex
	containerEnvironment map[string]map[string]string
	sourceLeases         map[string]string
	discoverer           discovery.Discoverer
	resources            *providers.Registry
}

// New constructs a control-plane service from persistent state and runtime dependencies.
func New(controlStore *database.Store, broker *events.Broker, config Config) *Service {
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
	trafficStore := traffic.NewStore(broker)
	service := &Service{
		database: controlStore, broker: broker, dataDirectory: config.DataDirectory,
		traffic:         trafficStore,
		installationKey: config.InstallationKey, daemonInstanceID: config.DaemonInstanceID, privateTCPIngress: config.PrivateTCPIngress,
		projectLocks: make(map[string]*sync.Mutex), containerEnvironment: make(map[string]map[string]string), sourceLeases: make(map[string]string),
		discoverer: discoverer, resources: resources,
	}
	service.proxy = proxy.NewManager(controlStore, trafficStore, broker)
	service.mocks = mocks.NewManager()
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
				trafficStore.EnsureSequence(scope, sequence)
			}
		}
	}
	return service
}

// Close stops runtime managers and closes all active proxy listeners.
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
	_ = s.mocks.Close(closeContext)
}
