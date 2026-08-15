package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/resource"
	"github.com/portless-run/portless/internal/runtime/container"
	"github.com/portless-run/portless/internal/runtime/logstore"
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

type binaryEngine struct {
	testEngine
	binary string
}

func (engine binaryEngine) Binary() string { return engine.binary }

type collisionEngine struct {
	binaryEngine
}

func (collisionEngine) ResourceExists(_ context.Context, kind, _ string) bool {
	return kind == "container"
}

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

func TestResourceEnvironmentPersistsSecretsAndRejectsPlanDrift(t *testing.T) {
	manager := New(testEngine{}, "installation", filepath.Join(t.TempDir(), "tmp"))
	specifications := []resource.EnvironmentVariable{
		{Name: "DATABASE", Value: "portless"},
		{Name: "PASSWORD", SecretBytes: 24},
	}
	first, path, err := manager.resourceEnvironmentFile("abcdef123456", "database", specifications)
	if err != nil {
		t.Fatal(err)
	}
	second, secondPath, err := manager.resourceEnvironmentFile("abcdef123456", "database", specifications)
	if err != nil {
		t.Fatal(err)
	}
	if first["PASSWORD"] == "" || first["PASSWORD"] != second["PASSWORD"] || path != secondPath || second["DATABASE"] != "portless" {
		t.Fatalf("resource environment was not stable: first=%#v second=%#v", first, second)
	}
	drifted := []resource.EnvironmentVariable{{Name: "DATABASE", Value: "changed"}, {Name: "PASSWORD", SecretBytes: 24}}
	if _, _, err := manager.resourceEnvironmentFile("abcdef123456", "database", drifted); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("plan drift error = %v", err)
	}
}

func TestPrivateLabelsAreRedactedFromErrors(t *testing.T) {
	arguments := redactArguments([]string{"run", labelInstall + "=secret-install", labelEnvironment + "=secret-environment", "image"})
	if arguments[1] != labelInstall+"=<private>" || arguments[2] != labelEnvironment+"=<private>" {
		t.Fatalf("arguments not redacted: %#v", arguments)
	}
}

func TestLongResourceNamesRemainStableAndCollisionResistant(t *testing.T) {
	first := resourceName("portless", strings.Repeat("environment", 8), "database", "data", "12345678")
	second := resourceName("portless", strings.Repeat("environment", 8), "database", "archive", "12345678")
	if len(first) > 63 || len(second) > 63 || first == second || first != resourceName("portless", strings.Repeat("environment", 8), "database", "data", "12345678") {
		t.Fatalf("resource names are not bounded, distinct, and stable: %q %q", first, second)
	}
}

func TestStartRefusesToRemoveUnownedContainerNameCollision(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "container-engine")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := New(collisionEngine{binaryEngine{binary: script}}, "installation", filepath.Join(root, "tmp"))
	service := model.ServiceDefinition{
		Name: "broker", Kind: model.ServiceResource,
		Resource: &model.ResourceDefinition{Type: "fixture", Version: "1"}, Port: 7777,
	}
	plan := resource.ContainerPlan{
		Image: "docker.io/example/fixture:1", ClientPort: 7777,
		Readiness: resource.Readiness{Kind: "tcp", Timeout: time.Minute, Interval: time.Second},
	}
	if _, err := manager.Start(context.Background(), "local", "abcdef123456", service, plan, 1, filepath.Join(root, "logs")); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("unowned name collision error = %v", err)
	}
}

func TestContainerLogCollectorWritesStructuredStreams(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "container-logs")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'container ready\\n'\nprintf 'container warning\\n' >&2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := New(binaryEngine{binary: script}, "installation", filepath.Join(root, "tmp"))
	directory := filepath.Join(root, "logs", "postgres", "1")
	if err := manager.startLogCollector("portless-postgres", "postgres", 1, directory); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, err := logstore.Read(filepath.Join(root, "logs"), []string{"postgres"}, 10, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 2 {
			manager.mu.Lock()
			collecting := len(manager.collectors) != 0
			manager.mu.Unlock()
			if collecting {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			messages := map[string]string{}
			for _, entry := range entries {
				if entry.Service != "postgres" || entry.Generation != 1 {
					t.Fatalf("unexpected structured container log: %#v", entry)
				}
				messages[entry.Stream] = entry.Message
			}
			if messages["stdout"] != "container ready" || messages["stderr"] != "container warning" {
				t.Fatalf("unexpected structured container logs: %#v", entries)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("container logs were not collected: %#v", entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
