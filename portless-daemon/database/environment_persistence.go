package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/portless-run/portless/portless-daemon/model"
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
SELECT service_name, status, reason, generation, pid, upstream_port, started_at, restart_count,
       launch_mode, debug_adapter, debug_host, debug_port, debug_state
FROM service_runtime WHERE environment_key = ?`, row.key)
	if err != nil {
		return model.Environment{}, err
	}
	runtime := map[string]model.Service{}
	for runtimeRows.Next() {
		var service model.Service
		var status string
		var launchMode, debugAdapter, debugHost, debugState string
		var debugPort int
		var started sql.NullString
		if err := runtimeRows.Scan(&service.Name, &status, &service.Reason, &service.Generation,
			&service.PID, &service.UpstreamPort, &started, &service.RestartCount,
			&launchMode, &debugAdapter, &debugHost, &debugPort, &debugState); err != nil {
			runtimeRows.Close()
			return model.Environment{}, err
		}
		service.Status = model.ServiceStatus(status)
		service.LaunchMode = model.LaunchMode(launchMode)
		if debugAdapter != "" {
			service.Debugger = &model.DebuggerRuntime{Adapter: model.DebugAdapter(debugAdapter), Host: debugHost, Port: debugPort, State: debugState}
		}
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
		if service.LaunchMode == "" {
			service.LaunchMode = model.LaunchManaged
		}
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
SELECT service_name, provider, source_name, config_json, modified_at
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
		var modifiedAt string
		if err := rows.Scan(&binding.Service, &provider, &binding.Source, &config, &modifiedAt); err != nil {
			return nil, err
		}
		binding.Provider = model.ProviderKind(provider)
		binding.ModifiedAt = parseTime(modifiedAt)
		if binding.Provider == model.ProviderRemote {
			var remote model.RemoteTarget
			if err := json.Unmarshal(config, &remote); err != nil {
				return nil, err
			}
			binding.Remote = &remote
		} else if binding.Provider == model.ProviderMock {
			var mock model.MockTarget
			if err := json.Unmarshal(config, &mock); err != nil {
				return nil, err
			}
			binding.Mock = &mock
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
	} else if binding.Mock != nil {
		config, err = json.Marshal(binding.Mock)
		if err != nil {
			return err
		}
	}
	modifiedAt := binding.ModifiedAt
	if modifiedAt.IsZero() {
		modifiedAt = time.Now().UTC()
	}
	_, err = executor.ExecContext(ctx, `
INSERT INTO environment_bindings(environment_key, service_name, provider, source_name, config_json, modified_at)
VALUES(?, ?, ?, ?, ?, ?)`, environmentKey, binding.Service, binding.Provider, binding.Source, config, modifiedAt.UTC().Format(time.RFC3339Nano))
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

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
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
