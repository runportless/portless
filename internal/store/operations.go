package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/portless-run/portless/internal/model"
)

func (s *Store) CreateOperation(ctx context.Context, selector, operationType, actor, idempotencyKey string) (model.Operation, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return model.Operation{}, err
	}
	if idempotencyKey != "" {
		var number int64
		err := s.db.QueryRowContext(ctx, `SELECT number FROM operations WHERE environment_key = ? AND idempotency_key = ?`, environmentKey, idempotencyKey).Scan(&number)
		if err == nil {
			return s.Operation(ctx, selector, number)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.Operation{}, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Operation{}, err
	}
	defer tx.Rollback()
	var number int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM operations WHERE environment_key = ?`, environmentKey).Scan(&number); err != nil {
		return model.Operation{}, err
	}
	started := nowText()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO operations(environment_key, number, type, state, actor, started_at, idempotency_key)
VALUES(?, ?, ?, 'running', ?, ?, ?)`, environmentKey, number, operationType, actor, started, idempotencyKey); err != nil {
		return model.Operation{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Operation{}, err
	}
	project, environment := publicScope(selector)
	return model.Operation{Project: project, Environment: environment, Number: number, Type: operationType, State: "running", Actor: actor, StartedAt: parseTime(started)}, nil
}

func (s *Store) AddOperationEvent(ctx context.Context, selector string, operationNumber int64, event model.OperationEvent) (model.OperationEvent, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return model.OperationEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.OperationEvent{}, err
	}
	defer tx.Rollback()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0) + 1 FROM operation_events
WHERE environment_key = ? AND operation_number = ?`, environmentKey, operationNumber).Scan(&event.Sequence); err != nil {
		return model.OperationEvent{}, err
	}
	payload, _ := json.Marshal(event.Payload)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO operation_events(environment_key, operation_number, sequence, timestamp, type, subject, message, payload_json)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, environmentKey, operationNumber, event.Sequence,
		event.Timestamp.Format(time.RFC3339Nano), event.Type, event.Subject, event.Message, payload); err != nil {
		return model.OperationEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.OperationEvent{}, err
	}
	return event, nil
}

func (s *Store) CompleteOperation(ctx context.Context, selector string, operationNumber int64, state, operationError string) error {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE operations SET state = ?, completed_at = ?, error = ?
WHERE environment_key = ? AND number = ?`, state, nowText(), operationError, environmentKey, operationNumber)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Operation(ctx context.Context, selector string, number int64) (model.Operation, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return model.Operation{}, err
	}
	var result model.Operation
	var started string
	var completed sql.NullString
	err = s.db.QueryRowContext(ctx, `
SELECT type, state, actor, started_at, completed_at, error
FROM operations WHERE environment_key = ? AND number = ?`, environmentKey, number).
		Scan(&result.Type, &result.State, &result.Actor, &started, &completed, &result.Error)
	if err != nil {
		return model.Operation{}, mapSQLError(err)
	}
	result.Project, result.Environment = publicScope(selector)
	result.Number = number
	result.StartedAt = parseTime(started)
	result.CompletedAt = parseOptionalTime(completed)
	events, err := s.OperationEvents(ctx, selector, number)
	if err != nil {
		return model.Operation{}, err
	}
	result.Events = events
	return result, nil
}

func (s *Store) Operations(ctx context.Context, selector string, limit int) ([]model.Operation, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT number, type, state, actor, started_at, completed_at, error
FROM operations WHERE environment_key = ? ORDER BY number DESC LIMIT ?`, environmentKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Operation, 0)
	for rows.Next() {
		var operation model.Operation
		var started string
		var completed sql.NullString
		if err := rows.Scan(&operation.Number, &operation.Type, &operation.State, &operation.Actor, &started, &completed, &operation.Error); err != nil {
			return nil, err
		}
		operation.Project, operation.Environment = publicScope(selector)
		operation.StartedAt = parseTime(started)
		operation.CompletedAt = parseOptionalTime(completed)
		operation.Events = []model.OperationEvent{}
		result = append(result, operation)
	}
	return result, rows.Err()
}

func (s *Store) OperationEvents(ctx context.Context, selector string, number int64) ([]model.OperationEvent, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT sequence, timestamp, type, subject, message, payload_json
FROM operation_events WHERE environment_key = ? AND operation_number = ? ORDER BY sequence`, environmentKey, number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.OperationEvent
	for rows.Next() {
		var event model.OperationEvent
		var timestamp string
		var payload []byte
		if err := rows.Scan(&event.Sequence, &timestamp, &event.Type, &event.Subject, &event.Message, &payload); err != nil {
			return nil, err
		}
		event.Timestamp = parseTime(timestamp)
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &event.Payload)
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) AddTimelineEvent(ctx context.Context, event model.TimelineEvent) (model.TimelineEvent, error) {
	scope := scopeFromFields(event.Project, event.Environment)
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, scope)
	if err != nil {
		return model.TimelineEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.TimelineEvent{}, err
	}
	defer tx.Rollback()
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM timeline_events WHERE environment_key = ?`, environmentKey).Scan(&event.Sequence); err != nil {
		return model.TimelineEvent{}, err
	}
	details, _ := json.Marshal(event.Details)
	_, err = tx.ExecContext(ctx, `
INSERT INTO timeline_events(environment_key, sequence, timestamp, actor, type, subject, severity, summary, details_json)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, environmentKey, event.Sequence, event.Timestamp.Format(time.RFC3339Nano),
		event.Actor, event.Type, event.Subject, event.Severity, event.Summary, details)
	if err != nil {
		return model.TimelineEvent{}, fmt.Errorf("insert timeline event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.TimelineEvent{}, err
	}
	return event, nil
}

func (s *Store) Timeline(ctx context.Context, selector string, limit int) ([]model.TimelineEvent, error) {
	environmentKey, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT sequence, timestamp, actor, type, subject, severity, summary, details_json
FROM timeline_events WHERE environment_key = ? ORDER BY sequence DESC LIMIT ?`, environmentKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.TimelineEvent
	for rows.Next() {
		var event model.TimelineEvent
		var timestamp string
		var details []byte
		if err := rows.Scan(&event.Sequence, &timestamp, &event.Actor, &event.Type, &event.Subject, &event.Severity, &event.Summary, &details); err != nil {
			return nil, err
		}
		event.Project, event.Environment = publicScope(selector)
		event.Timestamp = parseTime(timestamp)
		if len(details) > 0 {
			_ = json.Unmarshal(details, &event.Details)
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
