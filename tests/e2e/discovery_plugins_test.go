//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
)

func TestCLIFrameworkDiscoveryPluginMatrix(t *testing.T) {
	type expectation struct {
		framework       string
		portEnvironment string
		command         []string
		debugAdapter    model.DebugAdapter
		debugLauncher   model.DebugLauncher
	}
	cases := []struct {
		name        string
		files       map[string]string
		expectation expectation
	}{
		{
			name: "spring-gradle",
			files: map[string]string{
				"build.gradle": `plugins { id 'org.springframework.boot' version '3.5.0' }`,
			},
			expectation: expectation{framework: "spring-boot", portEnvironment: "SERVER_PORT", command: []string{"gradle", "bootRun"}, debugAdapter: model.DebugJDWP, debugLauncher: model.DebugSpringGradle},
		},
		{
			name: "spring-maven",
			files: map[string]string{
				"pom.xml": `<project><modelVersion>4.0.0</modelVersion><groupId>example</groupId><artifactId>spring-maven-api</artifactId><version>1</version><dependencies><dependency><groupId>org.springframework.boot</groupId><artifactId>spring-boot-starter-web</artifactId></dependency></dependencies></project>`,
			},
			expectation: expectation{framework: "spring-boot", portEnvironment: "SERVER_PORT", command: []string{"mvn", "spring-boot:run"}, debugAdapter: model.DebugJDWP, debugLauncher: model.DebugSpringMaven},
		},
		{
			name: "nestjs",
			files: map[string]string{
				"package.json": `{"name":"nest-api","scripts":{"start:dev":"nest start --watch","start:debug":"nest start --debug 127.0.0.1:9229 --watch"},"dependencies":{"@nestjs/core":"latest","express":"latest","fastify":"latest"}}`,
			},
			expectation: expectation{framework: "nestjs", portEnvironment: "PORT", command: []string{"npm", "run", "start:dev"}, debugAdapter: model.DebugNodeInspector, debugLauncher: model.DebugNestCLI},
		},
		{
			name: "express",
			files: map[string]string{
				"package.json": `{"name":"express-api","scripts":{"dev":"node server.js"},"dependencies":{"express":"latest"}}`,
			},
			expectation: expectation{framework: "express", portEnvironment: "PORT", command: []string{"npm", "run", "dev"}, debugAdapter: model.DebugNodeInspector, debugLauncher: model.DebugNodeDirect},
		},
		{
			name: "fastify",
			files: map[string]string{
				"package.json": `{"name":"fastify-api","scripts":{"dev":"node server.js"},"dependencies":{"fastify":"latest"}}`,
			},
			expectation: expectation{framework: "fastify", portEnvironment: "PORT", command: []string{"npm", "run", "dev"}, debugAdapter: model.DebugNodeInspector, debugLauncher: model.DebugNodeDirect},
		},
		{
			name: "nextjs",
			files: map[string]string{
				"package.json": `{"name":"next-web","scripts":{"dev":"next dev"},"dependencies":{"next":"latest","express":"latest"}}`,
			},
			expectation: expectation{framework: "nextjs", portEnvironment: "PORT", command: []string{"npm", "run", "dev"}},
		},
		{
			name: "go",
			files: map[string]string{
				"go.mod":  "module example.com/go-api\n\ngo 1.26\n",
				"main.go": "package main\nimport (\"net/http\"; \"os\")\nfunc main() { http.ListenAndServe(\":\"+os.Getenv(\"PORT\"), nil) }\n",
			},
			expectation: expectation{framework: "go", portEnvironment: "PORT", command: []string{"go", "run", "."}},
		},
		{
			name: "fastapi",
			files: map[string]string{
				"requirements.txt": "fastapi==1.0\nuvicorn==1.0\n",
				"main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			expectation: expectation{framework: "fastapi", portEnvironment: "UVICORN_PORT", command: []string{"python", "-m", "uvicorn", "main:app"}},
		},
	}

	binary := e2eBinary(t)
	root, err := os.MkdirTemp("/tmp", "portless-discovery-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	var cleanupDirectory string
	defer func() {
		if cleanupDirectory != "" {
			cleanupInstallation(t, binary, home, cleanupDirectory)
		}
	}()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := writeDiscoveryFixture(t, root, testCase.name, testCase.files)
			if cleanupDirectory == "" {
				cleanupDirectory = source
			}
			created := createDiscoveredProject(t, binary, home, source, "framework-"+testCase.name)
			if len(created.Project.Services) != 1 || len(created.Environment.Services) != 1 {
				t.Fatalf("discovered services = project:%#v environment:%#v", created.Project.Services, created.Environment.Services)
			}
			service := created.Project.Services[0]
			if service.Framework != testCase.expectation.framework || service.Kind != model.ServiceProcess || service.PortEnvironment != testCase.expectation.portEnvironment || strings.Join(service.Command, "\x00") != strings.Join(testCase.expectation.command, "\x00") {
				t.Fatalf("unexpected %s discovery: %#v", testCase.name, service)
			}
			if service.Health.Kind != "tcp" || !service.Required || len(service.Evidence) == 0 {
				t.Fatalf("incomplete %s discovery metadata: %#v", testCase.name, service)
			}
			if testCase.expectation.debugAdapter == "" {
				if service.Debug != nil {
					t.Fatalf("%s unexpectedly exposed a debugger: %#v", testCase.name, service.Debug)
				}
			} else if service.Debug == nil || service.Debug.Adapter != testCase.expectation.debugAdapter || service.Debug.Launcher != testCase.expectation.debugLauncher || len(service.Debug.Command) == 0 {
				t.Fatalf("%s debugger metadata = %#v", testCase.name, service.Debug)
			}
		})
	}
}

func TestCLIDiscoveryPrecedenceAndRescan(t *testing.T) {
	binary := e2eBinary(t)
	root, err := os.MkdirTemp("/tmp", "portless-rescan-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	source := writeDiscoveryFixture(t, root, "rescan-app", map[string]string{
		"package.json": `{"name":"rescan-api","scripts":{"dev":"node server.js"},"dependencies":{"express":"latest"}}`,
	})
	defer cleanupInstallation(t, binary, home, source)

	created := createDiscoveredProject(t, binary, home, source, "rescan-e2e")
	if len(created.Environment.Services) != 1 || created.Environment.Services[0].Framework != "express" {
		t.Fatalf("initial framework was not Express: %#v", created.Environment.Services)
	}
	initialRevision := created.Environment.Revision
	writeDiscoveryFile(t, source, "package.json", `{"name":"rescan-api","scripts":{"start:dev":"nest start --watch"},"dependencies":{"@nestjs/core":"latest","express":"latest","fastify":"latest"}}`)

	output, err := runCLIAt(binary, home, source, "--env", "rescan-e2e/local", "--json", "env", "rescan")
	if err != nil {
		t.Fatalf("rescan Express source as NestJS: %v\n%s", err, output)
	}
	var result struct {
		Environment model.Environment `json:"environment"`
		Warnings    []string          `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode rescan response: %v\n%s", err, output)
	}
	if result.Environment.Revision <= initialRevision || len(result.Environment.Services) != 1 || result.Environment.Services[0].Framework != "nestjs" {
		t.Fatalf("NestJS did not supersede Express/Fastify after rescan: %#v", result.Environment)
	}
	if result.Environment.Services[0].Debug == nil || result.Environment.Services[0].Debug.Adapter != model.DebugNodeInspector {
		t.Fatalf("rescanned NestJS service lacks debug metadata: %#v", result.Environment.Services[0])
	}

	writeDiscoveryFile(t, source, "package.json", `{"name":`)
	malformedOutput, malformedErr := runCLIAt(binary, home, source, "--env", "rescan-e2e/local", "env", "rescan")
	if malformedErr == nil || !strings.Contains(malformedOutput, "parse package.json") {
		t.Fatalf("malformed rescan did not fail closed: err=%v\n%s", malformedErr, malformedOutput)
	}
	unchanged := explicitEnvironmentStatus(t, binary, home, source, "rescan-e2e/local")
	if unchanged.Revision != result.Environment.Revision || len(unchanged.Services) != 1 || unchanged.Services[0].Framework != "nestjs" {
		t.Fatalf("failed rescan mutated the last valid model: %#v", unchanged)
	}
}

func TestCLIResourceDiscoveryPluginMatrix(t *testing.T) {
	cases := []struct {
		name         string
		dependency   string
		resource     string
		resourceType string
		version      string
		port         int
		environment  string
	}{
		{name: "postgres", dependency: `"pg":"latest"`, resource: "postgres", resourceType: "postgres", version: "17", port: 5432, environment: "DATABASE_URL"},
		{name: "valkey", dependency: `"redis":"latest"`, resource: "redis", resourceType: "valkey", version: "8", port: 6379, environment: "REDIS_URL"},
		{name: "mysql", dependency: `"mysql2":"latest"`, resource: "mysql", resourceType: "mysql", version: "8.4", port: 3306, environment: "DATABASE_URL"},
		{name: "nats", dependency: `"nats":"latest"`, resource: "nats", resourceType: "nats", version: "2", port: 4222, environment: "NATS_URL"},
	}

	binary := e2eBinary(t)
	root, err := os.MkdirTemp("/tmp", "portless-resource-discovery-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	var cleanupDirectory string
	defer func() {
		if cleanupDirectory != "" {
			cleanupInstallation(t, binary, home, cleanupDirectory)
		}
	}()

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := writeDiscoveryFixture(t, root, "resource-"+testCase.name, map[string]string{
				"package.json": `{"name":"resource-api","scripts":{"start":"node server.js"},"dependencies":{"express":"latest",` + testCase.dependency + `}}`,
			})
			if cleanupDirectory == "" {
				cleanupDirectory = source
			}
			created := createDiscoveredProject(t, binary, home, source, "resource-"+testCase.name)
			if len(created.Project.Services) != 2 || len(created.Project.Connections) != 1 {
				t.Fatalf("unexpected %s resource topology: services=%#v connections=%#v", testCase.name, created.Project.Services, created.Project.Connections)
			}
			var discovered *model.ServiceDefinition
			var consumer *model.ServiceDefinition
			for index := range created.Project.Services {
				if created.Project.Services[index].Kind == model.ServiceResource {
					discovered = &created.Project.Services[index]
				} else if created.Project.Services[index].Kind == model.ServiceProcess {
					consumer = &created.Project.Services[index]
				}
			}
			if discovered == nil || discovered.Name != testCase.resource || discovered.Resource == nil || discovered.Resource.Type != testCase.resourceType || discovered.Resource.Version != testCase.version || discovered.Port != testCase.port || !discovered.Required || len(discovered.Evidence) == 0 {
				t.Fatalf("unexpected %s resource definition: %#v", testCase.name, discovered)
			}
			connection := created.Project.Connections[0]
			if consumer == nil || connection.Source != consumer.Name || connection.Target != testCase.resource || connection.Protocol != model.ProtocolTCP || connection.Binding != testCase.resourceType || connection.Environment != testCase.environment || !connection.Required {
				t.Fatalf("unexpected %s resource connection: %#v", testCase.name, connection)
			}
		})
	}
}

type discoveredProject struct {
	Project     model.Project     `json:"project"`
	Environment model.Environment `json:"environment"`
	Warnings    []string          `json:"warnings"`
}

func createDiscoveredProject(t *testing.T, binary, home, source, name string) discoveredProject {
	t.Helper()
	output, err := runCLIAt(binary, home, source, "--json", "project", "create", name, "--source", "app="+source)
	if err != nil {
		t.Fatalf("create discovered project %s: %v\n%s\ndaemon log:\n%s", name, err, output, readDaemonLog(home))
	}
	var result discoveredProject
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode discovered project %s: %v\n%s", name, err, output)
	}
	return result
}

func writeDiscoveryFixture(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	directory := filepath.Join(root, name)
	for path, content := range files {
		writeDiscoveryFile(t, directory, path, content)
	}
	return directory
}

func writeDiscoveryFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
