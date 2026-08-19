package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
)

// MockProfiles lists the mock profiles configured for an environment.
func (s *Store) MockProfiles(ctx context.Context, project, environment string) ([]model.MockProfile, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT name, service_name, description, created_at, modified_at
FROM mock_profiles WHERE environment_key = ? ORDER BY name COLLATE NOCASE`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []model.MockProfile{}
	for rows.Next() {
		var profile model.MockProfile
		var created, modified string
		if err := rows.Scan(&profile.Name, &profile.Service, &profile.Description, &created, &modified); err != nil {
			return nil, err
		}
		profile.Project, profile.Environment = project, environment
		profile.CreatedAt, profile.ModifiedAt = parseTime(created), parseTime(modified)
		profile.Routes, err = s.mockRoutes(ctx, key, profile.Name)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

// MockProfile loads one environment-scoped mock profile by name.
func (s *Store) MockProfile(ctx context.Context, project, environment, name string) (model.MockProfile, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockProfile{}, err
	}
	var profile model.MockProfile
	var created, modified string
	err = s.db.QueryRowContext(ctx, `
SELECT name, service_name, description, created_at, modified_at
FROM mock_profiles WHERE environment_key = ? AND name = ? COLLATE NOCASE`, key, name).Scan(
		&profile.Name, &profile.Service, &profile.Description, &created, &modified)
	if err != nil {
		return model.MockProfile{}, mapSQLError(err)
	}
	profile.Project, profile.Environment = project, environment
	profile.CreatedAt, profile.ModifiedAt = parseTime(created), parseTime(modified)
	profile.Routes, err = s.mockRoutes(ctx, key, profile.Name)
	return profile, err
}

// CreateMockProfile creates an empty profile attached to one HTTP service.
func (s *Store) CreateMockProfile(ctx context.Context, project, environment string, profile model.MockProfile) (model.MockProfile, error) {
	if err := model.ValidateArtifactName(profile.Name); err != nil {
		return model.MockProfile{}, fmt.Errorf("invalid mock profile name: %w", err)
	}
	if err := model.ValidateServiceName(profile.Service); err != nil {
		return model.MockProfile{}, err
	}
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockProfile{}, err
	}
	now := nowText()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO mock_profiles(environment_key, name, service_name, description, created_at, modified_at)
VALUES(?, ?, ?, ?, ?, ?)`, key, profile.Name, profile.Service, profile.Description, now, now)
	if err != nil {
		if isUniqueError(err) {
			return model.MockProfile{}, ErrAlreadyExists
		}
		return model.MockProfile{}, err
	}
	return s.MockProfile(ctx, project, environment, profile.Name)
}

// DeleteMockProfile removes a profile and all of its routes.
func (s *Store) DeleteMockProfile(ctx context.Context, project, environment, name string) error {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM mock_profiles WHERE environment_key = ? AND name = ? COLLATE NOCASE`, key, name)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

// PutMockRoute creates or replaces a named route within a profile.
func (s *Store) PutMockRoute(ctx context.Context, project, environment, profileName string, route model.MockRoute) (model.MockProfile, error) {
	if err := model.ValidateArtifactName(route.Name); err != nil {
		return model.MockProfile{}, fmt.Errorf("invalid mock route name: %w", err)
	}
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockProfile{}, err
	}
	var canonicalProfile string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM mock_profiles WHERE environment_key = ? AND name = ? COLLATE NOCASE`, key, profileName).Scan(&canonicalProfile); err != nil {
		return model.MockProfile{}, mapSQLError(err)
	}
	queryJSON, err := json.Marshal(nonNilStringMap(route.Query))
	if err != nil {
		return model.MockProfile{}, err
	}
	headersJSON, err := json.Marshal(nonNilStringMap(route.Headers))
	if err != nil {
		return model.MockProfile{}, err
	}
	now := nowText()
	created := route.CreatedAt.UTC().Format(time.RFC3339Nano)
	if route.CreatedAt.IsZero() {
		created = now
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO mock_routes(environment_key, profile_name, name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(environment_key, profile_name, name) DO UPDATE SET
  method = excluded.method, path = excluded.path, query_json = excluded.query_json,
  status = excluded.status, headers_json = excluded.headers_json, body = excluded.body,
  delay_ms = excluded.delay_ms, enabled = excluded.enabled, modified_at = excluded.modified_at`,
		key, canonicalProfile, route.Name, strings.ToUpper(route.Method), route.Path, queryJSON, route.Status,
		headersJSON, route.Body, route.DelayMS, boolToInt(route.Enabled), created, now)
	if err != nil {
		return model.MockProfile{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE mock_profiles SET modified_at = ? WHERE environment_key = ? AND name = ? COLLATE NOCASE`, now, key, canonicalProfile); err != nil {
		return model.MockProfile{}, err
	}
	return s.MockProfile(ctx, project, environment, canonicalProfile)
}

// DeleteMockRoute removes one named route from a mock profile.
func (s *Store) DeleteMockRoute(ctx context.Context, project, environment, profileName, routeName string) (model.MockProfile, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockProfile{}, err
	}
	result, err := s.db.ExecContext(ctx, `
DELETE FROM mock_routes WHERE environment_key = ? AND profile_name = ? COLLATE NOCASE AND name = ? COLLATE NOCASE`, key, profileName, routeName)
	if err != nil {
		return model.MockProfile{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.MockProfile{}, ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE mock_profiles SET modified_at = ? WHERE environment_key = ? AND name = ? COLLATE NOCASE`, nowText(), key, profileName); err != nil {
		return model.MockProfile{}, err
	}
	return s.MockProfile(ctx, project, environment, profileName)
}

func (s *Store) mockRoutes(ctx context.Context, environmentKey, profileName string) ([]model.MockRoute, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at
FROM mock_routes WHERE environment_key = ? AND profile_name = ? COLLATE NOCASE
ORDER BY name COLLATE NOCASE`, environmentKey, profileName)
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
		if err := rows.Scan(&route.Name, &route.Method, &route.Path, &queryJSON, &route.Status, &headersJSON,
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

func (s *Store) cloneMockProfiles(ctx context.Context, project, sourceEnvironment, targetEnvironment string) error {
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
INSERT INTO mock_profiles(environment_key, name, service_name, description, created_at, modified_at)
SELECT ?, name, service_name, description, created_at, modified_at FROM mock_profiles WHERE environment_key = ?`, targetKey, sourceKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO mock_routes(environment_key, profile_name, name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at)
SELECT ?, profile_name, name, method, path, query_json, status, headers_json, body, delay_ms, enabled, created_at, modified_at
FROM mock_routes WHERE environment_key = ?`, targetKey, sourceKey); err != nil {
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
