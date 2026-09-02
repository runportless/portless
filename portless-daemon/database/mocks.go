package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

// MockScenarioActivationRecord retains the provider restored when a scenario is disabled.
type MockScenarioActivationRecord struct {
	Service         string
	PreviousBinding model.ComponentBinding
	ActivatedAt     time.Time
}

// MockScenarios lists the mock scenarios configured for an environment.
func (s *Store) MockScenarios(ctx context.Context, project, environment string) ([]model.MockScenario, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT name, description, created_at, modified_at
FROM mock_scenarios WHERE environment_key = ? ORDER BY name COLLATE NOCASE`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scenarios := []model.MockScenario{}
	for rows.Next() {
		var scenario model.MockScenario
		var created, modified string
		if err := rows.Scan(&scenario.Name, &scenario.Description, &created, &modified); err != nil {
			return nil, err
		}
		scenario.Project, scenario.Environment = project, environment
		scenario.CreatedAt, scenario.ModifiedAt = parseTime(created), parseTime(modified)
		if err := s.hydrateMockScenario(ctx, key, &scenario); err != nil {
			return nil, err
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios, rows.Err()
}

// MockScenario loads one environment-scoped mock scenario by name.
func (s *Store) MockScenario(ctx context.Context, project, environment, name string) (model.MockScenario, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockScenario{}, err
	}
	var scenario model.MockScenario
	var created, modified string
	err = s.db.QueryRowContext(ctx, `
SELECT name, description, created_at, modified_at
FROM mock_scenarios WHERE environment_key = ? AND name = ? COLLATE NOCASE`, key, name).Scan(
		&scenario.Name, &scenario.Description, &created, &modified)
	if err != nil {
		return model.MockScenario{}, mapSQLError(err)
	}
	scenario.Project, scenario.Environment = project, environment
	scenario.CreatedAt, scenario.ModifiedAt = parseTime(created), parseTime(modified)
	if err := s.hydrateMockScenario(ctx, key, &scenario); err != nil {
		return model.MockScenario{}, err
	}
	return scenario, nil
}

// CreateMockScenario creates an empty environment-scoped scenario.
func (s *Store) CreateMockScenario(ctx context.Context, project, environment string, scenario model.MockScenario) (model.MockScenario, error) {
	if err := model.ValidateArtifactName(scenario.Name); err != nil {
		return model.MockScenario{}, fmt.Errorf("invalid mock scenario name: %w", err)
	}
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockScenario{}, err
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO mock_scenarios(environment_key, name, description, created_at, modified_at)
VALUES(?, ?, ?, ?, ?)`, key, scenario.Name, scenario.Description, now, now)
	if err != nil {
		if isUniqueError(err) {
			return model.MockScenario{}, ErrAlreadyExists
		}
		return model.MockScenario{}, err
	}
	return s.MockScenario(ctx, project, environment, scenario.Name)
}

// DeleteMockScenario removes a disabled scenario and all of its routes.
func (s *Store) DeleteMockScenario(ctx context.Context, project, environment, name string) error {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM mock_scenarios WHERE environment_key = ? AND name = ? COLLATE NOCASE`, key, name)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

// PutMockRoute creates or replaces a named route within a scenario.
func (s *Store) PutMockRoute(ctx context.Context, project, environment, scenarioName string, route model.MockRoute) (model.MockScenario, error) {
	return s.PutMockRoutes(ctx, project, environment, scenarioName, []model.MockRoute{route})
}

// PutMockRoutes creates or replaces a validated batch of scenario routes atomically.
func (s *Store) PutMockRoutes(ctx context.Context, project, environment, scenarioName string, routes []model.MockRoute) (model.MockScenario, error) {
	for _, route := range routes {
		if err := model.ValidateArtifactName(route.Name); err != nil {
			return model.MockScenario{}, fmt.Errorf("invalid mock route name: %w", err)
		}
		if err := model.ValidateServiceName(route.Service); err != nil {
			return model.MockScenario{}, err
		}
	}
	if len(routes) == 0 {
		return s.MockScenario(ctx, project, environment, scenarioName)
	}
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockScenario{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.MockScenario{}, err
	}
	defer tx.Rollback()
	var canonicalScenario string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM mock_scenarios WHERE environment_key = ? AND name = ? COLLATE NOCASE`, key, scenarioName).Scan(&canonicalScenario); err != nil {
		return model.MockScenario{}, mapSQLError(err)
	}
	now := nowText()
	for _, route := range routes {
		if err := putMockRouteTx(ctx, tx, key, canonicalScenario, route, now); err != nil {
			return model.MockScenario{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE mock_scenarios SET modified_at = ? WHERE environment_key = ? AND name = ? COLLATE NOCASE`, now, key, canonicalScenario); err != nil {
		return model.MockScenario{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.MockScenario{}, err
	}
	return s.MockScenario(ctx, project, environment, canonicalScenario)
}

func putMockRouteTx(ctx context.Context, tx *sql.Tx, environmentKey, scenario string, route model.MockRoute, now string) error {
	queryJSON, err := json.Marshal(nonNilStringMap(route.Query))
	if err != nil {
		return err
	}
	headersJSON, err := json.Marshal(nonNilStringMap(route.Headers))
	if err != nil {
		return err
	}
	created := route.CreatedAt.UTC().Format(time.RFC3339Nano)
	if route.CreatedAt.IsZero() {
		created = now
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO mock_scenario_routes(environment_key, scenario_name, name, service_name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(environment_key, scenario_name, name) DO UPDATE SET
  service_name = excluded.service_name, method = excluded.method, path = excluded.path,
  query_json = excluded.query_json, status = excluded.status, headers_json = excluded.headers_json,
  body = excluded.body, delay_ms = excluded.delay_ms, enabled = excluded.enabled,
  modified_at = excluded.modified_at`,
		environmentKey, scenario, route.Name, route.Service, strings.ToUpper(route.Method), route.Path, queryJSON, route.Status,
		headersJSON, route.Body, route.DelayMS, boolToInt(route.Enabled), created, now)
	return err
}

// DeleteMockRoute removes one named route from a mock scenario.
func (s *Store) DeleteMockRoute(ctx context.Context, project, environment, scenarioName, routeName string) (model.MockScenario, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockScenario{}, err
	}
	result, err := s.db.ExecContext(ctx, `
DELETE FROM mock_scenario_routes WHERE environment_key = ? AND scenario_name = ? COLLATE NOCASE AND name = ? COLLATE NOCASE`, key, scenarioName, routeName)
	if err != nil {
		return model.MockScenario{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.MockScenario{}, ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE mock_scenarios SET modified_at = ? WHERE environment_key = ? AND name = ? COLLATE NOCASE`, nowText(), key, scenarioName); err != nil {
		return model.MockScenario{}, err
	}
	return s.MockScenario(ctx, project, environment, scenarioName)
}

// MockScenarioActivations returns the private restoration records for a scenario.
func (s *Store) MockScenarioActivations(ctx context.Context, project, environment, scenario string) ([]MockScenarioActivationRecord, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT service_name, previous_binding_json, activated_at
FROM mock_scenario_activations
WHERE environment_key = ? AND scenario_name = ? COLLATE NOCASE
ORDER BY service_name COLLATE NOCASE`, key, scenario)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MockScenarioActivationRecord{}
	for rows.Next() {
		var item MockScenarioActivationRecord
		var bindingJSON []byte
		var activated string
		if err := rows.Scan(&item.Service, &bindingJSON, &activated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(bindingJSON, &item.PreviousBinding); err != nil {
			return nil, fmt.Errorf("decode previous binding for %s: %w", item.Service, err)
		}
		item.ActivatedAt = parseTime(activated)
		result = append(result, item)
	}
	return result, rows.Err()
}

// ActiveMockScenarioForService returns the scenario which owns one service activation.
func (s *Store) ActiveMockScenarioForService(ctx context.Context, project, environment, service string) (string, bool, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return "", false, err
	}
	var scenario string
	err = s.db.QueryRowContext(ctx, `
SELECT scenario_name FROM mock_scenario_activations
WHERE environment_key = ? AND service_name = ? COLLATE NOCASE`, key, service).Scan(&scenario)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return scenario, true, nil
}

func (s *Store) hydrateMockScenario(ctx context.Context, environmentKey string, scenario *model.MockScenario) error {
	scenario.Activation = model.MockScenarioActivation{TargetServices: []string{}, ActiveServices: []string{}}
	routes, err := s.mockRoutes(ctx, environmentKey, scenario.Name)
	if err != nil {
		return err
	}
	scenario.Routes = routes
	targets := map[string]string{}
	for _, route := range routes {
		targets[strings.ToLower(route.Service)] = route.Service
	}
	for _, service := range targets {
		scenario.Activation.TargetServices = append(scenario.Activation.TargetServices, service)
	}
	sort.Slice(scenario.Activation.TargetServices, func(i, j int) bool {
		return strings.ToLower(scenario.Activation.TargetServices[i]) < strings.ToLower(scenario.Activation.TargetServices[j])
	})
	rows, err := s.db.QueryContext(ctx, `
SELECT a.service_name, a.activated_at, b.provider, b.config_json
FROM mock_scenario_activations a
LEFT JOIN environment_bindings b ON b.environment_key = a.environment_key AND b.service_name = a.service_name COLLATE NOCASE
WHERE a.environment_key = ? AND a.scenario_name = ? COLLATE NOCASE
ORDER BY a.service_name COLLATE NOCASE`, environmentKey, scenario.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	activationCount := 0
	for rows.Next() {
		var service, activated string
		var provider sql.NullString
		var configJSON []byte
		if err := rows.Scan(&service, &activated, &provider, &configJSON); err != nil {
			return err
		}
		activationCount++
		if scenario.Activation.EnabledAt.IsZero() || parseTime(activated).Before(scenario.Activation.EnabledAt) {
			scenario.Activation.EnabledAt = parseTime(activated)
		}
		if provider.String != string(model.ProviderMock) {
			continue
		}
		var target model.MockTarget
		if json.Unmarshal(configJSON, &target) == nil && strings.EqualFold(target.Scenario, scenario.Name) {
			scenario.Activation.ActiveServices = append(scenario.Activation.ActiveServices, service)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	switch {
	case activationCount == 0:
		scenario.Activation.State = model.MockScenarioDisabled
	case activationCount == len(scenario.Activation.TargetServices) && len(scenario.Activation.ActiveServices) == len(scenario.Activation.TargetServices):
		scenario.Activation.State = model.MockScenarioEnabled
	default:
		scenario.Activation.State = model.MockScenarioDegraded
	}
	return nil
}

func (s *Store) mockRoutes(ctx context.Context, environmentKey, scenarioName string) ([]model.MockRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT name, service_name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at
FROM mock_scenario_routes WHERE environment_key = ? AND scenario_name = ? COLLATE NOCASE
ORDER BY service_name COLLATE NOCASE, name COLLATE NOCASE`, environmentKey, scenarioName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := []model.MockRoute{}
	for rows.Next() {
		var route model.MockRoute
		var queryJSON, headersJSON []byte
		var enabled int
		var created, modified string
		if err := rows.Scan(&route.Name, &route.Service, &route.Method, &route.Path, &queryJSON, &route.Status, &headersJSON,
			&route.Body, &route.DelayMS, &enabled, &created, &modified); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(queryJSON, &route.Query); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(headersJSON, &route.Headers); err != nil {
			return nil, err
		}
		route.Enabled = enabled != 0
		route.CreatedAt, route.ModifiedAt = parseTime(created), parseTime(modified)
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (s *Store) cloneMockScenarios(ctx context.Context, project, sourceEnvironment, targetEnvironment string) error {
	var sourceKey, targetKey string
	if err := s.db.QueryRowContext(ctx, `SELECT e.private_key FROM environments e JOIN projects p ON p.private_key=e.project_key WHERE p.name=? COLLATE NOCASE AND e.name=? COLLATE NOCASE`, project, sourceEnvironment).Scan(&sourceKey); err != nil {
		return mapSQLError(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT e.private_key FROM environments e JOIN projects p ON p.private_key=e.project_key WHERE p.name=? COLLATE NOCASE AND e.name=? COLLATE NOCASE`, project, targetEnvironment).Scan(&targetKey); err != nil {
		return mapSQLError(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mock_scenarios(environment_key, name, description, created_at, modified_at)
SELECT ?, name, description, created_at, modified_at FROM mock_scenarios WHERE environment_key = ?`, targetKey, sourceKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mock_scenario_routes(environment_key, scenario_name, name, service_name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at)
SELECT ?, scenario_name, name, service_name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at
FROM mock_scenario_routes WHERE environment_key = ?`, targetKey, sourceKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mock_scenario_activations(environment_key, scenario_name, service_name, previous_binding_json, activated_at)
SELECT ?, scenario_name, service_name, previous_binding_json, ?
FROM mock_scenario_activations WHERE environment_key = ?`, targetKey, nowText(), sourceKey); err != nil {
		return err
	}
	return tx.Commit()
}

func nonNilStringMap(input map[string]string) map[string]string {
	if input == nil {
		return map[string]string{}
	}
	return input
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
