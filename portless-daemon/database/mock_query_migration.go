package database

import (
	"context"
	"fmt"
)

// Convert persisted query values once, without accepting the retired HTTP format.
func (s *Store) migrateMockQueryMatchers(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mock query migration: %w", err)
	}
	defer tx.Rollback()
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = 11)`).Scan(&applied); err != nil {
		return fmt.Errorf("read mock query schema version: %w", err)
	}
	if applied {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE mock_scenario_routes SET query_json = (
  SELECT json_group_object(key, json(CASE WHEN value = ''
    THEN json_object('match', 'exists')
    ELSE json_object('match', 'equals', 'value', value) END))
  FROM json_each(mock_scenario_routes.query_json)
)`); err != nil {
		return fmt.Errorf("convert stored mock query matchers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(11, ?)`, nowText()); err != nil {
		return fmt.Errorf("record mock query schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mock query migration: %w", err)
	}
	return nil
}
