package projectworkspace

import (
	"context"
	"errors"
	"time"
)

var (
	ErrProjectNotFound      = errors.New("workspace project not found")
	ErrInvalidInput         = errors.New("invalid workspace input")
	ErrWorkspaceDisabled    = errors.New("workspace disabled")
	ErrWorkspaceReadOnly    = errors.New("workspace read only")
	ErrGitUnavailable       = errors.New("git unavailable")
	ErrUnsafeContent        = errors.New("unsafe workspace content")
	ErrEditTokenInvalid     = errors.New("edit token invalid")
	ErrEditConflict         = errors.New("edit conflict")
	ErrIngestionUnsupported = errors.New("workspace ingestion unsupported")
)

const (
	ModeDisabled = "disabled"
	ModeReadOnly = "read_only"
	ModeEdit     = "edit"

	DiffScopeWorkingTree = "working_tree"
	DiffScopeStaged      = "staged"
	DiffScopeHead        = "head"
)

const (
	DefaultMaxDiffBytes = 64 << 10
	MaxDiffBytes        = 256 << 10
	DefaultMaxReadBytes = 64 << 10
	MaxReadBytes        = 256 << 10
	MaxPageSize         = 100
)

type API interface {
	GitAvailable(ctx context.Context, projectID string) (bool, error)
	GitStatus(ctx context.Context, projectID string, options GitStatusOptions) (GitStatus, error)
	GitDiff(ctx context.Context, projectID string, options GitDiffOptions) (GitDiff, error)
	ReadFile(ctx context.Context, projectID string, options ReadFileOptions) (WorkspaceFile, error)
	EditFile(ctx context.Context, projectID string, options EditFileOptions) (EditResult, error)
}

type GitStatusOptions struct {
	IncludeUntracked bool
	PathPrefix       string
	PageSize         int
	PageToken        string
}

type GitStatus struct {
	ProjectID     string        `json:"project_id"`
	Branch        string        `json:"branch,omitempty"`
	HeadOIDShort  string        `json:"head_oid_short,omitempty"`
	Entries       []StatusEntry `json:"entries"`
	Truncated     bool          `json:"truncated"`
	NextPageToken string        `json:"next_page_token,omitempty"`
}

type StatusEntry struct {
	RelativePath   string `json:"relative_path"`
	Status         string `json:"status"`
	StagedStatus   string `json:"staged_status,omitempty"`
	WorktreeStatus string `json:"worktree_status,omitempty"`
	RenamedFrom    string `json:"renamed_from,omitempty"`
}

type GitDiffOptions struct {
	Scope        string
	FileID       string
	RelativePath string
	PathPrefix   string
	ContextLines int
	MaxDiffBytes int
	PageToken    string
}

type GitDiff struct {
	ProjectID     string     `json:"project_id"`
	Scope         string     `json:"scope"`
	Files         []DiffFile `json:"files"`
	Skipped       []DiffSkip `json:"skipped,omitempty"`
	DiffTruncated bool       `json:"diff_truncated"`
	NextPageToken string     `json:"next_page_token,omitempty"`
}

type DiffFile struct {
	RelativePath string `json:"relative_path"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Diff         string `json:"diff"`
}

type DiffSkip struct {
	RelativePath string `json:"relative_path,omitempty"`
	Reason       string `json:"reason"`
}

type ReadFileOptions struct {
	FileID       string
	RelativePath string
	MaxBytes     int
}

type WorkspaceFile struct {
	FileID        string    `json:"file_id,omitempty"`
	ProjectID     string    `json:"project_id"`
	RelativePath  string    `json:"relative_path"`
	Extension     string    `json:"extension,omitempty"`
	SizeBytes     int64     `json:"size_bytes"`
	ModifiedAt    time.Time `json:"modified_at"`
	Text          string    `json:"text"`
	TextTruncated bool      `json:"text_truncated"`
	LineCount     int       `json:"line_count"`
	EditToken     string    `json:"edit_token,omitempty"`
}

type EditFileOptions struct {
	FileID       string
	RelativePath string
	EditToken    string
	DryRun       bool
	Edits        []ExactEdit
}

type ExactEdit struct {
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	OldText   string `json:"old_text"`
	NewText   string `json:"new_text"`
}

type EditResult struct {
	Applied        bool          `json:"applied"`
	File           WorkspaceFile `json:"file"`
	DiffPreview    string        `json:"diff_preview,omitempty"`
	TextTruncated  bool          `json:"text_truncated"`
	IngestionRunID string        `json:"ingestion_run_id,omitempty"`
	NewEditToken   string        `json:"new_edit_token,omitempty"`
}
