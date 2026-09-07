package server

import (
	"encoding/json"
	"fmt"
	"github.com/runportless/portless/portless-daemon/api/contract"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMockMetadataAndPayloadContinuation(t *testing.T) {
	server, auth := newMockPreviewServer(t)
	base := "/api/v1/environments/billing/local/mocks/checkout-empty"
	body := strings.Repeat("☕", 200000)
	route := contract.MockRoute{Name: "health", Service: "checkout", Method: "GET", Path: "/health", Status: 200, Enabled: true, Body: body, Headers: map[string]string{"X-Secret": "header-secret"}, Query: map[string]contract.MockQueryMatcher{"token": {Match: "equals", Value: "query-secret"}}}
	encoded, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "http://localhost:7331"+base+"/routes/health", strings.NewReader(string(encoded)))
	req.Header.Set("Authorization", "Bearer "+auth.Token())
	req.Header.Set(contract.MockMetadataHeader, "1")
	mutation := httptest.NewRecorder()
	server.ServeHTTP(mutation, req)
	if mutation.Code != 200 || !strings.Contains(mutation.Body.String(), `"routeCount":1`) || strings.Contains(mutation.Body.String(), "header-secret") || mutation.Body.Len() > 2048 {
		t.Fatalf("metadata receipt = %d %s", mutation.Code, mutation.Body.String())
	}
	for _, path := range []string{base + "?view=metadata&limit=1", "/api/v1/environments/billing/local/mocks?view=metadata&limit=1", base + "/routes/health"} {
		response := request(server, auth, http.MethodGet, path, "", true)
		if response.Code != 200 {
			t.Fatalf("metadata %s = %d %s", path, response.Code, response.Body.String())
		}
		for _, secret := range []string{"header-secret", "query-secret", "☕"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("metadata leaked %q", secret)
			}
		}
	}
	var assembled strings.Builder
	offset := 0
	var modified time.Time
	for {
		path := fmt.Sprintf("%s/routes/health?includePayloads=true&offset=%d&limit=32768", base, offset)
		if offset > 0 {
			path += "&expectedModifiedAt=" + url.QueryEscape(modified.Format(time.RFC3339Nano))
		}
		response := request(server, auth, http.MethodGet, path, "", true)
		if response.Code != 200 {
			t.Fatalf("payload page = %d %s", response.Code, response.Body.String())
		}
		var page contract.MockRouteDetail
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.Payload == nil || !utf8.ValidString(page.Payload.Content) || page.Payload.Offset != offset {
			t.Fatalf("invalid range %#v", page.Payload)
		}
		modified = page.Scenario.ModifiedAt
		assembled.WriteString(page.Payload.Content)
		if page.Payload.NextOffset == 0 {
			break
		}
		offset = page.Payload.NextOffset
	}
	var payload struct {
		Body    string
		Headers map[string]string
		Query   map[string]contract.MockQueryMatcher
	}
	if err := json.Unmarshal([]byte(assembled.String()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Body != body || payload.Headers["X-Secret"] != "header-secret" || payload.Query["token"].Value != "query-secret" {
		t.Fatal("payload continuation lost saved data")
	}
	changed := request(server, auth, http.MethodPut, base+"/routes/new", `{"name":"new","service":"checkout","method":"GET","path":"/other","status":200,"enabled":true}`, true)
	if changed.Code != 200 {
		t.Fatal(changed.Body.String())
	}
	stale := request(server, auth, http.MethodGet, base+"/routes/health?includePayloads=true&offset=1&expectedModifiedAt="+url.QueryEscape(modified.Format(time.RFC3339Nano)), "", true)
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "RESOURCE_CHANGED") {
		t.Fatalf("stale = %d %s", stale.Code, stale.Body.String())
	}
}
