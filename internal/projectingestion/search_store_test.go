package projectingestion

import (
	"context"
	"testing"
	"time"

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

func TestSQLiteStore_UpdateSearchFileMetadataBatchDiagnosticsTrackDeletes(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:main")
	if err := store.UpsertSearchFile(ctx, project, state, []Chunk{{Index: 0, Text: "body"}}, nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	skipped := state
	skipped.Status = FileStatusSkipped
	skipped.ContentSHA256 = ""
	if err := store.UpdateSearchFileMetadataBatch(ctx, project, []FileState{skipped}); err != nil {
		t.Fatalf("batch metadata delete: %v", err)
	}

	diagnostics := store.SearchWriteDiagnostics(project.ID)
	if diagnostics.TransactionCount != 2 {
		t.Fatalf("expected upsert plus metadata update transactions, got %#v", diagnostics)
	}
	if diagnostics.DeleteStatements["project_search_files_fts"] != 2 ||
		diagnostics.DeleteStatements["project_search_chunks_fts"] != 2 ||
		diagnostics.DeleteStatements["project_search_symbols_fts"] != 2 {
		t.Fatalf("expected delete diagnostics for upsert and non-eligible metadata update, got %#v", diagnostics.DeleteStatements)
	}
}

func TestSQLiteStore_ApplySearchFileBatchDoesNotRewriteFTSForUnchangedFiles(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:main")
	if err := store.UpsertSearchFile(ctx, project, state, []Chunk{{Index: 0, Text: "body"}}, nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	updated := state
	updated.ModifiedAt = state.ModifiedAt.Add(time.Hour)
	if err := store.ApplySearchFileBatch(ctx, project, []fullScanFileResult{{state: updated, unchanged: true}}); err != nil {
		t.Fatalf("apply unchanged batch: %v", err)
	}

	var versionModifiedAt string
	if err := store.db.QueryRowContext(ctx, `SELECT modified_at
		FROM project_search_file_versions
		WHERE project_id = ? AND file_id = ?`, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash)).Scan(&versionModifiedAt); err != nil {
		t.Fatalf("query version modified_at: %v", err)
	}
	if versionModifiedAt != formatTime(updated.ModifiedAt) {
		t.Fatalf("expected version metadata update, got %q", versionModifiedAt)
	}
	var ftsModifiedAt string
	if err := store.db.QueryRowContext(ctx, `SELECT modified_at
		FROM project_search_chunks_fts
		WHERE project_id = ? AND file_id = ?`, project.ID, repoFileID(project.GraphNamespace, state.RelativePathHash)).Scan(&ftsModifiedAt); err != nil {
		t.Fatalf("query fts modified_at: %v", err)
	}
	if ftsModifiedAt != formatTime(state.ModifiedAt) {
		t.Fatalf("expected unchanged FTS metadata, got %q", ftsModifiedAt)
	}
}

func TestSQLiteStore_ApplySearchFileBatchBoundsSubBatchesByWriteWeight(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	heavySymbols := make([]Symbol, fullScanPreparedBatchMaxWriteWeight)
	for i := range heavySymbols {
		heavySymbols[i] = Symbol{Kind: SymbolKindFunction, Name: "HeavySymbol"}
	}
	results := []fullScanFileResult{
		testSearchFileResult("cmd/heavy.go", "sha256:heavy", []Chunk{{Index: 0, Text: "alpha body"}}, heavySymbols),
		testSearchFileResult("cmd/light.go", "sha256:light", []Chunk{{Index: 0, Text: "beta body"}}, nil),
	}

	var subBatches [][]fullScanFileResult
	if err := forEachFullScanResultBatchByWeight(results, fullScanPreparedBatchMaxWriteWeight, func(batch []fullScanFileResult) error {
		copied := append([]fullScanFileResult(nil), batch...)
		subBatches = append(subBatches, copied)
		return nil
	}); err != nil {
		t.Fatalf("split batches: %v", err)
	}
	if len(subBatches) != 2 || len(subBatches[0]) != 1 || len(subBatches[1]) != 1 {
		t.Fatalf("expected bounded sub-batches, got %#v", subBatches)
	}

	if err := store.ApplySearchFileBatch(ctx, project, results); err != nil {
		t.Fatalf("apply split search batch: %v", err)
	}
}

func TestSQLiteStore_ApplySearchFileBatchSearchResultsSurviveSplitWrites(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	heavySymbols := make([]Symbol, fullScanPreparedBatchMaxWriteWeight)
	for i := range heavySymbols {
		heavySymbols[i] = Symbol{Kind: SymbolKindFunction, Name: "HeavySymbol"}
	}
	results := []fullScanFileResult{
		testSearchFileResult("cmd/heavy.go", "sha256:heavy", []Chunk{{Index: 0, Text: "alpha body"}}, heavySymbols),
		testSearchFileResult("cmd/light.go", "sha256:light", []Chunk{{Index: 0, Text: "beta body"}}, []Symbol{{Kind: SymbolKindFunction, Name: "LightSymbol"}}),
	}
	if err := store.ApplySearchFileBatch(ctx, project, results); err != nil {
		t.Fatalf("apply split search batch: %v", err)
	}

	alpha, err := store.SearchText(ctx, project, TextSearchOptions{Query: "alpha", MaxMatches: 10})
	if err != nil {
		t.Fatalf("search alpha: %v", err)
	}
	beta, err := store.SearchText(ctx, project, TextSearchOptions{Query: "beta", MaxMatches: 10})
	if err != nil {
		t.Fatalf("search beta: %v", err)
	}
	if len(alpha.Results) != 1 || alpha.Results[0].File.RelativePath != "cmd/heavy.go" {
		t.Fatalf("expected heavy-file alpha result, got %#v", alpha.Results)
	}
	if len(beta.Results) != 1 || beta.Results[0].File.RelativePath != "cmd/light.go" {
		t.Fatalf("expected light-file beta result, got %#v", beta.Results)
	}
	symbols, err := store.SearchSymbols(ctx, project, SymbolFilter{NameContains: "LightSymbol"}, Pagination{PageSize: 10})
	if err != nil {
		t.Fatalf("search symbols: %v", err)
	}
	if len(symbols.Symbols) != 1 || symbols.Symbols[0].Name != "LightSymbol" {
		t.Fatalf("expected light symbol after split write, got %#v", symbols.Symbols)
	}
}

func TestSQLiteStore_SearchWriteDiagnosticsTrackFTSBatchShape(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	results := []fullScanFileResult{
		testSearchFileResult("cmd/main.go", "sha256:main", []Chunk{{Index: 0, Text: "alpha body"}}, []Symbol{{Kind: SymbolKindFunction, Name: "MainSymbol"}}),
		{state: testSearchFileState("project-1", "cmd/unchanged.go", "sha256:unchanged"), unchanged: true},
		{state: testFileState("project-1", "secrets/token.go", FileStatusSkipped)},
	}
	results[0].headings = []Heading{{Level: 1, Text: "Title"}}
	results[0].implementations = []Implementation{{Kind: "implements", ImplementerName: "Impl", ImplementedName: "Iface"}}

	if err := store.ApplySearchFileBatch(ctx, project, results); err != nil {
		t.Fatalf("apply batch: %v", err)
	}

	diagnostics := store.SearchWriteDiagnostics(project.ID)
	if diagnostics.TransactionCount != 1 {
		t.Fatalf("expected one transaction, got %#v", diagnostics)
	}
	if diagnostics.MaxWriteWeight != 5 {
		t.Fatalf("unexpected write weight: %#v", diagnostics)
	}
	if diagnostics.RowsInserted["project_search_files_fts"] != 1 ||
		diagnostics.RowsInserted["project_search_chunks_fts"] != 1 ||
		diagnostics.RowsInserted["project_search_symbols_fts"] != 1 {
		t.Fatalf("unexpected inserted row diagnostics: %#v", diagnostics.RowsInserted)
	}
	if diagnostics.DeleteStatements["project_search_files_fts"] != 2 ||
		diagnostics.DeleteStatements["project_search_chunks_fts"] != 2 ||
		diagnostics.DeleteStatements["project_search_symbols_fts"] != 2 {
		t.Fatalf("unexpected delete diagnostics: %#v", diagnostics.DeleteStatements)
	}

	diagnostics.RowsInserted["project_search_files_fts"] = 99
	if cloned := store.SearchWriteDiagnostics(project.ID); cloned.RowsInserted["project_search_files_fts"] != 1 {
		t.Fatalf("expected diagnostics copy, got %#v", cloned.RowsInserted)
	}
}

func TestSQLiteStore_CountSearchSymbolsAndChunks(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:main")
	if err := store.UpsertSearchFile(ctx, project, state,
		[]Chunk{{Index: 0, Text: "body"}, {Index: 1, Text: "more"}},
		[]Symbol{{Kind: SymbolKindFunction, Name: "main"}, {Kind: SymbolKindFunction, Name: "helper"}},
		nil,
		nil,
	); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	symbols, err := store.CountSearchSymbols(ctx, project)
	if err != nil {
		t.Fatalf("count symbols: %v", err)
	}
	chunks, err := store.CountSearchChunks(ctx, project)
	if err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if symbols != 2 || chunks != 2 {
		t.Fatalf("expected two symbols and chunks, got symbols=%d chunks=%d", symbols, chunks)
	}
}

func TestSQLiteStore_ContextSearchIndexHealthDoesNotRunDriftScan(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	state := testSearchFileState("project-1", "cmd/main.go", "sha256:main")
	if err := store.UpsertSearchFile(ctx, project, state, []Chunk{{Index: 0, Text: "body"}}, nil, nil, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "DELETE FROM project_search_files_fts WHERE project_id = ?", "project-1"); err != nil {
		t.Fatalf("delete fts file row: %v", err)
	}

	health, err := store.ContextSearchIndexHealth(ctx, project)
	if err != nil {
		t.Fatalf("context health: %v", err)
	}
	if health.Degraded {
		t.Fatalf("context health should not run request-time drift scans, got %#v", health)
	}
	searchHealth, err := store.SearchIndexHealth(ctx, project)
	if err != nil {
		t.Fatalf("search health: %v", err)
	}
	if !searchHealth.Degraded || searchHealth.Reason != "search_index_drift" {
		t.Fatalf("expected deep search health to detect drift, got %#v", searchHealth)
	}
}

func TestSQLiteStore_SearchFilesPaginatesInStorage(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	project := testSearchProject()
	for _, relativePath := range []string{"cmd/a.go", "cmd/b.go", "cmd/c.go"} {
		if err := store.UpsertSearchFile(ctx, project, testSearchFileState("project-1", relativePath, "sha256:"+relativePath), nil, nil, nil, nil); err != nil {
			t.Fatalf("upsert %s: %v", relativePath, err)
		}
	}

	first, err := store.SearchFiles(ctx, project, FileSearchOptions{PathPrefix: "cmd/", PageSize: 2})
	if err != nil {
		t.Fatalf("search first page: %v", err)
	}
	if len(first.Files) != 2 || first.Files[0].RelativePath != "cmd/a.go" || first.Files[1].RelativePath != "cmd/b.go" || first.NextPageToken == "" {
		t.Fatalf("unexpected first page: %#v", first)
	}

	second, err := store.SearchFiles(ctx, project, FileSearchOptions{PathPrefix: "cmd/", PageSize: 2, PageToken: first.NextPageToken})
	if err != nil {
		t.Fatalf("search second page: %v", err)
	}
	if len(second.Files) != 1 || second.Files[0].RelativePath != "cmd/c.go" || second.NextPageToken != "" {
		t.Fatalf("unexpected second page: %#v", second)
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

func testSearchFileResult(relativePath string, contentSHA256 string, chunks []Chunk, symbols []Symbol) fullScanFileResult {
	return fullScanFileResult{
		state:       testSearchFileState("project-1", relativePath, contentSHA256),
		chunks:      chunks,
		symbols:     symbols,
		chunkCount:  len(chunks),
		symbolCount: len(symbols),
	}
}
