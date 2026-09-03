package database

import (
	"context"
	"fmt"

	"github.com/runportless/portless/portless-daemon/model"
)

// RelocateEnvironmentSources saves startup-prepared source paths and their
// relocated model at a revision without resetting runtime ownership or changing
// topology or providers. Every relocated source must have no active local
// runtime. Explicit CLI selections remain attached to the environment.
func (s *Store) RelocateEnvironmentSources(ctx context.Context, project, environment string, expectedRevision int64, definition model.ProjectModel, sources []model.SourceBinding) (model.Environment, error) {
	encoded, err := encodeProjectModel(definition)
	if err != nil {
		return model.Environment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Environment{}, err
	}
	defer tx.Rollback()
	var key string
	var revision int64
	if err := tx.QueryRowContext(ctx, `
SELECT e.private_key, e.revision FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, project, environment).Scan(&key, &revision); err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	if revision != expectedRevision {
		return model.Environment{}, ErrConflict
	}
	for _, source := range sources {
		var active int
		if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM service_runtime r
JOIN environment_bindings b ON b.environment_key = r.environment_key AND b.service_name = r.service_name COLLATE NOCASE
WHERE r.environment_key = ? AND b.provider = ? AND b.source_name = ? COLLATE NOCASE
AND r.status NOT IN (?, ?, ?, ?)`, key, model.ProviderLocal, source.Name,
			model.ServicePlanned, model.ServiceStopped, model.ServiceExited, model.ServiceFailed).Scan(&active); err != nil {
			return model.Environment{}, err
		}
		if active > 0 {
			return model.Environment{}, fmt.Errorf("source %s has active local services; its checkout cannot move", source.Name)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM environment_sources WHERE environment_key = ? AND source_name = ? COLLATE NOCASE`, key, source.Name)
		if err != nil {
			return model.Environment{}, err
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			return model.Environment{}, ErrConflict
		}
		if err := insertSource(ctx, tx, key, source); err != nil {
			return model.Environment{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE environments SET model_json = ?, revision = revision + 1, updated_at = ? WHERE private_key = ?`, encoded, nowText(), key); err != nil {
		return model.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, project, environment)
}
