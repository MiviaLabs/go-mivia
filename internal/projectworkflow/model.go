package projectworkflow

import "time"

const (
	WorkflowStatusDraft      = "draft"
	WorkflowStatusEnabled    = "enabled"
	WorkflowStatusDisabled   = "disabled"
	WorkflowStatusSuperseded = "superseded"
)

const (
	WorkflowStepKindWorkPlan        = "work_plan"
	WorkflowStepKindWorkTask        = "work_task"
	WorkflowStepKindAutomation      = "automation"
	WorkflowStepKindAutomationBatch = "automation_batch"
	WorkflowStepKindReviewGate      = "review_gate"
)

const (
	ReviewGateDecisionApproved     = "approved"
	ReviewGateDecisionRejected     = "rejected"
	ReviewGateDecisionNeedsChanges = "needs_changes"
	ReviewGateDecisionBlocked      = "blocked"
)

type WorkflowDefinition struct {
	ID                  string                       `json:"id"`
	ProjectID           string                       `json:"project_id"`
	WorkflowRef         string                       `json:"workflow_ref"`
	Title               string                       `json:"title"`
	Purpose             string                       `json:"purpose"`
	Status              string                       `json:"status"`
	Agents              []WorkflowAgentDefinition    `json:"agents,omitempty"`
	Steps               []WorkflowStep               `json:"steps,omitempty"`
	ReviewGates         []WorkflowReviewGate         `json:"review_gates,omitempty"`
	PermissionSnapshots []WorkflowPermissionSnapshot `json:"permission_snapshots,omitempty"`
	CreatedByRunID      string                       `json:"created_by_run_id,omitempty"`
	TraceID             string                       `json:"trace_id,omitempty"`
	CreatedAt           time.Time                    `json:"created_at"`
	UpdatedAt           time.Time                    `json:"updated_at"`
}

type WorkflowAgentDefinition struct {
	ID              string    `json:"id"`
	DisplayName     string    `json:"display_name"`
	Purpose         string    `json:"purpose"`
	AllowedSkills   []string  `json:"allowed_skills,omitempty"`
	AllowedTools    []string  `json:"allowed_tools,omitempty"`
	AllowedCommands []string  `json:"allowed_commands,omitempty"`
	DeniedCommands  []string  `json:"denied_commands,omitempty"`
	WorkspaceMode   string    `json:"workspace_mode,omitempty"`
	NetworkPolicy   string    `json:"network_policy,omitempty"`
	SecretPolicy    string    `json:"secret_policy,omitempty"`
	LogPolicy       string    `json:"log_policy,omitempty"`
	MaxRuntime      string    `json:"max_runtime,omitempty"`
	MaxRetries      int       `json:"max_retries,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type WorkflowPermissionSnapshot struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	AgentID         string    `json:"agent_id"`
	WorkflowID      string    `json:"workflow_id"`
	AllowedSkills   []string  `json:"allowed_skills,omitempty"`
	AllowedTools    []string  `json:"allowed_tools,omitempty"`
	AllowedCommands []string  `json:"allowed_commands,omitempty"`
	DeniedCommands  []string  `json:"denied_commands,omitempty"`
	WorkspaceMode   string    `json:"workspace_mode,omitempty"`
	NetworkPolicy   string    `json:"network_policy,omitempty"`
	SecretPolicy    string    `json:"secret_policy,omitempty"`
	LogPolicy       string    `json:"log_policy,omitempty"`
	MaxRuntime      string    `json:"max_runtime,omitempty"`
	MaxRetries      int       `json:"max_retries,omitempty"`
	ContentHash     string    `json:"content_hash"`
	CreatedByRunID  string    `json:"created_by_run_id,omitempty"`
	TraceID         string    `json:"trace_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type WorkflowStep struct {
	ID                      string   `json:"id"`
	Kind                    string   `json:"kind"`
	Title                   string   `json:"title"`
	Agent                   string   `json:"agent,omitempty"`
	DependsOn               []string `json:"depends_on,omitempty"`
	Description             string   `json:"description,omitempty"`
	EvidenceNeeded          []string `json:"evidence_needed,omitempty"`
	ContextPackRefs         []string `json:"context_pack_refs,omitempty"`
	LikelyFilesAffected     []string `json:"likely_files_affected,omitempty"`
	VerificationRequirement string   `json:"verification_requirement,omitempty"`
	ExpectedOutput          string   `json:"expected_output,omitempty"`
	FailureCriteria         string   `json:"failure_criteria,omitempty"`
	ResumeInstructions      string   `json:"resume_instructions,omitempty"`
	MaxParallelTasks        int      `json:"max_parallel_tasks,omitempty"`
	AutomationStatus        string   `json:"automation_status,omitempty"`
	TriggerKind             string   `json:"trigger_kind,omitempty"`
	SchedulePolicy          string   `json:"schedule_policy,omitempty"`
}

type WorkflowReviewGate struct {
	ID                   string   `json:"id"`
	AppliesTo            []string `json:"applies_to,omitempty"`
	ReviewerAgent        string   `json:"reviewer_agent"`
	Required             bool     `json:"required"`
	IndependentFromOwner bool     `json:"independent_from_owner"`
	RequiredArtifacts    []string `json:"required_artifacts,omitempty"`
	AllowedActions       []string `json:"allowed_actions,omitempty"`
	Instructions         string   `json:"instructions"`
}

type WorkflowValidationIssue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	FieldPath string `json:"field_path,omitempty"`
	Message   string `json:"message"`
}

type WorkflowCompileInput struct {
	ProjectID      string `json:"project_id,omitempty"`
	WorkflowID     string `json:"workflow_id"`
	UserRequestRef string `json:"user_request_ref,omitempty"`
	CreatedByRunID string `json:"created_by_run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	TitleOverride  string `json:"title_override,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type WorkflowCompileResult struct {
	WorkflowID            string                    `json:"workflow_id,omitempty"`
	WorkPlanID            string                    `json:"work_plan_id,omitempty"`
	WorkTaskIDs           []string                  `json:"work_task_ids,omitempty"`
	ReviewerTaskIDs       []string                  `json:"reviewer_task_ids,omitempty"`
	AutomationIDs         []string                  `json:"automation_ids,omitempty"`
	PermissionSnapshotIDs []string                  `json:"permission_snapshot_ids,omitempty"`
	ValidationIssues      []WorkflowValidationIssue `json:"validation_issues,omitempty"`
	DryRun                bool                      `json:"dry_run,omitempty"`
	CompiledAt            time.Time                 `json:"compiled_at,omitempty"`
}
