package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/portless-run/portless/internal/model"
)

type DaemonInstance struct {
	InstanceID string
	BuildID    string
	PID        int
	State      string
	StartedAt  time.Time
	StoppedAt  *time.Time
}

func (s *Store) RecordDaemonInstance(ctx context.Context, instance DaemonInstance) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO daemon_instances(instance_id, build_id, pid, state, started_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(instance_id) DO UPDATE SET build_id = excluded.build_id, pid = excluded.pid,
  state = excluded.state, started_at = excluded.started_at, stopped_at = NULL`,
		instance.InstanceID, instance.BuildID, instance.PID, instance.State, instance.StartedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) SetDaemonInstanceState(ctx context.Context, instanceID, state string, stopped bool) error {
	var stoppedAt any
	if stopped {
		stoppedAt = nowText()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE daemon_instances SET state = ?, stopped_at = ? WHERE instance_id = ?`, state, stoppedAt, instanceID)
	return err
}

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

func (s *Store) DeleteConnectionRuntimes(ctx context.Context, selector string) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM connection_runtime WHERE environment_key = ?`, key)
	return err
}

func (s *Store) InterruptRunningOperations(ctx context.Context, message string) error {
	now := nowText()
	_, err := s.db.ExecContext(ctx, `
UPDATE operations SET state = 'failed', completed_at = ?, error = ? WHERE state = 'running'`, now, message)
	return err
}
