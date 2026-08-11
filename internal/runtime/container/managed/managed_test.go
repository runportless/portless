package managed

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/portless-run/portless/internal/runtime/container"
)

type testEngine struct{}

func (testEngine) Name() container.RuntimeName { return container.RuntimeDocker }
func (testEngine) Binary() string              { return "docker" }
func (testEngine) Probe(context.Context) container.ProbeResult {
	return container.ProbeResult{Name: container.RuntimeDocker, State: "ready"}
}
func (testEngine) StartHost(context.Context) container.ProbeResult {
	return container.ProbeResult{Name: container.RuntimeDocker, State: "ready"}
}
func (testEngine) ResourceExists(context.Context, string, string) bool { return false }
func (testEngine) VolumeMount(volume, path string) string              { return volume + ":" + path }

func TestGeneratedEnvironmentPersistsAcrossContainerRecreation(t *testing.T) {
	manager := New(testEngine{}, "installation", filepath.Join(t.TempDir(), "tmp"))
	first, path, err := manager.environmentFile("abcdef123456", "postgres", map[string]string{"POSTGRES_PASSWORD": "first-secret"})
	if err != nil {
		t.Fatal(err)
	}
	second, secondPath, err := manager.environmentFile("abcdef123456", "postgres", map[string]string{"POSTGRES_PASSWORD": "different-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if first["POSTGRES_PASSWORD"] != "first-secret" || second["POSTGRES_PASSWORD"] != "first-secret" || secondPath != path {
		t.Fatalf("credentials were not stable: first=%v second=%v paths=%q/%q", first, second, path, secondPath)
	}
}

func TestPrivateLabelsAreRedactedFromErrors(t *testing.T) {
	arguments := redactArguments([]string{"run", labelInstall + "=secret-install", labelProject + "=secret-project", "image"})
	if arguments[1] != labelInstall+"=<private>" || arguments[2] != labelProject+"=<private>" {
		t.Fatalf("arguments not redacted: %#v", arguments)
	}
}
