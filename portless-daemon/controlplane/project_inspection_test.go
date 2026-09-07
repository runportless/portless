package controlplane

import (
	"encoding/json"
	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/events"
	"github.com/runportless/portless/portless-daemon/model"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMetadataAndDeclarationExcludeDiscoveredSecrets(t *testing.T) {
	data := t.TempDir()
	db, err := database.Open(filepath.Join(data, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	definition := model.ProjectModel{SuggestedName: "shop", Services: []model.ServiceDefinition{{Name: "web", Kind: model.ServiceProcess, Command: []string{"run", "--token=command-secret"}, Environment: map[string]string{"NOT_OBVIOUSLY_A_SECRET": "environment-secret"}, WorkingDirectory: "/private/checkout", Evidence: []model.Evidence{{Explanation: "evidence-secret"}}}}, References: []model.ConnectionReference{{TargetHint: "reference-secret"}}}
	if _, err := db.CreateProject(t.Context(), "shop", definition, []model.ProjectSource{{Name: "web", Services: []string{"web"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateEnvironment(t.Context(), "shop", "local", definition, nil, nil); err != nil {
		t.Fatal(err)
	}
	app := New(db, events.NewBroker(), Config{DataDirectory: data})
	defer app.Close(t.Context())
	metadata, err := app.ProjectMetadata(t.Context(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ServiceCount != 1 || metadata.SourceCount != 1 || len(metadata.Environments) != 1 {
		t.Fatalf("metadata=%#v", metadata)
	}
	list, total, err := app.ProjectMetadataList(t.Context(), 0, 1)
	if err != nil || total != 1 || len(list) != 1 || len(list[0].Services) != 0 {
		t.Fatalf("list=%#v total=%d err=%v", list, total, err)
	}
	content, err := app.ExportProject(t.Context(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"command-secret", "environment-secret", "evidence-secret", "reference-secret", "/private/checkout"} {
		if strings.Contains(string(content), secret) || strings.Contains(string(encoded), secret) {
			t.Fatalf("safe project inspection leaked %q", secret)
		}
	}
	var declaration model.ProjectDeclaration
	if err := json.Unmarshal(content, &declaration); err != nil {
		t.Fatal(err)
	}
	if declaration.SchemaVersion != 2 || declaration.Revision != 1 || declaration.CreatedAt.IsZero() || len(declaration.Redactions) < 4 {
		t.Fatalf("declaration=%#v", declaration)
	}
	stored, err := db.ProjectModel(t.Context(), "shop")
	if err != nil || stored.Services[0].Environment["NOT_OBVIOUSLY_A_SECRET"] != "environment-secret" {
		t.Fatal("export modified stored configuration")
	}
}
