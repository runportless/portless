package database

import (
	"context"
	"database/sql"
	"github.com/runportless/portless/portless-daemon/model"
)

func checkResourceVersion(expected *model.ResourceVersion, actual model.ResourceVersion) error {
	if expected == nil {
		return nil
	}
	if expected.StateDigest != actual.StateDigest || !expected.CreatedAt.Equal(actual.CreatedAt) || !expected.ModifiedAt.Equal(actual.ModifiedAt) || expected.Revision != actual.Revision || !expected.ParentCreatedAt.Equal(actual.ParentCreatedAt) {
		return ErrConflict
	}
	return nil
}

func mockVersionTx(ctx context.Context, tx *sql.Tx, key, name string) (model.ResourceVersion, error) {
	var result model.ResourceVersion
	var created, modified, parent string
	err := tx.QueryRowContext(ctx, `SELECT m.created_at,m.modified_at,e.created_at FROM mock_scenarios m JOIN environments e ON e.private_key=m.environment_key WHERE m.environment_key=? AND m.name=? COLLATE NOCASE`, key, name).Scan(&created, &modified, &parent)
	if err != nil {
		return result, mapSQLError(err)
	}
	result.CreatedAt, result.ModifiedAt, result.ParentCreatedAt = parseTime(created), parseTime(modified), parseTime(parent)
	return result, nil
}
