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
)

type ControlRecord struct {
	PID         int       `json:"pid"`
	Port        int       `json:"port"`
	APIVersion  string    `json:"apiVersion"`
	TokenPath   string    `json:"tokenPath"`
	StartedAt   time.Time `json:"startedAt"`
	ProcessHint string    `json:"processHint"`
}

func EnsureDaemon(ctx context.Context, paths Paths) (ControlRecord, error) {
	if record, err := readHealthyRecord(ctx, paths); err == nil {
		return record, nil
	}
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return ControlRecord{}, err
	}
	lock, err := os.OpenFile(paths.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return ControlRecord{}, err
	}
	defer lock.Close()
	deadline := time.Now().Add(12 * time.Second)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if record, healthErr := readHealthyRecord(ctx, paths); healthErr == nil {
			return record, nil
		}
		if time.Now().After(deadline) {
			return ControlRecord{}, errors.New("timed out waiting for another Portless CLI to start the daemon")
		}
		select {
		case <-ctx.Done():
			return ControlRecord{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if record, err := readHealthyRecord(ctx, paths); err == nil {
		return record, nil
	}
	if err := startDaemon(paths); err != nil {
		return ControlRecord{}, err
	}
	for time.Now().Before(deadline) {
		if record, err := readHealthyRecord(ctx, paths); err == nil {
			return record, nil
		}
		select {
		case <-ctx.Done():
			return ControlRecord{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	message := "daemon did not become ready; inspect " + paths.DaemonLog
	if tail := readLogTail(paths.DaemonLog, 4096); tail != "" {
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
	content, err := os.ReadFile(paths.Control)
	if err != nil {
		return ControlRecord{}, err
	}
	var record ControlRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return ControlRecord{}, err
	}
	if record.Port < 1 || record.Port > 65535 || record.PID <= 0 || record.APIVersion == "" {
		return ControlRecord{}, errors.New("invalid daemon discovery record")
	}
	return record, nil
}

// CheckDaemon verifies an existing daemon without starting or modifying it.
func CheckDaemon(ctx context.Context, paths Paths) (ControlRecord, error) {
	return readHealthyRecord(ctx, paths)
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

func removeOwnControl(paths Paths) {
	record, err := ReadControl(paths)
	if err == nil && record.PID == os.Getpid() {
		_ = os.Remove(paths.Control)
	}
}

func readHealthyRecord(ctx context.Context, paths Paths) (ControlRecord, error) {
	record, err := ReadControl(paths)
	if err != nil {
		return ControlRecord{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", record.Port), nil)
	if err != nil {
		return ControlRecord{}, err
	}
	client := &http.Client{Timeout: 350 * time.Millisecond}
	response, err := client.Do(request)
	if err != nil {
		return ControlRecord{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ControlRecord{}, fmt.Errorf("daemon health returned %s", response.Status)
	}
	var health struct {
		Ready      bool   `json:"ready"`
		APIVersion string `json:"apiVersion"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<10)).Decode(&health); err != nil {
		return ControlRecord{}, err
	}
	if !health.Ready || health.APIVersion != record.APIVersion {
		return ControlRecord{}, errors.New("daemon protocol is incompatible")
	}
	return record, nil
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
