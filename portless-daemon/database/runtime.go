package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

// DaemonInstance records one daemon process and the build that owns its runtimes.
type DaemonInstance struct {
	InstanceID string
	BuildID    string
	PID        int
	State      string
	StartedAt  time.Time
	StoppedAt  *time.Time
}

// EnvironmentRuntimeInventory is the format-independent runtime ownership
// view used by lifecycle authentication and destructive recovery. It is
// intentionally assembled from normalized environment and service-runtime
// rows, so an incompatible persisted project model cannot hide active work or
// block a guarded reset.
type EnvironmentRuntimeInventory struct {
	Project     string
	Environment string
	Status      model.EnvironmentStatus
	Services    []ServiceRuntimeRecord
}

// RuntimeInventory builds a schema-tolerant ownership view for reset and reconciliation.
func (s *Store) RuntimeInventory(ctx context.Context) ([]EnvironmentRuntimeInventory, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.name, e.name, e.status
FROM environments e JOIN projects p ON p.private_key = e.project_key
ORDER BY p.name COLLATE NOCASE, e.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	var result []EnvironmentRuntimeInventory
	for rows.Next() {
		var item EnvironmentRuntimeInventory
		var status string
		if err := rows.Scan(&item.Project, &item.Environment, &status); err != nil {
			rows.Close()
			return nil, err
		}
		item.Status = model.EnvironmentStatus(status)
		item.Services = []ServiceRuntimeRecord{}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		selector := model.EnvironmentSelector(result[index].Project, result[index].Environment)
		services, err := s.ServiceRuntimes(ctx, selector)
		if err != nil {
			return nil, err
		}
		result[index].Services = services
	}
	if result == nil {
		result = []EnvironmentRuntimeInventory{}
	}
	return result, nil
}

// ServiceRuntimes lists persisted runtime ownership records for an environment.
func (s *Store) ServiceRuntimes(ctx context.Context, selector string) ([]ServiceRuntimeRecord, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT service_name, status, reason, generation, pid, upstream_port, started_at, restart_count,
       log_path, private_run_key, owner_instance_id, supervisor_socket, supervisor_state, supervisor_pid, container_name, observed_at,
       launch_mode, debug_adapter, debug_host, debug_port, debug_state
FROM service_runtime WHERE environment_key = ? ORDER BY service_name COLLATE NOCASE`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ServiceRuntimeRecord
	for rows.Next() {
		var item ServiceRuntimeRecord
		var status string
		var launchMode, debugAdapter, debugHost, debugState string
		var debugPort int
		var started, observed sql.NullString
		if err := rows.Scan(
			&item.ServiceName, &status, &item.Reason, &item.Generation, &item.PID, &item.UpstreamPort,
			&started, &item.RestartCount, &item.LogPath, &item.PrivateRunKey, &item.OwnerInstanceID,
			&item.SupervisorSocket, &item.SupervisorState, &item.SupervisorPID, &item.ContainerName, &observed,
			&launchMode, &debugAdapter, &debugHost, &debugPort, &debugState,
		); err != nil {
			return nil, err
		}
		item.Status = model.ServiceStatus(status)
		item.LaunchMode = model.LaunchMode(launchMode)
		if debugAdapter != "" {
			item.Debugger = &model.DebuggerRuntime{Adapter: model.DebugAdapter(debugAdapter), Host: debugHost, Port: debugPort, State: debugState}
		}
		item.StartedAt = parseOptionalTime(started)
		item.ObservedAt = parseOptionalTime(observed)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []ServiceRuntimeRecord{}
	}
	return result, nil
}

// RecordDaemonInstance inserts or refreshes the current daemon ownership record.
func (s *Store) RecordDaemonInstance(ctx context.Context, instance DaemonInstance) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO daemon_instances(instance_id, build_id, pid, state, started_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(instance_id) DO UPDATE SET build_id = excluded.build_id, pid = excluded.pid,
  state = excluded.state, started_at = excluded.started_at, stopped_at = NULL`,
		instance.InstanceID, instance.BuildID, instance.PID, instance.State, instance.StartedAt.UTC().Format(time.RFC3339Nano))
	return err
}

// SetDaemonInstanceState updates daemon lifecycle state and optional stop time.
func (s *Store) SetDaemonInstanceState(ctx context.Context, instanceID, state string, stopped bool) error {
	var stoppedAt any
	if stopped {
		stoppedAt = nowText()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE daemon_instances SET state = ?, stopped_at = ? WHERE instance_id = ?`, state, stoppedAt, instanceID)
	return err
}

// ConnectionRuntime records ownership and listener state for one dependency proxy.
type ConnectionRuntime struct {
	Source           string
	Target           string
	Protocol         model.Protocol
	SourceGeneration int64
	ListenIP         string
	DNSName          string
	ListenPort       int
	OwnerInstanceID  string
	State            string
	Reason           string
	ObservedAt       *time.Time
}

// ConnectionRuntime returns the persisted runtime state for one dependency edge.
func (s *Store) ConnectionRuntime(ctx context.Context, selector, source, target string) (ConnectionRuntime, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return ConnectionRuntime{}, err
	}
	var result ConnectionRuntime
	var protocol string
	var observed sql.NullString
	err = s.db.QueryRowContext(ctx, `
SELECT source_name, target_name, protocol, source_generation, listen_ip, dns_name, listen_port, owner_instance_id, state, reason, observed_at
FROM connection_runtime WHERE environment_key = ? AND source_name = ? COLLATE NOCASE AND target_name = ? COLLATE NOCASE`,
		key, source, target).Scan(&result.Source, &result.Target, &protocol, &result.SourceGeneration, &result.ListenIP, &result.DNSName, &result.ListenPort,
		&result.OwnerInstanceID, &result.State, &result.Reason, &observed)
	if err != nil {
		return ConnectionRuntime{}, mapSQLError(err)
	}
	result.Protocol = model.Protocol(protocol)
	result.ObservedAt = parseOptionalTime(observed)
	return result, nil
}

// ConnectionRuntimes lists all persisted dependency proxy runtimes for an environment.
func (s *Store) ConnectionRuntimes(ctx context.Context, selector string) ([]ConnectionRuntime, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT source_name, target_name, protocol, source_generation, listen_ip, dns_name, listen_port, owner_instance_id, state, reason, observed_at
FROM connection_runtime WHERE environment_key = ? ORDER BY source_name COLLATE NOCASE, target_name COLLATE NOCASE`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ConnectionRuntime
	for rows.Next() {
		var item ConnectionRuntime
		var protocol string
		var observed sql.NullString
		if err := rows.Scan(&item.Source, &item.Target, &protocol, &item.SourceGeneration, &item.ListenIP, &item.DNSName, &item.ListenPort,
			&item.OwnerInstanceID, &item.State, &item.Reason, &observed); err != nil {
			return nil, err
		}
		item.Protocol = model.Protocol(protocol)
		item.ObservedAt = parseOptionalTime(observed)
		result = append(result, item)
	}
	return result, rows.Err()
}

// SaveConnectionRuntime inserts or replaces the runtime record for a dependency edge.
func (s *Store) SaveConnectionRuntime(ctx context.Context, selector string, runtime ConnectionRuntime) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	var observed any
	if runtime.ObservedAt != nil {
		observed = runtime.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO connection_runtime(environment_key, source_name, target_name, protocol, source_generation,
  listen_ip, dns_name, listen_port, owner_instance_id, state, reason, observed_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(environment_key, source_name, target_name) DO UPDATE SET protocol = excluded.protocol,
  source_generation = excluded.source_generation, listen_ip = excluded.listen_ip, dns_name = excluded.dns_name,
  listen_port = excluded.listen_port,
  owner_instance_id = excluded.owner_instance_id, state = excluded.state, reason = excluded.reason,
  observed_at = excluded.observed_at`, key, runtime.Source, runtime.Target, runtime.Protocol,
		runtime.SourceGeneration, runtime.ListenIP, runtime.DNSName, runtime.ListenPort, runtime.OwnerInstanceID, runtime.State, runtime.Reason, observed)
	return err
}

// DeleteConnectionRuntimes removes all dependency proxy ownership records for an environment.
func (s *Store) DeleteConnectionRuntimes(ctx context.Context, selector string) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM connection_runtime WHERE environment_key = ?`, key)
	return err
}

// InterruptRunningOperations fails operations left running across a daemon restart.
func (s *Store) InterruptRunningOperations(ctx context.Context, message string) error {
	now := nowText()
	_, err := s.db.ExecContext(ctx, `
UPDATE operations SET state = 'failed', completed_at = ?, error = ? WHERE state = 'running'`, now, message)
	return err
}
