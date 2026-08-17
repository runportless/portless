package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/projects/discovery/spec"
	"github.com/portless-run/portless/portless-daemon/providers"
)

type fixtureResourcePlugin struct{}

func (fixtureResourcePlugin) Descriptor() providers.Descriptor {
	return providers.Descriptor{ID: "fixture-broker", DefaultVersion: "1"}
}

func (fixtureResourcePlugin) Detect(_ context.Context, _ providers.Workspace, consumers []providers.Consumer) (providers.Findings, error) {
	if len(consumers) == 0 {
		return providers.Findings{}, nil
	}
	consumer := consumers[0]
	return providers.Findings{Candidates: []providers.Candidate{{
		Key: consumer.Directory, Name: "broker",
		Evidence: []model.Evidence{{File: "package.json", Explanation: "fixture broker dependency found", Confidence: "high"}},
		Bindings: []providers.BindingClaim{{ConsumerKey: consumer.Key, Environment: "BROKER_URL", Required: true}},
	}}}, nil
}

func (fixtureResourcePlugin) Plan(definition model.ResourceDefinition) (providers.ContainerPlan, error) {
	return providers.ContainerPlan{
		Image: "docker.io/example/fixture-broker:" + definition.Version, ClientPort: 7777,
		Volumes:   []providers.Volume{{Key: "data", Path: "/data"}, {Key: "archive", Path: "/archive"}},
		Readiness: providers.Readiness{Kind: "tcp", Timeout: time.Minute, Interval: time.Second},
	}, nil
}

func (fixtureResourcePlugin) Bind(context providers.BindingContext) (providers.BindingResult, error) {
	if !context.Active {
		return providers.BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	value := "fixture://" + context.Host
	return providers.BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: value}}, nil
}

func TestDiscoverNestServicesAndDependenciesWithoutExecutingCommands(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "package.json"), `{"private":true,"workspaces":["apps/*"]}`)
	writeFixture(t, filepath.Join(root, "apps", "gateway", "package.json"), `{"name":"gateway","scripts":{"start:dev":"node server.mjs"},"dependencies":{"@nestjs/core":"1.0.0"}}`)
	writeFixture(t, filepath.Join(root, "apps", "gateway", ".env.example"), "ORDERS_URL=http://orders\nREDIS_URL=redis://redis\n")
	writeFixture(t, filepath.Join(root, "apps", "orders", "package.json"), `{"name":"orders","scripts":{"start:dev":"node server.mjs"},"dependencies":{"@nestjs/core":"1.0.0"}}`)
	writeFixture(t, filepath.Join(root, "apps", "orders", ".env.example"), "DATABASE_URL=postgresql://postgres\n")

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model.SuggestedName == "" || len(result.Model.Services) != 4 {
		t.Fatalf("unexpected model: %#v", result.Model)
	}
	wanted := map[string]bool{"gateway": false, "orders": false, "postgres": false, "redis": false}
	for _, service := range result.Model.Services {
		if _, ok := wanted[service.Name]; ok {
			wanted[service.Name] = true
		}
	}
	for name, found := range wanted {
		if !found {
			t.Errorf("service %s was not discovered", name)
		}
	}
	for _, service := range result.Model.Services {
		switch service.Name {
		case "postgres":
			if service.Kind != model.ServiceResource || service.Resource == nil || service.Resource.Type != "postgres" || service.Resource.Version != "17" || service.Port != 5432 {
				t.Errorf("PostgreSQL resource = %#v", service)
			}
		case "redis":
			if service.Kind != model.ServiceResource || service.Resource == nil || service.Resource.Type != "valkey" || service.Resource.Version != "8" || service.Port != 6379 {
				t.Errorf("Valkey resource = %#v", service)
			}
		}
	}
	for _, connection := range result.Model.Connections {
		if connection.Target == "postgres" && (connection.Protocol != model.ProtocolTCP || connection.Binding != "postgres") {
			t.Errorf("PostgreSQL connection = %#v", connection)
		}
		if connection.Target == "redis" && (connection.Protocol != model.ProtocolTCP || connection.Binding != "valkey") {
			t.Errorf("Valkey connection = %#v", connection)
		}
	}
	if len(result.Model.Connections) < 3 {
		t.Fatalf("expected HTTP, Postgres, and Redis connections; got %#v", result.Model.Connections)
	}
	if len(result.Model.References) != 0 {
		t.Fatalf("managed dependency URLs became unresolved service references: %#v", result.Model.References)
	}
}

func TestDiscoverMySQLAndNATSResourcePlugins(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"name":"worker","scripts":{"start":"node server.js"},"dependencies":{"express":"5","mysql2":"3","nats":"2"}}`)
	writeFixture(t, filepath.Join(root, ".env.example"), "MYSQL_URL=mysql://mysql\nNATS_URL=nats://nats\n")

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]struct {
		resourceType string
		version      string
		port         int
	}{
		"mysql": {resourceType: "mysql", version: "8.4", port: 3306},
		"nats":  {resourceType: "nats", version: "2", port: 4222},
	}
	for _, service := range result.Model.Services {
		expected, exists := wanted[service.Name]
		if !exists {
			continue
		}
		if service.Kind != model.ServiceResource || service.Resource == nil || service.Resource.Type != expected.resourceType || service.Resource.Version != expected.version || service.Port != expected.port {
			t.Errorf("resource %s = %#v", service.Name, service)
		}
		delete(wanted, service.Name)
	}
	if len(wanted) != 0 {
		t.Fatalf("resources were not discovered: %v; model=%#v", wanted, result.Model)
	}
	bindings := map[string]string{}
	for _, connection := range result.Model.Connections {
		if connection.Source == "worker" {
			bindings[connection.Target] = connection.Binding + ":" + connection.Environment
		}
	}
	if bindings["mysql"] != "mysql:MYSQL_URL" || bindings["nats"] != "nats:NATS_URL" {
		t.Fatalf("resource bindings = %#v", bindings)
	}
	if len(result.Model.References) != 0 {
		t.Fatalf("resource URLs became unresolved HTTP references: %#v", result.Model.References)
	}
}

func TestCustomResourcePluginNeedsNoEngineSpecialCase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"5"}}`)
	resources, err := providers.NewRegistry(fixtureResourcePlugin{})
	if err != nil {
		t.Fatal(err)
	}
	discoverer, err := NewDefault(Config{Resources: resources})
	if err != nil {
		t.Fatal(err)
	}
	result, err := discoverer.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 2 || len(result.Model.Connections) != 1 {
		t.Fatalf("custom resource topology = %#v", result.Model)
	}
	service := result.Model.Services[1]
	if service.Name != "broker" || service.Resource == nil || service.Resource.Type != "fixture-broker" || service.Port != 7777 {
		t.Fatalf("custom resource = %#v", service)
	}
	connection := result.Model.Connections[0]
	if connection.Binding != "fixture-broker" || connection.Protocol != model.ProtocolTCP || connection.Environment != "BROKER_URL" {
		t.Fatalf("custom binding = %#v", connection)
	}
}

func TestResourceEnvironmentCollisionsFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"5","pg":"8","mysql2":"3"}}`)
	_, err := Discover(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "both inject DATABASE_URL") {
		t.Fatalf("resource binding collision error = %v", err)
	}
}

func TestStoreExampleMatchesItsRuntimeTopology(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate discovery test source")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..", "examples", "store")
	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model.SuggestedName != "store" {
		t.Fatalf("suggested name = %q, want store", result.Model.SuggestedName)
	}
	services := make(map[string]bool, len(result.Model.Services))
	for _, service := range result.Model.Services {
		services[service.Name] = true
		if service.Name == "inventory" {
			if service.Framework != "spring-boot" {
				t.Errorf("inventory framework = %q, want spring-boot", service.Framework)
			}
			if command := strings.Join(service.Command, " "); command != "./gradlew :apps:inventory:bootRun" {
				t.Errorf("inventory command = %q", command)
			}
			if service.Health.Kind != "http" || service.Health.Path != "/actuator/health" {
				t.Errorf("inventory health = %#v", service.Health)
			}
		}
	}
	for _, service := range []string{"checkout", "inventory", "orders", "postgres", "redis"} {
		if !services[service] {
			t.Errorf("service %s was not discovered", service)
		}
	}

	actual := make(map[string]bool, len(result.Model.Connections))
	for _, connection := range result.Model.Connections {
		actual[connection.Source+":"+connection.Target] = true
	}
	expected := []string{"checkout:inventory", "checkout:orders", "orders:postgres", "orders:redis"}
	if len(actual) != len(expected) {
		t.Fatalf("connections = %#v, want only %v", result.Model.Connections, expected)
	}
	for _, edge := range expected {
		if !actual[edge] {
			t.Errorf("connection %s was not discovered", edge)
		}
	}
	if len(result.Model.References) != 0 {
		t.Errorf("unexpected unresolved references: %#v", result.Model.References)
	}
}

func TestNodeFrameworkPluginsResolveSpecificFrameworks(t *testing.T) {
	tests := []struct {
		name         string
		manifest     string
		framework    string
		commandStart string
	}{
		{name: "express", manifest: `{"name":"api","scripts":{"dev":"node server.js"},"dependencies":{"express":"5"}}`, framework: "express", commandStart: "npm run dev"},
		{name: "fastify", manifest: `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"fastify":"5"}}`, framework: "fastify", commandStart: "npm run start"},
		{name: "nestjs supersedes adapters", manifest: `{"name":"api","scripts":{"start:dev":"nest start --watch"},"dependencies":{"@nestjs/core":"11","express":"5","fastify":"5"}}`, framework: "nestjs", commandStart: "npm run start:dev"},
		{name: "nextjs supersedes express", manifest: `{"name":"web","scripts":{"dev":"next dev"},"dependencies":{"next":"16","express":"5"}}`, framework: "nextjs", commandStart: "npm run dev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "workspace")
			writeFixture(t, filepath.Join(root, "package.json"), test.manifest)
			result, err := Discover(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Model.Services) != 1 {
				t.Fatalf("services = %#v", result.Model.Services)
			}
			service := result.Model.Services[0]
			if service.Framework != test.framework {
				t.Fatalf("framework = %q, want %q", service.Framework, test.framework)
			}
			if command := strings.Join(service.Command, " "); command != test.commandStart {
				t.Fatalf("command = %q, want %q", command, test.commandStart)
			}
		})
	}
}

func TestDiscoverGoHTTPServiceWithoutRunningGoCommands(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "go.mod"), "module example.com/store\n\ngo 1.26\n")
	writeFixture(t, filepath.Join(root, "cmd", "orders", "main.go"), `package main
import (
    "net/http"
    "os"
)
func main() { _ = http.ListenAndServe(":"+os.Getenv("PORT"), nil) }
`)

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 1 {
		t.Fatalf("services = %#v", result.Model.Services)
	}
	service := result.Model.Services[0]
	if service.Name != "orders" || service.Framework != "go" {
		t.Fatalf("service = %#v", service)
	}
	if command := strings.Join(service.Command, " "); command != "go run ./cmd/orders" {
		t.Fatalf("command = %q", command)
	}
}

func TestDiscoverGoRPCServiceAndVersionedModuleName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "worker")
	writeFixture(t, filepath.Join(root, "go.mod"), "module example.com/platform/worker/v2\n\ngo 1.26\n\nrequire google.golang.org/grpc v1.79.0\n")
	writeFixture(t, filepath.Join(root, "main.go"), `package main
import (
    "os"
    "google.golang.org/grpc"
)
func main() { _ = os.Getenv("PORT"); _ = grpc.NewServer() }
`)

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 1 || result.Model.Services[0].Name != "worker" || result.Model.Services[0].Framework != "go" {
		t.Fatalf("gRPC service = %#v", result.Model.Services)
	}
	if explanation := result.Model.Services[0].Evidence[0].Explanation; explanation != "gRPC server construction found" {
		t.Fatalf("gRPC evidence = %q", explanation)
	}
}

func TestDiscoverGoServerFromModuleDependency(t *testing.T) {
	root := filepath.Join(t.TempDir(), "api")
	writeFixture(t, filepath.Join(root, "go.mod"), "module example.com/api\n\ngo 1.26\n\nrequire github.com/gin-gonic/gin v1.11.0\n")
	writeFixture(t, filepath.Join(root, "main.go"), `package main
import "os"
func main() { _ = os.Getenv("PORT") }
`)

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 1 || result.Model.Services[0].Evidence[0].Explanation != "server dependency found in Go module" {
		t.Fatalf("module dependency service = %#v", result.Model.Services)
	}
}

func TestGoServiceWithoutDynamicPortFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "go.mod"), "module example.com/store\n\ngo 1.26\n")
	writeFixture(t, filepath.Join(root, "main.go"), `package main
import "net/http"
func main() { _ = http.ListenAndServe(":8080", nil) }
`)
	_, err := Discover(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "does not read the PORT environment variable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscoverSpringBootMavenService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "inventory")
	writeFixture(t, filepath.Join(root, "pom.xml"), `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>dev.portless</groupId><artifactId>inventory-service</artifactId><version>1</version>
  <dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-actuator</artifactId></dependency></dependencies>
  <build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build>
</project>`)
	writeFixture(t, filepath.Join(root, "mvnw"), "#!/bin/sh\n")

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	service := result.Model.Services[0]
	if service.Name != "inventory" || service.Framework != "spring-boot" {
		t.Fatalf("service = %#v", service)
	}
	if command := strings.Join(service.Command, " "); command != "./mvnw spring-boot:run" {
		t.Fatalf("command = %q", command)
	}
	if service.Health.Kind != "http" || service.Health.Path != "/actuator/health" {
		t.Fatalf("health = %#v", service.Health)
	}
}

func TestDiscoverFastAPIService(t *testing.T) {
	root := filepath.Join(t.TempDir(), "catalog")
	writeFixture(t, filepath.Join(root, "pyproject.toml"), "[project]\ndependencies = [\"fastapi\", \"uvicorn\"]\n")
	writeFixture(t, filepath.Join(root, "uv.lock"), "version = 1\n")
	writeFixture(t, filepath.Join(root, "app", "main.py"), "from fastapi import FastAPI\napp = FastAPI()\n")

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	service := result.Model.Services[0]
	if service.Name != "catalog" || service.Framework != "fastapi" || service.PortEnvironment != "UVICORN_PORT" {
		t.Fatalf("service = %#v", service)
	}
	if command := strings.Join(service.Command, " "); command != "uv run uvicorn app.main:app" {
		t.Fatalf("command = %q", command)
	}
}

func TestDiscoverNestedFastAPIServiceWithPoetryAndSrcLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"private":true}`)
	writeFixture(t, filepath.Join(root, "services", "catalog", "pyproject.toml"), "[tool.poetry.dependencies]\nfastapi = \"^0.1\"\nuvicorn = \"^0.1\"\n")
	writeFixture(t, filepath.Join(root, "services", "catalog", "poetry.lock"), "package = []\n")
	writeFixture(t, filepath.Join(root, "services", "catalog", "src", "catalog", "api.py"), "import fastapi\napplication = fastapi.FastAPI()\n")

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 1 || result.Model.Services[0].Name != "catalog" {
		t.Fatalf("nested FastAPI service = %#v", result.Model.Services)
	}
	if command := strings.Join(result.Model.Services[0].Command, " "); command != "poetry run uvicorn catalog.api:application --app-dir src" {
		t.Fatalf("nested FastAPI command = %q", command)
	}
}

func TestDuplicateServiceNamesFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"private":true}`)
	manifest := `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"5"}}`
	writeFixture(t, filepath.Join(root, "apps", "first", "package.json"), manifest)
	writeFixture(t, filepath.Join(root, "apps", "second", "package.json"), manifest)

	_, err := Discover(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "service name api") || !strings.Contains(err.Error(), "apps/first") || !strings.Contains(err.Error(), "apps/second") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscoveryOutputIsDeterministic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"private":true}`)
	writeFixture(t, filepath.Join(root, "apps", "web", "package.json"), `{"name":"web","scripts":{"dev":"next dev"},"dependencies":{"next":"16"}}`)
	writeFixture(t, filepath.Join(root, "apps", "api", "package.json"), `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"5"}}`)
	writeFixture(t, filepath.Join(root, "apps", "web", ".env.example"), "API_URL=http://api\n")

	first, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 10; attempt++ {
		current, err := Discover(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, current) {
			t.Fatalf("discovery changed between runs:\nfirst=%#v\ncurrent=%#v", first, current)
		}
	}
}

func TestMalformedManifestFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"name":`)
	_, err := Discover(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "parse package.json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDiscoverySkipsSymlinkedManifestAndActualEnvironmentFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires additional privileges on Windows")
	}
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"5"}}`)
	writeFixture(t, filepath.Join(root, ".env"), "REDIS_URL=redis://secret.example\n")
	external := filepath.Join(t.TempDir(), "package.json")
	writeFixture(t, external, `{"name":"escape","scripts":{"start":"node server.js"},"dependencies":{"express":"5"}}`)
	if err := os.MkdirAll(filepath.Join(root, "apps", "escape"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "apps", "escape", "package.json")); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 1 || result.Model.Services[0].Name != "api" {
		t.Fatalf("symlink or secret environment file affected discovery: %#v", result.Model.Services)
	}
}

func TestDiscoveryHonorsCancellationAndLimits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "package.json"), `{"name":"api","scripts":{"start":"node server.js"},"dependencies":{"express":"5"}}`)
	writeFixture(t, filepath.Join(root, "extra.txt"), "extra")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(cancelled, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery error = %v", err)
	}
	limited, err := NewDefault(Config{Limits: Limits{MaxFiles: 1}, ScanTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limited.Discover(context.Background(), root); err == nil || !strings.Contains(err.Error(), "file limit") {
		t.Fatalf("limited discovery error = %v", err)
	}
}

type fixtureDetector struct {
	descriptor spec.Descriptor
	name       string
}

func (d fixtureDetector) Descriptor() spec.Descriptor { return d.descriptor }

func (d fixtureDetector) Detect(context.Context, spec.Workspace) (spec.Findings, error) {
	return spec.Findings{Candidates: []spec.Candidate{{
		Key: ".", Directory: ".", RunDirectory: ".",
		Definition: model.ServiceDefinition{
			Name: d.name, Kind: model.ServiceProcess, Framework: d.descriptor.ID, Command: []string{"serve"}, PortEnvironment: "PORT", Required: true,
			Health: model.HealthCheck{Kind: "tcp", Timeout: time.Second, Interval: time.Second},
		},
	}}}, nil
}

func TestCustomDiscoveryPluginCanBeRegistered(t *testing.T) {
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "service.test"), "fixture")
	detector := fixtureDetector{descriptor: spec.Descriptor{ID: "fixture", RootMarkers: []string{"service.test"}}, name: "fixture"}
	discoverer, err := New(Config{}, []spec.ServiceDetector{detector}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := discoverer.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Model.Services) != 1 || result.Model.Services[0].Framework != "fixture" {
		t.Fatalf("result = %#v", result.Model)
	}
}

type panickingDetector struct{}

func (panickingDetector) Descriptor() spec.Descriptor {
	return spec.Descriptor{ID: "panic", RootMarkers: []string{"service.test"}}
}

func (panickingDetector) Detect(context.Context, spec.Workspace) (spec.Findings, error) {
	panic("plugin bug")
}

type panickingDescriptorDetector struct{}

func (panickingDescriptorDetector) Descriptor() spec.Descriptor { panic("descriptor bug") }

func (panickingDescriptorDetector) Detect(context.Context, spec.Workspace) (spec.Findings, error) {
	return spec.Findings{}, nil
}

func TestPluginRegistryRejectsDuplicatesAndContainsPanics(t *testing.T) {
	detector := fixtureDetector{descriptor: spec.Descriptor{ID: "fixture", RootMarkers: []string{"service.test"}}, name: "fixture"}
	if _, err := New(Config{}, []spec.ServiceDetector{detector, detector}, nil); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate registry error = %v", err)
	}
	if _, err := New(Config{}, []spec.ServiceDetector{panickingDescriptorDetector{}}, nil); err == nil || !strings.Contains(err.Error(), "panic: descriptor bug") {
		t.Fatalf("descriptor panic error = %v", err)
	}
	root := filepath.Join(t.TempDir(), "workspace")
	writeFixture(t, filepath.Join(root, "service.test"), "fixture")
	discoverer, err := New(Config{}, []spec.ServiceDetector{panickingDetector{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discoverer.Discover(context.Background(), root); err == nil || !strings.Contains(err.Error(), "panic: plugin bug") {
		t.Fatalf("plugin panic error = %v", err)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
