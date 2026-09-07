package server

import (
	"encoding/base64"
	"encoding/json"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConditionalMockDeletionRejectsChangedAndReusedNames(t *testing.T) {
	server, auth := newMockPreviewServer(t)
	base := "/api/v1/environments/billing/local/mocks/checkout-empty"
	preview := func() contract.MockDeletionPreview {
		t.Helper()
		response := request(server, auth, http.MethodDelete, base+"?mode=preview", "", true)
		if response.Code != 200 {
			t.Fatalf("preview=%d %s", response.Code, response.Body.String())
		}
		var result contract.MockDeletionPreview
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	apply := func(version contract.ResourceVersion) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(version)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodDelete, "http://localhost:7331"+base, nil)
		req.Header.Set("Authorization", "Bearer "+auth.Token())
		req.Header.Set(contract.ClientKindHeader, "mcp")
		req.Header.Set("If-Match", `"`+base64.RawURLEncoding.EncodeToString(encoded)+`"`)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		return response
	}
	before := preview()
	if len(before.Routes) != 1 || before.Routes[0] != "health" || before.Expected.ParentCreatedAt.IsZero() || len(before.Blocked) != 0 {
		t.Fatalf("preview=%#v", before)
	}
	missing := requestClientKind(server, auth, http.MethodDelete, base, "", "mcp")
	if missing.Code != 428 {
		t.Fatalf("missing precondition=%d %s", missing.Code, missing.Body.String())
	}
	updated := request(server, auth, http.MethodPut, base+"/routes/health", `{"name":"health","service":"checkout","method":"GET","path":"/health","status":201,"enabled":true}`, true)
	if updated.Code != 200 {
		t.Fatal(updated.Body.String())
	}
	if response := apply(before.Expected); response.Code != 409 {
		t.Fatalf("stale apply=%d %s", response.Code, response.Body.String())
	}
	current := preview()
	if response := apply(current.Expected); response.Code != 204 {
		t.Fatalf("apply=%d %s", response.Code, response.Body.String())
	}
	recreated := request(server, auth, http.MethodPost, "/api/v1/environments/billing/local/mocks", `{"name":"checkout-empty"}`, true)
	if recreated.Code != 201 {
		t.Fatal(recreated.Body.String())
	}
	if response := apply(current.Expected); response.Code != 409 {
		t.Fatalf("reused name apply=%d %s", response.Code, response.Body.String())
	}
	if response := request(server, auth, http.MethodGet, base+"?view=metadata", "", true); response.Code != 200 || !strings.Contains(response.Body.String(), `"routeCount":0`) {
		t.Fatal("stale delete removed the replacement scenario")
	}
}
