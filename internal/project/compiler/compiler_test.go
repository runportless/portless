package compiler

import (
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
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
			{Name: "checkout-db", Kind: model.ServiceContainer},
			{Name: "payments-db", Kind: model.ServiceContainer},
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
