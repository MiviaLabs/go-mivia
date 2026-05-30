package projectingestion

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

const (
	DefaultPageSize      = 50
	MaxPageSize          = 100
	DefaultMaxChunkBytes = 8192
)

type Pagination struct {
	PageSize  int
	PageToken string
}

type RunMetadata struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Trigger       string    `json:"trigger"`
	Mode          string    `json:"mode"`
	Status        string    `json:"status"`
	FilesSeen     int       `json:"files_seen"`
	FilesIngested int       `json:"files_ingested"`
	FilesSkipped  int       `json:"files_skipped"`
	ChunksStored  int       `json:"chunks_stored"`
	SymbolsStored int       `json:"symbols_stored"`
	ErrorCategory string    `json:"error_category,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
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
	Files         []FileMetadata `json:"files"`
	NextPageToken string         `json:"next_page_token,omitempty"`
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
}

type SymbolList struct {
	Symbols       []SymbolMetadata `json:"symbols"`
	NextPageToken string           `json:"next_page_token,omitempty"`
}

func MetadataForRun(run Run) RunMetadata {
	return RunMetadata{
		ID:            run.ID,
		ProjectID:     run.ProjectID,
		Trigger:       string(run.Trigger),
		Mode:          run.Mode,
		Status:        string(run.Status),
		FilesSeen:     run.FilesSeen,
		FilesIngested: run.FilesIngested,
		FilesSkipped:  run.FilesSkipped,
		ChunksStored:  run.ChunksStored,
		SymbolsStored: run.SymbolsStored,
		ErrorCategory: run.ErrorCategory,
		StartedAt:     run.StartedAt,
		FinishedAt:    run.FinishedAt,
	}
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
