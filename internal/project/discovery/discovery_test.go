package discovery

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDiscoverNestServicesAndDependenciesWithoutExecutingCommands(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "package.json"), `{"private":true,"workspaces":["apps/*"]}`)
	writeFixture(t, filepath.Join(root, "apps", "gateway", "package.json"), `{"name":"gateway","scripts":{"start:dev":"node server.mjs"},"dependencies":{"@nestjs/core":"1.0.0"}}`)
	writeFixture(t, filepath.Join(root, "apps", "gateway", ".env.example"), "ORDERS_URL=http://orders\nREDIS_URL=redis://redis\n")
	writeFixture(t, filepath.Join(root, "apps", "orders", "package.json"), `{"name":"orders","scripts":{"start:dev":"node server.mjs"},"dependencies":{"@nestjs/core":"1.0.0"}}`)
	writeFixture(t, filepath.Join(root, "apps", "orders", ".env.example"), "DATABASE_URL=postgresql://postgres\n")

	result, err := Discover(root)
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
	if len(result.Model.Connections) < 3 {
		t.Fatalf("expected HTTP, Postgres, and Redis connections; got %#v", result.Model.Connections)
	}
	if len(result.Model.References) != 0 {
		t.Fatalf("managed dependency URLs became unresolved service references: %#v", result.Model.References)
	}
}

func TestGoldenPathFixtureMatchesItsRuntimeTopology(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate discovery test source")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..", "..", "examples", "golden-path")
	result, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	actual := make(map[string]bool, len(result.Model.Connections))
	for _, connection := range result.Model.Connections {
		actual[connection.Source+":"+connection.Target] = true
	}
	expected := []string{"checkout:orders", "orders:postgres", "orders:redis"}
	if len(actual) != len(expected) {
		t.Fatalf("connections = %#v, want only %v", result.Model.Connections, expected)
	}
	for _, edge := range expected {
		if !actual[edge] {
			t.Errorf("connection %s was not discovered", edge)
		}
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
