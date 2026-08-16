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
		debug := nodeDebugCapability(current.manifest, manager, script)
		if debug == nil {
			result.Diagnostics = append(result.Diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "DEBUG_UNAVAILABLE", File: current.file,
				Message: fmt.Sprintf("%s can run normally, but its package scripts do not expose a safe Node or NestJS debug command", name),
			})
		}
		result.Candidates = append(result.Candidates, spec.Candidate{
			Key: current.directory, Directory: current.directory, RunDirectory: current.directory,
			Definition: model.ServiceDefinition{
				Name: name, Kind: model.ServiceProcess, Framework: d.descriptor.ID, Debug: debug,
				Command: []string{manager, "run", script}, PortEnvironment: "PORT", Required: true,
				Health:   model.HealthCheck{Kind: "tcp", Timeout: 90 * time.Second, Interval: time.Second},
				Evidence: []model.Evidence{{File: current.file, Explanation: d.explanation, Confidence: "high"}},
			},
		})
	}
	return result, nil
}

func nodeDebugCapability(manifest packageJSON, manager, managedScript string) *model.DebugCapability {
	candidates := []string{"start:debug", managedScript}
	seen := make(map[string]struct{}, len(candidates))
	for _, script := range candidates {
		if script == "" {
			continue
		}
		if _, duplicate := seen[script]; duplicate {
			continue
		}
		seen[script] = struct{}{}
		command, ok := tokenizeSimpleScript(manifest.Scripts[script])
		if !ok || len(command) == 0 {
			continue
		}
		switch path.Base(command[0]) {
		case "node":
			return &model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNodeDirect, Command: command}
		case "nest":
			if len(command) < 2 || command[1] != "start" {
				continue
			}
			command = stripNestDebugAddress(command)
			return &model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNestCLI, Command: packageExecCommand(manager, command)}
		}
	}
	return nil
}

func packageExecCommand(manager string, command []string) []string {
	var prefix []string
	switch manager {
	case "npm":
		prefix = []string{manager, "exec", "--"}
	case "bun":
		prefix = []string{manager, "x"}
	default:
		prefix = []string{manager, "exec"}
	}
	return append(prefix, command...)
}

func stripNestDebugAddress(command []string) []string {
	result := make([]string, 0, len(command))
	for index := 0; index < len(command); index++ {
		argument := command[index]
		if strings.HasPrefix(argument, "--debug=") {
			continue
		}
		if argument == "--debug" || argument == "-d" {
			if index+1 < len(command) && debugAddressArgument(command[index+1]) {
				index++
			}
			continue
		}
		result = append(result, argument)
	}
	return result
}

func debugAddressArgument(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	for _, character := range value {
		if character != ':' && character != '.' && (character < '0' || character > '9') &&
			(character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '-' {
			return false
		}
	}
	return true
}

// tokenizeSimpleScript accepts the conventional single-command scripts that
// Portless can safely execute without a shell. Pipelines, redirections,
// boolean operators, subshells, and leading environment assignments are
// intentionally rejected instead of being guessed at.
func tokenizeSimpleScript(value string) ([]string, bool) {
	var result []string
	var current strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			result = append(result, current.String())
			current.Reset()
			started = false
		}
	}
	for _, character := range value {
		if escaped {
			current.WriteRune(character)
			started, escaped = true, false
			continue
		}
		if quote != '\'' && character == '\\' {
			escaped, started = true, true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				started = true
			} else {
				current.WriteRune(character)
				started = true
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote, started = character, true
		case ' ', '\t':
			flush()
		case '|', '&', ';', '<', '>', '(', ')', '\r', '\n':
			return nil, false
		default:
			current.WriteRune(character)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, false
	}
	flush()
	if len(result) == 0 || strings.Contains(result[0], "=") {
		return nil, false
	}
	return result, true
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
