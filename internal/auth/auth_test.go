package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSingleUseBrowserClaimAndCSRF(t *testing.T) {
	manager, err := LoadOrCreate(filepath.Join(t.TempDir(), "install.key"))
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := manager.IssueClaim("/projects/billing")
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, next, _, err := manager.ConsumeClaim(code)
	if err != nil || token == "" || csrf == "" || next != "/projects/billing" {
		t.Fatalf("claim result token=%q csrf=%q next=%q err=%v", token, csrf, next, err)
	}
	if _, _, _, _, err := manager.ConsumeClaim(code); err == nil {
		t.Fatal("claim was reusable")
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost:7331/api/v1/projects/billing/up", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	request.Header.Set("Origin", "http://localhost:7331")
	principal, ok := manager.Authenticate(request)
	if !ok || !principal.Session {
		t.Fatal("browser session did not authenticate")
	}
	if err := manager.ValidateMutation(request, principal); err == nil {
		t.Fatal("mutation without CSRF was accepted")
	}
	request.Header.Set("X-Portless-CSRF", csrf)
	if err := manager.ValidateMutation(request, principal); err != nil {
		t.Fatalf("valid mutation rejected: %v", err)
	}
}
