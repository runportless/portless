package debuglaunch

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func TestPrepareBuildsFrameworkSpecificLoopbackCommands(t *testing.T) {
	tests := []struct {
		name       string
		capability model.DebugCapability
		want       []string
	}{
		{
			name: "node",
			capability: model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNodeDirect,
				Command: []string{"node", "dist/main.js"}},
			want: []string{"node", "--inspect=127.0.0.1:43123", "dist/main.js"},
		},
		{
			name: "nest",
			capability: model.DebugCapability{Adapter: model.DebugNodeInspector, Launcher: model.DebugNestCLI,
				Command: []string{"npm", "exec", "--", "nest", "start", "--watch"}},
			want: []string{"npm", "exec", "--", "nest", "start", "--watch", "--debug", "127.0.0.1:43123"},
		},
		{
			name: "maven",
			capability: model.DebugCapability{Adapter: model.DebugJDWP, Launcher: model.DebugSpringMaven,
				Command: []string{"./mvnw", "-pl", "inventory", "spring-boot:run"}},
			want: []string{"./mvnw", "-pl", "inventory", "spring-boot:run", "-Dspring-boot.run.jvmArguments=-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:43123"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Prepare(&test.capability, 43123, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Command, test.want) {
				t.Fatalf("command = %#v, want %#v", result.Command, test.want)
			}
			if result.Debugger.Host != Host || result.Debugger.Port != 43123 || result.Debugger.State != "starting" {
				t.Fatalf("debugger = %#v", result.Debugger)
			}
		})
	}
}

func TestPrepareGradleUsesPrivateInitScript(t *testing.T) {
	root := t.TempDir()
	capability := model.DebugCapability{Adapter: model.DebugJDWP, Launcher: model.DebugSpringGradle, Command: []string{"./gradlew", ":inventory:bootRun"}}
	result, err := Prepare(&capability, 43123, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Command) != 4 || result.Command[0] != "./gradlew" || result.Command[1] != "--init-script" || result.Command[3] != ":inventory:bootRun" {
		t.Fatalf("Gradle command = %#v", result.Command)
	}
	if filepath.Dir(result.Command[2]) != root || result.Environment["PORTLESS_GRADLE_DEBUG_TASK"] != ":inventory:bootRun" || result.Environment["PORTLESS_GRADLE_DEBUG_PORT"] != "43123" {
		t.Fatalf("Gradle launch = %#v", result)
	}
	content, err := os.ReadFile(result.Command[2])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "suspend = false") || !strings.Contains(string(content), "task.debugOptions") {
		t.Fatalf("Gradle init script = %s", content)
	}
	info, err := os.Stat(result.Command[2])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Gradle init script permissions = %v", info.Mode().Perm())
	}
}

func TestWaitRecognizesNodeInspectorAndJDWPWithoutSpeakingJDWP(t *testing.T) {
	nodeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nodeServer := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})}
	go func() { _ = nodeServer.Serve(nodeListener) }()
	defer nodeServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Wait(ctx, model.DebuggerRuntime{Adapter: model.DebugNodeInspector, Host: Host, Port: nodeListener.Addr().(*net.TCPAddr).Port}); err != nil {
		t.Fatal(err)
	}

	jdwpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer jdwpListener.Close()
	if err := Wait(ctx, model.DebuggerRuntime{Adapter: model.DebugJDWP, Host: Host, Port: jdwpListener.Addr().(*net.TCPAddr).Port}); err != nil {
		t.Fatal(err)
	}
}
