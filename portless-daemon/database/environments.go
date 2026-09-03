package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/runportless/portless/portless-daemon/model"
	"github.com/runportless/portless/portless-daemon/networking"
)

// ProjectEnvironmentConfiguration is one stopped environment configuration
// participating in an atomic project topology replacement.
type ProjectEnvironmentConfiguration struct {
	Name       string
	Revision   int64
	Definition model.ProjectModel
	Sources    []model.SourceBinding
	Bindings   []model.ComponentBinding
}

// CreateEnvironment persists a new stopped environment and allocates its stable endpoints.
func (s *Store) CreateEnvironment(ctx context.Context, projectName, environmentName string, definition model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) (model.Environment, error) {
	return s.createEnvironment(ctx, projectName, environmentName, "", definition, sources, bindings)
}

func (s *Store) createEnvironment(ctx context.Context, projectName, environmentName, clonedFrom string, definition model.ProjectModel, sources []model.SourceBinding, bindings []model.ComponentBinding) (model.Environment, error) {
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
	createdAt := parseTime(now)
	initialBindings := append([]model.ComponentBinding{}, bindings...)
	for index := range initialBindings {
		initialBindings[index].ModifiedAt = createdAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Environment{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO environments(private_key, project_key, name, revision, status, reason, primary_service, model_json, cloned_from_name, created_at, updated_at)
VALUES(?, ?, ?, 1, ?, '', ?, ?, ?, ?, ?)`, key, projectKey, environmentName, model.EnvironmentStopped, definition.PrimaryService, modelJSON, clonedFrom, now, now)
	if err != nil {
		if isUniqueError(err) {
			return model.Environment{}, ErrAlreadyExists
		}
		return model.Environment{}, err
	}
	if err := replaceEnvironmentChildren(ctx, tx, key, definition, sources, initialBindings); err != nil {
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

// CloneEnvironment copies topology, source bindings, provider bindings, and mock scenarios into a new environment.
func (s *Store) CloneEnvironment(ctx context.Context, projectName, sourceName, targetName string) (model.Environment, error) {
	source, err := s.Environment(ctx, projectName, sourceName)
	if err != nil {
		return model.Environment{}, err
	}
	definition, err := s.EnvironmentModel(ctx, projectName, sourceName)
	if err != nil {
		return model.Environment{}, err
	}
	created, err := s.createEnvironment(ctx, projectName, targetName, source.Name, definition, source.Sources, source.Bindings)
	if err != nil {
		return model.Environment{}, err
	}
	if err := s.cloneMockScenarios(ctx, projectName, sourceName, targetName); err != nil {
		_ = s.ForgetEnvironment(context.Background(), projectName, targetName)
		return model.Environment{}, err
	}
	return s.Environment(ctx, created.Project, created.Name)
}

// Environment loads and hydrates one environment by public project and environment names.
func (s *Store) Environment(ctx context.Context, projectName, environmentName string) (model.Environment, error) {
	row, err := s.readEnvironmentRow(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	return s.hydrateEnvironment(ctx, row)
}

// EnvironmentBySelector loads an environment addressed as project/environment.
func (s *Store) EnvironmentBySelector(ctx context.Context, selector string) (model.Environment, error) {
	project, environment, err := model.ParseEnvironmentSelector(selector)
	if err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, project, environment)
}

// ListEnvironments lists hydrated environments, optionally filtered by project.
func (s *Store) ListEnvironments(ctx context.Context, projectName string) ([]model.Environment, error) {
	query := `
SELECT e.private_key, e.project_key, p.name, e.name, e.revision, e.status, e.reason,
       e.primary_service, e.model_json, e.cloned_from_name, e.created_at, e.updated_at
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

// EnvironmentsByPath returns environments whose registered sources contain path.
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

// EnvironmentModel returns the compiled topology persisted for one environment.
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

// ReplaceEnvironmentConfiguration atomically replaces stopped-environment configuration at a revision.
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
	if _, err := tx.ExecContext(ctx, `
DELETE FROM context_selections
WHERE environment_key = ?
  AND path NOT IN (
    SELECT source.path FROM environment_sources source
    JOIN environments e ON e.private_key = source.environment_key
    WHERE e.project_key = (SELECT project_key FROM environments WHERE private_key = ?)
  )`, key, key); err != nil {
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

// ApplyActiveBindingConfiguration updates one provider binding and the compiled
// model without deleting runtime ownership, proxy, or source records for the
// rest of an active environment.
func (s *Store) ApplyActiveBindingConfiguration(ctx context.Context, projectName, environmentName string, expectedRevision int64, definition model.ProjectModel, binding model.ComponentBinding) (model.Environment, error) {
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
	if err := tx.QueryRowContext(ctx, `
SELECT e.private_key, e.revision, e.status FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName).Scan(&key, &revision, &status); err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	if expectedRevision > 0 && revision != expectedRevision {
		return model.Environment{}, ErrConflict
	}
	if status == string(model.EnvironmentStopped) {
		return model.Environment{}, errors.New("active binding configuration requires a running environment")
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE environments SET model_json = ?, primary_service = ?, revision = revision + 1, updated_at = ?
WHERE private_key = ?`, modelJSON, definition.PrimaryService, nowText(), key); err != nil {
		return model.Environment{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM environment_bindings
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, key, binding.Service); err != nil {
		return model.Environment{}, err
	}
	if err := insertBinding(ctx, tx, key, binding); err != nil {
		return model.Environment{}, err
	}
	for _, service := range definition.Services {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO service_runtime(environment_key, service_name, status)
VALUES(?, ?, ?)
ON CONFLICT(environment_key, service_name) DO NOTHING`, key, service.Name, model.ServicePlanned); err != nil {
			return model.Environment{}, err
		}
	}
	if err := syncNetworkAllocationsTx(ctx, tx, key, specs); err != nil {
		return model.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, projectName, environmentName)
}

// ApplyMockScenarioConfiguration atomically replaces provider bindings and the
// private restoration records for one scenario without disturbing runtime
// ownership for unrelated services.
func (s *Store) ApplyMockScenarioConfiguration(ctx context.Context, projectName, environmentName string, expectedRevision int64, definition model.ProjectModel, bindings []model.ComponentBinding, scenario string, previous []model.ComponentBinding, enabled bool) (model.Environment, error) {
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
	if err := tx.QueryRowContext(ctx, `
SELECT e.private_key, e.revision FROM environments e
JOIN projects p ON p.private_key = e.project_key
WHERE p.name = ? COLLATE NOCASE AND e.name = ? COLLATE NOCASE`, projectName, environmentName).Scan(&key, &revision); err != nil {
		return model.Environment{}, mapSQLError(err)
	}
	if expectedRevision > 0 && revision != expectedRevision {
		return model.Environment{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE environments SET model_json = ?, primary_service = ?, revision = revision + 1, updated_at = ?
WHERE private_key = ?`, modelJSON, definition.PrimaryService, nowText(), key); err != nil {
		return model.Environment{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM environment_bindings WHERE environment_key = ?`, key); err != nil {
		return model.Environment{}, err
	}
	for _, binding := range bindings {
		if err := insertBinding(ctx, tx, key, binding); err != nil {
			return model.Environment{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mock_scenario_activations WHERE environment_key = ? AND scenario_name = ? COLLATE NOCASE`, key, scenario); err != nil {
		return model.Environment{}, err
	}
	if enabled {
		activatedAt := nowText()
		for _, binding := range previous {
			bindingJSON, err := json.Marshal(binding)
			if err != nil {
				return model.Environment{}, err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO mock_scenario_activations(environment_key, scenario_name, service_name, previous_binding_json, activated_at)
VALUES(?, ?, ?, ?, ?)`, key, scenario, binding.Service, bindingJSON, activatedAt); err != nil {
				return model.Environment{}, err
			}
		}
	}
	for _, service := range definition.Services {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO service_runtime(environment_key, service_name, status)
VALUES(?, ?, ?)
ON CONFLICT(environment_key, service_name) DO NOTHING`, key, service.Name, model.ServicePlanned); err != nil {
			return model.Environment{}, err
		}
	}
	if err := syncNetworkAllocationsTx(ctx, tx, key, specs); err != nil {
		return model.Environment{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Environment{}, err
	}
	return s.Environment(ctx, projectName, environmentName)
}

// ReplaceProjectAndEnvironmentConfiguration atomically extends project topology and one stopped environment.
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

// ReplaceProjectConfiguration atomically replaces one project's logical
// topology and every stopped environment derived from it. Removed service
// names are used to discard service-scoped mocks and disable obsolete faults.
func (s *Store) ReplaceProjectConfiguration(ctx context.Context, projectName string, expectedProjectRevision int64, projectDefinition model.ProjectModel, projectSources []model.ProjectSource, environments []ProjectEnvironmentConfiguration, removedServices []string) ([]model.Environment, error) {
	projectDefinition.SuggestedName = projectName
	projectJSON, err := encodeProjectModel(logicalDefinition(projectDefinition))
	if err != nil {
		return nil, err
	}
	projectSourcesJSON, err := json.Marshal(projectSources)
	if err != nil {
		return nil, err
	}
	type preparedEnvironment struct {
		configuration ProjectEnvironmentConfiguration
		modelJSON     []byte
		allocations   []networking.AllocationSpec
	}
	prepared := make(map[string]preparedEnvironment, len(environments))
	for _, environment := range environments {
		if err := model.ValidateEnvironmentName(environment.Name); err != nil {
			return nil, err
		}
		key := strings.ToLower(environment.Name)
		if _, duplicate := prepared[key]; duplicate {
			return nil, fmt.Errorf("environment %s is configured more than once", environment.Name)
		}
		encoded, err := encodeProjectModel(environment.Definition)
		if err != nil {
			return nil, err
		}
		allocations, err := networking.AllocationSpecs(projectName, environment.Name, environment.Definition)
		if err != nil {
			return nil, err
		}
		prepared[key] = preparedEnvironment{configuration: environment, modelJSON: encoded, allocations: allocations}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var projectKey string
	var projectRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT private_key, revision FROM projects WHERE name = ? COLLATE NOCASE`, projectName).Scan(&projectKey, &projectRevision); err != nil {
		return nil, mapSQLError(err)
	}
	if expectedProjectRevision > 0 && projectRevision != expectedProjectRevision {
		return nil, ErrConflict
	}
	type storedEnvironment struct {
		key      string
		name     string
		revision int64
		status   string
	}
	rows, err := tx.QueryContext(ctx, `SELECT private_key, name, revision, status FROM environments WHERE project_key = ? ORDER BY name COLLATE NOCASE`, projectKey)
	if err != nil {
		return nil, err
	}
	stored := map[string]storedEnvironment{}
	var active []string
	for rows.Next() {
		var environment storedEnvironment
		if err := rows.Scan(&environment.key, &environment.name, &environment.revision, &environment.status); err != nil {
			rows.Close()
			return nil, err
		}
		stored[strings.ToLower(environment.name)] = environment
		if environment.status != string(model.EnvironmentStopped) {
			active = append(active, model.EnvironmentSelector(projectName, environment.name))
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(active) > 0 {
		return nil, ActiveProjectEnvironmentsError{Environments: active}
	}
	if len(prepared) != len(stored) {
		return nil, errors.New("project topology replacement must include every environment")
	}
	for key, environment := range stored {
		input, ok := prepared[key]
		if !ok {
			return nil, fmt.Errorf("project topology replacement is missing environment %s", environment.name)
		}
		if input.configuration.Revision > 0 && input.configuration.Revision != environment.revision {
			return nil, ErrConflict
		}
	}

	now := nowText()
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET model_json = ?, sources_json = ?, primary_service = ?, revision = revision + 1, updated_at = ? WHERE private_key = ?`, projectJSON, projectSourcesJSON, projectDefinition.PrimaryService, now, projectKey); err != nil {
		return nil, err
	}
	for key, environment := range stored {
		input := prepared[key]
		if _, err := tx.ExecContext(ctx, `UPDATE environments SET model_json = ?, primary_service = ?, revision = revision + 1, updated_at = ? WHERE private_key = ?`, input.modelJSON, input.configuration.Definition.PrimaryService, now, environment.key); err != nil {
			return nil, err
		}
		for _, table := range []string{"connection_runtime", "service_runtime", "environment_sources", "environment_bindings"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE environment_key = ?", environment.key); err != nil {
				return nil, err
			}
		}
		if err := replaceEnvironmentChildren(ctx, tx, environment.key, input.configuration.Definition, input.configuration.Sources, input.configuration.Bindings); err != nil {
			return nil, err
		}
		if err := syncNetworkAllocationsTx(ctx, tx, environment.key, input.allocations); err != nil {
			return nil, err
		}
		for _, service := range removedServices {
			var scenario, route string
			err := tx.QueryRowContext(ctx, `
SELECT scenario_name, name FROM mock_scenario_routes
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE
ORDER BY scenario_name COLLATE NOCASE, name COLLATE NOCASE LIMIT 1`, environment.key, service).Scan(&scenario, &route)
			if err == nil {
				return nil, fmt.Errorf("service %s is referenced by mock scenario %s route %s; move or delete those routes before removing the service", service, scenario, route)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE fault_rules SET enabled = 0, revision = revision + 1 WHERE environment_key = ? AND enabled = 1 AND (source = ? COLLATE NOCASE OR target = ? COLLATE NOCASE)`, environment.key, service, service); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	result := make([]model.Environment, 0, len(environments))
	for _, environment := range environments {
		updated, err := s.Environment(ctx, projectName, environment.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, updated)
	}
	return result, nil
}

// SetEnvironmentBinding replaces one service provider binding in a stopped environment.
func (s *Store) SetEnvironmentBinding(ctx context.Context, projectName, environmentName string, binding model.ComponentBinding) (model.Environment, error) {
	environment, err := s.Environment(ctx, projectName, environmentName)
	if err != nil {
		return model.Environment{}, err
	}
	if environment.Status != model.EnvironmentStopped {
		return model.Environment{}, errors.New("environment must be stopped before a binding changes")
	}
	binding.ModifiedAt = parseTime(nowText())
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

// SetContextSelection binds the closest registered source root containing path to an environment.
func (s *Store) SetContextSelection(ctx context.Context, path, projectName, environmentName string) error {
	path, err := canonicalPath(path)
	if err != nil {
		return err
	}
	key, err := s.PrivateEnvironmentKey(ctx, projectName, environmentName)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT source.path FROM environment_sources source
JOIN environments e ON e.private_key = source.environment_key
WHERE e.project_key = (SELECT project_key FROM environments WHERE private_key = ?)`, key)
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
		return errors.New("the selected environment's project does not use this source path")
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO context_selections(path, environment_key, selected_at) VALUES(?, ?, ?)
ON CONFLICT(path) DO UPDATE SET environment_key = excluded.environment_key, selected_at = excluded.selected_at`, selectionPath, key, nowText())
	return err
}

// ContextSelection resolves the nearest persisted environment selection containing path.
func (s *Store) ContextSelection(ctx context.Context, path string) (model.Environment, error) {
	path, err := canonicalPath(path)
	if err != nil {
		return model.Environment{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.path, p.name, e.name FROM context_selections c
JOIN environments e ON e.private_key = c.environment_key
JOIN projects p ON p.private_key = e.project_key
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

// ClearContextSelection removes the nearest persisted selection containing path.
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

// ForgetEnvironment deletes a stopped environment and all cascaded application state.
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

// SetEnvironmentStatus updates an environment's aggregate runtime status and reason.
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
