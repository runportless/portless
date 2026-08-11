package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func (s *Store) CreateRecording(ctx context.Context, recording model.Recording) (model.Recording, error) {
	scope := scopeFromFields(recording.Project, recording.Environment)
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return model.Recording{}, err
	}
	if recording.StartedAt.IsZero() {
		recording.StartedAt = time.Now().UTC()
	}
	if recording.MaxEvents <= 0 {
		recording.MaxEvents = 10_000
	}
	if recording.MaxBodyBytes <= 0 {
		recording.MaxBodyBytes = 64 * 1024
	}
	var activeName string
	err = s.db.QueryRowContext(ctx, `SELECT name FROM recordings WHERE environment_key = ? AND status = 'active' LIMIT 1`, environmentKey).Scan(&activeName)
	if err == nil {
		return model.Recording{}, fmt.Errorf("recording %s is already active; stop it before starting another: %w", activeName, ErrConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.Recording{}, err
	}
	recording.Status = "active"
	var expires any
	if recording.ExpiresAt != nil {
		expires = recording.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO recordings(environment_key, name, source, target, capture_bodies, max_events, max_body_bytes, status, started_at, expires_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, environmentKey, recording.Name, recording.Source, recording.Target,
		boolInt(recording.CaptureBodies), recording.MaxEvents, recording.MaxBodyBytes, recording.Status,
		recording.StartedAt.Format(time.RFC3339Nano), expires)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return model.Recording{}, ErrAlreadyExists
		}
		return model.Recording{}, err
	}
	return recording, nil
}

func (s *Store) Recordings(ctx context.Context, selector string) ([]model.Recording, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT name, source, target, capture_bodies, max_events, max_body_bytes, status, started_at, completed_at, expires_at, event_count
FROM recordings WHERE environment_key = ? ORDER BY started_at DESC`, environmentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Recording
	for rows.Next() {
		recording, err := scanRecording(rows, selector)
		if err != nil {
			return nil, err
		}
		result = append(result, recording)
	}
	return result, rows.Err()
}

func (s *Store) Recording(ctx context.Context, selector, name string) (model.Recording, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return model.Recording{}, err
	}
	recording, err := scanRecording(s.db.QueryRowContext(ctx, `
SELECT name, source, target, capture_bodies, max_events, max_body_bytes, status, started_at, completed_at, expires_at, event_count
FROM recordings WHERE environment_key = ? AND name = ? COLLATE NOCASE`, environmentKey, name), selector)
	return recording, mapSQLError(err)
}

func (s *Store) ActiveRecordings(ctx context.Context, selector string) ([]model.Recording, error) {
	recordings, err := s.Recordings(ctx, selector)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var active []model.Recording
	for _, recording := range recordings {
		if recording.Status != "active" {
			continue
		}
		if recording.ExpiresAt != nil && now.After(*recording.ExpiresAt) {
			_ = s.StopRecording(ctx, selector, recording.Name, "expired")
			continue
		}
		active = append(active, recording)
	}
	return active, nil
}

func (s *Store) StopRecording(ctx context.Context, selector, name, reason string) error {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	status := "completed"
	if reason != "" && reason != "stopped" && reason != "expired" {
		status = "failed"
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE recordings SET status = ?, completed_at = ?
WHERE environment_key = ? AND name = ? COLLATE NOCASE AND status = 'active'`, status, nowText(), environmentKey, name)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		if _, err := s.Recording(ctx, selector, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteRecording(ctx context.Context, selector, name string) error {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM recordings WHERE environment_key = ? AND name = ? COLLATE NOCASE`, environmentKey, name).Scan(&status); err != nil {
		return mapSQLError(err)
	}
	if status == "active" {
		return fmt.Errorf("active recording must be stopped before deletion")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_events WHERE environment_key = ? AND recording_name = ? COLLATE NOCASE`, environmentKey, name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recordings WHERE environment_key = ? AND name = ? COLLATE NOCASE`, environmentKey, name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PersistTraffic(ctx context.Context, event model.TrafficEvent) error {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, scopeFromFields(event.Project, event.Environment))
	if err != nil {
		return err
	}
	if event.Recording == "" {
		return nil
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE recordings SET event_count = event_count + 1
WHERE environment_key = ? AND name = ? COLLATE NOCASE AND status = 'active' AND event_count < max_events`, environmentKey, event.Recording)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		_, _ = tx.ExecContext(ctx, `UPDATE recordings SET status = 'completed', completed_at = ? WHERE environment_key = ? AND name = ? COLLATE NOCASE AND status = 'active'`, nowText(), environmentKey, event.Recording)
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO traffic_events(environment_key, sequence, recording_name, event_json) VALUES(?, ?, ?, ?)`, environmentKey, event.Sequence, event.Recording, encoded); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordedTraffic(ctx context.Context, selector, recordingName string, limit int) ([]model.TrafficEvent, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10_000 {
		limit = 1000
	}
	query := `SELECT event_json FROM traffic_events WHERE environment_key = ?`
	args := []any{environmentKey}
	if recordingName != "" {
		query += ` AND recording_name = ? COLLATE NOCASE`
		args = append(args, recordingName)
	}
	query += ` ORDER BY sequence DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.TrafficEvent
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var event model.TrafficEvent
		if err := json.Unmarshal(encoded, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) CreateFault(ctx context.Context, fault model.FaultRule) (model.FaultRule, error) {
	scope := scopeFromFields(fault.Project, fault.Environment)
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return model.FaultRule{}, err
	}
	if fault.CreatedAt.IsZero() {
		fault.CreatedAt = time.Now().UTC()
	}
	if fault.Probability == 0 {
		fault.Probability = 1
	}
	fault.Enabled = true
	fault.Revision = 1
	if fault.ScopeSummary == "" {
		fault.ScopeSummary = scopeSummary(fault)
	}
	var expires any
	if fault.ExpiresAt != nil {
		expires = fault.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO fault_rules(environment_key, name, source, target, method, path, probability, latency_ms, jitter_ms,
  status_code, abort, enabled, created_at, expires_at, revision, scope_summary)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, 1, ?)`, environmentKey, fault.Name, fault.Source, fault.Target,
		fault.Method, fault.Path, fault.Probability, fault.LatencyMS, fault.JitterMS, fault.StatusCode, boolInt(fault.Abort),
		fault.CreatedAt.Format(time.RFC3339Nano), expires, fault.ScopeSummary)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return model.FaultRule{}, ErrAlreadyExists
		}
		return model.FaultRule{}, err
	}
	return fault, nil
}

func (s *Store) Faults(ctx context.Context, selector string, enabledOnly bool) ([]model.FaultRule, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	query := `
SELECT name, source, target, method, path, probability, latency_ms, jitter_ms, status_code, abort,
  enabled, created_at, expires_at, match_count, revision, scope_summary
FROM fault_rules WHERE environment_key = ?`
	if enabledOnly {
		query += ` AND enabled = 1`
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, environmentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.FaultRule
	now := time.Now()
	for rows.Next() {
		fault, err := scanFault(rows, selector)
		if err != nil {
			return nil, err
		}
		if fault.Enabled && fault.ExpiresAt != nil && now.After(*fault.ExpiresAt) {
			_ = s.DisableFault(ctx, selector, fault.Name)
			fault.Enabled = false
			if enabledOnly {
				continue
			}
		}
		result = append(result, fault)
	}
	return result, rows.Err()
}

func (s *Store) Fault(ctx context.Context, selector, name string) (model.FaultRule, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return model.FaultRule{}, err
	}
	fault, err := scanFault(s.db.QueryRowContext(ctx, `
SELECT name, source, target, method, path, probability, latency_ms, jitter_ms, status_code, abort,
  enabled, created_at, expires_at, match_count, revision, scope_summary
FROM fault_rules WHERE environment_key = ? AND name = ? COLLATE NOCASE`, environmentKey, name), selector)
	return fault, mapSQLError(err)
}

func (s *Store) DisableFault(ctx context.Context, selector, name string) error {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE fault_rules SET enabled = 0, revision = revision + 1 WHERE environment_key = ? AND name = ? COLLATE NOCASE`, environmentKey, name)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DisableAllFaults(ctx context.Context, selector string) (int64, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return 0, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE fault_rules SET enabled = 0, revision = revision + 1 WHERE environment_key = ? AND enabled = 1`, environmentKey)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) IncrementFaultMatch(ctx context.Context, selector, name string) error {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE fault_rules SET match_count = match_count + 1 WHERE environment_key = ? AND name = ? COLLATE NOCASE`, environmentKey, name)
	return err
}

func scanRecording(scanner rowScanner, selector string) (model.Recording, error) {
	var recording model.Recording
	var capture int
	var started string
	var completed, expires sql.NullString
	err := scanner.Scan(&recording.Name, &recording.Source, &recording.Target, &capture, &recording.MaxEvents,
		&recording.MaxBodyBytes, &recording.Status, &started, &completed, &expires, &recording.EventCount)
	recording.Project, recording.Environment = publicScope(selector)
	recording.CaptureBodies = capture != 0
	recording.StartedAt = parseTime(started)
	recording.CompletedAt = parseOptionalTime(completed)
	recording.ExpiresAt = parseOptionalTime(expires)
	return recording, err
}

func scanFault(scanner rowScanner, selector string) (model.FaultRule, error) {
	var fault model.FaultRule
	var abort, enabled int
	var created string
	var expires sql.NullString
	err := scanner.Scan(&fault.Name, &fault.Source, &fault.Target, &fault.Method, &fault.Path, &fault.Probability,
		&fault.LatencyMS, &fault.JitterMS, &fault.StatusCode, &abort, &enabled, &created, &expires,
		&fault.MatchCount, &fault.Revision, &fault.ScopeSummary)
	fault.Project, fault.Environment = publicScope(selector)
	fault.Abort = abort != 0
	fault.Enabled = enabled != 0
	fault.CreatedAt = parseTime(created)
	fault.ExpiresAt = parseOptionalTime(expires)
	return fault, err
}

func scopeSummary(fault model.FaultRule) string {
	parts := []string{"Only requests from " + fault.Source + " to " + fault.Target}
	if fault.Method != "" {
		parts = append(parts, "using "+strings.ToUpper(fault.Method))
	}
	if fault.Path != "" {
		parts = append(parts, "matching "+fault.Path)
	}
	parts = append(parts, "are affected.")
	return strings.Join(parts, " ")
}
