package projectintegrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (store *SQLiteStore) UpsertSource(ctx context.Context, input SourceMetadataInput) (SourceMetadata, error) {
	source, err := sourceFromInput(input)
	if err != nil {
		return SourceMetadata{}, err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO project_integration_sources (
		project_id,
		provider,
		site_url_hash,
		cloud_id_hash,
		allowlist_hash,
		allowlist_count,
		auth_mode,
		ingestion_enabled,
		initial_full_sync,
		incremental_interval_ms,
		empty_poll_sleep_ms,
		max_idle_sleep_ms,
		overlap_window_ms,
		initial_page_size,
		incremental_page_size,
		max_results,
		updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_id, provider) DO UPDATE SET
		site_url_hash = excluded.site_url_hash,
		cloud_id_hash = excluded.cloud_id_hash,
		allowlist_hash = excluded.allowlist_hash,
		allowlist_count = excluded.allowlist_count,
		auth_mode = excluded.auth_mode,
		ingestion_enabled = excluded.ingestion_enabled,
		initial_full_sync = excluded.initial_full_sync,
		incremental_interval_ms = excluded.incremental_interval_ms,
		empty_poll_sleep_ms = excluded.empty_poll_sleep_ms,
		max_idle_sleep_ms = excluded.max_idle_sleep_ms,
		overlap_window_ms = excluded.overlap_window_ms,
		initial_page_size = excluded.initial_page_size,
		incremental_page_size = excluded.incremental_page_size,
		max_results = excluded.max_results,
		updated_at = excluded.updated_at`,
		source.ProjectID,
		string(source.Provider),
		source.SiteURLHash,
		source.CloudIDHash,
		source.AllowlistHash,
		source.AllowlistCount,
		source.AuthMode,
		boolToInt(source.IngestionEnabled),
		source.InitialFullSync,
		durationMillis(source.IncrementalInterval),
		durationMillis(source.EmptyPollSleep),
		durationMillis(source.MaxIdleSleep),
		durationMillis(source.OverlapWindow),
		source.InitialPageSize,
		source.IncrementalPageSize,
		source.MaxResults,
		formatTime(source.UpdatedAt),
	)
	if err != nil {
		return SourceMetadata{}, err
	}
	return source, nil
}

func (store *SQLiteStore) ListSources(ctx context.Context, projectID string) ([]SourceMetadata, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT
		project_id,
		provider,
		site_url_hash,
		cloud_id_hash,
		allowlist_hash,
		allowlist_count,
		auth_mode,
		ingestion_enabled,
		initial_full_sync,
		incremental_interval_ms,
		empty_poll_sleep_ms,
		max_idle_sleep_ms,
		overlap_window_ms,
		initial_page_size,
		incremental_page_size,
		max_results,
		updated_at
	FROM project_integration_sources
	WHERE project_id = ?
	ORDER BY provider`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []SourceMetadata
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

func (store *SQLiteStore) CreateSyncRun(ctx context.Context, run SyncRun) error {
	run, err := normalizeRun(run)
	if err != nil {
		return err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO project_integration_sync_runs (
		run_id,
		project_id,
		provider,
		sync_kind,
		status,
		items_seen,
		items_upserted,
		empty_poll,
		idle_sleep_ms,
		error_category,
		started_at,
		finished_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID,
		run.ProjectID,
		string(run.Provider),
		string(run.Kind),
		string(run.Status),
		run.ItemsSeen,
		run.ItemsUpserted,
		boolToInt(run.EmptyPoll),
		durationMillis(run.IdleSleep),
		run.ErrorCategory,
		formatTime(run.StartedAt),
		formatTime(run.FinishedAt),
	)
	return err
}

func (store *SQLiteStore) UpdateSyncRun(ctx context.Context, run SyncRun) error {
	run, err := normalizeRun(run)
	if err != nil {
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE project_integration_sync_runs
	SET status = ?,
		items_seen = ?,
		items_upserted = ?,
		empty_poll = ?,
		idle_sleep_ms = ?,
		error_category = ?,
		finished_at = ?
	WHERE project_id = ? AND provider = ? AND run_id = ?`,
		string(run.Status),
		run.ItemsSeen,
		run.ItemsUpserted,
		boolToInt(run.EmptyPoll),
		durationMillis(run.IdleSleep),
		run.ErrorCategory,
		formatTime(run.FinishedAt),
		run.ProjectID,
		string(run.Provider),
		run.ID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (store *SQLiteStore) GetSyncRun(ctx context.Context, projectID string, provider Provider, runID string) (SyncRun, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
		run_id,
		project_id,
		provider,
		sync_kind,
		status,
		items_seen,
		items_upserted,
		empty_poll,
		idle_sleep_ms,
		error_category,
		started_at,
		finished_at
	FROM project_integration_sync_runs
	WHERE project_id = ? AND provider = ? AND run_id = ?`, projectID, string(provider), runID)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncRun{}, ErrNotFound
	}
	return run, err
}

func (store *SQLiteStore) UpdateSyncState(ctx context.Context, input SyncStateInput) (SyncState, error) {
	state, err := stateFromInput(input)
	if err != nil {
		return SyncState{}, err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO project_integration_sync_state (
		project_id,
		provider,
		last_run_id,
		last_successful_run_id,
		last_success_at,
		last_full_sync_at,
		last_incremental_sync_at,
		last_empty_poll_at,
		empty_poll_count,
		current_idle_sleep_ms,
		cursor,
		cursor_hash,
		updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_id, provider) DO UPDATE SET
		last_run_id = excluded.last_run_id,
		last_successful_run_id = excluded.last_successful_run_id,
		last_success_at = excluded.last_success_at,
		last_full_sync_at = excluded.last_full_sync_at,
		last_incremental_sync_at = excluded.last_incremental_sync_at,
		last_empty_poll_at = excluded.last_empty_poll_at,
		empty_poll_count = excluded.empty_poll_count,
		current_idle_sleep_ms = excluded.current_idle_sleep_ms,
		cursor = excluded.cursor,
		cursor_hash = excluded.cursor_hash,
		updated_at = excluded.updated_at`,
		state.ProjectID,
		string(state.Provider),
		state.LastRunID,
		state.LastSuccessfulRunID,
		formatTime(state.LastSuccessAt),
		formatTime(state.LastFullSyncAt),
		formatTime(state.LastIncrementalSyncAt),
		formatTime(state.LastEmptyPollAt),
		state.EmptyPollCount,
		durationMillis(state.CurrentIdleSleep),
		state.Cursor,
		state.CursorHash,
		formatTime(state.UpdatedAt),
	)
	if err != nil {
		return SyncState{}, err
	}
	return state, nil
}

func (store *SQLiteStore) GetSyncState(ctx context.Context, projectID string, provider Provider) (SyncState, error) {
	row := store.db.QueryRowContext(ctx, `SELECT
		project_id,
		provider,
		last_run_id,
		last_successful_run_id,
		last_success_at,
		last_full_sync_at,
		last_incremental_sync_at,
		last_empty_poll_at,
		empty_poll_count,
		current_idle_sleep_ms,
		cursor,
		cursor_hash,
		updated_at
	FROM project_integration_sync_state
	WHERE project_id = ? AND provider = ?`, projectID, string(provider))
	state, err := scanState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncState{}, ErrNotFound
	}
	return state, err
}

func (store *SQLiteStore) UpsertItem(ctx context.Context, input ItemMetadataInput) (ItemMetadata, error) {
	item, err := itemFromInput(input)
	if err != nil {
		return ItemMetadata{}, err
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO project_integration_items (
		project_id,
		provider,
		item_id,
		item_key,
		item_id_hash,
		item_key_hash,
		item_type,
		item_status,
		item_updated_at,
		first_seen_at,
		last_seen_at,
		last_run_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(project_id, provider, item_id_hash) DO UPDATE SET
		item_id = excluded.item_id,
		item_key = excluded.item_key,
		item_key_hash = excluded.item_key_hash,
		item_type = excluded.item_type,
		item_status = excluded.item_status,
		item_updated_at = excluded.item_updated_at,
		last_seen_at = excluded.last_seen_at,
		last_run_id = excluded.last_run_id`,
		item.ProjectID,
		string(item.Provider),
		item.ItemID,
		item.ItemKey,
		item.ItemIDHash,
		item.ItemKeyHash,
		item.ItemType,
		item.ItemStatus,
		formatTime(item.ItemUpdatedAt),
		formatTime(item.FirstSeenAt),
		formatTime(item.LastSeenAt),
		item.LastRunID,
	)
	if err != nil {
		return ItemMetadata{}, err
	}
	return item, nil
}

func (store *SQLiteStore) ListItems(ctx context.Context, projectID string, provider Provider) ([]ItemMetadata, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT
		project_id,
		provider,
		item_id,
		item_key,
		item_id_hash,
		item_key_hash,
		item_type,
		item_status,
		item_updated_at,
		first_seen_at,
		last_seen_at,
		last_run_id
	FROM project_integration_items
	WHERE project_id = ? AND provider = ?
	ORDER BY item_id_hash`, projectID, string(provider))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ItemMetadata
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func sourceFromInput(input SourceMetadataInput) (SourceMetadata, error) {
	if !validProvider(input.Provider) || strings.TrimSpace(input.ProjectID) == "" {
		return SourceMetadata{}, ErrInvalidInput
	}
	if input.InitialPageSize < 0 || input.IncrementalPageSize < 0 || input.MaxResults < 0 {
		return SourceMetadata{}, ErrInvalidInput
	}
	allowlist := normalizedAllowlist(input.Allowlist)
	return SourceMetadata{
		ProjectID:           strings.TrimSpace(input.ProjectID),
		Provider:            input.Provider,
		SiteURLHash:         optionalHash("site_url", input.SiteURL),
		CloudIDHash:         optionalHash("cloud_id", input.CloudID),
		AllowlistHash:       allowlistHash(allowlist),
		AllowlistCount:      len(allowlist),
		AuthMode:            strings.TrimSpace(input.AuthMode),
		IngestionEnabled:    input.IngestionEnabled,
		InitialFullSync:     strings.TrimSpace(input.InitialFullSync),
		IncrementalInterval: input.IncrementalInterval,
		EmptyPollSleep:      input.EmptyPollSleep,
		MaxIdleSleep:        input.MaxIdleSleep,
		OverlapWindow:       input.OverlapWindow,
		InitialPageSize:     input.InitialPageSize,
		IncrementalPageSize: input.IncrementalPageSize,
		MaxResults:          input.MaxResults,
		UpdatedAt:           input.UpdatedAt.UTC(),
	}, nil
}

func normalizeRun(run SyncRun) (SyncRun, error) {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.ProjectID) == "" || !validProvider(run.Provider) {
		return SyncRun{}, ErrInvalidInput
	}
	if run.Kind != SyncKindInitialFull && run.Kind != SyncKindIncremental {
		return SyncRun{}, ErrInvalidInput
	}
	if run.Status == "" {
		run.Status = SyncRunStatusPending
	}
	switch run.Status {
	case SyncRunStatusPending, SyncRunStatusRunning, SyncRunStatusCompleted, SyncRunStatusFailed, SyncRunStatusNoOp:
	default:
		return SyncRun{}, ErrInvalidInput
	}
	if run.ItemsSeen < 0 || run.ItemsUpserted < 0 || run.IdleSleep < 0 {
		return SyncRun{}, ErrInvalidInput
	}
	run.ID = strings.TrimSpace(run.ID)
	run.ProjectID = strings.TrimSpace(run.ProjectID)
	run.ErrorCategory = strings.TrimSpace(run.ErrorCategory)
	run.StartedAt = run.StartedAt.UTC()
	run.FinishedAt = run.FinishedAt.UTC()
	return run, nil
}

func stateFromInput(input SyncStateInput) (SyncState, error) {
	if strings.TrimSpace(input.ProjectID) == "" || !validProvider(input.Provider) || input.EmptyPollCount < 0 || input.CurrentIdleSleep < 0 {
		return SyncState{}, ErrInvalidInput
	}
	return SyncState{
		ProjectID:             strings.TrimSpace(input.ProjectID),
		Provider:              input.Provider,
		LastRunID:             strings.TrimSpace(input.LastRunID),
		LastSuccessfulRunID:   strings.TrimSpace(input.LastSuccessfulRunID),
		LastSuccessAt:         input.LastSuccessAt.UTC(),
		LastFullSyncAt:        input.LastFullSyncAt.UTC(),
		LastIncrementalSyncAt: input.LastIncrementalSyncAt.UTC(),
		LastEmptyPollAt:       input.LastEmptyPollAt.UTC(),
		EmptyPollCount:        input.EmptyPollCount,
		CurrentIdleSleep:      input.CurrentIdleSleep,
		Cursor:                strings.TrimSpace(input.Cursor),
		CursorHash:            optionalHash("cursor", input.Cursor),
		UpdatedAt:             input.UpdatedAt.UTC(),
	}, nil
}

func itemFromInput(input ItemMetadataInput) (ItemMetadata, error) {
	if strings.TrimSpace(input.ProjectID) == "" || !validProvider(input.Provider) || strings.TrimSpace(input.ItemID) == "" || strings.TrimSpace(input.ItemType) == "" {
		return ItemMetadata{}, ErrInvalidInput
	}
	return ItemMetadata{
		ProjectID:     strings.TrimSpace(input.ProjectID),
		Provider:      input.Provider,
		ItemID:        strings.TrimSpace(input.ItemID),
		ItemKey:       strings.TrimSpace(input.ItemKey),
		ItemIDHash:    optionalHash("item_id", input.ItemID),
		ItemKeyHash:   optionalHash("item_key", input.ItemKey),
		ItemType:      strings.TrimSpace(input.ItemType),
		ItemStatus:    strings.TrimSpace(input.ItemStatus),
		ItemUpdatedAt: input.ItemUpdatedAt.UTC(),
		FirstSeenAt:   input.FirstSeenAt.UTC(),
		LastSeenAt:    input.LastSeenAt.UTC(),
		LastRunID:     strings.TrimSpace(input.LastRunID),
	}, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSource(rows scanner) (SourceMetadata, error) {
	var source SourceMetadata
	var provider string
	var ingestionEnabled int
	var incrementalIntervalMS int64
	var emptyPollSleepMS int64
	var maxIdleSleepMS int64
	var overlapWindowMS int64
	var updatedAt string
	err := rows.Scan(
		&source.ProjectID,
		&provider,
		&source.SiteURLHash,
		&source.CloudIDHash,
		&source.AllowlistHash,
		&source.AllowlistCount,
		&source.AuthMode,
		&ingestionEnabled,
		&source.InitialFullSync,
		&incrementalIntervalMS,
		&emptyPollSleepMS,
		&maxIdleSleepMS,
		&overlapWindowMS,
		&source.InitialPageSize,
		&source.IncrementalPageSize,
		&source.MaxResults,
		&updatedAt,
	)
	if err != nil {
		return SourceMetadata{}, err
	}
	source.Provider = Provider(provider)
	source.IngestionEnabled = ingestionEnabled == 1
	source.IncrementalInterval = time.Duration(incrementalIntervalMS) * time.Millisecond
	source.EmptyPollSleep = time.Duration(emptyPollSleepMS) * time.Millisecond
	source.MaxIdleSleep = time.Duration(maxIdleSleepMS) * time.Millisecond
	source.OverlapWindow = time.Duration(overlapWindowMS) * time.Millisecond
	parsed, err := parseOptionalTime(updatedAt)
	if err != nil {
		return SourceMetadata{}, err
	}
	source.UpdatedAt = parsed
	return source, nil
}

func scanRun(rows scanner) (SyncRun, error) {
	var run SyncRun
	var provider string
	var kind string
	var status string
	var emptyPoll int
	var idleSleepMS int64
	var startedAt string
	var finishedAt string
	err := rows.Scan(
		&run.ID,
		&run.ProjectID,
		&provider,
		&kind,
		&status,
		&run.ItemsSeen,
		&run.ItemsUpserted,
		&emptyPoll,
		&idleSleepMS,
		&run.ErrorCategory,
		&startedAt,
		&finishedAt,
	)
	if err != nil {
		return SyncRun{}, err
	}
	run.Provider = Provider(provider)
	run.Kind = SyncKind(kind)
	run.Status = SyncRunStatus(status)
	run.EmptyPoll = emptyPoll == 1
	run.IdleSleep = time.Duration(idleSleepMS) * time.Millisecond
	var parseErr error
	if run.StartedAt, parseErr = parseOptionalTime(startedAt); parseErr != nil {
		return SyncRun{}, parseErr
	}
	if run.FinishedAt, parseErr = parseOptionalTime(finishedAt); parseErr != nil {
		return SyncRun{}, parseErr
	}
	return run, nil
}

func scanState(rows scanner) (SyncState, error) {
	var state SyncState
	var provider string
	var lastSuccessAt string
	var lastFullSyncAt string
	var lastIncrementalSyncAt string
	var lastEmptyPollAt string
	var currentIdleSleepMS int64
	var cursor string
	var updatedAt string
	err := rows.Scan(
		&state.ProjectID,
		&provider,
		&state.LastRunID,
		&state.LastSuccessfulRunID,
		&lastSuccessAt,
		&lastFullSyncAt,
		&lastIncrementalSyncAt,
		&lastEmptyPollAt,
		&state.EmptyPollCount,
		&currentIdleSleepMS,
		&cursor,
		&state.CursorHash,
		&updatedAt,
	)
	if err != nil {
		return SyncState{}, err
	}
	state.Provider = Provider(provider)
	state.Cursor = cursor
	state.CurrentIdleSleep = time.Duration(currentIdleSleepMS) * time.Millisecond
	var parseErr error
	if state.LastSuccessAt, parseErr = parseOptionalTime(lastSuccessAt); parseErr != nil {
		return SyncState{}, parseErr
	}
	if state.LastFullSyncAt, parseErr = parseOptionalTime(lastFullSyncAt); parseErr != nil {
		return SyncState{}, parseErr
	}
	if state.LastIncrementalSyncAt, parseErr = parseOptionalTime(lastIncrementalSyncAt); parseErr != nil {
		return SyncState{}, parseErr
	}
	if state.LastEmptyPollAt, parseErr = parseOptionalTime(lastEmptyPollAt); parseErr != nil {
		return SyncState{}, parseErr
	}
	if state.UpdatedAt, parseErr = parseOptionalTime(updatedAt); parseErr != nil {
		return SyncState{}, parseErr
	}
	return state, nil
}

func scanItem(rows scanner) (ItemMetadata, error) {
	var item ItemMetadata
	var provider string
	var itemUpdatedAt string
	var firstSeenAt string
	var lastSeenAt string
	err := rows.Scan(
		&item.ProjectID,
		&provider,
		&item.ItemID,
		&item.ItemKey,
		&item.ItemIDHash,
		&item.ItemKeyHash,
		&item.ItemType,
		&item.ItemStatus,
		&itemUpdatedAt,
		&firstSeenAt,
		&lastSeenAt,
		&item.LastRunID,
	)
	if err != nil {
		return ItemMetadata{}, err
	}
	item.Provider = Provider(provider)
	var parseErr error
	if item.ItemUpdatedAt, parseErr = parseOptionalTime(itemUpdatedAt); parseErr != nil {
		return ItemMetadata{}, parseErr
	}
	if item.FirstSeenAt, parseErr = parseOptionalTime(firstSeenAt); parseErr != nil {
		return ItemMetadata{}, parseErr
	}
	if item.LastSeenAt, parseErr = parseOptionalTime(lastSeenAt); parseErr != nil {
		return ItemMetadata{}, parseErr
	}
	return item, nil
}

func validProvider(provider Provider) bool {
	return provider == ProviderJira || provider == ProviderConfluence
}

func normalizedAllowlist(values []string) []string {
	seen := make(map[string]bool)
	var normalized []string
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func allowlistHash(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return hashValue("allowlist", strings.Join(values, "\x00"))
}

func optionalHash(kind string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return hashValue(kind, value)
}

func hashValue(kind string, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func durationMillis(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value / time.Millisecond)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
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
