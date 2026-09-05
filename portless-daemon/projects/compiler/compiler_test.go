package compiler

import (
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestInitialProjectResolvesCrossSourceReference(t *testing.T) {
	sources := []model.SourceBinding{
		{Name: "checkout", Definition: model.ProjectModel{PrimaryService: "checkout", Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}, References: []model.ConnectionReference{{Source: "checkout", TargetHint: "payments", Environment: "PAYMENTS_URL", Protocol: model.ProtocolHTTP, Required: true}}}},
		{Name: "payments", Definition: model.ProjectModel{Services: []model.ServiceDefinition{{Name: "payments", Kind: model.ServiceProcess}}}},
	}
	definition, _, _, err := InitialProject("billing", sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Connections) != 1 || definition.Connections[0].Source != "checkout" || definition.Connections[0].Target != "payments" {
		t.Fatalf("connections = %#v", definition.Connections)
	}
}

func TestAddSourceMergesTopologyAndReturnsEnvironmentDefaults(t *testing.T) {
	project := model.ProjectModel{
		SuggestedName:  "store",
		PrimaryService: "checkout",
		Services:       []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}},
		References:     []model.ConnectionReference{{Source: "checkout", TargetHint: "inventory", Environment: "INVENTORY_URL", Protocol: model.ProtocolHTTP, Required: true}},
	}
	sources := []model.ProjectSource{{Name: "store", Services: []string{"checkout"}}}
	addition := model.SourceBinding{Name: "inventory", Definition: model.ProjectModel{Services: []model.ServiceDefinition{
		{Name: "inventory", Kind: model.ServiceProcess, WorkingDirectory: "/tmp/inventory", Command: []string{"./gradlew", "bootRun"}},
		resourceService("inventory-db", "postgres", "17", 5432),
	}, Connections: []model.Connection{{Source: "inventory", Target: "inventory-db", Protocol: model.ProtocolTCP, Binding: "postgres", Required: true}}}}

	definition, mergedSources, defaults, err := AddSource(project, sources, addition)
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Services) != 3 || definition.Services[2].Name != "inventory-db" {
		t.Fatalf("services = %#v", definition.Services)
	}
	if definition.Services[1].WorkingDirectory != "" {
		t.Fatalf("logical working directory = %q", definition.Services[1].WorkingDirectory)
	}
	if len(definition.Connections) != 2 || len(definition.References) != 0 {
		t.Fatalf("topology = connections %#v references %#v", definition.Connections, definition.References)
	}
	if len(mergedSources) != 2 || mergedSources[0].Name != "inventory" || strings.Join(mergedSources[0].Services, ",") != "inventory" {
		t.Fatalf("sources = %#v", mergedSources)
	}
	if len(defaults) != 2 || defaults[0].Provider != model.ProviderLocal || defaults[0].Source != "inventory" || defaults[1].Provider != model.ProviderContainer {
		t.Fatalf("defaults = %#v", defaults)
	}
}

func TestAddSourceRejectsDuplicateSource(t *testing.T) {
	_, _, _, err := AddSource(model.ProjectModel{}, []model.ProjectSource{{Name: "inventory"}}, model.SourceBinding{Name: "inventory"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddSourceRejectsProcessServiceOwnedByAnotherSource(t *testing.T) {
	project := model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}}
	_, _, _, err := AddSource(project, []model.ProjectSource{{Name: "store", Services: []string{"checkout"}}}, model.SourceBinding{
		Name: "other", Definition: model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}},
	})
	if err == nil || !strings.Contains(err.Error(), "already provided by store") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddSourceAllowsCompatibleSharedContainer(t *testing.T) {
	project := model.ProjectModel{Services: []model.ServiceDefinition{resourceService("postgres", "postgres", "17", 5432)}}
	definition, sources, defaults, err := AddSource(project, []model.ProjectSource{{Name: "store"}}, model.SourceBinding{
		Name: "inventory", Definition: model.ProjectModel{Services: []model.ServiceDefinition{resourceService("postgres", "postgres", "17", 5432)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(definition.Services) != 1 || len(sources) != 2 || len(defaults) != 0 {
		t.Fatalf("definition = %#v sources = %#v defaults = %#v", definition, sources, defaults)
	}
}

func TestRemoveSourcePrunesOwnedServicesConnectionsAndUnusedResources(t *testing.T) {
	project := model.ProjectModel{
		SuggestedName: "store", PrimaryService: "inventory",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "inventory", Kind: model.ServiceProcess},
			resourceService("postgres", "postgres", "17", 5432),
			resourceService("redis", "valkey", "8", 6379),
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "inventory"},
			{Source: "checkout", Target: "postgres"},
			{Source: "inventory", Target: "redis"},
		},
	}
	definition, sources, services, connections, err := RemoveSource(project, []model.ProjectSource{
		{Name: "store", Services: []string{"checkout"}},
		{Name: "inventory", Services: []string{"inventory"}},
	}, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "store" {
		t.Fatalf("sources = %#v", sources)
	}
	if names := serviceNames(definition.Services); strings.Join(names, ",") != "checkout,postgres" {
		t.Fatalf("remaining services = %v", names)
	}
	if strings.Join(services, ",") != "inventory,redis" {
		t.Fatalf("removed services = %v", services)
	}
	if len(definition.Connections) != 1 || definition.Connections[0].Target != "postgres" || len(connections) != 2 {
		t.Fatalf("remaining connections = %#v, removed = %#v", definition.Connections, connections)
	}
	if definition.PrimaryService != "checkout" {
		t.Fatalf("primary service = %q", definition.PrimaryService)
	}
}

func TestRemoveSourceRejectsTheOnlyOrUnknownSource(t *testing.T) {
	if _, _, _, _, err := RemoveSource(model.ProjectModel{}, []model.ProjectSource{{Name: "store"}}, "store"); err == nil || !strings.Contains(err.Error(), "retain at least one") {
		t.Fatalf("last source error = %v", err)
	}
	if _, _, _, _, err := RemoveSource(model.ProjectModel{}, []model.ProjectSource{{Name: "store"}, {Name: "inventory"}}, "missing"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown source error = %v", err)
	}
}

func TestCompileAllowsRemoteHTTPProvider(t *testing.T) {
	project := model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}, {Name: "payments", Kind: model.ServiceProcess}}, Connections: []model.Connection{{Source: "checkout", Target: "payments", Protocol: model.ProtocolHTTP, Environment: "PAYMENTS_URL", Required: true}}}
	sources := []model.SourceBinding{{Name: "checkout", Definition: model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess, Command: []string{"node"}}}}}}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "checkout"},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://payments.qa.example.com", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}},
	}
	result := Compile(project, sources, bindings)
	if len(result.Issues) != 0 || len(result.Definition.Services) != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestCompileAllowsMockHTTPProviderAndPrunesItsPrivateDependencies(t *testing.T) {
	project := model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "inventory", Kind: model.ServiceProcess},
			resourceService("inventory-db", "postgres", "17", 5432),
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "inventory", Protocol: model.ProtocolHTTP, Required: true},
			{Source: "inventory", Target: "inventory-db", Protocol: model.ProtocolTCP, Required: true},
		},
	}
	sources := []model.SourceBinding{{Name: "store", Definition: model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}}}}
	result := Compile(project, sources, []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "store"},
		{Service: "inventory", Provider: model.ProviderMock, Mock: &model.MockTarget{Scenario: "sold-out"}},
		{Service: "inventory-db", Provider: model.ProviderContainer},
	})
	if len(result.Issues) != 0 {
		t.Fatalf("issues = %#v", result.Issues)
	}
	if len(result.Definition.Services) != 2 || result.Definition.Services[0].Name != "checkout" || result.Definition.Services[1].Name != "inventory" {
		t.Fatalf("effective services = %#v", result.Definition.Services)
	}
	if len(result.Definition.Connections) != 1 || result.Definition.Connections[0].Target != "inventory" {
		t.Fatalf("effective connections = %#v", result.Definition.Connections)
	}
}

func TestCompileRejectsIncompleteMockProvider(t *testing.T) {
	project := model.ProjectModel{Services: []model.ServiceDefinition{{Name: "inventory", Kind: model.ServiceProcess}}}
	result := Compile(project, nil, []model.ComponentBinding{{Service: "inventory", Provider: model.ProviderMock}})
	if len(result.Issues) != 1 || result.Issues[0].Code != "INVALID_MOCK" {
		t.Fatalf("issues = %#v", result.Issues)
	}
}

func TestRemoteURLRejectsCredentials(t *testing.T) {
	err := ValidateRemote(&model.RemoteTarget{URL: "https://token@example.com", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly})
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestCompilePrunesContainersUsedOnlyByRemoteServices(t *testing.T) {
	project := model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "payments", Kind: model.ServiceProcess},
			resourceService("checkout-db", "postgres", "17", 5432),
			resourceService("payments-db", "postgres", "17", 5432),
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "checkout-db"},
			{Source: "checkout", Target: "payments"},
			{Source: "payments", Target: "payments-db"},
		},
	}
	sources := []model.SourceBinding{{Name: "apps", Definition: model.ProjectModel{Services: []model.ServiceDefinition{{Name: "checkout", Kind: model.ServiceProcess}}}}}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "apps"},
		{Service: "payments", Provider: model.ProviderRemote, Remote: &model.RemoteTarget{URL: "https://payments.qa.example.test", Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly}},
		{Service: "checkout-db", Provider: model.ProviderContainer},
		{Service: "payments-db", Provider: model.ProviderContainer},
	}
	result := Compile(project, sources, bindings)
	if len(result.Issues) != 0 {
		t.Fatalf("issues = %#v", result.Issues)
	}
	var names []string
	for _, service := range result.Definition.Services {
		names = append(names, service.Name)
	}
	if strings.Join(names, ",") != "checkout,payments,checkout-db" {
		t.Fatalf("effective services = %v", names)
	}
}

func TestRefreshDiscoveredTopologyReplacesStoredConnectionsFromCurrentSources(t *testing.T) {
	project := model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "orders", Kind: model.ServiceProcess},
			resourceService("retired-db", "postgres", "17", 5432),
			resourceService("redis", "valkey", "8", 6379),
		},
		Connections: []model.Connection{
			{Source: "external", Target: "checkout", Protocol: model.ProtocolHTTP},
			{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true},
			{Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey", Environment: "REDIS_URL", Required: true},
			{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey", Environment: "REDIS_URL", Required: true},
		},
	}
	current := []model.SourceBinding{{Name: "apps", Definition: model.ProjectModel{
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "orders", Kind: model.ServiceProcess},
			resourceService("inventory-postgres", "postgres", "17", 5432),
			resourceService("redis", "valkey", "8", 6379),
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true},
			{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey", Environment: "REDIS_URL", Required: true},
		},
	}}}
	bindings := []model.ComponentBinding{
		{Service: "checkout", Provider: model.ProviderLocal, Source: "apps"},
		{Service: "orders", Provider: model.ProviderLocal, Source: "apps"},
		{Service: "redis", Provider: model.ProviderContainer},
		{Service: "retired-db", Provider: model.ProviderContainer},
	}

	refreshed, refreshedBindings, err := RefreshDiscoveredTopology(project, current, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(serviceNames(refreshed.Services), ","); got != "checkout,inventory-postgres,orders,redis" {
		t.Fatalf("refreshed services = %s", got)
	}
	var edges []string
	for _, connection := range refreshed.Connections {
		edges = append(edges, connection.Source+":"+connection.Target)
	}
	if strings.Join(edges, ",") != "checkout:orders,orders:redis" {
		t.Fatalf("refreshed edges = %v", edges)
	}
	var gotBindings []string
	for _, binding := range refreshedBindings {
		gotBindings = append(gotBindings, binding.Service+":"+string(binding.Provider))
	}
	if strings.Join(gotBindings, ",") != "checkout:local,inventory-postgres:container,orders:local,redis:container" {
		t.Fatalf("refreshed bindings = %v", gotBindings)
	}
}

func resourceService(name, resourceType, version string, port int) model.ServiceDefinition {
	return model.ServiceDefinition{Name: name, Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: resourceType, Version: version}, Port: port}
}

func serviceNames(services []model.ServiceDefinition) []string {
	result := make([]string, 0, len(services))
	for _, service := range services {
		result = append(result, service.Name)
	}
	return result
}
