package projectingestion

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrRunNotFound = errors.New("ingestion run not found")

type FileStateFilter struct {
	Status FileStatus
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (store *SQLiteStore) SaveRun(ctx context.Context, run Run) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO project_ingestion_runs (
		run_id,
		project_id,
		trigger,
		mode,
		status,
		files_seen,
		files_ingested,
		files_skipped,
		chunks_stored,
		symbols_stored,
		error_category,
		started_at,
		finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(run_id) DO UPDATE SET
		project_id = excluded.project_id,
		trigger = excluded.trigger,
		mode = excluded.mode,
		status = excluded.status,
		files_seen = excluded.files_seen,
		files_ingested = excluded.files_ingested,
		files_skipped = excluded.files_skipped,
		chunks_stored = excluded.chunks_stored,
		symbols_stored = excluded.symbols_stored,
		error_category = excluded.error_category,
		started_at = excluded.started_at,
		finished_at = excluded.finished_at`,
		run.ID,
		run.ProjectID,
		string(run.Trigger),
		run.Mode,
		string(run.Status),
		run.FilesSeen,
		run.FilesIngested,
		run.FilesSkipped,
		run.ChunksStored,
		run.SymbolsStored,
		run.ErrorCategory,
		formatTime(run.StartedAt),
		formatTime(run.FinishedAt),
	)
	return err
}

func (store *SQLiteStore) GetRun(ctx context.Context, projectID string, runID string) (Run, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
		run_id,
		project_id,
		trigger,
		mode,
		status,
		files_seen,
		files_ingested,
		files_skipped,
		chunks_stored,
		symbols_stored,
		error_category,
		started_at,
		finished_at
	FROM project_ingestion_runs
	WHERE project_id = ? AND run_id = ?`, projectID, runID)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	return run, err
}

func (store *SQLiteStore) SaveFileState(ctx context.Context, state FileState) error {
	relativePath := state.RelativePath
	contentSHA256 := state.ContentSHA256
	if !state.RelativePathSafe {
		relativePath = ""
		contentSHA256 = ""
	}
	if state.Status != FileStatusEligible {
		contentSHA256 = ""
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO project_file_ingestion_state (
		project_id,
		relative_path_hash,
		relative_path,
		relative_path_safe,
		status,
		present,
		content_sha256,
		size_bytes,
		modified_at,
		last_event_at,
		last_ingested_at,
		skipped_reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_id, relative_path_hash) DO UPDATE SET
		relative_path = excluded.relative_path,
		relative_path_safe = excluded.relative_path_safe,
		status = excluded.status,
		present = excluded.present,
		content_sha256 = excluded.content_sha256,
		size_bytes = excluded.size_bytes,
		modified_at = excluded.modified_at,
		last_event_at = excluded.last_event_at,
		last_ingested_at = excluded.last_ingested_at,
		skipped_reason = excluded.skipped_reason`,
		state.ProjectID,
		state.RelativePathHash,
		relativePath,
		boolToInt(state.RelativePathSafe),
		string(state.Status),
		boolToInt(state.Present),
		contentSHA256,
		state.SizeBytes,
		formatTime(state.ModifiedAt),
		formatTime(state.LastEventAt),
		formatTime(state.LastIngestedAt),
		string(state.SkippedReason),
	)
	return err
}

func (store *SQLiteStore) ListFileStates(ctx context.Context, projectID string, filter FileStateFilter) ([]FileState, error) {
	query := `SELECT
		project_id,
		relative_path_hash,
		relative_path,
		relative_path_safe,
		status,
		present,
		content_sha256,
		size_bytes,
		modified_at,
		last_event_at,
		last_ingested_at,
		skipped_reason
	FROM project_file_ingestion_state
	WHERE project_id = ?`
	args := []any{projectID}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, string(filter.Status))
	}
	query += ` ORDER BY relative_path_hash`

	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []FileState
	for rows.Next() {
		state, err := scanFileState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

type runScanner interface {
	Scan(dest ...any) error
}

func scanRun(scanner runScanner) (Run, error) {
	var run Run
	var trigger string
	var status string
	var startedAt string
	var finishedAt string
	err := scanner.Scan(
		&run.ID,
		&run.ProjectID,
		&trigger,
		&run.Mode,
		&status,
		&run.FilesSeen,
		&run.FilesIngested,
		&run.FilesSkipped,
		&run.ChunksStored,
		&run.SymbolsStored,
		&run.ErrorCategory,
		&startedAt,
		&finishedAt,
	)
	if err != nil {
		return Run{}, err
	}
	run.Trigger = Trigger(trigger)
	run.Status = RunStatus(status)
	run.StartedAt, err = parseOptionalTime(startedAt)
	if err != nil {
		return Run{}, err
	}
	run.FinishedAt, err = parseOptionalTime(finishedAt)
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

type fileStateScanner interface {
	Scan(dest ...any) error
}

func scanFileState(scanner fileStateScanner) (FileState, error) {
	var state FileState
	var relativePathSafe int
	var present int
	var status string
	var modifiedAt string
	var lastEventAt string
	var lastIngestedAt string
	var skippedReason string
	err := scanner.Scan(
		&state.ProjectID,
		&state.RelativePathHash,
		&state.RelativePath,
		&relativePathSafe,
		&status,
		&present,
		&state.ContentSHA256,
		&state.SizeBytes,
		&modifiedAt,
		&lastEventAt,
		&lastIngestedAt,
		&skippedReason,
	)
	if err != nil {
		return FileState{}, err
	}
	state.RelativePathSafe = relativePathSafe == 1
	state.Status = FileStatus(status)
	state.Present = present == 1
	var parseErr error
	if state.ModifiedAt, parseErr = parseOptionalTime(modifiedAt); parseErr != nil {
		return FileState{}, parseErr
	}
	if state.LastEventAt, parseErr = parseOptionalTime(lastEventAt); parseErr != nil {
		return FileState{}, parseErr
	}
	if state.LastIngestedAt, parseErr = parseOptionalTime(lastIngestedAt); parseErr != nil {
		return FileState{}, parseErr
	}
	state.SkippedReason = SkipReason(skippedReason)
	return state, nil
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
