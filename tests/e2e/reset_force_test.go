//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/store"
)

func TestForcedResetRecoversActiveIncompatibleTopology(t *testing.T) {
	binary := e2eBinary(t)
	// Keep the data path short enough for the macOS Unix-domain socket limit.
	home, err := os.MkdirTemp("/tmp", "portless-reset-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	seedActiveIncompatibleTopology(t, home)
	t.Cleanup(func() {
		if output, err := runCLI(binary, home, "daemon", "stop", "--force"); err != nil {
			t.Errorf("stop isolated daemon: %v\n%s", err, output)
		}
	})

	// Start the real daemon and prove the ordinary lifecycle path cannot decode
	// the intentionally old model. This recreates the state that originally
	// trapped reset behind the down command.
	downOutput, downErr := runCLI(binary, home, "down", "--all")
	if downErr == nil {
		t.Fatalf("down --all unexpectedly accepted incompatible topology:\n%s", downOutput)
	}
	for _, expected := range []string{"INCOMPATIBLE_STATE", "portless reset --force --yes"} {
		if !strings.Contains(downOutput, expected) {
			t.Fatalf("down --all output does not contain %q:\n%s\nisolated daemon log:\n%s", expected, downOutput, readDaemonLog(home))
		}
	}

	resetOutput, err := runCLI(binary, home, "reset", "--force", "--yes")
	if err != nil {
		t.Fatalf("forced reset failed: %v\n%s", err, resetOutput)
	}
	if !strings.Contains(resetOutput, "Portless reset complete.") {
		t.Fatalf("forced reset did not report completion:\n%s", resetOutput)
	}

	projectsOutput, err := runCLI(binary, home, "--json", "project", "list")
	if err != nil {
		t.Fatalf("list projects after reset: %v\n%s", err, projectsOutput)
	}
	var projects struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal([]byte(projectsOutput), &projects); err != nil {
		t.Fatalf("decode project inventory after reset: %v\n%s", err, projectsOutput)
	}
	if len(projects.Projects) != 0 {
		t.Fatalf("reset left %d projects behind: %s", len(projects.Projects), projectsOutput)
	}
}

func readDaemonLog(home string) string {
	content, err := os.ReadFile(filepath.Join(home, "daemon.log"))
	if err != nil {
		return "<unavailable: " + err.Error() + ">"
	}
	return string(content)
}

func seedActiveIncompatibleTopology(t *testing.T, home string) {
	t.Helper()
	ctx := context.Background()
	controlStore, err := store.Open(filepath.Join(home, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	definition := model.ProjectModel{
		SuggestedName: "store",
		Services:      []model.ServiceDefinition{{Name: "api", Kind: model.ServiceProcess, Required: true}},
	}
	if _, err := controlStore.CreateProject(ctx, "store", definition, nil); err != nil {
		_ = controlStore.Close()
		t.Fatal(err)
	}
	if _, err := controlStore.CreateEnvironment(ctx, "store", "local", definition, nil, nil); err != nil {
		_ = controlStore.Close()
		t.Fatal(err)
	}
	if err := controlStore.SetEnvironmentStatus(ctx, "store", "local", model.EnvironmentHealthy, "legacy runtime is active"); err != nil {
		_ = controlStore.Close()
		t.Fatal(err)
	}
	legacyModel, err := json.Marshal(definition)
	if err != nil {
		_ = controlStore.Close()
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE projects SET model_json = ?`, legacyModel); err != nil {
		_ = controlStore.Close()
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE environments SET model_json = ?`, legacyModel); err != nil {
		_ = controlStore.Close()
		t.Fatal(err)
	}
	if err := controlStore.Close(); err != nil {
		t.Fatal(err)
	}
}

func e2eBinary(t *testing.T) string {
	t.Helper()
	if binary := strings.TrimSpace(os.Getenv("PORTLESS_E2E_BINARY")); binary != "" {
		absolute, err := filepath.Abs(binary)
		if err != nil {
			t.Fatal(err)
		}
		return absolute
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate E2E test source")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), "portless")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/portless")
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build E2E binary: %v\n%s", err, output)
	}
	return binary
}

func runCLI(binary, home string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = filepath.Dir(home)
	command.Env = isolatedEnvironment(home)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func isolatedEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "PORTLESS_HOME=") || strings.HasPrefix(entry, "NO_COLOR=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "PORTLESS_HOME="+home, "NO_COLOR=1")
}
