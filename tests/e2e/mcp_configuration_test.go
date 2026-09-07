//go:build e2e

package e2e_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/runportless/portless/portless-daemon/api/contract"
)

func TestMCPMultiSourceConfigurationAndGuardedCleanup(t *testing.T) {
	binary := e2eBinary(t)
	home, sources := isolatedMultiSourceFixture(t)
	checkout := sources["checkout"]
	defer cleanupInstallation(t, binary, home, checkout)
	session, _ := connectMCPCommand(t, binary, home, checkout, "mcp", "serve", "--all-environments", "--allow-configuration", "--allow-lifecycle", "--source-root", filepath.Dir(checkout))
	inputSources := []map[string]any{}
	for _, name := range []string{"checkout", "inventory", "orders"} {
		inputSources = append(inputSources, map[string]any{"name": name, "path": sources[name]})
	}
	invokeMCP[map[string]any](t, session, "portless_create_project", map[string]any{"project": "mcp-sources", "sources": inputSources})
	project := invokeMCP[contract.ProjectMetadata](t, session, "portless_get_project", map[string]any{"project": "mcp-sources"})
	if project.SourceCount != 3 || project.ServiceCount != 3 || len(project.Environments) != 1 {
		t.Fatalf("created project=%#v", project)
	}
	invokeMCP[map[string]any](t, session, "portless_clone_environment", map[string]any{"environment": "mcp-sources/local", "name": "qa"})
	added := invokeMCP[struct {
		ConfigurationRequired []string `json:"configurationRequired"`
	}](t, session, "portless_add_project_source", map[string]any{"project": "mcp-sources", "environment": "mcp-sources/local", "source": "catalog", "path": sources["catalog"]})
	if strings.Join(added.ConfigurationRequired, ",") != "mcp-sources/qa" {
		t.Fatalf("missing configuration consequence: %#v", added)
	}
	invokeMCP[map[string]any](t, session, "portless_set_source_checkout", map[string]any{"environment": "mcp-sources/qa", "source": "catalog", "path": sources["catalog"]})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("configuring a stopped environment contacted %s", r.URL)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()
	binding := invokeMCP[struct {
		Operation contract.Operation `json:"operation"`
	}](t, session, "portless_change_service_binding", map[string]any{
		"environment": "mcp-sources/qa", "service": "catalog", "waitSeconds": 120,
		"binding": map[string]any{"provider": "remote", "remote": map[string]any{"url": remote.URL, "classification": "qa", "writePolicy": "read-only"}},
	})
	if binding.Operation.State != "succeeded" || binding.Operation.Actor != "MCP" {
		t.Fatalf("provider operation=%#v", binding)
	}
	cleanup := func(tool string, input map[string]any) contract.ConfigurationPreview {
		t.Helper()
		preview := invokeMCP[struct {
			Preview contract.ConfigurationPreview `json:"preview"`
		}](t, session, tool, input).Preview
		if preview.Expected.StateDigest == "" || len(preview.Blocked) != 0 {
			t.Fatalf("ineligible %s preview=%#v", tool, preview)
		}
		input["mode"], input["confirm"], input["expected"] = "apply", true, preview.Expected
		result := invokeMCP[struct {
			Applied bool `json:"applied"`
		}](t, session, tool, input)
		if !result.Applied {
			t.Fatalf("%s was not applied", tool)
		}
		return preview
	}
	cleanup("portless_remove_source_checkout", map[string]any{"environment": "mcp-sources/qa", "source": "catalog"})
	project = invokeMCP[contract.ProjectMetadata](t, session, "portless_get_project", map[string]any{"project": "mcp-sources"})
	var localRevision int64
	for _, environment := range project.Environments {
		if environment.Name == "local" {
			localRevision = environment.Revision
		}
	}
	invokeMCP[map[string]any](t, session, "portless_rescan_environment", map[string]any{"environment": "mcp-sources/local", "expectedProjectRevision": project.Revision, "expectedEnvironmentRevision": localRevision})
	removed := cleanup("portless_delete_project_source", map[string]any{"project": "mcp-sources", "source": "catalog"})
	if len(removed.Environments) != 2 || len(removed.RemovedServices) != 1 || removed.RemovedServices[0] != "catalog" {
		t.Fatalf("source removal preview=%#v", removed)
	}
	project = invokeMCP[contract.ProjectMetadata](t, session, "portless_get_project", map[string]any{"project": "mcp-sources"})
	pinnedProject, _ := connectMCPCommand(t, binary, home, checkout, "mcp", "serve", "--project", "mcp-sources", "--allow-configuration")
	renamed := invokeMCP[struct {
		Project          string `json:"project"`
		ScopeConsequence string `json:"scopeConsequence"`
	}](t, pinnedProject, "portless_rename_project", map[string]any{"project": "mcp-sources", "name": "mcp-renamed", "expectedRevision": project.Revision})
	if renamed.Project != "mcp-renamed" || !strings.Contains(renamed.ScopeConsequence, "--project mcp-renamed") {
		t.Fatalf("rename=%#v", renamed)
	}
	denied, err := pinnedProject.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_get_project", Arguments: map[string]any{"project": "mcp-renamed"}})
	if err != nil || !denied.IsError {
		t.Fatalf("project scope followed rename: %v %v", denied, err)
	}
	cleanup("portless_forget_environment", map[string]any{"environment": "mcp-renamed/qa"})
	forgotten := cleanup("portless_forget_project", map[string]any{"project": "mcp-renamed"})
	if len(forgotten.Environments) != 1 || forgotten.Environments[0].Name != "local" {
		t.Fatalf("forgot wrong environments: %#v", forgotten)
	}
}
