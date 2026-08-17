// Package providers defines the contract and registry for managed resource
// plugins. A plugin may inspect only the bounded discovery workspace and
// returns declarative runtime and binding plans; it never invokes a container
// engine or reads arbitrary host paths.
package providers

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
)

var (
	pluginIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	environmentPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

const (
	maxPluginIDLength       = 64
	maxEnvironmentName      = 256
	maxEnvironmentValue     = 64 << 10
	maxPlanEnvironment      = 128
	maxPlanCommandArguments = 128
	maxPlanVolumes          = 16
)

// Severity classifies a resource discovery diagnostic.
type Severity string

const (
	// SeverityInfo is a non-actionable resource discovery detail.
	SeverityInfo Severity = "info"
	// SeverityWarning identifies a recoverable resource discovery concern.
	SeverityWarning Severity = "warning"
)

// Diagnostic is a structured message returned by a resource plugin.
type Diagnostic struct {
	Severity Severity
	Code     string
	File     string
	Message  string
}

// Workspace is the root-confined, read-only source capability supplied by the
// discovery engine.
type Workspace interface {
	// Root returns the canonical absolute discovery root.
	Root() string
	// Files returns sorted source-relative regular-file paths.
	Files() []string
	// Exists reports whether a regular file exists in the indexed snapshot.
	Exists(relativePath string) bool
	// IsDir reports whether a directory exists in the indexed snapshot.
	IsDir(relativePath string) bool
	// ReadFile returns a bounded read of an indexed regular file.
	ReadFile(context.Context, string) ([]byte, error)
}

// Consumer describes an application service that may depend on a resource.
type Consumer struct {
	Key       string
	Directory string
	Name      string
	Framework string
}

// BindingClaim proposes an environment-variable connection from a consumer.
type BindingClaim struct {
	ConsumerKey string
	Environment string
	Required    bool
}

// Candidate is a managed resource proposed from source evidence.
type Candidate struct {
	Key      string
	Name     string
	Version  string
	Evidence []model.Evidence
	Bindings []BindingClaim
}

// Findings contains resource candidates and diagnostics returned by a plugin.
type Findings struct {
	Candidates  []Candidate
	Diagnostics []Diagnostic
}

// Descriptor declares a resource plugin's canonical ID, aliases, and default version.
type Descriptor struct {
	ID             string
	Aliases        []string
	DefaultVersion string
}

// EnvironmentVariable declares a container setting or generated secret.
type EnvironmentVariable struct {
	Name        string
	Value       string
	SecretBytes int
}

// Volume declares an installation-owned persistent mount for a resource.
type Volume struct {
	Key  string
	Path string
}

// Readiness describes the TCP or exec probe for a managed container.
type Readiness struct {
	Kind     string
	Command  []string
	Timeout  time.Duration
	Interval time.Duration
}

// ContainerPlan is a validated declarative recipe for a resource container.
type ContainerPlan struct {
	Image       string
	ClientPort  int
	Environment []EnvironmentVariable
	Command     []string
	Volumes     []Volume
	Readiness   Readiness
}

// BindingContext contains the endpoint and generated settings used to bind a consumer.
type BindingContext struct {
	Environment       string
	Host              string
	Port              int
	TargetEnvironment map[string]string
	Active            bool
}

// BindingResult separates values injected into a process from values safe to
// expose through inspection APIs. Both maps must contain the same keys when a
// binding is active.
type BindingResult struct {
	Values     map[string]string
	SafeValues map[string]string
}

// Plugin discovers, plans, and binds one managed resource type.
type Plugin interface {
	// Descriptor returns immutable registration metadata for the plugin.
	Descriptor() Descriptor
	// Detect examines source evidence for resource candidates and consumer bindings.
	Detect(context.Context, Workspace, []Consumer) (Findings, error)
	// Plan returns a declarative container recipe for a resource version.
	Plan(model.ResourceDefinition) (ContainerPlan, error)
	// Bind constructs active and safely inspectable consumer environment values.
	Bind(BindingContext) (BindingResult, error)
}

// Registry validates and coordinates trusted in-process resource plugins.
type Registry struct {
	plugins     map[string]Plugin
	descriptors map[string]Descriptor
	aliases     map[string]string
	order       []string
	planMu      sync.Mutex
	plans       map[string]ContainerPlan
}

// NewRegistry validates plugins and eagerly caches each default container plan.
func NewRegistry(plugins ...Plugin) (*Registry, error) {
	registry := &Registry{
		plugins: make(map[string]Plugin), descriptors: make(map[string]Descriptor), aliases: make(map[string]string), plans: make(map[string]ContainerPlan),
	}
	for _, plugin := range plugins {
		if plugin == nil {
			return nil, errors.New("resource plugin is nil")
		}
		descriptor, err := callDescriptor(plugin)
		if err != nil {
			return nil, err
		}
		if !validPluginID(descriptor.ID) {
			return nil, fmt.Errorf("invalid resource plugin ID %q", descriptor.ID)
		}
		if !validVersion(descriptor.DefaultVersion) {
			return nil, fmt.Errorf("resource plugin %s has an invalid default version", descriptor.ID)
		}
		if _, exists := registry.aliases[descriptor.ID]; exists {
			return nil, fmt.Errorf("duplicate resource plugin ID or alias %q", descriptor.ID)
		}
		registry.plugins[descriptor.ID] = plugin
		descriptor.Aliases = append([]string(nil), descriptor.Aliases...)
		registry.descriptors[descriptor.ID] = descriptor
		registry.aliases[descriptor.ID] = descriptor.ID
		registry.order = append(registry.order, descriptor.ID)
		for _, alias := range descriptor.Aliases {
			if !validPluginID(alias) {
				return nil, fmt.Errorf("resource plugin %s has invalid alias %q", descriptor.ID, alias)
			}
			if _, exists := registry.aliases[alias]; exists {
				return nil, fmt.Errorf("duplicate resource plugin ID or alias %q", alias)
			}
			registry.aliases[alias] = descriptor.ID
		}
		definition := model.ResourceDefinition{Type: descriptor.ID, Version: descriptor.DefaultVersion}
		plan, err := callPlan(plugin, definition)
		if err != nil {
			return nil, fmt.Errorf("resource plugin %s default plan: %w", descriptor.ID, err)
		}
		if err := validatePlan(plan); err != nil {
			return nil, fmt.Errorf("resource plugin %s default plan: %w", descriptor.ID, err)
		}
		registry.plans[planKey(definition)] = clonePlan(plan)
	}
	sort.Strings(registry.order)
	return registry, nil
}

// MustRegistry constructs a registry or panics when plugin contracts are invalid.
func MustRegistry(plugins ...Plugin) *Registry {
	registry, err := NewRegistry(plugins...)
	if err != nil {
		panic(err)
	}
	return registry
}

// Plugins returns registered plugins in canonical ID order.
func (r *Registry) Plugins() []Plugin {
	if r == nil {
		return nil
	}
	result := make([]Plugin, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.plugins[id])
	}
	return result
}

// IDs returns registered canonical resource IDs in sorted order.
func (r *Registry) IDs() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.order...)
}

// Descriptor returns registration metadata for a canonical ID or alias.
func (r *Registry) Descriptor(resourceType string) (Descriptor, bool) {
	_, canonical, ok := r.lookup(resourceType)
	if !ok {
		return Descriptor{}, false
	}
	descriptor := r.descriptors[canonical]
	descriptor.Aliases = append([]string(nil), descriptor.Aliases...)
	return descriptor, true
}

// Resolve returns a canonical resource definition and its stable client port.
func (r *Registry) Resolve(resourceType, version string) (model.ResourceDefinition, int, error) {
	plugin, canonical, ok := r.lookup(resourceType)
	if !ok {
		return model.ResourceDefinition{}, 0, fmt.Errorf("unknown resource type %q", resourceType)
	}
	descriptor := r.descriptors[canonical]
	if version == "" {
		version = descriptor.DefaultVersion
	}
	if !validVersion(version) {
		return model.ResourceDefinition{}, 0, fmt.Errorf("resource %s has an invalid version", canonical)
	}
	definition := model.ResourceDefinition{Type: canonical, Version: version}
	plan, err := r.loadPlan(plugin, definition)
	if err != nil {
		return model.ResourceDefinition{}, 0, err
	}
	return definition, plan.ClientPort, nil
}

// Plan validates a resource service and returns its cached declarative container plan.
func (r *Registry) Plan(service model.ServiceDefinition) (ContainerPlan, error) {
	if service.Kind != model.ServiceResource || service.Resource == nil {
		return ContainerPlan{}, fmt.Errorf("service %s is not a resource", service.Name)
	}
	definition, port, err := r.Resolve(service.Resource.Type, service.Resource.Version)
	if err != nil {
		return ContainerPlan{}, err
	}
	if service.Resource.Type != definition.Type || service.Resource.Version != definition.Version {
		return ContainerPlan{}, fmt.Errorf("service %s has a non-canonical resource definition", service.Name)
	}
	if service.Port != port {
		return ContainerPlan{}, fmt.Errorf("service %s declares port %d but resource %s requires %d", service.Name, service.Port, definition.Type, port)
	}
	plugin := r.plugins[definition.Type]
	plan, err := r.loadPlan(plugin, definition)
	if err != nil {
		return ContainerPlan{}, err
	}
	return plan, nil
}

// Detect invokes one canonical resource plugin against a bounded workspace.
func (r *Registry) Detect(ctx context.Context, resourceType string, workspace Workspace, consumers []Consumer) (Findings, error) {
	plugin, canonical, ok := r.lookup(resourceType)
	if !ok || canonical != resourceType {
		return Findings{}, fmt.Errorf("resource plugin %s is not registered", resourceType)
	}
	return callDetect(ctx, plugin, workspace, append([]Consumer(nil), consumers...))
}

// Bind validates a resource edge and returns secret-bearing and safe consumer settings.
func (r *Registry) Bind(service model.ServiceDefinition, connection model.Connection, context BindingContext) (BindingResult, error) {
	if service.Kind != model.ServiceResource || service.Resource == nil {
		return BindingResult{}, fmt.Errorf("connection target %s is not a resource", service.Name)
	}
	plan, err := r.Plan(service)
	if err != nil {
		return BindingResult{}, err
	}
	plugin, canonical, ok := r.lookup(connection.Binding)
	if !ok {
		return BindingResult{}, fmt.Errorf("unknown resource binding %q", connection.Binding)
	}
	if canonical != service.Resource.Type {
		return BindingResult{}, fmt.Errorf("connection binding %s does not match target resource %s", canonical, service.Resource.Type)
	}
	if !validEnvironment(context.Environment) {
		return BindingResult{}, fmt.Errorf("resource binding has invalid environment variable %q", context.Environment)
	}
	if context.Active && (context.Host == "" || context.Port < 1 || context.Port > 65535) {
		return BindingResult{}, errors.New("active resource binding requires a valid host and port")
	}
	context.TargetEnvironment = cloneStringMap(context.TargetEnvironment)
	result, err := callBind(plugin, context)
	if err != nil {
		return BindingResult{}, fmt.Errorf("resource plugin %s binding: %w", canonical, err)
	}
	if err := validateBindingResult(result, context.Active); err != nil {
		return BindingResult{}, fmt.Errorf("resource plugin %s binding: %w", canonical, err)
	}
	if context.Active {
		for _, specification := range plan.Environment {
			if specification.SecretBytes == 0 {
				continue
			}
			secret := context.TargetEnvironment[specification.Name]
			for key, safe := range result.SafeValues {
				if secret != "" && strings.Contains(safe, secret) {
					return BindingResult{}, fmt.Errorf("resource plugin %s binding exposed %s through safe value %s", canonical, specification.Name, key)
				}
			}
		}
	}
	result.Values = cloneStringMap(result.Values)
	result.SafeValues = cloneStringMap(result.SafeValues)
	return result, nil
}

func (r *Registry) loadPlan(plugin Plugin, definition model.ResourceDefinition) (ContainerPlan, error) {
	key := planKey(definition)
	r.planMu.Lock()
	defer r.planMu.Unlock()
	if plan, exists := r.plans[key]; exists {
		return clonePlan(plan), nil
	}
	plan, err := callPlan(plugin, definition)
	if err != nil {
		return ContainerPlan{}, fmt.Errorf("resource plugin %s plan: %w", definition.Type, err)
	}
	if err := validatePlan(plan); err != nil {
		return ContainerPlan{}, fmt.Errorf("resource plugin %s plan: %w", definition.Type, err)
	}
	r.plans[key] = clonePlan(plan)
	return clonePlan(plan), nil
}

func planKey(definition model.ResourceDefinition) string {
	return definition.Type + "\x00" + definition.Version
}

func validVersion(version string) bool {
	return version != "" && len(version) <= 128 && strings.TrimSpace(version) == version && !strings.ContainsAny(version, "\x00\r\n\t ")
}

func validPluginID(value string) bool {
	return len(value) <= maxPluginIDLength && pluginIDPattern.MatchString(value)
}

func validEnvironment(value string) bool {
	return len(value) <= maxEnvironmentName && environmentPattern.MatchString(value)
}

func clonePlan(plan ContainerPlan) ContainerPlan {
	plan.Environment = append([]EnvironmentVariable(nil), plan.Environment...)
	plan.Command = append([]string(nil), plan.Command...)
	plan.Volumes = append([]Volume(nil), plan.Volumes...)
	plan.Readiness.Command = append([]string(nil), plan.Readiness.Command...)
	return plan
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (r *Registry) lookup(value string) (Plugin, string, bool) {
	if r == nil {
		return nil, "", false
	}
	canonical, ok := r.aliases[strings.ToLower(strings.TrimSpace(value))]
	if !ok {
		return nil, "", false
	}
	return r.plugins[canonical], canonical, true
}

func validatePlan(plan ContainerPlan) error {
	if plan.Image == "" || len(plan.Image) > 512 || strings.ContainsAny(plan.Image, "\x00\r\n\t ") || !strings.HasPrefix(plan.Image, "docker.io/") {
		return fmt.Errorf("container image %q must be a fully qualified docker.io reference", plan.Image)
	}
	if plan.ClientPort < 1 || plan.ClientPort > 65535 {
		return fmt.Errorf("client port %d is outside 1..65535", plan.ClientPort)
	}
	seenEnvironment := make(map[string]struct{}, len(plan.Environment))
	if len(plan.Environment) > maxPlanEnvironment {
		return fmt.Errorf("resource plan declares more than %d environment variables", maxPlanEnvironment)
	}
	for _, variable := range plan.Environment {
		if !validEnvironment(variable.Name) {
			return fmt.Errorf("invalid container environment variable %q", variable.Name)
		}
		if _, duplicate := seenEnvironment[variable.Name]; duplicate {
			return fmt.Errorf("duplicate container environment variable %q", variable.Name)
		}
		seenEnvironment[variable.Name] = struct{}{}
		if variable.SecretBytes > 0 && variable.Value != "" || variable.SecretBytes > 0 && variable.SecretBytes < 16 || variable.SecretBytes < 0 || variable.SecretBytes > 128 {
			return fmt.Errorf("container environment variable %s has an invalid value source", variable.Name)
		}
		if variable.SecretBytes == 0 && (len(variable.Value) > maxEnvironmentValue || strings.ContainsAny(variable.Value, "\x00\r\n")) {
			return fmt.Errorf("container environment variable %s contains an invalid value", variable.Name)
		}
	}
	if len(plan.Command) > maxPlanCommandArguments {
		return fmt.Errorf("resource plan declares more than %d command arguments", maxPlanCommandArguments)
	}
	for _, argument := range plan.Command {
		if argument == "" || len(argument) > 4096 || strings.ContainsAny(argument, "\x00\r\n") {
			return errors.New("container command contains an invalid argument")
		}
	}
	if len(plan.Volumes) > maxPlanVolumes {
		return fmt.Errorf("resource plan declares more than %d persistent volumes", maxPlanVolumes)
	}
	seenVolumes := make(map[string]struct{}, len(plan.Volumes))
	seenVolumePaths := make(map[string]struct{}, len(plan.Volumes))
	for _, volume := range plan.Volumes {
		if !validPluginID(volume.Key) {
			return fmt.Errorf("invalid container volume key %q", volume.Key)
		}
		if _, duplicate := seenVolumes[volume.Key]; duplicate {
			return fmt.Errorf("duplicate container volume key %q", volume.Key)
		}
		seenVolumes[volume.Key] = struct{}{}
		cleaned := path.Clean(volume.Path)
		if cleaned != volume.Path || len(cleaned) > 4096 || !strings.HasPrefix(cleaned, "/") || cleaned == "/" || strings.ContainsAny(cleaned, "\x00\r\n:\\") {
			return fmt.Errorf("invalid container volume path %q", volume.Path)
		}
		if _, duplicate := seenVolumePaths[cleaned]; duplicate {
			return fmt.Errorf("duplicate container volume path %q", volume.Path)
		}
		seenVolumePaths[cleaned] = struct{}{}
	}
	switch plan.Readiness.Kind {
	case "exec":
		if len(plan.Readiness.Command) == 0 || len(plan.Readiness.Command) > 64 {
			return errors.New("exec readiness requires a command")
		}
		for _, argument := range plan.Readiness.Command {
			if argument == "" || len(argument) > 4096 || strings.ContainsAny(argument, "\x00\r\n") {
				return errors.New("readiness command contains an invalid argument")
			}
		}
	case "tcp":
		if len(plan.Readiness.Command) != 0 {
			return errors.New("TCP readiness cannot declare a command")
		}
	default:
		return fmt.Errorf("unsupported readiness kind %q", plan.Readiness.Kind)
	}
	if plan.Readiness.Timeout <= 0 || plan.Readiness.Timeout > 5*time.Minute || plan.Readiness.Interval <= 0 || plan.Readiness.Interval > 30*time.Second {
		return errors.New("readiness timeout or interval is outside the allowed bounds")
	}
	return nil
}

func validateBindingResult(result BindingResult, active bool) error {
	if len(result.Values) > 64 || len(result.SafeValues) > 64 {
		return errors.New("binding returned too many environment values")
	}
	if !active && len(result.Values) != 0 {
		return errors.New("inactive binding returned injectable values")
	}
	if active && len(result.Values) == 0 {
		return errors.New("active binding returned no injectable values")
	}
	if len(result.SafeValues) == 0 {
		return errors.New("binding returned no safe values")
	}
	for key, value := range result.SafeValues {
		if !validEnvironment(key) || len(value) > maxEnvironmentValue || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("binding returned invalid safe environment value %q", key)
		}
		if active {
			if _, ok := result.Values[key]; !ok {
				return fmt.Errorf("safe binding key %s has no injectable value", key)
			}
		}
	}
	for key, value := range result.Values {
		if !validEnvironment(key) || len(value) > maxEnvironmentValue || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("binding returned invalid environment value %q", key)
		}
		if _, ok := result.SafeValues[key]; !ok {
			return fmt.Errorf("binding key %s has no safe value", key)
		}
	}
	return nil
}

func callDescriptor(plugin Plugin) (result Descriptor, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("resource plugin descriptor panic: %v", recovered)
		}
	}()
	return plugin.Descriptor(), nil
}

func callDetect(ctx context.Context, plugin Plugin, workspace Workspace, consumers []Consumer) (result Findings, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return plugin.Detect(ctx, workspace, consumers)
}

func callPlan(plugin Plugin, definition model.ResourceDefinition) (result ContainerPlan, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return plugin.Plan(definition)
}

func callBind(plugin Plugin, context BindingContext) (result BindingResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return plugin.Bind(context)
}
