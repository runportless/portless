package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
	"github.com/runportless/portless/portless-daemon/providers"
)

var (
	pluginIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	environmentPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Config sets discovery resource plugins, scan deadline, and workspace limits.
type Config struct {
	Limits      Limits
	ScanTimeout time.Duration
	Resources   *providers.Registry
}

// Result contains a canonical root, discovered topology, and user-facing diagnostics.
type Result struct {
	Root        string             `json:"root"`
	Model       model.ProjectModel `json:"model"`
	Diagnostics []spec.Diagnostic  `json:"diagnostics,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
}

// Engine coordinates bounded workspace indexing, service detectors, and topology analyzers.
type Engine struct {
	config      Config
	detectors   []detectorEntry
	analyzers   []analyzerEntry
	markers     []string
	descriptors map[string]spec.Descriptor
	supersedes  map[string]map[string]bool
	resources   *providers.Registry
}

type detectorEntry struct {
	plugin     spec.ServiceDetector
	descriptor spec.Descriptor
}

type analyzerEntry struct {
	plugin     spec.TopologyAnalyzer
	descriptor spec.Descriptor
}

// New validates and constructs a deterministic discovery engine.
func New(config Config, detectors []spec.ServiceDetector, analyzers []spec.TopologyAnalyzer) (*Engine, error) {
	if len(detectors) == 0 {
		return nil, errors.New("at least one discovery detector is required")
	}
	if config.ScanTimeout <= 0 {
		config.ScanTimeout = 15 * time.Second
	}
	config.Limits = normalizeLimits(config.Limits)
	if config.Resources == nil {
		var err error
		config.Resources, err = providers.NewRegistry()
		if err != nil {
			return nil, err
		}
	}
	result := &Engine{
		config:      config,
		descriptors: make(map[string]spec.Descriptor), supersedes: make(map[string]map[string]bool), resources: config.Resources,
	}
	markers := map[string]struct{}{".git": {}}
	for _, detector := range detectors {
		descriptor, err := callPluginDescriptor(detector)
		if err != nil {
			return nil, fmt.Errorf("discovery plugin descriptor failed: %w", err)
		}
		descriptor = cloneDescriptor(descriptor)
		if err := result.addDescriptor(descriptor); err != nil {
			return nil, err
		}
		result.detectors = append(result.detectors, detectorEntry{plugin: detector, descriptor: descriptor})
		for _, marker := range descriptor.RootMarkers {
			cleaned, ok := spec.CleanRelative(marker)
			if !ok || cleaned == "." || strings.Contains(cleaned, "/") {
				return nil, fmt.Errorf("discovery plugin %s has invalid root marker %q", descriptor.ID, marker)
			}
			markers[cleaned] = struct{}{}
		}
	}
	for _, analyzer := range analyzers {
		descriptor, err := callPluginDescriptor(analyzer)
		if err != nil {
			return nil, fmt.Errorf("discovery plugin descriptor failed: %w", err)
		}
		descriptor = cloneDescriptor(descriptor)
		if err := result.addDescriptor(descriptor); err != nil {
			return nil, err
		}
		result.analyzers = append(result.analyzers, analyzerEntry{plugin: analyzer, descriptor: descriptor})
	}
	if err := result.buildSupersedence(); err != nil {
		return nil, err
	}
	for marker := range markers {
		result.markers = append(result.markers, marker)
	}
	sort.Strings(result.markers)
	sort.Slice(result.detectors, func(i, j int) bool { return result.detectors[i].descriptor.ID < result.detectors[j].descriptor.ID })
	sort.Slice(result.analyzers, func(i, j int) bool { return result.analyzers[i].descriptor.ID < result.analyzers[j].descriptor.ID })
	return result, nil
}

func (e *Engine) addDescriptor(descriptor spec.Descriptor) error {
	if !pluginIDPattern.MatchString(descriptor.ID) {
		return fmt.Errorf("invalid discovery plugin ID %q", descriptor.ID)
	}
	if _, duplicate := e.descriptors[descriptor.ID]; duplicate {
		return fmt.Errorf("duplicate discovery plugin ID %q", descriptor.ID)
	}
	descriptor.RootMarkers = append([]string(nil), descriptor.RootMarkers...)
	descriptor.Supersedes = append([]string(nil), descriptor.Supersedes...)
	e.descriptors[descriptor.ID] = descriptor
	return nil
}

func cloneDescriptor(descriptor spec.Descriptor) spec.Descriptor {
	descriptor.RootMarkers = append([]string(nil), descriptor.RootMarkers...)
	descriptor.Supersedes = append([]string(nil), descriptor.Supersedes...)
	return descriptor
}

func (e *Engine) buildSupersedence() error {
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) (map[string]bool, error)
	visit = func(id string) (map[string]bool, error) {
		if visiting[id] {
			return nil, fmt.Errorf("discovery plugin supersedence contains a cycle at %s", id)
		}
		if visited[id] {
			return e.supersedes[id], nil
		}
		visiting[id] = true
		closure := make(map[string]bool)
		for _, target := range e.descriptors[id].Supersedes {
			closure[target] = true
			if _, registered := e.descriptors[target]; !registered {
				continue
			}
			nested, err := visit(target)
			if err != nil {
				return nil, err
			}
			for value := range nested {
				closure[value] = true
			}
		}
		visiting[id] = false
		visited[id] = true
		e.supersedes[id] = closure
		return closure, nil
	}
	for id := range e.descriptors {
		if _, err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// FindRoot walks upward from start to the nearest registered project marker.
func (e *Engine) FindRoot(ctx context.Context, start string) (string, error) {
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve discovery start: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect discovery start: %w", err)
	}
	if !info.IsDir() {
		absolute = filepath.Dir(absolute)
	}
	for current := absolute; ; current = filepath.Dir(current) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		found, err := e.hasRootMarker(current)
		if err != nil {
			return "", err
		}
		if found {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", fmt.Errorf("canonicalize discovery root: %w", err)
			}
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return "", fmt.Errorf("no project root found; expected one of %s", strings.Join(e.markers, ", "))
}

func (e *Engine) hasRootMarker(directory string) (bool, error) {
	for _, marker := range e.markers {
		_, err := os.Lstat(filepath.Join(directory, marker))
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return false, fmt.Errorf("inspect project root marker %s: %w", filepath.Join(directory, marker), err)
		}
	}
	return false, nil
}

// Discover indexes the project, resolves service candidates, and analyzes its topology.
func (e *Engine) Discover(ctx context.Context, start string) (Result, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.config.ScanTimeout)
		defer cancel()
	}
	root, err := e.FindRoot(ctx, start)
	if err != nil {
		return Result{}, err
	}
	workspace, err := openWorkspace(ctx, root, e.config.Limits)
	if err != nil {
		return Result{}, err
	}
	defer workspace.Close()

	var candidates []pluginCandidate
	var diagnostics []spec.Diagnostic
	for _, detector := range e.detectors {
		descriptor := detector.descriptor
		findings, err := callDetector(ctx, detector.plugin, workspace)
		if err != nil {
			return Result{}, fmt.Errorf("discovery plugin %s failed: %w", descriptor.ID, err)
		}
		for _, diagnostic := range findings.Diagnostics {
			diagnostic, err = normalizeDiagnostic(workspace, descriptor.ID, diagnostic)
			if err != nil {
				return Result{}, fmt.Errorf("discovery plugin %s returned an invalid diagnostic: %w", descriptor.ID, err)
			}
			diagnostics = append(diagnostics, diagnostic)
		}
		for _, candidate := range findings.Candidates {
			normalized, err := normalizeCandidate(root, workspace, descriptor, candidate)
			if err != nil {
				return Result{}, fmt.Errorf("discovery plugin %s returned an invalid candidate: %w", descriptor.ID, err)
			}
			candidates = append(candidates, normalized)
		}
	}
	resolved, err := e.resolveCandidates(candidates)
	if err != nil {
		return Result{}, err
	}
	if len(resolved) == 0 {
		ids := make([]string, 0, len(e.detectors))
		for _, detector := range e.detectors {
			ids = append(ids, detector.descriptor.ID)
		}
		message := fmt.Sprintf("no supported services were discovered (enabled plugins: %s)", strings.Join(ids, ", "))
		if len(diagnostics) > 0 {
			sortDiagnostics(diagnostics)
			details := make([]string, 0, len(diagnostics))
			for _, diagnostic := range diagnostics {
				details = append(details, diagnosticMessage(diagnostic))
			}
			message += "; " + strings.Join(details, "; ")
		}
		return Result{Root: root, Diagnostics: diagnostics}, errors.New(message)
	}

	definition := model.ProjectModel{SuggestedName: model.NormalizeDNSName(filepath.Base(root))}
	if definition.SuggestedName == "" {
		definition.SuggestedName = "project"
	}
	definition.PrimaryService = selectPrimary(resolved)
	for _, service := range resolved {
		definition.Services = append(definition.Services, service.Definition)
	}
	consumers := make([]providers.Consumer, 0, len(resolved))
	consumerByKey := make(map[string]providers.Consumer, len(resolved))
	for _, service := range resolved {
		consumer := providers.Consumer{
			Key: service.Key, Directory: service.Directory, Name: service.Definition.Name, Framework: service.Plugin,
		}
		consumers = append(consumers, consumer)
		consumerByKey[consumer.Key] = consumer
	}
	claimedBindings := make(map[string]struct{})
	resourceKeys := make(map[string]struct{})
	resourceNames := make(map[string]string)
	for _, resourceType := range e.resources.IDs() {
		findings, err := e.resources.Detect(ctx, resourceType, workspace, consumers)
		if err != nil {
			return Result{}, fmt.Errorf("resource plugin %s failed: %w", resourceType, err)
		}
		for _, diagnostic := range findings.Diagnostics {
			normalized, normalizeErr := normalizeDiagnostic(workspace, resourceType, spec.Diagnostic{
				Severity: spec.Severity(diagnostic.Severity), Code: diagnostic.Code, File: diagnostic.File, Message: diagnostic.Message,
			})
			if normalizeErr != nil {
				return Result{}, fmt.Errorf("resource plugin %s returned an invalid diagnostic: %w", resourceType, normalizeErr)
			}
			diagnostics = append(diagnostics, normalized)
		}
		for _, candidate := range findings.Candidates {
			key, service, connections, include, normalizeErr := e.normalizeResourceCandidate(workspace, resourceType, candidate, consumerByKey)
			if normalizeErr != nil {
				return Result{}, fmt.Errorf("resource plugin %s returned an invalid candidate: %w", resourceType, normalizeErr)
			}
			if !include {
				diagnostics = append(diagnostics, spec.Diagnostic{
					Severity: spec.SeverityInfo, Code: "UNOWNED_RESOURCE", Plugin: resourceType,
					Message: "resource evidence could not be assigned to an application service and was ignored",
				})
				continue
			}
			identity := resourceType + "\x00" + key
			if _, duplicate := resourceKeys[identity]; duplicate {
				return Result{}, fmt.Errorf("resource plugin %s returned duplicate candidate key %s", resourceType, key)
			}
			resourceKeys[identity] = struct{}{}
			nameKey := strings.ToLower(service.Name)
			if previous, duplicate := resourceNames[nameKey]; duplicate && previous != identity {
				return Result{}, fmt.Errorf("resource service name %s was discovered for both %s and %s", service.Name, previous, identity)
			}
			resourceNames[nameKey] = identity
			if err := mergeService(&definition.Services, service); err != nil {
				return Result{}, fmt.Errorf("resource plugin %s: %w", resourceType, err)
			}
			for _, connection := range connections {
				definition.Connections = append(definition.Connections, connection)
				claimedBindings[bindingClaimKey(connection.Source, connection.Environment)] = struct{}{}
			}
		}
	}
	for _, analyzer := range e.analyzers {
		descriptor := analyzer.descriptor
		findings, err := callAnalyzer(ctx, analyzer.plugin, workspace, resolved)
		if err != nil {
			return Result{}, fmt.Errorf("discovery plugin %s failed: %w", descriptor.ID, err)
		}
		for _, diagnostic := range findings.Diagnostics {
			diagnostic, err = normalizeDiagnostic(workspace, descriptor.ID, diagnostic)
			if err != nil {
				return Result{}, fmt.Errorf("discovery plugin %s returned an invalid diagnostic: %w", descriptor.ID, err)
			}
			diagnostics = append(diagnostics, diagnostic)
		}
		definition.Connections = append(definition.Connections, findings.Connections...)
		definition.References = append(definition.References, findings.References...)
	}
	definition.References = filterClaimedReferences(definition.References, claimedBindings)
	sort.Slice(definition.Services, func(i, j int) bool { return definition.Services[i].Name < definition.Services[j].Name })
	definition.Connections, err = uniqueConnections(definition.Connections)
	if err != nil {
		return Result{}, err
	}
	definition.References = uniqueReferences(definition.References)
	if err := Validate(definition, e.resources); err != nil {
		return Result{}, err
	}
	sortDiagnostics(diagnostics)
	result := Result{Root: root, Model: definition, Diagnostics: diagnostics}
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != spec.SeverityInfo {
			result.Warnings = append(result.Warnings, diagnosticMessage(diagnostic))
		}
	}
	return result, nil
}

type pluginCandidate struct {
	spec.Candidate
	plugin       string
	primaryOrder int
}

func normalizeCandidate(root string, workspace spec.Workspace, descriptor spec.Descriptor, candidate spec.Candidate) (pluginCandidate, error) {
	key, ok := spec.CleanRelative(candidate.Key)
	if !ok {
		return pluginCandidate{}, fmt.Errorf("invalid key %q", candidate.Key)
	}
	directory, ok := spec.CleanRelative(candidate.Directory)
	if !ok {
		return pluginCandidate{}, fmt.Errorf("invalid service directory %q", candidate.Directory)
	}
	runDirectory, ok := spec.CleanRelative(candidate.RunDirectory)
	if !ok {
		return pluginCandidate{}, fmt.Errorf("invalid run directory %q", candidate.RunDirectory)
	}
	if !workspace.IsDir(directory) {
		return pluginCandidate{}, fmt.Errorf("service directory %q does not exist in the workspace", directory)
	}
	if !workspace.IsDir(runDirectory) {
		return pluginCandidate{}, fmt.Errorf("run directory %q does not exist in the workspace", runDirectory)
	}
	if candidate.Definition.WorkingDirectory != "" {
		return pluginCandidate{}, errors.New("plugins must return a relative RunDirectory instead of WorkingDirectory")
	}
	if err := model.ValidateServiceName(candidate.Definition.Name); err != nil {
		return pluginCandidate{}, fmt.Errorf("service %q: %w", candidate.Definition.Name, err)
	}
	if candidate.Definition.Kind != model.ServiceProcess {
		return pluginCandidate{}, fmt.Errorf("framework candidate %s is not a process service", candidate.Definition.Name)
	}
	if len(candidate.Definition.Command) == 0 {
		return pluginCandidate{}, fmt.Errorf("process service %s has no command", candidate.Definition.Name)
	}
	for _, argument := range candidate.Definition.Command {
		if argument == "" || strings.ContainsAny(argument, "\x00\r\n") {
			return pluginCandidate{}, fmt.Errorf("service %s has an invalid command argument", candidate.Definition.Name)
		}
	}
	if candidate.Definition.Debug != nil {
		if candidate.Definition.Debug.Adapter == "" || candidate.Definition.Debug.Launcher == "" || len(candidate.Definition.Debug.Command) == 0 {
			return pluginCandidate{}, fmt.Errorf("service %s has an incomplete debug capability", candidate.Definition.Name)
		}
		for _, argument := range candidate.Definition.Debug.Command {
			if argument == "" || strings.ContainsAny(argument, "\x00\r\n") {
				return pluginCandidate{}, fmt.Errorf("service %s has an invalid debug command argument", candidate.Definition.Name)
			}
		}
	}
	if candidate.Definition.PortEnvironment != "" && !environmentPattern.MatchString(candidate.Definition.PortEnvironment) {
		return pluginCandidate{}, fmt.Errorf("service %s has invalid port environment %q", candidate.Definition.Name, candidate.Definition.PortEnvironment)
	}
	if err := validateEvidence(workspace, candidate.Definition.Evidence); err != nil {
		return pluginCandidate{}, err
	}
	workingDirectory := root
	if runDirectory != "." {
		workingDirectory = filepath.Join(root, filepath.FromSlash(runDirectory))
	}
	serviceDirectory := root
	if directory != "." {
		serviceDirectory = filepath.Join(root, filepath.FromSlash(directory))
	}
	candidate.Key = key
	candidate.Directory = directory
	candidate.RunDirectory = runDirectory
	candidate.Definition.WorkingDirectory = workingDirectory
	candidate.Definition.ServiceDirectory = serviceDirectory
	return pluginCandidate{Candidate: candidate, plugin: descriptor.ID, primaryOrder: descriptor.PrimaryOrder}, nil
}

func (e *Engine) normalizeResourceCandidate(workspace spec.Workspace, resourceType string, candidate providers.Candidate, consumers map[string]providers.Consumer) (string, model.ServiceDefinition, []model.Connection, bool, error) {
	key, ok := spec.CleanRelative(candidate.Key)
	if !ok || !workspace.IsDir(key) {
		return "", model.ServiceDefinition{}, nil, false, fmt.Errorf("invalid resource key %q", candidate.Key)
	}
	if err := model.ValidateServiceName(candidate.Name); err != nil {
		return "", model.ServiceDefinition{}, nil, false, fmt.Errorf("resource service %q: %w", candidate.Name, err)
	}
	if len(candidate.Evidence) == 0 {
		return "", model.ServiceDefinition{}, nil, false, errors.New("resource candidate has no evidence")
	}
	if err := validateEvidence(workspace, candidate.Evidence); err != nil {
		return "", model.ServiceDefinition{}, nil, false, err
	}
	definition, port, err := e.resources.Resolve(resourceType, candidate.Version)
	if err != nil {
		return "", model.ServiceDefinition{}, nil, false, err
	}
	service := model.ServiceDefinition{
		Name: candidate.Name, Kind: model.ServiceResource, Resource: &definition, Port: port, Required: true,
		Evidence: append([]model.Evidence(nil), candidate.Evidence...),
	}
	plan, err := e.resources.Plan(service)
	if err != nil {
		return "", model.ServiceDefinition{}, nil, false, err
	}
	service.Health = model.HealthCheck{Kind: plan.Readiness.Kind, Timeout: plan.Readiness.Timeout, Interval: plan.Readiness.Interval}
	seenConsumers := make(map[string]string)
	connections := make([]model.Connection, 0, len(candidate.Bindings))
	for _, binding := range candidate.Bindings {
		consumer, exists := consumers[binding.ConsumerKey]
		if !exists {
			return "", model.ServiceDefinition{}, nil, false, fmt.Errorf("binding references unknown consumer key %q", binding.ConsumerKey)
		}
		if !environmentPattern.MatchString(binding.Environment) {
			return "", model.ServiceDefinition{}, nil, false, fmt.Errorf("binding for %s has invalid environment variable %q", consumer.Name, binding.Environment)
		}
		if previous, duplicate := seenConsumers[binding.ConsumerKey]; duplicate {
			if previous != binding.Environment {
				return "", model.ServiceDefinition{}, nil, false, fmt.Errorf("consumer %s has conflicting resource bindings %s and %s", consumer.Name, previous, binding.Environment)
			}
			continue
		}
		seenConsumers[binding.ConsumerKey] = binding.Environment
		connections = append(connections, model.Connection{
			Source: consumer.Name, Target: candidate.Name, Protocol: model.ProtocolTCP, Binding: definition.Type,
			Environment: binding.Environment, Required: binding.Required,
		})
	}
	sort.Slice(connections, func(i, j int) bool { return connections[i].Source < connections[j].Source })
	return key, service, connections, len(connections) > 0, nil
}

func normalizeDiagnostic(workspace spec.Workspace, plugin string, diagnostic spec.Diagnostic) (spec.Diagnostic, error) {
	if diagnostic.Plugin == "" {
		diagnostic.Plugin = plugin
	} else if diagnostic.Plugin != plugin {
		return spec.Diagnostic{}, fmt.Errorf("diagnostic attributed to plugin %s", diagnostic.Plugin)
	}
	if diagnostic.Code == "" || diagnostic.Message == "" {
		return spec.Diagnostic{}, errors.New("diagnostic code and message are required")
	}
	if diagnostic.Severity != spec.SeverityInfo && diagnostic.Severity != spec.SeverityWarning {
		return spec.Diagnostic{}, fmt.Errorf("invalid diagnostic severity %q", diagnostic.Severity)
	}
	if diagnostic.File != "" {
		cleaned, ok := spec.CleanRelative(diagnostic.File)
		if !ok || !workspace.Exists(cleaned) {
			return spec.Diagnostic{}, fmt.Errorf("diagnostic file %q is not a workspace file", diagnostic.File)
		}
		diagnostic.File = cleaned
	}
	return diagnostic, nil
}

func validateEvidence(workspace spec.Workspace, evidence []model.Evidence) error {
	for _, item := range evidence {
		if item.File == "" || item.Explanation == "" {
			return errors.New("evidence file and explanation are required")
		}
		cleaned, ok := spec.CleanRelative(item.File)
		if !ok || !workspace.Exists(cleaned) {
			return fmt.Errorf("evidence file %q is not a workspace file", item.File)
		}
		switch item.Confidence {
		case "low", "medium", "high":
		default:
			return fmt.Errorf("evidence for %s has invalid confidence %q", item.File, item.Confidence)
		}
	}
	return nil
}

func (e *Engine) resolveCandidates(candidates []pluginCandidate) ([]spec.ResolvedService, error) {
	byKey := make(map[string][]pluginCandidate)
	for _, candidate := range candidates {
		byKey[candidate.Key] = append(byKey[candidate.Key], candidate)
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result []spec.ResolvedService
	for _, key := range keys {
		claims := byKey[key]
		winnerIndex := -1
		for candidateIndex, candidate := range claims {
			dominates := true
			for otherIndex, other := range claims {
				if candidateIndex == otherIndex {
					continue
				}
				if !e.supersedes[candidate.plugin][other.plugin] {
					dominates = false
					break
				}
			}
			if dominates {
				if winnerIndex >= 0 {
					return nil, fmt.Errorf("service unit %s has multiple dominant discovery claims (%s and %s)", key, claims[winnerIndex].plugin, candidate.plugin)
				}
				winnerIndex = candidateIndex
			}
		}
		if len(claims) > 1 && winnerIndex < 0 {
			plugins := make([]string, 0, len(claims))
			for _, claim := range claims {
				plugins = append(plugins, claim.plugin)
			}
			sort.Strings(plugins)
			return nil, fmt.Errorf("service unit %s was claimed by incompatible discovery plugins %s", key, strings.Join(plugins, ", "))
		}
		if winnerIndex < 0 {
			winnerIndex = 0
		}
		winner := claims[winnerIndex]
		result = append(result, spec.ResolvedService{
			Key: key, Directory: winner.Directory, Plugin: winner.plugin, Definition: winner.Definition,
			PrimaryOrder: winner.primaryOrder + winner.PrimaryPreference,
		})
	}
	seenNames := make(map[string]spec.ResolvedService)
	for _, service := range result {
		name := strings.ToLower(service.Definition.Name)
		if previous, duplicate := seenNames[name]; duplicate {
			return nil, fmt.Errorf("service name %s was discovered for both %s and %s", service.Definition.Name, previous.Key, service.Key)
		}
		seenNames[name] = service
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Definition.Name < result[j].Definition.Name })
	return result, nil
}

func selectPrimary(services []spec.ResolvedService) string {
	bestName := ""
	bestScore := -1 << 30
	for _, service := range services {
		if service.PrimaryOrder > bestScore || service.PrimaryOrder == bestScore && (bestName == "" || service.Definition.Name < bestName) {
			bestName = service.Definition.Name
			bestScore = service.PrimaryOrder
		}
	}
	return bestName
}

func mergeService(services *[]model.ServiceDefinition, candidate model.ServiceDefinition) error {
	if err := model.ValidateServiceName(candidate.Name); err != nil {
		return fmt.Errorf("service %q: %w", candidate.Name, err)
	}
	for _, existing := range *services {
		if !strings.EqualFold(existing.Name, candidate.Name) {
			continue
		}
		if sameResource(existing, candidate) {
			return nil
		}
		return fmt.Errorf("service %s conflicts with an existing discovery result", candidate.Name)
	}
	*services = append(*services, candidate)
	return nil
}

func sameResource(left, right model.ServiceDefinition) bool {
	return left.Kind == model.ServiceResource && right.Kind == model.ServiceResource && left.Resource != nil && right.Resource != nil &&
		*left.Resource == *right.Resource && left.Port == right.Port
}

func bindingClaimKey(source, environment string) string {
	return strings.ToLower(source) + "\x00" + strings.ToUpper(environment)
}

func filterClaimedReferences(references []model.ConnectionReference, claimed map[string]struct{}) []model.ConnectionReference {
	result := references[:0]
	for _, reference := range references {
		if _, resourceBinding := claimed[bindingClaimKey(reference.Source, reference.Environment)]; resourceBinding {
			continue
		}
		result = append(result, reference)
	}
	return result
}

func uniqueConnections(input []model.Connection) ([]model.Connection, error) {
	seen := make(map[string]model.Connection)
	for _, connection := range input {
		key := strings.ToLower(connection.Source + "\x00" + connection.Target)
		if existing, duplicate := seen[key]; duplicate {
			if existing == connection {
				continue
			}
			return nil, fmt.Errorf("conflicting connections from %s to %s were discovered", connection.Source, connection.Target)
		}
		seen[key] = connection
	}
	result := make([]model.Connection, 0, len(seen))
	for _, connection := range seen {
		result = append(result, connection)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source == result[j].Source {
			return result[i].Target < result[j].Target
		}
		return result[i].Source < result[j].Source
	})
	return result, nil
}

func uniqueReferences(input []model.ConnectionReference) []model.ConnectionReference {
	seen := make(map[string]model.ConnectionReference)
	for _, reference := range input {
		key := strings.ToLower(reference.Source + "\x00" + reference.TargetHint + "\x00" + reference.Environment + "\x00" + string(reference.Protocol) + "\x00" + reference.Binding)
		seen[key] = reference
	}
	result := make([]model.ConnectionReference, 0, len(seen))
	for _, reference := range seen {
		result = append(result, reference)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source == result[j].Source {
			if result[i].TargetHint == result[j].TargetHint {
				return result[i].Environment < result[j].Environment
			}
			return result[i].TargetHint < result[j].TargetHint
		}
		return result[i].Source < result[j].Source
	})
	return result
}

func sortDiagnostics(input []spec.Diagnostic) {
	sort.Slice(input, func(i, j int) bool {
		if input[i].Plugin != input[j].Plugin {
			return input[i].Plugin < input[j].Plugin
		}
		if input[i].File != input[j].File {
			return input[i].File < input[j].File
		}
		if input[i].Code != input[j].Code {
			return input[i].Code < input[j].Code
		}
		return input[i].Message < input[j].Message
	})
}

func diagnosticMessage(diagnostic spec.Diagnostic) string {
	prefix := diagnostic.Plugin
	if diagnostic.File != "" {
		prefix += " (" + diagnostic.File + ")"
	}
	if prefix == "" {
		return diagnostic.Message
	}
	return prefix + ": " + diagnostic.Message
}

func callDetector(ctx context.Context, detector spec.ServiceDetector, workspace spec.Workspace) (result spec.Findings, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return detector.Detect(ctx, workspace)
}

type describedPlugin interface {
	Descriptor() spec.Descriptor
}

func callPluginDescriptor(plugin describedPlugin) (result spec.Descriptor, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return plugin.Descriptor(), nil
}

func callAnalyzer(ctx context.Context, analyzer spec.TopologyAnalyzer, workspace spec.Workspace, services []spec.ResolvedService) (result spec.TopologyFindings, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return analyzer.Analyze(ctx, workspace, services)
}

// Validate checks names, service definitions, resources, and connection references.
func Validate(definition model.ProjectModel, resources *providers.Registry) error {
	if resources == nil {
		var err error
		resources, err = providers.NewRegistry()
		if err != nil {
			return err
		}
	}
	if err := model.ValidateProjectName(definition.SuggestedName); err != nil {
		return fmt.Errorf("suggested project name: %w", err)
	}
	services := make(map[string]model.ServiceDefinition, len(definition.Services))
	for _, service := range definition.Services {
		if err := model.ValidateServiceName(service.Name); err != nil {
			return fmt.Errorf("service %q: %w", service.Name, err)
		}
		key := strings.ToLower(service.Name)
		if _, duplicate := services[key]; duplicate {
			return fmt.Errorf("duplicate service name %q", service.Name)
		}
		switch service.Kind {
		case model.ServiceProcess:
			if len(service.Command) == 0 {
				return fmt.Errorf("process service %q has no command", service.Name)
			}
			if service.Resource != nil {
				return fmt.Errorf("process service %q declares a resource definition", service.Name)
			}
		case model.ServiceResource:
			if service.Resource == nil {
				return fmt.Errorf("resource service %q has no resource definition", service.Name)
			}
			if len(service.Command) != 0 {
				return fmt.Errorf("resource service %q declares a process command", service.Name)
			}
			if _, err := resources.Plan(service); err != nil {
				return fmt.Errorf("resource service %q: %w", service.Name, err)
			}
		default:
			return fmt.Errorf("service %q has unsupported kind %q", service.Name, service.Kind)
		}
		services[key] = service
	}
	primary, ok := services[strings.ToLower(definition.PrimaryService)]
	if !ok {
		return fmt.Errorf("primary service %q does not exist", definition.PrimaryService)
	}
	if primary.Kind != model.ServiceProcess {
		return fmt.Errorf("primary service %q is not an application process", definition.PrimaryService)
	}
	edges := make(map[string]struct{}, len(definition.Connections))
	bindingOwners := make(map[string]string)
	for _, connection := range definition.Connections {
		if connection.Protocol != model.ProtocolHTTP && connection.Protocol != model.ProtocolTCP {
			return fmt.Errorf("connection %s to %s has unsupported transport %q", connection.Source, connection.Target, connection.Protocol)
		}
		if connection.Environment != "" && !environmentPattern.MatchString(connection.Environment) {
			return fmt.Errorf("connection %s to %s has invalid environment variable %q", connection.Source, connection.Target, connection.Environment)
		}
		if connection.Source != "external" {
			if _, ok := services[strings.ToLower(connection.Source)]; !ok {
				return fmt.Errorf("connection source %q does not exist", connection.Source)
			}
		}
		target, ok := services[strings.ToLower(connection.Target)]
		if !ok {
			return fmt.Errorf("connection target %q does not exist", connection.Target)
		}
		if target.Kind == model.ServiceResource {
			if connection.Protocol != model.ProtocolTCP || connection.Binding == "" || connection.Binding != target.Resource.Type {
				return fmt.Errorf("connection %s to resource %s must use its %s TCP binding", connection.Source, connection.Target, target.Resource.Type)
			}
			if connection.Source != "external" && connection.Environment == "" {
				return fmt.Errorf("connection %s to resource %s has no environment binding", connection.Source, connection.Target)
			}
		} else if connection.Binding != "" {
			return fmt.Errorf("connection %s to process %s declares resource binding %q", connection.Source, connection.Target, connection.Binding)
		}
		if connection.Source != "external" && connection.Environment != "" {
			bindingKeys := []string{connection.Environment}
			if target.Kind == model.ServiceResource {
				preview, err := resources.Bind(target, connection, providers.BindingContext{Environment: connection.Environment, Active: false})
				if err != nil {
					return fmt.Errorf("connection %s to %s: %w", connection.Source, connection.Target, err)
				}
				bindingKeys = bindingKeys[:0]
				for key := range preview.SafeValues {
					bindingKeys = append(bindingKeys, key)
				}
			}
			for _, environment := range bindingKeys {
				key := strings.ToLower(connection.Source) + "\x00" + environment
				if previous, duplicate := bindingOwners[key]; duplicate && !strings.EqualFold(previous, connection.Target) {
					return fmt.Errorf("connections from %s to %s and %s both inject %s", connection.Source, previous, connection.Target, environment)
				}
				bindingOwners[key] = connection.Target
			}
		}
		key := strings.ToLower(connection.Source + "\x00" + connection.Target)
		if _, duplicate := edges[key]; duplicate {
			return fmt.Errorf("duplicate connection %s to %s", connection.Source, connection.Target)
		}
		edges[key] = struct{}{}
	}
	for _, reference := range definition.References {
		if _, ok := services[strings.ToLower(reference.Source)]; !ok {
			return fmt.Errorf("connection reference source %q does not exist", reference.Source)
		}
		if reference.Protocol != model.ProtocolHTTP && reference.Protocol != model.ProtocolTCP {
			return fmt.Errorf("connection reference from %s has unsupported transport %q", reference.Source, reference.Protocol)
		}
		if reference.Environment == "" || !environmentPattern.MatchString(reference.Environment) {
			return fmt.Errorf("connection reference from %s has invalid environment variable %q", reference.Source, reference.Environment)
		}
	}
	return nil
}
