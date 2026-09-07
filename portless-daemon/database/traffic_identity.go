package database

import (
	"context"
	"github.com/runportless/portless/portless-daemon/model"
)

// WithLiveTrafficIdentity excludes environment replacement while clear updates its live window.
// The callback must perform only the bounded in-memory clear, without database access.
func (s *Store) WithLiveTrafficIdentity(ctx context.Context, project, environment string, expected model.ResourceVersion, clear func()) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var actual model.ResourceVersion
	var created string
	err = tx.QueryRowContext(ctx, `SELECT e.created_at,e.revision FROM environments e JOIN projects p ON p.private_key=e.project_key WHERE p.name=? COLLATE NOCASE AND e.name=? COLLATE NOCASE`, project, environment).Scan(&created, &actual.Revision)
	if err != nil {
		return mapSQLError(err)
	}
	actual.CreatedAt = parseTime(created)
	if err := checkResourceVersion(&expected, actual); err != nil {
		return err
	}
	clear()
	return tx.Commit()
}
