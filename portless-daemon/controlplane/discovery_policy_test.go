package controlplane

import (
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/projects/discovery"
	"path/filepath"
	"testing"
)

func TestWorkspaceCreationDenialPrecedesPersistence(t *testing.T) {
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	other := t.TempDir()
	app := New(db, events.NewBroker(), Config{DataDirectory: data, Discoverer: &fixtureDiscoverer{result: discovery.Result{Root: other, Model: model.ProjectModel{SuggestedName: "shop"}}}})
	defer app.Close(t.Context())
	_, _, _, err = app.CreateProject(t.Context(), "shop", []SourceInput{{Name: "web", Path: other}}, root, "MCP")
	if err == nil {
		t.Fatal("created a project outside startup workspace")
	}
	projects, err := db.ListProjects(t.Context())
	if err != nil || len(projects) != 0 {
		t.Fatalf("denial persisted state: %v %v", projects, err)
	}
}

func TestWorkspaceCreationCannotEscapeSavedSelection(t *testing.T) {
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := nestFixture(t, filepath.Join(t.TempDir(), "checkout"))
	app := New(db, events.NewBroker(), Config{DataDirectory: data})
	defer app.Close(t.Context())
	inputs := []SourceInput{{Name: "checkout", Path: root}}
	if _, _, _, err := app.CreateProject(t.Context(), "selected", inputs, "", "CLI"); err != nil {
		t.Fatal(err)
	}
	if err := app.SelectEnvironment(t.Context(), root, "selected", "local"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := app.CreateProject(t.Context(), "hidden", inputs, root, "MCP"); err == nil {
		t.Fatal("created a project hidden by the workspace's saved selection")
	}
	projects, err := db.ListProjects(t.Context())
	if err != nil || len(projects) != 1 || projects[0].Name != "selected" {
		t.Fatalf("denial persisted state: %v %v", projects, err)
	}
}
