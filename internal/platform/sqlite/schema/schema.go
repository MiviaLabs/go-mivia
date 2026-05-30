package schema

import (
	"context"
	"database/sql"
	"fmt"
)

const Component = "sqlite_app_config"
const Version = 7

var statements = []string{
	`CREATE TABLE IF NOT EXISTS app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		value_type TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS runtime_flags (
		key TEXT PRIMARY KEY,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		description TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS schema_versions (
		component TEXT PRIMARY KEY,
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS configured_projects (
		id TEXT PRIMARY KEY,
		graph_namespace TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		description TEXT NOT NULL,
		root_path TEXT NOT NULL,
		enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
		classification TEXT NOT NULL,
		digest_mode TEXT NOT NULL,
		update_policy TEXT NOT NULL,
		include_patterns TEXT NOT NULL,
		exclude_patterns TEXT NOT NULL,
		follow_symlinks INTEGER NOT NULL CHECK (follow_symlinks IN (0, 1)),
		validation_status TEXT NOT NULL,
		validation_error TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS project_digest_runs (
		run_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		started_at TEXT NOT NULL,
		finished_at TEXT NOT NULL,
		status TEXT NOT NULL,
		file_count INTEGER NOT NULL DEFAULT 0 CHECK (file_count >= 0),
		skipped_count INTEGER NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
		error_category TEXT NOT NULL,
		FOREIGN KEY(project_id) REFERENCES configured_projects(id)
	)`,
	`CREATE TABLE IF NOT EXISTS project_ingestion_runs (
		run_id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		trigger TEXT NOT NULL,
		mode TEXT NOT NULL,
		status TEXT NOT NULL,
		files_seen INTEGER NOT NULL DEFAULT 0 CHECK (files_seen >= 0),
		files_ingested INTEGER NOT NULL DEFAULT 0 CHECK (files_ingested >= 0),
		files_skipped INTEGER NOT NULL DEFAULT 0 CHECK (files_skipped >= 0),
		chunks_stored INTEGER NOT NULL DEFAULT 0 CHECK (chunks_stored >= 0),
		symbols_stored INTEGER NOT NULL DEFAULT 0 CHECK (symbols_stored >= 0),
		error_category TEXT NOT NULL,
		started_at TEXT NOT NULL,
		finished_at TEXT NOT NULL,
		FOREIGN KEY(project_id) REFERENCES configured_projects(id)
	)`,
	`CREATE TABLE IF NOT EXISTS project_ingestion_run_reason_counts (
		project_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		reason TEXT NOT NULL,
		count INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0),
		PRIMARY KEY(project_id, run_id, reason),
		FOREIGN KEY(run_id) REFERENCES project_ingestion_runs(run_id)
	)`,
	`CREATE TABLE IF NOT EXISTS project_file_ingestion_state (
		project_id TEXT NOT NULL,
		relative_path_hash TEXT NOT NULL,
		relative_path TEXT NOT NULL DEFAULT '',
		relative_path_safe INTEGER NOT NULL CHECK (relative_path_safe IN (0, 1)),
		status TEXT NOT NULL,
		present INTEGER NOT NULL CHECK (present IN (0, 1)),
		content_sha256 TEXT NOT NULL DEFAULT '',
		size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
		modified_at TEXT NOT NULL DEFAULT '',
		last_event_at TEXT NOT NULL DEFAULT '',
		last_ingested_at TEXT NOT NULL DEFAULT '',
		skipped_reason TEXT NOT NULL DEFAULT '',
		PRIMARY KEY(project_id, relative_path_hash),
		FOREIGN KEY(project_id) REFERENCES configured_projects(id),
		CHECK (relative_path_safe = 1 OR (relative_path = '' AND content_sha256 = ''))
	)`,
	`CREATE TABLE IF NOT EXISTS project_watch_state (
		project_id TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		watched_directory_count INTEGER NOT NULL DEFAULT 0 CHECK (watched_directory_count >= 0),
		queue_depth INTEGER NOT NULL DEFAULT 0 CHECK (queue_depth >= 0),
		last_error_category TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL,
		FOREIGN KEY(project_id) REFERENCES configured_projects(id)
	)`,
	`CREATE TABLE IF NOT EXISTS project_extractor_cache (
		project_id TEXT NOT NULL,
		relative_path_hash TEXT NOT NULL,
		content_sha256 TEXT NOT NULL,
		extractor_name TEXT NOT NULL,
		extractor_version TEXT NOT NULL,
		symbols_json TEXT NOT NULL DEFAULT '[]',
		headings_json TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY(project_id, relative_path_hash, content_sha256, extractor_name, extractor_version),
		FOREIGN KEY(project_id) REFERENCES configured_projects(id),
		CHECK (content_sha256 != ''),
		CHECK (extractor_name != ''),
		CHECK (extractor_version != '')
	)`,
	`CREATE INDEX IF NOT EXISTS idx_project_file_ingestion_state_project_status_path
		ON project_file_ingestion_state(project_id, status, relative_path_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_project_file_ingestion_state_project_hash
		ON project_file_ingestion_state(project_id, relative_path_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_project_file_ingestion_state_project_present_reason
		ON project_file_ingestion_state(project_id, present, skipped_reason, relative_path_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_project_file_ingestion_state_project_relative_path
		ON project_file_ingestion_state(project_id, relative_path_safe, relative_path)`,
	`CREATE INDEX IF NOT EXISTS idx_project_file_ingestion_state_project_modified
		ON project_file_ingestion_state(project_id, modified_at, relative_path_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_project_extractor_cache_project_file
		ON project_extractor_cache(project_id, relative_path_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_project_extractor_cache_project_extractor
		ON project_extractor_cache(project_id, extractor_name, extractor_version)`,
}

const versionStatement = `INSERT INTO schema_versions (component, version)
	 VALUES (?, ?)
	 ON CONFLICT(component) DO UPDATE SET version = excluded.version`

var configuredProjectColumns = []columnDefinition{
	{
		Name:       "max_file_bytes",
		Definition: "max_file_bytes INTEGER NOT NULL DEFAULT 0 CHECK (max_file_bytes >= 0)",
	},
	{
		Name:       "max_chunk_bytes",
		Definition: "max_chunk_bytes INTEGER NOT NULL DEFAULT 0 CHECK (max_chunk_bytes >= 0)",
	},
	{
		Name:       "sensitive_marker_policy",
		Definition: "sensitive_marker_policy TEXT NOT NULL DEFAULT 'skip_file'",
	},
	{
		Name:       "graph_storage",
		Definition: "graph_storage TEXT NOT NULL DEFAULT 'persistent'",
	},
}

type columnDefinition struct {
	Name       string
	Definition string
}

func Bootstrap(ctx context.Context, db *sql.DB) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("bootstrap sqlite app-config schema: %w", err)
		}
	}
	if err := ensureColumns(ctx, db, "configured_projects", configuredProjectColumns); err != nil {
		return fmt.Errorf("bootstrap sqlite app-config schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, versionStatement, Component, Version); err != nil {
		return fmt.Errorf("bootstrap sqlite app-config schema: %w", err)
	}
	return nil
}

func ensureColumns(ctx context.Context, db *sql.DB, table string, columns []columnDefinition) error {
	existing, err := existingColumns(ctx, db, table)
	if err != nil {
		return err
	}
	for _, column := range columns {
		if existing[column.Name] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, column.Definition)); err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, column.Name, err)
		}
	}
	return nil
}

func existingColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("inspect columns for %s: %w", table, err)
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name string
		var columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("scan columns for %s: %w", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect columns for %s: %w", table, err)
	}
	return columns, nil
}
