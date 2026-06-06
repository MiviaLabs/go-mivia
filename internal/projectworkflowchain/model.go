package projectworkflowchain

import "time"

const (
	ChainStatusPlanned              = "planned"
	ChainStatusQueued               = "queued"
	ChainStatusCompleted            = "completed"
	ChainStatusPostValidationPassed = "post_validation_passed"
	ChainStatusBlocked              = "blocked"
)

const (
	StageStatusPlanned   = "planned"
	StageStatusQueued    = "queued"
	StageStatusCompleted = "completed"
	StageStatusBlocked   = "blocked"
)

const (
	InputKindJiraIssueKey = "jira_issue_key"
	InputKindSafeRef      = "safe_ref"

	ContextProviderJira        = "jira"
	ContextProviderConfluence  = "confluence"
	ContextProviderIndexedRepo = "indexed_repo"

	ContextModeLocalIngested = "local_ingested"
	ContextModeIndexed       = "indexed"

	TriggerOnChainStart              = "on_chain_start"
	TriggerAfterStageReviewPassed    = "after_stage_review_passed"
	GitOpsModeDraftPRAfterValidation = "draft_pr_after_post_validation"
)

type Config struct {
	ProjectID            string        `json:"project_id"`
	ChainRef             string        `json:"chain_ref"`
	Enabled              bool          `json:"enabled"`
	InputKind            string        `json:"input_kind,omitempty"`
	InputPattern         string        `json:"input_pattern,omitempty"`
	ContextProvider      string        `json:"context_provider,omitempty"`
	ContextMode          string        `json:"context_mode,omitempty"`
	DefaultTitleTemplate string        `json:"default_title_template,omitempty"`
	GitOpsMode           string        `json:"gitops_mode,omitempty"`
	GitOpsEnabled        bool          `json:"gitops_enabled,omitempty"`
	Stages               []StageConfig `json:"stages,omitempty"`
}

type StageConfig struct {
	StageRef                 string   `json:"stage_ref"`
	WorkflowRef              string   `json:"workflow_ref"`
	Trigger                  string   `json:"trigger,omitempty"`
	DependsOn                []string `json:"depends_on,omitempty"`
	AutomationRefTemplate    string   `json:"automation_ref_template,omitempty"`
	RequiredStatusBeforeNext string   `json:"required_status_before_next,omitempty"`
}

type ChainRun struct {
	ID             string     `json:"chain_run_id"`
	ProjectID      string     `json:"project_id"`
	ChainRef       string     `json:"chain_ref"`
	InputRef       string     `json:"input_ref"`
	Status         string     `json:"status"`
	ContextRefs    []string   `json:"context_refs,omitempty"`
	StageRuns      []StageRun `json:"stage_runs,omitempty"`
	WorkPlanIDs    []string   `json:"work_plan_ids,omitempty"`
	AutomationIDs  []string   `json:"automation_ids,omitempty"`
	CreatedByRunID string     `json:"created_by_run_id,omitempty"`
	TraceID        string     `json:"trace_id,omitempty"`
	GitOpsReady    bool       `json:"gitops_ready,omitempty"`
	NextAction     string     `json:"next_action,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type StageRun struct {
	StageRef      string    `json:"stage_ref"`
	WorkflowRef   string    `json:"workflow_ref"`
	WorkflowID    string    `json:"workflow_id,omitempty"`
	Status        string    `json:"status"`
	WorkPlanID    string    `json:"work_plan_id,omitempty"`
	WorkTaskIDs   []string  `json:"work_task_ids,omitempty"`
	AutomationIDs []string  `json:"automation_ids,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	CompletedAt   time.Time `json:"completed_at,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
}

type StartInput struct {
	ProjectID      string
	ChainRef       string
	InputText      string
	CreatedByRunID string
	TraceID        string
	DryRun         bool
}

type StartResult struct {
	ProjectID     string     `json:"project_id"`
	ChainRef      string     `json:"chain_ref"`
	InputRef      string     `json:"input_ref"`
	Status        string     `json:"status"`
	ChainRunID    string     `json:"chain_run_id,omitempty"`
	StageRuns     []StageRun `json:"stage_runs,omitempty"`
	WorkPlanIDs   []string   `json:"work_plan_ids,omitempty"`
	AutomationIDs []string   `json:"automation_ids,omitempty"`
	DryRun        bool       `json:"dry_run,omitempty"`
	NextAction    string     `json:"next_action"`
}

type ChainFilter struct {
	ProjectID string
	ChainRef  string
	Status    string
}

type ListResult struct {
	Chains []Config   `json:"chains,omitempty"`
	Runs   []ChainRun `json:"runs,omitempty"`
}
