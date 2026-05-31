package projectingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"
)

var ErrRunNotFound = errors.New("ingestion run not found")
var ErrExtractorCacheMiss = errors.New("extractor cache miss")

type FileStateFilter struct {
	Status        FileStatus
	Extension     string
	PathPrefix    string
	SkippedReason SkipReason
	Present       *bool
	ModifiedSince time.Time
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

type ExtractorCacheEntry struct {
	ProjectID        string
	RelativePathHash string
	ContentSHA256    string
	ExtractorName    string
	ExtractorVersion string
	Symbols          []Symbol
	Headings         []Heading
	References       []Reference
	Calls            []Call
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (store *SQLiteStore) SaveRun(ctx context.Context, run Run) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO project_ingestion_runs (
		run_id,
		project_id,
		trigger,
		mode,
		status,
		files_seen,
		files_ingested,
		files_skipped,
		files_unchanged,
		chunks_stored,
		symbols_stored,
		error_category,
		current_phase,
		started_at,
		finished_at,
		heartbeat_at,
		last_progress_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(run_id) DO UPDATE SET
		project_id = excluded.project_id,
		trigger = excluded.trigger,
		mode = excluded.mode,
		status = excluded.status,
		files_seen = excluded.files_seen,
		files_ingested = excluded.files_ingested,
		files_skipped = excluded.files_skipped,
		files_unchanged = excluded.files_unchanged,
		chunks_stored = excluded.chunks_stored,
		symbols_stored = excluded.symbols_stored,
		error_category = excluded.error_category,
		current_phase = excluded.current_phase,
		started_at = excluded.started_at,
		finished_at = excluded.finished_at,
		heartbeat_at = excluded.heartbeat_at,
		last_progress_at = excluded.last_progress_at`,
		run.ID,
		run.ProjectID,
		string(run.Trigger),
		run.Mode,
		string(run.Status),
		run.FilesSeen,
		run.FilesIngested,
		run.FilesSkipped,
		run.FilesUnchanged,
		run.ChunksStored,
		run.SymbolsStored,
		run.ErrorCategory,
		run.CurrentPhase,
		formatTime(run.StartedAt),
		formatTime(run.FinishedAt),
		formatTime(run.HeartbeatAt),
		formatTime(run.LastProgressAt),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_ingestion_run_reason_counts WHERE project_id = ? AND run_id = ?`, run.ProjectID, run.ID); err != nil {
		return err
	}
	for reason, count := range run.ReasonCounts {
		if strings.TrimSpace(reason) == "" || count <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_ingestion_run_reason_counts (
			project_id,
			run_id,
			reason,
			count
		) VALUES (?, ?, ?, ?)`, run.ProjectID, run.ID, reason, count); err != nil {
			return err
		}
	}
	return tx.Commit()
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
		files_unchanged,
		chunks_stored,
		symbols_stored,
		error_category,
		current_phase,
		started_at,
		finished_at,
		heartbeat_at,
		last_progress_at
	FROM project_ingestion_runs
	WHERE project_id = ? AND run_id = ?`, projectID, runID)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	if err != nil {
		return Run{}, err
	}
	counts, err := store.loadRunReasonCounts(ctx, projectID, runID)
	if err != nil {
		return Run{}, err
	}
	run.ReasonCounts = counts
	return run, nil
}

func (store *SQLiteStore) ListLatestRuns(ctx context.Context, projectID string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := store.db.QueryContext(ctx, `SELECT
		run_id,
		project_id,
		trigger,
		mode,
		status,
		files_seen,
		files_ingested,
		files_skipped,
		files_unchanged,
		chunks_stored,
		symbols_stored,
		error_category,
		current_phase,
		started_at,
		finished_at,
		heartbeat_at,
		last_progress_at
	FROM project_ingestion_runs
	WHERE project_id = ?
	ORDER BY started_at DESC, run_id DESC
	LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range runs {
		counts, err := store.loadRunReasonCounts(ctx, projectID, runs[i].ID)
		if err != nil {
			return nil, err
		}
		runs[i].ReasonCounts = counts
	}
	return runs, nil
}

func (store *SQLiteStore) ListActiveRuns(ctx context.Context, projectID string) ([]Run, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT
		run_id,
		project_id,
		trigger,
		mode,
		status,
		files_seen,
		files_ingested,
		files_skipped,
		files_unchanged,
		chunks_stored,
		symbols_stored,
		error_category,
		current_phase,
		started_at,
		finished_at,
		heartbeat_at,
		last_progress_at
	FROM project_ingestion_runs
	WHERE project_id = ?
		AND status IN (?, ?)
	ORDER BY started_at ASC, run_id ASC`, projectID, string(RunStatusPending), string(RunStatusRunning))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range runs {
		counts, err := store.loadRunReasonCounts(ctx, projectID, runs[i].ID)
		if err != nil {
			return nil, err
		}
		runs[i].ReasonCounts = counts
	}
	return runs, nil
}

func (store *SQLiteStore) FailActiveRuns(ctx context.Context, projectID string, errorCategory string, finishedAt time.Time) (int, error) {
	result, err := store.db.ExecContext(ctx, `UPDATE project_ingestion_runs
	SET status = ?,
		error_category = ?,
		current_phase = ?,
		finished_at = ?,
		heartbeat_at = ?,
		last_progress_at = ?
	WHERE project_id = ?
		AND status IN (?, ?)`,
		string(RunStatusFailed),
		errorCategory,
		"interrupted",
		formatTime(finishedAt),
		formatTime(finishedAt),
		formatTime(finishedAt),
		projectID,
		string(RunStatusPending),
		string(RunStatusRunning),
	)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

func (store *SQLiteStore) loadRunReasonCounts(ctx context.Context, projectID string, runID string) (map[string]int, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT reason, count
	FROM project_ingestion_run_reason_counts
	WHERE project_id = ? AND run_id = ?
	ORDER BY reason`, projectID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var reason string
		var count int
		if err := rows.Scan(&reason, &count); err != nil {
			return nil, err
		}
		if count > 0 {
			counts[reason] = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(counts) == 0 {
		return nil, nil
	}
	return counts, nil
}

func (store *SQLiteStore) SaveFileState(ctx context.Context, state FileState) error {
	return saveFileState(ctx, store.db, state)
}

func (store *SQLiteStore) SaveFileStatesBatch(ctx context.Context, states []FileState) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, state := range states {
		if err := saveFileState(ctx, tx, state); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type fileStateExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func saveFileState(ctx context.Context, exec fileStateExecutor, state FileState) error {
	relativePath := state.RelativePath
	contentSHA256 := state.ContentSHA256
	if !state.RelativePathSafe {
		relativePath = ""
		contentSHA256 = ""
	}
	if state.Status != FileStatusEligible {
		contentSHA256 = ""
	}
	extension := strings.ToLower(path.Ext(relativePath))
	_, err := exec.ExecContext(ctx, `INSERT INTO project_file_ingestion_state (
		project_id,
		relative_path_hash,
		relative_path,
		relative_path_safe,
		status,
		present,
		content_sha256,
		extension,
		size_bytes,
		modified_at,
		last_event_at,
		last_ingested_at,
		skipped_reason
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_id, relative_path_hash) DO UPDATE SET
		relative_path = excluded.relative_path,
		relative_path_safe = excluded.relative_path_safe,
		status = excluded.status,
		present = excluded.present,
		content_sha256 = excluded.content_sha256,
		extension = excluded.extension,
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
		extension,
		state.SizeBytes,
		formatTime(state.ModifiedAt),
		formatTime(state.LastEventAt),
		formatTime(state.LastIngestedAt),
		string(state.SkippedReason),
	)
	return err
}

func (store *SQLiteStore) GetExtractorCache(ctx context.Context, projectID string, relativePathHash string, contentSHA256 string, extractorName string, extractorVersion string) (ExtractorCacheEntry, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
		project_id,
		relative_path_hash,
		content_sha256,
		extractor_name,
		extractor_version,
		symbols_json,
		headings_json,
		references_json,
		calls_json,
		created_at,
		updated_at
	FROM project_extractor_cache
	WHERE project_id = ?
		AND relative_path_hash = ?
		AND content_sha256 = ?
		AND extractor_name = ?
		AND extractor_version = ?`,
		projectID,
		relativePathHash,
		contentSHA256,
		extractorName,
		extractorVersion,
	)
	entry, err := scanExtractorCacheEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExtractorCacheEntry{}, ErrExtractorCacheMiss
	}
	return entry, err
}

func (store *SQLiteStore) SaveExtractorCache(ctx context.Context, entry ExtractorCacheEntry) error {
	if entry.ProjectID == "" || entry.RelativePathHash == "" || entry.ContentSHA256 == "" || entry.ExtractorName == "" || entry.ExtractorVersion == "" {
		return ErrInvalidInput
	}
	symbolsJSON, err := json.Marshal(entry.Symbols)
	if err != nil {
		return err
	}
	headingsJSON, err := json.Marshal(entry.Headings)
	if err != nil {
		return err
	}
	referencesJSON, err := json.Marshal(entry.References)
	if err != nil {
		return err
	}
	callsJSON, err := json.Marshal(entry.Calls)
	if err != nil {
		return err
	}
	createdAt := entry.CreatedAt
	if createdAt.IsZero() {
		createdAt = entry.UpdatedAt
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := entry.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO project_extractor_cache (
		project_id,
		relative_path_hash,
		content_sha256,
		extractor_name,
		extractor_version,
		symbols_json,
		headings_json,
		references_json,
		calls_json,
		created_at,
		updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_id, relative_path_hash, content_sha256, extractor_name, extractor_version) DO UPDATE SET
		symbols_json = excluded.symbols_json,
		headings_json = excluded.headings_json,
		references_json = excluded.references_json,
		calls_json = excluded.calls_json,
		updated_at = excluded.updated_at`,
		entry.ProjectID,
		entry.RelativePathHash,
		entry.ContentSHA256,
		entry.ExtractorName,
		entry.ExtractorVersion,
		string(symbolsJSON),
		string(headingsJSON),
		string(referencesJSON),
		string(callsJSON),
		formatTime(createdAt),
		formatTime(updatedAt),
	)
	return err
}

func (store *SQLiteStore) DeleteExtractorCacheForFile(ctx context.Context, projectID string, relativePathHash string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM project_extractor_cache WHERE project_id = ? AND relative_path_hash = ?`, projectID, relativePathHash)
	return err
}

func (store *SQLiteStore) GetFileStateByHash(ctx context.Context, projectID string, relativePathHash string) (FileState, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
		project_id,
		relative_path_hash,
		relative_path,
		relative_path_safe,
		status,
		present,
		content_sha256,
		extension,
		size_bytes,
		modified_at,
		last_event_at,
		last_ingested_at,
		skipped_reason
	FROM project_file_ingestion_state
	WHERE project_id = ? AND relative_path_hash = ?`, projectID, relativePathHash)
	state, err := scanFileState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FileState{}, ErrIngestionNotFound
	}
	return state, err
}

func (store *SQLiteStore) ListFileStates(ctx context.Context, projectID string, filter FileStateFilter) ([]FileState, error) {
	rows, err := store.queryFileStates(ctx, projectID, filter, 0, 0)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileStates(rows)
}

func (store *SQLiteStore) ListFileStatesPage(ctx context.Context, projectID string, filter FileStateFilter, pagination Pagination) ([]FileState, string, error) {
	pageSize, offset, err := paginationWindow(pagination)
	if err != nil {
		return nil, "", err
	}
	rows, err := store.queryFileStates(ctx, projectID, filter, pageSize+1, offset)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	states, err := scanFileStates(rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(states) > pageSize {
		states = states[:pageSize]
		next = strconv.Itoa(offset + pageSize)
	}
	return states, next, nil
}

func (store *SQLiteStore) queryFileStates(ctx context.Context, projectID string, filter FileStateFilter, limit int, offset int) (*sql.Rows, error) {
	query := `SELECT
		project_id,
		relative_path_hash,
		relative_path,
		relative_path_safe,
		status,
		present,
		content_sha256,
		extension,
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
	if filter.Extension != "" {
		query += ` AND extension = ?`
		args = append(args, strings.ToLower(filter.Extension))
	}
	if filter.PathPrefix != "" {
		query += ` AND relative_path_safe = 1 AND relative_path LIKE ?`
		args = append(args, filter.PathPrefix+"%")
	}
	if filter.SkippedReason != "" {
		query += ` AND skipped_reason = ?`
		args = append(args, string(filter.SkippedReason))
	}
	if filter.Present != nil {
		query += ` AND present = ?`
		args = append(args, boolToInt(*filter.Present))
	}
	if !filter.ModifiedSince.IsZero() {
		query += ` AND modified_at >= ?`
		args = append(args, formatTime(filter.ModifiedSince))
	}
	query += ` ORDER BY relative_path_hash`
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	return store.db.QueryContext(ctx, query, args...)
}

func scanFileStates(rows *sql.Rows) ([]FileState, error) {
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
	var heartbeatAt string
	var lastProgressAt string
	err := scanner.Scan(
		&run.ID,
		&run.ProjectID,
		&trigger,
		&run.Mode,
		&status,
		&run.FilesSeen,
		&run.FilesIngested,
		&run.FilesSkipped,
		&run.FilesUnchanged,
		&run.ChunksStored,
		&run.SymbolsStored,
		&run.ErrorCategory,
		&run.CurrentPhase,
		&startedAt,
		&finishedAt,
		&heartbeatAt,
		&lastProgressAt,
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
	run.HeartbeatAt, err = parseOptionalTime(heartbeatAt)
	if err != nil {
		return Run{}, err
	}
	run.LastProgressAt, err = parseOptionalTime(lastProgressAt)
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
	var extension string
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
		&extension,
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

func scanExtractorCacheEntry(scanner runScanner) (ExtractorCacheEntry, error) {
	var entry ExtractorCacheEntry
	var symbolsJSON string
	var headingsJSON string
	var referencesJSON string
	var callsJSON string
	var createdAt string
	var updatedAt string
	err := scanner.Scan(
		&entry.ProjectID,
		&entry.RelativePathHash,
		&entry.ContentSHA256,
		&entry.ExtractorName,
		&entry.ExtractorVersion,
		&symbolsJSON,
		&headingsJSON,
		&referencesJSON,
		&callsJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return ExtractorCacheEntry{}, err
	}
	if err := json.Unmarshal([]byte(symbolsJSON), &entry.Symbols); err != nil {
		return ExtractorCacheEntry{}, err
	}
	if err := json.Unmarshal([]byte(headingsJSON), &entry.Headings); err != nil {
		return ExtractorCacheEntry{}, err
	}
	if err := json.Unmarshal([]byte(referencesJSON), &entry.References); err != nil {
		return ExtractorCacheEntry{}, err
	}
	if err := json.Unmarshal([]byte(callsJSON), &entry.Calls); err != nil {
		return ExtractorCacheEntry{}, err
	}
	var parseErr error
	if entry.CreatedAt, parseErr = parseOptionalTime(createdAt); parseErr != nil {
		return ExtractorCacheEntry{}, parseErr
	}
	if entry.UpdatedAt, parseErr = parseOptionalTime(updatedAt); parseErr != nil {
		return ExtractorCacheEntry{}, parseErr
	}
	return entry, nil
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
