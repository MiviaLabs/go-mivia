package schema_test

import (
	"context"
	"database/sql"
	"testing"

	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
)

func TestBootstrap_Idempotent(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	var version int
	if err := db.SQLDB().QueryRowContext(context.Background(), `SELECT version FROM schema_versions WHERE component = ?`, schema.Component).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != schema.Version {
		t.Fatalf("expected version %d, got %d", schema.Version, version)
	}
}

func TestBootstrap_ProjectRegistryTablesExist(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}

	for _, table := range []string{"configured_projects", "project_digest_runs"} {
		assertTable(t, db.SQLDB(), table)
	}
}

func TestBootstrap_IngestionTablesExist(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}

	for _, table := range []string{
		"project_ingestion_runs",
		"project_ingestion_run_reason_counts",
		"project_file_ingestion_state",
		"project_watch_state",
		"project_extractor_cache",
	} {
		assertTable(t, db.SQLDB(), table)
	}
}

func TestBootstrap_SearchIndexTablesExist(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}

	for _, table := range []string{
		"project_search_index_state",
		"project_search_chunks_fts",
		"project_search_files_fts",
		"project_search_symbols_fts",
		"project_search_references_fts",
		"project_search_calls_fts",
	} {
		assertTable(t, db.SQLDB(), table)
	}
}

func TestBootstrap_ConfiguredProjectIngestionColumnsExist(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}

	assertColumn(t, db.SQLDB(), "configured_projects", "max_file_bytes")
	assertColumn(t, db.SQLDB(), "configured_projects", "max_chunk_bytes")
	assertColumn(t, db.SQLDB(), "configured_projects", "sensitive_marker_policy")
	assertColumn(t, db.SQLDB(), "configured_projects", "graph_storage")
}

func TestBootstrap_UpgradesLegacyConfiguredProjectsWithIngestionColumns(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.SQLDB().ExecContext(context.Background(), `CREATE TABLE configured_projects (
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
	)`); err != nil {
		t.Fatalf("create legacy configured_projects: %v", err)
	}

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}

	assertColumn(t, db.SQLDB(), "configured_projects", "max_file_bytes")
	assertColumn(t, db.SQLDB(), "configured_projects", "max_chunk_bytes")
	assertColumn(t, db.SQLDB(), "configured_projects", "sensitive_marker_policy")
	assertColumn(t, db.SQLDB(), "configured_projects", "graph_storage")
}

func TestBootstrap_ProjectFileIngestionStateRejectsUnsafePathLeak(t *testing.T) {
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if err := schema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}

	_, err = db.SQLDB().ExecContext(context.Background(), `INSERT INTO project_file_ingestion_state (
		project_id,
		relative_path_hash,
		relative_path,
		relative_path_safe,
		status,
		present,
		content_sha256
	) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"project-1",
		"hash-1",
		"blocked/path",
		0,
		"skipped",
		1,
		"content-hash",
	)
	if err == nil {
		t.Fatal("expected unsafe path row with path/hash values to fail")
	}

	if _, err := db.SQLDB().ExecContext(context.Background(), `INSERT INTO project_file_ingestion_state (
		project_id,
		relative_path_hash,
		relative_path_safe,
		status,
		present,
		skipped_reason
	) VALUES (?, ?, ?, ?, ?, ?)`,
		"project-1",
		"hash-2",
		0,
		"skipped",
		1,
		"sensitive_path",
	); err != nil {
		t.Fatalf("expected hash-only unsafe path row to pass: %v", err)
	}
}

func assertTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var name string
	err := db.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err != nil {
		t.Fatalf("expected table %s: %v", table, err)
	}
	if name != table {
		t.Fatalf("expected table %s, got %s", table, name)
	}
}

func assertColumn(t *testing.T, db *sql.DB, table string, column string) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		t.Fatalf("inspect column %s.%s: %v", table, column, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("expected column %s.%s", table, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("inspect column %s.%s: %v", table, column, err)
	}
}
