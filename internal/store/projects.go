package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
	"github.com/portless-run/portless/internal/networking"
)

type environmentRow struct {
	key            string
	projectKey     string
	projectName    string
	environment    string
	revision       int64
	status         string
	reason         string
	primaryService string
	modelJSON      []byte
	createdAt      string
	updatedAt      string
}

func (s *Store) CreateProject(ctx context.Context, name string, definition model.ProjectModel, sources []model.ProjectSource) (model.Project, error) {
	if err := model.ValidateProjectName(name); err != nil {
		return model.Project{}, err
	}
	key, err := newPrivateKey()
	if err != nil {
		return model.Project{}, err
	}
	definition.SuggestedName = name
	modelJSON, err := encodeProjectModel(logicalDefinition(definition))
	if err != nil {
		return model.Project{}, err
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return model.Project{}, err
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO projects(private_key, name, revision, primary_service, model_json, sources_json, created_at, updated_at)
VALUES(?, ?, 1, ?, ?, ?, ?, ?)`, key, name, definition.PrimaryService, modelJSON, sourcesJSON, now, now)
	if err != nil {
		if isUniqueError(err) {
			return model.Project{}, ErrNameTaken
		}
		return model.Project{}, err
	}
	return s.Project(ctx, name)
}

func (s *Store) Project(ctx context.Context, name string) (model.Project, error) {
	var result model.Project
	var modelJSON, sourcesJSON []byte
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
SELECT name, revision, primary_service, model_json, sources_json, created_at, updated_at
FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(
		&result.Name, &result.Revision, &result.PrimaryService, &modelJSON, &sourcesJSON, &createdAt, &updatedAt,
	)
	if err != nil {
		return model.Project{}, mapSQLError(err)
	}
	definition, err := decodeProjectModel(modelJSON)
	if err != nil {
		return model.Project{}, fmt.Errorf("decode project %s: %w", name, err)
	}
	if err := json.Unmarshal(sourcesJSON, &result.Sources); err != nil {
		return model.Project{}, fmt.Errorf("decode project sources %s: %w", name, err)
	}
	result.Services = definition.Services
	result.Connections = definition.Connections
	result.CreatedAt = parseTime(createdAt)
	result.UpdatedAt = parseTime(updatedAt)

	environments, err := s.ListEnvironments(ctx, name)
	if err != nil {
		return model.Project{}, err
	}
	for _, environment := range environments {
		ready, remote := 0, 0
		for _, service := range environment.Services {
			if service.Status == model.ServiceReady {
				ready++
			}
			if bindingFor(environment.Bindings, service.Name).Provider == model.ProviderRemote {
				remote++
			}
		}
		result.Environments = append(result.Environments, model.EnvironmentSummary{
			Project: result.Name, Name: environment.Name, Revision: environment.Revision,
			Status: environment.Status, Reason: environment.Reason, ServiceCount: len(environment.Services),
			ReadyCount: ready, RemoteCount: remote, UpdatedAt: environment.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM projects ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	projects := make([]model.Project, 0, len(names))
	for _, name := range names {
		project, err := s.Project(ctx, name)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, nil
}

func (s *Store) ProjectModel(ctx context.Context, name string) (model.ProjectModel, error) {
	var encoded []byte
	if err := s.db.QueryRowContext(ctx, `SELECT model_json FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(&encoded); err != nil {
		return model.ProjectModel{}, mapSQLError(err)
	}
	definition, err := decodeProjectModel(encoded)
	if err != nil {
		return model.ProjectModel{}, err
	}
	return definition, nil
}

func (s *Store) UpdateProjectDefinition(ctx context.Context, name string, expectedRevision int64, definition model.ProjectModel, sources []model.ProjectSource) (model.Project, error) {
	definition.SuggestedName = name
	modelJSON, err := encodeProjectModel(logicalDefinition(definition))
	if err != nil {
		return model.Project{}, err
	}
	sourcesJSON, err := json.Marshal(sources)
	if err != nil {
		return model.Project{}, err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE projects SET model_json = ?, sources_json = ?, primary_service = ?, revision = revision + 1, updated_at = ?
WHERE name = ? COLLATE NOCASE AND (? = 0 OR revision = ?)`, modelJSON, sourcesJSON, definition.PrimaryService, nowText(), name, expectedRevision, expectedRevision)
	if err != nil {
		return model.Project{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		if _, err := s.Project(ctx, name); err != nil {
			return model.Project{}, err
		}
		return model.Project{}, ErrConflict
	}
	return s.Project(ctx, name)
}

func (s *Store) RenameProject(ctx context.Context, oldName, newName string, expectedRevision int64) (model.Project, error) {
	if err := model.ValidateProjectName(newName); err != nil {
		return model.Project{}, err
	}
	project, err := s.Project(ctx, oldName)
	if err != nil {
		return model.Project{}, err
	}
	if expectedRevision > 0 && project.Revision != expectedRevision {
		return model.Project{}, ErrConflict
	}
	for _, environment := range project.Environments {
		if environment.Status != model.EnvironmentStopped {
			return model.Project{}, errors.New("all environments must be stopped before the project is renamed")
		}
	}
	type allocationPlan struct {
		environment string
		specs       []networking.AllocationSpec
	}
	plans := make([]allocationPlan, 0, len(project.Environments))
	for _, environment := range project.Environments {
		definition, definitionErr := s.EnvironmentModel(ctx, oldName, environment.Name)
		if definitionErr != nil {
			return model.Project{}, definitionErr
		}
		specs, specErr := networking.AllocationSpecs(newName, environment.Name, definition)
		if specErr != nil {
			return model.Project{}, specErr
		}
		plans = append(plans, allocationPlan{environment: environment.Name, specs: specs})
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Project{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE projects SET name = ?, revision = revision + 1, updated_at = ?
WHERE name = ? COLLATE NOCASE AND (? = 0 OR revision = ?)`, newName, nowText(), oldName, expectedRevision, expectedRevision)
	if err != nil {
		if isUniqueError(err) {
			return model.Project{}, ErrNameTaken
		}
		return model.Project{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return model.Project{}, ErrConflict
	}
	for _, plan := range plans {
		var environmentKey string
		if queryErr := tx.QueryRowContext(ctx, `
SELECT e.private_key FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, newName, plan.environment).Scan(&environmentKey); queryErr != nil {
			return model.Project{}, mapSQLError(queryErr)
		}
		if syncErr := syncNetworkAllocationsTx(ctx, tx, environmentKey, plan.specs); syncErr != nil {
			return model.Project{}, syncErr
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Project{}, err
	}
	return s.Project(ctx, newName)
}

func (s *Store) ForgetProject(ctx context.Context, name string) error {
	project, err := s.Project(ctx, name)
	if err != nil {
		return err
	}
	for _, environment := range project.Environments {
		if environment.Status != model.EnvironmentStopped {
			return errors.New("all environments must be stopped before the project is forgotten")
		}
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE name = ? COLLATE NOCASE`, name)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

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
SELECT DISTINCT p.name, e.name
FROM environment_sources source
JOIN environments e ON e.private_key = source.environment_key
JOIN projects p ON p.private_key = e.project_key
WHERE source.path = ? ORDER BY p.name, e.name`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs [][2]string
	for rows.Next() {
		var ref [2]string
		if err := rows.Scan(&ref[0], &ref[1]); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]model.Environment, 0, len(refs))
	for _, ref := range refs {
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
	var belongs int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM environment_sources WHERE environment_key = ? AND path = ?`, key, path).Scan(&belongs); err != nil {
		return err
	}
	if belongs == 0 {
		return errors.New("the selected environment does not use this source path")
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO context_selections(path, environment_key, selected_at) VALUES(?, ?, ?)
ON CONFLICT(path) DO UPDATE SET environment_key = excluded.environment_key, selected_at = excluded.selected_at`, path, key, nowText())
	return err
}

func (s *Store) ContextSelection(ctx context.Context, path string) (model.Environment, error) {
	path, err := canonicalPath(path)
	if err != nil {
		return model.Environment{}, err
	}
	var projectName, environmentName string
	err = s.db.QueryRowContext(ctx, `
SELECT p.name, e.name FROM context_selections c
JOIN environments e ON e.private_key = c.environment_key
JOIN projects p ON p.private_key = e.project_key
JOIN environment_sources source ON source.environment_key = c.environment_key AND source.path = c.path
WHERE c.path = ?`, path).Scan(&projectName, &environmentName)
	if err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	return s.Environment(ctx, projectName, environmentName)
}

func (s *Store) ClearContextSelection(ctx context.Context, path string) (bool, error) {
	path, err := canonicalPath(path)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM context_selections WHERE path = ?`, path)
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

type ServiceRuntimeUpdate struct {
	Status           model.ServiceStatus
	Reason           string
	Generation       int64
	PID              int
	UpstreamPort     int
	StartedAt        *time.Time
	RestartCount     int64
	LogPath          string
	PrivateRunKey    string
	OwnerInstanceID  string
	SupervisorSocket string
	SupervisorState  string
	SupervisorPID    int
	ContainerName    string
	ObservedAt       *time.Time
}

type ServiceRuntimeRecord struct {
	ServiceName      string
	Status           model.ServiceStatus
	Reason           string
	Generation       int64
	PID              int
	UpstreamPort     int
	StartedAt        *time.Time
	RestartCount     int64
	LogPath          string
	PrivateRunKey    string
	OwnerInstanceID  string
	SupervisorSocket string
	SupervisorState  string
	SupervisorPID    int
	ContainerName    string
	ObservedAt       *time.Time
}

func (s *Store) SetServiceRuntime(ctx context.Context, selector, serviceName string, update ServiceRuntimeUpdate) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	var started any
	if update.StartedAt != nil {
		started = update.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	var observed any
	if update.ObservedAt != nil {
		observed = update.ObservedAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET status = ?, reason = ?, generation = ?, pid = ?, upstream_port = ?,
  started_at = ?, restart_count = ?, log_path = ?, private_run_key = ?, owner_instance_id = ?,
  supervisor_socket = ?, supervisor_state = ?, supervisor_pid = ?, container_name = ?, observed_at = ?
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, update.Status, update.Reason, update.Generation,
		update.PID, update.UpstreamPort, started, update.RestartCount, update.LogPath, update.PrivateRunKey,
		update.OwnerInstanceID, update.SupervisorSocket, update.SupervisorState, update.SupervisorPID, update.ContainerName, observed, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ServiceRuntime(ctx context.Context, selector, serviceName string) (ServiceRuntimeRecord, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return ServiceRuntimeRecord{}, err
	}
	var result ServiceRuntimeRecord
	var status string
	var started, observed sql.NullString
	err = s.db.QueryRowContext(ctx, `
SELECT service_name, status, reason, generation, pid, upstream_port, started_at, restart_count,
       log_path, private_run_key, owner_instance_id, supervisor_socket, supervisor_state, supervisor_pid, container_name, observed_at
FROM service_runtime WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, key, serviceName).Scan(
		&result.ServiceName, &status, &result.Reason, &result.Generation, &result.PID, &result.UpstreamPort,
		&started, &result.RestartCount, &result.LogPath, &result.PrivateRunKey, &result.OwnerInstanceID,
		&result.SupervisorSocket, &result.SupervisorState, &result.SupervisorPID, &result.ContainerName, &observed,
	)
	if err != nil {
		return ServiceRuntimeRecord{}, mapSQLError(err)
	}
	result.Status = model.ServiceStatus(status)
	result.StartedAt = parseOptionalTime(started)
	result.ObservedAt = parseOptionalTime(observed)
	return result, nil
}

func (s *Store) SetServiceStatus(ctx context.Context, selector, serviceName string, status model.ServiceStatus, reason string) error {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET status = ?, reason = ?
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, status, reason, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) PrivateEnvironmentKey(ctx context.Context, projectName, environmentName string) (string, error) {
	var key string
	err := s.db.QueryRowContext(ctx, `
SELECT e.private_key FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName).Scan(&key)
	if err != nil {
		return "", mapSQLError(err)
	}
	return key, nil
}

func (s *Store) PrivateEnvironmentKeyForSelector(ctx context.Context, selector string) (string, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return "", err
	}
	return s.PrivateEnvironmentKey(ctx, project, environment)
}

func (s *Store) ServiceLogPath(ctx context.Context, selector, serviceName string) (string, error) {
	key, err := s.PrivateEnvironmentKeyForSelector(ctx, selector)
	if err != nil {
		return "", err
	}
	var path string
	if err := s.db.QueryRowContext(ctx, `SELECT log_path FROM service_runtime WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, key, serviceName).Scan(&path); err != nil {
		return "", mapSQLError(err)
	}
	return path, nil
}

func (s *Store) readEnvironmentRow(ctx context.Context, projectName, environmentName string) (environmentRow, error) {
	row, err := scanEnvironmentRow(s.db.QueryRowContext(ctx, `
SELECT e.private_key, e.project_key, p.name, e.name, e.revision, e.status, e.reason,
       e.primary_service, e.model_json, e.created_at, e.updated_at
FROM environments e JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName))
	return row, mapSQLError(err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEnvironmentRow(scanner rowScanner) (environmentRow, error) {
	var row environmentRow
	err := scanner.Scan(&row.key, &row.projectKey, &row.projectName, &row.environment,
		&row.revision, &row.status, &row.reason, &row.primaryService, &row.modelJSON, &row.createdAt, &row.updatedAt)
	return row, err
}

func (s *Store) hydrateEnvironment(ctx context.Context, row environmentRow) (model.Environment, error) {
	selector := model.EnvironmentSelector(row.projectName, row.environment)
	definition, err := decodeProjectModel(row.modelJSON)
	if err != nil {
		return model.Environment{}, fmt.Errorf("decode environment %s: %w", selector, err)
	}
	runtimeRows, err := s.db.QueryContext(ctx, `
SELECT service_name, status, reason, generation, pid, upstream_port, started_at, restart_count
FROM service_runtime WHERE environment_key = ?`, row.key)
	if err != nil {
		return model.Environment{}, err
	}
	runtime := map[string]model.Service{}
	for runtimeRows.Next() {
		var service model.Service
		var status string
		var started sql.NullString
		if err := runtimeRows.Scan(&service.Name, &status, &service.Reason, &service.Generation,
			&service.PID, &service.UpstreamPort, &started, &service.RestartCount); err != nil {
			runtimeRows.Close()
			return model.Environment{}, err
		}
		service.Status = model.ServiceStatus(status)
		service.StartedAt = parseOptionalTime(started)
		runtime[strings.ToLower(service.Name)] = service
	}
	if err := runtimeRows.Close(); err != nil {
		return model.Environment{}, err
	}
	services := make([]model.Service, 0, len(definition.Services))
	for _, serviceDefinition := range definition.Services {
		service := runtime[strings.ToLower(serviceDefinition.Name)]
		service.ServiceDefinition = serviceDefinition
		if service.Status == "" {
			service.Status = model.ServicePlanned
		}
		services = append(services, service)
	}
	sort.SliceStable(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	sources, err := s.environmentSources(ctx, row.key)
	if err != nil {
		return model.Environment{}, err
	}
	bindings, err := s.environmentBindings(ctx, row.key)
	if err != nil {
		return model.Environment{}, err
	}
	return model.Environment{
		Project: row.projectName, Name: row.environment, Revision: row.revision,
		Status: model.EnvironmentStatus(row.status), Reason: row.reason, PrimaryService: row.primaryService,
		CreatedAt: parseTime(row.createdAt), UpdatedAt: parseTime(row.updatedAt), Sources: sources,
		Bindings: bindings, Services: services, Connections: definition.Connections,
	}, nil
}

func (s *Store) environmentSources(ctx context.Context, environmentKey string) ([]model.SourceBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT source_name, path, status, warnings_json, discovery_json, scanned_at
FROM environment_sources WHERE environment_key = ? ORDER BY source_name COLLATE NOCASE`, environmentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.SourceBinding
	for rows.Next() {
		var source model.SourceBinding
		var warningsJSON, discoveryJSON []byte
		var scannedAt string
		if err := rows.Scan(&source.Name, &source.Path, &source.Status, &warningsJSON, &discoveryJSON, &scannedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(warningsJSON, &source.Warnings)
		definition, err := decodeProjectModel(discoveryJSON)
		if err != nil {
			return nil, err
		}
		source.Definition = definition
		source.ScannedAt = parseTime(scannedAt)
		result = append(result, source)
	}
	return result, rows.Err()
}

func (s *Store) environmentBindings(ctx context.Context, environmentKey string) ([]model.ComponentBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT service_name, provider, source_name, config_json
FROM environment_bindings WHERE environment_key = ? ORDER BY service_name COLLATE NOCASE`, environmentKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.ComponentBinding
	for rows.Next() {
		var binding model.ComponentBinding
		var provider string
		var config []byte
		if err := rows.Scan(&binding.Service, &provider, &binding.Source, &config); err != nil {
			return nil, err
		}
		binding.Provider = model.ProviderKind(provider)
		if binding.Provider == model.ProviderRemote {
			var remote model.RemoteTarget
			if err := json.Unmarshal(config, &remote); err != nil {
				return nil, err
			}
			binding.Remote = &remote
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func replaceEnvironmentChildren(ctx context.Context, executor sqlExecutor, environmentKey string, definition model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) error {
	for _, service := range definition.Services {
		if _, err := executor.ExecContext(ctx, `INSERT INTO service_runtime(environment_key, service_name, status) VALUES(?, ?, ?)`, environmentKey, service.Name, model.ServicePlanned); err != nil {
			return err
		}
	}
	for _, source := range sources {
		if err := insertSource(ctx, executor, environmentKey, source); err != nil {
			return err
		}
	}
	for _, binding := range bindings {
		if err := insertBinding(ctx, executor, environmentKey, binding); err != nil {
			return err
		}
	}
	return nil
}

func insertSource(ctx context.Context, executor sqlExecutor, environmentKey string, source model.SourceBinding) error {
	if err := model.ValidateSourceName(source.Name); err != nil {
		return err
	}
	path, err := canonicalPath(source.Path)
	if err != nil {
		return err
	}
	warningsJSON, _ := json.Marshal(source.Warnings)
	discoveryJSON, err := encodeProjectModel(source.Definition)
	if err != nil {
		return err
	}
	scanned := source.ScannedAt
	if scanned.IsZero() {
		scanned = time.Now().UTC()
	}
	status := source.Status
	if status == "" {
		status = "ready"
	}
	_, err = executor.ExecContext(ctx, `
INSERT INTO environment_sources(environment_key, source_name, path, status, warnings_json, discovery_json, scanned_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`, environmentKey, source.Name, path, status, warningsJSON, discoveryJSON, scanned.Format(time.RFC3339Nano))
	return err
}

func insertBinding(ctx context.Context, executor sqlExecutor, environmentKey string, binding model.ComponentBinding) error {
	config := []byte("{}")
	var err error
	if binding.Remote != nil {
		config, err = json.Marshal(binding.Remote)
		if err != nil {
			return err
		}
	}
	_, err = executor.ExecContext(ctx, `
INSERT INTO environment_bindings(environment_key, service_name, provider, source_name, config_json)
VALUES(?, ?, ?, ?, ?)`, environmentKey, binding.Service, binding.Provider, binding.Source, config)
	return err
}

func logicalDefinition(input model.ProjectModel) model.ProjectModel {
	result := input
	result.Services = append([]model.ServiceDefinition{}, input.Services...)
	for index := range result.Services {
		result.Services[index].WorkingDirectory = ""
	}
	return result
}

const projectModelFormatVersion = 1

type persistedProjectModel struct {
	FormatVersion int                `json:"formatVersion"`
	Definition    model.ProjectModel `json:"definition"`
}

func encodeProjectModel(definition model.ProjectModel) ([]byte, error) {
	return json.Marshal(persistedProjectModel{FormatVersion: projectModelFormatVersion, Definition: definition})
}

func decodeProjectModel(encoded []byte) (model.ProjectModel, error) {
	var persisted persistedProjectModel
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		return model.ProjectModel{}, err
	}
	if persisted.FormatVersion != projectModelFormatVersion {
		return model.ProjectModel{}, fmt.Errorf("%w: found format %d, expected %d; reset application state and rediscover sources", ErrIncompatibleState, persisted.FormatVersion, projectModelFormatVersion)
	}
	return persisted.Definition, nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	return filepath.Clean(absolute), nil
}

func bindingFor(bindings []model.ComponentBinding, service string) model.ComponentBinding {
	for _, binding := range bindings {
		if strings.EqualFold(binding.Service, service) {
			return binding
		}
	}
	return model.ComponentBinding{Service: service}
}

func scopeFromFields(project, environment string) string {
	return model.EnvironmentSelector(project, environment)
}

func publicScope(selector string) (string, string) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return selector, ""
	}
	return project, environment
}

func isUniqueError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}
