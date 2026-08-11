package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/internal/model"
)

type projectRow struct {
	key            string
	name           string
	path           string
	revision       int64
	status         string
	reason         string
	primaryService string
	modelJSON      []byte
	createdAt      string
	updatedAt      string
}

func (s *Store) CreateProject(ctx context.Context, name, path string, definition model.ProjectModel) (model.Project, error) {
	if existing, err := s.ProjectByPath(ctx, path); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return model.Project{}, err
	}
	var ownerPath string
	err := s.db.QueryRowContext(ctx, `SELECT path FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(&ownerPath)
	if err == nil && ownerPath != path {
		return model.Project{}, ErrNameTaken
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.Project{}, err
	}
	privateKey, err := newPrivateKey()
	if err != nil {
		return model.Project{}, fmt.Errorf("generate private project key: %w", err)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return model.Project{}, fmt.Errorf("encode project model: %w", err)
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Project{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO projects(private_key, name, path, revision, status, reason, primary_service, trusted, model_json, created_at, updated_at)
VALUES(?, ?, ?, 1, ?, ?, ?, 1, ?, ?, ?)`,
		privateKey, name, path, model.ProjectStopped,
		"", definition.PrimaryService, encoded, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return model.Project{}, ErrNameTaken
		}
		return model.Project{}, fmt.Errorf("insert project: %w", err)
	}
	for _, service := range definition.Services {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO service_runtime(project_key, service_name, status)
VALUES(?, ?, ?)`, privateKey, service.Name, model.ServicePlanned); err != nil {
			return model.Project{}, fmt.Errorf("insert service runtime %s: %w", service.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Project{}, err
	}
	return s.Project(ctx, name)
}

func (s *Store) Project(ctx context.Context, name string) (model.Project, error) {
	row, err := s.readProjectRow(ctx, `SELECT private_key, name, path, revision, status, reason, primary_service, trusted, model_json, created_at, updated_at FROM projects WHERE name = ? COLLATE NOCASE`, name)
	if err != nil {
		return model.Project{}, err
	}
	return s.hydrateProject(ctx, row)
}

func (s *Store) ProjectByPath(ctx context.Context, path string) (model.Project, error) {
	row, err := s.readProjectRow(ctx, `SELECT private_key, name, path, revision, status, reason, primary_service, trusted, model_json, created_at, updated_at FROM projects WHERE path = ?`, path)
	if err != nil {
		return model.Project{}, err
	}
	return s.hydrateProject(ctx, row)
}

func (s *Store) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT private_key, name, path, revision, status, reason, primary_service, trusted, model_json, created_at, updated_at
FROM projects
ORDER BY CASE WHEN status = 'stopped' THEN 1 ELSE 0 END, updated_at DESC, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.Project
	for rows.Next() {
		row, err := scanProjectRow(rows)
		if err != nil {
			return nil, err
		}
		project, err := s.hydrateProject(ctx, row)
		if err != nil {
			return nil, err
		}
		result = append(result, project)
	}
	return result, rows.Err()
}

func (s *Store) ProjectModel(ctx context.Context, name string) (model.ProjectModel, error) {
	var encoded []byte
	if err := s.db.QueryRowContext(ctx, `SELECT model_json FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(&encoded); err != nil {
		return model.ProjectModel{}, mapSQLError(err)
	}
	var definition model.ProjectModel
	if err := json.Unmarshal(encoded, &definition); err != nil {
		return model.ProjectModel{}, fmt.Errorf("decode project model: %w", err)
	}
	return definition, nil
}

func (s *Store) UpdateProjectModel(ctx context.Context, name string, expectedRevision int64, definition model.ProjectModel) (model.Project, error) {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return model.Project{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Project{}, err
	}
	defer tx.Rollback()
	var projectKey string
	var revision int64
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT private_key, revision, status FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(&projectKey, &revision, &status); err != nil {
		return model.Project{}, mapSQLError(err)
	}
	if expectedRevision > 0 && expectedRevision != revision {
		return model.Project{}, ErrConflict
	}
	if status != string(model.ProjectStopped) {
		return model.Project{}, fmt.Errorf("project must be stopped before rescan")
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE projects SET model_json = ?, primary_service = ?, revision = revision + 1,
	  trusted = 1, status = ?, reason = '', updated_at = ? WHERE private_key = ?`,
		encoded, definition.PrimaryService, model.ProjectStopped, nowText(), projectKey); err != nil {
		return model.Project{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM service_runtime WHERE project_key = ?`, projectKey); err != nil {
		return model.Project{}, err
	}
	for _, service := range definition.Services {
		if _, err := tx.ExecContext(ctx, `INSERT INTO service_runtime(project_key, service_name, status) VALUES(?, ?, ?)`, projectKey, service.Name, model.ServicePlanned); err != nil {
			return model.Project{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Project{}, err
	}
	return s.Project(ctx, name)
}

func (s *Store) RenameProject(ctx context.Context, oldName, newName string, expectedRevision int64) (model.Project, error) {
	project, err := s.Project(ctx, oldName)
	if err != nil {
		return model.Project{}, err
	}
	if project.Status != model.ProjectStopped {
		return model.Project{}, fmt.Errorf("project must be stopped before rename")
	}
	if project.Revision != expectedRevision {
		return model.Project{}, ErrConflict
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE name = ? COLLATE NOCASE`, newName).Scan(&exists); err == nil {
		return model.Project{}, ErrNameTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.Project{}, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE projects SET name = ?, revision = revision + 1, updated_at = ? WHERE name = ? COLLATE NOCASE`, newName, nowText(), oldName)
	if err != nil {
		return model.Project{}, err
	}
	return s.Project(ctx, newName)
}

func (s *Store) ForgetProject(ctx context.Context, name string) error {
	project, err := s.Project(ctx, name)
	if err != nil {
		return err
	}
	if project.Status != model.ProjectStopped {
		return fmt.Errorf("project must be stopped before it can be forgotten")
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

func (s *Store) SetProjectStatus(ctx context.Context, name string, status model.ProjectStatus, reason string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET status = ?, reason = ?, updated_at = ? WHERE name = ? COLLATE NOCASE`, status, reason, nowText(), name)
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
	Status        model.ServiceStatus
	Reason        string
	Generation    int64
	PID           int
	UpstreamPort  int
	StartedAt     *time.Time
	RestartCount  int64
	LogPath       string
	PrivateRunKey string
}

func (s *Store) SetServiceRuntime(ctx context.Context, projectName, serviceName string, update ServiceRuntimeUpdate) error {
	key, err := s.PrivateProjectKey(ctx, projectName)
	if err != nil {
		return err
	}
	var started any
	if update.StartedAt != nil {
		started = update.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE service_runtime SET status = ?, reason = ?, generation = ?, pid = ?, upstream_port = ?,
  started_at = ?, restart_count = ?, log_path = ?, private_run_key = ?
WHERE project_key = ? AND service_name = ? COLLATE NOCASE`,
		update.Status, update.Reason, update.Generation, update.PID, update.UpstreamPort,
		started, update.RestartCount, update.LogPath, update.PrivateRunKey, key, serviceName)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) PrivateProjectKey(ctx context.Context, name string) (string, error) {
	var key string
	if err := s.db.QueryRowContext(ctx, `SELECT private_key FROM projects WHERE name = ? COLLATE NOCASE`, name).Scan(&key); err != nil {
		return "", mapSQLError(err)
	}
	return key, nil
}

func (s *Store) ServiceLogPath(ctx context.Context, projectName, serviceName string) (string, error) {
	key, err := s.PrivateProjectKey(ctx, projectName)
	if err != nil {
		return "", err
	}
	var path string
	if err := s.db.QueryRowContext(ctx, `SELECT log_path FROM service_runtime WHERE project_key = ? AND service_name = ? COLLATE NOCASE`, key, serviceName).Scan(&path); err != nil {
		return "", mapSQLError(err)
	}
	return path, nil
}

func (s *Store) readProjectRow(ctx context.Context, query string, args ...any) (projectRow, error) {
	row, err := scanProjectRow(s.db.QueryRowContext(ctx, query, args...))
	return row, mapSQLError(err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProjectRow(scanner rowScanner) (projectRow, error) {
	var row projectRow
	var legacyTrusted int
	err := scanner.Scan(&row.key, &row.name, &row.path, &row.revision, &row.status, &row.reason,
		&row.primaryService, &legacyTrusted, &row.modelJSON, &row.createdAt, &row.updatedAt)
	return row, err
}

func (s *Store) hydrateProject(ctx context.Context, row projectRow) (model.Project, error) {
	var definition model.ProjectModel
	if err := json.Unmarshal(row.modelJSON, &definition); err != nil {
		return model.Project{}, fmt.Errorf("decode project %s model: %w", row.name, err)
	}
	runtimeRows, err := s.db.QueryContext(ctx, `
SELECT service_name, status, reason, generation, pid, upstream_port, started_at, restart_count
FROM service_runtime WHERE project_key = ?`, row.key)
	if err != nil {
		return model.Project{}, err
	}
	defer runtimeRows.Close()
	runtime := map[string]model.Service{}
	for runtimeRows.Next() {
		var service model.Service
		var status string
		var started sql.NullString
		if err := runtimeRows.Scan(&service.Name, &status, &service.Reason, &service.Generation,
			&service.PID, &service.UpstreamPort, &started, &service.RestartCount); err != nil {
			return model.Project{}, err
		}
		service.Status = model.ServiceStatus(status)
		service.StartedAt = parseOptionalTime(started)
		runtime[strings.ToLower(service.Name)] = service
	}
	var services []model.Service
	for _, definitionService := range definition.Services {
		service := runtime[strings.ToLower(definitionService.Name)]
		service.ServiceDefinition = definitionService
		if service.Status == "" {
			service.Status = model.ServicePlanned
		}
		services = append(services, service)
	}
	sort.SliceStable(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return model.Project{
		Name:           row.name,
		Path:           row.path,
		Revision:       row.revision,
		Status:         model.ProjectStatus(row.status),
		Reason:         row.reason,
		PrimaryService: row.primaryService,
		CreatedAt:      parseTime(row.createdAt),
		UpdatedAt:      parseTime(row.updatedAt),
		Services:       services,
		Connections:    definition.Connections,
	}, nil
}
