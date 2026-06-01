package agentactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
)

func TestRecorderRecentFiltersByProjectAndKeepsCapacity(t *testing.T) {
	recorder := NewRecorder(2)
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", RawArgs: json.RawMessage(`{"id":"alpha"}`)})
	recorder.Record(Event{ProjectID: "beta", Method: "tools/call"})
	recorder.Record(Event{ProjectID: "alpha", Method: "resources/read"})

	all := recorder.Recent("", 10)
	if len(all) != 2 {
		t.Fatalf("expected capacity-limited recent events, got %d", len(all))
	}
	if replay := recorder.Recent("", 0); len(replay) != 0 {
		t.Fatalf("expected zero replay events, got %d", len(replay))
	}
	alpha := recorder.Recent("alpha", 10)
	if len(alpha) != 1 || alpha[0].Method != "resources/read" {
		t.Fatalf("expected only latest alpha event, got %#v", alpha)
	}
}

func TestRecorderSubscribeReceivesNewEvents(t *testing.T) {
	recorder := NewRecorder(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := recorder.Subscribe(ctx)

	recorded := recorder.Record(Event{ProjectID: "alpha", Method: "tools/call"})
	select {
	case event := <-events:
		if event.ID != recorded.ID || event.ProjectID != "alpha" {
			t.Fatalf("unexpected event %#v", event)
		}
	default:
		t.Fatal("expected subscriber event")
	}
}

func TestRecorderPersistsRedactedAuditEvents(t *testing.T) {
	ctx := context.Background()
	store, db := newTestSQLiteStore(t, SQLiteStoreOptions{})
	recorder := NewRecorderWithStore(10, store)

	recorder.Record(Event{
		ProjectID: "alpha",
		TraceID:   "trace_1",
		RunID:     "agent_run_1",
		Method:    "tools/call",
		ToolName:  "projects.get",
		Status:    "ok",
		UserAgent: "codex",
		RawArgs:   json.RawMessage(`{"id":"alpha","token":"secret"}`),
		RawResult: json.RawMessage(`{"ok":true}`),
	})

	recent, err := store.Recent(ctx, "alpha", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected one persisted event, got %#v", recent)
	}
	got := recent[0]
	if got.RawArgs != nil || got.RawResult != nil {
		t.Fatalf("expected raw payloads omitted by default, got %#v", got)
	}
	if got.InputSummaryHash != "" || got.OutputSummaryHash != "" {
		t.Fatalf("expected durable payload hashes omitted by default, got %#v", got)
	}
	if got.InputSummaryClass != "object" || got.OutputSummaryClass != "object" || got.ClientClass != "codex" || got.TraceID != "trace_1" || got.RunID != "agent_run_1" {
		t.Fatalf("expected redacted summaries and client class, got %#v", got)
	}
	assertAgentActivityTableOmits(t, db.SQLDB(), "secret", `{"id":"alpha"}`, `{"ok":true}`)
}

func TestRecorderRecentPrefersLiveMemoryPayloadOverPersistedRedaction(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t, SQLiteStoreOptions{})
	recorder := NewRecorderWithStore(10, store)

	recorded := recorder.Record(Event{
		ProjectID: "alpha",
		Method:    "tools/call",
		ToolName:  "projects.context_pack.build",
		Status:    "ok",
		RawArgs:   json.RawMessage(`{"id":"alpha","query":"ContextPack"}`),
		RawResult: json.RawMessage(`{"structuredContent":{"manifest":{"graph_status":"ready","contains_source":false}}}`),
	})

	persisted, err := store.Recent(ctx, "alpha", 10)
	if err != nil {
		t.Fatalf("recent persisted: %v", err)
	}
	if len(persisted) != 1 || persisted[0].RawArgs != nil || persisted[0].RawResult != nil {
		t.Fatalf("expected persisted payload redaction, got %#v", persisted)
	}
	recent := recorder.Recent("alpha", 10)
	if len(recent) != 1 || recent[0].ID != recorded.ID {
		t.Fatalf("expected one live event, got %#v", recent)
	}
	if string(recent[0].RawArgs) == "" || string(recent[0].RawResult) == "" {
		t.Fatalf("expected live memory payload to remain visible, got %#v", recent[0])
	}
}

func TestRecorderRunEventSanitizesAndPersistsCorrelation(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t, SQLiteStoreOptions{})
	recorder := NewRecorderWithStore(10, store)

	recorded := recorder.RecordRunEvent(Event{
		EventKind:       "agent_step",
		ProjectID:       "alpha",
		TraceID:         "trace_1",
		RunID:           "agent_run_1",
		ParentID:        "agent_step_1",
		CorrelationKind: "agent_run",
		Method:          "agent_step",
		ToolName:        "go",
		Status:          "completed",
		RawArgs:         json.RawMessage(`{"raw":"prompt"}`),
	})
	if recorded.EventKind != "agent_step" || recorded.TraceID != "trace_1" || recorded.RunID != "agent_run_1" {
		t.Fatalf("unexpected recorded event: %#v", recorded)
	}
	recent, err := store.Recent(ctx, "alpha", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 || recent[0].ParentID != "agent_step_1" || recent[0].CorrelationKind != "agent_run" || recent[0].RawArgs != nil {
		t.Fatalf("expected redacted correlated run event, got %#v", recent)
	}
}

func TestRecorderPersistsPolicyEventWithoutRawPayloads(t *testing.T) {
	ctx := context.Background()
	store, db := newTestSQLiteStore(t, SQLiteStoreOptions{})
	recorder := NewRecorderWithStore(10, store)

	recorded := recorder.RecordPolicyEvent(PolicyEvent{
		ProjectID: "alpha",
		Category:  "sensitive_content",
		Path:      "internal/config/example.go",
	})

	if recorded.EventKind != "policy_event" || recorded.Method != "policy_event" || recorded.PolicyCategory != "sensitive_content" {
		t.Fatalf("expected normalized policy event, got %#v", recorded)
	}
	recent, err := store.Recent(ctx, "alpha", 10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 || recent[0].EventKind != "policy_event" || recent[0].RelativePath != "internal/config/example.go" {
		t.Fatalf("expected persisted policy metadata, got %#v", recent)
	}
	assertAgentActivityTableOmits(t, db.SQLDB(), "secret", `{"id":"alpha"}`, `{"ok":true}`)
}

func TestRecorderPolicyEventDropsUnsafeRelativePath(t *testing.T) {
	recorder := NewRecorder(10)
	recorded := recorder.RecordPolicyEvent(PolicyEvent{
		ProjectID: "alpha",
		Category:  "denied_path",
		Path:      "../secrets/.env",
	})

	if recorded.RelativePath != "" {
		t.Fatalf("expected unsafe policy path to be omitted, got %#v", recorded)
	}
}

func TestSQLiteStoreCanRetainRawPayloadsWhenExplicitlyEnabled(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t, SQLiteStoreOptions{RetainRawPayloads: true})

	if err := store.Record(ctx, Event{
		ProjectID: "alpha",
		Method:    "tools/call",
		Status:    "ok",
		RawArgs:   json.RawMessage(`{"id":"alpha"}`),
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	recent, err := store.Recent(ctx, "alpha", 1)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 || string(recent[0].RawArgs) != `{"id":"alpha"}` || recent[0].InputSummaryHash == "" {
		t.Fatalf("expected explicit raw retention, got %#v", recent)
	}
}

func TestSQLiteStoreSinceReturnsEventsAfterCursorAscending(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t, SQLiteStoreOptions{})
	for _, event := range []Event{
		{ID: 1, ProjectID: "alpha", Method: "tools/list", Status: "ok"},
		{ID: 2, ProjectID: "beta", Method: "tools/call", Status: "ok"},
		{ID: 3, ProjectID: "alpha", Method: "tools/call", ToolName: "projects.get", Status: "ok"},
		{ID: 4, ProjectID: "alpha", Method: "resources/read", Status: "ok"},
	} {
		if err := store.Record(ctx, event); err != nil {
			t.Fatalf("record %d: %v", event.ID, err)
		}
	}

	recent, err := store.Since(ctx, "alpha", 1, 10)
	if err != nil {
		t.Fatalf("since: %v", err)
	}
	if len(recent) != 2 || recent[0].ID != 3 || recent[1].ID != 4 {
		t.Fatalf("expected alpha events after cursor in ascending order, got %#v", recent)
	}
}

func TestRecorderInitializesNextIDFromStore(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t, SQLiteStoreOptions{})
	if err := store.Record(ctx, Event{ID: 41, ProjectID: "alpha", Method: "tools/call", Status: "ok"}); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	recorder := NewRecorderWithStore(10, store)
	recorded := recorder.Record(Event{ProjectID: "alpha", Method: "tools/list", Status: "ok"})

	if recorded.ID != 42 {
		t.Fatalf("expected recorder to continue persisted IDs, got %d", recorded.ID)
	}
}

func TestRecorderRecentFallsBackToMemoryWhenStoreFails(t *testing.T) {
	recorder := NewRecorderWithStore(10, failingStore{})
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call"})

	recent := recorder.Recent("alpha", 10)
	if len(recent) != 1 || recent[0].ProjectID != "alpha" {
		t.Fatalf("expected in-memory fallback, got %#v", recent)
	}
}

func TestRecorderRecentMergesMemoryWhenStoreWriteFails(t *testing.T) {
	store := &recordFailReadSuccessStore{
		recent: []Event{{ID: 1, ProjectID: "alpha", Method: "tools/list", Status: "ok"}},
	}
	recorder := NewRecorderWithStore(10, store)
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", Status: "ok"})

	recent := recorder.Recent("alpha", 10)
	if len(recent) != 2 || recent[0].Method != "tools/list" || recent[1].Method != "tools/call" {
		t.Fatalf("expected persisted plus unpersisted memory events, got %#v", recent)
	}
}

type failingStore struct{}

func (failingStore) Record(context.Context, Event) error {
	return assertErr("record failed")
}

func (failingStore) Recent(context.Context, string, int) ([]Event, error) {
	return nil, assertErr("recent failed")
}

type assertErr string

func (err assertErr) Error() string {
	return string(err)
}

type recordFailReadSuccessStore struct {
	recent []Event
}

func (store *recordFailReadSuccessStore) Record(context.Context, Event) error {
	return assertErr("record failed")
}

func (store *recordFailReadSuccessStore) Recent(context.Context, string, int) ([]Event, error) {
	return store.recent, nil
}

func (store *recordFailReadSuccessStore) MaxID(context.Context) (int64, error) {
	return 1, nil
}

func newTestSQLiteStore(t *testing.T, options SQLiteStoreOptions) (*SQLiteStore, *sqliteplatform.DB) {
	t.Helper()
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	return NewSQLiteStore(db.SQLDB(), options), db
}

func assertAgentActivityTableOmits(t *testing.T, db *sql.DB, forbidden ...string) {
	t.Helper()
	var rawRequest, rawParams, rawArgs, rawResult string
	if err := db.QueryRowContext(context.Background(), `SELECT raw_request, raw_params, raw_arguments, raw_result FROM agent_activity_events LIMIT 1`).Scan(&rawRequest, &rawParams, &rawArgs, &rawResult); err != nil {
		t.Fatalf("query raw payload columns: %v", err)
	}
	joined := rawRequest + rawParams + rawArgs + rawResult
	for _, value := range forbidden {
		if strings.Contains(joined, value) {
			t.Fatalf("agent activity table leaked %q in raw payload columns", value)
		}
	}
}
