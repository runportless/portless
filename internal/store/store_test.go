package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portless-run/portless/internal/model"
)

func TestNamedResourcesAndPrivateProjectKey(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	definition := model.ProjectModel{SuggestedName: "billing", PrimaryService: "gateway", Services: []model.ServiceDefinition{{Name: "gateway", Kind: model.ServiceProcess, Required: true, Command: []string{"node", "server.js"}}}}
	project, err := controlStore.CreateProject(ctx, "billing", "/tmp/billing-fixture", definition)
	if err != nil {
		t.Fatal(err)
	}
	if project.Status != model.ProjectStopped || project.Reason != "" {
		t.Fatalf("new project should be immediately startable, got status=%s reason=%q", project.Status, project.Reason)
	}
	privateKey, err := controlStore.PrivateProjectKey(ctx, "billing")
	if err != nil || privateKey == "" {
		t.Fatalf("private key = %q, err = %v", privateKey, err)
	}
	encoded, _ := json.Marshal(project)
	if strings.Contains(string(encoded), privateKey) || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), `"trusted"`) {
		t.Fatalf("private key leaked through project JSON: %s", encoded)
	}
	operation, err := controlStore.CreateOperation(ctx, "billing", "up", "CLI", "same-request")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := controlStore.CreateOperation(ctx, "billing", "up", "CLI", "same-request")
	if err != nil || repeated.Number != operation.Number {
		t.Fatalf("idempotent operation = %#v, err = %v", repeated, err)
	}
	if _, err := controlStore.AddTimelineEvent(ctx, model.TimelineEvent{Project: "billing", Actor: "CLI", Type: "test", Severity: "info", Summary: "named project event"}); err != nil {
		t.Fatal(err)
	}
	timeline, err := controlStore.Timeline(ctx, "billing", 10)
	if err != nil || len(timeline) != 1 || timeline[0].Project != "billing" {
		t.Fatalf("timeline = %#v, err = %v", timeline, err)
	}
}

func TestLegacyReviewStateMigratesToStopped(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	controlStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateProject(ctx, "billing", "/tmp/legacy-review-fixture", model.ProjectModel{SuggestedName: "billing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.DB().ExecContext(ctx, `UPDATE projects SET trusted = 0, status = 'review_required', reason = 'project model must be reviewed' WHERE name = 'billing'`); err != nil {
		t.Fatal(err)
	}
	if err := controlStore.Close(); err != nil {
		t.Fatal(err)
	}

	controlStore, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	project, err := controlStore.Project(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if project.Status != model.ProjectStopped || project.Reason != "" {
		t.Fatalf("legacy project was not made immediately startable: status=%s reason=%q", project.Status, project.Reason)
	}
	var legacyTrusted int
	if err := controlStore.DB().QueryRowContext(ctx, `SELECT trusted FROM projects WHERE name = 'billing'`).Scan(&legacyTrusted); err != nil {
		t.Fatal(err)
	}
	if legacyTrusted != 1 {
		t.Fatalf("legacy compatibility column = %d, want 1", legacyTrusted)
	}
}

func TestOnlyOneRecordingIsActivePerProject(t *testing.T) {
	ctx := context.Background()
	controlStore, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer controlStore.Close()
	_, err = controlStore.CreateProject(ctx, "billing", "/tmp/recording-fixture", model.ProjectModel{SuggestedName: "billing", PrimaryService: "gateway", Services: []model.ServiceDefinition{{Name: "gateway"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Name: "first"}); err != nil {
		t.Fatal(err)
	}
	_, err = controlStore.CreateRecording(ctx, model.Recording{Project: "billing", Name: "second"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}
