package container

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/portless-run/portless/internal/model"
)

type fakeRuntime struct {
	name       RuntimeName
	probe      ProbeResult
	startCalls int
	adoptCalls int
}

func (r *fakeRuntime) Name() RuntimeName { return r.name }
func (r *fakeRuntime) Probe(context.Context) ProbeResult {
	result := r.probe
	result.Name = r.name
	return result
}
func (r *fakeRuntime) StartHost(context.Context) ProbeResult {
	r.startCalls++
	return r.Probe(context.Background())
}
func (r *fakeRuntime) Start(context.Context, string, string, model.ServiceDefinition, int64, string) (StartResult, error) {
	return StartResult{}, nil
}
func (r *fakeRuntime) Adopt(context.Context, string, string, model.ServiceDefinition, int64, string) (StartResult, error) {
	r.adoptCalls++
	return StartResult{ContainerName: "adopted", Port: 54321}, nil
}
func (r *fakeRuntime) StopEnvironment(context.Context, string, bool) error { return nil }
func (r *fakeRuntime) StopService(context.Context, string, string) error {
	return nil
}

func TestAutoSelectsFirstReadyRuntimeAndPersistsIt(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "runtime.json")
	podman := &fakeRuntime{name: RuntimePodman, probe: ProbeResult{State: "missing", Reason: "not installed"}}
	docker := &fakeRuntime{name: RuntimeDocker, probe: ProbeResult{State: "ready", Version: "29.4.0"}}

	status := NewManager(statePath, podman, docker).Status(context.Background())
	if status.Preference != RuntimeAuto || status.Selected != RuntimeDocker || status.State != "ready" {
		t.Fatalf("unexpected status: %#v", status)
	}
	content, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved persistedSelection
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Preference != RuntimeAuto || saved.Selected != RuntimeDocker {
		t.Fatalf("unexpected persisted selection: %#v", saved)
	}
}

func TestAutomaticSelectionStaysStableAcrossRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "runtime.json")
	docker := &fakeRuntime{name: RuntimeDocker, probe: ProbeResult{State: "ready"}}
	first := NewManager(statePath, docker)
	if got := first.Status(context.Background()).Selected; got != RuntimeDocker {
		t.Fatalf("selected %q", got)
	}

	docker.probe = ProbeResult{State: "failed", Reason: "engine stopped"}
	podman := &fakeRuntime{name: RuntimePodman, probe: ProbeResult{State: "ready"}}
	status := NewManager(statePath, podman, docker).Status(context.Background())
	if status.Selected != RuntimeDocker || status.State != "failed" {
		t.Fatalf("silently switched away from persisted runtime: %#v", status)
	}
}

func TestAdoptUsesThePersistedSelectedRuntime(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "runtime.json")
	docker := &fakeRuntime{name: RuntimeDocker, probe: ProbeResult{State: "ready"}}
	podman := &fakeRuntime{name: RuntimePodman, probe: ProbeResult{State: "ready"}}
	manager := NewManager(statePath, docker, podman)
	if err := manager.SetPreference(RuntimeDocker); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Adopt(context.Background(), "billing-local", "private", model.ServiceDefinition{Name: "postgres", Kind: model.ServiceContainer}, 2, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.ContainerName != "adopted" || docker.adoptCalls != 1 || podman.adoptCalls != 0 {
		t.Fatalf("adoption used the wrong runtime: result=%#v docker=%d podman=%d", result, docker.adoptCalls, podman.adoptCalls)
	}
}

func TestExplicitPreferenceCanBeChangedAndStarted(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "runtime.json")
	podman := &fakeRuntime{name: RuntimePodman, probe: ProbeResult{State: "ready"}}
	docker := &fakeRuntime{name: RuntimeDocker, probe: ProbeResult{State: "ready"}}
	manager := NewManager(statePath, podman, docker)
	if err := manager.SetPreference(RuntimeDocker); err != nil {
		t.Fatal(err)
	}
	status := manager.StartHost(context.Background())
	if status.Preference != RuntimeDocker || status.Selected != RuntimeDocker || docker.startCalls != 1 || podman.startCalls != 0 {
		t.Fatalf("wrong runtime started: status=%#v docker=%d podman=%d", status, docker.startCalls, podman.startCalls)
	}
}

func TestUnavailableStatusExplainsEveryCandidate(t *testing.T) {
	podman := &fakeRuntime{name: RuntimePodman, probe: ProbeResult{State: "missing", Reason: "not installed"}}
	docker := &fakeRuntime{name: RuntimeDocker, probe: ProbeResult{State: "failed", Reason: "engine stopped"}}
	status := NewManager(filepath.Join(t.TempDir(), "runtime.json"), podman, docker).Status(context.Background())
	if status.State != "failed" || len(status.Candidates) != 2 || status.Reason == "" {
		t.Fatalf("unexpected unavailable status: %#v", status)
	}
}
