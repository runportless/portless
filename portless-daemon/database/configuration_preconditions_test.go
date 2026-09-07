package database

import (
	"errors"
	"github.com/runportless/portless/portless-daemon/model"
	"path/filepath"
	"testing"
)

func TestConfigurationPreviewGuardsAllAffectedState(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := t.Context()
	definition := model.ProjectModel{SuggestedName: "shop"}
	if _, err := db.CreateProject(ctx, "shop", definition, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateEnvironment(ctx, "shop", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	preview, err := db.PreviewConfiguration(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateMockScenario(ctx, "shop", "local", model.MockScenario{Name: "reviewed"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ForgetProject(ctx, "shop", &preview.Expected); !errors.Is(err, ErrConflict) {
		t.Fatalf("new artifact lost: %v", err)
	}
	preview, err = db.PreviewConfiguration(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Environments) != 1 || len(preview.Environments[0].MockScenarios) != 1 || preview.Environments[0].MockScenarios[0] != "reviewed" {
		t.Fatalf("incomplete preview: %#v", preview)
	}
	if _, err := db.CreateEnvironment(ctx, "shop", "qa", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.ForgetProject(ctx, "shop", &preview.Expected); !errors.Is(err, ErrConflict) {
		t.Fatalf("new environment lost: %v", err)
	}
	envPreview, err := db.PreviewConfiguration(ctx, "shop", "qa")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetEnvironmentStatus(ctx, "shop", "qa", model.EnvironmentHealthy, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ForgetEnvironment(ctx, "shop", "qa", &envPreview.Expected); !errors.Is(err, ErrConflict) {
		t.Fatalf("active environment lost: %v", err)
	}
	if err := db.SetEnvironmentStatus(ctx, "shop", "qa", model.EnvironmentStopped, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.ForgetEnvironment(ctx, "shop", "qa", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateEnvironment(ctx, "shop", "qa", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.ForgetEnvironment(ctx, "shop", "qa", &envPreview.Expected); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused environment name lost: %v", err)
	}
	current, err := db.PreviewConfiguration(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ForgetProject(ctx, "shop", &current.Expected); err != nil {
		t.Fatal(err)
	}
}
