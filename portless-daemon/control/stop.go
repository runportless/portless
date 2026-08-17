package control

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

	"github.com/portless-run/portless/portless-daemon/identity"
	"github.com/portless-run/portless/portless-daemon/lifecycle"
	"github.com/portless-run/portless/portless-daemon/system/installation"
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

func (m *Manager) stopDaemon(ctx context.Context, options StopOptions) (StopResult, error) {
	paths := m.layout
	if options.Timeout <= 0 {
		options.Timeout = 15 * time.Second
	}
	inspection, err := m.inspectDaemon(ctx)
	if err == nil {
		return m.stopVerifiedDaemon(ctx, inspection, options, true, "requested by the CLI")
	}
	if errors.Is(err, os.ErrNotExist) {
		return StopResult{Stopped: true, ActiveEnvironments: []string{}}, nil
	}
	record, recordErr := identity.Read(paths)
	if recordErr != nil {
		return StopResult{}, fmt.Errorf("inspect daemon before stopping it: %w", err)
	}
	alive, aliveErr := m.hooks.ProcessAlive(record.PID)
	if aliveErr != nil {
		return StopResult{}, aliveErr
	}
	if !alive {
		identity.RemoveMatching(paths, record)
		return StopResult{Stopped: true, PID: record.PID, ActiveEnvironments: []string{}}, nil
	}
	if !options.Force {
		return StopResult{}, unverifiedDaemonError(record, err)
	}
	if err := m.hooks.VerifyProcess(ctx, paths, record); err != nil {
		return StopResult{}, fmt.Errorf("refusing forced daemon stop: %w", err)
	}
	result := StopResult{WasRunning: true, Forced: true, Legacy: true, PID: record.PID, InstanceID: record.InstanceID, ActiveEnvironments: []string{}}
	if err := m.signalAndWait(ctx, record, syscall.SIGTERM, options.Timeout); err != nil {
		if killErr := m.signalAndWait(ctx, record, syscall.SIGKILL, 3*time.Second); killErr != nil {
			return result, fmt.Errorf("forced daemon stop failed after SIGTERM (%v): %w", err, killErr)
		}
	}
	result.Stopped = true
	return result, nil
}

func (m *Manager) stopVerifiedDaemon(ctx context.Context, inspection Inspection, options StopOptions, allowSignals bool, reason string) (StopResult, error) {
	paths := m.layout
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
	handoff := options.Handoff && inspection.Identity.ProtocolVersion == lifecycle.ProtocolVersion
	response, err := m.requestDaemonShutdown(ctx, inspection, options.Force, handoff, reason)
	if err != nil {
		return StopResult{}, err
	}
	result := StopResult{
		WasRunning: true, Forced: options.Force, PID: inspection.Record.PID,
		InstanceID: inspection.Record.InstanceID, ActiveEnvironments: response.ActiveEnvironments,
	}
	if err := m.waitForInstanceStop(ctx, inspection.Record, options.Timeout); err == nil {
		result.Stopped = true
		return result, nil
	} else if !allowSignals {
		return result, fmt.Errorf("daemon accepted shutdown but did not exit: %w", err)
	}

	// The instance was authenticated immediately before shutdown. Before using a
	// process signal, also verify that the unchanged control record still points
	// at a Portless daemon command owned by this user.
	if err := m.hooks.VerifyProcess(ctx, paths, inspection.Record); err != nil {
		return result, fmt.Errorf("daemon accepted shutdown but did not exit; refusing to signal PID %d: %w", inspection.Record.PID, err)
	}
	if err := m.signalAndWait(ctx, inspection.Record, syscall.SIGTERM, 3*time.Second); err == nil {
		result.Stopped = true
		return result, nil
	} else if !options.Force {
		return result, fmt.Errorf("daemon did not exit after authenticated shutdown and SIGTERM: %w", err)
	}
	if err := m.signalAndWait(ctx, inspection.Record, syscall.SIGKILL, 3*time.Second); err != nil {
		return result, fmt.Errorf("daemon did not exit after SIGKILL: %w", err)
	}
	result.Stopped = true
	return result, nil
}

func (m *Manager) requestDaemonShutdown(ctx context.Context, inspection Inspection, force, handoff bool, reason string) (lifecycle.ShutdownResponse, error) {
	payload, err := json.Marshal(lifecycle.ShutdownRequest{InstanceID: inspection.Identity.InstanceID, Force: force, Handoff: handoff, Reason: reason})
	if err != nil {
		return lifecycle.ShutdownResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d%s", inspection.Record.Port, lifecycle.ShutdownPath), bytes.NewReader(payload))
	if err != nil {
		return lifecycle.ShutdownResponse{}, err
	}
	token, err := installation.ReadPrivateTextFile(inspection.Record.TokenPath)
	if err != nil {
		return lifecycle.ShutdownResponse{}, fmt.Errorf("read CLI authentication token: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := m.hooks.HTTPClient(3 * time.Second).Do(request)
	if err != nil {
		return lifecycle.ShutdownResponse{}, fmt.Errorf("request daemon shutdown: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return lifecycle.ShutdownResponse{}, err
	}
	if response.StatusCode == http.StatusConflict {
		var envelope lifecycle.ErrorResponse
		_ = json.Unmarshal(content, &envelope)
		if len(envelope.Error.ActiveEnvironments) > 0 {
			return lifecycle.ShutdownResponse{}, &ActiveEnvironmentsError{Environments: envelope.Error.ActiveEnvironments}
		}
	}
	if response.StatusCode != http.StatusAccepted {
		return lifecycle.ShutdownResponse{}, fmt.Errorf("daemon shutdown endpoint returned %s", response.Status)
	}
	var result lifecycle.ShutdownResponse
	if err := json.Unmarshal(content, &result); err != nil {
		return lifecycle.ShutdownResponse{}, fmt.Errorf("decode daemon shutdown response: %w", err)
	}
	if !result.Stopping || result.InstanceID != inspection.Identity.InstanceID {
		return lifecycle.ShutdownResponse{}, errors.New("daemon returned an invalid shutdown acknowledgement")
	}
	if result.ActiveEnvironments == nil {
		result.ActiveEnvironments = []string{}
	}
	return result, nil
}

func (m *Manager) waitForInstanceStop(ctx context.Context, expected identity.Record, timeout time.Duration) error {
	paths := m.layout
	deadline := m.hooks.Now().Add(timeout)
	for {
		current, err := identity.Read(paths)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil && (current.PID != expected.PID || current.InstanceID != expected.InstanceID) {
			return nil
		}
		alive, aliveErr := m.hooks.ProcessAlive(expected.PID)
		if aliveErr == nil && !alive {
			identity.RemoveMatching(paths, expected)
			return nil
		}
		if m.hooks.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for PID %d", expected.PID)
		}
		if err := m.hooks.Wait(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

func (m *Manager) signalAndWait(ctx context.Context, record identity.Record, signal syscall.Signal, timeout time.Duration) error {
	if err := m.hooks.SignalProcess(record.PID, signal); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return m.waitForInstanceStop(ctx, record, timeout)
}

func signalProcess(pid int, signal os.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(signal)
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

func verifyRecordedDaemonProcess(ctx context.Context, paths installation.Layout, record identity.Record) error {
	current, err := identity.Read(paths)
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
