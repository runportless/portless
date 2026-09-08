package database

import (
	"context"
	"encoding/json"
	"github.com/runportless/portless/portless-daemon/model"
)

// MockScenarioMetadataList loads a bounded page without reading response bodies.
func (s *Store) MockScenarioMetadataList(ctx context.Context, project, environment string, offset, limit int) ([]model.MockScenarioMetadata, int, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mock_scenarios WHERE environment_key=?`, key).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM mock_scenarios WHERE environment_key=? ORDER BY name COLLATE NOCASE LIMIT ? OFFSET ?`, key, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, 0, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	items := []model.MockScenarioMetadata{}
	for _, name := range names {
		item, err := s.MockScenarioMetadata(ctx, project, environment, name)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, nil
}

// MockScenarioMetadata loads scenario identity, counts, and provider activation.
func (s *Store) MockScenarioMetadata(ctx context.Context, project, environment, name string) (model.MockScenarioMetadata, error) {
	var result model.MockScenarioMetadata
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return result, err
	}
	var created, modified string
	err = s.db.QueryRowContext(ctx, `SELECT name,description,created_at,modified_at,unmatched_requests,(SELECT COUNT(*) FROM mock_scenario_routes r WHERE r.environment_key=s.environment_key AND r.scenario_name=s.name) FROM mock_scenarios s WHERE environment_key=? AND name=? COLLATE NOCASE`, key, name).Scan(&result.Name, &result.Description, &created, &modified, &result.UnmatchedRequests, &result.RouteCount)
	if err != nil {
		return result, mapSQLError(err)
	}
	result.Project, result.Environment = project, environment
	result.CreatedAt, result.ModifiedAt = parseTime(created), parseTime(modified)
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT service_name FROM mock_scenario_routes WHERE environment_key=? AND scenario_name=? COLLATE NOCASE`, key, name)
	if err != nil {
		return result, err
	}
	scenario := model.MockScenario{Name: result.Name, UnmatchedRequests: result.UnmatchedRequests}
	for rows.Next() {
		var service string
		if err := rows.Scan(&service); err != nil {
			rows.Close()
			return result, err
		}
		scenario.Routes = append(scenario.Routes, model.MockRoute{Service: service})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if err := s.hydrateMockActivation(ctx, key, &scenario); err != nil {
		return result, err
	}
	result.Activation = scenario.Activation
	var parent string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM environments WHERE private_key=?`, key).Scan(&parent); err != nil {
		return result, err
	}
	result.Version = model.ResourceVersion{CreatedAt: result.CreatedAt, ModifiedAt: result.ModifiedAt, ParentCreatedAt: parseTime(parent)}
	return result, nil
}

// MockRouteMetadataPage reads an indexed page, excluding matcher values and bodies.
func (s *Store) MockRouteMetadataPage(ctx context.Context, project, environment, scenario, name string, offset, limit int) ([]model.MockRouteMetadata, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name,service_name,method,path,status,delay_ms,enabled,created_at,modified_at,length(CAST(body AS BLOB)),
 (SELECT COALESCE(json_group_object(j.key,json_extract(j.value,'$.match')),'{}') FROM json_each(query_json) j),
 (SELECT COALESCE(json_group_array(j.key),'[]') FROM json_each(headers_json) j)
 FROM mock_scenario_routes WHERE environment_key=? AND scenario_name=? COLLATE NOCASE AND (?='' OR name=? COLLATE NOCASE)
 ORDER BY service_name COLLATE NOCASE,name COLLATE NOCASE LIMIT ? OFFSET ?`, key, scenario, name, name, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.MockRouteMetadata{}
	for rows.Next() {
		var route model.MockRouteMetadata
		var created, modified string
		var query, headers []byte
		if err := rows.Scan(&route.Name, &route.Service, &route.Method, &route.Path, &route.Status, &route.DelayMS, &route.Enabled, &created, &modified, &route.BodyBytes, &query, &headers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(query, &route.QueryKinds); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(headers, &route.HeaderNames); err != nil {
			return nil, err
		}
		route.CreatedAt, route.ModifiedAt = parseTime(created), parseTime(modified)
		result = append(result, route)
	}
	return result, rows.Err()
}

// MockRoute loads exactly one route, including its saved application payload.
func (s *Store) MockRoute(ctx context.Context, project, environment, scenario, name string) (model.MockRoute, error) {
	key, err := s.PrivateEnvironmentKey(ctx, project, environment)
	if err != nil {
		return model.MockRoute{}, err
	}
	routes, err := s.mockRoutesMatching(ctx, key, scenario, name)
	if err != nil {
		return model.MockRoute{}, err
	}
	if len(routes) == 0 {
		return model.MockRoute{}, ErrNotFound
	}
	return routes[0], nil
}
