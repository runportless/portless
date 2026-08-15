package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrNameTaken         = errors.New("name already exists")
	ErrPathTaken         = errors.New("source path already registered")
	ErrConflict          = errors.New("revision conflict")
	ErrAlreadyExists     = errors.New("resource already exists")
	ErrIncompatibleState = errors.New("stored project model is incompatible with this Portless build")
)

type ActiveProjectEnvironmentsError struct {
	Environments []string `json:"environments"`
}

func (e ActiveProjectEnvironmentsError) Error() string {
	return "all project environments must be stopped before the project topology changes: " + strings.Join(e.Environments, ", ")
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	// Immediate transactions serialize writers before they read sequence values.
	// Deferred transactions can deadlock while upgrading two concurrent
	// read-then-write operations even when busy_timeout is configured.
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_foreign_keys=on&_busy_timeout=5000&_journal_mode=wal&_txlock=immediate"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pragma := range []string{"PRAGMA foreign_keys = ON", "PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL"} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	result := &Store{db: db}
	if err := result.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create sqlite schema: %w", err)
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"owner_instance_id", "TEXT NOT NULL DEFAULT ''"},
		{"supervisor_socket", "TEXT NOT NULL DEFAULT ''"},
		{"supervisor_state", "TEXT NOT NULL DEFAULT ''"},
		{"supervisor_pid", "INTEGER NOT NULL DEFAULT 0"},
		{"container_name", "TEXT NOT NULL DEFAULT ''"},
		{"observed_at", "TEXT"},
		{"listen_ip", "TEXT NOT NULL DEFAULT '127.0.0.1'"},
		{"dns_name", "TEXT NOT NULL DEFAULT ''"},
	} {
		table := "service_runtime"
		if column.name == "listen_ip" || column.name == "dns_name" {
			table = "connection_runtime"
		}
		if err := s.ensureColumn(ctx, table, column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, ?)`, nowText()); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(2, ?)`, nowText()); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(3, ?)`, nowText()); err != nil {
		return fmt.Errorf("record schema version: %w", err)
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, name, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var index int
		var column, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&index, &column, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if column == name {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, name, err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
  private_key TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  revision INTEGER NOT NULL DEFAULT 1,
  primary_service TEXT NOT NULL DEFAULT '',
  model_json BLOB NOT NULL,
  sources_json BLOB NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS environments (
  private_key TEXT PRIMARY KEY,
  project_key TEXT NOT NULL REFERENCES projects(private_key) ON DELETE CASCADE,
  name TEXT NOT NULL COLLATE NOCASE,
  revision INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  primary_service TEXT NOT NULL DEFAULT '',
  model_json BLOB NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(project_key, name)
);

CREATE TABLE IF NOT EXISTS environment_sources (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  source_name TEXT NOT NULL COLLATE NOCASE,
  path TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'ready',
  warnings_json BLOB NOT NULL DEFAULT '[]',
  discovery_json BLOB NOT NULL,
  scanned_at TEXT NOT NULL,
  PRIMARY KEY(environment_key, source_name),
  UNIQUE(environment_key, path)
);

CREATE INDEX IF NOT EXISTS environment_sources_by_path ON environment_sources(path, environment_key);

CREATE TABLE IF NOT EXISTS environment_bindings (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  service_name TEXT NOT NULL COLLATE NOCASE,
  provider TEXT NOT NULL,
  source_name TEXT NOT NULL DEFAULT '',
  config_json BLOB NOT NULL DEFAULT '{}',
  PRIMARY KEY(environment_key, service_name)
);

CREATE TABLE IF NOT EXISTS context_selections (
  path TEXT PRIMARY KEY,
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  selected_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS service_runtime (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  service_name TEXT NOT NULL COLLATE NOCASE,
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  generation INTEGER NOT NULL DEFAULT 0,
  pid INTEGER NOT NULL DEFAULT 0,
  upstream_port INTEGER NOT NULL DEFAULT 0,
  started_at TEXT,
  restart_count INTEGER NOT NULL DEFAULT 0,
  log_path TEXT NOT NULL DEFAULT '',
  private_run_key TEXT NOT NULL DEFAULT '',
  owner_instance_id TEXT NOT NULL DEFAULT '',
  supervisor_socket TEXT NOT NULL DEFAULT '',
  supervisor_state TEXT NOT NULL DEFAULT '',
  supervisor_pid INTEGER NOT NULL DEFAULT 0,
  container_name TEXT NOT NULL DEFAULT '',
  observed_at TEXT,
  PRIMARY KEY(environment_key, service_name)
);

CREATE TABLE IF NOT EXISTS daemon_instances (
  instance_id TEXT PRIMARY KEY,
  build_id TEXT NOT NULL,
  pid INTEGER NOT NULL,
  state TEXT NOT NULL,
  started_at TEXT NOT NULL,
  stopped_at TEXT
);

CREATE TABLE IF NOT EXISTS connection_runtime (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  source_name TEXT NOT NULL COLLATE NOCASE,
  target_name TEXT NOT NULL COLLATE NOCASE,
  protocol TEXT NOT NULL,
  source_generation INTEGER NOT NULL DEFAULT 0,
	listen_ip TEXT NOT NULL DEFAULT '127.0.0.1',
	dns_name TEXT NOT NULL DEFAULT '',
  listen_port INTEGER NOT NULL,
  owner_instance_id TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'planned',
  reason TEXT NOT NULL DEFAULT '',
  observed_at TEXT,
  PRIMARY KEY(environment_key, source_name, target_name)
);

CREATE TABLE IF NOT EXISTS network_allocations (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  endpoint_kind TEXT NOT NULL,
  source_name TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
  target_name TEXT NOT NULL COLLATE NOCASE,
  protocol TEXT NOT NULL,
  dns_name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  listen_ip TEXT NOT NULL UNIQUE,
  listen_port INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY(environment_key, endpoint_kind, source_name, target_name, protocol)
);

CREATE INDEX IF NOT EXISTS network_allocations_by_environment
ON network_allocations(environment_key, endpoint_kind, target_name, source_name);

CREATE TABLE IF NOT EXISTS operations (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  number INTEGER NOT NULL,
  type TEXT NOT NULL,
  state TEXT NOT NULL,
  actor TEXT NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  error TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(environment_key, number)
);

CREATE UNIQUE INDEX IF NOT EXISTS operations_idempotency ON operations(environment_key, idempotency_key) WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS operation_events (
  environment_key TEXT NOT NULL,
  operation_number INTEGER NOT NULL,
  sequence INTEGER NOT NULL,
  timestamp TEXT NOT NULL,
  type TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL,
  payload_json BLOB,
  PRIMARY KEY(environment_key, operation_number, sequence),
  FOREIGN KEY(environment_key, operation_number) REFERENCES operations(environment_key, number) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timeline_events (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  timestamp TEXT NOT NULL,
  actor TEXT NOT NULL,
  type TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  severity TEXT NOT NULL,
  summary TEXT NOT NULL,
  details_json BLOB,
  PRIMARY KEY(environment_key, sequence)
);

CREATE TABLE IF NOT EXISTS recordings (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  name TEXT NOT NULL COLLATE NOCASE,
  source TEXT NOT NULL DEFAULT '',
  target TEXT NOT NULL DEFAULT '',
  capture_bodies INTEGER NOT NULL DEFAULT 0,
  max_events INTEGER NOT NULL,
  max_body_bytes INTEGER NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  expires_at TEXT,
  event_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(environment_key, name)
);

CREATE UNIQUE INDEX IF NOT EXISTS recordings_one_active_per_environment ON recordings(environment_key) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS fault_rules (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  name TEXT NOT NULL COLLATE NOCASE,
  source TEXT NOT NULL,
  target TEXT NOT NULL,
  method TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  probability REAL NOT NULL DEFAULT 1,
  latency_ms INTEGER NOT NULL DEFAULT 0,
  jitter_ms INTEGER NOT NULL DEFAULT 0,
  status_code INTEGER NOT NULL DEFAULT 0,
  abort INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  expires_at TEXT,
  match_count INTEGER NOT NULL DEFAULT 0,
  revision INTEGER NOT NULL DEFAULT 1,
  scope_summary TEXT NOT NULL,
  PRIMARY KEY(environment_key, name)
);

CREATE TABLE IF NOT EXISTS traffic_events (
  environment_key TEXT NOT NULL REFERENCES environments(private_key) ON DELETE CASCADE,
  sequence INTEGER NOT NULL,
  recording_name TEXT NOT NULL,
  event_json BLOB NOT NULL,
  PRIMARY KEY(environment_key, sequence)
);

CREATE INDEX IF NOT EXISTS traffic_by_recording ON traffic_events(environment_key, recording_name, sequence);
`

func newPrivateKey() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := parseTime(value.String)
	return &parsed
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mapSQLError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
