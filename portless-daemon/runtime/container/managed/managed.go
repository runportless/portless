package managed

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/providers"
	"github.com/portless-run/portless/portless-daemon/runtime/container"
	"github.com/portless-run/portless/portless-daemon/runtime/logstore"
)

const (
	labelOwner           = "dev.portless.owner"
	labelInstall         = "dev.portless.installation.key"
	labelEnvironment     = "dev.portless.environment.key"
	labelEnvironmentName = "dev.portless.environment.name"
	labelService         = "dev.portless.service.name"
	labelGeneration      = "dev.portless.service.generation"
	labelResourceType    = "dev.portless.resource.type"
	labelResourceVersion = "dev.portless.resource.version"
	labelResourceImage   = "dev.portless.resource.image"
	labelResourceVolume  = "dev.portless.resource.volume"
)

// Engine abstracts Docker- and Podman-specific host commands used by the managed runtime.
type Engine interface {
	// Name returns the engine's canonical runtime name.
	Name() container.RuntimeName
	// Binary returns the executable used for engine commands.
	Binary() string
	// Probe reports whether the engine is ready.
	Probe(context.Context) container.ProbeResult
	// StartHost attempts to activate the engine host.
	StartHost(context.Context) container.ProbeResult
	// ResourceExists reports whether a named engine resource exists.
	ResourceExists(context.Context, string, string) bool
	// VolumeMount formats a named-volume mount argument for this engine.
	VolumeMount(string, string) string
}

// Manager implements ownership-safe managed resources for one container engine.
type Manager struct {
	engine          Engine
	installationKey string
	temporaryRoot   string
	credentialsRoot string
	mu              sync.Mutex
	collectors      map[string]*logCollector
}

type logCollector struct {
	cancel context.CancelFunc
}

// New constructs a managed-resource runtime for a concrete container engine.
func New(engine Engine, installationKey, temporaryRoot string) *Manager {
	return &Manager{engine: engine, installationKey: installationKey, temporaryRoot: temporaryRoot, credentialsRoot: filepath.Join(filepath.Dir(temporaryRoot), "secrets"), collectors: make(map[string]*logCollector)}
}

// Name returns the underlying container engine name.
func (m *Manager) Name() container.RuntimeName { return m.engine.Name() }

// Probe reports whether the underlying container engine is ready.
func (m *Manager) Probe(ctx context.Context) container.ProbeResult { return m.engine.Probe(ctx) }

// StartHost attempts to activate the underlying container engine host.
func (m *Manager) StartHost(ctx context.Context) container.ProbeResult {
	return m.engine.StartHost(ctx)
}

// Start creates or safely replaces the owned resource container for a service generation.
func (m *Manager) Start(ctx context.Context, environmentName, environmentKey string, service model.ServiceDefinition, plan providers.ContainerPlan, generation int64, logsRoot string) (container.StartResult, error) {
	if service.Kind != model.ServiceResource || service.Resource == nil {
		return container.StartResult{}, errors.New("container runtime only starts resource services")
	}
	if result := m.Probe(ctx); result.State != "ready" {
		return container.StartResult{}, errors.New(result.Reason)
	}
	suffix := environmentKey
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	networkName := resourceName("portless", environmentName, suffix)
	if err := m.ensureNetwork(ctx, networkName, environmentName, environmentKey); err != nil {
		return container.StartResult{}, err
	}
	containerName := resourceName("portless", environmentName, service.Name, suffix)
	logDirectory := filepath.Join(logsRoot, service.Name, strconv.FormatInt(generation, 10))
	if _, port, verifyErr := m.verifyAdoptableContainer(ctx, environmentKey, service, plan, generation, containerName); verifyErr == nil {
		environment, _ := m.inspectEnvironment(ctx, containerName)
		if err := m.startLogCollector(containerName, service.Name, generation, logDirectory); err != nil {
			return container.StartResult{}, err
		}
		return container.StartResult{ContainerName: containerName, Port: port, Environment: environment, StartedAt: time.Now().UTC(), LogDirectory: logDirectory}, nil
	}
	staleContainers, err := m.ownedServiceContainers(ctx, environmentKey, service.Name)
	if err != nil {
		return container.StartResult{}, err
	}
	if len(staleContainers) > 1 {
		return container.StartResult{}, fmt.Errorf("found %d managed %s containers; refusing ambiguous replacement", len(staleContainers), service.Name)
	}
	for _, staleName := range staleContainers {
		containerID, ownershipErr := m.verifiedContainerID(ctx, staleName, environmentKey, service.Name)
		if ownershipErr != nil {
			return container.StartResult{}, ownershipErr
		}
		if err := m.run(ctx, "rm", "-f", containerID); err != nil {
			return container.StartResult{}, fmt.Errorf("remove stale managed %s container: %w", service.Name, err)
		}
	}
	if m.engine.ResourceExists(ctx, "container", containerName) {
		return container.StartResult{}, fmt.Errorf("container name %s is already in use by a resource Portless does not own", containerName)
	}
	environment, envFile, err := m.resourceEnvironmentFile(environmentKey, service.Name, plan.Environment)
	if err != nil {
		return container.StartResult{}, err
	}
	args := []string{"run", "-d", "--name", containerName,
		"--network", networkName,
		"--label", labelOwner + "=true",
		"--label", labelInstall + "=" + m.installationKey,
		"--label", labelEnvironment + "=" + environmentKey,
		"--label", labelEnvironmentName + "=" + environmentName,
		"--label", labelService + "=" + service.Name,
		"--label", labelGeneration + "=" + strconv.FormatInt(generation, 10),
		"--label", labelResourceType + "=" + service.Resource.Type,
		"--label", labelResourceVersion + "=" + service.Resource.Version,
		"--label", labelResourceImage + "=" + plan.Image,
		"--env-file", envFile,
		"-p", "127.0.0.1::" + strconv.Itoa(plan.ClientPort),
	}
	for _, volume := range plan.Volumes {
		volumeName := resourceName("portless", environmentName, service.Name, volume.Key, suffix)
		if err := m.ensureVolume(ctx, volumeName, environmentName, environmentKey, service.Name, volume.Key); err != nil {
			return container.StartResult{}, err
		}
		args = append(args, "-v", m.engine.VolumeMount(volumeName, volume.Path))
	}
	args = append(args, plan.Image)
	args = append(args, plan.Command...)
	if output, err := m.output(ctx, args...); err != nil {
		return container.StartResult{}, fmt.Errorf("start %s container: %s: %w", service.Name, strings.TrimSpace(string(output)), err)
	}
	port, err := m.waitForPort(ctx, containerName, plan.ClientPort)
	if err != nil {
		_ = m.run(context.Background(), "rm", "-f", containerName)
		return container.StartResult{}, err
	}
	if err := m.waitForHealth(ctx, containerName, port, plan.Readiness); err != nil {
		_ = m.run(context.Background(), "rm", "-f", containerName)
		return container.StartResult{}, err
	}
	if err := m.startLogCollector(containerName, service.Name, generation, logDirectory); err != nil {
		_ = m.run(context.Background(), "rm", "-f", containerName)
		return container.StartResult{}, err
	}
	return container.StartResult{ContainerName: containerName, Port: port, Environment: environment, StartedAt: time.Now().UTC(), LogDirectory: logDirectory}, nil
}

// Adopt verifies an existing container and resumes health and log observation.
func (m *Manager) Adopt(ctx context.Context, environmentName, environmentKey string, service model.ServiceDefinition, plan providers.ContainerPlan, generation int64, logsRoot string) (container.StartResult, error) {
	if service.Kind != model.ServiceResource || service.Resource == nil {
		return container.StartResult{}, errors.New("only resource services can be adopted")
	}
	name, port, err := m.verifyAdoptableContainer(ctx, environmentKey, service, plan, generation, "")
	if err != nil {
		return container.StartResult{}, err
	}
	healthCtx, healthCancel := context.WithTimeout(ctx, 10*time.Second)
	err = m.waitForHealth(healthCtx, name, port, plan.Readiness)
	healthCancel()
	if err != nil {
		return container.StartResult{}, err
	}
	environment, err := m.inspectEnvironment(ctx, name)
	if err != nil {
		return container.StartResult{}, fmt.Errorf("inspect managed %s environment: %w", service.Name, err)
	}
	logDirectory := filepath.Join(logsRoot, service.Name, strconv.FormatInt(generation, 10))
	if err := m.startLogCollector(name, service.Name, generation, logDirectory); err != nil {
		return container.StartResult{}, err
	}
	return container.StartResult{
		ContainerName: name, Port: port, Environment: environment,
		StartedAt: time.Now().UTC(), LogDirectory: logDirectory,
	}, nil
}

// Verify checks an existing container's ownership, resource identity, generation, and port.
func (m *Manager) Verify(ctx context.Context, environmentKey string, service model.ServiceDefinition, plan providers.ContainerPlan, generation int64, containerName string) error {
	_, _, err := m.verifyAdoptableContainer(ctx, environmentKey, service, plan, generation, containerName)
	return err
}

func (m *Manager) verifyAdoptableContainer(ctx context.Context, environmentKey string, service model.ServiceDefinition, plan providers.ContainerPlan, generation int64, expectedName string) (string, int, error) {
	if service.Kind != model.ServiceResource || service.Resource == nil {
		return "", 0, errors.New("only resource services can be verified")
	}
	if result := m.Probe(ctx); result.State != "ready" {
		return "", 0, errors.New(result.Reason)
	}
	names, err := m.ownedServiceContainers(ctx, environmentKey, service.Name)
	if err != nil {
		return "", 0, err
	}
	if len(names) == 0 {
		return "", 0, fmt.Errorf("managed %s container is missing", service.Name)
	}
	if len(names) != 1 {
		return "", 0, fmt.Errorf("found %d managed %s containers; refusing ambiguous adoption", len(names), service.Name)
	}
	name := names[0]
	if expectedName != "" && name != expectedName {
		return "", 0, fmt.Errorf("managed container %s does not match persisted container %s", name, expectedName)
	}
	encodedGeneration, labelErr := m.inspectLabel(ctx, name, labelGeneration)
	if labelErr != nil || encodedGeneration == "" {
		return "", 0, fmt.Errorf("managed %s container has no recoverable generation label", service.Name)
	}
	actual, parseErr := strconv.ParseInt(encodedGeneration, 10, 64)
	if parseErr != nil || actual != generation {
		return "", 0, fmt.Errorf("managed %s container generation does not match persisted generation %d", service.Name, generation)
	}
	for label, expected := range map[string]string{
		labelResourceType: service.Resource.Type, labelResourceVersion: service.Resource.Version, labelResourceImage: plan.Image,
	} {
		actual, labelErr := m.inspectLabel(ctx, name, label)
		if labelErr != nil || actual != expected {
			return "", 0, fmt.Errorf("managed %s container resource identity does not match %s %s", service.Name, service.Resource.Type, service.Resource.Version)
		}
	}
	running, port := m.inspectRunning(ctx, name, plan.ClientPort)
	if !running || port == 0 {
		return "", 0, fmt.Errorf("managed %s container is not running", service.Name)
	}
	return name, port, nil
}

func (m *Manager) ownedServiceContainers(ctx context.Context, environmentKey, serviceName string) ([]string, error) {
	output, err := m.output(ctx, "ps", "-a",
		"--filter", "label="+labelOwner+"=true",
		"--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelEnvironment+"="+environmentKey,
		"--filter", "label="+labelService+"="+serviceName,
		"--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("find managed %s container: %w", serviceName, err)
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) verifiedContainerID(ctx context.Context, name, environmentKey, serviceName string) (string, error) {
	output, err := m.output(ctx, "inspect", "--format", "{{.Id}}", name)
	if err != nil {
		return "", fmt.Errorf("inspect managed %s container identity: %w", serviceName, err)
	}
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return "", fmt.Errorf("managed %s container has no inspectable identity", serviceName)
	}
	expected := map[string]string{
		labelOwner: "true", labelInstall: m.installationKey, labelEnvironment: environmentKey, labelService: serviceName,
	}
	labels := make([]string, 0, len(expected))
	for label := range expected {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		actual, labelErr := m.inspectLabel(ctx, containerID, label)
		if labelErr != nil || actual != expected[label] {
			return "", fmt.Errorf("container %s does not have the expected Portless ownership labels", name)
		}
	}
	return containerID, nil
}

// StopEnvironment removes owned containers, networks, and optionally volumes and credentials.
func (m *Manager) StopEnvironment(ctx context.Context, environmentKey string, removeVolumes bool) error {
	containers, err := m.ownedContainers(ctx, environmentKey)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := m.run(ctx, "rm", "-f", name); err != nil {
			return fmt.Errorf("remove container %s: %w", name, err)
		}
		m.stopLogCollector(name)
	}
	if removeVolumes {
		volumes, err := m.ownedResources(ctx, "volume", environmentKey)
		if err != nil {
			return err
		}
		for _, name := range volumes {
			if err := m.run(ctx, "volume", "rm", name); err != nil {
				return fmt.Errorf("remove volume %s: %w", name, err)
			}
		}
		if safePathComponent(environmentKey) {
			if err := os.RemoveAll(filepath.Join(m.credentialsRoot, environmentKey)); err != nil {
				return fmt.Errorf("remove generated credentials: %w", err)
			}
		}
	}
	networks, err := m.ownedResources(ctx, "network", environmentKey)
	if err != nil {
		return err
	}
	for _, name := range networks {
		_ = m.run(ctx, "network", "rm", name)
	}
	return nil
}

// ResetInstallation removes every container, volume, network, and credential owned by this installation.
func (m *Manager) ResetInstallation(ctx context.Context) (container.ResetResult, error) {
	result := container.ResetResult{Runtime: m.Name()}
	if probe := m.Probe(ctx); probe.State != "ready" {
		return result, fmt.Errorf("%s is not ready: %s", m.Name(), probe.Reason)
	}
	containers, err := m.ownedInstallationContainers(ctx)
	if err != nil {
		return result, err
	}
	for _, name := range containers {
		if err := m.run(ctx, "rm", "-f", name); err != nil {
			return result, fmt.Errorf("remove container %s: %w", name, err)
		}
		m.stopLogCollector(name)
		result.Containers++
	}
	volumes, err := m.ownedInstallationResources(ctx, "volume")
	if err != nil {
		return result, err
	}
	for _, name := range volumes {
		if err := m.run(ctx, "volume", "rm", name); err != nil {
			return result, fmt.Errorf("remove volume %s: %w", name, err)
		}
		result.Volumes++
	}
	networks, err := m.ownedInstallationResources(ctx, "network")
	if err != nil {
		return result, err
	}
	for _, name := range networks {
		if err := m.run(ctx, "network", "rm", name); err != nil {
			return result, fmt.Errorf("remove network %s: %w", name, err)
		}
		result.Networks++
	}
	if err := os.RemoveAll(m.credentialsRoot); err != nil {
		return result, fmt.Errorf("remove generated credentials: %w", err)
	}
	return result, nil
}

// StopService verifies and removes all owned containers for one service.
func (m *Manager) StopService(ctx context.Context, environmentKey, serviceName string) error {
	output, err := m.output(ctx, "ps", "-a", "--filter", "label="+labelOwner+"=true",
		"--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelEnvironment+"="+environmentKey,
		"--filter", "label="+labelService+"="+serviceName,
		"--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("find managed %s container: %w", serviceName, err)
	}
	for _, name := range nonemptyLines(string(output)) {
		if err := m.run(ctx, "rm", "-f", name); err != nil {
			return fmt.Errorf("remove managed %s container: %w", serviceName, err)
		}
		m.stopLogCollector(name)
	}
	return nil
}

// Close stops all active container log collectors.
func (m *Manager) Close() {
	m.mu.Lock()
	collectors := make([]*logCollector, 0, len(m.collectors))
	for name, collector := range m.collectors {
		collectors = append(collectors, collector)
		delete(m.collectors, name)
	}
	m.mu.Unlock()
	for _, collector := range collectors {
		collector.cancel()
	}
}

func (m *Manager) startLogCollector(containerName, service string, generation int64, directory string) error {
	m.stopLogCollector(containerName)
	stdout, err := logstore.OpenSink(directory, service, "stdout", generation)
	if err != nil {
		return err
	}
	stderr, err := logstore.OpenSink(directory, service, "stderr", generation)
	if err != nil {
		_ = stdout.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, m.engine.Binary(), "logs", "--follow", containerName)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		cancel()
		_ = stdout.Close()
		_ = stderr.Close()
		return fmt.Errorf("collect %s logs: %w", service, err)
	}
	collector := &logCollector{cancel: cancel}
	m.mu.Lock()
	m.collectors[containerName] = collector
	m.mu.Unlock()
	go func() {
		_ = command.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
		m.mu.Lock()
		if m.collectors[containerName] == collector {
			delete(m.collectors, containerName)
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) stopLogCollector(containerName string) {
	m.mu.Lock()
	collector := m.collectors[containerName]
	delete(m.collectors, containerName)
	m.mu.Unlock()
	if collector != nil {
		collector.cancel()
	}
}

func (m *Manager) ensureNetwork(ctx context.Context, name, environmentName, environmentKey string) error {
	expected := map[string]string{
		labelOwner: "true", labelInstall: m.installationKey, labelEnvironment: environmentKey, labelEnvironmentName: environmentName,
	}
	if m.engine.ResourceExists(ctx, "network", name) {
		return m.verifyResourceLabels(ctx, "network", name, expected)
	}
	return m.run(ctx, "network", "create",
		"--label", labelOwner+"=true",
		"--label", labelInstall+"="+m.installationKey,
		"--label", labelEnvironment+"="+environmentKey,
		"--label", labelEnvironmentName+"="+environmentName,
		name)
}

func (m *Manager) ensureVolume(ctx context.Context, name, environmentName, environmentKey, service, volumeKey string) error {
	expected := map[string]string{
		labelOwner: "true", labelInstall: m.installationKey, labelEnvironment: environmentKey,
		labelEnvironmentName: environmentName, labelService: service, labelResourceVolume: volumeKey,
	}
	if m.engine.ResourceExists(ctx, "volume", name) {
		return m.verifyResourceLabels(ctx, "volume", name, expected)
	}
	return m.run(ctx, "volume", "create",
		"--label", labelOwner+"=true",
		"--label", labelInstall+"="+m.installationKey,
		"--label", labelEnvironment+"="+environmentKey,
		"--label", labelEnvironmentName+"="+environmentName,
		"--label", labelService+"="+service,
		"--label", labelResourceVolume+"="+volumeKey,
		name)
}

func (m *Manager) verifyResourceLabels(ctx context.Context, kind, name string, expected map[string]string) error {
	labels := make([]string, 0, len(expected))
	for label := range expected {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		output, err := m.output(ctx, kind, "inspect", "--format", "{{ index .Labels \""+label+"\" }}", name)
		if err != nil || strings.TrimSpace(string(output)) != expected[label] {
			return fmt.Errorf("existing managed %s %s has mismatched ownership labels", kind, name)
		}
	}
	return nil
}

func (m *Manager) inspectRunning(ctx context.Context, name string, containerPort int) (bool, int) {
	output, err := m.output(ctx, "inspect", "--format", "{{.State.Running}}", name)
	if err != nil || strings.TrimSpace(string(output)) != "true" {
		return false, 0
	}
	port, err := m.publishedPort(ctx, name, containerPort)
	return err == nil, port
}

func (m *Manager) inspectEnvironment(ctx context.Context, name string) (map[string]string, error) {
	output, err := m.output(ctx, "inspect", "--format", "{{json .Config.Env}}", name)
	if err != nil {
		return nil, err
	}
	var values []string
	if err := json.Unmarshal(output, &values); err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, value := range values {
		name, content, found := strings.Cut(value, "=")
		if found {
			result[name] = content
		}
	}
	return result, nil
}

func (m *Manager) inspectLabel(ctx context.Context, name, label string) (string, error) {
	output, err := m.output(ctx, "inspect", "--format", "{{ index .Config.Labels \""+label+"\" }}", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *Manager) waitForPort(ctx context.Context, name string, containerPort int) (int, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		port, err := m.publishedPort(deadlineCtx, name, containerPort)
		if err == nil && port > 0 {
			return port, nil
		}
		select {
		case <-deadlineCtx.Done():
			return 0, fmt.Errorf("container %s did not publish port %d", name, containerPort)
		case <-ticker.C:
		}
	}
}

func (m *Manager) publishedPort(ctx context.Context, name string, containerPort int) (int, error) {
	output, err := m.output(ctx, "port", name, strconv.Itoa(containerPort)+"/tcp")
	if err != nil {
		return 0, err
	}
	line := strings.TrimSpace(strings.Split(string(output), "\n")[0])
	if _, port, err := net.SplitHostPort(line); err == nil {
		return strconv.Atoi(port)
	}
	if colon := strings.LastIndex(line, ":"); colon >= 0 {
		return strconv.Atoi(line[colon+1:])
	}
	return strconv.Atoi(line)
}

func (m *Manager) waitForHealth(ctx context.Context, name string, publishedPort int, readiness providers.Readiness) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, readiness.Timeout)
	defer cancel()
	ticker := time.NewTicker(readiness.Interval)
	defer ticker.Stop()
	for {
		ready := false
		switch readiness.Kind {
		case "exec":
			args := append([]string{"exec", name}, readiness.Command...)
			ready = m.run(deadlineCtx, args...) == nil
		case "tcp":
			connection, err := (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext(deadlineCtx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(publishedPort)))
			if err == nil {
				_ = connection.Close()
				ready = true
			}
		}
		if ready {
			return nil
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("container %s failed readiness", name)
		case <-ticker.C:
		}
	}
}

func (m *Manager) environmentFile(environmentKey, serviceName string, defaults map[string]string) (map[string]string, string, error) {
	if !safePathComponent(environmentKey) || !safePathComponent(serviceName) {
		return nil, "", errors.New("unsafe environment or service key for credential storage")
	}
	directory := filepath.Join(m.credentialsRoot, environmentKey)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(directory, serviceName+".env")
	if content, err := os.ReadFile(path); err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, "", err
		}
		return parseEnvironment(content), path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	temporary, err := os.CreateTemp(directory, serviceName+".env.tmp-*")
	if err != nil {
		return nil, "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return nil, "", err
	}
	writer := bufio.NewWriter(temporary)
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.ContainsAny(key, "=\r\n") || strings.ContainsAny(defaults[key], "\r\n") {
			temporary.Close()
			return nil, "", errors.New("container environment contains an unsupported newline")
		}
		if _, err := fmt.Fprintf(writer, "%s=%s\n", key, defaults[key]); err != nil {
			temporary.Close()
			return nil, "", err
		}
	}
	if err := writer.Flush(); err != nil {
		temporary.Close()
		return nil, "", err
	}
	if err := temporary.Close(); err != nil {
		return nil, "", err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, "", err
	}
	return defaults, path, nil
}

func (m *Manager) resourceEnvironmentFile(environmentKey, serviceName string, specifications []providers.EnvironmentVariable) (map[string]string, string, error) {
	if !safePathComponent(environmentKey) || !safePathComponent(serviceName) {
		return nil, "", errors.New("unsafe environment or service key for credential storage")
	}
	path := filepath.Join(m.credentialsRoot, environmentKey, serviceName+".env")
	if content, err := os.ReadFile(path); err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, "", err
		}
		environment := parseEnvironment(content)
		if err := validatePersistedEnvironment(environment, specifications); err != nil {
			return nil, "", fmt.Errorf("managed %s credentials: %w", serviceName, err)
		}
		return environment, path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	defaults, err := resolveEnvironment(specifications)
	if err != nil {
		return nil, "", err
	}
	return m.environmentFile(environmentKey, serviceName, defaults)
}

func validatePersistedEnvironment(environment map[string]string, specifications []providers.EnvironmentVariable) error {
	if len(environment) != len(specifications) {
		return errors.New("stored environment does not match the registered resource plan")
	}
	for _, specification := range specifications {
		value, exists := environment[specification.Name]
		if !exists || specification.SecretBytes > 0 && value == "" {
			return fmt.Errorf("stored environment is missing %s", specification.Name)
		}
		if specification.SecretBytes == 0 && value != specification.Value {
			return fmt.Errorf("stored value for %s does not match the registered resource plan", specification.Name)
		}
	}
	return nil
}

func parseEnvironment(content []byte) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		name, value, found := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
		if found && name != "" {
			result[name] = value
		}
	}
	return result
}

func (m *Manager) ownedContainers(ctx context.Context, environmentKey string) ([]string, error) {
	output, err := m.output(ctx, "ps", "-a", "--filter", "label="+labelOwner+"=true", "--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelEnvironment+"="+environmentKey, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) ownedInstallationContainers(ctx context.Context) ([]string, error) {
	output, err := m.output(ctx, "ps", "-a", "--filter", "label="+labelOwner+"=true", "--filter", "label="+labelInstall+"="+m.installationKey,
		"--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) ownedResources(ctx context.Context, kind, environmentKey string) ([]string, error) {
	output, err := m.output(ctx, kind, "ls", "--filter", "label="+labelOwner+"=true", "--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelEnvironment+"="+environmentKey, "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) ownedInstallationResources(ctx context.Context, kind string) ([]string, error) {
	output, err := m.output(ctx, kind, "ls", "--filter", "label="+labelOwner+"=true", "--filter", "label="+labelInstall+"="+m.installationKey,
		"--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) run(ctx context.Context, args ...string) error {
	output, err := m.output(ctx, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %s: %w", m.engine.Name(), strings.Join(redactArguments(args), " "), strings.TrimSpace(string(output)), err)
	}
	return nil
}

func redactArguments(arguments []string) []string {
	result := make([]string, len(arguments))
	for index, argument := range arguments {
		switch {
		case strings.HasPrefix(argument, labelInstall+"="):
			result[index] = labelInstall + "=<private>"
		case strings.HasPrefix(argument, labelEnvironment+"="):
			result[index] = labelEnvironment + "=<private>"
		default:
			result[index] = argument
		}
	}
	return result
}

func (m *Manager) output(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, m.engine.Binary(), args...).CombinedOutput()
}

func resolveEnvironment(specifications []providers.EnvironmentVariable) (map[string]string, error) {
	result := make(map[string]string, len(specifications))
	for _, specification := range specifications {
		value := specification.Value
		if specification.SecretBytes > 0 {
			generated, err := secret(specification.SecretBytes)
			if err != nil {
				return nil, fmt.Errorf("generate %s: %w", specification.Name, err)
			}
			value = generated
		}
		result[specification.Name] = value
	}
	return result, nil
}

func resourceName(parts ...string) string {
	name := strings.ToLower(strings.Join(parts, "-"))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name)
	name = strings.Trim(name, "-")
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	hash := hex.EncodeToString(digest[:6])
	prefix := strings.TrimRight(name[:63-len(hash)-1], "-")
	return prefix + "-" + hash
}

func secret(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func nonemptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func safePathComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
