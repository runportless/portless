package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

// ServiceRuntimeUpdate contains the complete mutable runtime state for a service.
type ServiceRuntimeUpdate struct {
	Status           model.ServiceStatus
	Reason           string
	Generation       int64
	PID              int
	UpstreamPort     int
	StartedAt        *time.Time
	RestartCount     int64
	LogPath          string
	PrivateRunKey    string
	OwnerInstanceID  string
	SupervisorSocket string
	SupervisorState  string
	SupervisorPID    int
	ContainerName    string
	ObservedAt       *time.Time
	LaunchMode       model.LaunchMode
	Debugger         *model.DebuggerRuntime
}

// ServiceRuntimeRecord is the durable runtime and ownership record for a service.
type ServiceRuntimeRecord struct {
	ServiceName      string
	Status           model.ServiceStatus
	Reason           string
	Generation       int64
	PID              int
	UpstreamPort     int
	StartedAt        *time.Time
	RestartCount     int64
	LogPath          string
	PrivateRunKey    string
	OwnerInstanceID  string
	SupervisorSocket string
	SupervisorState  string
	SupervisorPID    int
	ContainerName    string
	ObservedAt       *time.Time
	LaunchMode       model.LaunchMode
	Debugger         *model.DebuggerRuntime
}

// SetServiceRuntime replaces the complete runtime state for an existing service row.
func (s *Store) SetServiceRuntime(ctx context.Context, selector, serviceName string, update ServiceRuntimeUpdate) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	var started any
	if update.StartedAt != nil {
		started = update.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	var observed any
	if update.ObservedAt != nil {
		observed = update.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	if update.LaunchMode == "" {
		update.LaunchMode = model.LaunchManaged
	}
	debugAdapter, debugHost, debugPort, debugState := "", "", 0, ""
	if update.Debugger != nil {
		debugAdapter = string(update.Debugger.Adapter)
		debugHost = update.Debugger.Host
		debugPort = update.Debugger.Port
		debugState = update.Debugger.State
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET status = ?, reason = ?, generation = ?, pid = ?, upstream_port = ?,
  started_at = ?, restart_count = ?, log_path = ?, private_run_key = ?, owner_instance_id = ?,
  supervisor_socket = ?, supervisor_state = ?, supervisor_pid = ?, container_name = ?, observed_at = ?,
  launch_mode = ?, debug_adapter = ?, debug_host = ?, debug_port = ?, debug_state = ?
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, update.Status, update.Reason, update.Generation,
		update.PID, update.UpstreamPort, started, update.RestartCount, update.LogPath, update.PrivateRunKey,
		update.OwnerInstanceID, update.SupervisorSocket, update.SupervisorState, update.SupervisorPID, update.ContainerName, observed,
		update.LaunchMode, debugAdapter, debugHost, debugPort, debugState, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// ServiceRuntime returns the durable runtime state for one service.
func (s *Store) ServiceRuntime(ctx context.Context, selector, serviceName string) (ServiceRuntimeRecord, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return ServiceRuntimeRecord{}, err
	}
	var result ServiceRuntimeRecord
	var status string
	var launchMode, debugAdapter, debugHost, debugState string
	var debugPort int
	var started, observed sql.NullString
	err = s.db.QueryRowContext(ctx, `
SELECT service_name, status, reason, generation, pid, upstream_port, started_at, restart_count,
       log_path, private_run_key, owner_instance_id, supervisor_socket, supervisor_state, supervisor_pid, container_name, observed_at,
       launch_mode, debug_adapter, debug_host, debug_port, debug_state
FROM service_runtime WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, key, serviceName).Scan(
		&result.ServiceName, &status, &result.Reason, &result.Generation, &result.PID, &result.UpstreamPort,
		&started, &result.RestartCount, &result.LogPath, &result.PrivateRunKey, &result.OwnerInstanceID,
		&result.SupervisorSocket, &result.SupervisorState, &result.SupervisorPID, &result.ContainerName, &observed,
		&launchMode, &debugAdapter, &debugHost, &debugPort, &debugState,
	)
	if err != nil {
		return ServiceRuntimeRecord{}, mapSQLError(err)
	}
	result.Status = model.ServiceStatus(status)
	result.LaunchMode = model.LaunchMode(launchMode)
	if debugAdapter != "" {
		result.Debugger = &model.DebuggerRuntime{Adapter: model.DebugAdapter(debugAdapter), Host: debugHost, Port: debugPort, State: debugState}
	}
	result.StartedAt = parseOptionalTime(started)
	result.ObservedAt = parseOptionalTime(observed)
	return result, nil
}

// SetServiceStatus updates only the observed status and reason for a service.
func (s *Store) SetServiceStatus(ctx context.Context, selector, serviceName string, status model.ServiceStatus, reason string) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET status = ?, reason = ?
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, status, reason, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// SetServiceLaunch updates launch mode and debugger endpoint metadata.
func (s *Store) SetServiceLaunch(ctx context.Context, selector, serviceName string, mode model.LaunchMode, debugger *model.DebuggerRuntime) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	if mode == "" {
		mode = model.LaunchManaged
	}
	debugAdapter, debugHost, debugPort, debugState := "", "", 0, ""
	if debugger != nil {
		debugAdapter, debugHost, debugPort, debugState = string(debugger.Adapter), debugger.Host, debugger.Port, debugger.State
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET launch_mode = ?, debug_adapter = ?, debug_host = ?, debug_port = ?, debug_state = ?
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, mode, debugAdapter, debugHost, debugPort, debugState, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// SetServiceDebuggerState updates the state of an existing debugger listener.
func (s *Store) SetServiceDebuggerState(ctx context.Context, selector, serviceName, state string) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET debug_state = ?
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE AND debug_adapter <> ''`, state, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

// PrivateEnvironmentKey resolves public names to the opaque environment storage key.
func (s *Store) PrivateEnvironmentKey(ctx context.Context, projectName, environmentName string) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `
SELECT e.private_key FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName).Scan(&key)
	if err != nil {
		return "", mapSQLError(err)
	}
	return key, nil
}

// PrivateEnvironmentKeyForSelector resolves a project/environment selector to its storage key.
func (s *Store) PrivateEnvironmentKeyForSelector(ctx context.Context, selector string) (string, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return "", err
	}
	return s.PrivateEnvironmentKey(ctx, project, environment)
}

// ServiceLogPath returns the managed log file recorded for a service runtime.
func (s *Store) ServiceLogPath(ctx context.Context, selector, serviceName string) (string, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return "", err
	}
	var path string
	if err := s.db.QueryRowContext(ctx, `SELECT log_path FROM service_runtime WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, key, serviceName).Scan(&path); err != nil {
		return "", mapSQLError(err)
	}
	return path, nil
}
