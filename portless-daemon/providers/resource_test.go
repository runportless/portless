package providers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

type fixturePlugin struct {
	descriptor  Descriptor
	plan        ContainerPlan
	panicDetect bool
	panicPlan   bool
	panicBind   bool
}

func (plugin fixturePlugin) Descriptor() Descriptor { return plugin.descriptor }

func (plugin fixturePlugin) Detect(context.Context, Workspace, []Consumer) (Findings, error) {
	if plugin.panicDetect {
		panic("detect bug")
	}
	return Findings{}, nil
}

func (plugin fixturePlugin) Plan(model.ResourceDefinition) (ContainerPlan, error) {
	if plugin.panicPlan {
		panic("plan bug")
	}
	return plugin.plan, nil
}

func (plugin fixturePlugin) Bind(context BindingContext) (BindingResult, error) {
	if plugin.panicBind {
		panic("bind bug")
	}
	if !context.Active {
		return BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	return BindingResult{
		Values: map[string]string{context.Environment: "fixture://active"}, SafeValues: map[string]string{context.Environment: "fixture://active"},
	}, nil
}

type fixtureWorkspace struct{}

func (fixtureWorkspace) Root() string                                     { return "/workspace" }
func (fixtureWorkspace) Files() []string                                  { return nil }
func (fixtureWorkspace) Exists(string) bool                               { return false }
func (fixtureWorkspace) IsDir(string) bool                                { return false }
func (fixtureWorkspace) ReadFile(context.Context, string) ([]byte, error) { return nil, nil }

type changingPlanPlugin struct {
	calls int
}

type leakingBindingPlugin struct {
	fixturePlugin
}

func (plugin leakingBindingPlugin) Bind(context BindingContext) (BindingResult, error) {
	value := context.TargetEnvironment["PASSWORD"]
	return BindingResult{Values: map[string]string{context.Environment: value}, SafeValues: map[string]string{context.Environment: value}}, nil
}

func (*changingPlanPlugin) Descriptor() Descriptor {
	return Descriptor{ID: "changing", DefaultVersion: "1"}
}

func (*changingPlanPlugin) Detect(context.Context, Workspace, []Consumer) (Findings, error) {
	return Findings{}, nil
}

func (plugin *changingPlanPlugin) Plan(definition model.ResourceDefinition) (ContainerPlan, error) {
	plugin.calls++
	return ContainerPlan{
		Image: "docker.io/example/changing:" + definition.Version, ClientPort: 7000 + plugin.calls,
		Environment: []EnvironmentVariable{{Name: "MODE", Value: "stable"}},
		Readiness:   Readiness{Kind: "tcp", Timeout: time.Minute, Interval: time.Second},
	}, nil
}

func (*changingPlanPlugin) Bind(context BindingContext) (BindingResult, error) {
	if !context.Active {
		return BindingResult{SafeValues: map[string]string{context.Environment: "not active"}}, nil
	}
	return BindingResult{Values: map[string]string{context.Environment: "changing://active"}, SafeValues: map[string]string{context.Environment: "changing://active"}}, nil
}

func validFixturePlugin() fixturePlugin {
	return fixturePlugin{
		descriptor: Descriptor{ID: "fixture", Aliases: []string{"fixture-db"}, DefaultVersion: "1"},
		plan: ContainerPlan{
			Image: "docker.io/example/fixture:1", ClientPort: 1234,
			Readiness: Readiness{Kind: "tcp", Timeout: time.Minute, Interval: time.Second},
		},
	}
}

func TestRegistryCanonicalizesAliasesAndValidatesServices(t *testing.T) {
	registry, err := NewRegistry(validFixturePlugin())
	if err != nil {
		t.Fatal(err)
	}
	definition, port, err := registry.Resolve("fixture-db", "")
	if err != nil {
		t.Fatal(err)
	}
	if definition.Type != "fixture" || definition.Version != "1" || port != 1234 {
		t.Fatalf("resolved definition = %#v port=%d", definition, port)
	}
	service := model.ServiceDefinition{Name: "fixture", Kind: model.ServiceResource, Resource: &definition, Port: port}
	if _, err := registry.Plan(service); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Plan(model.ServiceDefinition{Name: "fixture", Kind: model.ServiceResource, Resource: &definition, Port: 9999}); err == nil || !strings.Contains(err.Error(), "requires 1234") {
		t.Fatalf("port mismatch error = %v", err)
	}
}

func TestRegistryRejectsDuplicateAliasesAndInvalidPlans(t *testing.T) {
	first := validFixturePlugin()
	second := validFixturePlugin()
	second.descriptor = Descriptor{ID: "other", Aliases: []string{"fixture"}, DefaultVersion: "1"}
	if _, err := NewRegistry(first, second); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate alias error = %v", err)
	}
	invalid := validFixturePlugin()
	invalid.plan.Image = "fixture:latest"
	if _, err := NewRegistry(invalid); err == nil || !strings.Contains(err.Error(), "fully qualified") {
		t.Fatalf("invalid plan error = %v", err)
	}
	invalid = validFixturePlugin()
	invalid.plan.Volumes = []Volume{{Key: "data", Path: "/data"}, {Key: "data", Path: "/cache"}}
	if _, err := NewRegistry(invalid); err == nil || !strings.Contains(err.Error(), "duplicate container volume key") {
		t.Fatalf("duplicate volume error = %v", err)
	}
	invalid = validFixturePlugin()
	invalid.plan.Environment = []EnvironmentVariable{{Name: "PASSWORD", SecretBytes: 8}}
	if _, err := NewRegistry(invalid); err == nil || !strings.Contains(err.Error(), "invalid value source") {
		t.Fatalf("weak generated secret error = %v", err)
	}
}

func TestRegistryAcceptsMultipleNamedVolumes(t *testing.T) {
	plugin := validFixturePlugin()
	plugin.plan.Volumes = []Volume{{Key: "data", Path: "/var/lib/fixture"}, {Key: "archive", Path: "/var/lib/archive"}}
	registry, err := NewRegistry(plugin)
	if err != nil {
		t.Fatal(err)
	}
	definition, port, err := registry.Resolve("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Plan(model.ServiceDefinition{Name: "fixture", Kind: model.ServiceResource, Resource: &definition, Port: port})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Volumes) != 2 || plan.Volumes[1].Key != "archive" {
		t.Fatalf("volumes = %#v", plan.Volumes)
	}
}

func TestRegistryCachesAndCopiesVersionPlans(t *testing.T) {
	plugin := &changingPlanPlugin{}
	registry, err := NewRegistry(plugin)
	if err != nil {
		t.Fatal(err)
	}
	definition, port, err := registry.Resolve("changing", "2")
	if err != nil {
		t.Fatal(err)
	}
	service := model.ServiceDefinition{Name: "changing", Kind: model.ServiceResource, Resource: &definition, Port: port}
	first, err := registry.Plan(service)
	if err != nil {
		t.Fatal(err)
	}
	first.Environment[0].Value = "mutated"
	second, err := registry.Plan(service)
	if err != nil {
		t.Fatal(err)
	}
	if plugin.calls != 2 || second.ClientPort != port || second.Environment[0].Value != "stable" {
		t.Fatalf("cached plan calls=%d port=%d plan=%#v", plugin.calls, port, second)
	}
}

func TestRegistryContainsPluginPanics(t *testing.T) {
	panickingPlan := validFixturePlugin()
	panickingPlan.panicPlan = true
	if _, err := NewRegistry(panickingPlan); err == nil || !strings.Contains(err.Error(), "panic: plan bug") {
		t.Fatalf("plan panic error = %v", err)
	}

	panickingDetect := validFixturePlugin()
	panickingDetect.panicDetect = true
	registry, err := NewRegistry(panickingDetect)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Detect(context.Background(), "fixture", fixtureWorkspace{}, nil); err == nil || !strings.Contains(err.Error(), "panic: detect bug") {
		t.Fatalf("detect panic error = %v", err)
	}

	panickingBind := validFixturePlugin()
	panickingBind.panicBind = true
	registry, err = NewRegistry(panickingBind)
	if err != nil {
		t.Fatal(err)
	}
	definition, port, _ := registry.Resolve("fixture", "")
	service := model.ServiceDefinition{Name: "fixture", Kind: model.ServiceResource, Resource: &definition, Port: port}
	connection := model.Connection{Source: "api", Target: "fixture", Protocol: model.ProtocolTCP, Binding: "fixture", Environment: "FIXTURE_URL"}
	if _, err := registry.Bind(service, connection, BindingContext{Environment: "FIXTURE_URL", Host: "fixture.test", Port: port, Active: true}); err == nil || !strings.Contains(err.Error(), "panic: bind bug") {
		t.Fatalf("bind panic error = %v", err)
	}
}

func TestRegistryRejectsSecretsInSafeBindingValues(t *testing.T) {
	plugin := leakingBindingPlugin{fixturePlugin: validFixturePlugin()}
	plugin.plan.Environment = []EnvironmentVariable{{Name: "PASSWORD", SecretBytes: 24}}
	registry, err := NewRegistry(plugin)
	if err != nil {
		t.Fatal(err)
	}
	definition, port, err := registry.Resolve("fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	service := model.ServiceDefinition{Name: "fixture", Kind: model.ServiceResource, Resource: &definition, Port: port}
	connection := model.Connection{Source: "api", Target: "fixture", Protocol: model.ProtocolTCP, Binding: "fixture", Environment: "FIXTURE_URL"}
	_, err = registry.Bind(service, connection, BindingContext{
		Environment: "FIXTURE_URL", Host: "fixture.test", Port: port, TargetEnvironment: map[string]string{"PASSWORD": "generated-secret-value"}, Active: true,
	})
	if err == nil || !strings.Contains(err.Error(), "exposed PASSWORD") {
		t.Fatalf("safe secret exposure error = %v", err)
	}
}
