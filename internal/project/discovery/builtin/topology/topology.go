// Package topology discovers application-to-application URL relationships.
// Managed dependencies are discovered by independently registered resource
// plugins and are deliberately not encoded here.
package topology

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery/spec"
)

var environmentURLPattern = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*)_URL\b`)

type Analyzer struct{}

func New() spec.TopologyAnalyzer { return Analyzer{} }

func (Analyzer) Descriptor() spec.Descriptor { return spec.Descriptor{ID: "service-url"} }

func (Analyzer) Analyze(ctx context.Context, workspace spec.Workspace, services []spec.ResolvedService) (spec.TopologyFindings, error) {
	serviceNames := make(map[string]string, len(services))
	for _, service := range services {
		serviceNames[strings.ToLower(service.Definition.Name)] = service.Definition.Name
	}
	var connections []model.Connection
	var references []model.ConnectionReference
	for _, file := range workspace.Files() {
		if !topologyFile(file) {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return spec.TopologyFindings{}, err
		}
		owner := owningService(file, services)
		if owner == nil {
			continue
		}
		for _, match := range environmentURLPattern.FindAllStringSubmatch(string(encoded), -1) {
			environment := match[0]
			targetHint := model.NormalizeDNSName(strings.ToLower(match[1]))
			if targetHint == "" || strings.EqualFold(owner.Definition.Name, targetHint) {
				continue
			}
			if target, exists := serviceNames[strings.ToLower(targetHint)]; exists {
				connections = append(connections, model.Connection{
					Source: owner.Definition.Name, Target: target, Protocol: model.ProtocolHTTP,
					Environment: environment, Required: true,
				})
			} else {
				references = append(references, model.ConnectionReference{
					Source: owner.Definition.Name, TargetHint: targetHint, Protocol: model.ProtocolHTTP,
					Environment: environment, Required: true,
				})
			}
		}
	}
	return spec.TopologyFindings{Connections: connections, References: references}, nil
}

func topologyFile(file string) bool {
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
	return strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template")
}

func owningService(file string, services []spec.ResolvedService) *spec.ResolvedService {
	directory := path.Dir(file)
	best := -1
	var owner *spec.ResolvedService
	for index := range services {
		serviceDirectory := services[index].Directory
		owned := serviceDirectory == "." || directory == serviceDirectory || strings.HasPrefix(directory, serviceDirectory+"/")
		if owned && len(serviceDirectory) > best {
			best = len(serviceDirectory)
			owner = &services[index]
		}
	}
	if owner == nil && len(services) == 1 {
		return &services[0]
	}
	return owner
}
