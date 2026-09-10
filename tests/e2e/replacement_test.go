//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

func TestCLIFailedReplacementRollsBack(t *testing.T) {
	original := e2eBinary(t)
	candidate := filepath.Join(t.TempDir(), "portless-candidate")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-tags=e2e", "-trimpath", "-ldflags", "-X github.com/runportless/portless/portless-cli.Version=recovery-e2e -X github.com/runportless/portless/portless-daemon.replacementFailureEnabled=true", "-o", candidate, "./portless-cli/cmd/portless")
	build.Dir = e2eRepositoryPath(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failure candidate: %v\n%s", err, output)
	}
	for _, mode := range []string{"exec", "crash", "deadline", "state"} {
		t.Run(mode, func(t *testing.T) {
			home, checkout := isolatedFixture(t, "store-lite")
			executable := filepath.Join(filepath.Dir(home), "portless-recovery")
			copyExecutableForE2E(t, original, executable)
			defer func() {
				if mode == "exec" {
					repaired := executable + ".repair"
					copyExecutableForE2E(t, original, repaired)
					if err := os.Rename(repaired, executable); err != nil {
						t.Error(err)
					}
				}
				cleanupInstallation(t, executable, home, checkout)
			}()
			if output, err := runCLIAt(executable, home, checkout, "up", "--name", "recovery-e2e", "--no-open", "--timeout", "2m"); err != nil {
				t.Fatalf("start: %v\n%s", err, output)
			}
			before := environmentStatus(t, executable, home, checkout)
			previous := daemonStatus(t, executable, home, checkout)
			fault := filepath.Join(home, ".e2e-replacement-failure")
			if err := os.WriteFile(fault, []byte(mode), 0o600); err != nil {
				t.Fatal(err)
			}
			staged := executable + ".new"
			if mode == "exec" {
				if err := os.WriteFile(staged, []byte("not an executable image\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				copyExecutableForE2E(t, candidate, staged)
			}
			if err := os.Rename(staged, executable); err != nil {
				t.Fatal(err)
			}
			// Read-only status uses the original CLI while the installed image can be
			// invalid; the watcher must recover without a CLI remaining alive.
			var recovered e2eDaemonStatus
			deadline := time.Now().Add(25 * time.Second)
			for time.Now().Before(deadline) {
				output, err := runCLIAt(original, home, checkout, "--json", "daemon", "status")
				if err == nil && json.Unmarshal([]byte(output), &recovered) == nil && recovered.LastRestart != nil && recovered.LastRestart.Outcome == "rolled-back" && recovered.InstanceID != previous.InstanceID {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if recovered.LastRestart == nil || recovered.LastRestart.Outcome != "rolled-back" || recovered.PID != previous.PID || recovered.BuildID != previous.BuildID || recovered.HandoffState != "ready" || len(recovered.RecoveryProblems) != 0 {
				t.Fatalf("failed recovery: before=%#v after=%#v\n%s", previous, recovered, readDaemonLog(home))
			}
			inspector := executable
			if mode == "exec" {
				inspector = original
			}
			after := environmentStatus(t, inspector, home, checkout)
			if after.Status != model.EnvironmentHealthy || !maps.Equal(servicePIDs(before), servicePIDs(after)) {
				t.Fatalf("rollback replaced service processes: before=%#v after=%#v", before, after)
			}
			response := applicationRequest(t, home, "checkout.local.recovery-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("routing did not recover: %s", response.Status)
			}
			time.Sleep(750 * time.Millisecond)
			stable := daemonStatus(t, inspector, home, checkout)
			if stable.InstanceID != recovered.InstanceID {
				t.Fatalf("rejected build entered a replacement loop: %#v -> %#v", recovered, stable)
			}
			if mode == "state" {
				// Normal operations use the retained working executable after rollback.
				if output, err := runCLIAt(executable, home, checkout, "service", "restart", "orders", "--timeout", "2m"); err != nil {
					t.Fatalf("use recovered daemon: %v\n%s", err, output)
				}
				if err := os.Remove(fault); err != nil {
					t.Fatal(err)
				}
				if output, err := runCLIAt(executable, home, checkout, "daemon", "restart"); err != nil {
					t.Fatalf("explicit retry: %v\n%s\n%s", err, output, readDaemonLog(home))
				}
				current := daemonStatus(t, executable, home, checkout)
				if !current.CurrentBuild || current.LastRestart == nil || current.LastRestart.Outcome != "replaced" {
					t.Fatalf("retry did not commit: %#v", current)
				}
				if _, err := os.Stat(filepath.Join(home, "daemon-recovery.json")); !os.IsNotExist(err) {
					t.Fatalf("successful retry retained quarantine: %v", err)
				}
			}
			if strings.Contains(readDaemonLog(home), "replacement process exit could not be confirmed") {
				t.Fatal("trial did not reap its failed child")
			}
		})
	}
}
