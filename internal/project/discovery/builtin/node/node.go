// Package node contains the shared Node workspace adapter and framework
// recognizers. Each recognizer is independently registered with the engine,
// while manifest parsing and package-manager selection stay consistent.
package node

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery/spec"
)

type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type packageManifest struct {
	file      string
	directory string
	manifest  packageJSON
}

type detector struct {
	descriptor        spec.Descriptor
	dependencies      []string
	scripts           []string
	scriptRecognition func(string) bool
	explanation       string
}

func NestJS() spec.ServiceDetector {
	return detector{
		descriptor:   spec.Descriptor{ID: "nestjs", RootMarkers: []string{"package.json"}, Supersedes: []string{"express", "fastify"}, PrimaryOrder: 100},
		dependencies: []string{"@nestjs/core"}, scripts: []string{"start:dev", "dev", "start"},
		scriptRecognition: func(command string) bool {
			command = strings.ToLower(strings.TrimSpace(command))
			return command == "nest" || strings.HasPrefix(command, "nest ")
		},
		explanation: "NestJS dependency and runnable script found",
	}
}

func Express() spec.ServiceDetector {
	return detector{
		descriptor:   spec.Descriptor{ID: "express", RootMarkers: []string{"package.json"}, PrimaryOrder: 80},
		dependencies: []string{"express"}, scripts: []string{"dev", "start", "serve"},
		explanation: "Express dependency and runnable script found",
	}
}

func Fastify() spec.ServiceDetector {
	return detector{
		descriptor:   spec.Descriptor{ID: "fastify", RootMarkers: []string{"package.json"}, PrimaryOrder: 80},
		dependencies: []string{"fastify"}, scripts: []string{"dev", "start", "serve"},
		explanation: "Fastify dependency and runnable script found",
	}
}

func NextJS() spec.ServiceDetector {
	return detector{
		descriptor:   spec.Descriptor{ID: "nextjs", RootMarkers: []string{"package.json"}, Supersedes: []string{"express", "fastify"}, PrimaryOrder: 90},
		dependencies: []string{"next"}, scripts: []string{"dev", "start"},
		explanation: "Next.js dependency and runnable script found",
	}
}

func (d detector) Descriptor() spec.Descriptor {
	return d.descriptor
}

func (d detector) Detect(ctx context.Context, workspace spec.Workspace) (spec.Findings, error) {
	packages, err := loadPackages(ctx, workspace)
	if err != nil {
		return spec.Findings{}, err
	}
	var result spec.Findings
	for _, current := range packages {
		if err := ctx.Err(); err != nil {
			return spec.Findings{}, err
		}
		if !d.matches(current.manifest) {
			continue
		}
		script := selectScript(current.manifest.Scripts, d.scripts)
		if script == "" {
			result.Diagnostics = append(result.Diagnostics, spec.Diagnostic{
				Severity: spec.SeverityWarning, Code: "NO_RUN_SCRIPT", File: current.file,
				Message: fmt.Sprintf("%s was detected but no supported development script was found", d.descriptor.ID),
			})
			continue
		}
		nameSource := packageName(current.manifest.Name)
		if nameSource == "" {
			if current.directory == "." {
				nameSource = filepath.Base(workspace.Root())
			} else {
				nameSource = path.Base(current.directory)
			}
		}
		name := spec.ServiceName(nameSource)
		if name == "" {
			return spec.Findings{}, fmt.Errorf("package %s does not produce a valid service name", current.file)
		}
		manager := packageManager(workspace, current.directory)
		result.Candidates = append(result.Candidates, spec.Candidate{
			Key: current.directory, Directory: current.directory, RunDirectory: current.directory,
			Definition: model.ServiceDefinition{
				Name: name, Kind: model.ServiceProcess, Framework: d.descriptor.ID,
				Command: []string{manager, "run", script}, PortEnvironment: "PORT", Required: true,
				Health:   model.HealthCheck{Kind: "tcp", Timeout: 90 * time.Second, Interval: time.Second},
				Evidence: []model.Evidence{{File: current.file, Explanation: d.explanation, Confidence: "high"}},
			},
		})
	}
	return result, nil
}

func (d detector) matches(manifest packageJSON) bool {
	for _, dependency := range d.dependencies {
		if _, ok := manifest.Dependencies[dependency]; ok {
			return true
		}
		if _, ok := manifest.DevDependencies[dependency]; ok {
			return true
		}
	}
	if d.scriptRecognition != nil {
		for _, command := range manifest.Scripts {
			if d.scriptRecognition(command) {
				return true
			}
		}
	}
	return false
}

func loadPackages(ctx context.Context, workspace spec.Workspace) ([]packageManifest, error) {
	var result []packageManifest
	for _, file := range workspace.Files() {
		if path.Base(file) != "package.json" {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		var manifest packageJSON
		if err := json.Unmarshal(encoded, &manifest); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		directory := path.Dir(file)
		if directory == "" {
			directory = "."
		}
		result = append(result, packageManifest{file: file, directory: directory, manifest: manifest})
	}
	return result, nil
}

func selectScript(scripts map[string]string, candidates []string) string {
	for _, candidate := range candidates {
		if command, ok := scripts[candidate]; ok && strings.TrimSpace(command) != "" {
			return candidate
		}
	}
	return ""
}

func packageName(value string) string {
	value = strings.TrimPrefix(value, "@")
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	return value
}

func packageManager(workspace spec.Workspace, directory string) string {
	for current := directory; ; current = path.Dir(current) {
		for _, candidate := range []struct {
			file    string
			manager string
		}{
			{file: "pnpm-lock.yaml", manager: "pnpm"},
			{file: "yarn.lock", manager: "yarn"},
			{file: "bun.lock", manager: "bun"},
			{file: "bun.lockb", manager: "bun"},
			{file: "package-lock.json", manager: "npm"},
		} {
			file := candidate.file
			if current != "." {
				file = path.Join(current, file)
			}
			if workspace.Exists(file) {
				return candidate.manager
			}
		}
		if current == "." {
			break
		}
	}
	return "npm"
}
