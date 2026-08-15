package builtin

import (
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/resource"
)

func TestBuiltinsExposeValidatedRuntimePlans(t *testing.T) {
	registry := Registry()
	wanted := map[string]struct {
		version string
		port    int
	}{
		"mysql":    {version: "8.4", port: 3306},
		"nats":     {version: "2", port: 4222},
		"postgres": {version: "17", port: 5432},
		"valkey":   {version: "8", port: 6379},
	}
	for _, resourceType := range registry.IDs() {
		expected, exists := wanted[resourceType]
		if !exists {
			t.Errorf("unexpected resource plugin %s", resourceType)
			continue
		}
		definition, port, err := registry.Resolve(resourceType, "")
		if err != nil {
			t.Fatal(err)
		}
		service := model.ServiceDefinition{Name: resourceType, Kind: model.ServiceResource, Resource: &definition, Port: port}
		plan, err := registry.Plan(service)
		if err != nil {
			t.Fatal(err)
		}
		if definition.Version != expected.version || port != expected.port || plan.ClientPort != expected.port || !strings.HasPrefix(plan.Image, "docker.io/") {
			t.Errorf("%s definition=%#v plan=%#v", resourceType, definition, plan)
		}
		delete(wanted, resourceType)
	}
	if len(wanted) != 0 {
		t.Fatalf("missing resource plugins: %v", wanted)
	}
}

func TestBuiltinsGenerateResourceSpecificBindingsAndSafeValues(t *testing.T) {
	registry := Registry()
	tests := []struct {
		resourceType string
		environment  string
		target       map[string]string
		prefix       string
		keys         int
	}{
		{resourceType: "postgres", environment: "DATABASE_URL", target: map[string]string{"POSTGRES_USER": "portless", "POSTGRES_PASSWORD": "secret", "POSTGRES_DB": "portless"}, prefix: "postgresql://", keys: 1},
		{resourceType: "postgres", environment: "SPRING_DATASOURCE_URL", target: map[string]string{"POSTGRES_USER": "portless", "POSTGRES_PASSWORD": "secret", "POSTGRES_DB": "portless"}, prefix: "jdbc:postgresql://", keys: 3},
		{resourceType: "mysql", environment: "DATABASE_URL", target: map[string]string{"MYSQL_USER": "portless", "MYSQL_PASSWORD": "secret", "MYSQL_DATABASE": "portless"}, prefix: "mysql://", keys: 1},
		{resourceType: "valkey", environment: "REDIS_URL", prefix: "redis://", keys: 1},
		{resourceType: "nats", environment: "NATS_URL", prefix: "nats://", keys: 1},
	}
	for _, test := range tests {
		t.Run(test.resourceType+"/"+test.environment, func(t *testing.T) {
			definition, port, err := registry.Resolve(test.resourceType, "")
			if err != nil {
				t.Fatal(err)
			}
			service := model.ServiceDefinition{Name: test.resourceType, Kind: model.ServiceResource, Resource: &definition, Port: port}
			connection := model.Connection{Source: "api", Target: test.resourceType, Protocol: model.ProtocolTCP, Binding: test.resourceType, Environment: test.environment}
			binding, err := registry.Bind(service, connection, resource.BindingContext{
				Environment: test.environment, Host: "resource.portless.test", Port: port, TargetEnvironment: test.target, Active: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(binding.Values) != test.keys || len(binding.SafeValues) != test.keys || !strings.HasPrefix(binding.Values[test.environment], test.prefix) {
				t.Fatalf("binding = %#v", binding)
			}
			for key, safe := range binding.SafeValues {
				if strings.Contains(safe, "secret") {
					t.Fatalf("safe binding %s exposed a secret: %q", key, safe)
				}
			}
		})
	}
}
