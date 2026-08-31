package springboot

import (
	"context"
	"encoding/xml"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery/spec"
)

// Detector discovers Gradle and Maven Spring Boot applications.
type Detector struct{}

// New returns a Spring Boot service detector.
func New() spec.ServiceDetector {
	return Detector{}
}

// Descriptor returns Spring Boot detector registration metadata.
func (Detector) Descriptor() spec.Descriptor {
	return spec.Descriptor{
		ID: "spring-boot", RootMarkers: []string{"settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts", "pom.xml"},
		PrimaryOrder: 50,
	}
}

type pomProject struct {
	ArtifactID   string `xml:"artifactId"`
	Name         string `xml:"name"`
	Dependencies []struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"dependencies>dependency"`
	Plugins []struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"build>plugins>plugin"`
}

// Detect finds Spring Boot modules and their framework-specific run and debug commands.
func (Detector) Detect(ctx context.Context, workspace spec.Workspace) (spec.Findings, error) {
	result := spec.Findings{}
	seen := make(map[string]string)
	for _, file := range workspace.Files() {
		base := path.Base(file)
		if base != "build.gradle" && base != "build.gradle.kts" && base != "pom.xml" {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return spec.Findings{}, err
		}
		directory := path.Dir(file)
		if directory == "" {
			directory = "."
		}
		var candidate spec.Candidate
		var matched bool
		if base == "pom.xml" {
			candidate, matched, err = mavenCandidate(workspace, file, directory, encoded)
		} else {
			candidate, matched, err = gradleCandidate(workspace, file, directory, encoded)
		}
		if err != nil {
			return spec.Findings{}, err
		}
		if !matched {
			continue
		}
		if candidate.Definition.Health.Kind == "http" {
			healthDetection, healthErr := detectSpringHealth(ctx, workspace, directory, file, candidate.Definition.Health.Timeout)
			if healthErr != nil {
				return spec.Findings{}, healthErr
			}
			candidate.Definition.Health = healthDetection.health
			candidate.Definition.Evidence = append(candidate.Definition.Evidence, healthDetection.evidence...)
			result.Diagnostics = append(result.Diagnostics, healthDetection.diagnostics...)
		}
		if previous, duplicate := seen[directory]; duplicate {
			result.Diagnostics = append(result.Diagnostics, spec.Diagnostic{
				Severity: spec.SeverityWarning, Code: "MULTIPLE_BUILD_SYSTEMS", File: file,
				Message: fmt.Sprintf("Spring Boot service was already discovered from %s; %s was ignored", previous, file),
			})
			continue
		}
		seen[directory] = file
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, nil
}

func gradleCandidate(workspace spec.Workspace, file, directory string, encoded []byte) (spec.Candidate, bool, error) {
	lower := strings.ToLower(string(encoded))
	if !strings.Contains(lower, "org.springframework.boot") {
		return spec.Candidate{}, false, nil
	}
	buildRoot, executable := gradleRoot(workspace, directory)
	task := "bootRun"
	if relative := relativeDirectory(buildRoot, directory); relative != "." {
		task = ":" + strings.ReplaceAll(relative, "/", ":") + ":bootRun"
	}
	nameSource := path.Base(directory)
	if directory == "." {
		nameSource = filepath.Base(workspace.Root())
	}
	health := model.HealthCheck{Kind: "tcp", Timeout: 2 * time.Minute, Interval: time.Second}
	if strings.Contains(lower, "actuator") {
		health.Kind = "http"
		health.Path = "/actuator/health"
	}
	return springCandidate(file, directory, buildRoot, spec.ServiceName(nameSource), []string{executable, task}, model.DebugSpringGradle, health, "Spring Boot Gradle plugin found"), true, nil
}

func mavenCandidate(workspace spec.Workspace, file, directory string, encoded []byte) (spec.Candidate, bool, error) {
	var project pomProject
	if err := xml.Unmarshal(encoded, &project); err != nil {
		return spec.Candidate{}, false, fmt.Errorf("parse %s: %w", file, err)
	}
	matched := false
	actuator := false
	for _, dependency := range project.Dependencies {
		artifact := strings.ToLower(dependency.ArtifactID)
		if strings.HasPrefix(artifact, "spring-boot-starter-") || artifact == "spring-boot" {
			matched = true
		}
		if artifact == "spring-boot-starter-actuator" {
			actuator = true
		}
	}
	for _, plugin := range project.Plugins {
		if strings.EqualFold(plugin.ArtifactID, "spring-boot-maven-plugin") {
			matched = true
		}
	}
	if !matched {
		return spec.Candidate{}, false, nil
	}
	buildRoot, executable := mavenRoot(workspace, directory)
	command := []string{executable}
	if relative := relativeDirectory(buildRoot, directory); relative != "." {
		command = append(command, "-pl", relative)
	}
	command = append(command, "spring-boot:run")
	nameSource := project.ArtifactID
	if nameSource == "" {
		nameSource = project.Name
	}
	if nameSource == "" {
		if directory == "." {
			nameSource = filepath.Base(workspace.Root())
		} else {
			nameSource = path.Base(directory)
		}
	}
	health := model.HealthCheck{Kind: "tcp", Timeout: 2 * time.Minute, Interval: time.Second}
	if actuator {
		health.Kind = "http"
		health.Path = "/actuator/health"
	}
	return springCandidate(file, directory, buildRoot, spec.ServiceName(nameSource), command, model.DebugSpringMaven, health, "Spring Boot Maven configuration found"), true, nil
}

func springCandidate(file, directory, runDirectory, name string, command []string, launcher model.DebugLauncher, health model.HealthCheck, explanation string) spec.Candidate {
	return spec.Candidate{
		Key: directory, Directory: directory, RunDirectory: runDirectory,
		Definition: model.ServiceDefinition{
			Name: name, Kind: model.ServiceProcess, Framework: "spring-boot", Command: command,
			Debug:           &model.DebugCapability{Adapter: model.DebugJDWP, Launcher: launcher, Command: append([]string{}, command...)},
			PortEnvironment: "SERVER_PORT", Required: true, Health: health,
			Evidence: []model.Evidence{{File: file, Explanation: explanation, Confidence: "high"}},
		},
	}
}

func gradleRoot(workspace spec.Workspace, directory string) (string, string) {
	if root, ok := nearestAncestorWith(workspace, directory, "gradlew"); ok {
		return root, "./gradlew"
	}
	if root, ok := nearestAncestorWithEither(workspace, directory, "settings.gradle", "settings.gradle.kts"); ok {
		return root, "gradle"
	}
	return directory, "gradle"
}

func mavenRoot(workspace spec.Workspace, directory string) (string, string) {
	if root, ok := nearestAncestorWith(workspace, directory, "mvnw"); ok {
		return root, "./mvnw"
	}
	if workspace.Exists("pom.xml") {
		return ".", "mvn"
	}
	return directory, "mvn"
}

func nearestAncestorWith(workspace spec.Workspace, directory string, file string) (string, bool) {
	return nearestAncestorWithEither(workspace, directory, file)
}

func nearestAncestorWithEither(workspace spec.Workspace, directory string, files ...string) (string, bool) {
	for current := directory; ; current = path.Dir(current) {
		for _, file := range files {
			candidate := file
			if current != "." {
				candidate = path.Join(current, file)
			}
			if workspace.Exists(candidate) {
				return current, true
			}
		}
		if current == "." {
			break
		}
	}
	return "", false
}

func relativeDirectory(base, target string) string {
	if base == target {
		return "."
	}
	if base == "." {
		return target
	}
	return strings.TrimPrefix(target, base+"/")
}
