//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestCLIAutomaticallyIsolatesSharedGitCheckout(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Portless Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		command := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=" + os.DevNull, "-C", checkout}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("prepare Git fixture: %v: %s", err, output)
		}
	}
	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "worktree-e2e", "--managed", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start original: %v\n%s", err, output)
	}
	original := environmentStatus(t, binary, home, checkout)
	orders := filepath.Join(checkout, "apps", "orders", "main.go")
	content, err := os.ReadFile(orders)
	if err != nil {
		t.Fatal(err)
	}
	dirty := strings.ReplaceAll(string(content), `"number": 42`, `"number": 84`)
	if err := os.WriteFile(orders, []byte(dirty), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"env", "clone", "qa"}, {"env", "select", "worktree-e2e/qa"}} {
		if output, err := runCLIAt(binary, home, checkout, args...); err != nil {
			t.Fatalf("select clone: %v\n%s", err, output)
		}
	}
	output, err := runCLIAt(binary, home, checkout, "--json", "up", "--managed", "--no-open", "--timeout", "2m")
	if err != nil {
		t.Fatalf("start clone automatically: %v\n%s", err, output)
	}
	var started struct {
		Environment model.Environment `json:"environment"`
		Operation   model.Operation   `json:"operation"`
	}
	if err := json.Unmarshal([]byte(output), &started); err != nil {
		t.Fatalf("decode JSON startup: %v\n%s", err, output)
	}
	if started.Environment.Status != model.EnvironmentHealthy || len(started.Environment.Sources) != 1 || started.Environment.Sources[0].Path == original.Sources[0].Path {
		t.Fatalf("clone is not independent: %#v", started.Environment)
	}
	prepared := false
	for _, event := range started.Operation.Events {
		prepared = prepared || event.Type == "source.prepared"
	}
	if !prepared {
		t.Fatal("JSON startup did not include checkout preparation progress")
	}
	assertOrder := func(environment, number string) {
		t.Helper()
		response := applicationRequest(t, home, "orders."+environment+".worktree-e2e.localhost", "/orders?sku=coffee&quantity=1", nil)
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"number":`+number) {
			t.Fatalf("%s orders response = %s, %s, %v", environment, response.Status, body, err)
		}
	}
	assertOrder("local", "42")
	assertOrder("qa", "84")
	selected := environmentStatus(t, binary, home, checkout)
	if selected.Name != "qa" {
		t.Fatalf("automatic checkout lost the saved CLI selection: %#v", selected)
	}
	path := started.Environment.Sources[0].Path
	if output, err := runCLIAt(binary, home, checkout, "down"); err != nil {
		t.Fatalf("stop clone: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(path, "apps", "orders", "main.go"), []byte(strings.ReplaceAll(dirty, `"number": 84`, `"number": 99`)), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runCLIAt(binary, home, checkout, "env", "checkout", "set", selected.Sources[0].Name, "--path", path); err != nil {
		t.Fatalf("reapply the selected independent checkout: %v\n%s", err, output)
	}
	if output, err := runCLIAt(binary, home, checkout, "up", "--managed", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("restart saved clone: %v\n%s", err, output)
	}
	assertOrder("qa", "99")
	if environment := environmentStatus(t, binary, home, checkout); environment.Sources[0].Path != path {
		t.Fatal("restart allocated another checkout")
	}
	if output, err := runCLIAt(binary, home, checkout, "daemon", "restart"); err != nil {
		t.Fatalf("restart isolated daemon: %v\n%s", err, output)
	}
	assertOrder("qa", "99")
	assertOrder("local", "42")
	recovered := explicitEnvironmentStatus(t, binary, home, checkout, "worktree-e2e/local")
	for _, service := range original.Services {
		assertSameServiceProcess(t, service, requireService(t, recovered, service.Name))
	}
}
