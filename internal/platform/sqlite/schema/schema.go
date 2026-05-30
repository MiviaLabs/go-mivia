package schema

import (
	"context"
	"database/sql"
	"fmt"
)

const Component = "sqlite_app_config"
const Version = 1

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
	`INSERT INTO schema_versions (component, version)
	 VALUES ('sqlite_app_config', 1)
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
