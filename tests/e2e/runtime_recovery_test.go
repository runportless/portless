//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/runportless/portless/portless-daemon/database"
	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/runtime/supervisor"
)

func TestCLIRebootRecoveryRestartsProvablyGoneRuntimes(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "reboot-recovery-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start reboot recovery environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	before := environmentStatus(t, binary, home, checkout)
	beforeDaemon := daemonStatus(t, binary, home, checkout)
	runtimes := persistedProcessRuntimes(t, home, "reboot-recovery-e2e/local")
	strandReadyProcessRuntimes(t, beforeDaemon.PID, runtimes)

	output, err := runCLIAt(binary, home, checkout, "up", "--no-open", "--timeout", "2m")
	if err != nil {
		t.Fatalf("up did not recover provably gone runtimes: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	if strings.Contains(output, "runtime recovery is incomplete") {
		t.Fatalf("up retained the stale recovery failure:\n%s", output)
	}
	after := environmentStatus(t, binary, home, checkout)
	if after.Status != model.EnvironmentHealthy {
		t.Fatalf("environment after reboot recovery = %s: %#v", after.Status, after)
	}
	for _, previous := range before.Services {
		current := requireService(t, after, previous.Name)
		if current.Status != model.ServiceReady || current.PID == 0 || current.PID == previous.PID || current.Generation != previous.Generation+1 {
			t.Fatalf("service %s was not restarted at the next generation: before=%#v after=%#v", previous.Name, previous, current)
		}
	}
	afterDaemon := daemonStatus(t, binary, home, checkout)
	if afterDaemon.PID == beforeDaemon.PID || afterDaemon.RuntimeState != "ready" || !afterDaemon.HandoffReady || len(afterDaemon.RecoveryProblems) != 0 {
		t.Fatalf("daemon remained unhealthy after reboot recovery: before=%#v after=%#v", beforeDaemon, afterDaemon)
	}
	response := applicationRequest(t, home, "checkout.local.reboot-recovery-e2e.localhost", "/checkout?sku=coffee-mug&quantity=1", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("application route failed after reboot recovery: %s", response.Status)
	}
}

func TestCLIForcedResetAcceptsProvablyGoneReadyRuntimes(t *testing.T) {
	binary := e2eBinary(t)
	home, checkout := isolatedFixture(t, "store-lite")
	defer cleanupInstallation(t, binary, home, checkout)

	if output, err := runCLIAt(binary, home, checkout, "up", "--name", "gone-reset-e2e", "--no-open", "--timeout", "2m"); err != nil {
		t.Fatalf("start forced reset environment: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	daemon := daemonStatus(t, binary, home, checkout)
	runtimes := persistedProcessRuntimes(t, home, "gone-reset-e2e/local")
	strandReadyProcessRuntimes(t, daemon.PID, runtimes)

	output, err := runCLIAt(binary, home, checkout, "reset", "--force", "--yes")
	if err != nil {
		t.Fatalf("forced reset rejected provably gone ready runtimes: %v\n%s\ndaemon log:\n%s", err, output, readDaemonLog(home))
	}
	if !strings.Contains(output, "Portless reset complete.") {
		t.Fatalf("forced reset did not report completion:\n%s", output)
	}
	projectsOutput, err := runCLIAt(binary, home, checkout, "--json", "project", "list")
	if err != nil {
		t.Fatalf("list projects after forced reset: %v\n%s", err, projectsOutput)
	}
	var projects struct {
		Projects []json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal([]byte(projectsOutput), &projects); err != nil || len(projects.Projects) != 0 {
		t.Fatalf("forced reset retained project state: err=%v output=%s", err, projectsOutput)
	}
}

func persistedProcessRuntimes(t *testing.T, home, selector string) []database.ServiceRuntimeRecord {
	t.Helper()
	controlStore, err := database.Open(filepath.Join(home, "portless.db"))
	if err != nil {
		t.Fatal(err)
	}
	runtimes, runtimeErr := controlStore.ServiceRuntimes(context.Background(), selector)
	closeErr := controlStore.Close()
	if runtimeErr != nil {
		t.Fatal(runtimeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	var processes []database.ServiceRuntimeRecord
	for _, runtime := range runtimes {
		if runtime.SupervisorPID == 0 && runtime.PID == 0 {
			continue
		}
		if runtime.SupervisorPID <= 0 || runtime.PID <= 0 || runtime.SupervisorSocket == "" || runtime.SupervisorState == "" {
			t.Fatalf("process runtime %s has incomplete recovery identity", runtime.ServiceName)
		}
		processes = append(processes, runtime)
	}
	if len(processes) == 0 {
		t.Fatal("environment has no persisted process runtimes")
	}
	return processes
}

func strandReadyProcessRuntimes(t *testing.T, daemonPID int, runtimes []database.ServiceRuntimeRecord) {
	t.Helper()
	if err := syscall.Kill(daemonPID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill isolated daemon PID %d: %v", daemonPID, err)
	}
	waitForProcessExit(t, daemonPID)
	for _, runtime := range runtimes {
		if err := syscall.Kill(runtime.SupervisorPID, syscall.SIGSTOP); err != nil {
			t.Fatalf("freeze isolated supervisor for %s at PID %d: %v", runtime.ServiceName, runtime.SupervisorPID, err)
		}
	}
	for _, runtime := range runtimes {
		if err := syscall.Kill(-runtime.PID, syscall.SIGKILL); err != nil {
			t.Fatalf("kill isolated process group for %s at %d: %v", runtime.ServiceName, runtime.PID, err)
		}
	}
	for _, runtime := range runtimes {
		if err := syscall.Kill(runtime.SupervisorPID, syscall.SIGKILL); err != nil {
			t.Fatalf("kill isolated supervisor for %s at PID %d: %v", runtime.ServiceName, runtime.SupervisorPID, err)
		}
	}
	var pids []int
	for _, runtime := range runtimes {
		pids = append(pids, runtime.PID, runtime.SupervisorPID)
	}
	waitForProcessExit(t, pids...)
	waitForProcessGroupsExit(t, runtimes)
	for _, runtime := range runtimes {
		if err := os.Remove(runtime.SupervisorSocket); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove stale supervisor socket for %s: %v", runtime.ServiceName, err)
		}
		content, err := os.ReadFile(runtime.SupervisorState)
		if err != nil {
			t.Fatalf("read durable supervisor state for %s: %v", runtime.ServiceName, err)
		}
		var status supervisor.Status
		if err := json.Unmarshal(content, &status); err != nil {
			t.Fatalf("decode durable supervisor state for %s: %v", runtime.ServiceName, err)
		}
		if status.State != "ready" || status.PID != runtime.PID || status.SupervisorPID != runtime.SupervisorPID {
			t.Fatalf("runtime %s did not retain the stale ready evidence: %#v", runtime.ServiceName, status)
		}
	}
}

func waitForProcessGroupsExit(t *testing.T, runtimes []database.ServiceRuntimeRecord) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var alive []string
		for _, runtime := range runtimes {
			err := syscall.Kill(-runtime.PID, syscall.Signal(0))
			if err == nil || errors.Is(err, syscall.EPERM) {
				alive = append(alive, runtime.ServiceName)
			}
		}
		if len(alive) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("isolated process groups survived reboot simulation")
}
