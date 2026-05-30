package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

var ErrNotFound = errors.New("project not found")

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (store *SQLiteStore) SaveProjects(ctx context.Context, projects []projectregistry.Project) error {
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, project := range projects {
		includePatterns, err := json.Marshal(project.Include)
		if err != nil {
			return err
		}
		excludePatterns, err := json.Marshal(project.Exclude)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO configured_projects (
			id,
			graph_namespace,
			display_name,
			description,
			root_path,
			enabled,
			classification,
			digest_mode,
			update_policy,
			include_patterns,
			exclude_patterns,
			follow_symlinks,
			max_file_bytes,
			max_chunk_bytes,
			sensitive_marker_policy,
			validation_status,
			validation_error,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			graph_namespace = excluded.graph_namespace,
			display_name = excluded.display_name,
			description = excluded.description,
			root_path = excluded.root_path,
			enabled = excluded.enabled,
			classification = excluded.classification,
			digest_mode = excluded.digest_mode,
			update_policy = excluded.update_policy,
			include_patterns = excluded.include_patterns,
			exclude_patterns = excluded.exclude_patterns,
			follow_symlinks = excluded.follow_symlinks,
			max_file_bytes = excluded.max_file_bytes,
			max_chunk_bytes = excluded.max_chunk_bytes,
			sensitive_marker_policy = excluded.sensitive_marker_policy,
			validation_status = excluded.validation_status,
			validation_error = excluded.validation_error,
			updated_at = excluded.updated_at`,
			project.ID,
			project.GraphNamespace,
			project.DisplayName,
			project.Description,
			project.RootPath,
			boolToInt(project.Enabled),
			project.Classification,
			project.DigestMode,
			project.UpdatePolicy,
			string(includePatterns),
			string(excludePatterns),
			boolToInt(project.FollowSymlinks),
			project.MaxFileBytes,
			project.MaxChunkBytes,
			project.SensitiveMarkerPolicy,
			project.ValidationStatus,
			project.ValidationError,
			updatedAt,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (store *SQLiteStore) GetProject(ctx context.Context, id string) (projectregistry.Project, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
		id,
		graph_namespace,
		display_name,
		description,
		root_path,
		enabled,
		classification,
		digest_mode,
		update_policy,
		include_patterns,
		exclude_patterns,
		follow_symlinks,
		max_file_bytes,
		max_chunk_bytes,
		sensitive_marker_policy,
		validation_status,
		validation_error
	FROM configured_projects WHERE id = ?`, id)

	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return projectregistry.Project{}, ErrNotFound
	}
	return project, err
}

func (store *SQLiteStore) ListProjects(ctx context.Context) ([]projectregistry.Project, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT
		id,
		graph_namespace,
		display_name,
		description,
		root_path,
		enabled,
		classification,
		digest_mode,
		update_policy,
		include_patterns,
		exclude_patterns,
		follow_symlinks,
		max_file_bytes,
		max_chunk_bytes,
		sensitive_marker_policy,
		validation_status,
		validation_error
	FROM configured_projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]projectregistry.Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

type projectScanner interface {
	Scan(dest ...any) error
}

func scanProject(scanner projectScanner) (projectregistry.Project, error) {
	var project projectregistry.Project
	var enabled int
	var followSymlinks int
	var includePatterns string
	var excludePatterns string
	err := scanner.Scan(
		&project.ID,
		&project.GraphNamespace,
		&project.DisplayName,
		&project.Description,
		&project.RootPath,
		&enabled,
		&project.Classification,
		&project.DigestMode,
		&project.UpdatePolicy,
		&includePatterns,
		&excludePatterns,
		&followSymlinks,
		&project.MaxFileBytes,
		&project.MaxChunkBytes,
		&project.SensitiveMarkerPolicy,
		&project.ValidationStatus,
		&project.ValidationError,
	)
	if err != nil {
		return projectregistry.Project{}, err
	}
	if err := json.Unmarshal([]byte(includePatterns), &project.Include); err != nil {
		return projectregistry.Project{}, err
	}
	if err := json.Unmarshal([]byte(excludePatterns), &project.Exclude); err != nil {
		return projectregistry.Project{}, err
	}
	project.Enabled = enabled == 1
	project.FollowSymlinks = followSymlinks == 1
	return project, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
