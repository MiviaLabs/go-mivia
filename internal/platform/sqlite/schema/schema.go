package schema

import (
	"context"
	"database/sql"
	"fmt"
)

const Component = "sqlite_app_config"
const Version = 2

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
	`INSERT INTO schema_versions (component, version)
	 VALUES ('sqlite_app_config', 2)
	 ON CONFLICT(component) DO UPDATE SET version = excluded.version`,
}

func Bootstrap(ctx context.Context, db *sql.DB) error {
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("bootstrap sqlite app-config schema: %w", err)
		}
	}
	return nil
}
