package agentactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type SQLiteStoreOptions struct {
	RetainRawPayloads bool
}

type SQLiteStore struct {
	db      *sql.DB
	options SQLiteStoreOptions
}

func NewSQLiteStore(db *sql.DB, options SQLiteStoreOptions) *SQLiteStore {
	return &SQLiteStore{db: db, options: options}
}

func (store *SQLiteStore) Record(ctx context.Context, event Event) error {
	if store == nil || store.db == nil {
		return nil
	}
	event = enrichEvent(event)
	rawRequest, rawParams, rawArgs, rawResult := "", "", "", ""
	if store.options.RetainRawPayloads {
		rawRequest = string(event.RawRequest)
		rawParams = string(event.RawParams)
		rawArgs = string(event.RawArgs)
		rawResult = string(event.RawResult)
	} else {
		event.InputSummaryHash = ""
		event.OutputSummaryHash = ""
	}
	var eventID any
	if event.ID > 0 {
		eventID = event.ID
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO agent_activity_events (
		id,
		occurred_at,
		event_kind,
		project_id,
		trace_id,
		run_id,
		parent_id,
		correlation_kind,
		method,
		tool_name,
		status,
		duration_ms,
		failure_category,
		policy_category,
		relative_path,
		request_id,
		client_class,
		input_summary_hash,
		input_summary_class,
		output_summary_hash,
		output_summary_class,
		raw_request,
		raw_params,
		raw_arguments,
		raw_result
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		formatTime(event.Timestamp),
		event.EventKind,
		event.ProjectID,
		event.TraceID,
		event.RunID,
		event.ParentID,
		event.CorrelationKind,
		event.Method,
		event.ToolName,
		event.Status,
		event.DurationMS,
		event.FailureCategory,
		event.PolicyCategory,
		event.RelativePath,
		event.RequestID,
		event.ClientClass,
		event.InputSummaryHash,
		event.InputSummaryClass,
		event.OutputSummaryHash,
		event.OutputSummaryClass,
		rawRequest,
		rawParams,
		rawArgs,
		rawResult,
	)
	return err
}

func (store *SQLiteStore) MaxID(ctx context.Context) (int64, error) {
	if store == nil || store.db == nil {
		return 0, nil
	}
	var id int64
	if err := store.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM agent_activity_events`).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (store *SQLiteStore) Recent(ctx context.Context, projectID string, limit int) ([]Event, error) {
	if store == nil || store.db == nil || limit == 0 {
		return nil, nil
	}
	if limit < 0 {
		limit = defaultCapacity
	}
	query := `SELECT
		id,
		occurred_at,
		event_kind,
		project_id,
		trace_id,
		run_id,
		parent_id,
		correlation_kind,
		method,
		tool_name,
		status,
		duration_ms,
		failure_category,
		policy_category,
		relative_path,
		request_id,
		client_class,
		input_summary_hash,
		input_summary_class,
		output_summary_hash,
		output_summary_class,
		raw_request,
		raw_params,
		raw_arguments,
		raw_result
	FROM agent_activity_events`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var selected []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		selected = append(selected, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected, nil
}

func (store *SQLiteStore) Since(ctx context.Context, projectID string, afterID int64, limit int) ([]Event, error) {
	if store == nil || store.db == nil || limit == 0 {
		return nil, nil
	}
	if limit < 0 {
		limit = defaultCapacity
	}
	query := `SELECT
		id,
		occurred_at,
		event_kind,
		project_id,
		trace_id,
		run_id,
		parent_id,
		correlation_kind,
		method,
		tool_name,
		status,
		duration_ms,
		failure_category,
		policy_category,
		relative_path,
		request_id,
		client_class,
		input_summary_hash,
		input_summary_class,
		output_summary_hash,
		output_summary_class,
		raw_request,
		raw_params,
		raw_arguments,
		raw_result
	FROM agent_activity_events
	WHERE id > ?`
	args := []any{afterID}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var selected []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		selected = append(selected, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return selected, nil
}

func scanEvent(scanner interface {
	Scan(dest ...any) error
}) (Event, error) {
	var event Event
	var occurredAt string
	var rawRequest, rawParams, rawArgs, rawResult string
	if err := scanner.Scan(
		&event.ID,
		&occurredAt,
		&event.EventKind,
		&event.ProjectID,
		&event.TraceID,
		&event.RunID,
		&event.ParentID,
		&event.CorrelationKind,
		&event.Method,
		&event.ToolName,
		&event.Status,
		&event.DurationMS,
		&event.FailureCategory,
		&event.PolicyCategory,
		&event.RelativePath,
		&event.RequestID,
		&event.ClientClass,
		&event.InputSummaryHash,
		&event.InputSummaryClass,
		&event.OutputSummaryHash,
		&event.OutputSummaryClass,
		&rawRequest,
		&rawParams,
		&rawArgs,
		&rawResult,
	); err != nil {
		return Event{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return Event{}, err
	}
	event.Timestamp = parsed
	event.RawRequest = rawJSON(rawRequest)
	event.RawParams = rawJSON(rawParams)
	event.RawArgs = rawJSON(rawArgs)
	event.RawResult = rawJSON(rawResult)
	return event, nil
}

func rawJSON(value string) json.RawMessage {
	if value == "" {
		return nil
	}
	return json.RawMessage(value)
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		value = time.Now().UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func IsNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
