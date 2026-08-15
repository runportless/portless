package golang

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery/spec"
)

type Detector struct{}

func New() spec.ServiceDetector {
	return Detector{}
}

func (Detector) Descriptor() spec.Descriptor {
	return spec.Descriptor{ID: "go", RootMarkers: []string{"go.mod", "go.work"}, PrimaryOrder: 70}
}

type module struct {
	directory string
	path      string
	content   string
}

type mainPackage struct {
	directory   string
	module      module
	evidence    string
	signal      string
	dynamicPort bool
}

var serverImports = map[string]string{
	"github.com/gin-gonic/gin":    "Gin server import",
	"github.com/go-chi/chi":       "Chi server import",
	"github.com/gofiber/fiber":    "Fiber server import",
	"github.com/gofiber/fiber/v2": "Fiber server import",
	"github.com/labstack/echo":    "Echo server import",
	"github.com/labstack/echo/v4": "Echo server import",
}

var moduleServerSignals = []string{
	"github.com/gin-gonic/gin", "github.com/go-chi/chi", "github.com/gofiber/fiber", "github.com/labstack/echo",
}

var portEnvironmentPattern = regexp.MustCompile("(?:Getenv|LookupEnv)\\s*\\(\\s*(?:\"PORT\"|`PORT`)")

func (Detector) Detect(ctx context.Context, workspace spec.Workspace) (spec.Findings, error) {
	modules, err := loadModules(ctx, workspace)
	if err != nil {
		return spec.Findings{}, err
	}
	if len(modules) == 0 {
		return spec.Findings{}, nil
	}
	packages := make(map[string]*mainPackage)
	moduleDynamicPort := make(map[string]bool)
	for _, file := range workspace.Files() {
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		directory := path.Dir(file)
		currentModule, ok := owningModule(modules, directory)
		if !ok {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return spec.Findings{}, err
		}
		if hasDynamicPort(encoded) {
			moduleDynamicPort[currentModule.directory] = true
		}
		clause, err := parser.ParseFile(token.NewFileSet(), file, encoded, parser.PackageClauseOnly)
		if err != nil {
			return spec.Findings{}, fmt.Errorf("parse Go package clause in %s: %w", file, err)
		}
		if clause.Name.Name != "main" {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, encoded, parser.ImportsOnly)
		if err != nil {
			return spec.Findings{}, fmt.Errorf("parse Go service entrypoint %s: %w", file, err)
		}
		current := packages[directory]
		if current == nil {
			current = &mainPackage{directory: directory, module: currentModule, evidence: file}
			packages[directory] = current
		}
		if current.signal == "" {
			current.signal = serverSignal(parsed, encoded)
		}
		if hasDynamicPort(encoded) {
			current.dynamicPort = true
		}
	}

	moduleMainCounts := make(map[string]int)
	for _, current := range packages {
		moduleMainCounts[current.module.directory]++
	}
	var result spec.Findings
	for _, current := range packages {
		if current.signal == "" && moduleMainCounts[current.module.directory] == 1 {
			current.signal = moduleDependencySignal(current.module.content)
		}
		if current.signal == "" {
			result.Diagnostics = append(result.Diagnostics, spec.Diagnostic{
				Severity: spec.SeverityInfo, Code: "NON_SERVER_MAIN", File: current.evidence,
				Message: "Go main package has no static HTTP or RPC server evidence and was ignored",
			})
			continue
		}
		if !current.dynamicPort && !(moduleMainCounts[current.module.directory] == 1 && moduleDynamicPort[current.module.directory]) {
			result.Diagnostics = append(result.Diagnostics, spec.Diagnostic{
				Severity: spec.SeverityWarning, Code: "NO_DYNAMIC_PORT", File: current.evidence,
				Message: "Go server does not read the PORT environment variable and cannot be assigned a safe Portless port",
			})
			continue
		}
		nameSource := path.Base(current.directory)
		if current.directory == current.module.directory {
			nameSource = moduleName(current.module.path)
		}
		name := spec.ServiceName(nameSource)
		if name == "" {
			return spec.Findings{}, fmt.Errorf("Go package %s does not produce a valid service name", current.directory)
		}
		relativePackage := relativeDirectory(current.module.directory, current.directory)
		commandTarget := "."
		if relativePackage != "." {
			commandTarget = "./" + relativePackage
		}
		result.Candidates = append(result.Candidates, spec.Candidate{
			Key: current.directory, Directory: current.directory, RunDirectory: current.module.directory,
			Definition: model.ServiceDefinition{
				Name: name, Kind: model.ServiceProcess, Framework: "go", Command: []string{"go", "run", commandTarget},
				PortEnvironment: "PORT", Required: true,
				Health:   model.HealthCheck{Kind: "tcp", Timeout: 90 * time.Second, Interval: time.Second},
				Evidence: []model.Evidence{{File: current.evidence, Explanation: current.signal, Confidence: "high"}},
			},
		})
	}
	return result, nil
}

func loadModules(ctx context.Context, workspace spec.Workspace) ([]module, error) {
	var result []module
	for _, file := range workspace.Files() {
		if path.Base(file) != "go.mod" {
			continue
		}
		encoded, err := workspace.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		modulePath := ""
		scanner := bufio.NewScanner(bytes.NewReader(encoded))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) == 2 && fields[0] == "module" {
				modulePath = strings.Trim(fields[1], `"`)
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("parse %s: %w", file, err)
		}
		if modulePath == "" {
			return nil, fmt.Errorf("parse %s: module directive not found", file)
		}
		result = append(result, module{directory: path.Dir(file), path: modulePath, content: strings.ToLower(string(encoded))})
	}
	return result, nil
}

func owningModule(modules []module, directory string) (module, bool) {
	bestLength := -1
	var best module
	for _, candidate := range modules {
		if directory != candidate.directory && candidate.directory != "." && !strings.HasPrefix(directory, candidate.directory+"/") {
			continue
		}
		if candidate.directory == "." || directory == candidate.directory || strings.HasPrefix(directory, candidate.directory+"/") {
			if len(candidate.directory) > bestLength {
				best = candidate
				bestLength = len(candidate.directory)
			}
		}
	}
	return best, bestLength >= 0
}

func serverSignal(file *ast.File, encoded []byte) string {
	lower := strings.ToLower(string(encoded))
	for _, signal := range []struct {
		call        string
		explanation string
	}{
		{call: "listenandservetls(", explanation: "net/http TLS server listener found"},
		{call: "listenandserve(", explanation: "net/http server listener found"},
		{call: "grpc.newserver(", explanation: "gRPC server construction found"},
		{call: "http.serve(", explanation: "net/http server listener found"},
	} {
		if strings.Contains(lower, signal.call) {
			return signal.explanation
		}
	}
	for _, imported := range file.Imports {
		value, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			continue
		}
		if signal := serverImports[value]; signal != "" {
			return signal
		}
	}
	return ""
}

func moduleDependencySignal(content string) string {
	for _, dependency := range moduleServerSignals {
		if strings.Contains(content, dependency) {
			return "server dependency found in Go module"
		}
	}
	return ""
}

func moduleName(modulePath string) string {
	name := path.Base(modulePath)
	if len(name) > 1 && name[0] == 'v' {
		if _, err := strconv.Atoi(name[1:]); err == nil {
			return path.Base(path.Dir(modulePath))
		}
	}
	return name
}

func hasDynamicPort(encoded []byte) bool {
	return portEnvironmentPattern.Match(encoded)
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
