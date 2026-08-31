package examples_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/compiler"
	"github.com/runportless/portless/portless-daemon/projects/discovery"
	"go.yaml.in/yaml/v3"
)

func TestDispatchExampleCompilesThreeCheckoutsIntoOneProject(t *testing.T) {
	root := repositoryRoot(t)
	templates := filepath.Join(root, "examples", "dispatch", "templates")
	engine, err := discovery.NewDefault(discovery.Config{ScanTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	var bindings []model.SourceBinding
	for _, source := range []string{"console", "operations", "maps"} {
		path := filepath.Join(templates, source)
		result, err := engine.Discover(context.Background(), path)
		if err != nil {
			t.Fatalf("discover %s: %v", source, err)
		}
		bindings = append(bindings, model.SourceBinding{Name: source, Path: path, Definition: result.Model})
	}

	project, sources, defaults, err := compiler.InitialProject("dispatch", bindings)
	if err != nil {
		t.Fatalf("compile dispatch project: %v", err)
	}
	if project.PrimaryService != "console" {
		t.Fatalf("primary service = %q", project.PrimaryService)
	}
	if got := serviceNames(project.Services); strings.Join(got, ",") != "api,api-mysql,console,dispatch-nats,geocoder,notifier,routing" {
		t.Fatalf("services = %v", got)
	}
	for _, service := range project.Services {
		if service.Kind == model.ServiceProcess && (service.Health.Kind != "http" || service.Health.Path != "/health") {
			t.Errorf("%s readiness = %#v, want HTTP /health", service.Name, service.Health)
		}
	}
	if got := sourceServices(sources); strings.Join(got, ",") != "console=console,maps=geocoder+routing,operations=api+notifier" {
		t.Fatalf("source services = %v", got)
	}
	if got := connectionKeys(project.Connections); strings.Join(got, ",") != "api:api-mysql:tcp,api:dispatch-nats:tcp,api:geocoder:http,api:routing:http,console:api:http,console:notifier:http,notifier:dispatch-nats:tcp,routing:geocoder:http" {
		t.Fatalf("connections = %v", got)
	}
	if len(project.References) != 0 {
		t.Fatalf("unresolved references = %#v", project.References)
	}
	if got := providerDefaults(defaults); strings.Join(got, ",") != "api-mysql=container,api=local:operations,console=local:console,dispatch-nats=container,geocoder=local:maps,notifier=local:operations,routing=local:maps" {
		t.Fatalf("provider defaults = %v", got)
	}
}

func TestDispatchBootstrapCreatesIndependentCleanCheckouts(t *testing.T) {
	root := repositoryRoot(t)
	example := filepath.Join(root, "examples", "dispatch")
	workspace := filepath.Join(t.TempDir(), "dispatch-workspace")
	command := exec.Command("bash", filepath.Join(example, "bootstrap.sh"), "--workspace", workspace)
	command.Dir = example
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bootstrap dispatch workspace: %v\n%s", err, output)
	}
	for _, source := range []string{"console", "operations", "maps"} {
		checkout := filepath.Join(workspace, "checkouts", source)
		if info, statErr := os.Stat(filepath.Join(checkout, ".git")); statErr != nil || !info.IsDir() {
			t.Fatalf("%s checkout has no independent Git repository: %v", source, statErr)
		}
		status := exec.Command("git", "-C", checkout, "status", "--porcelain")
		encoded, statusErr := status.CombinedOutput()
		if statusErr != nil || len(encoded) != 0 {
			t.Fatalf("%s checkout is not clean: err=%v\n%s", source, statusErr, encoded)
		}
		for _, declaration := range []string{"portless.yaml", "portless.yml", "portless.json"} {
			if _, statErr := os.Stat(filepath.Join(checkout, declaration)); !os.IsNotExist(statErr) {
				t.Fatalf("%s unexpectedly contains %s: %v", source, declaration, statErr)
			}
		}
	}

	apply := exec.Command(
		"git", "-C", filepath.Join(workspace, "checkouts", "maps"),
		"apply", "--check", filepath.Join(example, "scenarios", "scenic-routing.patch"),
	)
	if output, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("scenic worktree patch does not apply to bootstrapped maps: %v\n%s", err, output)
	}

	repeated := exec.Command("bash", filepath.Join(example, "bootstrap.sh"), "--workspace", workspace)
	repeated.Dir = example
	output, err = repeated.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "destination already exists") {
		t.Fatalf("repeated bootstrap did not fail closed: err=%v\n%s", err, output)
	}
}

func TestDispatchOpenAPIContractsAreReadable(t *testing.T) {
	contracts, err := filepath.Glob(filepath.Join(repositoryRoot(t), "examples", "dispatch", "contracts", "*.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts) != 4 {
		t.Fatalf("OpenAPI contracts = %d, want 4: %v", len(contracts), contracts)
	}
	for _, contract := range contracts {
		encoded, err := os.ReadFile(contract)
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			OpenAPI string `yaml:"openapi"`
			Info    struct {
				Title   string `yaml:"title"`
				Version string `yaml:"version"`
			} `yaml:"info"`
			Paths map[string]any `yaml:"paths"`
		}
		if err := yaml.Unmarshal(encoded, &document); err != nil {
			t.Fatalf("parse %s: %v", filepath.Base(contract), err)
		}
		if document.OpenAPI != "3.1.0" || document.Info.Title == "" || document.Info.Version == "" || len(document.Paths) == 0 {
			t.Fatalf("incomplete OpenAPI contract %s: %#v", filepath.Base(contract), document)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
}

func serviceNames(services []model.ServiceDefinition) []string {
	result := make([]string, 0, len(services))
	for _, service := range services {
		result = append(result, service.Name)
	}
	sort.Strings(result)
	return result
}

func sourceServices(sources []model.ProjectSource) []string {
	result := make([]string, 0, len(sources))
	for _, source := range sources {
		services := append([]string(nil), source.Services...)
		sort.Strings(services)
		result = append(result, source.Name+"="+strings.Join(services, "+"))
	}
	sort.Strings(result)
	return result
}

func connectionKeys(connections []model.Connection) []string {
	result := make([]string, 0, len(connections))
	for _, connection := range connections {
		result = append(result, connection.Source+":"+connection.Target+":"+string(connection.Protocol))
	}
	sort.Strings(result)
	return result
}

func providerDefaults(bindings []model.ComponentBinding) []string {
	result := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		value := binding.Service + "=" + string(binding.Provider)
		if binding.Source != "" {
			value += ":" + binding.Source
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
