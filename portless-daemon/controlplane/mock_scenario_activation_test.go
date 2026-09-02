package controlplane

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestMockScenarioActivatesAllServicesAndRestoresTheirExactProviders(t *testing.T) {
	app, store := mockScenarioTestService(t)
	ctx := context.Background()
	before, err := store.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	createTestMockScenario(t, app, "checkout-failure", "inventory", "payments")
	operation, err := app.SetMockScenarioEnabled(ctx, "store", "local", "checkout-failure", true, "test", "enable-checkout-failure")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("enable operation = %#v", operation)
	}
	scenario, err := app.MockScenario(ctx, "store", "local", "checkout-failure")
	if err != nil || scenario.Activation.State != model.MockScenarioEnabled || !reflect.DeepEqual(scenario.Activation.ActiveServices, []string{"inventory", "payments"}) {
		t.Fatalf("activation = %#v, err = %v", scenario.Activation, err)
	}
	active, err := store.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"inventory", "payments"} {
		binding := bindingForEnvironment(active, service)
		if binding.Provider != model.ProviderMock || binding.Mock == nil || binding.Mock.Scenario != scenario.Name {
			t.Fatalf("%s did not activate: %#v", service, binding)
		}
	}
	if !sameProviderBinding(bindingForEnvironment(before, "checkout"), bindingForEnvironment(active, "checkout")) {
		t.Fatal("activation changed an unrelated service")
	}
	if _, err := app.ChangeBinding(ctx, "store", "local", "inventory", bindingForEnvironment(before, "inventory"), "test", "direct-change"); err == nil || !strings.Contains(err.Error(), "controlled by mock scenario") {
		t.Fatalf("direct provider change escaped scenario ownership: %v", err)
	}
	operation, err = app.SetMockScenarioEnabled(ctx, "store", "local", "checkout-failure", false, "test", "disable-checkout-failure")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("disable operation = %#v", operation)
	}
	after, err := store.Environment(ctx, "store", "local")
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range before.Bindings {
		if !sameProviderBinding(binding, bindingForEnvironment(after, binding.Service)) {
			t.Fatalf("provider for %s was not restored: before=%#v after=%#v", binding.Service, binding, bindingForEnvironment(after, binding.Service))
		}
	}
	scenario, _ = app.MockScenario(ctx, "store", "local", scenario.Name)
	if scenario.Activation.State != model.MockScenarioDisabled || len(scenario.Activation.ActiveServices) != 0 || scenario.Activation.ActiveServices == nil {
		t.Fatalf("disabled activation = %#v", scenario.Activation)
	}
}

func TestMockScenarioRejectsOverlapAndCoverageChangesWhileActive(t *testing.T) {
	app, _ := mockScenarioTestService(t)
	ctx := context.Background()
	createTestMockScenario(t, app, "first", "inventory", "payments")
	createTestMockScenario(t, app, "overlap", "payments")
	createTestMockScenario(t, app, "disjoint", "checkout")
	for _, name := range []string{"first", "disjoint"} {
		operation, err := app.SetMockScenarioEnabled(ctx, "store", "local", name, true, "test", "enable-"+name)
		if err != nil {
			t.Fatal(err)
		}
		if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
			t.Fatalf("enable %s = %#v", name, operation)
		}
	}
	operation, err := app.SetMockScenarioEnabled(ctx, "store", "local", "overlap", true, "test", "overlap")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "failed" || !strings.Contains(operation.Error, "already controlled") {
		t.Fatalf("overlapping activation = %#v", operation)
	}
	if _, err := app.PutMockRoute(ctx, "store", "local", "first", model.MockRoute{Name: "new-service", Service: "checkout", Method: "GET", Path: "/", Status: 200, Enabled: true}, "test"); err == nil || !strings.Contains(err.Error(), "which services") {
		t.Fatalf("active target expansion was accepted: %v", err)
	}
	if _, err := app.DeleteMockRoute(ctx, "store", "local", "first", "inventory-health", "test"); err == nil || !strings.Contains(err.Error(), "final route") {
		t.Fatalf("active target removal was accepted: %v", err)
	}
	if _, err := app.PutMockRoute(ctx, "store", "local", "first", model.MockRoute{Name: "inventory-health", Service: "inventory", Method: "GET", Path: "/health", Status: 503, Enabled: false}, "test"); err != nil {
		t.Fatalf("same-coverage route update was rejected: %v", err)
	}
	if err := app.DeleteMockScenario(ctx, "store", "local", "first", "test"); err == nil {
		t.Fatal("active scenario deletion was accepted")
	}
	if _, err := app.ChangeBinding(ctx, "store", "local", "checkout", model.ComponentBinding{Provider: model.ProviderMock, Mock: &model.MockTarget{Scenario: "first"}}, "test", "individual-mock"); err == nil || !strings.Contains(err.Error(), "individually") {
		t.Fatalf("individual mock binding was accepted: %v", err)
	}
}

func TestMockScenarioInterruptedActivationRetainsRestorationState(t *testing.T) {
	app, store := mockScenarioTestService(t)
	ctx := context.Background()
	createTestMockScenario(t, app, "interrupted", "inventory", "payments")
	environment, _ := store.Environment(ctx, "store", "local")
	definition, _ := store.EnvironmentModel(ctx, "store", "local")
	previous := []model.ComponentBinding{bindingForEnvironment(environment, "inventory"), bindingForEnvironment(environment, "payments")}
	if _, err := store.ApplyMockScenarioConfiguration(ctx, "store", "local", environment.Revision, definition, environment.Bindings, "interrupted", previous, true); err != nil {
		t.Fatal(err)
	}
	scenario, _ := app.MockScenario(ctx, "store", "local", "interrupted")
	if scenario.Activation.State != model.MockScenarioDegraded {
		t.Fatalf("interrupted activation = %#v", scenario.Activation)
	}
	if _, err := app.SetMockScenarioEnabled(ctx, "store", "local", "interrupted", true, "test", "retry-enable"); err == nil || !strings.Contains(err.Error(), "partially active") {
		t.Fatalf("degraded enable overwrote restoration state: %v", err)
	}
	operation, err := app.SetMockScenarioEnabled(ctx, "store", "local", "interrupted", false, "test", "recover-disable")
	if err != nil {
		t.Fatal(err)
	}
	if operation = waitForOperation(t, app, operation); operation.State != "succeeded" {
		t.Fatalf("recovery operation = %#v", operation)
	}
	scenario, _ = app.MockScenario(ctx, "store", "local", "interrupted")
	if scenario.Activation.State != model.MockScenarioDisabled {
		t.Fatalf("recovered activation = %#v", scenario.Activation)
	}
}

func mockScenarioTestService(t *testing.T) (*Service, *database.Store) {
	t.Helper()
	ctx := context.Background()
	data := t.TempDir()
	store, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	definition := model.ProjectModel{SuggestedName: "store", PrimaryService: "checkout"}
	for _, name := range []string{"checkout", "inventory", "payments"} {
		definition.Services = append(definition.Services, model.ServiceDefinition{Name: name, Kind: model.ServiceProcess, Command: []string{"echo", name}, WorkingDirectory: root, ServiceDirectory: root, Required: true})
	}
	if _, err := store.CreateProject(ctx, "store", definition, []model.ProjectSource{{Name: "store", Services: []string{"checkout", "inventory", "payments"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEnvironment(ctx, "store", "local", definition,
		[]model.SourceBinding{{Name: "store", Path: root, Status: "ready", Definition: definition}},
		[]model.ComponentBinding{
			{Service: "checkout", Provider: model.ProviderLocal, Source: "store"},
			{Service: "inventory", Provider: model.ProviderLocal, Source: "store"},
			{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://payments.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly, HealthPath: "/ready"}},
		}); err != nil {
		t.Fatal(err)
	}
	app := New(store, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	t.Cleanup(func() { app.Close(ctx); _ = store.Close() })
	return app, store
}

func createTestMockScenario(t *testing.T, app *Service, name string, services ...string) {
	t.Helper()
	ctx := context.Background()
	if _, err := app.CreateMockScenario(ctx, "store", "local", model.MockScenario{Name: name}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if _, err := app.PutMockRoute(ctx, "store", "local", name, model.MockRoute{Name: service + "-health", Service: service, Method: "GET", Path: "/health", Status: 503, Enabled: true}, "test"); err != nil {
			t.Fatal(err)
		}
	}
}
