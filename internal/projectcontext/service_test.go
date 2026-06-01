package projectcontext

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

func TestServiceBuild_ComposesBoundedSearchFilesSymbolsAndImpact(t *testing.T) {
	generatedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ingestion := &fakeIngestion{
		text: projectingestion.TextSearchResultList{Results: []projectingestion.TextSearchResult{{
			File: fileMeta("file-1", "internal/projectcontext/service.go"),
			Chunk: projectingestion.ChunkMetadata{
				ID:   "chunk-1",
				Text: "raw chunk text must not be copied into context packs",
			},
			Snippet: "ContextPack",
		}}, Index: &projectingestion.SearchIndexMetadata{IndexStatus: "completed", IngestionRunID: "ingest-1"}},
		files: projectingestion.FileList{Files: []projectingestion.FileMetadata{
			fileMeta("file-1", "internal/projectcontext/service.go"),
			fileMeta("file-2", "internal/projectregistry/mcpapi/mcpapi.go"),
		}, Index: &projectingestion.SearchIndexMetadata{IndexStatus: "completed", IngestionRunID: "ingest-1"}},
		symbols: projectingestion.SymbolList{Symbols: []projectingestion.SymbolMetadata{{
			ID:        "sym-1",
			FileID:    "file-1",
			ProjectID: "project-1",
			Kind:      "function",
			Name:      "Build",
		}}, Index: &projectingestion.SearchIndexMetadata{IndexStatus: "completed", IngestionRunID: "ingest-1"}},
	}
	impact := &fakeImpact{result: projectreliability.ImpactAnalysis{
		ProjectID:    "project-1",
		ChangedPaths: []string{"internal/projectcontext/service.go"},
		SourceAnchors: []projectreliability.SourceAnchor{{
			Path: "internal/projectcontext/service.go",
			Kind: "changed_file",
		}},
	}}

	service := NewService(ingestion, impact)
	service.setNowForTest(func() time.Time { return generatedAt })
	pack, err := service.Build(context.Background(), BuildRequest{
		ProjectID:       " project-1 ",
		Query:           " Build ",
		ChangedPaths:    []string{"internal/projectcontext/service.go", "internal/projectcontext/service.go"},
		MaxItems:        1,
		MaxSnippetBytes: 80,
		IncludeImpact:   true,
	})
	if err != nil {
		t.Fatalf("build context pack: %v", err)
	}
	if pack.ProjectID != "project-1" || pack.Query != "Build" {
		t.Fatalf("unexpected pack identity: %#v", pack)
	}
	if len(pack.TextHits) != 1 || pack.TextHits[0].Snippet != "ContextPack" {
		t.Fatalf("unexpected text hits: %#v", pack.TextHits)
	}
	if pack.TextHits[0].Chunk.Text != "" {
		t.Fatalf("context pack must not include raw chunk text: %#v", pack.TextHits[0].Chunk)
	}
	if len(pack.Files) != 1 || pack.Files[0].ID != "file-1" {
		t.Fatalf("expected bounded deduplicated files, got %#v", pack.Files)
	}
	if len(pack.Symbols) != 1 || pack.Symbols[0].Name != "Build" {
		t.Fatalf("unexpected symbols: %#v", pack.Symbols)
	}
	if pack.Impact == nil || len(pack.Impact.SourceAnchors) != 1 {
		t.Fatalf("expected impact analysis, got %#v", pack.Impact)
	}
	if len(pack.ChangedPaths) != 1 {
		t.Fatalf("expected normalized changed paths, got %#v", pack.ChangedPaths)
	}
	if ingestion.textOptions.PageSize != 1 || ingestion.textOptions.MaxSnippetBytes != 80 {
		t.Fatalf("search limits not applied: %#v", ingestion.textOptions)
	}
	if !contains(pack.Limitations, "integration_artifacts_not_included_v1") {
		t.Fatalf("expected v1 limitation, got %#v", pack.Limitations)
	}
	if pack.Manifest.Version != "context-pack-manifest.v1" || pack.Manifest.GeneratedAt != generatedAt {
		t.Fatalf("unexpected manifest identity: %#v", pack.Manifest)
	}
	if pack.Manifest.GraphStatus != "ready" || pack.Manifest.ContainsSource || pack.Manifest.ExportMode != "none" {
		t.Fatalf("unexpected manifest safety fields: %#v", pack.Manifest)
	}
	if len(pack.Manifest.FileIDs) != 1 || pack.Manifest.FileIDs[0] != "file-1" {
		t.Fatalf("expected deterministic file ids, got %#v", pack.Manifest.FileIDs)
	}
	if len(pack.Manifest.SymbolIDs) != 1 || pack.Manifest.SymbolIDs[0] != "sym-1" {
		t.Fatalf("expected deterministic symbol ids, got %#v", pack.Manifest.SymbolIDs)
	}
	if len(pack.Manifest.ChunkIDs) != 1 || pack.Manifest.ChunkIDs[0] != "chunk-1" {
		t.Fatalf("expected deterministic chunk ids, got %#v", pack.Manifest.ChunkIDs)
	}
	if len(pack.Manifest.RedactedHashes) != 4 || len(pack.Manifest.RedactedHashes[0].Value) != 16 {
		t.Fatalf("expected redacted hashes, got %#v", pack.Manifest.RedactedHashes)
	}
	if !contains(pack.Manifest.Limitations, "full_source_not_included_by_default") {
		t.Fatalf("expected source exclusion limitation, got %#v", pack.Manifest.Limitations)
	}
}

func TestServiceBuild_EmptyQueryReturnsFileSampleAndWarning(t *testing.T) {
	ingestion := &fakeIngestion{
		listFiles: projectingestion.FileList{Files: []projectingestion.FileMetadata{
			fileMeta("file-1", "README.md"),
		}},
	}

	pack, err := NewService(ingestion, nil).Build(context.Background(), BuildRequest{
		ProjectID: "project-1",
		MaxItems:  2,
	})
	if err != nil {
		t.Fatalf("build context pack: %v", err)
	}
	if len(pack.Files) != 1 || pack.Files[0].RelativePath != "README.md" {
		t.Fatalf("expected file sample, got %#v", pack.Files)
	}
	if !contains(pack.Warnings, "query_empty") {
		t.Fatalf("expected query_empty warning, got %#v", pack.Warnings)
	}
}

func TestServiceBuild_RejectsInvalidInput(t *testing.T) {
	_, err := NewService(&fakeIngestion{}, nil).Build(context.Background(), BuildRequest{ProjectID: "project-1", PathPrefix: "../secret"})
	if !errors.Is(err, projectingestion.ErrInvalidInput) {
		t.Fatalf("expected invalid input for unsafe path prefix, got %v", err)
	}
	_, err = NewService(&fakeIngestion{}, nil).Build(context.Background(), BuildRequest{ProjectID: "project-1", Query: "ok", MaxItems: -1})
	if !errors.Is(err, projectingestion.ErrInvalidInput) {
		t.Fatalf("expected invalid input for negative max items, got %v", err)
	}
}

func TestServiceBuild_ManifestFlagsStaleGraph(t *testing.T) {
	ingestion := &fakeIngestion{
		text: projectingestion.TextSearchResultList{
			Results: []projectingestion.TextSearchResult{{
				File:    fileMeta("file-1", "internal/projectcontext/service.go"),
				Chunk:   projectingestion.ChunkMetadata{ID: "chunk-1"},
				Snippet: "Build",
			}},
			Index: &projectingestion.SearchIndexMetadata{IndexStatus: "running", IngestionRunID: "ingest-running"},
		},
	}

	pack, err := NewService(ingestion, nil).Build(context.Background(), BuildRequest{
		ProjectID: "project-1",
		Query:     "Build",
		MaxItems:  1,
	})
	if err != nil {
		t.Fatalf("build context pack: %v", err)
	}
	if pack.Manifest.GraphStatus != "stale" {
		t.Fatalf("expected stale graph status, got %#v", pack.Manifest)
	}
}

type fakeIngestion struct {
	text        projectingestion.TextSearchResultList
	files       projectingestion.FileList
	listFiles   projectingestion.FileList
	symbols     projectingestion.SymbolList
	err         error
	textOptions projectingestion.TextSearchOptions
}

func (fake *fakeIngestion) ListFiles(context.Context, string, projectingestion.FileStateFilter, projectingestion.Pagination) (projectingestion.FileList, error) {
	if fake.err != nil {
		return projectingestion.FileList{}, fake.err
	}
	return fake.listFiles, nil
}

func (fake *fakeIngestion) SearchText(_ context.Context, _ string, options projectingestion.TextSearchOptions) (projectingestion.TextSearchResultList, error) {
	if fake.err != nil {
		return projectingestion.TextSearchResultList{}, fake.err
	}
	fake.textOptions = options
	return fake.text, nil
}

func (fake *fakeIngestion) SearchFiles(context.Context, string, projectingestion.FileSearchOptions) (projectingestion.FileList, error) {
	if fake.err != nil {
		return projectingestion.FileList{}, fake.err
	}
	return fake.files, nil
}

func (fake *fakeIngestion) SearchSymbols(context.Context, string, projectingestion.SymbolFilter, projectingestion.Pagination) (projectingestion.SymbolList, error) {
	if fake.err != nil {
		return projectingestion.SymbolList{}, fake.err
	}
	return fake.symbols, nil
}

type fakeImpact struct {
	result projectreliability.ImpactAnalysis
	err    error
}

func (fake *fakeImpact) Analyze(context.Context, projectreliability.ImpactAnalysisRequest) (projectreliability.ImpactAnalysis, error) {
	return fake.result, fake.err
}

func fileMeta(id string, path string) projectingestion.FileMetadata {
	return projectingestion.FileMetadata{
		ID:             id,
		ProjectID:      "project-1",
		RelativePath:   path,
		Extension:      ".go",
		Status:         string(projectingestion.FileStatusEligible),
		Present:        true,
		ModifiedAt:     time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC),
		RelativePathOK: true,
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
