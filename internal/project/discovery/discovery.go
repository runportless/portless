package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
)

var environmentURLPattern = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*)_URL\b`)

type Result struct {
	Root     string             `json:"root"`
	Model    model.ProjectModel `json:"model"`
	Warnings []string           `json:"warnings,omitempty"`
}

func FindRoot(start string) (string, error) {
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		absolute = filepath.Dir(absolute)
	}
	for current := absolute; ; current = filepath.Dir(current) {
		if isRootMarker(current) {
			resolved, err := filepath.EvalSymlinks(current)
			if err == nil {
				return resolved, nil
			}
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return "", errors.New("no project root found; expected .git, settings.gradle, settings.gradle.kts, or package.json")
}

func Discover(start string) (Result, error) {
	root, err := FindRoot(start)
	if err != nil {
		return Result{}, err
	}
	result := Result{Root: root}
	result.Model.SuggestedName = model.NormalizeDNSName(filepath.Base(root))
	if result.Model.SuggestedName == "" {
		result.Model.SuggestedName = "project"
	}
	files, err := relevantFiles(root)
	if err != nil {
		return Result{}, err
	}
	services := discoverGradle(root, files)
	services = append(services, discoverNode(root, files)...)
	services = uniqueServices(services, &result.Warnings)
	if len(services) == 0 {
		return Result{}, errors.New("no supported Spring Boot or NestJS services were discovered")
	}
	serviceNames := make(map[string]struct{}, len(services))
	for _, service := range services {
		serviceNames[service.Name] = struct{}{}
		if result.Model.PrimaryService == "" && service.Framework == "nestjs" {
			result.Model.PrimaryService = service.Name
		}
	}
	if result.Model.PrimaryService == "" {
		result.Model.PrimaryService = services[0].Name
	}
	needsPostgres, needsRedis, bindings := discoverDependencies(root, files, services, serviceNames)
	if needsPostgres {
		services = append(services, model.ServiceDefinition{
			Name: "postgres", Kind: model.ServiceContainer, Template: "postgres", Version: "17", Required: true,
			Health:   model.HealthCheck{Kind: "exec", Timeout: 2 * time.Minute, Interval: time.Second},
			Evidence: []model.Evidence{{File: "project configuration", Explanation: "datasource or PostgreSQL configuration key found", Confidence: "medium"}},
		})
	}
	if needsRedis {
		services = append(services, model.ServiceDefinition{
			Name: "redis", Kind: model.ServiceContainer, Template: "valkey", Version: "8", Required: true,
			Health:   model.HealthCheck{Kind: "exec", Timeout: time.Minute, Interval: time.Second},
			Evidence: []model.Evidence{{File: "project configuration", Explanation: "Redis-compatible configuration key found; Valkey proposed", Confidence: "medium"}},
		})
	}
	result.Model.Services = services
	result.Model.Connections = uniqueConnections(bindings)
	if err := Validate(result.Model); err != nil {
		return Result{}, err
	}
	return result, nil
}

func Validate(definition model.ProjectModel) error {
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
		if service.Kind == model.ServiceProcess && len(service.Command) == 0 {
			return fmt.Errorf("process service %q has no command", service.Name)
		}
		services[key] = service
	}
	if _, ok := services[strings.ToLower(definition.PrimaryService)]; !ok {
		return fmt.Errorf("primary service %q does not exist", definition.PrimaryService)
	}
	edges := make(map[string]struct{}, len(definition.Connections))
	for _, connection := range definition.Connections {
		if connection.Source != "external" {
			if _, ok := services[strings.ToLower(connection.Source)]; !ok {
				return fmt.Errorf("connection source %q does not exist", connection.Source)
			}
		}
		if _, ok := services[strings.ToLower(connection.Target)]; !ok {
			return fmt.Errorf("connection target %q does not exist", connection.Target)
		}
		key := strings.ToLower(connection.Source + "\x00" + connection.Target)
		if _, duplicate := edges[key]; duplicate {
			return fmt.Errorf("duplicate connection %s to %s", connection.Source, connection.Target)
		}
		edges[key] = struct{}{}
	}
	return nil
}

func isRootMarker(path string) bool {
	for _, marker := range []string{".git", "settings.gradle", "settings.gradle.kts", "package.json"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func relevantFiles(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".gradle", "node_modules", "build", "dist", "target", ".idea", ".next", "coverage":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := entry.Name()
		if name == "build.gradle" || name == "build.gradle.kts" || name == "package.json" ||
			strings.HasPrefix(name, "application") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".properties")) ||
			strings.HasPrefix(name, ".env") || strings.HasSuffix(name, ".example") {
			result = append(result, path)
		}
		return nil
	})
	sort.Strings(result)
	return result, err
}

func discoverGradle(root string, files []string) []model.ServiceDefinition {
	var result []model.ServiceDefinition
	gradleWrapper := filepath.Join(root, "gradlew")
	executable := "gradle"
	if info, err := os.Stat(gradleWrapper); err == nil && !info.IsDir() {
		executable = "./gradlew"
	}
	for _, path := range files {
		if filepath.Base(path) != "build.gradle" && filepath.Base(path) != "build.gradle.kts" {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(strings.ToLower(string(content)), "org.springframework.boot") {
			continue
		}
		directory := filepath.Dir(path)
		relative, _ := filepath.Rel(root, directory)
		nameSource := filepath.Base(directory)
		if relative == "." {
			nameSource = filepath.Base(root)
		}
		name := serviceName(nameSource)
		command := []string{executable, "bootRun"}
		if relative != "." {
			module := strings.ReplaceAll(filepath.ToSlash(relative), "/", ":")
			command = []string{executable, ":" + module + ":bootRun"}
		}
		healthKind := "tcp"
		if strings.Contains(strings.ToLower(string(content)), "actuator") {
			healthKind = "http"
		}
		relativeFile, _ := filepath.Rel(root, path)
		result = append(result, model.ServiceDefinition{
			Name: name, Kind: model.ServiceProcess, Framework: "spring-boot", Command: command,
			WorkingDirectory: root, PortEnvironment: "SERVER_PORT", Required: true,
			Health:   model.HealthCheck{Kind: healthKind, Path: healthPath(healthKind), Timeout: 2 * time.Minute, Interval: time.Second},
			Evidence: []model.Evidence{{File: filepath.ToSlash(relativeFile), Explanation: "Spring Boot Gradle plugin found", Confidence: "high"}},
		})
	}
	return result
}

type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func discoverNode(root string, files []string) []model.ServiceDefinition {
	manager := nodeManager(root)
	var result []model.ServiceDefinition
	for _, path := range files {
		if filepath.Base(path) != "package.json" {
			continue
		}
		encoded, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var manifest packageJSON
		if json.Unmarshal(encoded, &manifest) != nil {
			continue
		}
		_, nestDependency := manifest.Dependencies["@nestjs/core"]
		_, nestDevDependency := manifest.DevDependencies["@nestjs/core"]
		if !nestDependency && !nestDevDependency && !containsNestScript(manifest.Scripts) {
			continue
		}
		script := ""
		for _, candidate := range []string{"start:dev", "dev", "start"} {
			if _, ok := manifest.Scripts[candidate]; ok {
				script = candidate
				break
			}
		}
		if script == "" {
			continue
		}
		nameSource := strings.TrimPrefix(manifest.Name, "@")
		if slash := strings.LastIndex(nameSource, "/"); slash >= 0 {
			nameSource = nameSource[slash+1:]
		}
		if nameSource == "" {
			nameSource = filepath.Base(filepath.Dir(path))
		}
		relativeFile, _ := filepath.Rel(root, path)
		result = append(result, model.ServiceDefinition{
			Name: serviceName(nameSource), Kind: model.ServiceProcess, Framework: "nestjs",
			Command: []string{manager, "run", script}, WorkingDirectory: filepath.Dir(path),
			PortEnvironment: "PORT", Required: true,
			Health:   model.HealthCheck{Kind: "tcp", Timeout: 90 * time.Second, Interval: time.Second},
			Evidence: []model.Evidence{{File: filepath.ToSlash(relativeFile), Explanation: "NestJS dependency and runnable script found", Confidence: "high"}},
		})
	}
	return result
}

func discoverDependencies(root string, files []string, services []model.ServiceDefinition, names map[string]struct{}) (bool, bool, []model.Connection) {
	needsPostgres := false
	needsRedis := false
	var connections []model.Connection
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(content))
		postgres := strings.Contains(lower, "datasource") || strings.Contains(lower, "jdbc:postgresql") || strings.Contains(lower, "postgres")
		redis := strings.Contains(lower, "redis") || strings.Contains(lower, "valkey")
		service := owningService(root, path, services)
		if postgres {
			needsPostgres = true
			if service != "" {
				connections = append(connections, model.Connection{Source: service, Target: "postgres", Protocol: model.ProtocolPostgres, Environment: postgresBinding(services, service), Required: true})
			}
		}
		if redis {
			needsRedis = true
			if service != "" {
				connections = append(connections, model.Connection{Source: service, Target: "redis", Protocol: model.ProtocolRedis, Environment: redisBinding(services, service), Required: true})
			}
		}
		for _, match := range environmentURLPattern.FindAllStringSubmatch(string(content), -1) {
			target := model.NormalizeDNSName(strings.ToLower(match[1]))
			if _, ok := names[target]; ok && service != "" && service != target {
				connections = append(connections, model.Connection{Source: service, Target: target, Protocol: model.ProtocolHTTP, Environment: match[0], Required: true})
			}
		}
	}
	return needsPostgres, needsRedis, connections
}

func uniqueServices(input []model.ServiceDefinition, warnings *[]string) []model.ServiceDefinition {
	seen := make(map[string]model.ServiceDefinition)
	for _, service := range input {
		name := service.Name
		if previous, exists := seen[name]; exists {
			*warnings = append(*warnings, fmt.Sprintf("service name %s was discovered more than once (%s and %s)", name, previous.WorkingDirectory, service.WorkingDirectory))
			continue
		}
		seen[name] = service
	}
	result := make([]model.ServiceDefinition, 0, len(seen))
	for _, service := range seen {
		result = append(result, service)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func uniqueConnections(input []model.Connection) []model.Connection {
	seen := make(map[string]model.Connection)
	for _, connection := range input {
		key := connection.Source + "\x00" + connection.Target
		if existing, ok := seen[key]; ok {
			if existing.Environment == "" && connection.Environment != "" {
				seen[key] = connection
			}
			continue
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
	return result
}

func owningService(root, path string, services []model.ServiceDefinition) string {
	best := ""
	bestLength := -1
	for _, service := range services {
		directory := service.WorkingDirectory
		if service.Framework == "spring-boot" && len(service.Evidence) > 0 {
			directory = filepath.Dir(filepath.Join(root, filepath.FromSlash(service.Evidence[0].File)))
		}
		if directory == "" {
			continue
		}
		if path == directory || strings.HasPrefix(path, directory+string(os.PathSeparator)) {
			if len(directory) > bestLength {
				best = service.Name
				bestLength = len(directory)
			}
		}
	}
	if best == "" && strings.HasPrefix(path, root) && len(services) == 1 {
		return services[0].Name
	}
	return best
}

func nodeManager(root string) string {
	if _, err := os.Stat(filepath.Join(root, "pnpm-lock.yaml")); err == nil {
		return "pnpm"
	}
	if _, err := os.Stat(filepath.Join(root, "yarn.lock")); err == nil {
		return "yarn"
	}
	return "npm"
}

func containsNestScript(scripts map[string]string) bool {
	for _, value := range scripts {
		if strings.Contains(strings.ToLower(value), "nest ") || strings.HasPrefix(strings.ToLower(value), "nest") {
			return true
		}
	}
	return false
}

func serviceName(value string) string {
	value = strings.TrimSuffix(value, "-service")
	value = strings.TrimSuffix(value, "-api")
	return model.NormalizeDNSName(value)
}

func healthPath(kind string) string {
	if kind == "http" {
		return "/actuator/health"
	}
	return ""
}

func postgresBinding(services []model.ServiceDefinition, source string) string {
	if frameworkFor(services, source) == "spring-boot" {
		return "SPRING_DATASOURCE_URL"
	}
	return "DATABASE_URL"
}

func redisBinding(services []model.ServiceDefinition, source string) string {
	if frameworkFor(services, source) == "spring-boot" {
		return "SPRING_DATA_REDIS_URL"
	}
	return "REDIS_URL"
}

func frameworkFor(services []model.ServiceDefinition, name string) string {
	for _, service := range services {
		if service.Name == name {
			return service.Framework
		}
	}
	return ""
}
