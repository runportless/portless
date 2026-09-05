package server

import (
	"io"
	"net/http"
	"slices"
	"testing"
)

func TestApplicationHostsPreserveBrowserSecurityHeaders(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		headers http.Header
	}{
		{name: "no application policy", status: http.StatusOK},
		{name: "application policy", status: http.StatusOK, headers: http.Header{
			"Content-Security-Policy": {"default-src 'none'; script-src 'unsafe-inline'; connect-src https://api.example.test; frame-ancestors 'self'"},
			"X-Frame-Options":         {"SAMEORIGIN"},
			"Referrer-Policy":         {"origin"},
			"X-Content-Type-Options":  {"nosniff"},
		}},
		{name: "multiple policies on an application error", status: http.StatusNotFound, headers: http.Header{
			"Content-Security-Policy":             {"default-src 'self'", "frame-ancestors https://parent.example.test"},
			"Content-Security-Policy-Report-Only": {"default-src 'none'; report-uri /reports"},
			"Referrer-Policy":                     {"strict-origin-when-cross-origin"},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, _ := newApplicationHostServerWithUpstream(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/health" {
					writer.WriteHeader(http.StatusOK)
					return
				}
				for name, values := range test.headers {
					writer.Header()[name] = slices.Clone(values)
				}
				writer.Header().Set("Content-Type", "text/html; charset=utf-8")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, "<html>application response</html>")
			}))
			for _, path := range []string{"/", "/api/v1/health", "/auth/login"} {
				response := requestHost(server, server.auth, http.MethodGet, path, "", false, "checkout.local.billing.localhost")
				if response.Code != test.status || response.Body.String() != "<html>application response</html>" {
					t.Fatalf("application response for %s = %d %s", path, response.Code, response.Body.String())
				}
				for _, name := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only", "X-Frame-Options", "Referrer-Policy", "X-Content-Type-Options"} {
					if got := response.Header().Values(name); !slices.Equal(got, test.headers.Values(name)) {
						t.Errorf("%s: %s = %q, want application values %q", path, name, got, test.headers.Values(name))
					}
				}
			}
		})
	}
}

func TestControlHostsRetainBrowserSecurityHeaders(t *testing.T) {
	server, _ := newApplicationHostServer(t)
	for _, host := range []string{"portless.localhost", "LOCALHOST:7331", "127.0.0.1:7331", "[::1]:7331"} {
		t.Run(host, func(t *testing.T) {
			claim, _, err := server.auth.IssueClaim("/projects")
			if err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				method, path  string
				authenticated bool
				status        int
			}{
				{http.MethodGet, "/", false, http.StatusOK},
				{http.MethodGet, "/api/v1/health", false, http.StatusOK},
				{http.MethodPost, "/api/v1/health", false, http.StatusMethodNotAllowed},
				{http.MethodGet, "/api/v1/projects", false, http.StatusUnauthorized},
				{http.MethodGet, "/api/v1/projects", true, http.StatusOK},
				{http.MethodGet, "/auth/claim/" + claim, false, http.StatusSeeOther},
				{http.MethodGet, "/auth/claim/invalid", false, http.StatusUnauthorized},
			} {
				response := requestHost(server, server.auth, test.method, test.path, "", test.authenticated, host)
				if response.Code != test.status {
					t.Fatalf("%s %s = %d, want %d", test.method, test.path, response.Code, test.status)
				}
				for name, expected := range map[string]string{
					"Content-Security-Policy": "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'",
					"X-Frame-Options":         "DENY",
					"Referrer-Policy":         "no-referrer",
					"X-Content-Type-Options":  "nosniff",
				} {
					if got := response.Header().Values(name); !slices.Equal(got, []string{expected}) {
						t.Errorf("%s %s: %s = %q, want %q", test.method, test.path, name, got, expected)
					}
				}
			}
		})
	}
}
