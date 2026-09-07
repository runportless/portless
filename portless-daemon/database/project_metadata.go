package database

import (
	"context"
	"encoding/json"
	"github.com/runportless/portless/portless-daemon/model"
)

// ProjectDeclaration reads declaration identity and topology from the same database row.
// The control plane must apply its safe projection before exposing this value.
func (s *Store) ProjectDeclaration(ctx context.Context, name string) (model.ProjectDeclaration, error) {
	var result model.ProjectDeclaration
	var created string
	var encoded []byte
	err := s.db.QueryRowContext(ctx, `SELECT name,revision,created_at,model_json FROM projects WHERE name=? COLLATE NOCASE`, name).Scan(&result.Project, &result.Revision, &created, &encoded)
	if err != nil {
		return result, mapSQLError(err)
	}
	result.CreatedAt = parseTime(created)
	result.ProjectModel, err = decodeProjectModel(encoded)
	return result, err
}

// ProjectMetadataList reads a bounded page of public project summaries.
func (s *Store) ProjectMetadataList(ctx context.Context, offset, limit int) ([]model.ProjectMetadata, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM projects ORDER BY name COLLATE NOCASE LIMIT ? OFFSET ?`, limit, offset)
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
	result := []model.ProjectMetadata{}
	for _, name := range names {
		item, err := s.ProjectMetadata(ctx, name, false)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, item)
	}
	return result, total, nil
}

// ProjectMetadata reads public state without hydrating private runtime bindings.
func (s *Store) ProjectMetadata(ctx context.Context, name string, topology bool) (model.ProjectMetadata, error) {
	var result model.ProjectMetadata
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT name,revision,primary_service,created_at,updated_at,COALESCE(json_array_length(model_json,'$.definition.services'),0),COALESCE(json_array_length(sources_json),0) FROM projects WHERE name=? COLLATE NOCASE`, name).Scan(&result.Name, &result.Revision, &result.PrimaryService, &created, &updated, &result.ServiceCount, &result.SourceCount)
	if err != nil {
		return result, mapSQLError(err)
	}
	result.CreatedAt, result.UpdatedAt = parseTime(created), parseTime(updated)
	rows, err := s.db.QueryContext(ctx, `SELECT e.name,e.revision,e.status,e.reason,e.cloned_from_name,e.updated_at,COALESCE(json_array_length(e.model_json,'$.definition.services'),0),(SELECT COUNT(*) FROM environment_bindings b WHERE b.environment_key=e.private_key AND b.provider='remote'),(SELECT COUNT(*) FROM service_runtime r WHERE r.environment_key=e.private_key AND r.status='ready') FROM environments e JOIN projects p ON p.private_key=e.project_key WHERE p.name=? COLLATE NOCASE ORDER BY e.name COLLATE NOCASE`, name)
	if err != nil {
		return result, err
	}
	result.Environments = []model.EnvironmentSummary{}
	for rows.Next() {
		var item model.EnvironmentSummary
		var updated string
		item.Project = result.Name
		if err := rows.Scan(&item.Name, &item.Revision, &item.Status, &item.Reason, &item.ClonedFrom, &updated, &item.ServiceCount, &item.RemoteCount, &item.ReadyCount); err != nil {
			rows.Close()
			return result, err
		}
		item.UpdatedAt = parseTime(updated)
		result.Environments = append(result.Environments, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if topology {
		definition, err := s.ProjectModel(ctx, name)
		if err != nil {
			return result, err
		}
		var sources []byte
		if err := s.db.QueryRowContext(ctx, `SELECT sources_json FROM projects WHERE name=? COLLATE NOCASE`, name).Scan(&sources); err != nil {
			return result, err
		}
		if err := json.Unmarshal(sources, &result.Sources); err != nil {
			return result, err
		}
		result.Connections = definition.Connections
		for _, service := range definition.Services {
			result.Services = append(result.Services, model.LogicalServiceMetadata{Name: service.Name, Kind: service.Kind, Framework: service.Framework, Resource: service.Resource, Required: service.Required})
		}
	}
	return result, nil
}
