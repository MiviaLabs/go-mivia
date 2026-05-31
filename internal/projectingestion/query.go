package projectingestion

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

const (
	DefaultPageSize        = 50
	MaxPageSize            = 100
	DefaultMaxChunkBytes   = 8192
	DefaultMaxSourceBytes  = 8192
	DefaultMaxSnippetBytes = 240
	MaxSnippetBytes        = 1024
	MaxSearchQueryBytes    = 256
	MaxASTQueryBytes       = 64
	MaxCallGraphDepth      = 5
	MaxCallGraphNodes      = 100
)

type Pagination struct {
	PageSize  int
	PageToken string
}

type SymbolFilter struct {
	Kind          SymbolKind
	NamePrefix    string
	NameContains  string
	FileID        string
	Extension     string
	Package       string
	Receiver      string
	CaseSensitive bool
}

type TextSearchOptions struct {
	Query           string
	Mode            string
	CaseSensitive   bool
	Extension       string
	PathPrefix      string
	PageSize        int
	PageToken       string
	MaxSnippetBytes int
	MaxMatches      int
}

type FileSearchOptions struct {
	Extension     string
	PathPrefix    string
	PathContains  string
	CaseSensitive bool
	PageSize      int
	PageToken     string
}

type ReferenceSearchOptions struct {
	NameContains       string
	TargetNameContains string
	CallerNameContains string
	CalleeNameContains string
	EnclosingContains  string
	Extension          string
	PathPrefix         string
	ResolutionStatus   string
	Confidence         string
	CaseSensitive      bool
	PageSize           int
	PageToken          string
}

type ASTSearchOptions struct {
	Language        string
	Query           string
	Captures        []string
	Extension       string
	PathPrefix      string
	PageSize        int
	PageToken       string
	MaxMatches      int
	MaxSnippetBytes int
}

type SearchIndexMetadata struct {
	IndexStatus    string `json:"index_status"`
	IngestionRunID string `json:"ingestion_run_id,omitempty"`
	Degraded       bool   `json:"degraded,omitempty"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}

type FileOutlineOptions struct {
	SymbolFilter     SymbolFilter
	SymbolPagination Pagination
	IncludeChunkText bool
	MaxChunkBytes    int
}

type SymbolSourceOptions struct {
	MaxSourceBytes int
}

type CallGraphOptions struct {
	Direction string
	MaxDepth  int
	MaxNodes  int
}

type RunMetadata struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id"`
	Trigger        string         `json:"trigger"`
	Mode           string         `json:"mode"`
	Status         string         `json:"status"`
	FilesSeen      int            `json:"files_seen"`
	FilesIngested  int            `json:"files_ingested"`
	FilesSkipped   int            `json:"files_skipped"`
	FilesUnchanged int            `json:"files_unchanged"`
	ChunksStored   int            `json:"chunks_stored"`
	SymbolsStored  int            `json:"symbols_stored"`
	ErrorCategory  string         `json:"error_category,omitempty"`
	ReasonCounts   map[string]int `json:"reason_counts,omitempty"`
	CurrentPhase   string         `json:"current_phase,omitempty"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
	HeartbeatAt    time.Time      `json:"heartbeat_at,omitempty"`
	LastProgressAt time.Time      `json:"last_progress_at,omitempty"`
}

type FileMetadata struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	RelativePath   string    `json:"relative_path,omitempty"`
	Extension      string    `json:"extension,omitempty"`
	Status         string    `json:"status"`
	Present        bool      `json:"present"`
	SizeBytes      int64     `json:"size_bytes"`
	ModifiedAt     time.Time `json:"modified_at"`
	SkippedReason  string    `json:"skipped_reason,omitempty"`
	RelativePathOK bool      `json:"relative_path_safe"`
}

type FileList struct {
	Files         []FileMetadata       `json:"files"`
	NextPageToken string               `json:"next_page_token,omitempty"`
	Index         *SearchIndexMetadata `json:"index,omitempty"`
}

type ChunkMetadata struct {
	ID            string `json:"id"`
	FileID        string `json:"file_id"`
	ProjectID     string `json:"project_id"`
	Index         int    `json:"index"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	ByteStart     int    `json:"byte_start"`
	ByteEnd       int    `json:"byte_end"`
	Text          string `json:"text"`
	TextTruncated bool   `json:"text_truncated"`
}

type ChunkList struct {
	Chunks        []ChunkMetadata `json:"chunks"`
	NextPageToken string          `json:"next_page_token,omitempty"`
}

type SymbolMetadata struct {
	ID          string `json:"id"`
	FileID      string `json:"file_id"`
	ProjectID   string `json:"project_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	PackageName string `json:"package,omitempty"`
	ImportPath  string `json:"import_path,omitempty"`
	Receiver    string `json:"receiver,omitempty"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	StartByte   int    `json:"start_byte,omitempty"`
	EndByte     int    `json:"end_byte,omitempty"`
	StartColumn int    `json:"start_column,omitempty"`
	EndColumn   int    `json:"end_column,omitempty"`
}

type SymbolList struct {
	Symbols       []SymbolMetadata     `json:"symbols"`
	NextPageToken string               `json:"next_page_token,omitempty"`
	Index         *SearchIndexMetadata `json:"index,omitempty"`
}

type TextSearchResult struct {
	File             FileMetadata  `json:"file"`
	Chunk            ChunkMetadata `json:"chunk"`
	LineStart        int           `json:"line_start"`
	LineEnd          int           `json:"line_end"`
	ByteStart        int           `json:"byte_start"`
	ByteEnd          int           `json:"byte_end"`
	Snippet          string        `json:"snippet"`
	SnippetTruncated bool          `json:"snippet_truncated"`
}

type TextSearchResultList struct {
	Results         []TextSearchResult   `json:"results"`
	NextPageToken   string               `json:"next_page_token,omitempty"`
	MaxSnippetBytes int                  `json:"max_snippet_bytes"`
	Index           *SearchIndexMetadata `json:"index,omitempty"`
}

type SymbolSource struct {
	Symbol        SymbolMetadata `json:"symbol"`
	Text          string         `json:"text"`
	TextTruncated bool           `json:"text_truncated"`
	MaxBytes      int            `json:"max_bytes"`
}

type SymbolReferenceMetadata struct {
	ID                  string `json:"id"`
	FileID              string `json:"file_id"`
	ProjectID           string `json:"project_id"`
	Kind                string `json:"kind"`
	Name                string `json:"name"`
	TargetName          string `json:"target_name,omitempty"`
	TargetSymbolID      string `json:"target_symbol_id,omitempty"`
	PackageName         string `json:"package,omitempty"`
	Receiver            string `json:"receiver,omitempty"`
	ImportPath          string `json:"import_path,omitempty"`
	EnclosingSymbolID   string `json:"enclosing_symbol_id,omitempty"`
	EnclosingSymbolName string `json:"enclosing_symbol_name,omitempty"`
	StartLine           int    `json:"start_line"`
	EndLine             int    `json:"end_line"`
	StartByte           int    `json:"start_byte,omitempty"`
	EndByte             int    `json:"end_byte,omitempty"`
	StartColumn         int    `json:"start_column,omitempty"`
	EndColumn           int    `json:"end_column,omitempty"`
	ResolutionStatus    string `json:"resolution_status"`
	Confidence          string `json:"confidence,omitempty"`
}

type SymbolReferenceList struct {
	Symbol        SymbolMetadata            `json:"symbol"`
	References    []SymbolReferenceMetadata `json:"references"`
	NextPageToken string                    `json:"next_page_token,omitempty"`
	Index         *SearchIndexMetadata      `json:"index,omitempty"`
}

type SymbolCallEdge struct {
	ID               string `json:"id"`
	CallID           string `json:"call_id,omitempty"`
	FileID           string `json:"file_id,omitempty"`
	ProjectID        string `json:"project_id"`
	CallerSymbolID   string `json:"caller_symbol_id"`
	CalleeSymbolID   string `json:"callee_symbol_id"`
	CallerName       string `json:"caller_name,omitempty"`
	CalleeName       string `json:"callee_name,omitempty"`
	Receiver         string `json:"receiver,omitempty"`
	ImportPath       string `json:"import_path,omitempty"`
	StartLine        int    `json:"start_line,omitempty"`
	EndLine          int    `json:"end_line,omitempty"`
	StartByte        int    `json:"start_byte,omitempty"`
	EndByte          int    `json:"end_byte,omitempty"`
	StartColumn      int    `json:"start_column,omitempty"`
	EndColumn        int    `json:"end_column,omitempty"`
	ResolutionStatus string `json:"resolution_status"`
	Confidence       string `json:"confidence,omitempty"`
}

type SymbolCallEdgeList struct {
	Symbol        SymbolMetadata       `json:"symbol"`
	Edges         []SymbolCallEdge     `json:"edges"`
	NextPageToken string               `json:"next_page_token,omitempty"`
	Index         *SearchIndexMetadata `json:"index,omitempty"`
}

type SymbolCallGraph struct {
	Symbol    SymbolMetadata   `json:"symbol"`
	Direction string           `json:"direction"`
	MaxDepth  int              `json:"max_depth"`
	MaxNodes  int              `json:"max_nodes"`
	Nodes     []SymbolMetadata `json:"nodes"`
	Edges     []SymbolCallEdge `json:"edges"`
	Truncated bool             `json:"truncated"`
}

type ASTSearchResult struct {
	File                 FileMetadata  `json:"file"`
	Chunk                ChunkMetadata `json:"chunk"`
	CaptureName          string        `json:"capture_name"`
	CaptureText          string        `json:"capture_text,omitempty"`
	CaptureTextTruncated bool          `json:"capture_text_truncated,omitempty"`
	LineStart            int           `json:"line_start"`
	LineEnd              int           `json:"line_end"`
	ByteStart            int           `json:"byte_start"`
	ByteEnd              int           `json:"byte_end"`
	StartColumn          int           `json:"start_column,omitempty"`
	EndColumn            int           `json:"end_column,omitempty"`
	Snippet              string        `json:"snippet,omitempty"`
	SnippetTruncated     bool          `json:"snippet_truncated,omitempty"`
}

type ASTSearchResultList struct {
	Results         []ASTSearchResult    `json:"results"`
	NextPageToken   string               `json:"next_page_token,omitempty"`
	QueryLanguage   string               `json:"query_language"`
	QueryVersion    string               `json:"query_version"`
	ResultTruncated bool                 `json:"result_truncated"`
	MaxSnippetBytes int                  `json:"max_snippet_bytes"`
	Coverage        *ASTCoverageMetadata `json:"coverage,omitempty"`
	Index           *SearchIndexMetadata `json:"index,omitempty"`
}

type ASTQueryCatalog struct {
	Queries  []ASTQueryMetadata    `json:"queries"`
	Coverage []ASTCoverageMetadata `json:"coverage,omitempty"`
	Index    *SearchIndexMetadata  `json:"index,omitempty"`
}

type ASTQueryMetadata struct {
	ID         string   `json:"id"`
	Language   string   `json:"language"`
	Version    string   `json:"version"`
	Captures   []string `json:"captures"`
	Extensions []string `json:"extensions"`
}

type ASTCoverageMetadata struct {
	Language             string   `json:"language"`
	Extensions           []string `json:"extensions"`
	CoverageScope        string   `json:"coverage_scope"`
	EligibleFiles        int      `json:"eligible_files"`
	SkippedFileTooLarge  int      `json:"skipped_file_too_large"`
	CoverageStatus       string   `json:"coverage_status"`
	CoveragePartialCause string   `json:"coverage_partial_cause,omitempty"`
}

type HeadingMetadata struct {
	ID          string `json:"id"`
	FileID      string `json:"file_id"`
	ProjectID   string `json:"project_id"`
	Level       int    `json:"level"`
	Text        string `json:"text"`
	ParentIndex int    `json:"parent_index"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
}

type HeadingList struct {
	Headings      []HeadingMetadata `json:"headings"`
	NextPageToken string            `json:"next_page_token,omitempty"`
}

type OutlineChunkMetadata struct {
	ID            string `json:"id"`
	FileID        string `json:"file_id"`
	ProjectID     string `json:"project_id"`
	Index         int    `json:"index"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	ByteStart     int    `json:"byte_start"`
	ByteEnd       int    `json:"byte_end"`
	Text          string `json:"text,omitempty"`
	TextTruncated bool   `json:"text_truncated,omitempty"`
}

type FileOutline struct {
	File                 FileMetadata           `json:"file"`
	Headings             []HeadingMetadata      `json:"headings,omitempty"`
	Symbols              []SymbolMetadata       `json:"symbols,omitempty"`
	SymbolsNextPageToken string                 `json:"symbols_next_page_token,omitempty"`
	Chunks               []OutlineChunkMetadata `json:"chunks,omitempty"`
}

func MetadataForRun(run Run) RunMetadata {
	return RunMetadata{
		ID:             run.ID,
		ProjectID:      run.ProjectID,
		Trigger:        string(run.Trigger),
		Mode:           run.Mode,
		Status:         string(run.Status),
		FilesSeen:      run.FilesSeen,
		FilesIngested:  run.FilesIngested,
		FilesSkipped:   run.FilesSkipped,
		FilesUnchanged: run.FilesUnchanged,
		ChunksStored:   run.ChunksStored,
		SymbolsStored:  run.SymbolsStored,
		ErrorCategory:  run.ErrorCategory,
		ReasonCounts:   copyReasonCounts(run.ReasonCounts),
		CurrentPhase:   run.CurrentPhase,
		StartedAt:      run.StartedAt,
		FinishedAt:     run.FinishedAt,
		HeartbeatAt:    run.HeartbeatAt,
		LastProgressAt: run.LastProgressAt,
	}
}

func copyReasonCounts(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for reason, count := range in {
		if count > 0 {
			out[reason] = count
		}
	}
	return out
}

func MetadataForFileState(project projectregistry.Project, state FileState) FileMetadata {
	metadata := FileMetadata{
		ID:             repoFileID(project.GraphNamespace, state.RelativePathHash),
		ProjectID:      project.ID,
		Status:         string(state.Status),
		Present:        state.Present,
		SizeBytes:      state.SizeBytes,
		ModifiedAt:     state.ModifiedAt,
		SkippedReason:  string(state.SkippedReason),
		RelativePathOK: state.RelativePathSafe,
	}
	if state.RelativePathSafe {
		metadata.RelativePath = state.RelativePath
		metadata.Extension = strings.ToLower(path.Ext(state.RelativePath))
	}
	return metadata
}

func effectiveMaxChunkBytes(project projectregistry.Project, requested int) int {
	limit := project.MaxChunkBytes
	if limit <= 0 {
		limit = DefaultMaxChunkBytes
	}
	if requested > 0 && requested < limit {
		return requested
	}
	return limit
}

func effectiveMaxSourceBytes(project projectregistry.Project, requested int) int {
	limit := project.MaxChunkBytes
	if limit <= 0 {
		limit = DefaultMaxSourceBytes
	}
	if requested > 0 && requested < limit {
		return requested
	}
	return limit
}

func paginate[T any](items []T, pagination Pagination) ([]T, string, error) {
	pageSize, offset, err := paginationWindow(pagination)
	if err != nil {
		return nil, "", err
	}
	if offset >= len(items) {
		return []T{}, "", nil
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[offset:end], next, nil
}

func paginationWindow(pagination Pagination) (int, int, error) {
	pageSize := pagination.PageSize
	if pageSize < 0 {
		return 0, 0, fmt.Errorf("%w: page_size is invalid", ErrInvalidInput)
	}
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return 0, 0, fmt.Errorf("%w: page_size exceeds max %d", ErrInvalidInput, MaxPageSize)
	}
	offset := 0
	if strings.TrimSpace(pagination.PageToken) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(pagination.PageToken))
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("%w: page_token is invalid", ErrInvalidInput)
		}
		offset = parsed
	}
	return pageSize, offset, nil
}

func validOpaqueID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsRune(id, '\x00') {
		return false
	}
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return false
	}
	return id == strings.TrimSpace(id)
}

func truncateUTF8Bytes(value string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value, false
	}
	if maxBytes >= len(value) {
		return value, false
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	if cut <= 0 {
		return "", true
	}
	return value[:cut], true
}
