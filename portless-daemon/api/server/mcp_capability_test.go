package server

import (
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryReplayRouteRequiresMCPDeclaration(t *testing.T) {
	server, auth := newMockPreviewServer(t)
	base := "/api/v1/environments/billing/local/traffic/replays"
	for _, route := range []struct{ method, suffix string }{{"POST", ""}, {"GET", "/1"}, {"DELETE", "/1"}, {"GET", "/1/status"}, {"POST", "/1/activity"}, {"PUT", "/1/draft"}, {"POST", "/1/runs"}} {
		for _, capability := range []string{"", "wrong", "1"} {
			req := httptest.NewRequest(route.method, "http://localhost:7331"+base+route.suffix, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+auth.Token())
			req.Header.Set(contract.ClientKindHeader, "mcp")
			req.Header.Set(contract.MCPReplayCapabilityHeader, capability)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if (response.Code == http.StatusForbidden) != (capability != "1") {
				t.Fatalf("%s %s declaration=%q status=%d %s", route.method, route.suffix, capability, response.Code, response.Body.String())
			}
		}
	}
}
func TestMCPSourceIntroductionRequiresConfinedRoot(t *testing.T) {
	server, auth := newMockPreviewServer(t)
	response := requestClientKind(server, auth, http.MethodPost, "/api/v1/projects/discover", `{"path":"/tmp","name":"unconfined"}`, "mcp")
	if response.Code != 400 || !strings.Contains(response.Body.String(), "SOURCE_ROOT_REQUIRED") {
		t.Fatalf("unconfined discovery=%d %s", response.Code, response.Body.String())
	}
}
