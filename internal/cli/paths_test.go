package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
)

func TestAbsoluteSourcePathUsesCLIWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	if err := os.Mkdir(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	actual, err := absoluteSourcePath("checkout")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("absoluteSourcePath = %q, want %q", actual, expected)
	}
}

func TestDebugServiceForPathSelectsTheDeepestLocalProcess(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "apps", "checkout")
	orders := filepath.Join(root, "apps", "orders")
	for _, directory := range []string{checkout, orders} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	environment := model.Environment{
		Bindings: []model.ComponentBinding{
			{Service: "checkout", Provider: model.ProviderLocal},
			{Service: "orders", Provider: model.ProviderLocal},
		},
		Services: []model.Service{
			{ServiceDefinition: model.ServiceDefinition{Name: "checkout", Kind: model.ServiceProcess, ServiceDirectory: checkout}},
			{ServiceDefinition: model.ServiceDefinition{Name: "orders", Kind: model.ServiceProcess, ServiceDirectory: orders}},
		},
	}
	selected, err := debugServiceForPath(environment, filepath.Join(checkout, "src"))
	if err != nil || selected != "checkout" {
		t.Fatalf("selected = %q, err=%v", selected, err)
	}
	selected, err = debugServiceForPath(environment, root)
	if err != nil || selected != "" {
		t.Fatalf("project root selected = %q, err=%v", selected, err)
	}
}

func TestDebugServiceForPathDoesNotTreatSharedBuildRootAsAService(t *testing.T) {
	root := t.TempDir()
	inventory := filepath.Join(root, "apps", "inventory")
	if err := os.MkdirAll(inventory, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := model.Environment{
		Sources:  []model.SourceBinding{{Name: "store", Path: root}},
		Bindings: []model.ComponentBinding{{Service: "inventory", Provider: model.ProviderLocal, Source: "store"}},
		Services: []model.Service{{ServiceDefinition: model.ServiceDefinition{
			Name: "inventory", Kind: model.ServiceProcess, WorkingDirectory: root,
			Evidence: []model.Evidence{{File: "apps/inventory/build.gradle"}},
		}}},
	}
	selected, err := debugServiceForPath(environment, root)
	if err != nil || selected != "" {
		t.Fatalf("project root selected = %q, err=%v", selected, err)
	}
	selected, err = debugServiceForPath(environment, inventory)
	if err != nil || selected != "inventory" {
		t.Fatalf("inventory directory selected = %q, err=%v", selected, err)
	}
}

func TestInvocationKeysAreUnique(t *testing.T) {
	first, err := invocationKey("cli-up")
	if err != nil {
		t.Fatal(err)
	}
	second, err := invocationKey("cli-up")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "cli-up-") || len(first) != len("cli-up-")+32 {
		t.Fatalf("invocation keys = %q, %q", first, second)
	}
}
