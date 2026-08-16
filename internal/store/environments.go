package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/networking"
)

func (s *Store) CreateEnvironment(ctx context.Context, projectName, environmentName string, definition model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) (model.Environment, error) {
	if err := model.ValidateEnvironmentName(environmentName); err != nil {
		return model.Environment{}, err
	}
	var projectKey string
	if err := s.db.QueryRowContext(ctx, `SELECT private_key FROM projects WHERE name = ? COLLATE NOCASE`, projectName).Scan(&projectKey); err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	key, err := newPrivateKey()
	if err != nil {
		return model.Environment{}, err
	}
	modelJSON, err := encodeProjectModel(definition)
	if err != nil {
		return model.Environment{}, err
	}
	specs, err := networking.AllocationSpecs(projectName, environmentName, definition)
	if err != nil {
		return model.Environment{}, err
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Environment{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO environments(private_key, project_key, name, revision, status, reason, primary_service, model_json, created_at, updated_at)
VALUES(?, ?, ?, 1, ?, '', ?, ?, ?, ?)`, key, projectKey, environmentName, model.EnvironmentStopped, definition.PrimaryService, modelJSON, now, now)
	if err != nil {
		if isUniqueError(err) {
			return model.Environment{}, ErrAlreadyExists
		}
		return model.Environment{}, err
	}
	if err := replaceEnvironmentChildren(ctx, tx, key, definition, sources, bindings); err != nil {
		return model.Environment{}, err
	}
	if err := syncNetworkAllocationsTx(ctx, tx, key, specs); err != nil {
		return model.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, projectName, environmentName)
}

func (s *Store) CloneEnvironment(ctx context.Context, projectName, sourceName, targetName string) (model.Environment, error) {
	source, err := s.Environment(ctx, projectName, sourceName)
	if err != nil {
		return model.Environment{}, err
	}
	definition, err := s.EnvironmentModel(ctx, projectName, sourceName)
	if err != nil {
		return model.Environment{}, err
	}
	return s.CreateEnvironment(ctx, projectName, targetName, definition, source.Sources, source.Bindings)
}

func (s *Store) Environment(ctx context.Context, projectName, environmentName string) (model.Environment, error) {
	row, err := s.readEnvironmentRow(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	return s.hydrateEnvironment(ctx, row)
}

func (s *Store) EnvironmentBySelector(ctx context.Context, selector string) (model.Environment, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, project, environment)
}

func (s *Store) ListEnvironments(ctx context.Context, projectName string) ([]model.Environment, error) {
	query := `
SELECT e.private_key, e.project_key, p.name, e.name, e.revision, e.status, e.reason,
       e.primary_service, e.model_json, e.created_at, e.updated_at
FROM environments e JOIN projects p ON p.private_key = e.project_key`
	var args []any
	if projectName != "" {
		query += ` WHERE p.name = ? COLLATE NOCASE`
		args = append(args, projectName)
	}
	query += ` ORDER BY CASE WHEN e.status = 'stopped' THEN 1 ELSE 0 END, e.updated_at DESC, e.name COLLATE NOCASE`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var raw []environmentRow
	for rows.Next() {
		row, err := scanEnvironmentRow(rows)
		if err != nil {
			return nil, err
		}
		raw = append(raw, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]model.Environment, 0, len(raw))
	for _, row := range raw {
		environment, err := s.hydrateEnvironment(ctx, row)
		if err != nil {
			return nil, err
		}
		result = append(result, environment)
	}
	return result, nil
}

func (s *Store) EnvironmentsByPath(ctx context.Context, path string) ([]model.Environment, error) {
	path, err := canonicalPath(path)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT p.name, e.name, source.path
FROM environment_sources source
JOIN environments e ON e.private_key = source.environment_key
JOIN projects p ON p.private_key = e.project_key
ORDER BY p.name, e.name, source.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make(map[[2]string]struct{})
	for rows.Next() {
		var ref [2]string
		var sourcePath string
		if err := rows.Scan(&ref[0], &ref[1], &sourcePath); err != nil {
			return nil, err
		}
		if pathWithin(sourcePath, path) {
			refs[ref] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ordered := make([][2]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i][0] == ordered[j][0] {
			return ordered[i][1] < ordered[j][1]
		}
		return ordered[i][0] < ordered[j][0]
	})
	result := make([]model.Environment, 0, len(ordered))
	for _, ref := range ordered {
		environment, err := s.Environment(ctx, ref[0], ref[1])
		if err != nil {
			return nil, err
		}
		result = append(result, environment)
	}
	return result, nil
}

func (s *Store) EnvironmentModel(ctx context.Context, projectName, environmentName string) (model.ProjectModel, error) {
	var encoded []byte
	err := s.db.QueryRowContext(ctx, `
SELECT e.model_json FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName).Scan(&encoded)
	if err != nil {
		return model.ProjectModel{}, mapSQLError(err)
	}
	definition, err := decodeProjectModel(encoded)
	if err != nil {
		return model.ProjectModel{}, err
	}
	return definition, nil
}

func (s *Store) ReplaceEnvironmentConfiguration(ctx context.Context, projectName, environmentName string, expectedRevision int64, definition model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) (model.Environment, error) {
	modelJSON, err := encodeProjectModel(definition)
	if err != nil {
		return model.Environment{}, err
	}
	specs, err := networking.AllocationSpecs(projectName, environmentName, definition)
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
	var status string
	err = tx.QueryRowContext(ctx, `
SELECT e.private_key, e.revision, e.status FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName).Scan(&key, &revision, &status)
	if err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	if expectedRevision > 0 && revision != expectedRevision {
		return model.Environment{}, ErrConflict
	}
	if status != string(model.EnvironmentStopped) {
		return model.Environment{}, errors.New("environment must be stopped before its configuration changes")
	}
	_, err = tx.ExecContext(ctx, `
UPDATE environments SET model_json = ?, primary_service = ?, revision = revision + 1, updated_at = ?
WHERE private_key = ?`, modelJSON, definition.PrimaryService, nowText(), key)
	if err != nil {
		return model.Environment{}, err
	}
	for _, table := range []string{"connection_runtime", "service_runtime", "environment_sources", "environment_bindings"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE environment_key = ?", key); err != nil {
			return model.Environment{}, err
		}
	}
	if err := replaceEnvironmentChildren(ctx, tx, key, definition, sources, bindings); err != nil {
		return model.Environment{}, err
	}
	if err := syncNetworkAllocationsTx(ctx, tx, key, specs); err != nil {
		return model.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, projectName, environmentName)
}

func (s *Store) ReplaceProjectAndEnvironmentConfiguration(ctx context.Context, projectName string, expectedProjectRevision int64, projectDefinition model.ProjectModel, projectSources []model.ProjectSource, environmentName string, expectedEnvironmentRevision int64, environmentDefinition model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) (model.Environment, error) {
	projectDefinition.SuggestedName = projectName
	projectJSON, err := encodeProjectModel(logicalDefinition(projectDefinition))
	if err != nil {
		return model.Environment{}, err
	}
	projectSourcesJSON, err := json.Marshal(projectSources)
	if err != nil {
		return model.Environment{}, err
	}
	environmentJSON, err := encodeProjectModel(environmentDefinition)
	if err != nil {
		return model.Environment{}, err
	}
	specs, err := networking.AllocationSpecs(projectName, environmentName, environmentDefinition)
	if err != nil {
		return model.Environment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Environment{}, err
	}
	defer tx.Rollback()
	var projectKey string
	var projectRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT private_key, revision FROM projects WHERE name = ? COLLATE NOCASE`, projectName).Scan(&projectKey, &projectRevision); err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	if expectedProjectRevision > 0 && projectRevision != expectedProjectRevision {
		return model.Environment{}, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT name FROM environments WHERE project_key = ? AND status != ? ORDER BY name COLLATE NOCASE`, projectKey, string(model.EnvironmentStopped))
	if err != nil {
		return model.Environment{}, err
	}
	var active []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return model.Environment{}, err
		}
		active = append(active, model.EnvironmentSelector(projectName, name))
	}
	if err := rows.Close(); err != nil {
		return model.Environment{}, err
	}
	if len(active) > 0 {
		return model.Environment{}, ActiveProjectEnvironmentsError{Environments: active}
	}
	var environmentKey string
	var environmentRevision int64
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT private_key, revision, status FROM environments WHERE project_key = ? AND name = ? COLLATE NOCASE`, projectKey, environmentName).Scan(&environmentKey, &environmentRevision, &status); err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	if expectedEnvironmentRevision > 0 && environmentRevision != expectedEnvironmentRevision {
		return model.Environment{}, ErrConflict
	}
	if status != string(model.EnvironmentStopped) {
		return model.Environment{}, errors.New("environment must be stopped before its configuration changes")
	}
	now := nowText()
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET model_json = ?, sources_json = ?, primary_service = ?, revision = revision + 1, updated_at = ? WHERE private_key = ?`, projectJSON, projectSourcesJSON, projectDefinition.PrimaryService, now, projectKey); err != nil {
		return model.Environment{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE environments SET model_json = ?, primary_service = ?, revision = revision + 1, updated_at = ? WHERE private_key = ?`, environmentJSON, environmentDefinition.PrimaryService, now, environmentKey); err != nil {
		return model.Environment{}, err
	}
	for _, table := range []string{"connection_runtime", "service_runtime", "environment_sources", "environment_bindings"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE environment_key = ?", environmentKey); err != nil {
			return model.Environment{}, err
		}
	}
	if err := replaceEnvironmentChildren(ctx, tx, environmentKey, environmentDefinition, sources, bindings); err != nil {
		return model.Environment{}, err
	}
	if err := syncNetworkAllocationsTx(ctx, tx, environmentKey, specs); err != nil {
		return model.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, projectName, environmentName)
}

func (s *Store) SetEnvironmentBinding(ctx context.Context, projectName, environmentName string, binding model.ComponentBinding) (model.Environment, error) {
	environment, err := s.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	if environment.Status != model.EnvironmentStopped {
		return model.Environment{}, errors.New("environment must be stopped before a binding changes")
	}
	replaced := false
	for index := range environment.Bindings {
		if strings.EqualFold(environment.Bindings[index].Service, binding.Service) {
			environment.Bindings[index] = binding
			replaced = true
			break
		}
	}
	if !replaced {
		environment.Bindings = append(environment.Bindings, binding)
	}
	definition, err := s.EnvironmentModel(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	return s.ReplaceEnvironmentConfiguration(ctx, projectName, environmentName, environment.Revision, definition, environment.Sources, environment.Bindings)
}

func (s *Store) SetContextSelection(ctx context.Context, path, projectName, environmentName string) error {
	path, err := canonicalPath(path)
	if err != nil {
		return err
	}
	key, err := s.PrivateEnvironmentKey(ctx, projectName, environmentName)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM environment_sources WHERE environment_key = ?`, key)
	if err != nil {
		return err
	}
	defer rows.Close()
	selectionPath := ""
	for rows.Next() {
		var sourcePath string
		if err := rows.Scan(&sourcePath); err != nil {
			return err
		}
		if pathWithin(sourcePath, path) && len(sourcePath) > len(selectionPath) {
			selectionPath = sourcePath
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if selectionPath == "" {
		return errors.New("the selected environment does not use this source path")
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO context_selections(path, environment_key, selected_at) VALUES(?, ?, ?)
ON CONFLICT(path) DO UPDATE SET environment_key = excluded.environment_key, selected_at = excluded.selected_at`, selectionPath, key, nowText())
	return err
}

func (s *Store) ContextSelection(ctx context.Context, path string) (model.Environment, error) {
	path, err := canonicalPath(path)
	if err != nil {
		return model.Environment{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.path, p.name, e.name FROM context_selections c
JOIN environments e ON e.private_key = c.environment_key
JOIN projects p ON p.private_key = e.project_key
JOIN environment_sources source ON source.environment_key = c.environment_key AND source.path = c.path
	ORDER BY length(c.path) DESC`)
	if err != nil {
		return model.Environment{}, err
	}
	defer rows.Close()
	var projectName, environmentName string
	for rows.Next() {
		var selectionPath, candidateProject, candidateEnvironment string
		if err := rows.Scan(&selectionPath, &candidateProject, &candidateEnvironment); err != nil {
			return model.Environment{}, err
		}
		if pathWithin(selectionPath, path) {
			projectName, environmentName = candidateProject, candidateEnvironment
			break
		}
	}
	if err := rows.Err(); err != nil {
		return model.Environment{}, err
	}
	if projectName == "" {
		return model.Environment{}, ErrNotFound
	}
	return s.Environment(ctx, projectName, environmentName)
}

func (s *Store) ClearContextSelection(ctx context.Context, path string) (bool, error) {
	path, err := canonicalPath(path)
	if err != nil {
		return false, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM context_selections ORDER BY length(path) DESC`)
	if err != nil {
		return false, err
	}
	selectionPath := ""
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			rows.Close()
			return false, err
		}
		if pathWithin(candidate, path) {
			selectionPath = candidate
			break
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if selectionPath == "" {
		return false, nil
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM context_selections WHERE path = ?`, selectionPath)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return changed > 0, nil
}

func (s *Store) ForgetEnvironment(ctx context.Context, projectName, environmentName string) error {
	environment, err := s.Environment(ctx, projectName, environmentName)
	if err != nil {
		return err
	}
	if environment.Status != model.EnvironmentStopped {
		return errors.New("environment must be stopped before it can be forgotten")
	}
	key, err := s.PrivateEnvironmentKey(ctx, projectName, environmentName)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM environments WHERE private_key = ?`, key)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetEnvironmentStatus(ctx context.Context, projectName, environmentName string, status model.EnvironmentStatus, reason string) error {
	key, err := s.PrivateEnvironmentKey(ctx, projectName, environmentName)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE environments SET status = ?, reason = ?, updated_at = ? WHERE private_key = ?`, status, reason, nowText(), key)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}
