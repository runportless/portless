package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/portless-run/portless/internal/api"
	"github.com/portless-run/portless/internal/daemon"
)

type ControlRecord struct {
	PID              int       `json:"pid"`
	Port             int       `json:"port"`
	ProtocolVersion  string    `json:"protocolVersion,omitempty"`
	APIVersion       string    `json:"apiVersion"`
	InstallationID   string    `json:"installationId,omitempty"`
	InstanceID       string    `json:"instanceId,omitempty"`
	BuildID          string    `json:"buildId,omitempty"`
	State            string    `json:"state,omitempty"`
	HandoffReady     bool      `json:"handoffReady,omitempty"`
	RecoveryProblems []string  `json:"recoveryProblems,omitempty"`
	TokenPath        string    `json:"tokenPath"`
	StartedAt        time.Time `json:"startedAt"`
	ProcessHint      string    `json:"processHint"`
}

type DaemonInspection struct {
	Record          ControlRecord
	Identity        daemon.Identity
	Compatible      bool
	CurrentBuild    bool
	ExpectedBuildID string
	Problems        []string
}

var ErrLegacyDaemon = errors.New("daemon predates the authenticated lifecycle protocol")

func EnsureDaemon(ctx context.Context, paths Paths) (ControlRecord, error) {
	if err := ensurePrivateDirectory(paths.Root); err != nil {
		return ControlRecord{}, err
	}
	if inspection, err := InspectDaemon(ctx, paths); err == nil && inspection.Compatible {
		if inspection.CurrentBuild || len(inspection.Identity.ActiveEnvironments) > 0 && !inspection.Identity.HandoffReady {
			return inspection.Record, nil
		}
	}
	lock, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ControlRecord{}, err
	}
	defer lock.Close()
	lockDeadline := time.Now().Add(65 * time.Second)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if inspection, inspectErr := InspectDaemon(ctx, paths); inspectErr == nil && inspection.Compatible {
			if inspection.CurrentBuild || len(inspection.Identity.ActiveEnvironments) > 0 && !inspection.Identity.HandoffReady {
				return inspection.Record, nil
			}
		}
		if time.Now().After(lockDeadline) {
			return ControlRecord{}, errors.New("timed out waiting for another Portless CLI to prepare the daemon")
		}
		select {
		case <-ctx.Done():
			return ControlRecord{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	inspection, inspectErr := InspectDaemon(ctx, paths)
	if inspectErr == nil {
		if inspection.Compatible && inspection.CurrentBuild {
			return inspection.Record, nil
		}
		if inspection.Compatible && len(inspection.Identity.ActiveEnvironments) > 0 && !inspection.Identity.HandoffReady {
			return inspection.Record, nil
		}
		if _, err := stopVerifiedDaemon(ctx, paths, inspection, StopOptions{Timeout: 15 * time.Second, Handoff: true}, false, "replace an outdated daemon"); err != nil {
			return ControlRecord{}, err
		}
	} else {
		record, recordErr := ReadControl(paths)
		switch {
		case recordErr == nil:
			alive, aliveErr := processIsAlive(record.PID)
			if aliveErr != nil {
				return ControlRecord{}, fmt.Errorf("inspect recorded daemon process %d: %w", record.PID, aliveErr)
			}
			if alive {
				return ControlRecord{}, unverifiedDaemonError(record, inspectErr)
			}
			removeMatchingControl(paths, record)
		case errors.Is(recordErr, os.ErrNotExist):
			// There is no daemon to replace.
		default:
			return ControlRecord{}, fmt.Errorf("the daemon control record is invalid; refusing to start a second daemon: %w", recordErr)
		}
	}

	if err := startDaemon(paths); err != nil {
		return ControlRecord{}, err
	}
	// Reconciliation verifies each surviving process/container and restores its
	// dependency listeners before the daemon publishes readiness. A multi-service
	// environment can legitimately take longer than the old process-spawn-only
	// startup budget.
	startupDeadline := time.Now().Add(60 * time.Second)
	var lastError error
	for time.Now().Before(startupDeadline) {
		inspection, err := InspectDaemon(ctx, paths)
		if err == nil && inspection.Compatible && inspection.CurrentBuild {
			return inspection.Record, nil
		}
		if err != nil {
			lastError = err
		} else {
			lastError = incompatibleDaemonError(inspection)
		}
		select {
		case <-ctx.Done():
			return ControlRecord{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	message := "daemon did not become ready; inspect " + paths.DaemonLog
	if lastError != nil {
		message += ": " + lastError.Error()
	} else if tail := readLogTail(paths.DaemonLog, 4096); tail != "" {
		message += ": " + tail
	}
	return ControlRecord{}, errors.New(message)
}

func startDaemon(paths Paths) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.DaemonLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	command := exec.Command(executable, "__daemon", "--data-dir", paths.Root)
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Portless daemon: %w", err)
	}
	return command.Process.Release()
}

func ReadControl(paths Paths) (ControlRecord, error) {
	content, err := readPrivateTextFile(paths.Control)
	if err != nil {
		return ControlRecord{}, err
	}
	var record ControlRecord
	if err := json.Unmarshal([]byte(content), &record); err != nil {
		return ControlRecord{}, err
	}
	if record.Port < 1 || record.Port > 65535 || record.PID <= 0 || record.APIVersion == "" || record.TokenPath == "" {
		return ControlRecord{}, errors.New("invalid daemon discovery record")
	}
	return record, nil
}

// InspectDaemon authenticates the daemon and verifies that its response matches
// the private discovery record and this Portless installation. Compatibility is
// reported separately so a verified older build can be stopped safely.
func InspectDaemon(ctx context.Context, paths Paths) (DaemonInspection, error) {
	record, err := ReadControl(paths)
	if err != nil {
		return DaemonInspection{}, err
	}
	if record.TokenPath != paths.Token {
		return DaemonInspection{}, fmt.Errorf("daemon token path %s does not match this installation", record.TokenPath)
	}
	if record.ProtocolVersion == "" || record.InstallationID == "" || record.InstanceID == "" || record.BuildID == "" || record.StartedAt.IsZero() {
		return DaemonInspection{}, fmt.Errorf("%w: identity metadata is missing", ErrLegacyDaemon)
	}
	token, err := readPrivateTextFile(paths.Token)
	if err != nil {
		return DaemonInspection{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	expectedInstallationID, err := InstallationID(paths)
	if err != nil {
		return DaemonInspection{}, err
	}
	identity, err := fetchDaemonIdentity(ctx, record.Port, token)
	if err != nil {
		return DaemonInspection{}, err
	}
	if identity.Product != daemon.Product {
		return DaemonInspection{}, fmt.Errorf("unexpected daemon product %q", identity.Product)
	}
	if identity.PID != record.PID || identity.ProtocolVersion != record.ProtocolVersion || identity.APIVersion != record.APIVersion ||
		identity.InstallationID != record.InstallationID || identity.InstanceID != record.InstanceID || identity.BuildID != record.BuildID ||
		!identity.StartedAt.Equal(record.StartedAt) {
		return DaemonInspection{}, errors.New("authenticated daemon identity does not match the discovery record")
	}
	if identity.InstallationID != expectedInstallationID {
		return DaemonInspection{}, errors.New("authenticated daemon belongs to a different Portless installation")
	}
	expectedBuildID, err := CurrentBuildID()
	if err != nil {
		return DaemonInspection{}, err
	}
	inspection := DaemonInspection{
		Record: record, Identity: identity, Compatible: true,
		CurrentBuild: identity.BuildID == expectedBuildID, ExpectedBuildID: expectedBuildID,
	}
	if identity.ProtocolVersion != daemon.ProtocolVersion {
		inspection.Compatible = false
		inspection.Problems = append(inspection.Problems, fmt.Sprintf("daemon protocol %s, CLI protocol %s", identity.ProtocolVersion, daemon.ProtocolVersion))
	}
	if identity.APIVersion != api.APIVersion {
		inspection.Compatible = false
		inspection.Problems = append(inspection.Problems, fmt.Sprintf("daemon API %s, CLI API %s", identity.APIVersion, api.APIVersion))
	}
	if identity.BuildID != expectedBuildID {
		inspection.Problems = append(inspection.Problems, "daemon executable differs from the current CLI executable")
	}
	return inspection, nil
}

// CheckDaemon verifies an existing compatible daemon without starting or
// modifying it.
func CheckDaemon(ctx context.Context, paths Paths) (ControlRecord, error) {
	inspection, err := InspectDaemon(ctx, paths)
	if err != nil {
		return ControlRecord{}, err
	}
	if !inspection.Compatible || !inspection.CurrentBuild {
		return ControlRecord{}, incompatibleDaemonError(inspection)
	}
	record := inspection.Record
	// The discovery record is an atomic startup snapshot. Runtime recovery and
	// handoff safety are live properties, so diagnostics must use the freshly
	// authenticated identity response rather than stale JSON on disk.
	record.State = inspection.Identity.State
	record.HandoffReady = inspection.Identity.HandoffReady
	record.RecoveryProblems = append([]string(nil), inspection.Identity.RecoveryProblems...)
	return record, nil
}

func fetchDaemonIdentity(ctx context.Context, port int, token string) (daemon.Identity, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, daemon.IdentityPath), nil)
	if err != nil {
		return daemon.Identity{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	// Identity includes a live handoff-safety check. Container ownership probes
	// can require a few local engine round trips, so this timeout must cover more
	// than a simple health endpoint while remaining tightly bounded.
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return daemon.Identity{}, fmt.Errorf("connect to recorded daemon identity endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return daemon.Identity{}, fmt.Errorf("recorded daemon identity endpoint returned %s", response.Status)
	}
	var identity daemon.Identity
	if err := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&identity); err != nil {
		return daemon.Identity{}, fmt.Errorf("decode recorded daemon identity: %w", err)
	}
	return identity, nil
}

func writeControl(paths Paths, record ControlRecord) error {
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temporary := paths.Control + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, paths.Control); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func removeOwnControl(paths Paths, instanceID string) {
	record, err := ReadControl(paths)
	if err == nil && record.PID == os.Getpid() && record.InstanceID == instanceID {
		_ = os.Remove(paths.Control)
	}
}

func removeMatchingControl(paths Paths, expected ControlRecord) {
	current, err := ReadControl(paths)
	if err != nil || current.PID != expected.PID || current.Port != expected.Port || current.InstanceID != expected.InstanceID {
		return
	}
	_ = os.Remove(paths.Control)
}

func incompatibleDaemonError(inspection DaemonInspection) error {
	if inspection.Compatible && !inspection.CurrentBuild {
		return errors.New("Portless daemon is compatible but runs a different executable build")
	}
	return fmt.Errorf("Portless daemon is not compatible with this CLI: %s", strings.Join(inspection.Problems, "; "))
}

func unverifiedDaemonError(record ControlRecord, cause error) error {
	return fmt.Errorf("cannot authenticate the recorded Portless daemon at PID %d: %v; refusing to replace or signal it (run `portless daemon restart --force` only after confirming active environments may be interrupted)", record.PID, cause)
}

func readLogTail(path string, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := info.Size() - limit
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	content, _ := io.ReadAll(io.LimitReader(file, limit))
	return strings.TrimSpace(string(content))
}
