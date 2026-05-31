package projectingestion

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestSQLiteStore_HasSearchFileVersionUsesDirectMetadata(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:main")

	if err := store.UpsertSearchFile(ctx, project, state, nil, nil, nil, nil); err != nil {
		t.Fatalf("upsert search file: %v", err)
	}

	ok, err := store.HasSearchFileVersion(ctx, project, state)
	if err != nil {
		t.Fatalf("has search file version: %v", err)
	}
	if ok {
		t.Fatalf("expected missing keyed FTS metadata to require repair")
	}
}

func TestSQLiteStore_UpsertSearchFilesBatchPreservesDeleteBeforeInsert(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:old")
	if err := store.UpsertSearchFile(ctx, project, state, []Chunk{{Index: 0, Text: "old"}}, nil, nil, nil); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}

	state.ContentSHA256 = "sha256:new"
	if err := store.UpsertSearchFilesBatch(ctx, project, []PreparedSearchFile{{
		State:  state,
		Chunks: []Chunk{{Index: 0, Text: "new"}},
	}}); err != nil {
		t.Fatalf("batch upsert: %v", err)
	}

	results, err := store.SearchText(ctx, project, TextSearchOptions{Query: "old", MaxMatches: 10})
	if err != nil {
		t.Fatalf("search old text: %v", err)
	}
	if len(results.Results) != 0 {
		t.Fatalf("expected old chunks to be deleted, got %#v", results.Results)
	}
	ok, err := store.HasSearchFileVersion(ctx, project, state)
	if err != nil {
		t.Fatalf("has search file version: %v", err)
	}
	if !ok {
		t.Fatalf("expected new batch version metadata")
	}
}

func TestSQLiteStore_UpdateSearchFileMetadataBatch(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:main")
	if err := store.UpsertSearchFile(ctx, project, state, []Chunk{{Index: 0, Text: "body"}}, nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	state.RelativePath = "internal/main.go"
	if err := store.UpdateSearchFileMetadataBatch(ctx, project, []FileState{state}); err != nil {
		t.Fatalf("batch metadata update: %v", err)
	}
	files, err := store.SearchFiles(ctx, project, FileSearchOptions{PathContains: "internal/main.go"})
	if err != nil {
		t.Fatalf("search files: %v", err)
	}
	if len(files.Files) != 1 || files.Files[0].RelativePath != "internal/main.go" {
		t.Fatalf("expected updated path, got %#v", files.Files)
	}
}

func TestSQLiteStore_BatchWriteFailureMarksSearchIndexDegraded(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	if _, err := store.db.ExecContext(ctx, `DROP TABLE project_search_chunks_fts`); err != nil {
		t.Fatalf("drop chunks table: %v", err)
	}

	err := store.UpsertSearchFilesBatch(ctx, project, []PreparedSearchFile{{
		State:  testSearchFileState("project-1", "cmd/main.go", "sha256:main"),
		Chunks: []Chunk{{Index: 0, Text: "body"}},
	}})
	if err == nil {
		t.Fatalf("expected batch write failure")
	}
	health, err := store.SearchIndexHealth(ctx, project)
	if err != nil {
		t.Fatalf("search health: %v", err)
	}
	if !health.Degraded || health.Reason != "search_index_write_failed" {
		t.Fatalf("expected degraded search index, got %#v", health)
	}
}

func testSearchProject() projectregistry.Project {
	return projectregistry.Project{
		ID:             "project-1",
		GraphNamespace: "project-1",
	}
}

func testSearchFileState(projectID string, relativePath string, contentSHA256 string) FileState {
	state := testFileState(projectID, relativePath, FileStatusEligible)
	state.ContentSHA256 = contentSHA256
	return state
}
