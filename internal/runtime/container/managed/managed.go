package managed

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/runtime/container"
)

const (
	labelOwner       = "dev.portless.owner"
	labelInstall     = "dev.portless.installation.key"
	labelProject     = "dev.portless.project.key"
	labelProjectName = "dev.portless.project.name"
	labelService     = "dev.portless.service.name"
)

type Engine interface {
	Name() container.RuntimeName
	Binary() string
	Probe(context.Context) container.ProbeResult
	StartHost(context.Context) container.ProbeResult
	ResourceExists(context.Context, string, string) bool
	VolumeMount(string, string) string
}

type Manager struct {
	engine          Engine
	installationKey string
	temporaryRoot   string
	credentialsRoot string
}

func New(engine Engine, installationKey, temporaryRoot string) *Manager {
	return &Manager{engine: engine, installationKey: installationKey, temporaryRoot: temporaryRoot, credentialsRoot: filepath.Join(filepath.Dir(temporaryRoot), "secrets")}
}

func (m *Manager) Name() container.RuntimeName { return m.engine.Name() }

func (m *Manager) Probe(ctx context.Context) container.ProbeResult { return m.engine.Probe(ctx) }

func (m *Manager) StartHost(ctx context.Context) container.ProbeResult {
	return m.engine.StartHost(ctx)
}

func (m *Manager) Start(ctx context.Context, projectName, projectKey string, service model.ServiceDefinition) (container.StartResult, error) {
	if service.Kind != model.ServiceContainer {
		return container.StartResult{}, errors.New("container runtime only starts container services")
	}
	if result := m.Probe(ctx); result.State != "ready" {
		return container.StartResult{}, errors.New(result.Reason)
	}
	suffix := projectKey
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	networkName := resourceName("portless", projectName, suffix)
	if err := m.ensureNetwork(ctx, networkName, projectName, projectKey); err != nil {
		return container.StartResult{}, err
	}
	containerName := resourceName("portless", projectName, service.Name, suffix)
	if running, port := m.inspectRunning(ctx, containerName, servicePort(service)); running {
		environment, _ := m.inspectEnvironment(ctx, containerName)
		return container.StartResult{ContainerName: containerName, Port: port, Environment: environment, StartedAt: time.Now().UTC()}, nil
	}
	_ = m.run(ctx, "rm", "-f", containerName)
	image, defaults, healthCommand, err := template(service)
	if err != nil {
		return container.StartResult{}, err
	}
	environment, envFile, err := m.environmentFile(projectKey, service.Name, defaults)
	if err != nil {
		return container.StartResult{}, err
	}
	volumeName := resourceName("portless", projectName, service.Name, "data", suffix)
	existingVolumes, err := m.ownedServiceVolumes(ctx, projectKey, service.Name)
	if err != nil {
		return container.StartResult{}, err
	}
	if len(existingVolumes) > 1 {
		return container.StartResult{}, fmt.Errorf("multiple managed volumes found for %s; refusing to choose implicitly", service.Name)
	}
	if len(existingVolumes) == 1 {
		volumeName = existingVolumes[0]
	}
	if err := m.ensureVolume(ctx, volumeName, projectName, projectKey, service.Name); err != nil {
		return container.StartResult{}, err
	}
	args := []string{"run", "-d", "--name", containerName,
		"--network", networkName,
		"--label", labelOwner + "=true",
		"--label", labelInstall + "=" + m.installationKey,
		"--label", labelProject + "=" + projectKey,
		"--label", labelProjectName + "=" + projectName,
		"--label", labelService + "=" + service.Name,
		"--env-file", envFile,
		"-p", "127.0.0.1::" + strconv.Itoa(servicePort(service)),
		"-v", m.engine.VolumeMount(volumeName, volumePath(service)),
		image,
	}
	if output, err := m.output(ctx, args...); err != nil {
		return container.StartResult{}, fmt.Errorf("start %s container: %s: %w", service.Name, strings.TrimSpace(string(output)), err)
	}
	port, err := m.waitForPort(ctx, containerName, servicePort(service))
	if err != nil {
		_ = m.run(context.Background(), "rm", "-f", containerName)
		return container.StartResult{}, err
	}
	if err := m.waitForHealth(ctx, containerName, healthCommand); err != nil {
		_ = m.run(context.Background(), "rm", "-f", containerName)
		return container.StartResult{}, err
	}
	return container.StartResult{ContainerName: containerName, Port: port, Environment: environment, StartedAt: time.Now().UTC()}, nil
}

func (m *Manager) StopProject(ctx context.Context, projectKey string, removeVolumes bool) error {
	containers, err := m.ownedContainers(ctx, projectKey)
	if err != nil {
		return err
	}
	for _, name := range containers {
		if err := m.run(ctx, "rm", "-f", name); err != nil {
			return fmt.Errorf("remove container %s: %w", name, err)
		}
	}
	if removeVolumes {
		volumes, err := m.ownedResources(ctx, "volume", projectKey)
		if err != nil {
			return err
		}
		for _, name := range volumes {
			if err := m.run(ctx, "volume", "rm", name); err != nil {
				return fmt.Errorf("remove volume %s: %w", name, err)
			}
		}
		if safePathComponent(projectKey) {
			if err := os.RemoveAll(filepath.Join(m.credentialsRoot, projectKey)); err != nil {
				return fmt.Errorf("remove generated credentials: %w", err)
			}
		}
	}
	networks, err := m.ownedResources(ctx, "network", projectKey)
	if err != nil {
		return err
	}
	for _, name := range networks {
		_ = m.run(ctx, "network", "rm", name)
	}
	return nil
}

func (m *Manager) StopService(ctx context.Context, projectKey, serviceName string) error {
	output, err := m.output(ctx, "ps", "-a", "--filter", "label="+labelOwner+"=true",
		"--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelProject+"="+projectKey,
		"--filter", "label="+labelService+"="+serviceName,
		"--format", "{{.Names}}")
	if err != nil {
		return fmt.Errorf("find managed %s container: %w", serviceName, err)
	}
	for _, name := range nonemptyLines(string(output)) {
		if err := m.run(ctx, "rm", "-f", name); err != nil {
			return fmt.Errorf("remove managed %s container: %w", serviceName, err)
		}
	}
	return nil
}

func (m *Manager) ensureNetwork(ctx context.Context, name, projectName, projectKey string) error {
	if m.engine.ResourceExists(ctx, "network", name) {
		return nil
	}
	return m.run(ctx, "network", "create",
		"--label", labelOwner+"=true",
		"--label", labelInstall+"="+m.installationKey,
		"--label", labelProject+"="+projectKey,
		"--label", labelProjectName+"="+projectName,
		name)
}

func (m *Manager) ensureVolume(ctx context.Context, name, projectName, projectKey, service string) error {
	if m.engine.ResourceExists(ctx, "volume", name) {
		return nil
	}
	return m.run(ctx, "volume", "create",
		"--label", labelOwner+"=true",
		"--label", labelInstall+"="+m.installationKey,
		"--label", labelProject+"="+projectKey,
		"--label", labelProjectName+"="+projectName,
		"--label", labelService+"="+service,
		name)
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

func (m *Manager) waitForHealth(ctx context.Context, name string, command []string) error {
	deadlineCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		args := append([]string{"exec", name}, command...)
		if err := m.run(deadlineCtx, args...); err == nil {
			return nil
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("container %s failed readiness", name)
		case <-ticker.C:
		}
	}
}

func (m *Manager) environmentFile(projectKey, serviceName string, defaults map[string]string) (map[string]string, string, error) {
	if !safePathComponent(projectKey) || !safePathComponent(serviceName) {
		return nil, "", errors.New("unsafe project or service key for credential storage")
	}
	directory := filepath.Join(m.credentialsRoot, projectKey)
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

func parseEnvironment(content []byte) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(string(content), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && name != "" {
			result[name] = value
		}
	}
	return result
}

func (m *Manager) ownedContainers(ctx context.Context, projectKey string) ([]string, error) {
	output, err := m.output(ctx, "ps", "-a", "--filter", "label="+labelOwner+"=true", "--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelProject+"="+projectKey, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) ownedResources(ctx context.Context, kind, projectKey string) ([]string, error) {
	output, err := m.output(ctx, kind, "ls", "--filter", "label="+labelOwner+"=true", "--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelProject+"="+projectKey, "--format", "{{.Name}}")
	if err != nil {
		return nil, err
	}
	return nonemptyLines(string(output)), nil
}

func (m *Manager) ownedServiceVolumes(ctx context.Context, projectKey, serviceName string) ([]string, error) {
	output, err := m.output(ctx, "volume", "ls", "--filter", "label="+labelOwner+"=true",
		"--filter", "label="+labelInstall+"="+m.installationKey,
		"--filter", "label="+labelProject+"="+projectKey,
		"--filter", "label="+labelService+"="+serviceName,
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
		case strings.HasPrefix(argument, labelProject+"="):
			result[index] = labelProject + "=<private>"
		default:
			result[index] = argument
		}
	}
	return result
}

func (m *Manager) output(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, m.engine.Binary(), args...).CombinedOutput()
}

func template(service model.ServiceDefinition) (string, map[string]string, []string, error) {
	switch service.Template {
	case "postgres":
		password, err := secret(24)
		if err != nil {
			return "", nil, nil, err
		}
		return "docker.io/library/postgres:" + defaultVersion(service.Version, "17"), map[string]string{
			"POSTGRES_DB": "portless", "POSTGRES_USER": "portless", "POSTGRES_PASSWORD": password,
		}, []string{"pg_isready", "-U", "portless", "-d", "portless"}, nil
	case "valkey", "redis":
		image := "docker.io/valkey/valkey:" + defaultVersion(service.Version, "8")
		command := []string{"valkey-cli", "ping"}
		if service.Template == "redis" {
			image = "docker.io/library/redis:" + defaultVersion(service.Version, "7")
			command = []string{"redis-cli", "ping"}
		}
		return image, map[string]string{}, command, nil
	default:
		return "", nil, nil, fmt.Errorf("unsupported container template %q", service.Template)
	}
}

func servicePort(service model.ServiceDefinition) int {
	if service.Template == "postgres" {
		return 5432
	}
	return 6379
}

func volumePath(service model.ServiceDefinition) string {
	if service.Template == "postgres" {
		return "/var/lib/postgresql/data"
	}
	return "/data"
}

func resourceName(parts ...string) string {
	name := strings.ToLower(strings.Join(parts, "-"))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name)
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-")
}

func secret(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func defaultVersion(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
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
