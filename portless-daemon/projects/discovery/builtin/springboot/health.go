package springboot

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
	"github.com/runportless/portless/portless-daemon/projects/discovery/statichealth"
	"go.yaml.in/yaml/v3"
)

var springHealthProperties = []string{
	"management.server.port",
	"management.endpoint.health.enabled",
	"management.endpoints.web.exposure.include",
	"management.endpoints.web.exposure.exclude",
	"management.endpoints.web.base-path",
	"management.endpoints.web.path-mapping.health",
	"server.servlet.context-path",
	"spring.webflux.base-path",
}

var springConfigurationIndirection = []string{
	"spring.config.activate.on-profile",
	"spring.profiles",
	"spring.profiles.active",
	"spring.config.import",
	"spring.config.location",
	"spring.config.additional-location",
}

type springPropertyValue struct {
	value string
	file  string
}

type springHealthDetection struct {
	health      model.HealthCheck
	evidence    []model.Evidence
	diagnostics []spec.Diagnostic
}

func detectSpringHealth(ctx context.Context, workspace spec.Workspace, directory, buildFile string, timeout time.Duration) (springHealthDetection, error) {
	result := springHealthDetection{health: model.HealthCheck{Kind: "http", Path: "/actuator/health", Timeout: timeout, Interval: time.Second}}
	properties, parseDiagnostics, err := loadSpringHealthProperties(ctx, workspace, directory)
	if err != nil {
		return springHealthDetection{}, err
	}
	result.diagnostics = append(result.diagnostics, parseDiagnostics...)
	if len(parseDiagnostics) > 0 {
		result.health.Kind = "tcp"
		result.health.Path = ""
		return result, nil
	}

	resolved := make(map[string]springPropertyValue)
	for _, key := range springHealthProperties {
		value, conflict := resolveSpringProperty(properties[key])
		if conflict {
			result.health.Kind = "tcp"
			result.health.Path = ""
			result.diagnostics = append(result.diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "AMBIGUOUS_HEALTH_ENDPOINT", File: properties[key][0].file,
				Message: fmt.Sprintf("conflicting %s values were found; TCP readiness was kept", key),
			})
			return result, nil
		}
		if value.file != "" {
			resolved[key] = value
		}
	}

	if value := resolved["management.server.port"]; value.file != "" && !sameSpringManagementPort(value.value) {
		result.health.Kind = "tcp"
		result.health.Path = ""
		result.diagnostics = append(result.diagnostics, spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "SEPARATE_MANAGEMENT_PORT", File: value.file,
			Message: "Spring Boot management endpoints use a separate or dynamic port; TCP readiness was kept",
		})
		return result, nil
	}
	if value := resolved["management.endpoint.health.enabled"]; value.file != "" {
		switch {
		case strings.EqualFold(value.value, "false"):
			result.health.Kind = "tcp"
			result.health.Path = ""
			result.diagnostics = append(result.diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "HEALTH_ENDPOINT_DISABLED", File: value.file,
				Message: "Spring Boot Actuator health is disabled; TCP readiness was kept",
			})
			return result, nil
		case !strings.EqualFold(value.value, "true"):
			result.health.Kind = "tcp"
			result.health.Path = ""
			result.diagnostics = append(result.diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "DYNAMIC_HEALTH_CONFIGURATION", File: value.file,
				Message: "Spring Boot Actuator health enablement is dynamic; TCP readiness was kept",
			})
			return result, nil
		}
	}
	for _, key := range []string{"management.endpoints.web.exposure.include", "management.endpoints.web.exposure.exclude"} {
		if value := resolved[key]; value.file != "" && springDynamicValue(value.value) {
			result.health.Kind = "tcp"
			result.health.Path = ""
			result.diagnostics = append(result.diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "DYNAMIC_HEALTH_CONFIGURATION", File: value.file,
				Message: "Spring Boot Actuator web exposure is dynamic; TCP readiness was kept",
			})
			return result, nil
		}
	}
	if !springHealthExposed(resolved) {
		file := buildFile
		if value := resolved["management.endpoints.web.exposure.include"]; value.file != "" {
			file = value.file
		}
		if value := resolved["management.endpoints.web.exposure.exclude"]; value.file != "" {
			file = value.file
		}
		result.health.Kind = "tcp"
		result.health.Path = ""
		result.diagnostics = append(result.diagnostics, spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "HEALTH_ENDPOINT_NOT_EXPOSED", File: file,
			Message: "Spring Boot Actuator health is excluded from web exposure; TCP readiness was kept",
		})
		return result, nil
	}

	contextPath := springProperty(resolved, "server.servlet.context-path", "")
	webfluxPath := springProperty(resolved, "spring.webflux.base-path", "")
	if contextPath != "" && webfluxPath != "" && contextPath != webfluxPath {
		result.health.Kind = "tcp"
		result.health.Path = ""
		result.diagnostics = append(result.diagnostics, spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "AMBIGUOUS_HEALTH_ENDPOINT", File: resolved["spring.webflux.base-path"].file,
			Message: "both servlet and WebFlux base paths were configured differently; TCP readiness was kept",
		})
		return result, nil
	}
	if contextPath == "" {
		contextPath = webfluxPath
	}
	basePath := springProperty(resolved, "management.endpoints.web.base-path", "/actuator")
	healthPath := springProperty(resolved, "management.endpoints.web.path-mapping.health", "health")
	if value := resolved["management.endpoints.web.path-mapping.health"]; value.file != "" && strings.TrimSpace(healthPath) == "" {
		result.health.Kind = "tcp"
		result.health.Path = ""
		result.diagnostics = append(result.diagnostics, spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "DYNAMIC_HEALTH_ENDPOINT", File: value.file,
			Message: "Spring Boot health path mapping is empty; TCP readiness was kept",
		})
		return result, nil
	}
	endpoint := statichealth.JoinPath(contextPath, basePath, healthPath)
	if endpoint == "" {
		result.health.Kind = "tcp"
		result.health.Path = ""
		file := firstSpringPropertyFile(resolved, buildFile)
		result.diagnostics = append(result.diagnostics, spec.Diagnostic{
			Severity: spec.SeverityInfo, Code: "DYNAMIC_HEALTH_ENDPOINT", File: file,
			Message: "Spring Boot health routing contains a dynamic or malformed path; TCP readiness was kept",
		})
		return result, nil
	}
	result.health.Path = endpoint
	evidenceFile := firstSpringPropertyFile(resolved, buildFile)
	result.evidence = append(result.evidence, model.Evidence{
		File: evidenceFile, Explanation: fmt.Sprintf("Spring Boot Actuator readiness endpoint %s resolved", endpoint), Confidence: "high",
	})
	return result, nil
}

func loadSpringHealthProperties(ctx context.Context, workspace spec.Workspace, directory string) (map[string][]springPropertyValue, []spec.Diagnostic, error) {
	result := make(map[string][]springPropertyValue)
	var diagnostics []spec.Diagnostic
	for _, file := range workspace.Files() {
		base := path.Base(file)
		if base != "application.properties" && base != "application.yml" && base != "application.yaml" {
			continue
		}
		if !directoryContainsSpring(directory, path.Dir(file)) || nestedSpringModule(workspace, directory, path.Dir(file)) {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return nil, nil, err
		}
		values := make(map[string]string)
		if base == "application.properties" {
			values = parseSpringProperties(encoded)
		} else if err := parseSpringYAML(encoded, values); err != nil {
			diagnostics = append(diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "UNPARSEABLE_HEALTH_CONFIGURATION", File: file,
				Message: "Spring Boot configuration could not be parsed statically; TCP readiness was kept",
			})
			continue
		}
		indirect := false
		for _, key := range springConfigurationIndirection {
			if _, exists := values[key]; exists {
				diagnostics = append(diagnostics, spec.Diagnostic{
					Severity: spec.SeverityInfo, Code: "DYNAMIC_HEALTH_CONFIGURATION", File: file,
					Message: "Spring Boot configuration activates profiles or imports other configuration; TCP readiness was kept",
				})
				indirect = true
				break
			}
		}
		if indirect {
			continue
		}
		for _, key := range springHealthProperties {
			if value, exists := values[key]; exists {
				result[key] = append(result[key], springPropertyValue{value: strings.TrimSpace(value), file: file})
			}
		}
	}
	return result, diagnostics, nil
}

func directoryContainsSpring(directory, target string) bool {
	return directory == "." || target == directory || strings.HasPrefix(target, directory+"/")
}

func nestedSpringModule(workspace spec.Workspace, serviceDirectory, fileDirectory string) bool {
	for current := fileDirectory; current != serviceDirectory && current != "."; current = path.Dir(current) {
		for _, marker := range []string{"build.gradle", "build.gradle.kts", "pom.xml"} {
			if workspace.Exists(path.Join(current, marker)) {
				return true
			}
		}
	}
	return false
}

func parseSpringProperties(encoded []byte) map[string]string {
	result := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(encoded))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		separator := strings.IndexAny(line, "=:")
		if separator < 0 {
			for index, character := range line {
				if character == ' ' || character == '\t' {
					separator = index
					break
				}
			}
		}
		if separator <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		value := strings.Trim(strings.TrimSpace(line[separator+1:]), `"'`)
		result[key] = value
	}
	return result
}

func parseSpringYAML(encoded []byte, result map[string]string) error {
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	documents := 0
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(document) == 0 {
			continue
		}
		documents++
		if documents > 1 {
			return fmt.Errorf("multiple YAML documents cannot be resolved statically")
		}
		flattenSpringYAML("", document, result)
	}
}

func flattenSpringYAML(prefix string, values map[string]any, result map[string]string) {
	for key, value := range values {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}
		switch typed := value.(type) {
		case map[string]any:
			flattenSpringYAML(fullKey, typed, result)
		case []any:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				items = append(items, fmt.Sprint(item))
			}
			result[fullKey] = strings.Join(items, ",")
		case nil:
			result[fullKey] = ""
		default:
			result[fullKey] = fmt.Sprint(typed)
		}
	}
}

func resolveSpringProperty(values []springPropertyValue) (springPropertyValue, bool) {
	if len(values) == 0 {
		return springPropertyValue{}, false
	}
	sort.Slice(values, func(i, j int) bool { return values[i].file < values[j].file })
	selected := values[0]
	for _, value := range values[1:] {
		if value.value != selected.value {
			return springPropertyValue{}, true
		}
	}
	return selected, false
}

func sameSpringManagementPort(value string) bool {
	value = strings.TrimSpace(value)
	return value == "${SERVER_PORT}" || value == "${server.port}" || value == "${SERVER_PORT:${server.port}}"
}

func springHealthExposed(values map[string]springPropertyValue) bool {
	if excluded := values["management.endpoints.web.exposure.exclude"]; excluded.file != "" && springListContains(excluded.value, "health") {
		return false
	}
	if included := values["management.endpoints.web.exposure.include"]; included.file != "" {
		return springListContains(included.value, "health")
	}
	return true
}

func springListContains(value, endpoint string) bool {
	for _, item := range strings.Split(value, ",") {
		item = strings.Trim(strings.TrimSpace(item), `"'`)
		if item == "*" || strings.EqualFold(item, endpoint) {
			return true
		}
	}
	return false
}

func springDynamicValue(value string) bool {
	return strings.Contains(value, "${") || strings.Contains(value, "#{")
}

func springProperty(values map[string]springPropertyValue, key, fallback string) string {
	if value := values[key]; value.file != "" {
		return value.value
	}
	return fallback
}

func firstSpringPropertyFile(values map[string]springPropertyValue, fallback string) string {
	files := make([]string, 0, len(values))
	for _, value := range values {
		if value.file != "" {
			files = append(files, value.file)
		}
	}
	if len(files) == 0 {
		return fallback
	}
	sort.Strings(files)
	return files[0]
}
