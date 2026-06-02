package projectintegrations

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
)

func TestSQLiteStore_UpsertSourceStoresHashesAndCountsOnly(t *testing.T) {
	ctx := context.Background()
	store, db := newTestSQLiteStore(t)
	input := testSourceInput()

	source, err := store.UpsertSource(ctx, input)
	if err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if source.SiteURLHash == "" || source.SiteURLHash == input.SiteURL {
		t.Fatalf("expected site URL hash, got %#v", source)
	}
	if source.AllowlistHash == "" || strings.Contains(source.AllowlistHash, "ACME") {
		t.Fatalf("expected allowlist hash, got %#v", source)
	}
	if source.AllowlistCount != 2 {
		t.Fatalf("expected deduplicated allowlist count, got %#v", source)
	}

	sources, err := store.ListSources(ctx, "project-1")
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(sources) != 1 || sources[0].SiteURLHash != source.SiteURLHash || sources[0].AllowlistCount != 2 {
		t.Fatalf("unexpected sources: %#v", sources)
	}
	assertTableOmits(t, db, "project_integration_sources", input.SiteURL, "ACME", "ENG")
}

func TestSQLiteStore_SyncRunLifecycle(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	if _, err := store.UpsertSource(ctx, testSourceInput()); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	run := SyncRun{
		ID:        "run-1",
		ProjectID: "project-1",
		Provider:  ProviderJira,
		Kind:      SyncKindIncremental,
		Status:    SyncRunStatusRunning,
		StartedAt: testTime(),
	}
	if err := store.CreateSyncRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	run.Status = SyncRunStatusNoOp
	run.EmptyPoll = true
	run.IdleSleep = 2 * time.Minute
	run.ItemsChanged = 1
	run.ItemsUnchanged = 2
	run.RichContentChanged = 3
	run.RichContentUnchanged = 4
	run.FinishedAt = testTime().Add(time.Minute)
	if err := store.UpdateSyncRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	got, err := store.GetSyncRun(ctx, "project-1", ProviderJira, "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Status != SyncRunStatusNoOp || !got.EmptyPoll || got.IdleSleep != 2*time.Minute || got.ItemsChanged != 1 || got.ItemsUnchanged != 2 || got.RichContentChanged != 3 || got.RichContentUnchanged != 4 {
		t.Fatalf("unexpected run: %#v", got)
	}
	if _, err := store.GetSyncRun(ctx, "project-1", ProviderJira, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestSQLiteStore_GetActiveSyncRunReturnsNewestPendingOrRunning(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	runs := []SyncRun{
		{ID: "run-completed", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKindIncremental, Status: SyncRunStatusCompleted, StartedAt: testTime()},
		{ID: "run-pending", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKindInitialFull, Status: SyncRunStatusPending, StartedAt: testTime().Add(time.Minute)},
		{ID: "run-running", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKindInitialFull, Status: SyncRunStatusRunning, ItemsSeen: 2, ItemsUpserted: 2, StartedAt: testTime().Add(2 * time.Minute)},
	}
	for _, run := range runs {
		if err := store.CreateSyncRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
	}

	active, err := store.GetActiveSyncRun(ctx, "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("get active run: %v", err)
	}
	if active.ID != "run-running" || active.ItemsSeen != 2 || active.Status != SyncRunStatusRunning {
		t.Fatalf("unexpected active run: %#v", active)
	}
	if _, err := store.GetActiveSyncRun(ctx, "project-1", ProviderConfluence); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing active run, got %v", err)
	}
}

func TestSQLiteStore_FailActiveSyncRunsMarksInterruptedRunsFailed(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	runs := []SyncRun{
		{ID: "pending-run", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKindInitialFull, Status: SyncRunStatusPending, StartedAt: testTime()},
		{ID: "running-run", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKindIncremental, Status: SyncRunStatusRunning, StartedAt: testTime().Add(time.Minute)},
		{ID: "completed-run", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKindIncremental, Status: SyncRunStatusCompleted, StartedAt: testTime().Add(2 * time.Minute), FinishedAt: testTime().Add(3 * time.Minute)},
	}
	for _, run := range runs {
		if err := store.CreateSyncRun(ctx, run); err != nil {
			t.Fatalf("create run %s: %v", run.ID, err)
		}
	}

	finishedAt := testTime().Add(4 * time.Minute)
	failed, err := store.FailActiveSyncRuns(ctx, finishedAt, string(ErrorCategoryInterrupted))
	if err != nil {
		t.Fatalf("fail active runs: %v", err)
	}
	if failed != 2 {
		t.Fatalf("expected 2 failed runs, got %d", failed)
	}
	for _, id := range []string{"pending-run", "running-run"} {
		run, err := store.GetSyncRun(ctx, "project-1", ProviderJira, id)
		if err != nil {
			t.Fatalf("get run %s: %v", id, err)
		}
		if run.Status != SyncRunStatusFailed || run.ErrorCategory != string(ErrorCategoryInterrupted) || !run.FinishedAt.Equal(finishedAt) {
			t.Fatalf("unexpected failed run %s: %#v", id, run)
		}
	}
	completed, err := store.GetSyncRun(ctx, "project-1", ProviderJira, "completed-run")
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if completed.Status != SyncRunStatusCompleted {
		t.Fatalf("completed run should not be changed: %#v", completed)
	}
}

func TestSQLiteStore_UpdateSyncStatePersistsApprovedRawCursorAndHash(t *testing.T) {
	ctx := context.Background()
	store, db := newTestSQLiteStore(t)
	if _, err := store.UpsertSource(ctx, testSourceInput()); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	state, err := store.UpdateSyncState(ctx, SyncStateInput{
		ProjectID:             "project-1",
		Provider:              ProviderJira,
		LastRunID:             "run-1",
		LastSuccessfulRunID:   "run-1",
		LastSuccessAt:         testTime(),
		LastIncrementalSyncAt: testTime(),
		LastEmptyPollAt:       testTime().Add(time.Minute),
		EmptyPollCount:        1,
		CurrentIdleSleep:      3 * time.Minute,
		Cursor:                "raw-provider-cursor",
		UpdatedAt:             testTime().Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("update state: %v", err)
	}
	if state.Cursor != "raw-provider-cursor" || state.CursorHash == "" || strings.Contains(state.CursorHash, "raw-provider-cursor") {
		t.Fatalf("expected raw cursor plus hash, got %#v", state)
	}

	got, err := store.GetSyncState(ctx, "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.EmptyPollCount != 1 || got.CurrentIdleSleep != 3*time.Minute || got.Cursor != "raw-provider-cursor" || got.CursorHash != state.CursorHash {
		t.Fatalf("unexpected state: %#v", got)
	}
	assertTableOmits(t, db, "project_integration_sync_state", "MIVIA_ATLASSIAN", "/home/mac/secret", "api-token")
}

func TestSQLiteStore_UpsertItemStoresApprovedRawIDsAndHashesWithoutRichContent(t *testing.T) {
	ctx := context.Background()
	store, db := newTestSQLiteStore(t)
	if _, err := store.UpsertSource(ctx, testSourceInput()); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	item, err := store.UpsertItem(ctx, ItemMetadataInput{
		ProjectID:     "project-1",
		Provider:      ProviderJira,
		ItemID:        "10001",
		ItemKey:       "ACME-123",
		ItemType:      "issue",
		ItemStatus:    "updated",
		ItemUpdatedAt: testTime(),
		FirstSeenAt:   testTime(),
		LastSeenAt:    testTime().Add(time.Minute),
		LastRunID:     "run-1",
	})
	if err != nil {
		t.Fatalf("upsert item: %v", err)
	}
	if item.ItemID != "10001" || item.ItemKey != "ACME-123" || item.ItemIDHash == "10001" || item.ItemKeyHash == "ACME-123" {
		t.Fatalf("expected raw item identifiers plus hashes, got %#v", item)
	}

	items, err := store.ListItems(ctx, "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(items) != 1 || items[0].ItemID != "10001" || items[0].ItemKey != "ACME-123" || items[0].ItemIDHash != item.ItemIDHash || items[0].ItemType != "issue" || items[0].ContentSHA256 == "" {
		t.Fatalf("unexpected items: %#v", items)
	}
	assertTableOmits(t, db, "project_integration_items", "page body", "comment text", "MIVIA_ATLASSIAN", "/home/mac/secret", "api-token")
}

func TestSQLiteStore_CountItemsByProjectAndProvider(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	inputs := []ItemMetadataInput{
		{ProjectID: "project-1", Provider: ProviderJira, ItemID: "10001", ItemKey: "ACME-1", ItemType: "issue", FirstSeenAt: testTime(), LastSeenAt: testTime()},
		{ProjectID: "project-1", Provider: ProviderJira, ItemID: "10002", ItemKey: "ACME-2", ItemType: "issue", FirstSeenAt: testTime(), LastSeenAt: testTime()},
		{ProjectID: "project-1", Provider: ProviderConfluence, ItemID: "20001", ItemType: "page", FirstSeenAt: testTime(), LastSeenAt: testTime()},
		{ProjectID: "project-2", Provider: ProviderJira, ItemID: "30001", ItemKey: "OTHER-1", ItemType: "issue", FirstSeenAt: testTime(), LastSeenAt: testTime()},
	}
	for _, input := range inputs {
		if _, err := store.UpsertItem(ctx, input); err != nil {
			t.Fatalf("upsert item %#v: %v", input, err)
		}
	}

	jiraCount, err := store.CountItems(ctx, "project-1", ProviderJira)
	if err != nil {
		t.Fatalf("count jira: %v", err)
	}
	confluenceCount, err := store.CountItems(ctx, "project-1", ProviderConfluence)
	if err != nil {
		t.Fatalf("count confluence: %v", err)
	}
	if jiraCount != 2 || confluenceCount != 1 {
		t.Fatalf("unexpected counts: jira=%d confluence=%d", jiraCount, confluenceCount)
	}
}

func TestSQLiteStore_ListJiraItemsDefaultsToUpdatedDesc(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	inputs := []ItemMetadataInput{
		{ProjectID: "project-1", Provider: ProviderJira, ItemID: "10001", ItemKey: "LOCAL-1", ItemType: "issue", ItemUpdatedAt: testTime().Add(-time.Hour), FirstSeenAt: testTime(), LastSeenAt: testTime()},
		{ProjectID: "project-1", Provider: ProviderJira, ItemID: "10002", ItemKey: "LOCAL-2", ItemType: "issue", ItemUpdatedAt: testTime(), FirstSeenAt: testTime(), LastSeenAt: testTime()},
		{ProjectID: "project-1", Provider: ProviderJira, ItemID: "10003", ItemKey: "LOCAL-3", ItemType: "task", ItemUpdatedAt: testTime().Add(time.Hour), FirstSeenAt: testTime(), LastSeenAt: testTime()},
	}
	for _, input := range inputs {
		if _, err := store.UpsertItem(ctx, input); err != nil {
			t.Fatalf("upsert item %#v: %v", input, err)
		}
	}

	page, err := store.ListItemsPage(ctx, "project-1", ProviderJira, ItemListOptions{ItemType: "issue", PageSize: 2})
	if err != nil {
		t.Fatalf("list jira items: %v", err)
	}
	if page.Sort != "updated_desc" {
		t.Fatalf("Sort = %q, want updated_desc", page.Sort)
	}
	if len(page.Items) != 2 || page.Items[0].ItemKey != "LOCAL-2" || page.Items[1].ItemKey != "LOCAL-1" {
		t.Fatalf("expected recent issue items sorted by updated desc, got %#v", page.Items)
	}
	if _, err := store.ListItemsPage(ctx, "project-1", ProviderJira, ItemListOptions{Sort: "provider_url_desc"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid sort error, got %v", err)
	}
}

func TestSQLiteStore_UpsertItemReportsUnchangedContent(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	if _, err := store.UpsertSource(ctx, testSourceInput()); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	input := ItemMetadataInput{
		ProjectID:       "project-1",
		Provider:        ProviderJira,
		ItemID:          "10001",
		ItemKey:         "ACME-123",
		ItemType:        "issue",
		ItemStatus:      "updated",
		ItemUpdatedAt:   testTime(),
		ProviderVersion: "7",
		ProviderETag:    "etag-1",
		FirstSeenAt:     testTime(),
		LastSeenAt:      testTime(),
		LastRunID:       "run-1",
	}
	first, err := store.UpsertItem(ctx, input)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if !first.Changed || first.ContentSHA256 == "" {
		t.Fatalf("expected first upsert to be changed: %#v", first)
	}

	input.LastSeenAt = testTime().Add(time.Minute)
	input.LastRunID = "run-2"
	second, err := store.UpsertItem(ctx, input)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.Changed || second.ContentSHA256 != first.ContentSHA256 {
		t.Fatalf("expected unchanged content hash: first=%#v second=%#v", first, second)
	}

	input.ItemStatus = "done"
	third, err := store.UpsertItem(ctx, input)
	if err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if !third.Changed || third.ContentSHA256 == first.ContentSHA256 {
		t.Fatalf("expected status change to change content hash: first=%#v third=%#v", first, third)
	}
}

func TestSQLiteStore_RejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	store, _ := newTestSQLiteStore(t)
	if _, err := store.UpsertSource(ctx, SourceMetadataInput{ProjectID: "project-1", Provider: Provider("other")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid source, got %v", err)
	}
	if err := store.CreateSyncRun(ctx, SyncRun{ID: "run-1", ProjectID: "project-1", Provider: ProviderJira, Kind: SyncKind("full")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid run, got %v", err)
	}
	if _, err := store.UpsertItem(ctx, ItemMetadataInput{ProjectID: "project-1", Provider: ProviderJira, ItemType: "issue"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid item, got %v", err)
	}
}

func TestSQLiteStore_SQLiteBusyErrorDetection(t *testing.T) {
	if !isSQLiteBusyError(errors.New("database is locked (SQLITE_BUSY)")) {
		t.Fatal("expected sqlite busy error to be retryable")
	}
	if isSQLiteBusyError(errors.New("constraint failed")) {
		t.Fatal("expected non-locking sqlite error to be non-retryable")
	}
}

func newTestSQLiteStore(t *testing.T) (*SQLiteStore, *sql.DB) {
	t.Helper()
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	return NewSQLiteStore(db.SQLDB()), db.SQLDB()
}

func testSourceInput() SourceMetadataInput {
	return SourceMetadataInput{
		ProjectID:           "project-1",
		Provider:            ProviderJira,
		SiteURL:             "https://example.atlassian.net",
		CloudID:             "cloud-1",
		Allowlist:           []string{"ACME", "ENG", "acme"},
		AuthMode:            "basic",
		IngestionEnabled:    false,
		InitialFullSync:     "manual",
		IncrementalInterval: time.Minute,
		EmptyPollSleep:      5 * time.Minute,
		MaxIdleSleep:        30 * time.Minute,
		OverlapWindow:       2 * time.Minute,
		InitialPageSize:     50,
		IncrementalPageSize: 25,
		MaxResults:          100,
		UpdatedAt:           testTime(),
	}
}

func testTime() time.Time {
	return time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
}

func assertTableOmits(t *testing.T, db *sql.DB, table string, forbidden ...string) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT * FROM "+table)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns for %s: %v", table, err)
	}
	values := make([]sql.NullString, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		for _, value := range values {
			for _, raw := range forbidden {
				if value.Valid && raw != "" && strings.Contains(value.String, raw) {
					t.Fatalf("%s persisted raw value %q in row %#v", table, raw, values)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
}
