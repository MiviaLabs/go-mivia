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
