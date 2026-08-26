package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/providers"
)

type persistedSelection struct {
	Preference RuntimeName   `json:"preference"`
	Selected   RuntimeName   `json:"selected,omitempty"`
	Used       []RuntimeName `json:"used"`
}

// Manager selects a runtime, persists that choice, and delegates resource operations.
type Manager struct {
	mu         sync.Mutex
	statePath  string
	preference RuntimeName
	selected   RuntimeName
	used       map[RuntimeName]bool
	runtimes   map[RuntimeName]Runtime
	order      []RuntimeName
	resources  *providers.Registry
}

// NewManager constructs a runtime selector from registered engines and persisted preference.
func NewManager(statePath string, resources *providers.Registry, runtimes ...Runtime) *Manager {
	manager := &Manager{statePath: statePath, preference: RuntimeAuto, used: make(map[RuntimeName]bool), runtimes: make(map[RuntimeName]Runtime), resources: resources}
	for _, runtime := range runtimes {
		if runtime == nil || runtime.Name() == "" {
			continue
		}
		if _, exists := manager.runtimes[runtime.Name()]; exists {
			continue
		}
		manager.runtimes[runtime.Name()] = runtime
		manager.order = append(manager.order, runtime.Name())
	}
	manager.load()
	return manager
}

// Status probes candidates and reports the effective runtime selection.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	probes := m.probes(ctx)
	runtime := m.choose(probes)
	if runtime == nil {
		return unavailableStatus(m.preference, probes)
	}
	probe := probeFor(probes, runtime.Name())
	return Status{Preference: m.preference, Selected: runtime.Name(), State: probe.State, Version: probe.Version, Reason: probe.Reason, Candidates: probes}
}

// StartHost attempts to activate the preferred available runtime host.
func (m *Manager) StartHost(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	probes := m.probes(ctx)
	runtime := m.choose(probes)
	if runtime == nil {
		for _, candidate := range probes {
			if candidate.State != "missing" {
				runtime = m.runtimes[candidate.Name]
				break
			}
		}
	}
	if runtime == nil {
		return unavailableStatus(m.preference, probes)
	}
	result := runtime.StartHost(ctx)
	replaceProbe(probes, result)
	if result.State == "ready" {
		m.selected = runtime.Name()
		_ = m.persist()
	}
	return Status{Preference: m.preference, Selected: runtime.Name(), State: result.State, Version: result.Version, Reason: result.Reason, Candidates: probes}
}

// SetPreference persists an automatic or explicit runtime selection.
func (m *Manager) SetPreference(value RuntimeName) error {
	if _, err := ParseRuntimeName(string(value)); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previousPreference, previousSelected := m.preference, m.selected
	m.preference = value
	if value == RuntimeAuto {
		m.selected = ""
	} else {
		if _, exists := m.runtimes[value]; !exists {
			m.preference, m.selected = previousPreference, previousSelected
			return fmt.Errorf("container runtime %s is not configured", value)
		}
		m.selected = value
	}
	if err := m.persist(); err != nil {
		m.preference, m.selected = previousPreference, previousSelected
		return err
	}
	return nil
}

// Start validates a resource plan and starts it with the selected runtime.
func (m *Manager) Start(ctx context.Context, environmentName, environmentKey string, service model.ServiceDefinition, generation int64, logsRoot string) (StartResult, error) {
	plan, err := m.resourcePlan(service)
	if err != nil {
		return StartResult{}, err
	}
	runtime, err := m.readyRuntime(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if err := m.markUsed(runtime.Name()); err != nil {
		return StartResult{}, fmt.Errorf("record used container runtime: %w", err)
	}
	return runtime.Start(ctx, environmentName, environmentKey, service, plan, generation, logsRoot)
}

// Recover verifies and resumes management of a resource container after daemon replacement.
func (m *Manager) Recover(ctx context.Context, environmentKey string, service model.ServiceDefinition, generation int64, containerName, logsRoot string) (RecoveryResult, error) {
	plan, err := m.resourcePlan(service)
	if err != nil {
		return RecoveryResult{}, err
	}
	runtime, err := m.readyRuntime(ctx)
	if err != nil {
		return RecoveryResult{}, err
	}
	recoverer, ok := runtime.(Recoverer)
	if !ok {
		return RecoveryResult{}, errors.New("selected container runtime does not support recovery")
	}
	if err := m.markUsed(runtime.Name()); err != nil {
		return RecoveryResult{}, fmt.Errorf("record used container runtime: %w", err)
	}
	return recoverer.Recover(ctx, environmentKey, service, plan, generation, containerName, logsRoot)
}

// Verify checks persisted container identity and ownership through the selected runtime.
func (m *Manager) Verify(ctx context.Context, environmentKey string, service model.ServiceDefinition, generation int64, containerName string) error {
	plan, err := m.resourcePlan(service)
	if err != nil {
		return err
	}
	runtime, err := m.readyRuntime(ctx)
	if err != nil {
		return err
	}
	verifier, ok := runtime.(Verifier)
	if !ok {
		return errors.New("selected container runtime does not support ownership verification")
	}
	if err := m.markUsed(runtime.Name()); err != nil {
		return fmt.Errorf("record used container runtime: %w", err)
	}
	return verifier.Verify(ctx, environmentKey, service, plan, generation, containerName)
}

func (m *Manager) resourcePlan(service model.ServiceDefinition) (providers.ContainerPlan, error) {
	if m.resources == nil {
		return providers.ContainerPlan{}, errors.New("resource registry is not configured")
	}
	return m.resources.Plan(service)
}

// Close releases background resources held by every configured runtime.
func (m *Manager) Close() {
	m.mu.Lock()
	runtimes := make([]Runtime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.mu.Unlock()
	for _, runtime := range runtimes {
		if closer, ok := runtime.(Closer); ok {
			closer.Close()
		}
	}
}

// StopEnvironment removes managed resources for an environment through the selected runtime.
func (m *Manager) StopEnvironment(ctx context.Context, environmentKey string, removeVolumes bool) error {
	runtime, err := m.readyRuntime(ctx)
	if err != nil {
		return err
	}
	return runtime.StopEnvironment(ctx, environmentKey, removeVolumes)
}

// StopService removes one managed resource container through the selected runtime.
func (m *Manager) StopService(ctx context.Context, environmentKey, serviceName string) error {
	runtime, err := m.readyRuntime(ctx)
	if err != nil {
		return err
	}
	return runtime.StopService(ctx, environmentKey, serviceName)
}

// ResetInstallations removes Portless-owned artifacts from every runtime used by this installation.
func (m *Manager) ResetInstallations(ctx context.Context) ([]ResetResult, error) {
	m.mu.Lock()
	names := make([]RuntimeName, 0, len(m.used))
	for name := range m.used {
		names = append(names, name)
	}
	runtimes := make(map[RuntimeName]Runtime, len(m.runtimes))
	for name, runtime := range m.runtimes {
		runtimes[name] = runtime
	}
	m.mu.Unlock()
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	results := make([]ResetResult, 0, len(names))
	for _, name := range names {
		runtime := runtimes[name]
		if runtime == nil {
			return results, fmt.Errorf("previously used container runtime %s is no longer configured", name)
		}
		result, err := runtime.ResetInstallation(ctx)
		if err != nil {
			return results, fmt.Errorf("reset Portless resources in %s: %w", name, err)
		}
		results = append(results, result)
	}
	m.mu.Lock()
	m.used = make(map[RuntimeName]bool)
	err := m.persist()
	m.mu.Unlock()
	return results, err
}

func (m *Manager) readyRuntime(ctx context.Context) (Runtime, error) {
	status := m.Status(ctx)
	if status.Selected == "" || status.State != "ready" {
		return nil, fmt.Errorf("%w: %s", ErrRuntimeUnavailable, status.Reason)
	}
	m.mu.Lock()
	runtime := m.runtimes[status.Selected]
	m.mu.Unlock()
	if runtime == nil {
		return nil, ErrRuntimeUnavailable
	}
	return runtime, nil
}

func (m *Manager) probes(ctx context.Context) []ProbeResult {
	result := make([]ProbeResult, 0, len(m.order))
	for _, name := range m.order {
		probe := m.runtimes[name].Probe(ctx)
		probe.Name = name
		result = append(result, probe)
	}
	return result
}

func (m *Manager) choose(probes []ProbeResult) Runtime {
	if m.selected != "" {
		return m.runtimes[m.selected]
	}
	if m.preference != RuntimeAuto {
		m.selected = m.preference
		_ = m.persist()
		return m.runtimes[m.selected]
	}
	for _, probe := range probes {
		if probe.State == "ready" {
			m.selected = probe.Name
			_ = m.persist()
			return m.runtimes[probe.Name]
		}
	}
	return nil
}

func (m *Manager) load() {
	content, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var state persistedSelection
	if json.Unmarshal(content, &state) != nil {
		return
	}
	if parsed, err := ParseRuntimeName(string(state.Preference)); err == nil {
		m.preference = parsed
	}
	if _, exists := m.runtimes[state.Selected]; exists {
		m.selected = state.Selected
	}
	if state.Used == nil && m.selected != "" {
		m.used[m.selected] = true
	} else {
		for _, name := range state.Used {
			if _, exists := m.runtimes[name]; exists {
				m.used[name] = true
			}
		}
	}
	if m.preference != RuntimeAuto {
		m.selected = m.preference
	}
}

func (m *Manager) persist() error {
	directory := filepath.Dir(m.statePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create runtime state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, "runtime.json.tmp-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	used := make([]RuntimeName, 0, len(m.used))
	for name := range m.used {
		used = append(used, name)
	}
	sort.Slice(used, func(i, j int) bool { return used[i] < used[j] })
	if err := json.NewEncoder(temporary).Encode(persistedSelection{Preference: m.preference, Selected: m.selected, Used: used}); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(path, m.statePath); err != nil {
		return err
	}
	return os.Chmod(m.statePath, 0o600)
}

func (m *Manager) markUsed(name RuntimeName) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.used[name] {
		return nil
	}
	m.used[name] = true
	return m.persist()
}

func unavailableStatus(preference RuntimeName, probes []ProbeResult) Status {
	state := "missing"
	parts := make([]string, 0, len(probes))
	for _, probe := range probes {
		if probe.State != "missing" {
			state = "failed"
		}
		reason := probe.Reason
		if reason == "" {
			reason = probe.State
		}
		parts = append(parts, string(probe.Name)+": "+reason)
	}
	return Status{Preference: preference, State: state, Reason: "no container runtime is ready (" + strings.Join(parts, "; ") + ")", Candidates: probes}
}

func probeFor(probes []ProbeResult, name RuntimeName) ProbeResult {
	for _, probe := range probes {
		if probe.Name == name {
			return probe
		}
	}
	return ProbeResult{Name: name, State: "missing", Reason: "runtime is not configured"}
}

func replaceProbe(probes []ProbeResult, replacement ProbeResult) {
	for index := range probes {
		if probes[index].Name == replacement.Name {
			probes[index] = replacement
			return
		}
	}
}

// IsUnavailable reports whether err means no container runtime is ready.
func IsUnavailable(err error) bool { return errors.Is(err, ErrRuntimeUnavailable) }
