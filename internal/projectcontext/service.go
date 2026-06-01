package projectcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

const (
	DefaultMaxItems = 8
	MaxItems        = 25
)

type Ingestion interface {
	ListFiles(context.Context, string, projectingestion.FileStateFilter, projectingestion.Pagination) (projectingestion.FileList, error)
	SearchText(context.Context, string, projectingestion.TextSearchOptions) (projectingestion.TextSearchResultList, error)
	SearchFiles(context.Context, string, projectingestion.FileSearchOptions) (projectingestion.FileList, error)
	SearchSymbols(context.Context, string, projectingestion.SymbolFilter, projectingestion.Pagination) (projectingestion.SymbolList, error)
}

type ImpactAnalyzer interface {
	Analyze(context.Context, projectreliability.ImpactAnalysisRequest) (projectreliability.ImpactAnalysis, error)
}

type Service struct {
	ingestion Ingestion
	impact    ImpactAnalyzer
	now       func() time.Time
}

type BuildRequest struct {
	ProjectID       string   `json:"project_id,omitempty"`
	Query           string   `json:"query,omitempty"`
	PathPrefix      string   `json:"path_prefix,omitempty"`
	ChangedPaths    []string `json:"changed_paths,omitempty"`
	DiffScope       string   `json:"diff_scope,omitempty"`
	MaxDiffBytes    int      `json:"max_diff_bytes,omitempty"`
	MaxItems        int      `json:"max_items,omitempty"`
	MaxSnippetBytes int      `json:"max_snippet_bytes,omitempty"`
	IncludeImpact   bool     `json:"include_impact"`
}

type ContextPack struct {
	ProjectID    string                              `json:"project_id"`
	Query        string                              `json:"query,omitempty"`
	PathPrefix   string                              `json:"path_prefix,omitempty"`
	ChangedPaths []string                            `json:"changed_paths,omitempty"`
	Limits       Limits                              `json:"limits"`
	TextHits     []projectingestion.TextSearchResult `json:"text_hits,omitempty"`
	Files        []projectingestion.FileMetadata     `json:"files,omitempty"`
	Symbols      []projectingestion.SymbolMetadata   `json:"symbols,omitempty"`
	Impact       *projectreliability.ImpactAnalysis  `json:"impact,omitempty"`
	Manifest     ContextPackManifest                 `json:"manifest"`
	Partial      bool                                `json:"partial,omitempty"`
	Warnings     []string                            `json:"warnings,omitempty"`
	Limitations  []string                            `json:"limitations,omitempty"`
}

type ContextPackManifest struct {
	Version             string                  `json:"version"`
	GeneratedAt         time.Time               `json:"generated_at"`
	Mode                string                  `json:"mode"`
	ProjectID           string                  `json:"project_id"`
	Query               string                  `json:"query,omitempty"`
	PathPrefix          string                  `json:"path_prefix,omitempty"`
	ChangedPaths        []string                `json:"changed_paths,omitempty"`
	GraphStatus         string                  `json:"graph_status"`
	SearchIndexStatuses []SearchIndexSnapshot   `json:"search_index_statuses,omitempty"`
	FileIDs             []string                `json:"file_ids,omitempty"`
	SymbolIDs           []string                `json:"symbol_ids,omitempty"`
	ChunkIDs            []string                `json:"chunk_ids,omitempty"`
	FileTimestamps      []FileTimestampSnapshot `json:"file_timestamps,omitempty"`
	RedactedHashes      []RedactedHash          `json:"redacted_hashes,omitempty"`
	ContainsSource      bool                    `json:"contains_source"`
	ExportMode          string                  `json:"export_mode"`
	Warnings            []string                `json:"warnings,omitempty"`
	Limitations         []string                `json:"limitations,omitempty"`
}

type SearchIndexSnapshot struct {
	Source         string `json:"source"`
	IndexStatus    string `json:"index_status"`
	IngestionRunID string `json:"ingestion_run_id,omitempty"`
	Degraded       bool   `json:"degraded,omitempty"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}

type FileTimestampSnapshot struct {
	FileID     string    `json:"file_id"`
	ModifiedAt time.Time `json:"modified_at"`
}

type RedactedHash struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type Limits struct {
	MaxItems        int `json:"max_items"`
	MaxSnippetBytes int `json:"max_snippet_bytes"`
}

func NewService(ingestion Ingestion, impact ImpactAnalyzer) *Service {
	return &Service{ingestion: ingestion, impact: impact, now: func() time.Time { return time.Now().UTC() }}
}

func (service *Service) setNowForTest(now func() time.Time) {
	if service != nil && now != nil {
		service.now = now
	}
}

func (service *Service) Build(ctx context.Context, request BuildRequest) (ContextPack, error) {
	if service == nil || service.ingestion == nil {
		return ContextPack{}, projectingestion.ErrUnsupportedIngest
	}
	projectID := strings.TrimSpace(request.ProjectID)
	if projectID == "" {
		return ContextPack{}, projectingestion.ErrInvalidInput
	}
	pathPrefix, err := normalizeOptionalPathPrefix(request.PathPrefix)
	if err != nil {
		return ContextPack{}, err
	}
	query := strings.TrimSpace(request.Query)
	if query != "" {
		if _, err := projectingestion.NormalizeTextSearchOptions(projectingestion.TextSearchOptions{
			Query:           query,
			PathPrefix:      pathPrefix,
			PageSize:        effectiveMaxItems(request.MaxItems),
			MaxSnippetBytes: effectiveMaxSnippetBytes(request.MaxSnippetBytes),
			MaxMatches:      effectiveMaxItems(request.MaxItems),
		}); err != nil {
			return ContextPack{}, err
		}
	}
	if request.MaxItems < 0 || request.MaxSnippetBytes < 0 {
		return ContextPack{}, projectingestion.ErrInvalidInput
	}
	changedPaths, err := normalizeChangedPaths(request.ChangedPaths)
	if err != nil {
		return ContextPack{}, err
	}
	maxItems := effectiveMaxItems(request.MaxItems)
	maxSnippetBytes := effectiveMaxSnippetBytes(request.MaxSnippetBytes)
	pack := ContextPack{
		ProjectID:    projectID,
		Query:        query,
		PathPrefix:   pathPrefix,
		ChangedPaths: changedPaths,
		Limits: Limits{
			MaxItems:        maxItems,
			MaxSnippetBytes: maxSnippetBytes,
		},
		Limitations: []string{
			"integration_artifacts_not_included_v1",
			"raw_workspace_diff_not_included",
			"full_source_not_included_by_default",
		},
	}
	if query != "" {
		pack = service.addTextHits(ctx, pack, query, pathPrefix, maxItems, maxSnippetBytes)
		pack = service.addSymbolSearch(ctx, pack, query, maxItems)
	} else {
		pack.Warnings = appendUnique(pack.Warnings, "query_empty")
	}
	for _, changedPath := range changedPaths {
		pack = service.addChangedPathFile(ctx, pack, changedPath, maxItems)
	}
	if query != "" {
		pack = service.addFileSearch(ctx, pack, query, pathPrefix, maxItems)
	} else {
		pack = service.addFileSample(ctx, pack, pathPrefix, maxItems)
	}
	if request.IncludeImpact && service.impact != nil {
		impact, err := service.impact.Analyze(ctx, projectreliability.ImpactAnalysisRequest{
			ProjectID:    projectID,
			ChangedPaths: changedPaths,
			DiffScope:    strings.TrimSpace(request.DiffScope),
			MaxDiffBytes: request.MaxDiffBytes,
		})
		if err != nil {
			if isInvalidInput(err) {
				return ContextPack{}, err
			}
			pack.Partial = true
			pack.Warnings = appendUnique(pack.Warnings, "impact_unavailable")
		} else {
			pack.Impact = &impact
			if impact.Partial {
				pack.Partial = true
				pack.Warnings = appendUnique(pack.Warnings, "impact_partial")
			}
		}
	}
	pack.Manifest = service.buildManifest(pack)
	return pack, nil
}

func (service *Service) addTextHits(ctx context.Context, pack ContextPack, query string, pathPrefix string, maxItems int, maxSnippetBytes int) ContextPack {
	results, err := service.ingestion.SearchText(ctx, pack.ProjectID, projectingestion.TextSearchOptions{
		Query:           query,
		PathPrefix:      pathPrefix,
		PageSize:        maxItems,
		MaxMatches:      maxItems,
		MaxSnippetBytes: maxSnippetBytes,
	})
	if err != nil {
		if isInvalidInput(err) {
			pack.Partial = true
			pack.Warnings = appendUnique(pack.Warnings, "text_search_invalid")
			return pack
		}
		pack.Partial = true
		pack.Warnings = appendUnique(pack.Warnings, "text_search_unavailable")
		return pack
	}
	pack.Manifest.SearchIndexStatuses = appendSearchIndexSnapshot(pack.Manifest.SearchIndexStatuses, "text_search", results.Index)
	pack.TextHits = boundedTextHits(results.Results, maxItems)
	return pack
}

func (service *Service) addFileSearch(ctx context.Context, pack ContextPack, query string, pathPrefix string, maxItems int) ContextPack {
	files, err := service.ingestion.SearchFiles(ctx, pack.ProjectID, projectingestion.FileSearchOptions{
		PathContains: query,
		PathPrefix:   pathPrefix,
		PageSize:     maxItems,
	})
	if err != nil {
		if isInvalidInput(err) {
			pack.Warnings = appendUnique(pack.Warnings, "file_search_invalid")
			return pack
		}
		pack.Partial = true
		pack.Warnings = appendUnique(pack.Warnings, "file_search_unavailable")
		return pack
	}
	pack.Manifest.SearchIndexStatuses = appendSearchIndexSnapshot(pack.Manifest.SearchIndexStatuses, "file_search", files.Index)
	pack.Files = appendFiles(pack.Files, files.Files, maxItems)
	return pack
}

func (service *Service) addFileSample(ctx context.Context, pack ContextPack, pathPrefix string, maxItems int) ContextPack {
	files, err := service.ingestion.ListFiles(ctx, pack.ProjectID, projectingestion.FileStateFilter{
		Status:     projectingestion.FileStatusEligible,
		PathPrefix: pathPrefix,
	}, projectingestion.Pagination{PageSize: maxItems})
	if err != nil {
		if isInvalidInput(err) {
			pack.Warnings = appendUnique(pack.Warnings, "file_sample_invalid")
			return pack
		}
		pack.Partial = true
		pack.Warnings = appendUnique(pack.Warnings, "file_sample_unavailable")
		return pack
	}
	pack.Manifest.SearchIndexStatuses = appendSearchIndexSnapshot(pack.Manifest.SearchIndexStatuses, "file_sample", files.Index)
	pack.Files = appendFiles(pack.Files, files.Files, maxItems)
	return pack
}

func (service *Service) addChangedPathFile(ctx context.Context, pack ContextPack, changedPath string, maxItems int) ContextPack {
	files, err := service.ingestion.ListFiles(ctx, pack.ProjectID, projectingestion.FileStateFilter{
		Status:     projectingestion.FileStatusEligible,
		PathPrefix: changedPath,
	}, projectingestion.Pagination{PageSize: maxItems})
	if err != nil {
		if isInvalidInput(err) {
			pack.Warnings = appendUnique(pack.Warnings, "changed_path_lookup_invalid")
			return pack
		}
		pack.Partial = true
		pack.Warnings = appendUnique(pack.Warnings, "changed_path_lookup_unavailable")
		return pack
	}
	pack.Manifest.SearchIndexStatuses = appendSearchIndexSnapshot(pack.Manifest.SearchIndexStatuses, "changed_path_lookup", files.Index)
	for _, file := range files.Files {
		if file.RelativePath == changedPath {
			pack.Files = appendFiles(pack.Files, []projectingestion.FileMetadata{file}, maxItems)
			return pack
		}
	}
	pack.Warnings = appendUnique(pack.Warnings, "changed_path_not_indexed")
	return pack
}

func (service *Service) addSymbolSearch(ctx context.Context, pack ContextPack, query string, maxItems int) ContextPack {
	symbols, err := service.ingestion.SearchSymbols(ctx, pack.ProjectID, projectingestion.SymbolFilter{
		NameContains: query,
	}, projectingestion.Pagination{PageSize: maxItems})
	if err != nil {
		if isInvalidInput(err) {
			pack.Warnings = appendUnique(pack.Warnings, "symbol_search_invalid")
			return pack
		}
		pack.Partial = true
		pack.Warnings = appendUnique(pack.Warnings, "symbol_search_unavailable")
		return pack
	}
	pack.Manifest.SearchIndexStatuses = appendSearchIndexSnapshot(pack.Manifest.SearchIndexStatuses, "symbol_search", symbols.Index)
	if len(symbols.Symbols) > maxItems {
		pack.Symbols = append(pack.Symbols, symbols.Symbols[:maxItems]...)
		return pack
	}
	pack.Symbols = append(pack.Symbols, symbols.Symbols...)
	return pack
}

func normalizeOptionalPathPrefix(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	return projectingestion.NormalizePathPrefix(raw)
}

func normalizeChangedPaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		normalized, err := normalizeOptionalPathPrefix(path)
		if err != nil {
			return nil, err
		}
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func effectiveMaxItems(requested int) int {
	if requested <= 0 {
		return DefaultMaxItems
	}
	if requested > MaxItems {
		return MaxItems
	}
	return requested
}

func effectiveMaxSnippetBytes(requested int) int {
	if requested <= 0 {
		return projectingestion.DefaultMaxSnippetBytes
	}
	if requested > projectingestion.MaxSnippetBytes {
		return projectingestion.MaxSnippetBytes
	}
	return requested
}

func boundedTextHits(results []projectingestion.TextSearchResult, maxItems int) []projectingestion.TextSearchResult {
	if len(results) > maxItems {
		results = results[:maxItems]
	}
	out := append([]projectingestion.TextSearchResult(nil), results...)
	for i := range out {
		out[i].Chunk.Text = ""
		out[i].Chunk.TextTruncated = false
	}
	return out
}

func appendFiles(existing []projectingestion.FileMetadata, incoming []projectingestion.FileMetadata, maxItems int) []projectingestion.FileMetadata {
	seen := map[string]struct{}{}
	for _, file := range existing {
		seen[file.ID] = struct{}{}
	}
	for _, file := range incoming {
		if len(existing) >= maxItems {
			return existing
		}
		key := file.ID
		if key == "" {
			key = file.RelativePath
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, file)
	}
	return existing
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func isInvalidInput(err error) bool {
	return errors.Is(err, projectingestion.ErrInvalidInput)
}

func (service *Service) buildManifest(pack ContextPack) ContextPackManifest {
	manifest := pack.Manifest
	manifest.Version = "context-pack-manifest.v1"
	manifest.GeneratedAt = serviceNow(service)
	manifest.Mode = "manifest_only"
	manifest.ProjectID = pack.ProjectID
	manifest.Query = pack.Query
	manifest.PathPrefix = pack.PathPrefix
	manifest.ChangedPaths = append([]string(nil), pack.ChangedPaths...)
	manifest.FileIDs = sortedFileIDs(pack)
	manifest.SymbolIDs = sortedSymbolIDs(pack.Symbols)
	manifest.ChunkIDs = sortedChunkIDs(pack.TextHits)
	manifest.FileTimestamps = fileTimestamps(pack)
	manifest.SearchIndexStatuses = sortedSearchIndexSnapshots(manifest.SearchIndexStatuses)
	manifest.GraphStatus = graphStatus(pack, manifest.SearchIndexStatuses)
	manifest.ContainsSource = false
	manifest.ExportMode = "none"
	manifest.Warnings = append([]string(nil), pack.Warnings...)
	manifest.Limitations = append([]string(nil), pack.Limitations...)
	manifest.RedactedHashes = []RedactedHash{
		{Kind: "manifest_inputs_sha256_16", Value: redactedHash(manifest.ProjectID, manifest.Query, manifest.PathPrefix, strings.Join(manifest.ChangedPaths, "\x00"))},
		{Kind: "file_ids_sha256_16", Value: redactedHash(strings.Join(manifest.FileIDs, "\x00"))},
		{Kind: "symbol_ids_sha256_16", Value: redactedHash(strings.Join(manifest.SymbolIDs, "\x00"))},
		{Kind: "chunk_ids_sha256_16", Value: redactedHash(strings.Join(manifest.ChunkIDs, "\x00"))},
	}
	return manifest
}

func serviceNow(service *Service) time.Time {
	if service != nil && service.now != nil {
		return service.now().UTC()
	}
	return time.Now().UTC()
}

func appendSearchIndexSnapshot(existing []SearchIndexSnapshot, source string, metadata *projectingestion.SearchIndexMetadata) []SearchIndexSnapshot {
	if metadata == nil {
		return existing
	}
	return append(existing, SearchIndexSnapshot{
		Source:         source,
		IndexStatus:    metadata.IndexStatus,
		IngestionRunID: metadata.IngestionRunID,
		Degraded:       metadata.Degraded,
		DegradedReason: metadata.DegradedReason,
	})
}

func sortedFileIDs(pack ContextPack) []string {
	seen := map[string]struct{}{}
	for _, file := range pack.Files {
		if file.ID != "" {
			seen[file.ID] = struct{}{}
		}
	}
	for _, hit := range pack.TextHits {
		if hit.File.ID != "" {
			seen[hit.File.ID] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func sortedSymbolIDs(symbols []projectingestion.SymbolMetadata) []string {
	seen := map[string]struct{}{}
	for _, symbol := range symbols {
		if symbol.ID != "" {
			seen[symbol.ID] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func sortedChunkIDs(hits []projectingestion.TextSearchResult) []string {
	seen := map[string]struct{}{}
	for _, hit := range hits {
		if hit.Chunk.ID != "" {
			seen[hit.Chunk.ID] = struct{}{}
		}
	}
	return sortedKeys(seen)
}

func fileTimestamps(pack ContextPack) []FileTimestampSnapshot {
	byID := map[string]time.Time{}
	for _, file := range pack.Files {
		if file.ID != "" && !file.ModifiedAt.IsZero() {
			byID[file.ID] = file.ModifiedAt.UTC()
		}
	}
	for _, hit := range pack.TextHits {
		if hit.File.ID != "" && !hit.File.ModifiedAt.IsZero() {
			byID[hit.File.ID] = hit.File.ModifiedAt.UTC()
		}
	}
	ids := sortedKeysFromTimeMap(byID)
	out := make([]FileTimestampSnapshot, 0, len(ids))
	for _, id := range ids {
		out = append(out, FileTimestampSnapshot{FileID: id, ModifiedAt: byID[id]})
	}
	return out
}

func sortedSearchIndexSnapshots(values []SearchIndexSnapshot) []SearchIndexSnapshot {
	out := append([]SearchIndexSnapshot(nil), values...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].IngestionRunID < out[j].IngestionRunID
	})
	return out
}

func graphStatus(pack ContextPack, indexes []SearchIndexSnapshot) string {
	if pack.Partial {
		return "partial"
	}
	for _, index := range indexes {
		if index.Degraded {
			return "degraded"
		}
		if index.IndexStatus != "" && index.IndexStatus != "completed" {
			return "stale"
		}
	}
	return "ready"
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedKeysFromTimeMap(values map[string]time.Time) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func redactedHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])[:16]
}
