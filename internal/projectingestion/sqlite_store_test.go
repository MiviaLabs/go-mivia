package projectingestion

import (
	"context"
	"errors"
	"testing"
	"time"

	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
)

func TestSQLiteStore_ListFileStatesPageFiltersInStorage(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	for _, state := range []FileState{
		testFileState("project-1", "cmd/a.go", FileStatusEligible),
		testFileState("project-1", "cmd/b.go", FileStatusEligible),
		testFileState("project-1", "docs/guide.md", FileStatusEligible),
		testFileState("project-1", "tmp/skip.go", FileStatusSkipped),
		testFileState("project-2", "cmd/other.go", FileStatusEligible),
	} {
		if err := store.SaveFileState(ctx, state); err != nil {
			t.Fatalf("save file state: %v", err)
		}
	}

	first, next, err := store.ListFileStatesPage(ctx, "project-1", FileStateFilter{
		Status:    FileStatusEligible,
		Extension: ".go",
	}, Pagination{PageSize: 1})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if len(first) != 1 || first[0].Status != FileStatusEligible || next == "" {
		t.Fatalf("unexpected first page: states=%#v next=%q", first, next)
	}

	second, next, err := store.ListFileStatesPage(ctx, "project-1", FileStateFilter{
		Status:    FileStatusEligible,
		Extension: ".go",
	}, Pagination{PageSize: 1, PageToken: next})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second) != 1 || second[0].RelativePath == first[0].RelativePath || next != "" {
		t.Fatalf("unexpected second page: states=%#v next=%q", second, next)
	}

	if _, _, err := store.ListFileStatesPage(ctx, "project-1", FileStateFilter{}, Pagination{PageSize: MaxPageSize + 1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid page size, got %v", err)
	}
}

func TestSQLiteStore_GetFileStateByHash(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	state := testFileState("project-1", "cmd/main.go", FileStatusEligible)
	if err := store.SaveFileState(ctx, state); err != nil {
		t.Fatalf("save file state: %v", err)
	}

	got, err := store.GetFileStateByHash(ctx, "project-1", state.RelativePathHash)
	if err != nil {
		t.Fatalf("get file state by hash: %v", err)
	}
	if got.RelativePath != state.RelativePath {
		t.Fatalf("expected %q, got %#v", state.RelativePath, got)
	}

	if _, err := store.GetFileStateByHash(ctx, "project-1", hashValue("missing.go")); !errors.Is(err, ErrIngestionNotFound) {
		t.Fatalf("expected missing lookup error, got %v", err)
	}
}

func TestSQLiteStore_SaveRunPersistsReasonCounts(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	run := Run{
		ID:        "run-1",
		ProjectID: "project-1",
		Trigger:   TriggerManual,
		Mode:      "content_graph",
		Status:    RunStatusCompleted,
		ReasonCounts: map[string]int{
			string(SkipReasonSensitiveContent): 2,
			string(SkipReasonParseError):       1,
			"":                                 99,
		},
		StartedAt:  time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 5, 30, 12, 1, 0, 0, time.UTC),
	}
	if err := store.SaveRun(ctx, run); err != nil {
		t.Fatalf("save run: %v", err)
	}

	got, err := store.GetRun(ctx, "project-1", "run-1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if got.ReasonCounts[string(SkipReasonSensitiveContent)] != 2 || got.ReasonCounts[string(SkipReasonParseError)] != 1 {
		t.Fatalf("expected persisted reason counts, got %#v", got.ReasonCounts)
	}
	if _, ok := got.ReasonCounts[""]; ok {
		t.Fatalf("empty reason must not persist: %#v", got.ReasonCounts)
	}
}

func TestSQLiteStore_ListLatestRunsReturnsSafeMetadata(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	older := Run{
		ID:        "run-older",
		ProjectID: "project-1",
		Trigger:   TriggerManual,
		Mode:      "content_graph",
		Status:    RunStatusCompleted,
		StartedAt: time.Date(2026, 5, 30, 11, 0, 0, 0, time.UTC),
	}
	latest := Run{
		ID:            "run-latest",
		ProjectID:     "project-1",
		Trigger:       TriggerManual,
		Mode:          "content_graph",
		Status:        RunStatusFailed,
		FilesSeen:     3,
		FilesSkipped:  1,
		ErrorCategory: "walk_failed",
		ReasonCounts:  map[string]int{string(SkipReasonSensitiveContent): 1},
		StartedAt:     time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 5, 30, 12, 1, 0, 0, time.UTC),
	}
	for _, run := range []Run{older, latest} {
		if err := store.SaveRun(ctx, run); err != nil {
			t.Fatalf("save run: %v", err)
		}
	}

	runs, err := store.ListLatestRuns(ctx, "project-1", 1)
	if err != nil {
		t.Fatalf("list latest runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != latest.ID || runs[0].ErrorCategory != "walk_failed" {
		t.Fatalf("unexpected latest run: %#v", runs)
	}
	if runs[0].ReasonCounts[string(SkipReasonSensitiveContent)] != 1 {
		t.Fatalf("expected reason counts, got %#v", runs[0].ReasonCounts)
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	return NewSQLiteStore(db.SQLDB())
}

func testFileState(projectID string, relativePath string, status FileStatus) FileState {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	return FileState{
		ProjectID:        projectID,
		RelativePathHash: hashValue(relativePath),
		RelativePath:     relativePath,
		RelativePathSafe: true,
		Status:           status,
		Present:          true,
		SizeBytes:        12,
		ModifiedAt:       now,
		LastEventAt:      now,
		LastIngestedAt:   now,
	}
}
