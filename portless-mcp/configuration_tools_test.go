package portlessmcp

import (
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestConfigurationScopeDenialsPrecedeMutation(t *testing.T) {
	var calls atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowConfiguration: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	cases := []struct {
		name string
		args map[string]any
	}{
		{"portless_clone_environment", map[string]any{"environment": "shop/local", "name": "qa"}},
		{"portless_add_project_source", map[string]any{"project": "shop", "environment": "shop/local", "source": "api", "path": t.TempDir()}},
		{"portless_forget_project", map[string]any{"project": "shop"}},
		{"portless_rename_project", map[string]any{"project": "shop", "name": "new", "expectedRevision": 1}},
		{"portless_rescan_environment", map[string]any{"environment": "shop/local", "expectedProjectRevision": 1, "expectedEnvironmentRevision": 1}},
	}
	for _, test := range cases {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: test.name, Arguments: test.args})
		if err != nil || !result.IsError {
			t.Fatalf("scope escape %s=%v %v", test.name, result, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("denial reached daemon: %d", calls.Load())
	}
}
func TestConfigurationDiscoveryPropagatesConfinementAndActor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var discoveries atomic.Int32
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(contract.ClientKindHeader) != string(contract.ClientKindMCP) {
			t.Error("missing MCP actor")
		}
		switch r.URL.Path {
		case "/api/v1/projects/discover":
			var input contract.DiscoverProjectRequest
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Path != root || input.AllowedRoot != root || input.RequiredProject != "shop" {
				t.Errorf("unconfined request=%#v", input)
			}
			discoveries.Add(1)
			_ = json.NewEncoder(w).Encode(contract.ProjectMutation{Project: contract.Project{Name: "shop", Revision: 1}, Environment: contract.Environment{Project: "shop", Name: "local", Revision: 1}})
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer daemon.Close()
	session, closeSession := connectTestServer(t, Config{Project: "shop", AllowConfiguration: true, AllowedSourceRoots: []string{root}}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
	defer closeSession()
	for _, path := range []string{t.TempDir(), root} {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_discover_project", Arguments: map[string]any{"path": path}})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError != (path != root) {
			t.Fatalf("discovery %s=%v", path, result)
		}
	}
	if discoveries.Load() != 1 {
		t.Fatalf("unexpected mutation count %d", discoveries.Load())
	}
}
