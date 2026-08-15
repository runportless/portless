package bootstrap

import (
	"bytes"
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

	"github.com/portless-run/portless/internal/daemon"
)

type StopOptions struct {
	Force   bool
	Handoff bool
	Timeout time.Duration
}

type StopResult struct {
	WasRunning         bool     `json:"wasRunning"`
	Stopped            bool     `json:"stopped"`
	Forced             bool     `json:"forced"`
	Legacy             bool     `json:"legacy"`
	PID                int      `json:"pid,omitempty"`
	InstanceID         string   `json:"instanceId,omitempty"`
	ActiveEnvironments []string `json:"activeEnvironments"`
}

type ActiveEnvironmentsError struct {
	Environments []string
}

func (e *ActiveEnvironmentsError) Error() string {
	return fmt.Sprintf("daemon is managing active environments: %s; stop them first with `portless down --all`, or use `portless daemon restart --force` (or `stop --force`) to leave their processes and containers unmanaged", strings.Join(e.Environments, ", "))
}

func StopDaemon(ctx context.Context, paths Paths, options StopOptions) (StopResult, error) {
	if options.Timeout <= 0 {
		options.Timeout = 15 * time.Second
	}
	inspection, err := InspectDaemon(ctx, paths)
	if err == nil {
		return stopVerifiedDaemon(ctx, paths, inspection, options, true, "requested by the CLI")
	}
	if errors.Is(err, os.ErrNotExist) {
		return StopResult{Stopped: true, ActiveEnvironments: []string{}}, nil
	}
	record, recordErr := ReadControl(paths)
	if recordErr != nil {
		return StopResult{}, fmt.Errorf("inspect daemon before stopping it: %w", err)
	}
	alive, aliveErr := processIsAlive(record.PID)
	if aliveErr != nil {
		return StopResult{}, aliveErr
	}
	if !alive {
		removeMatchingControl(paths, record)
		return StopResult{Stopped: true, PID: record.PID, ActiveEnvironments: []string{}}, nil
	}
	if !options.Force {
		return StopResult{}, unverifiedDaemonError(record, err)
	}
	if err := verifyRecordedDaemonProcess(ctx, paths, record); err != nil {
		return StopResult{}, fmt.Errorf("refusing forced daemon stop: %w", err)
	}
	result := StopResult{WasRunning: true, Forced: true, Legacy: true, PID: record.PID, InstanceID: record.InstanceID, ActiveEnvironments: []string{}}
	if err := signalAndWait(ctx, paths, record, syscall.SIGTERM, options.Timeout); err != nil {
		if killErr := signalAndWait(ctx, paths, record, syscall.SIGKILL, 3*time.Second); killErr != nil {
			return result, fmt.Errorf("forced daemon stop failed after SIGTERM (%v): %w", err, killErr)
		}
	}
	result.Stopped = true
	return result, nil
}

func stopVerifiedDaemon(ctx context.Context, paths Paths, inspection DaemonInspection, options StopOptions, allowSignals bool, reason string) (StopResult, error) {
	if options.Timeout <= 0 {
		options.Timeout = 15 * time.Second
	}
	active := append([]string(nil), inspection.Identity.ActiveEnvironments...)
	if len(active) > 0 && !options.Force && !(options.Handoff && inspection.Identity.HandoffReady) {
		return StopResult{}, &ActiveEnvironmentsError{Environments: active}
	}
	// Older authenticated lifecycle protocols do not know the handoff field and
	// may reject it under strict JSON decoding. They can still perform an ordinary
	// or forced shutdown; only current-protocol daemons receive a handoff request.
	handoff := options.Handoff && inspection.Identity.ProtocolVersion == daemon.ProtocolVersion
	response, err := requestDaemonShutdown(ctx, inspection, options.Force, handoff, reason)
	if err != nil {
		return StopResult{}, err
	}
	result := StopResult{
		WasRunning: true, Forced: options.Force, PID: inspection.Record.PID,
		InstanceID: inspection.Record.InstanceID, ActiveEnvironments: response.ActiveEnvironments,
	}
	if err := waitForInstanceStop(ctx, paths, inspection.Record, options.Timeout); err == nil {
		result.Stopped = true
		return result, nil
	} else if !allowSignals {
		return result, fmt.Errorf("daemon accepted shutdown but did not exit: %w", err)
	}

	// The instance was authenticated immediately before shutdown. Before using a
	// process signal, also verify that the unchanged control record still points
	// at a Portless daemon command owned by this user.
	if err := verifyRecordedDaemonProcess(ctx, paths, inspection.Record); err != nil {
		return result, fmt.Errorf("daemon accepted shutdown but did not exit; refusing to signal PID %d: %w", inspection.Record.PID, err)
	}
	if err := signalAndWait(ctx, paths, inspection.Record, syscall.SIGTERM, 3*time.Second); err == nil {
		result.Stopped = true
		return result, nil
	} else if !options.Force {
		return result, fmt.Errorf("daemon did not exit after authenticated shutdown and SIGTERM: %w", err)
	}
	if err := signalAndWait(ctx, paths, inspection.Record, syscall.SIGKILL, 3*time.Second); err != nil {
		return result, fmt.Errorf("daemon did not exit after SIGKILL: %w", err)
	}
	result.Stopped = true
	return result, nil
}

func requestDaemonShutdown(ctx context.Context, inspection DaemonInspection, force, handoff bool, reason string) (daemon.ShutdownResponse, error) {
	payload, err := json.Marshal(daemon.ShutdownRequest{InstanceID: inspection.Identity.InstanceID, Force: force, Handoff: handoff, Reason: reason})
	if err != nil {
		return daemon.ShutdownResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", inspection.Record.Port, daemon.ShutdownPath), bytes.NewReader(payload))
	if err != nil {
		return daemon.ShutdownResponse{}, err
	}
	token, err := readPrivateTextFile(inspection.Record.TokenPath)
	if err != nil {
		return daemon.ShutdownResponse{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		return daemon.ShutdownResponse{}, fmt.Errorf("request daemon shutdown: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return daemon.ShutdownResponse{}, err
	}
	if response.StatusCode == http.StatusConflict {
		var envelope daemon.ErrorResponse
		_ = json.Unmarshal(content, &envelope)
		if len(envelope.Error.ActiveEnvironments) > 0 {
			return daemon.ShutdownResponse{}, &ActiveEnvironmentsError{Environments: envelope.Error.ActiveEnvironments}
		}
	}
	if response.StatusCode != http.StatusAccepted {
		return daemon.ShutdownResponse{}, fmt.Errorf("daemon shutdown endpoint returned %s", response.Status)
	}
	var result daemon.ShutdownResponse
	if err := json.Unmarshal(content, &result); err != nil {
		return daemon.ShutdownResponse{}, fmt.Errorf("decode daemon shutdown response: %w", err)
	}
	if !result.Stopping || result.InstanceID != inspection.Identity.InstanceID {
		return daemon.ShutdownResponse{}, errors.New("daemon returned an invalid shutdown acknowledgement")
	}
	if result.ActiveEnvironments == nil {
		result.ActiveEnvironments = []string{}
	}
	return result, nil
}

func waitForInstanceStop(ctx context.Context, paths Paths, expected ControlRecord, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		current, err := ReadControl(paths)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil && (current.PID != expected.PID || current.InstanceID != expected.InstanceID) {
			return nil
		}
		alive, aliveErr := processIsAlive(expected.PID)
		if aliveErr == nil && !alive {
			removeMatchingControl(paths, expected)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for PID %d", expected.PID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func signalAndWait(ctx context.Context, paths Paths, record ControlRecord, signal syscall.Signal, timeout time.Duration) error {
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return err
	}
	if err := process.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return waitForInstanceStop(ctx, paths, record, timeout)
}

func processIsAlive(pid int) (bool, error) {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = process.Signal(syscall.Signal(0))
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, os.ErrProcessDone), errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func verifyRecordedDaemonProcess(ctx context.Context, paths Paths, record ControlRecord) error {
	current, err := ReadControl(paths)
	if err != nil {
		return fmt.Errorf("re-read control record: %w", err)
	}
	if current.PID != record.PID || current.Port != record.Port || current.InstanceID != record.InstanceID {
		return errors.New("daemon control record changed")
	}
	command := exec.CommandContext(ctx, "ps", "-ww", "-p", strconv.Itoa(record.PID), "-o", "uid=", "-o", "command=")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect recorded process: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 {
		return errors.New("recorded process details are incomplete")
	}
	uid, err := strconv.Atoi(fields[0])
	if err != nil || uid != os.Geteuid() {
		return fmt.Errorf("recorded process belongs to UID %s, expected UID %d", fields[0], os.Geteuid())
	}
	processCommand := strings.Join(fields[1:], " ")
	if !strings.Contains(processCommand, "__daemon") || !strings.Contains(processCommand, "--data-dir") || !strings.Contains(processCommand, paths.Root) {
		return errors.New("recorded PID is not a Portless daemon for this data directory")
	}
	return nil
}
