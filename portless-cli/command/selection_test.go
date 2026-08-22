package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestEffectiveEnvironmentSelectorPrefersOneInvocationOverride(t *testing.T) {
	context, _, _ := newTestContext(t, t.TempDir())
	if actual, err := context.EffectiveEnvironmentSelector(""); err != nil || actual != "" {
		t.Fatalf("empty selector = %q, %v", actual, err)
	}
	if actual, err := context.EffectiveEnvironmentSelector("billing/local"); err != nil || actual != "billing/local" {
		t.Fatalf("explicit selector = %q, %v", actual, err)
	}

	context.EnvironmentOverride = "billing/qa"
	if actual, err := context.EffectiveEnvironmentSelector(""); err != nil || actual != "billing/qa" {
		t.Fatalf("override selector = %q, %v", actual, err)
	}
	if _, err := context.EffectiveEnvironmentSelector("billing/local"); err == nil || !strings.Contains(err.Error(), "provided twice") {
		t.Fatalf("duplicate selector error = %v", err)
	}
	for resolution, expected := range map[string]string{
		"flag":     "--env override for this invocation",
		"selected": "saved selection for this checkout",
		"inferred": "only environment using this checkout",
	} {
		if actual := EnvironmentResolutionDescription(resolution); actual != expected {
			t.Errorf("description for %q = %q, want %q", resolution, actual, expected)
		}
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
	selected, err := DebugServiceForPath(environment, filepath.Join(checkout, "src"))
	if err != nil || selected != "checkout" {
		t.Fatalf("selected = %q, err=%v", selected, err)
	}
	selected, err = DebugServiceForPath(environment, root)
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
	selected, err := DebugServiceForPath(environment, root)
	if err != nil || selected != "" {
		t.Fatalf("project root selected = %q, err=%v", selected, err)
	}
	selected, err = DebugServiceForPath(environment, inventory)
	if err != nil || selected != "inventory" {
		t.Fatalf("inventory directory selected = %q, err=%v", selected, err)
	}
}
