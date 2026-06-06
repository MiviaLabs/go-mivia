package projectautomation

import (
	"context"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

const (
	AutomationStatusDraft      = "draft"
	AutomationStatusEnabled    = "enabled"
	AutomationStatusDisabled   = "disabled"
	AutomationStatusRunning    = "running"
	AutomationStatusPaused     = "paused"
	AutomationStatusFailed     = "failed"
	AutomationStatusCancelled  = "cancelled"
	AutomationStatusSuperseded = "superseded"
)

const (
	RunStatusQueued            = "queued"
	RunStatusClaiming          = "claiming"
	RunStatusStarting          = "starting"
	RunStatusRunning           = "running"
	RunStatusVerifying         = "verifying"
	RunStatusCompleted         = "completed"
	RunStatusFailed            = "failed"
	RunStatusBlocked           = "blocked"
	RunStatusCancelled         = "cancelled"
	RunStatusPolicyDenied      = "policy_denied"
	RunStatusRunnerUnavailable = "runner_unavailable"
	RunStatusTimeout           = "timeout"
)

const RunSafeSummaryGitOpsPostTaskRecovery = "gitops_post_task_recovery"
const RunSafeSummaryGitOpsRecoveryRequeuedImplementation = "gitops_recovery_requeued_implementation"
const RunSafeSummaryPostImplementationReviewQueued = "post_implementation_review_queued"
const RunSafeSummaryVerifiedTaskDone = "external_codex_cli_verified_task_done"

const (
	BatchStatusPlanned   = "planned"
	BatchStatusRunning   = "running"
	BatchStatusCompleted = "completed"
	BatchStatusFailed    = "failed"
	BatchStatusBlocked   = "blocked"
	BatchStatusCancelled = "cancelled"
)

const (
	RunnerKindCodexCLI = "codex_cli"
	RunnerKindManual   = "manual"
)

const (
	RunnerExecutionInProcess = "in_process"
	RunnerExecutionExternal  = "external"
	RunnerExecutionManaged   = "managed"
)

const (
	TriggerKindManual    = "manual"
	TriggerKindAutomatic = "automatic"
)

const (
	AutomationSourceManual   = "manual"
	AutomationSourceWorkflow = "workflow"
)

const PermissionSnapshotRefPrefix = "permission_snapshot:"

type Automation struct {
	ID                    string    `json:"id"`
	ProjectID             string    `json:"project_id"`
	AutomationRef         string    `json:"automation_ref"`
	Title                 string    `json:"title"`
	Purpose               string    `json:"purpose"`
	Status                string    `json:"status"`
	AgentID               string    `json:"agent_id"`
	PlanID                string    `json:"plan_id,omitempty"`
	AllowedTaskRefs       []string  `json:"allowed_task_refs,omitempty"`
	RequiredReviewTaskIDs []string  `json:"required_review_task_ids,omitempty"`
	TriggerKind           string    `json:"trigger_kind"`
	SourceKind            string    `json:"source_kind,omitempty"`
	SchedulePolicy        string    `json:"schedule_policy,omitempty"`
	PermissionRef         string    `json:"permission_ref"`
	CreatedByRunID        string    `json:"created_by_run_id,omitempty"`
	TraceID               string    `json:"trace_id,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type AutomationAgent struct {
	ID              string        `json:"id"`
	DisplayName     string        `json:"display_name"`
	Purpose         string        `json:"purpose"`
	Enabled         bool          `json:"enabled"`
	AllowedSkills   []string      `json:"allowed_skills,omitempty"`
	AllowedTools    []string      `json:"allowed_tools,omitempty"`
	AllowedCommands []CommandSpec `json:"allowed_commands,omitempty"`
	DeniedCommands  []string      `json:"denied_commands,omitempty"`
	WorkspaceMode   string        `json:"workspace_mode"`
	NetworkPolicy   string        `json:"network_policy"`
	SecretPolicy    string        `json:"secret_policy"`
	LogPolicy       string        `json:"log_policy"`
	MaxRuntime      time.Duration `json:"max_runtime"`
	MaxRetries      int           `json:"max_retries"`
}

type CommandSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type AutomationRun struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	AutomationID      string    `json:"automation_id"`
	AgentID           string    `json:"agent_id"`
	PlanID            string    `json:"plan_id"`
	TaskID            string    `json:"task_id"`
	WorkTaskStatus    string    `json:"work_task_status"`
	Status            string    `json:"status"`
	RunnerKind        string    `json:"runner_kind"`
	AgentRunID        string    `json:"agent_run_id,omitempty"`
	TraceID           string    `json:"trace_id,omitempty"`
	AttemptCount      int       `json:"attempt_count"`
	OrchestratorRunID string    `json:"orchestrator_run_id,omitempty"`
	ParentRunID       string    `json:"parent_run_id,omitempty"`
	WorkerRunIDs      []string  `json:"worker_run_ids,omitempty"`
	ParallelGroupID   string    `json:"parallel_group_id,omitempty"`
	FailureCategory   string    `json:"failure_category,omitempty"`
	SafeSummary       string    `json:"safe_summary,omitempty"`
	ClaimID           string    `json:"claim_id,omitempty"`
	RunnerID          string    `json:"runner_id,omitempty"`
	ClaimedAt         time.Time `json:"claimed_at,omitempty"`
	LastHeartbeatAt   time.Time `json:"last_heartbeat_at,omitempty"`
	LeaseExpiresAt    time.Time `json:"lease_expires_at,omitempty"`
	StartedAt         time.Time `json:"started_at,omitempty"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type AutomationAttempt struct {
	ID                 string    `json:"id"`
	ProjectID          string    `json:"project_id"`
	AutomationRunID    string    `json:"automation_run_id"`
	AttemptNumber      int       `json:"attempt_number"`
	RunnerKind         string    `json:"runner_kind"`
	CommandRef         string    `json:"command_ref,omitempty"`
	InputSummaryHash   string    `json:"input_summary_hash,omitempty"`
	OutputSummaryHash  string    `json:"output_summary_hash,omitempty"`
	Status             string    `json:"status"`
	FailureCategory    string    `json:"failure_category,omitempty"`
	DurationMS         int64     `json:"duration_ms"`
	VerifierResultRefs []string  `json:"verifier_result_refs,omitempty"`
	EvidenceRefs       []string  `json:"evidence_refs,omitempty"`
	ClaimRefs          []string  `json:"claim_refs,omitempty"`
	KnowledgeRefs      []string  `json:"knowledge_candidate_refs,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	FinishedAt         time.Time `json:"finished_at,omitempty"`
}

type AutomationParallelBatch struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	AutomationRunID   string    `json:"automation_run_id"`
	OrchestratorRunID string    `json:"orchestrator_run_id"`
	PlanID            string    `json:"plan_id"`
	TaskIDs           []string  `json:"task_ids"`
	Status            string    `json:"status"`
	SafetyReason      string    `json:"safety_reason"`
	ConflictSummary   string    `json:"conflict_summary,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Options struct {
	Enabled                   bool
	RunnerEnabled             bool
	RequireCodexWhenAvailable bool
	AllowManualRunner         bool
	RunnerExecution           string
	MaxParallelTasks          int
	DefaultMaxRuntime         time.Duration
	CodexBinaryPath           string
	Agents                    []AutomationAgent
	PermissionResolver        PermissionResolver
	Governance                GovernanceOptions
	WorkPlanStatusTrigger     WorkPlanStatusTriggerOptions
	DirtyScopeRecovery        DirtyScopeRecoveryOptions
}

type DirtyScopeRecoveryOptions struct {
	AllowedSupportPathspecs []string
	PathspecResolver        func(projectID string) []string
}

type WorkPlanStatusTriggerOptions struct {
	Enabled  bool
	Statuses []string
}

type ExecutorOptions struct {
	Enabled               bool
	RunnerEnabled         bool
	RunnerExecution       string
	PollInterval          time.Duration
	GlobalWorkerCount     int
	PerProjectWorkerLimit int
	PerAgentWorkerLimit   int
	ProjectIDs            []string
}

type CreateAutomationInput struct {
	ProjectID             string   `json:"project_id,omitempty"`
	AutomationRef         string   `json:"automation_ref"`
	Title                 string   `json:"title"`
	Purpose               string   `json:"purpose"`
	Status                string   `json:"status,omitempty"`
	AgentID               string   `json:"agent_id"`
	PlanID                string   `json:"plan_id,omitempty"`
	AllowedTaskRefs       []string `json:"allowed_task_refs,omitempty"`
	RequiredReviewTaskIDs []string `json:"required_review_task_ids,omitempty"`
	TriggerKind           string   `json:"trigger_kind,omitempty"`
	SchedulePolicy        string   `json:"schedule_policy,omitempty"`
	PermissionRef         string   `json:"permission_ref"`
	SourceKind            string   `json:"source_kind,omitempty"`
	CreatedByRunID        string   `json:"created_by_run_id,omitempty"`
	TraceID               string   `json:"trace_id,omitempty"`
}

type UpdateAutomationStatusInput struct {
	ProjectID    string `json:"project_id,omitempty"`
	AutomationID string `json:"automation_id"`
	Status       string `json:"status"`
	RunID        string `json:"run_id,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
}

type AutomationFilter struct {
	ProjectID string `json:"project_id,omitempty"`
	Status    string `json:"status,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

type SubmitRunInput struct {
	ProjectID         string   `json:"project_id,omitempty"`
	AutomationID      string   `json:"automation_id"`
	PlanID            string   `json:"plan_id,omitempty"`
	TaskID            string   `json:"task_id,omitempty"`
	OwnerAgent        string   `json:"owner_agent,omitempty"`
	RunnerKind        string   `json:"runner_kind,omitempty"`
	OrchestratorRunID string   `json:"orchestrator_run_id,omitempty"`
	ParentRunID       string   `json:"parent_run_id,omitempty"`
	EvidenceRefs      []string `json:"evidence_refs,omitempty"`
	VerifierRefs      []string `json:"verifier_result_refs,omitempty"`
	SafeNextAction    string   `json:"safe_next_action,omitempty"`
}

type CreateRemediationFromFindingInput struct {
	ProjectID               string   `json:"project_id,omitempty"`
	FindingRef              string   `json:"finding_ref"`
	FindingStatus           string   `json:"finding_status"`
	Title                   string   `json:"title"`
	Summary                 string   `json:"summary"`
	Severity                string   `json:"severity,omitempty"`
	OwnerAgent              string   `json:"owner_agent,omitempty"`
	ImplementationAgentID   string   `json:"implementation_agent_id,omitempty"`
	CreatedByRunID          string   `json:"created_by_run_id,omitempty"`
	TraceID                 string   `json:"trace_id,omitempty"`
	GitBaseRef              string   `json:"git_base_ref,omitempty"`
	GitBranchRef            string   `json:"git_branch_ref,omitempty"`
	GitWorktreeRef          string   `json:"git_worktree_ref,omitempty"`
	FilesToRead             []string `json:"files_to_read,omitempty"`
	FilesToEdit             []string `json:"files_to_edit,omitempty"`
	LikelyFilesAffected     []string `json:"likely_files_affected,omitempty"`
	EvidenceRefs            []string `json:"evidence_refs,omitempty"`
	VerificationRequirement string   `json:"verification_requirement"`
	ReviewGate              string   `json:"review_gate,omitempty"`
	ActivatePlan            bool     `json:"activate_plan,omitempty"`
}

type CreateRemediationFromFindingResult struct {
	WorkPlan         projectworkplan.WorkPlan `json:"work_plan"`
	WorkTask         projectworkplan.WorkTask `json:"work_task"`
	ReviewTask       projectworkplan.WorkTask `json:"review_task,omitempty"`
	Automation       Automation               `json:"automation"`
	ReviewAutomation Automation               `json:"review_automation,omitempty"`
	Activated        bool                     `json:"activated"`
}

type RunFilter struct {
	ProjectID         string `json:"project_id,omitempty"`
	AutomationID      string `json:"automation_id,omitempty"`
	PlanID            string `json:"plan_id,omitempty"`
	Status            string `json:"status,omitempty"`
	OrchestratorRunID string `json:"orchestrator_run_id,omitempty"`
}

type ComputeParallelBatchInput struct {
	ProjectID         string   `json:"project_id,omitempty"`
	AutomationRunID   string   `json:"automation_run_id,omitempty"`
	OrchestratorRunID string   `json:"orchestrator_run_id,omitempty"`
	PlanID            string   `json:"plan_id,omitempty"`
	TaskIDs           []string `json:"task_ids,omitempty"`
	MaxTasks          int      `json:"max_tasks,omitempty"`
}

type ClaimNextRunInput struct {
	ProjectID  string `json:"project_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	RunnerKind string `json:"runner_kind,omitempty"`
	RunnerID   string `json:"runner_id,omitempty"`
}

type ClaimedRun struct {
	Run        AutomationRun  `json:"run"`
	CodexInput CodexTaskInput `json:"codex_input"`
	TimeoutMS  int64          `json:"timeout_ms"`
}

type CompleteAttemptInput struct {
	ProjectID          string   `json:"project_id,omitempty"`
	RunID              string   `json:"run_id"`
	ClaimID            string   `json:"claim_id,omitempty"`
	RunnerID           string   `json:"runner_id,omitempty"`
	Status             string   `json:"status"`
	FailureCategory    string   `json:"failure_category,omitempty"`
	DurationMS         int64    `json:"duration_ms,omitempty"`
	VerifierResultRefs []string `json:"verifier_result_refs,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ClaimRefs          []string `json:"claim_refs,omitempty"`
	ReviewRefs         []string `json:"review_result_refs,omitempty"`
	KnowledgeRefs      []string `json:"knowledge_candidate_refs,omitempty"`
}

type HeartbeatRunInput struct {
	ProjectID string `json:"project_id,omitempty"`
	RunID     string `json:"run_id"`
	ClaimID   string `json:"claim_id"`
	RunnerID  string `json:"runner_id,omitempty"`
}

type PermissionCheckInput struct {
	ProjectID       string
	AutomationID    string
	AutomationRef   string
	AgentID         string
	PermissionRef   string
	RunnerKind      string
	RunnerExecution string
}

type PermissionSnapshotMetadata struct {
	PermissionRef      string
	AgentID            string
	AllowedRunnerKinds []string
	DeniedCommands     []string
}

type PermissionResolver interface {
	CheckAutomationPermission(context.Context, PermissionCheckInput) (PermissionSnapshotMetadata, error)
}
