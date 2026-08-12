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
	request := httptest.NewRequest(http.MethodPost, "http://localhost:7331/api/v1/environments/billing/local/up", nil)
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

func TestControlOriginAllowsCleanPortlessURL(t *testing.T) {
	for _, origin := range []string{"http://portless.localhost", "http://localhost:5173", "http://127.0.0.1:7331", "http://[::1]:7331"} {
		if !isControlOrigin(origin) {
			t.Errorf("expected control origin %q to be accepted", origin)
		}
	}
	for _, origin := range []string{"https://portless.localhost", "http://portless.localhost.evil.example", "http://checkout.billing.localhost", "http://portless.localhost/path"} {
		if isControlOrigin(origin) {
			t.Errorf("expected origin %q to be rejected", origin)
		}
	}
}

func TestBrowserSessionSurvivesManagerRestartAndLogoutRevokesIt(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "install.key")
	manager, err := LoadOrCreate(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := manager.IssueClaim("/projects")
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, _, _, err := manager.ConsumeClaim(code)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://portless.localhost/api/v1/daemon/restart", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	request.Header.Set("Origin", "http://portless.localhost")
	request.Header.Set("X-Portless-CSRF", csrf)

	reloaded, err := LoadOrCreate(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	principal, ok := reloaded.Authenticate(request)
	if !ok || !principal.Session || principal.CSRF != csrf {
		t.Fatalf("reloaded manager rejected persisted browser session: %#v %v", principal, ok)
	}
	if err := reloaded.ValidateMutation(request, principal); err != nil {
		t.Fatalf("reloaded manager rejected persisted CSRF token: %v", err)
	}
	if err := reloaded.Logout(request); err != nil {
		t.Fatal(err)
	}

	afterLogout, err := LoadOrCreate(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := afterLogout.Authenticate(request); ok {
		t.Fatal("logged-out browser session remained valid after another manager restart")
	}
}
