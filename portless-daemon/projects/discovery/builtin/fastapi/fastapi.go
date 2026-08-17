package fastapi

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/projects/discovery/spec"
)

var appPattern = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:[A-Za-z_][A-Za-z0-9_]*\.)?FastAPI\s*\(`)

type Detector struct{}

func New() spec.ServiceDetector {
	return Detector{}
}

func (Detector) Descriptor() spec.Descriptor {
	return spec.Descriptor{ID: "fastapi", RootMarkers: []string{"pyproject.toml", "requirements.txt", "uv.lock"}, PrimaryOrder: 60}
}

type entrypoint struct {
	file        string
	directory   string
	projectRoot string
	app         string
}

func (Detector) Detect(ctx context.Context, workspace spec.Workspace) (spec.Findings, error) {
	projectRoots, err := fastAPIProjects(ctx, workspace)
	if err != nil {
		return spec.Findings{}, err
	}
	if len(projectRoots) == 0 {
		return spec.Findings{}, nil
	}
	entries := make(map[string]entrypoint)
	for _, file := range workspace.Files() {
		if !strings.HasSuffix(file, ".py") || ignoredPythonFile(file) {
			continue
		}
		root, ok := nearestProjectRoot(projectRoots, path.Dir(file))
		if !ok {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return spec.Findings{}, err
		}
		match := appPattern.FindSubmatch(encoded)
		if len(match) == 0 {
			continue
		}
		if previous, duplicate := entries[root]; duplicate {
			return spec.Findings{}, fmt.Errorf("FastAPI project %s has multiple statically discoverable entrypoints (%s and %s)", root, previous.file, file)
		}
		entries[root] = entrypoint{file: file, directory: path.Dir(file), projectRoot: root, app: string(match[1])}
	}
	var result spec.Findings
	for _, entry := range entries {
		module, appDirectory := pythonModule(entry.projectRoot, entry.file)
		command := pythonCommand(workspace, entry.projectRoot, module+":"+entry.app, appDirectory)
		nameSource := path.Base(entry.projectRoot)
		if entry.projectRoot == "." {
			nameSource = filepath.Base(workspace.Root())
		}
		name := spec.ServiceName(nameSource)
		if name == "" {
			return spec.Findings{}, fmt.Errorf("FastAPI project %s does not produce a valid service name", entry.projectRoot)
		}
		result.Candidates = append(result.Candidates, spec.Candidate{
			Key: entry.projectRoot, Directory: entry.projectRoot, RunDirectory: entry.projectRoot,
			Definition: model.ServiceDefinition{
				Name: name, Kind: model.ServiceProcess, Framework: "fastapi", Command: command,
				PortEnvironment: "UVICORN_PORT", Required: true,
				Health:   model.HealthCheck{Kind: "tcp", Timeout: 90 * time.Second, Interval: time.Second},
				Evidence: []model.Evidence{{File: entry.file, Explanation: "FastAPI application entrypoint found", Confidence: "high"}},
			},
		})
	}
	return result, nil
}

func fastAPIProjects(ctx context.Context, workspace spec.Workspace) ([]string, error) {
	seen := make(map[string]bool)
	for _, file := range workspace.Files() {
		base := path.Base(file)
		if base != "pyproject.toml" && !(strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt")) {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(string(encoded))
		if strings.Contains(lower, "fastapi") {
			seen[path.Dir(file)] = true
		}
	}
	result := make([]string, 0, len(seen))
	for root := range seen {
		result = append(result, root)
	}
	return result, nil
}

func nearestProjectRoot(roots []string, directory string) (string, bool) {
	best := ""
	for _, root := range roots {
		if root == "." || directory == root || strings.HasPrefix(directory, root+"/") {
			if best == "" || len(root) > len(best) {
				best = root
			}
		}
	}
	return best, best != ""
}

func ignoredPythonFile(file string) bool {
	base := path.Base(file)
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return true
	}
	for _, component := range strings.Split(file, "/") {
		if component == "test" || component == "tests" {
			return true
		}
	}
	return false
}

func pythonModule(projectRoot, file string) (string, string) {
	relative := strings.TrimSuffix(relativeDirectory(projectRoot, file), ".py")
	appDirectory := ""
	if strings.HasPrefix(relative, "src/") {
		relative = strings.TrimPrefix(relative, "src/")
		appDirectory = "src"
	}
	return strings.ReplaceAll(relative, "/", "."), appDirectory
}

func pythonCommand(workspace spec.Workspace, projectRoot, target, appDirectory string) []string {
	prefix := []string{"python", "-m", "uvicorn"}
	if existsAt(workspace, projectRoot, "uv.lock") {
		prefix = []string{"uv", "run", "uvicorn"}
	} else if existsAt(workspace, projectRoot, "poetry.lock") {
		prefix = []string{"poetry", "run", "uvicorn"}
	}
	result := append(prefix, target)
	if appDirectory != "" {
		result = append(result, "--app-dir", appDirectory)
	}
	return result
}

func existsAt(workspace spec.Workspace, directory, file string) bool {
	if directory != "." {
		file = path.Join(directory, file)
	}
	return workspace.Exists(file)
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
