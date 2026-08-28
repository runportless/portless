package relay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type commandResponse struct {
	output string
	err    error
}

type scriptedCommandRunner struct {
	mutex     sync.Mutex
	responses []commandResponse
	calls     []string
}

func (runner *scriptedCommandRunner) combinedOutput(_ context.Context, executable string, arguments ...string) ([]byte, error) {
	runner.mutex.Lock()
	defer runner.mutex.Unlock()
	runner.calls = append(runner.calls, strings.Join(append([]string{executable}, arguments...), " "))
	if len(runner.responses) == 0 {
		return nil, nil
	}
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	return []byte(response.output), response.err
}

type testCommandExitError int

func (err testCommandExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", err)
}
func (err testCommandExitError) ExitCode() int { return int(err) }

type fakeHostPlatform struct {
	details   platformInstallation
	state     relayServiceState
	stateErr  error
	poolReady bool
	poolErr   error
}

func (platform *fakeHostPlatform) installation() platformInstallation { return platform.details }
func (*fakeHostPlatform) install(context.Context, SetupRequest) error { return nil }
func (*fakeHostPlatform) restart(context.Context) error               { return nil }
func (*fakeHostPlatform) uninstall(context.Context, uninstallSpec) error {
	return nil
}
func (*fakeHostPlatform) prepareRuntime(context.Context) error { return nil }
func (platform *fakeHostPlatform) serviceState(context.Context) (relayServiceState, error) {
	return platform.state, platform.stateErr
}
func (platform *fakeHostPlatform) loopbackPoolStatus() (bool, string, error) {
	return platform.poolReady, "fixture pool", platform.poolErr
}

func successfulInspectionProbes() inspectionProbes {
	return inspectionProbes{
		http:     func(context.Context) error { return nil },
		dns:      func(context.Context) error { return nil },
		resolver: func(context.Context) error { return nil },
	}
}

func TestInspectDoesNotInferOwnershipWithoutReceipt(t *testing.T) {
	directory := t.TempDir()
	configuration := filepath.Join(directory, "relay.service")
	if err := os.WriteFile(configuration, []byte("owner=501"), 0o644); err != nil {
		t.Fatal(err)
	}
	platform := &fakeHostPlatform{
		details: platformInstallation{
			Name: "fixture", Service: "relay", ConfigurationPath: configuration,
			ReceiptPath: filepath.Join(directory, "receipt.json"), ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
		},
		state: relayServiceStopped, poolReady: true,
	}
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || status.OwnerUID != 0 || !strings.Contains(status.Problem, "receipt is missing") {
		t.Fatalf("configuration-only installation was treated as owned: %#v", status)
	}
	if err := ValidateOwnership(status, os.Geteuid()); err == nil {
		t.Fatal("configuration-only installation passed ownership validation")
	}
	lifecycleStatus, err := inspectInstallation(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycleStatus.HTTPHealthy || lifecycleStatus.DNSHealthy || lifecycleStatus.ResolverHealthy {
		t.Fatalf("lifecycle inspection unexpectedly ran network health probes: %#v", lifecycleStatus)
	}
	if err := validateInstallOwnership(context.Background(), platform, os.Geteuid()); err == nil || !strings.Contains(err.Error(), "refuse to replace") {
		t.Fatalf("installation replaced an unowned partial relay: %v", err)
	}
}

func TestInspectFailsClosedWhenServiceStateIsUnknown(t *testing.T) {
	probeError := errors.New("service manager unavailable")
	platform := &fakeHostPlatform{state: relayServiceUnknown, stateErr: probeError, poolReady: true}
	if _, err := inspect(context.Background(), platform, successfulInspectionProbes()); !errors.Is(err, probeError) {
		t.Fatalf("inspect error = %v, want %v", err, probeError)
	}
}

func TestInspectDoesNotReportStoppedOrIncompleteInstallationHealthy(t *testing.T) {
	directory := t.TempDir()
	configuration := filepath.Join(directory, "relay.service")
	resolver := filepath.Join(directory, "resolver.conf")
	for _, path := range []string{configuration, resolver} {
		if err := os.WriteFile(path, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	platform := &fakeHostPlatform{
		details: platformInstallation{
			Name: "fixture", Service: "relay", ConfigurationPath: configuration,
			ReceiptPath: filepath.Join(directory, "receipt.json"), ResolverPath: resolver,
			ArtifactUID: os.Geteuid(), ArtifactGID: os.Getegid(),
		},
		state: relayServiceStopped, poolReady: true,
	}
	status, err := inspect(context.Background(), platform, successfulInspectionProbes())
	if err != nil {
		t.Fatal(err)
	}
	if status.Healthy || !status.HTTPHealthy || !status.DNSHealthy || !status.ResolverHealthy {
		t.Fatalf("stopped partial installation health = %#v", status)
	}
}

func TestArtifactTransactionRestoresExistingAndRemovesNewFiles(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "existing")
	created := filepath.Join(directory, "created")
	if err := os.WriteFile(existing, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(existing, created)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(directory, "replacement")
	if err := os.WriteFile(replacement, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, existing); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(created, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(existing)
	if err != nil || string(content) != "before" {
		t.Fatalf("existing artifact was not restored: content=%q err=%v", content, err)
	}
	if _, err := os.Lstat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new artifact remains after rollback: %v", err)
	}
}

func TestArtifactTransactionRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	link := filepath.Join(directory, "link")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := beginArtifactTransaction(link); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink transaction error = %v", err)
	}
}

func TestArtifactTransactionPreservesBackupWhenRestoreCannotReplaceTarget(t *testing.T) {
	directory := t.TempDir()
	artifact := filepath.Join(directory, "artifact")
	if err := os.WriteFile(artifact, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(artifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err == nil || !strings.Contains(err.Error(), "preserved backup") {
		t.Fatalf("restore obstruction error = %v", err)
	}
	backupPath := transaction.backups[0].backupPath
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("recovery backup was not preserved at %s: %v", backupPath, err)
	}
	if err := os.Remove(artifact); err != nil {
		t.Fatal(err)
	}
	if err := transaction.rollback(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(artifact)
	if err != nil || string(content) != "before" {
		t.Fatalf("retry did not restore preserved backup: content=%q err=%v", content, err)
	}
}

func TestArtifactTransactionCommitDiscardsBackupWithoutChangingArtifact(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(artifact, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := beginArtifactTransaction(artifact)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := transaction.backups[0].backupPath
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(backupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed transaction retained backup: %v", err)
	}
	content, err := os.ReadFile(artifact)
	if err != nil || string(content) != "current" {
		t.Fatalf("commit changed artifact: content=%q err=%v", content, err)
	}
}
