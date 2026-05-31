package projectreliability

import "time"

type ContextHealthStatus string

const (
	ContextHealthReady       ContextHealthStatus = "ready"
	ContextHealthWarmingUp   ContextHealthStatus = "warming_up"
	ContextHealthRunning     ContextHealthStatus = "running"
	ContextHealthSyncing     ContextHealthStatus = "syncing"
	ContextHealthDegraded    ContextHealthStatus = "degraded"
	ContextHealthStale       ContextHealthStatus = "stale"
	ContextHealthEmpty       ContextHealthStatus = "empty"
	ContextHealthDisabled    ContextHealthStatus = "disabled"
	ContextHealthUnavailable ContextHealthStatus = "unavailable"
)

type ContextHealth struct {
	ProjectID             string              `json:"project_id"`
	Status                ContextHealthStatus `json:"status"`
	Enabled               bool                `json:"enabled"`
	DigestMode            string              `json:"digest_mode"`
	UpdatePolicy          string              `json:"update_policy"`
	WorkspaceMode         string              `json:"workspace_mode"`
	GraphStorage          string              `json:"graph_storage"`
	ValidationStatus      string              `json:"validation_status"`
	StatusReason          string              `json:"status_reason,omitempty"`
	LatestRun             *RunSummary         `json:"latest_run,omitempty"`
	ActiveRunID           string              `json:"active_run_id,omitempty"`
	EligibleFileCount     int                 `json:"eligible_file_count"`
	IndexedSymbolCount    int                 `json:"indexed_symbol_count"`
	IndexedChunkCount     int                 `json:"indexed_chunk_count"`
	SearchIndex           SearchIndexHealth   `json:"search_index"`
	WorkspaceGitAvailable bool                `json:"workspace_git_available"`
	ReasonCounts          map[string]int      `json:"reason_counts,omitempty"`
	CheckedAt             time.Time           `json:"checked_at"`
}

type RunSummary struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	Trigger        string         `json:"trigger,omitempty"`
	RunKind        string         `json:"run_kind,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	FilesSeen      int            `json:"files_seen"`
	FilesIngested  int            `json:"files_ingested"`
	FilesSkipped   int            `json:"files_skipped"`
	FilesUnchanged int            `json:"files_unchanged"`
	ChunksStored   int            `json:"chunks_stored"`
	SymbolsStored  int            `json:"symbols_stored"`
	ErrorCategory  string         `json:"error_category,omitempty"`
	ReasonCounts   map[string]int `json:"reason_counts,omitempty"`
	CurrentPhase   string         `json:"current_phase,omitempty"`
	StartedAt      time.Time      `json:"started_at,omitempty"`
	FinishedAt     time.Time      `json:"finished_at,omitempty"`
	HeartbeatAt    time.Time      `json:"heartbeat_at,omitempty"`
	LastProgressAt time.Time      `json:"last_progress_at,omitempty"`
}

type SearchIndexHealth struct {
	Status   string `json:"status"`
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
}
