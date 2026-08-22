package springboot

import (
	"reflect"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

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
