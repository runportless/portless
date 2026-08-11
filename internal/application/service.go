package application

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	pathmatch "path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery"
	"github.com/portless-run/portless/internal/proxy"
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

func (e RuntimeInUseError) Error() string {
	return "stop project " + e.Project + " before changing the container runtime"
}

func (e NameConflictError) Error() string {
	return "project name " + e.Name + " is already used by another path"
}

type Config struct {
	DataDirectory   string
	InstallationKey string
}

type Service struct {
	store                *store.Store
	broker               *events.Broker
	processes            *processruntime.Manager
	containers           *container.Manager
	proxy                *proxy.Manager
	dataDirectory        string
	installationKey      string
	mu                   sync.RWMutex
	projectLocks         map[string]*sync.Mutex
	containerEnvironment map[string]map[string]string
}

func New(controlStore *store.Store, broker *events.Broker, config Config) *Service {
	service := &Service{
		store: controlStore, broker: broker, dataDirectory: config.DataDirectory,
		installationKey: config.InstallationKey,
		projectLocks:    make(map[string]*sync.Mutex), containerEnvironment: make(map[string]map[string]string),
	}
	service.proxy = proxy.NewManager(controlStore, broker)
	temporaryRoot := filepath.Join(config.DataDirectory, "tmp")
	service.containers = container.NewManager(
		filepath.Join(config.DataDirectory, "runtime.json"),
		podman.New(config.InstallationKey, temporaryRoot),
		docker.New(config.InstallationKey, temporaryRoot),
	)
	service.processes = processruntime.NewManager(service.processExited)
	return service
}

func (s *Service) Close(ctx context.Context) { s.proxy.Close(ctx) }

func (s *Service) Discover(ctx context.Context, path, requestedName string) (model.Project, []string, error) {
	result, err := discovery.Discover(path)
	if err != nil {
		return model.Project{}, nil, err
	}
	if known, err := s.store.ProjectByPath(ctx, result.Root); err == nil {
		return s.decorateProject(known), result.Warnings, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.Project{}, nil, err
	}
	name := requestedName
	if name == "" {
		name = result.Model.SuggestedName
	}
	name = model.NormalizeDNSName(name)
	if err := model.ValidateProjectName(name); err != nil {
		return model.Project{}, nil, err
	}
	result.Model.SuggestedName = name
	project, err := s.store.CreateProject(ctx, name, result.Root, result.Model)
	if errors.Is(err, store.ErrNameTaken) {
		return model.Project{}, result.Warnings, NameConflictError{Name: name, Suggestions: projectNameSuggestions(name, result.Root)}
	}
	if err != nil {
		return model.Project{}, result.Warnings, err
	}
	_, _ = s.timeline(ctx, name, "CLI", "project.discovered", name, "info", "Discovered project", map[string]any{"path": result.Root})
	return s.decorateProject(project), result.Warnings, nil
}

func (s *Service) Rescan(ctx context.Context, projectName string) (model.Project, []string, error) {
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		return model.Project{}, nil, err
	}
	if project.Status != model.ProjectStopped {
		return model.Project{}, nil, errors.New("project must be stopped before rescan")
	}
	result, err := discovery.Discover(project.Path)
	if err != nil {
		return model.Project{}, nil, err
	}
	result.Model.SuggestedName = project.Name
	updated, err := s.store.UpdateProjectModel(ctx, projectName, project.Revision, result.Model)
	if err != nil {
		return model.Project{}, nil, err
	}
	_, _ = s.timeline(ctx, projectName, "CLI", "project.rescanned", projectName, "info", "Project model refreshed", nil)
	return s.decorateProject(updated), result.Warnings, nil
}

func (s *Service) Rename(ctx context.Context, projectName, newName string, revision int64, actor string) (model.Project, error) {
	newName = model.NormalizeDNSName(newName)
	if err := model.ValidateProjectName(newName); err != nil {
		return model.Project{}, err
	}
	project, err := s.store.RenameProject(ctx, projectName, newName, revision)
	if err != nil {
		return model.Project{}, err
	}
	_, _ = s.timeline(ctx, newName, actor, "project.renamed", newName, "warning", "Project renamed from "+projectName+" to "+newName, map[string]any{"previousName": projectName})
	return s.decorateProject(project), nil
}

func (s *Service) Forget(ctx context.Context, projectName string) error {
	return s.store.ForgetProject(ctx, projectName)
}

func (s *Service) Projects(ctx context.Context) ([]model.Project, error) {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for index := range projects {
		projects[index] = s.decorateProject(projects[index])
	}
	return projects, nil
}

func (s *Service) Project(ctx context.Context, name string) (model.Project, error) {
	project, err := s.store.Project(ctx, name)
	if err != nil {
		return model.Project{}, err
	}
	return s.decorateProject(project), nil
}

func (s *Service) ProjectModel(ctx context.Context, name string) (model.ProjectModel, error) {
	return s.store.ProjectModel(ctx, name)
}

func (s *Service) Up(ctx context.Context, projectName, actor, idempotencyKey string) (model.Operation, error) {
	if _, err := s.store.Project(ctx, projectName); err != nil {
		return model.Operation{}, err
	}
	operation, err := s.store.CreateOperation(ctx, projectName, "up", actor, idempotencyKey)
	if err != nil {
		return model.Operation{}, err
	}
	if operation.State != "running" || len(operation.Events) > 0 {
		return operation, nil
	}
	go s.runUp(projectName, operation)
	return operation, nil
}

func (s *Service) Down(ctx context.Context, projectName, actor, idempotencyKey string, removeVolumes bool) (model.Operation, error) {
	if _, err := s.store.Project(ctx, projectName); err != nil {
		return model.Operation{}, err
	}
	operation, err := s.store.CreateOperation(ctx, projectName, "down", actor, idempotencyKey)
	if err != nil {
		return model.Operation{}, err
	}
	if operation.State != "running" || len(operation.Events) > 0 {
		return operation, nil
	}
	go s.runDown(projectName, operation, removeVolumes)
	return operation, nil
}

func (s *Service) Operation(ctx context.Context, projectName string, number int64) (model.Operation, error) {
	return s.store.Operation(ctx, projectName, number)
}

func (s *Service) Operations(ctx context.Context, projectName string, limit int) ([]model.Operation, error) {
	return s.store.Operations(ctx, projectName, limit)
}

func (s *Service) RestartService(ctx context.Context, projectName, serviceName, actor string) (model.Operation, error) {
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		return model.Operation{}, err
	}
	definition, ok := serviceDefinitionForProject(project, serviceName)
	if !ok {
		return model.Operation{}, store.ErrNotFound
	}
	if definition.Kind != model.ServiceProcess {
		operation, err := s.store.CreateOperation(ctx, projectName, "restart", actor, "")
		if err != nil {
			return model.Operation{}, err
		}
		go func() {
			lock := s.projectLock(projectName)
			lock.Lock()
			defer lock.Unlock()
			privateKey, keyErr := s.store.PrivateProjectKey(context.Background(), projectName)
			if keyErr == nil {
				keyErr = s.containers.StopService(context.Background(), privateKey, serviceName)
			}
			if keyErr == nil {
				keyErr = s.startContainer(context.Background(), project, definition)
			}
			if keyErr != nil {
				s.failOperation(projectName, operation, keyErr)
				return
			}
			s.reconcileProjectStatus(context.Background(), projectName)
			s.completeOperation(projectName, operation, "Service "+serviceName+" restarted")
		}()
		return operation, nil
	}
	operation, err := s.store.CreateOperation(ctx, projectName, "restart", actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	go func() {
		lock := s.projectLock(projectName)
		lock.Lock()
		defer lock.Unlock()
		_ = s.processes.Stop(context.Background(), projectName, serviceName, 10*time.Second)
		if err := s.startProcess(context.Background(), project, definition, operation); err != nil {
			s.failOperation(projectName, operation, err)
			return
		}
		s.reconcileProjectStatus(context.Background(), projectName)
		s.completeOperation(projectName, operation, "Service "+serviceName+" restarted")
	}()
	return operation, nil
}

func (s *Service) StopService(ctx context.Context, projectName, serviceName, actor string) (model.Operation, error) {
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		return model.Operation{}, err
	}
	definition, ok := serviceDefinitionForProject(project, serviceName)
	if !ok {
		return model.Operation{}, store.ErrNotFound
	}
	if definition.Kind != model.ServiceProcess {
		operation, err := s.store.CreateOperation(ctx, projectName, "stop-service", actor, "")
		if err != nil {
			return model.Operation{}, err
		}
		go func() {
			lock := s.projectLock(projectName)
			lock.Lock()
			defer lock.Unlock()
			privateKey, stopErr := s.store.PrivateProjectKey(context.Background(), projectName)
			if stopErr == nil {
				stopErr = s.containers.StopService(context.Background(), privateKey, serviceName)
			}
			if stopErr != nil {
				s.failOperation(projectName, operation, stopErr)
				return
			}
			runtime := runtimeFor(project, serviceName)
			_ = s.store.SetServiceRuntime(context.Background(), projectName, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: runtime.Generation})
			s.proxy.RemoveTarget(projectName, serviceName)
			s.reconcileProjectStatus(context.Background(), projectName)
			s.completeOperation(projectName, operation, "Service "+serviceName+" stopped")
		}()
		return operation, nil
	}
	operation, err := s.store.CreateOperation(ctx, projectName, "stop-service", actor, "")
	if err != nil {
		return model.Operation{}, err
	}
	go func() {
		lock := s.projectLock(projectName)
		lock.Lock()
		defer lock.Unlock()
		_ = s.serviceEvent(projectName, operation, serviceName, "stopping", "Stopping service")
		if err := s.processes.Stop(context.Background(), projectName, serviceName, 10*time.Second); err != nil {
			s.failOperation(projectName, operation, err)
			return
		}
		s.proxy.RemoveTarget(projectName, serviceName)
		runtime := runtimeFor(project, serviceName)
		_ = s.store.SetServiceRuntime(context.Background(), projectName, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: runtime.Generation, RestartCount: runtime.RestartCount})
		s.reconcileProjectStatus(context.Background(), projectName)
		s.completeOperation(projectName, operation, "Service "+serviceName+" stopped")
	}()
	return operation, nil
}

func (s *Service) RuntimeStatus(ctx context.Context) container.Status {
	return s.containers.Status(ctx)
}

func (s *Service) StartRuntime(ctx context.Context) container.Status {
	return s.containers.StartHost(ctx)
}

func (s *Service) UseRuntime(ctx context.Context, value string) (container.Status, error) {
	preference, err := container.ParseRuntimeName(value)
	if err != nil {
		return container.Status{}, err
	}
	current := s.containers.Status(ctx)
	if preference != current.Selected {
		projects, err := s.store.ListProjects(ctx)
		if err != nil {
			return container.Status{}, err
		}
		for _, project := range projects {
			for _, service := range project.Services {
				if service.Kind != model.ServiceContainer {
					continue
				}
				switch service.Status {
				case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
					return container.Status{}, RuntimeInUseError{Project: project.Name}
				}
			}
		}
	}
	if err := s.containers.SetPreference(preference); err != nil {
		return container.Status{}, err
	}
	return s.containers.Status(ctx), nil
}

func (s *Service) Traffic(project string, limit int) []model.TrafficEvent {
	return s.broker.RecentTraffic(project, limit)
}

func (s *Service) RecordedTraffic(ctx context.Context, project, recording string, limit int) ([]model.TrafficEvent, error) {
	return s.store.RecordedTraffic(ctx, project, recording, limit)
}

func (s *Service) Timeline(ctx context.Context, project string, limit int) ([]model.TimelineEvent, error) {
	return s.store.Timeline(ctx, project, limit)
}

func (s *Service) StartRecording(ctx context.Context, recording model.Recording, actor string) (model.Recording, error) {
	if err := model.ValidateArtifactName(recording.Name); err != nil {
		return model.Recording{}, err
	}
	if recording.Project == "" {
		return model.Recording{}, errors.New("project is required")
	}
	if recording.CaptureBodies {
		return model.Recording{}, errors.New("request and response body capture is not enabled in this build; start a metadata-only recording")
	}
	definition, err := s.store.ProjectModel(ctx, recording.Project)
	if err != nil {
		return model.Recording{}, err
	}
	if err := validateExperimentScope(definition, recording.Source, recording.Target, true); err != nil {
		return model.Recording{}, err
	}
	if recording.MaxEvents < 0 || recording.MaxEvents > 100_000 {
		return model.Recording{}, errors.New("maxEvents must be between 1 and 100000")
	}
	if recording.MaxBodyBytes < 0 || recording.MaxBodyBytes > 1<<20 {
		return model.Recording{}, errors.New("maxBodyBytes must not exceed 1048576")
	}
	if recording.ExpiresAt == nil {
		expires := time.Now().UTC().Add(15 * time.Minute)
		recording.ExpiresAt = &expires
	}
	if !recording.ExpiresAt.After(time.Now()) || recording.ExpiresAt.After(time.Now().Add(time.Hour)) {
		return model.Recording{}, errors.New("recording expiry must be in the future and no more than one hour away")
	}
	created, err := s.store.CreateRecording(ctx, recording)
	if err != nil {
		return model.Recording{}, err
	}
	_, _ = s.timeline(ctx, recording.Project, actor, "recording.started", recording.Name, "info", "Recording "+recording.Name+" started", nil)
	s.broker.Publish(events.Event{Type: "recording.state", Project: recording.Project, Data: created})
	return created, nil
}

func (s *Service) StopRecording(ctx context.Context, project, name, actor string) error {
	if err := s.store.StopRecording(ctx, project, name, "stopped"); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, project, actor, "recording.stopped", name, "info", "Recording "+name+" stopped", nil)
	s.broker.Publish(events.Event{Type: "recording.state", Project: project, Data: map[string]any{"name": name, "status": "completed"}})
	return nil
}

func (s *Service) Recordings(ctx context.Context, project string) ([]model.Recording, error) {
	return s.store.Recordings(ctx, project)
}

func (s *Service) DeleteRecording(ctx context.Context, project, name, actor string) error {
	if err := s.store.DeleteRecording(ctx, project, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, project, actor, "recording.deleted", name, "warning", "Recording "+name+" deleted", nil)
	return nil
}

func (s *Service) CreateFault(ctx context.Context, fault model.FaultRule, actor string) (model.FaultRule, error) {
	if err := model.ValidateArtifactName(fault.Name); err != nil {
		return model.FaultRule{}, err
	}
	if fault.Probability == 0 {
		fault.Probability = 1
	}
	if fault.Probability < 0 || fault.Probability > 1 {
		return model.FaultRule{}, errors.New("probability must be between 0 and 1")
	}
	definition, err := s.store.ProjectModel(ctx, fault.Project)
	if err != nil {
		return model.FaultRule{}, err
	}
	if err := validateExperimentScope(definition, fault.Source, fault.Target, false); err != nil {
		return model.FaultRule{}, err
	}
	if fault.LatencyMS < 0 || fault.JitterMS < 0 || fault.LatencyMS+fault.JitterMS > 60_000 {
		return model.FaultRule{}, errors.New("latency plus jitter must be between 0 and 60000 milliseconds")
	}
	if fault.StatusCode != 0 && (fault.StatusCode < 400 || fault.StatusCode > 599) {
		return model.FaultRule{}, errors.New("synthetic status must be between 400 and 599")
	}
	if fault.LatencyMS == 0 && fault.JitterMS == 0 && fault.StatusCode == 0 && !fault.Abort {
		return model.FaultRule{}, errors.New("fault must define latency, jitter, a synthetic status, or an abort")
	}
	if strings.ContainsAny(fault.Method, " \t\r\n") {
		return model.FaultRule{}, errors.New("HTTP method filter must be a single token")
	}
	fault.Method = strings.ToUpper(fault.Method)
	if fault.Path != "" {
		if _, err := pathmatch.Match(fault.Path, "/validation"); err != nil {
			return model.FaultRule{}, fmt.Errorf("invalid path glob: %w", err)
		}
	}
	if fault.ExpiresAt == nil {
		expires := time.Now().UTC().Add(10 * time.Minute)
		fault.ExpiresAt = &expires
	}
	if !fault.ExpiresAt.After(time.Now()) || fault.ExpiresAt.After(time.Now().Add(time.Hour)) {
		return model.FaultRule{}, errors.New("fault expiry must be in the future and no more than one hour away")
	}
	created, err := s.store.CreateFault(ctx, fault)
	if err != nil {
		return model.FaultRule{}, err
	}
	_, _ = s.timeline(ctx, fault.Project, actor, "fault.enabled", fault.Name, "warning", created.ScopeSummary, nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: fault.Project, Data: created})
	return created, nil
}

func (s *Service) Faults(ctx context.Context, project string) ([]model.FaultRule, error) {
	return s.store.Faults(ctx, project, false)
}

func (s *Service) DisableFault(ctx context.Context, project, name, actor string) error {
	if err := s.store.DisableFault(ctx, project, name); err != nil {
		return err
	}
	_, _ = s.timeline(ctx, project, actor, "fault.disabled", name, "info", "Fault "+name+" disabled", nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Data: map[string]any{"name": name, "enabled": false}})
	return nil
}

func (s *Service) DisableAllFaults(ctx context.Context, project, actor string) (int64, error) {
	count, err := s.store.DisableAllFaults(ctx, project)
	if err != nil {
		return 0, err
	}
	_, _ = s.timeline(ctx, project, actor, "faults.disabled_all", project, "info", fmt.Sprintf("Disabled %d active faults", count), nil)
	s.broker.Publish(events.Event{Type: "fault.state", Project: project, Data: map[string]any{"enabled": false, "count": count}})
	return count, nil
}

func (s *Service) Logs(ctx context.Context, project, service string, limit int) ([]string, error) {
	path, err := s.store.ServiceLogPath(ctx, project, service)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return []string{}, nil
	}
	var lines []string
	for _, name := range []string{"stdout.log", "stderr.log"} {
		file, err := os.Open(filepath.Join(path, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		buffer := make([]byte, 64*1024)
		scanner.Buffer(buffer, 1024*1024)
		for scanner.Scan() {
			lines = append(lines, strings.TrimRight(scanner.Text(), "\r\n"))
		}
		file.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	if limit <= 0 {
		limit = 500
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, nil
}

func (s *Service) ExportProject(ctx context.Context, project string) ([]byte, error) {
	definition, err := s.store.ProjectModel(ctx, project)
	if err != nil {
		return nil, err
	}
	definition.SuggestedName = project
	return json.MarshalIndent(struct {
		SchemaVersion int `json:"schemaVersion"`
		model.ProjectModel
	}{SchemaVersion: 1, ProjectModel: definition}, "", "  ")
}

func (s *Service) Proxy() *proxy.Manager { return s.proxy }

func (s *Service) Broker() *events.Broker { return s.broker }

func (s *Service) runUp(projectName string, operation model.Operation) {
	lock := s.projectLock(projectName)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		s.failOperation(projectName, operation, err)
		return
	}
	if project.Status == model.ProjectHealthy {
		s.completeOperation(projectName, operation, "Project is already healthy")
		return
	}
	_ = s.store.SetProjectStatus(ctx, projectName, model.ProjectStarting, "services are starting")
	s.broker.Publish(events.Event{Type: "project.state", Project: projectName, Data: map[string]any{"status": model.ProjectStarting}})
	definition, err := s.store.ProjectModel(ctx, projectName)
	if err != nil {
		s.failOperation(projectName, operation, err)
		return
	}
	order, err := startOrder(definition)
	if err != nil {
		s.failOperation(projectName, operation, err)
		return
	}
	for _, serviceName := range order {
		service, _ := serviceDefinition(definition, serviceName)
		_ = s.serviceEvent(projectName, operation, serviceName, "starting", "Starting "+serviceName)
		if service.Kind == model.ServiceContainer {
			err = s.startContainer(ctx, project, service)
		} else {
			err = s.startProcess(ctx, project, service, operation)
		}
		if err != nil {
			runtime := runtimeFor(project, serviceName)
			_ = s.store.SetServiceRuntime(context.Background(), projectName, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceFailed, Reason: err.Error(), Generation: runtime.Generation})
			s.failOperation(projectName, operation, fmt.Errorf("%s: %w", serviceName, err))
			return
		}
		_ = s.serviceEvent(projectName, operation, serviceName, "ready", serviceName+" is ready")
		project, _ = s.store.Project(ctx, projectName)
	}
	_ = s.store.SetProjectStatus(ctx, projectName, model.ProjectHealthy, "")
	_, _ = s.timeline(ctx, projectName, operation.Actor, "project.healthy", projectName, "info", "All required services are ready", nil)
	s.completeOperation(projectName, operation, "Project is healthy")
	snapshot, _ := s.Project(ctx, projectName)
	s.broker.Publish(events.Event{Type: "project.state", Project: projectName, Data: snapshot})
}

func (s *Service) runDown(projectName string, operation model.Operation, removeVolumes bool) {
	lock := s.projectLock(projectName)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		s.failOperation(projectName, operation, err)
		return
	}
	definition, err := s.store.ProjectModel(ctx, projectName)
	if err != nil {
		s.failOperation(projectName, operation, err)
		return
	}
	order, err := startOrder(definition)
	if err != nil {
		s.failOperation(projectName, operation, err)
		return
	}
	_ = s.store.SetProjectStatus(ctx, projectName, model.ProjectStopping, "services are stopping")
	for _, serviceName := range reverse(order) {
		service, _ := serviceDefinition(definition, serviceName)
		if service.Kind != model.ServiceProcess {
			continue
		}
		_ = s.serviceEvent(projectName, operation, serviceName, "stopping", "Stopping "+serviceName)
		if err := s.processes.Stop(ctx, projectName, serviceName, 10*time.Second); err != nil {
			s.failOperation(projectName, operation, err)
			return
		}
		runtime := runtimeFor(project, serviceName)
		_ = s.store.SetServiceRuntime(ctx, projectName, serviceName, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: runtime.Generation, RestartCount: runtime.RestartCount})
		s.proxy.RemoveTarget(projectName, serviceName)
	}
	privateKey, err := s.store.PrivateProjectKey(ctx, projectName)
	containerDefined, containerMayBeRunning := false, false
	for _, service := range project.Services {
		if service.Kind != model.ServiceContainer {
			continue
		}
		containerDefined = true
		switch service.Status {
		case model.ServiceReady, model.ServiceStarting, model.ServiceUnhealthy, model.ServiceStopping, model.ServiceUnknown:
			containerMayBeRunning = true
		}
	}
	if err == nil && containerDefined {
		probe := s.containers.Status(ctx)
		if probe.State != "ready" && containerMayBeRunning {
			s.failOperation(projectName, operation, fmt.Errorf("container runtime is unavailable while managed containers may still be running: %s", probe.Reason))
			return
		}
		if probe.State == "ready" {
			if err := s.containers.StopProject(ctx, privateKey, removeVolumes); err != nil {
				s.failOperation(projectName, operation, err)
				return
			}
		}
	}
	for _, service := range project.Services {
		if service.Kind == model.ServiceContainer {
			_ = s.store.SetServiceRuntime(ctx, projectName, service.Name, store.ServiceRuntimeUpdate{Status: model.ServiceStopped, Generation: service.Generation})
		}
	}
	s.proxy.CloseProject(ctx, projectName)
	_, _ = s.store.DisableAllFaults(ctx, projectName)
	for _, recording := range mustRecordings(s.store.Recordings(ctx, projectName)) {
		if recording.Status == "active" {
			_ = s.store.StopRecording(ctx, projectName, recording.Name, "stopped")
		}
	}
	_ = s.store.SetProjectStatus(ctx, projectName, model.ProjectStopped, "")
	_, _ = s.timeline(ctx, projectName, operation.Actor, "project.stopped", projectName, "info", "Project stopped", map[string]any{"volumesRemoved": removeVolumes})
	s.completeOperation(projectName, operation, "Project stopped")
	snapshot, _ := s.Project(ctx, projectName)
	s.broker.Publish(events.Event{Type: "project.state", Project: projectName, Data: snapshot})
}

func (s *Service) startContainer(ctx context.Context, project model.Project, definition model.ServiceDefinition) error {
	privateKey, err := s.store.PrivateProjectKey(ctx, project.Name)
	if err != nil {
		return err
	}
	runtime := runtimeFor(project, definition.Name)
	result, err := s.containers.Start(ctx, project.Name, privateKey, definition)
	if err != nil {
		return err
	}
	s.proxy.SetTarget(project.Name, definition.Name, result.Port)
	s.mu.Lock()
	s.containerEnvironment[targetEnvironmentKey(project.Name, definition.Name)] = result.Environment
	s.mu.Unlock()
	return s.store.SetServiceRuntime(ctx, project.Name, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: runtime.Generation + 1, UpstreamPort: result.Port, StartedAt: &result.StartedAt,
	})
}

func (s *Service) startProcess(ctx context.Context, project model.Project, definition model.ServiceDefinition, operation model.Operation) error {
	environment := make(map[string]string)
	modelDefinition, err := s.store.ProjectModel(ctx, project.Name)
	if err != nil {
		return err
	}
	for _, connection := range modelDefinition.Connections {
		if connection.Source != definition.Name {
			continue
		}
		port, err := s.proxy.EnsureEdge(ctx, project.Name, connection)
		if err != nil {
			return err
		}
		applyBinding(environment, connection, port, s.containerEnvironmentFor(project.Name, connection.Target))
	}
	runtime := runtimeFor(project, definition.Name)
	privateKey, err := s.store.PrivateProjectKey(ctx, project.Name)
	if err != nil {
		return err
	}
	logsRoot := filepath.Join(s.dataDirectory, "projects", privateKey, "logs")
	result, err := s.processes.Start(ctx, project.Name, definition, runtime.Generation+1, environment, logsRoot)
	if err != nil {
		return err
	}
	s.proxy.SetTarget(project.Name, definition.Name, result.Port)
	return s.store.SetServiceRuntime(ctx, project.Name, definition.Name, store.ServiceRuntimeUpdate{
		Status: model.ServiceReady, Generation: result.Generation, PID: result.PID, UpstreamPort: result.Port,
		StartedAt: &result.StartedAt, RestartCount: runtime.RestartCount, LogPath: result.LogDirectory, PrivateRunKey: result.PrivateRunKey,
	})
}

func (s *Service) processExited(event processruntime.ExitEvent) {
	if event.Expected {
		return
	}
	ctx := context.Background()
	project, err := s.store.Project(ctx, event.Project)
	if err != nil {
		return
	}
	runtime := runtimeFor(project, event.Service)
	if runtime.Generation != event.Generation {
		return
	}
	status := model.ServiceExited
	reason := "process exited"
	if event.Error != nil {
		status = model.ServiceFailed
		reason = event.Error.Error()
	}
	_ = s.store.SetServiceRuntime(ctx, event.Project, event.Service, store.ServiceRuntimeUpdate{Status: status, Reason: reason, Generation: event.Generation, RestartCount: runtime.RestartCount, LogPath: mustLogPath(s.store.ServiceLogPath(ctx, event.Project, event.Service))})
	s.proxy.RemoveTarget(event.Project, event.Service)
	s.reconcileProjectStatus(ctx, event.Project)
	_, _ = s.timeline(ctx, event.Project, "runtime", "service.exited", event.Service, "error", event.Service+" exited: "+reason, nil)
	s.broker.Publish(events.Event{Type: "service.state", Project: event.Project, Data: map[string]any{"service": event.Service, "state": status, "reason": reason}})
}

func (s *Service) completeOperation(project string, operation model.Operation, message string) {
	ctx := context.Background()
	_, _ = s.store.AddOperationEvent(ctx, project, operation.Number, model.OperationEvent{Type: "operation.completed", Message: message})
	_ = s.store.CompleteOperation(ctx, project, operation.Number, "succeeded", "")
	completed, _ := s.store.Operation(ctx, project, operation.Number)
	s.broker.Publish(events.Event{Type: "operation.state", Project: project, Data: completed})
}

func (s *Service) reconcileProjectStatus(ctx context.Context, projectName string) {
	project, err := s.store.Project(ctx, projectName)
	if err != nil {
		return
	}
	status, reason := model.DeriveProjectStatus(project.Services, "")
	_ = s.store.SetProjectStatus(ctx, projectName, status, reason)
	s.broker.Publish(events.Event{Type: "project.state", Project: projectName, Data: map[string]any{"status": status, "reason": reason}})
}

func (s *Service) failOperation(project string, operation model.Operation, err error) {
	ctx := context.Background()
	_, _ = s.store.AddOperationEvent(ctx, project, operation.Number, model.OperationEvent{Type: "operation.failed", Message: err.Error()})
	_ = s.store.CompleteOperation(ctx, project, operation.Number, "failed", err.Error())
	_ = s.store.SetProjectStatus(ctx, project, model.ProjectFailed, err.Error())
	_, _ = s.timeline(ctx, project, operation.Actor, "operation.failed", project, "error", err.Error(), map[string]any{"operation": operation.Number})
	failed, _ := s.store.Operation(ctx, project, operation.Number)
	s.broker.Publish(events.Event{Type: "operation.state", Project: project, Data: failed})
}

func (s *Service) serviceEvent(project string, operation model.Operation, service, state, message string) error {
	ctx := context.Background()
	_, err := s.store.AddOperationEvent(ctx, project, operation.Number, model.OperationEvent{Type: "service." + state, Subject: service, Message: message})
	s.broker.Publish(events.Event{Type: "service.state", Project: project, Data: map[string]any{"service": service, "state": state}})
	return err
}

func (s *Service) timeline(ctx context.Context, project, actor, eventType, subject, severity, summary string, details map[string]any) (model.TimelineEvent, error) {
	event, err := s.store.AddTimelineEvent(ctx, model.TimelineEvent{Project: project, Actor: actor, Type: eventType, Subject: subject, Severity: severity, Summary: summary, Details: details})
	if err == nil {
		s.broker.Publish(events.Event{Type: "timeline", Project: project, Data: event})
	}
	return event, err
}

func (s *Service) decorateProject(project model.Project) model.Project {
	project.DashboardURL = fmt.Sprintf("http://portless.localhost/projects/%s", project.Name)
	for index := range project.Services {
		if project.Services[index].Kind == model.ServiceProcess {
			project.Services[index].IngressURL = fmt.Sprintf("http://%s.%s.localhost", project.Services[index].Name, project.Name)
		}
	}
	return project
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

func applyBinding(environment map[string]string, connection model.Connection, port int, targetEnvironment map[string]string) {
	address := "127.0.0.1:" + strconv.Itoa(port)
	switch connection.Protocol {
	case model.ProtocolHTTP:
		environment[connection.Environment] = "http://" + address
	case model.ProtocolPostgres:
		user := targetEnvironment["POSTGRES_USER"]
		password := targetEnvironment["POSTGRES_PASSWORD"]
		database := targetEnvironment["POSTGRES_DB"]
		if user == "" {
			user, database = "portless", "portless"
		}
		if strings.Contains(connection.Environment, "SPRING_DATASOURCE") {
			environment[connection.Environment] = "jdbc:postgresql://" + address + "/" + database
			environment["SPRING_DATASOURCE_USERNAME"] = user
			environment["SPRING_DATASOURCE_PASSWORD"] = password
		} else {
			environment[connection.Environment] = "postgresql://" + url.QueryEscape(user) + ":" + url.QueryEscape(password) + "@" + address + "/" + database
		}
	case model.ProtocolRedis:
		environment[connection.Environment] = "redis://" + address
	}
}

func serviceDefinitionForProject(project model.Project, name string) (model.ServiceDefinition, bool) {
	for _, service := range project.Services {
		if service.Name == name {
			return service.ServiceDefinition, true
		}
	}
	return model.ServiceDefinition{}, false
}

func runtimeFor(project model.Project, name string) model.Service {
	for _, service := range project.Services {
		if service.Name == name {
			return service
		}
	}
	return model.Service{ServiceDefinition: model.ServiceDefinition{Name: name}}
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
	if source != "" && source != "external" && target != "" {
		for _, connection := range definition.Connections {
			if connection.Source == source && connection.Target == target {
				return nil
			}
		}
		return fmt.Errorf("connection %s:%s is not present in the accepted project model", source, target)
	}
	return nil
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
