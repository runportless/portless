package portlessmcp

import (
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	apiclient "github.com/runportless/portless/portless-daemon/api/client"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEnableFaultRejectsUnboundedAndExpiredRulesBeforeMutation(t *testing.T) {
	for _, kind := range []string{"unbounded", "expired", "wide", "too-long", "stale", "finite"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Now().UTC()
			expires := now.Add(time.Minute)
			fault := contract.FaultRule{Name: "delay", Source: "external", Target: "web", ExpiresAt: &expires, CreatedAt: now, Revision: 2}
			switch kind {
			case "unbounded":
				fault.ExpiresAt = nil
			case "expired":
				expires = now.Add(-time.Minute)
			case "wide":
				fault.Source = ""
			case "too-long":
				expires = now.Add(2 * time.Hour)
			}
			mutations := 0
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if strings.HasSuffix(req.URL.Path, "/enable") {
					mutations++
					if req.Header.Get("If-Match") == "" {
						t.Error("missing revision precondition")
					}
					fault.Enabled = true
					_ = json.NewEncoder(w).Encode(fault)
					return
				}
				if req.Method != "DELETE" || req.URL.Query().Get("mode") != "preview" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				}
				_ = json.NewEncoder(w).Encode(contract.FaultDeletionPreview{Fault: fault, Expected: contract.ResourceVersion{CreatedAt: now, Revision: 2, ParentCreatedAt: now}})
			}))
			defer daemon.Close()
			session, closeSession := connectTestServer(t, Config{Environment: "shop/local", AllowTrafficControl: true}, testConnector{client: apiclient.New(daemon.URL, "test", daemon.Client())})
			defer closeSession()
			revision := 2
			if kind == "stale" {
				revision = 1
			}
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "portless_enable_fault", Arguments: map[string]any{"environment": "shop/local", "fault": "delay", "expectedRevision": revision}})
			if err != nil || result.IsError == (kind == "finite") {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			expected := 0
			if kind == "finite" {
				expected = 1
			}
			if mutations != expected {
				t.Fatalf("mutations=%d", mutations)
			}
		})
	}
}
