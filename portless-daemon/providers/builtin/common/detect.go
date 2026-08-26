// Package common contains source-scanning helpers shared by built-in resource
// plugins. It has no runtime behavior.
package common

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/providers"
)

// Detection configures shared marker-based resource discovery.
type Detection struct {
	Name                string
	GenericNames        []string
	Explanation         string
	Markers             []string
	DefaultEnvironment  func(providers.Consumer) string
	ExplicitEnvironment func(string, providers.Consumer) string
	ResourceName        func(string) string
}

// Detect scans topology files for resource markers and assigns consumer binding claims.
func Detect(ctx context.Context, workspace providers.Workspace, consumers []providers.Consumer, config Detection) (providers.Findings, error) {
	var unownedEvidence []model.Evidence
	evidenceByConsumer := make(map[string][]model.Evidence)
	consumerByKey := make(map[string]providers.Consumer, len(consumers))
	for _, consumer := range consumers {
		consumerByKey[consumer.Key] = consumer
	}
	type detectedBinding struct {
		claim               providers.BindingClaim
		environmentExplicit bool
		resourceName        string
		resourceExplicit    bool
	}
	bindings := make(map[string]detectedBinding)
	for _, file := range workspace.Files() {
		if err := ctx.Err(); err != nil {
			return providers.Findings{}, err
		}
		if !TopologyFile(file) {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return providers.Findings{}, err
		}
		content := string(encoded)
		lower := strings.ToLower(content)
		if !ContainsMarker(lower, config.Markers) {
			continue
		}
		evidence := model.Evidence{File: file, Explanation: config.Explanation, Confidence: "high"}
		consumer := OwningConsumer(file, consumers)
		if consumer == nil {
			unownedEvidence = append(unownedEvidence, evidence)
			continue
		}
		evidenceByConsumer[consumer.Key] = append(evidenceByConsumer[consumer.Key], evidence)
		environment := ""
		environmentExplicit := false
		if config.ExplicitEnvironment != nil {
			environment = config.ExplicitEnvironment(content, *consumer)
			environmentExplicit = environment != ""
		}
		if environment == "" && config.DefaultEnvironment != nil {
			environment = config.DefaultEnvironment(*consumer)
		}
		if environment == "" {
			continue
		}
		resourceName := config.Name
		resourceExplicit := false
		if config.ResourceName != nil {
			if configured := config.ResourceName(content); configured != "" {
				resourceName = configured
				resourceExplicit = true
			}
		}
		if genericResourceName(resourceName, config) {
			resourceName = consumerResourceName(consumer.Name, config.Name)
		}
		claim := providers.BindingClaim{ConsumerKey: consumer.Key, Environment: environment, Required: true}
		detected := detectedBinding{
			claim: claim, environmentExplicit: environmentExplicit,
			resourceName: resourceName, resourceExplicit: resourceExplicit,
		}
		if existing, exists := bindings[consumer.Key]; exists {
			if existing.claim.Environment == environment && existing.resourceName == resourceName {
				if bindingPriority(detected) > bindingPriority(existing) {
					bindings[consumer.Key] = detected
				}
				continue
			}
			if bindingPriority(detected) == bindingPriority(existing) {
				return providers.Findings{}, fmt.Errorf(
					"resource configuration for %s conflicts between %s on %s and %s on %s",
					consumer.Name, existing.resourceName, existing.claim.Environment, resourceName, environment,
				)
			}
			if bindingPriority(detected) < bindingPriority(existing) {
				continue
			}
		}
		bindings[consumer.Key] = detected
	}
	if len(unownedEvidence) == 0 && len(evidenceByConsumer) == 0 {
		return providers.Findings{}, nil
	}
	if len(bindings) == 0 {
		evidence := append([]model.Evidence(nil), unownedEvidence...)
		for _, owned := range evidenceByConsumer {
			evidence = append(evidence, owned...)
		}
		sortEvidence(evidence)
		return providers.Findings{Candidates: []providers.Candidate{{Key: ".", Name: config.Name, Evidence: evidence}}}, nil
	}
	type candidateGroup struct {
		name        string
		evidence    []model.Evidence
		bindings    []providers.BindingClaim
		directories []string
	}
	groups := make(map[string]*candidateGroup)
	consumerKeys := make([]string, 0, len(bindings))
	for consumerKey := range bindings {
		consumerKeys = append(consumerKeys, consumerKey)
	}
	sort.Strings(consumerKeys)
	for _, consumerKey := range consumerKeys {
		binding := bindings[consumerKey]
		group := groups[binding.resourceName]
		if group == nil {
			group = &candidateGroup{name: binding.resourceName}
			groups[binding.resourceName] = group
		}
		group.evidence = append(group.evidence, evidenceByConsumer[consumerKey]...)
		group.bindings = append(group.bindings, binding.claim)
		group.directories = append(group.directories, consumerByKey[consumerKey].Directory)
	}
	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	groups[groupNames[0]].evidence = append(groups[groupNames[0]].evidence, unownedEvidence...)
	candidates := make([]providers.Candidate, 0, len(groups))
	for _, name := range groupNames {
		group := groups[name]
		sortEvidence(group.evidence)
		sort.Slice(group.bindings, func(i, j int) bool { return group.bindings[i].ConsumerKey < group.bindings[j].ConsumerKey })
		sort.Strings(group.directories)
		key := "."
		if len(groups) > 1 {
			key = group.directories[0]
		}
		candidates = append(candidates, providers.Candidate{
			Key: key, Name: group.name, Evidence: group.evidence, Bindings: group.bindings,
		})
	}
	return providers.Findings{Candidates: candidates}, nil
}

func bindingPriority(binding struct {
	claim               providers.BindingClaim
	environmentExplicit bool
	resourceName        string
	resourceExplicit    bool
}) int {
	priority := 0
	if binding.environmentExplicit {
		priority++
	}
	if binding.resourceExplicit {
		priority += 2
	}
	return priority
}

func sortEvidence(evidence []model.Evidence) {
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].File < evidence[j].File })
}

func genericResourceName(name string, config Detection) bool {
	if strings.EqualFold(name, config.Name) {
		return true
	}
	for _, generic := range config.GenericNames {
		if strings.EqualFold(name, generic) {
			return true
		}
	}
	return false
}

func consumerResourceName(consumer, resource string) string {
	const maxServiceNameLength = 63
	available := maxServiceNameLength - len(resource) - 1
	if available < 1 {
		return model.NormalizeDNSName(consumer + "-" + resource)
	}
	if len(consumer) > available {
		consumer = strings.TrimRight(consumer[:available], "-")
	}
	return consumer + "-" + resource
}

// LogicalServiceHost returns the first static single-label service hostname
// used with one of schemes in content. Localhost, IP addresses, and external
// DNS names do not declare a Portless resource identity.
func LogicalServiceHost(content string, schemes ...string) string {
	lower := strings.ToLower(content)
	for _, scheme := range schemes {
		prefix := strings.ToLower(scheme) + "://"
		for offset := 0; offset < len(lower); {
			index := strings.Index(lower[offset:], prefix)
			if index < 0 {
				break
			}
			index += offset
			value := content[index:]
			if end := strings.IndexAny(value, " \t\r\n\"'`})]"); end >= 0 {
				value = value[:end]
			}
			parsed, err := url.Parse(value)
			if err == nil {
				host := strings.ToLower(parsed.Hostname())
				if host != "localhost" && !strings.Contains(host, ".") && net.ParseIP(host) == nil && model.ValidateServiceName(host) == nil {
					return host
				}
			}
			offset = index + len(prefix)
		}
	}
	return ""
}

// TopologyFile reports whether a source file is safe and useful for dependency discovery.
func TopologyFile(file string) bool {
	base := path.Base(file)
	switch base {
	case "build.gradle", "build.gradle.kts", "go.mod", "package.json", "pom.xml", "pyproject.toml":
		return true
	}
	if strings.HasPrefix(base, "application") && (strings.HasSuffix(base, ".properties") || strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
		return true
	}
	if strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return true
	}
	// Actual .env files are intentionally excluded. Templates are useful static
	// evidence and should not contain developer credentials.
	return strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template")
}

// ContainsMarker reports whether lowercased content contains any configured marker.
func ContainsMarker(content string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(content, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// FirstEnvironment returns the first environment-variable name present in content.
func FirstEnvironment(content string, names ...string) string {
	upper := strings.ToUpper(content)
	for _, name := range names {
		if strings.Contains(upper, name) {
			return name
		}
	}
	return ""
}

// OwningConsumer returns the nearest service directory containing file.
func OwningConsumer(file string, consumers []providers.Consumer) *providers.Consumer {
	directory := path.Dir(file)
	best := -1
	var owner *providers.Consumer
	for index := range consumers {
		consumerDirectory := consumers[index].Directory
		owned := consumerDirectory == "." || directory == consumerDirectory || strings.HasPrefix(directory, consumerDirectory+"/")
		if owned && len(consumerDirectory) > best {
			best = len(consumerDirectory)
			owner = &consumers[index]
		}
	}
	if owner == nil && len(consumers) == 1 {
		return &consumers[0]
	}
	return owner
}

// FrameworkEnvironment selects a Spring-specific variable or the generic fallback.
func FrameworkEnvironment(consumer providers.Consumer, spring, fallback string) string {
	if consumer.Framework == "spring-boot" {
		return spring
	}
	return fallback
}
