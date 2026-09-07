package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/runportless/portless/portless-daemon/model"
)

// PreviewConfiguration reads a coherent identity, eligibility, and retained-data snapshot.
func (s *Store) PreviewConfiguration(ctx context.Context, project, environment string) (model.ConfigurationPreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ConfigurationPreview{}, err
	}
	defer tx.Rollback()
	result, err := configurationPreviewTx(ctx, tx, project, environment)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func configurationPreviewTx(ctx context.Context, tx *sql.Tx, project, environment string) (model.ConfigurationPreview, error) {
	result := model.ConfigurationPreview{Project: project, Environment: environment, Environments: []model.ConfigurationEnvironmentPreview{}, Blocked: []string{}, Retained: []string{"filesystem checkouts", "managed volumes and container data", "other projects"}}
	var key, created string
	if err := tx.QueryRowContext(ctx, `SELECT private_key,created_at,revision FROM projects WHERE name=? COLLATE NOCASE`, project).Scan(&key, &created, &result.Expected.Revision); err != nil {
		return result, mapSQLError(err)
	}
	result.Expected.CreatedAt = parseTime(created)
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	_ = encoder.Encode(result.Expected)
	query := `SELECT private_key,name,created_at,revision,status FROM environments WHERE project_key=?`
	args := []any{key}
	if environment != "" {
		query += ` AND name=? COLLATE NOCASE`
		args = append(args, environment)
	}
	query += ` ORDER BY name COLLATE NOCASE`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return result, err
	}
	type row struct {
		key, name, created, status string
		revision                   int64
	}
	var stored []row
	for rows.Next() {
		var value row
		if err := rows.Scan(&value.key, &value.name, &value.created, &value.revision, &value.status); err != nil {
			rows.Close()
			return result, err
		}
		stored = append(stored, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	if environment != "" && len(stored) == 0 {
		return result, ErrNotFound
	}
	for _, entry := range stored {
		_ = encoder.Encode([]any{entry.name, entry.created, entry.revision, entry.status})
		if environment != "" {
			result.Expected.ParentCreatedAt = result.Expected.CreatedAt
			result.Expected.CreatedAt = parseTime(entry.created)
			result.Expected.Revision = entry.revision
		}
		item := model.ConfigurationEnvironmentPreview{Name: entry.name, Revision: entry.revision, Status: model.EnvironmentStatus(entry.status)}
		item.Sources, item.Services, item.Recordings, item.Faults, item.MockScenarios = []string{}, []string{}, []string{}, []string{}, []string{}
		if item.Status != model.EnvironmentStopped {
			result.Blocked = append(result.Blocked, entry.name+": environment must be stopped")
		}
		// Hash selected metadata, never payloads, runtime credentials, or executable values.
		specs := []struct {
			query string
			names *[]string
		}{
			{`SELECT source_name,path,created_at,scanned_at FROM environment_sources WHERE environment_key=? ORDER BY source_name COLLATE NOCASE`, &item.Sources},
			{`SELECT service_name,provider,source_name,modified_at FROM environment_bindings WHERE environment_key=? ORDER BY service_name COLLATE NOCASE`, &item.Services},
			{`SELECT name,started_at,status,event_count,completed_at FROM recordings WHERE environment_key=? ORDER BY name COLLATE NOCASE`, &item.Recordings},
			{`SELECT name,created_at,revision,enabled FROM fault_rules WHERE environment_key=? ORDER BY name COLLATE NOCASE`, &item.Faults},
			{`SELECT name,created_at,modified_at FROM mock_scenarios WHERE environment_key=? ORDER BY name COLLATE NOCASE`, &item.MockScenarios},
			{`SELECT scenario_name,name,modified_at FROM mock_scenario_routes WHERE environment_key=? ORDER BY scenario_name,name`, nil},
			{`SELECT number,state,started_at,completed_at FROM operations WHERE environment_key=? ORDER BY number`, nil},
		}
		for _, spec := range specs {
			rs, err := tx.QueryContext(ctx, spec.query, entry.key)
			if err != nil {
				return result, err
			}
			cols, err := rs.Columns()
			if err != nil {
				rs.Close()
				return result, err
			}
			for rs.Next() {
				values := make([]any, len(cols))
				pointers := make([]any, len(cols))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err := rs.Scan(pointers...); err != nil {
					rs.Close()
					return result, err
				}
				_ = encoder.Encode(values)
				if spec.names != nil {
					*spec.names = append(*spec.names, fmt.Sprint(values[0]))
				}
			}
			if err := rs.Err(); err != nil {
				rs.Close()
				return result, err
			}
			rs.Close()
		}
		var activeOperations, activeRecordings int
		err = tx.QueryRowContext(ctx, `SELECT
   (SELECT COUNT(*) FROM mock_scenario_routes WHERE environment_key=?),
   (SELECT COUNT(*) FROM traffic_events WHERE environment_key=?),
   (SELECT COUNT(*) FROM operations WHERE environment_key=?),
   (SELECT COUNT(*) FROM timeline_events WHERE environment_key=?),
   (SELECT COUNT(*) FROM operations WHERE environment_key=? AND state IN ('queued','running','pending')),
   (SELECT COUNT(*) FROM recordings WHERE environment_key=? AND status='active')`, entry.key, entry.key, entry.key, entry.key, entry.key, entry.key).Scan(&item.MockRoutes, &item.RecordedEvents, &item.Operations, &item.TimelineEvents, &activeOperations, &activeRecordings)
		if err != nil {
			return result, err
		}
		_ = encoder.Encode(item)
		if activeOperations > 0 {
			result.Blocked = append(result.Blocked, entry.name+": operation is pending")
		}
		if activeRecordings > 0 {
			result.Blocked = append(result.Blocked, entry.name+": recording is active")
		}
		result.Environments = append(result.Environments, item)
	}
	result.Expected.StateDigest = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

func checkConfigurationTx(ctx context.Context, tx *sql.Tx, project, environment string, expected *model.ResourceVersion, eligible bool) error {
	if expected == nil && !eligible {
		return nil
	}
	actual, err := configurationPreviewTx(ctx, tx, project, environment)
	if err != nil {
		return err
	}
	if err := checkResourceVersion(expected, actual.Expected); err != nil {
		return err
	}
	if eligible && len(actual.Blocked) > 0 {
		return errors.New(actual.Blocked[0])
	}
	return nil
}
