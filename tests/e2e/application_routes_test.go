//go:build e2e

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestCLIApplicationHostPaths(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "application-routes", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("portless up failed: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	const host = "checkout.local.application-routes.localhost"
	for _, test := range []struct {
		method, path, query, body string
	}{
		{http.MethodGet, "/api/orders", "tag=one&tag=two&search=coffee%20mug", ""},
		{http.MethodPost, "/auth/login", "", `{"fixture":"login request"}`},
	} {
		t.Run(test.path, func(t *testing.T) {
			target := test.path
			if test.query != "" {
				target += "?" + test.query
			}
			response := applicationRequestWithMethod(t, home, host, test.method, target, test.body, map[string]string{
				"Content-Type": "application/json", "X-E2E-Application": "preserved",
			})
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || response.Header.Get("X-E2E-Service") != "checkout" {
				t.Fatalf("application response = %s\n%s", response.Status, body)
			}
			for _, name := range []string{"Content-Security-Policy", "X-Frame-Options", "Referrer-Policy", "X-Content-Type-Options"} {
				if values := response.Header.Values(name); len(values) != 0 {
					t.Errorf("application response acquired a control header: %s = %q", name, values)
				}
			}
			var received struct {
				Service, Method, Path, Query, Body, Header string
			}
			if err := json.Unmarshal(body, &received); err != nil {
				t.Fatal(err)
			}
			if received.Service != "checkout" || received.Method != test.method || received.Path != test.path ||
				received.Query != test.query || received.Body != test.body || received.Header != "preserved" {
				t.Fatalf("application received a changed request: %#v", received)
			}
		})
	}

	response := applicationRequest(t, home, host, "/api/v1/projects", nil)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNotFound || string(body) != "404 page not found\n" {
		t.Fatalf("control-shaped path did not return the application's 404: %s\n%s", response.Status, body)
	}

	output, err := runCLIAt(binary, home, checkout, "--json", "traffic", "list", "--edge", "external:checkout", "--limit", "20")
	if err != nil {
		t.Fatalf("list application traffic: %v\n%s", err, output)
	}
	var traffic struct {
		Project, Environment string
		Exchanges            []model.TrafficExchange
	}
	if err := json.Unmarshal([]byte(output), &traffic); err != nil {
		t.Fatalf("decode application traffic: %v\n%s", err, output)
	}
	if traffic.Project != "application-routes" || traffic.Environment != "local" || len(traffic.Exchanges) != 3 {
		t.Fatalf("unexpected application traffic: %#v", traffic)
	}
	statuses := make(map[string]int)
	for _, exchange := range traffic.Exchanges {
		if exchange.Source != "external" || exchange.Target != "checkout" {
			t.Fatalf("application traffic lost caller identity: %#v", exchange)
		}
		statuses[exchange.Path] = exchange.Status
	}
	if statuses["/api/orders"] != http.StatusOK || statuses["/auth/login"] != http.StatusOK || statuses["/api/v1/projects"] != http.StatusNotFound {
		t.Fatalf("application traffic lost original paths or responses: %#v", traffic.Exchanges)
	}
}
