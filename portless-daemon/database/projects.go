package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/portless-run/portless/portless-daemon/model"
	"github.com/portless-run/portless/portless-daemon/networking"
)

// CreateProject persists a new reusable project topology and source catalog.
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

// Project loads a project together with topology and environment summaries.
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

// ListProjects returns every project ordered by public name.
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

// ProjectModel returns the reusable logical topology stored for a project.
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

// UpdateProjectDefinition replaces topology and sources using optimistic concurrency.
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

// RenameProject renames a stopped project and reallocates its stable DNS endpoints.
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

// ForgetProject deletes a project only when all of its environments are stopped.
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
