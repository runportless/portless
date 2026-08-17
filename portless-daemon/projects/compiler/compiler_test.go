package compiler

import (
	"strings"
	"testing"

	"github.com/portless-run/portless/portless-daemon/model"
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
			resourceService("redis", "valkey", "8", 6379),
		},
		Connections: []model.Connection{
			{Source: "external", Target: "checkout", Protocol: model.ProtocolHTTP},
			{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true},
			{Source: "checkout", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey", Environment: "REDIS_URL", Required: true},
			{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey", Environment: "REDIS_URL", Required: true},
		},
	}
	current := []model.SourceBinding{{Name: "apps", Definition: model.ProjectModel{Connections: []model.Connection{
		{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP, Environment: "ORDERS_URL", Required: true},
		{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey", Environment: "REDIS_URL", Required: true},
	}}}}

	refreshed := RefreshDiscoveredTopology(project, current)
	var edges []string
	for _, connection := range refreshed.Connections {
		edges = append(edges, connection.Source+":"+connection.Target)
	}
	if strings.Join(edges, ",") != "checkout:orders,orders:redis" {
		t.Fatalf("refreshed edges = %v", edges)
	}
}

func resourceService(name, resourceType, version string, port int) model.ServiceDefinition {
	return model.ServiceDefinition{Name: name, Kind: model.ServiceResource, Resource: &model.ResourceDefinition{Type: resourceType, Version: version}, Port: port}
}
