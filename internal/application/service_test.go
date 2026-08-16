package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portless-run/portless/internal/events"
	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/project/discovery"
	"github.com/portless-run/portless/internal/runtime/supervisor"
	"github.com/portless-run/portless/internal/store"
)

func TestMain(testingMain *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "__runner" && os.Args[2] == "--manifest" {
		if err := supervisor.Run(context.Background(), os.Args[3]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(testingMain.Run())
}

type fixtureDiscoverer struct {
	result discovery.Result
	path   string
}

func (d *fixtureDiscoverer) FindRoot(context.Context, string) (string, error) {
	return d.result.Root, nil
}

func (d *fixtureDiscoverer) Discover(_ context.Context, path string) (discovery.Result, error) {
	d.path = path
	return d.result, nil
}

func TestApplicationUsesInjectedDiscoveryEngine(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	controlStore, err := store.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	source := t.TempDir()
	discoverer := &fixtureDiscoverer{result: discovery.Result{Root: source, Model: model.ProjectModel{
		SuggestedName: "fixture", PrimaryService: "api",
		Services: []model.ServiceDefinition{{
			Name: "api", Kind: model.ServiceProcess, Framework: "fixture", Command: []string{"serve"}, WorkingDirectory: source,
			PortEnvironment: "PORT", Required: true, Health: model.HealthCheck{Kind: "tcp", Timeout: time.Second, Interval: time.Second},
		}},
	}}}
	app := New(controlStore, events.NewBroker(), Config{DataDirectory: data, InstallationKey: "test", Discoverer: discoverer})
	defer app.Close(ctx)

	_, environment, _, err := app.CreateProject(ctx, "fixture", []SourceInput{{Name: "fixture", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if discoverer.path != source || len(environment.Services) != 1 || environment.Services[0].Framework != "fixture" {
		t.Fatalf("injected discovery result was not used: path=%q environment=%#v", discoverer.path, environment)
	}
}
