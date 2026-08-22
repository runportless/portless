package controlplane

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
	resourcebuiltin "github.com/runportless/portless/portless-daemon/providers/builtin"
)

func TestValidateExperimentScopeUsesConfiguredDirectedConnections(t *testing.T) {
	definition := model.ProjectModel{
		PrimaryService: "checkout",
		Services: []model.ServiceDefinition{
			{Name: "checkout", Kind: model.ServiceProcess},
			{Name: "orders", Kind: model.ServiceProcess},
			{Name: "postgres", Kind: model.ServiceResource},
			{Name: "redis", Kind: model.ServiceResource},
		},
		Connections: []model.Connection{
			{Source: "checkout", Target: "orders", Protocol: model.ProtocolHTTP},
			{Source: "orders", Target: "postgres", Protocol: model.ProtocolTCP, Binding: "postgres"},
			{Source: "orders", Target: "redis", Protocol: model.ProtocolTCP, Binding: "valkey"},
		},
	}

	for _, scope := range [][2]string{
		{"external", "checkout"},
		{"checkout", "orders"},
		{"orders", "postgres"},
		{"orders", "redis"},
	} {
		if err := validateExperimentScope(definition, scope[0], scope[1], false); err != nil {
			t.Fatalf("valid scope %s → %s was rejected: %v", scope[0], scope[1], err)
		}
	}

	err := validateExperimentScope(definition, "orders", "checkout", false)
	if err == nil {
		t.Fatal("reverse connection was accepted")
	}
	for _, expected := range []string{
		"orders → checkout is not a configured connection",
		"external → checkout",
		"checkout → orders",
		"orders → postgres",
		"orders → redis",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not contain %q", err, expected)
		}
	}

	err = validateExperimentScope(definition, "external", "orders", false)
	if err == nil || !strings.Contains(err.Error(), "external → orders is not a configured connection") {
		t.Fatalf("non-primary external connection error = %v", err)
	}
}

func TestApplyBindingUsesDNSHostForGenericTCP(t *testing.T) {
	service := &Service{resources: resourcebuiltin.Registry()}
	binding, err := service.connectionBinding(model.ServiceDefinition{Name: "broker", Kind: model.ServiceProcess}, model.Connection{Protocol: model.ProtocolTCP, Environment: "BROKER_ADDRESS"}, "broker.via-orders.local.store.portless.test", 4222, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Values["BROKER_ADDRESS"] != "broker.via-orders.local.store.portless.test:4222" {
		t.Fatalf("generic TCP binding = %q", binding.Values["BROKER_ADDRESS"])
	}
}

func TestResourceBindingInjectsMultipleVariablesAndRedactsSecrets(t *testing.T) {
	service := &Service{resources: resourcebuiltin.Registry()}
	target := model.ServiceDefinition{
		Name: "postgres", Kind: model.ServiceResource,
		Resource: &model.ResourceDefinition{Type: "postgres", Version: "17"}, Port: 5432,
	}
	connection := model.Connection{
		Source: "inventory", Target: "postgres", Protocol: model.ProtocolTCP,
		Binding: "postgres", Environment: "SPRING_DATASOURCE_URL", Required: true,
	}
	binding, err := service.connectionBinding(target, connection, "postgres.via-inventory.local.store.portless.test", 5432, map[string]string{
		"POSTGRES_DB": "portless", "POSTGRES_USER": "portless", "POSTGRES_PASSWORD": "generated-secret",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.Values) != 3 || binding.Values["SPRING_DATASOURCE_USERNAME"] != "portless" || !strings.HasPrefix(binding.Values["SPRING_DATASOURCE_URL"], "jdbc:postgresql://") {
		t.Fatalf("resource binding values = %#v", binding.Values)
	}
	if binding.SafeValues["SPRING_DATASOURCE_PASSWORD"] != "••••••••" {
		t.Fatalf("resource binding exposed its secret: %#v", binding.SafeValues)
	}
	for _, value := range binding.SafeValues {
		if strings.Contains(value, "generated-secret") {
			t.Fatalf("resource binding exposed its secret: %#v", binding.SafeValues)
		}
	}
}

func TestApplicationProcessHelper(t *testing.T) {
	if os.Getenv("PORTLESS_APPLICATION_TEST_HELPER") != "1" {
		return
	}
	for index, argument := range os.Args {
		if argument != "--debug" || index+1 >= len(os.Args) {
			continue
		}
		debugListener, err := net.Listen("tcp", os.Args[index+1])
		if err != nil {
			os.Exit(4)
		}
		debugServer := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/json/list" {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`[{"type":"node","title":"checkout"}]`))
		})}
		go func() { _ = debugServer.Serve(debugListener) }()
		break
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		os.Exit(2)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"service":"checkout"}`))
	})}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		os.Exit(3)
	}
}

func nestFixture(t *testing.T, root string) string {
	return nestNamedFixture(t, root, "checkout")
}

func nestNamedFixture(t *testing.T, root, name string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"name":"` + name + `","scripts":{"start:dev":"node server.js"},"dependencies":{"@nestjs/core":"1.0.0"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func sourcePath(sources []model.SourceBinding, name string) string {
	for _, source := range sources {
		if strings.EqualFold(source.Name, name) {
			return source.Path
		}
	}
	return ""
}
