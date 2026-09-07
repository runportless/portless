package database

import (
	"context"
	"fmt"
	"github.com/runportless/portless/portless-daemon/model"
	"time"
)

// RecordingExportSnapshot captures an identity, event count, and sequence watermark atomically.
func (s *Store) RecordingExportSnapshot(ctx context.Context, project, environment, name string) (model.RecordingExportSnapshot, error) {
	result := model.RecordingExportSnapshot{Project: project, Environment: environment, Recording: name}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var started, created string
	var key string
	err = tx.QueryRowContext(ctx, `SELECT e.private_key,p.name,e.name,r.name,r.started_at,e.created_at FROM recordings r JOIN environments e ON e.private_key=r.environment_key JOIN projects p ON p.private_key=e.project_key WHERE p.name=? COLLATE NOCASE AND e.name=? COLLATE NOCASE AND r.name=? COLLATE NOCASE`, project, environment, name).Scan(&key, &result.Project, &result.Environment, &result.Recording, &started, &created)
	if err != nil {
		return result, mapSQLError(err)
	}
	result.RecordingStartedAt, result.EnvironmentCreatedAt = parseTime(started), parseTime(created)
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(sequence),0) FROM traffic_events WHERE environment_key=? AND recording_name=? COLLATE NOCASE`, key, name).Scan(&result.EventCount, &result.ThroughSequence); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// ValidateRecordingExportSnapshot rejects deleted or replaced recording/environment identities.
func (s *Store) ValidateRecordingExportSnapshot(ctx context.Context, snapshot model.RecordingExportSnapshot) error {
	var started, created string
	err := s.db.QueryRowContext(ctx, `SELECT r.started_at,e.created_at FROM recordings r JOIN environments e ON e.private_key=r.environment_key JOIN projects p ON p.private_key=e.project_key WHERE p.name=? COLLATE NOCASE AND e.name=? COLLATE NOCASE AND r.name=? COLLATE NOCASE`, snapshot.Project, snapshot.Environment, snapshot.Recording).Scan(&started, &created)
	if err != nil {
		return mapSQLError(err)
	}
	if !parseTime(started).Equal(snapshot.RecordingStartedAt) || !parseTime(created).Equal(snapshot.EnvironmentCreatedAt) {
		return fmt.Errorf("recording export identity changed: %w", ErrConflict)
	}
	return nil
}

// RecordingExportEvent reads one exact event or the next descending indexed sequence.
// Snapshot identities are checked in the same query that reads application data.
func (s *Store) RecordingExportEvent(ctx context.Context, snapshot model.RecordingExportSnapshot, before, sequence int64) (model.RecordingExportEvent, error) {
	var result model.RecordingExportEvent
	err := s.db.QueryRowContext(ctx, `SELECT t.sequence,t.event_json FROM traffic_events t JOIN recordings r ON r.environment_key=t.environment_key AND r.name=t.recording_name COLLATE NOCASE JOIN environments e ON e.private_key=t.environment_key JOIN projects p ON p.private_key=e.project_key
 WHERE p.name=? COLLATE NOCASE AND e.name=? COLLATE NOCASE AND r.name=? COLLATE NOCASE AND r.started_at=? AND e.created_at=? AND t.sequence<=? AND (?=0 OR t.sequence<?) AND (?=0 OR t.sequence=?) ORDER BY t.sequence DESC LIMIT 1`, snapshot.Project, snapshot.Environment, snapshot.Recording, snapshot.RecordingStartedAt.UTC().Format(time.RFC3339Nano), snapshot.EnvironmentCreatedAt.UTC().Format(time.RFC3339Nano), snapshot.ThroughSequence, before, before, sequence, sequence).Scan(&result.Sequence, &result.JSON)
	return result, mapSQLError(err)
}
