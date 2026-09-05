package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
)

func TestSharedGitCheckoutIsPreparedAutomaticallyAndReused(t *testing.T) {
	for _, individual := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment", true: "service"}[individual], func(t *testing.T) {
			ctx := context.Background()
			app, store, root := worktreeTestService(t, "checkout")
			local := startWorktreeEnvironment(t, app, "local")
			if _, err := app.CloneEnvironment(ctx, "billing", "local", "qa"); err != nil {
				t.Fatal(err)
			}
			original := local.Sources[0].Path
			if err := app.SelectEnvironment(ctx, original, "billing", "qa"); err != nil {
				t.Fatal(err)
			}
			// Retrying an environment that previously hit a checkout conflict
			// must work directly, without a preliminary down or configuration edit.
			if err := store.SetEnvironmentStatus(ctx, "billing", "qa", model.EnvironmentFailed, "previous startup failed"); err != nil {
				t.Fatal(err)
			}
			var operation model.Operation
			var err error
			if individual {
				operation, err = app.StartService(ctx, "billing", "qa", "checkout", "test", "")
			} else {
				operation, err = app.Up(ctx, "billing", "qa", "test", "", UpOptions{})
			}
			if err != nil {
				t.Fatal(err)
			}
			completed := waitForOperation(t, app, operation)
			if completed.State != "succeeded" {
				t.Fatalf("automatic start = %#v", completed)
			}
			prepared := false
			for _, event := range completed.Events {
				prepared = prepared || event.Type == "source.prepared"
			}
			if !prepared {
				t.Fatal("startup did not report source preparation")
			}
			qa, err := app.Environment(ctx, "billing", "qa")
			if err != nil || qa.Status != model.EnvironmentHealthy || qa.Sources[0].Path == original {
				t.Fatalf("isolated environment = %#v, %v", qa, err)
			}
			checkout := qa.Sources[0].Path
			if !strings.HasSuffix(checkout, filepath.Join("apps", "checkout")) || pathInCheckout(root, checkout) {
				t.Fatalf("nested source path was not preserved in a separate worktree: %s", checkout)
			}
			if selected, err := app.EnvironmentContext(ctx, original); err != nil || selected.Environment == nil || selected.Environment.Name != "qa" {
				t.Fatalf("automatic preparation lost CLI selection: %#v, %v", selected, err)
			}
			for _, name := range []string{"local", "qa"} {
				if err := app.SelectEnvironment(ctx, original, "billing", name); err != nil {
					t.Fatalf("select %s from the original source: %v", name, err)
				}
			}
			latestLocal, _ := app.Environment(ctx, "billing", "local")
			if latestLocal.Services[0].PID != local.Services[0].PID || latestLocal.Services[0].Generation != local.Services[0].Generation {
				t.Fatal("starting the clone restarted the original process")
			}
			definition, err := store.EnvironmentModel(ctx, "billing", "qa")
			if err != nil || definition.Services[0].WorkingDirectory != checkout || definition.Services[0].ServiceDirectory != checkout {
				t.Fatalf("launch directories were not relocated: %#v, %v", definition, err)
			}
			stopped, err := app.Down(ctx, "billing", "qa", "test", "", false)
			if err != nil || waitForOperation(t, app, stopped).State != "succeeded" {
				t.Fatalf("stop clone: %v", err)
			}
			if err := os.WriteFile(filepath.Join(checkout, "local-edit.txt"), []byte("keep this edit"), 0o600); err != nil {
				t.Fatal(err)
			}
			restarted := startWorktreeEnvironment(t, app, "qa")
			if restarted.Sources[0].Path != checkout {
				t.Fatal("restart created another checkout")
			}
			if content, err := os.ReadFile(filepath.Join(checkout, "local-edit.txt")); err != nil || string(content) != "keep this edit" {
				t.Fatalf("restart discarded local edits: %q, %v", content, err)
			}
			if _, err := os.Stat(filepath.Join(original, "local-edit.txt")); !os.IsNotExist(err) {
				t.Fatalf("clone edits reached the original: %v", err)
			}
		})
	}
}

func TestConcurrentStartsShareOneWorktreePerEnvironmentRepository(t *testing.T) {
	ctx := context.Background()
	app, _, root := worktreeTestService(t, "checkout", "inventory")
	for _, name := range []string{"qa", "preview"} {
		if _, err := app.CloneEnvironment(ctx, "billing", "local", name); err != nil {
			t.Fatal(err)
		}
	}
	var operations []model.Operation
	for _, name := range []string{"local", "qa", "preview"} {
		operation, err := app.Up(ctx, "billing", name, "test", "", UpOptions{})
		if err != nil {
			t.Fatal(err)
		}
		operations = append(operations, operation)
	}
	roots := make(map[string]bool)
	for _, operation := range operations {
		if completed := waitForOperation(t, app, operation); completed.State != "succeeded" {
			t.Fatalf("concurrent start failed: %#v", completed)
		}
		environment, err := app.Environment(ctx, "billing", operation.Environment)
		if err != nil || environment.Status != model.EnvironmentHealthy {
			t.Fatalf("concurrent environment = %#v, %v", environment, err)
		}
		checkoutRoot := filepath.Dir(filepath.Dir(environment.Sources[0].Path))
		if roots[checkoutRoot] {
			t.Fatalf("environments share checkout %s", checkoutRoot)
		}
		roots[checkoutRoot] = true
		for _, source := range environment.Sources {
			if filepath.Dir(filepath.Dir(source.Path)) != checkoutRoot {
				t.Fatal("sources in one repository were split into separate worktrees")
			}
		}
	}
	if !roots[root] {
		t.Fatal("the first environment unnecessarily abandoned the original checkout")
	}
}

func TestSwitchToLocalPreparesAnIndependentCheckout(t *testing.T) {
	ctx := context.Background()
	app, _, _ := worktreeTestService(t, "checkout")
	local := startWorktreeEnvironment(t, app, "local")
	if _, err := app.CloneEnvironment(ctx, "billing", "local", "qa"); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer remote.Close()
	operation, err := app.ChangeBinding(ctx, "billing", "qa", "checkout", model.ComponentBinding{Provider: model.ProviderRemote, Remote: &model.RemoteTarget{
		URL: remote.URL, Classification: model.RemoteQA, WritePolicy: model.WriteReadOnly,
	}}, "test", "")
	if err != nil || waitForOperation(t, app, operation).State != "succeeded" {
		t.Fatalf("bind remote provider: %v", err)
	}
	startWorktreeEnvironment(t, app, "qa")
	operation, err = app.ChangeBinding(ctx, "billing", "qa", "checkout", model.ComponentBinding{Provider: model.ProviderLocal, Source: "checkout"}, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitForOperation(t, app, operation); completed.State != "succeeded" {
		t.Fatalf("switch to local failed: %#v", completed)
	}
	qa, _ := app.Environment(ctx, "billing", "qa")
	if qa.Status != model.EnvironmentHealthy || qa.Sources[0].Path == local.Sources[0].Path {
		t.Fatalf("local provider was not isolated: %#v", qa)
	}
}

func worktreeTestService(t *testing.T, names ...string) (*Service, *database.Store, string) {
	t.Helper()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sources []model.SourceBinding
	var projectSources []model.ProjectSource
	var bindings []model.ComponentBinding
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: names[0]}
	for _, name := range names {
		path := filepath.Join(root, "apps", name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "source.txt"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		service := model.ServiceDefinition{
			Name: name, Kind: model.ServiceProcess, Required: true,
			Command:          []string{os.Args[0], "-test.run=TestApplicationProcessHelper", "--"},
			Environment:      map[string]string{"PORTLESS_APPLICATION_TEST_HELPER": "1"},
			WorkingDirectory: path, ServiceDirectory: path, PortEnvironment: "PORT",
			Health: model.HealthCheck{Kind: "tcp", Timeout: 3 * time.Second, Interval: 20 * time.Millisecond},
		}
		definition.Services = append(definition.Services, service)
		sources = append(sources, model.SourceBinding{Name: name, Path: path, Status: "ready", Definition: model.ProjectModel{Services: []model.ServiceDefinition{service}}})
		projectSources = append(projectSources, model.ProjectSource{Name: name, Services: []string{name}})
		bindings = append(bindings, model.ComponentBinding{Service: name, Provider: model.ProviderLocal, Source: name})
	}
	for _, arguments := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Portless Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		command := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-C", root}, arguments...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare Git fixture: %v: %s", err, output)
		}
	}
	data := t.TempDir()
	store, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(ctx, "billing", definition, projectSources); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEnvironment(ctx, "billing", "local", definition, sources, bindings); err != nil {
		t.Fatal(err)
	}
	app := New(store, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test"})
	t.Cleanup(func() {
		for _, environment := range []string{"local", "qa", "preview"} {
			for _, name := range names {
				_ = app.processes.Stop(ctx, "billing/"+environment, name, time.Second)
			}
		}
		app.Close(ctx)
	})
	return app, store, root
}

func startWorktreeEnvironment(t *testing.T, app *Service, name string) model.Environment {
	t.Helper()
	ctx := context.Background()
	operation, err := app.Up(ctx, "billing", name, "test", "", UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitForOperation(t, app, operation); completed.State != "succeeded" {
		t.Fatalf("start %s: %#v", name, completed)
	}
	environment, err := app.Environment(ctx, "billing", name)
	if err != nil {
		t.Fatal(err)
	}
	return environment
}
