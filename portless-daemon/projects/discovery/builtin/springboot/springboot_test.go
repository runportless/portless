package springboot

import (
	"reflect"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestSpringConfigurationParsersReadHealthRouting(t *testing.T) {
	properties := parseSpringProperties([]byte(`
# comment
server.servlet.context-path=/inventory
management.endpoints.web.base-path: /manage
management.endpoints.web.path-mapping.health=ready
`))
	if properties["server.servlet.context-path"] != "/inventory" || properties["management.endpoints.web.path-mapping.health"] != "ready" {
		t.Fatalf("properties = %#v", properties)
	}
	yamlValues := make(map[string]string)
	if err := parseSpringYAML([]byte(`
management:
  endpoints:
    web:
      exposure:
        include: [health, info]
      path-mapping:
        health: readyz
`), yamlValues); err != nil {
		t.Fatal(err)
	}
	if yamlValues["management.endpoints.web.exposure.include"] != "health,info" || yamlValues["management.endpoints.web.path-mapping.health"] != "readyz" {
		t.Fatalf("YAML values = %#v", yamlValues)
	}
}

func TestSpringManagementPortMustShareTheApplicationPort(t *testing.T) {
	for _, value := range []string{"8081", "${MANAGEMENT_PORT}", "0"} {
		if sameSpringManagementPort(value) {
			t.Errorf("separate management port %q was accepted", value)
		}
	}
	for _, value := range []string{"${SERVER_PORT}", "${server.port}", "${SERVER_PORT:${server.port}}"} {
		if !sameSpringManagementPort(value) {
			t.Errorf("shared management port %q was rejected", value)
		}
	}
}

func TestSpringCandidateCarriesJDWPLaunchRecipe(t *testing.T) {
	command := []string{"./gradlew", ":inventory:bootRun"}
	candidate := springCandidate("inventory/build.gradle", "inventory", ".", "inventory", command, model.DebugSpringGradle,
		model.HealthCheck{Kind: "tcp", Timeout: time.Minute}, "Spring Boot Gradle plugin found")
	debug := candidate.Definition.Debug
	if debug == nil || debug.Adapter != model.DebugJDWP || debug.Launcher != model.DebugSpringGradle || !reflect.DeepEqual(debug.Command, command) {
		t.Fatalf("debug capability = %#v", debug)
	}
	command[1] = "changed"
	if debug.Command[1] != ":inventory:bootRun" {
		t.Fatal("debug command aliases the managed command slice")
	}
}
