package projectworkplan

import "time"

const (
	WorkPlanStatusPlanned     = "planned"
	WorkPlanStatusActive      = "active"
	WorkPlanStatusBlocked     = "blocked"
	WorkPlanStatusNeedsReview = "needs_review"
	WorkPlanStatusDone        = "done"
	WorkPlanStatusFailed      = "failed"
	WorkPlanStatusCancelled   = "cancelled"
	WorkPlanStatusSuperseded  = "superseded"
)

const (
	WorkPlanIsolationShared            = "shared"
	WorkPlanIsolationDedicatedWorktree = "dedicated_worktree"
	WorkPlanIsolationUnavailable       = "unavailable"
)

const (
	WorkTaskStatusPlanned     = "planned"
	WorkTaskStatusReady       = "ready"
	WorkTaskStatusClaimed     = "claimed"
	WorkTaskStatusInProgress  = "in_progress"
	WorkTaskStatusBlocked     = "blocked"
	WorkTaskStatusNeedsReview = "needs_review"
	WorkTaskStatusVerifying   = "verifying"
	WorkTaskStatusDone        = "done"
	WorkTaskStatusFailed      = "failed"
	WorkTaskStatusCancelled   = "cancelled"
	WorkTaskStatusSuperseded  = "superseded"
)

const (
	DecompositionDraft               = "draft"
	DecompositionReady               = "ready"
	DecompositionTooBroad            = "too_broad"
	DecompositionMissingEvidence     = "missing_evidence"
	DecompositionMissingContext      = "missing_context"
	DecompositionMissingVerification = "missing_verification"
	DecompositionMissingResume       = "missing_resume"
)

type WorkPlan struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	PlanRef          string    `json:"plan_ref"`
	UserRequestRef   string    `json:"user_request_ref,omitempty"`
	Title            string    `json:"title"`
	GoalSummary      string    `json:"goal_summary"`
	Status           string    `json:"status"`
	OwnerAgent       string    `json:"owner_agent,omitempty"`
	CreatedByRunID   string    `json:"created_by_run_id,omitempty"`
	TraceID          string    `json:"trace_id,omitempty"`
	CurrentTaskID    string    `json:"current_task_id,omitempty"`
	ResumeSummary    string    `json:"resume_summary,omitempty"`
	Outcome          string    `json:"outcome,omitempty"`
	IsolationMode    string    `json:"isolation_mode,omitempty"`
	ParallelGroupRef string    `json:"parallel_group_ref,omitempty"`
	WorkspaceRef     string    `json:"workspace_ref,omitempty"`
	GitBaseRef       string    `json:"git_base_ref,omitempty"`
	GitBranchRef     string    `json:"git_branch_ref,omitempty"`
	GitWorktreeRef   string    `json:"git_worktree_ref,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type WorkTask struct {
	ID                      string    `json:"id"`
	ProjectID               string    `json:"project_id"`
	PlanID                  string    `json:"plan_id"`
	TaskRef                 string    `json:"task_ref"`
	Title                   string    `json:"title"`
	Description             string    `json:"description,omitempty"`
	Status                  string    `json:"status"`
	OwnerAgent              string    `json:"owner_agent,omitempty"`
	ClaimedByRunID          string    `json:"claimed_by_run_id,omitempty"`
	TraceID                 string    `json:"trace_id,omitempty"`
	EvidenceNeeded          []string  `json:"evidence_needed,omitempty"`
	ContextPackRefs         []string  `json:"context_pack_refs,omitempty"`
	FilesToRead             []string  `json:"files_to_read,omitempty"`
	FilesToEdit             []string  `json:"files_to_edit,omitempty"`
	LikelyFilesAffected     []string  `json:"likely_files_affected,omitempty"`
	DependencyTaskIDs       []string  `json:"dependency_task_ids,omitempty"`
	VerificationRequirement string    `json:"verification_requirement"`
	GitOpsVerificationMode  string    `json:"gitops_verification_mode,omitempty"`
	ExpectedOutput          string    `json:"expected_output,omitempty"`
	FailureCriteria         string    `json:"failure_criteria,omitempty"`
	ReviewGate              string    `json:"review_gate,omitempty"`
	Outcome                 string    `json:"outcome,omitempty"`
	ResumeInstructions      string    `json:"resume_instructions,omitempty"`
	BlockedReason           string    `json:"blocked_reason,omitempty"`
	BlockedByTaskIDs        []string  `json:"blocked_by_task_ids,omitempty"`
	KnowledgeCandidateRefs  []string  `json:"knowledge_candidate_refs,omitempty"`
	EvidenceRefs            []string  `json:"evidence_refs,omitempty"`
	ClaimRefs               []string  `json:"claim_refs,omitempty"`
	VerifierResultRefs      []string  `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs        []string  `json:"review_result_refs,omitempty"`
	ReviewExemptReason      string    `json:"review_exempt_reason,omitempty"`
	ArtifactRefs            []string  `json:"artifact_refs,omitempty"`
	AgentRunIDs             []string  `json:"agent_run_ids,omitempty"`
	DecompositionQuality    string    `json:"decomposition_quality"`
	AcceptanceCriteria      []string  `json:"acceptance_criteria,omitempty"`
	StopConditions          []string  `json:"stop_conditions,omitempty"`
	VerifierLadder          []string  `json:"verifier_ladder,omitempty"`
	RegressionApplicability string    `json:"regression_test_applicability,omitempty"`
	DownstreamImpactRefs    []string  `json:"downstream_impact_refs,omitempty"`
	OutputContract          string    `json:"output_contract,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	ClaimedAt               time.Time `json:"claimed_at,omitempty"`
	StartedAt               time.Time `json:"started_at,omitempty"`
	CompletedAt             time.Time `json:"completed_at,omitempty"`
}

type Attachment struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	PlanID          string    `json:"plan_id"`
	TaskID          string    `json:"task_id"`
	Kind            string    `json:"kind"`
	Ref             string    `json:"ref"`
	AttachedByRunID string    `json:"attached_by_run_id,omitempty"`
	TraceID         string    `json:"trace_id,omitempty"`
	Note            string    `json:"note,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type CreateWorkPlanInput struct {
	ProjectID        string `json:"project_id,omitempty"`
	PlanRef          string `json:"plan_ref"`
	UserRequestRef   string `json:"user_request_ref,omitempty"`
	Title            string `json:"title"`
	GoalSummary      string `json:"goal_summary"`
	OwnerAgent       string `json:"owner_agent,omitempty"`
	CreatedByRunID   string `json:"created_by_run_id,omitempty"`
	TraceID          string `json:"trace_id,omitempty"`
	ResumeSummary    string `json:"resume_summary,omitempty"`
	IsolationMode    string `json:"isolation_mode,omitempty"`
	ParallelGroupRef string `json:"parallel_group_ref,omitempty"`
	WorkspaceRef     string `json:"workspace_ref,omitempty"`
	GitBaseRef       string `json:"git_base_ref,omitempty"`
	GitBranchRef     string `json:"git_branch_ref,omitempty"`
	GitWorktreeRef   string `json:"git_worktree_ref,omitempty"`
}

type WorkPlanFilter struct {
	ProjectID  string `json:"project_id,omitempty"`
	Status     string `json:"status,omitempty"`
	OwnerAgent string `json:"owner_agent,omitempty"`
	PageSize   int    `json:"page_size,omitempty"`
	PageToken  string `json:"page_token,omitempty"`
}

type UpdateWorkPlanStatusInput struct {
	ProjectID      string `json:"project_id,omitempty"`
	PlanID         string `json:"plan_id"`
	Status         string `json:"status"`
	SafeNextAction string `json:"safe_next_action,omitempty"`
	ResumeSummary  string `json:"resume_summary,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	CurrentTaskID  string `json:"current_task_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
}

type ResumeWorkPlanInput struct {
	ProjectID string `json:"project_id,omitempty"`
	PlanID    string `json:"plan_id"`
}

type CreateWorkTaskInput struct {
	ProjectID               string   `json:"project_id,omitempty"`
	PlanID                  string   `json:"plan_id"`
	TaskRef                 string   `json:"task_ref"`
	Title                   string   `json:"title"`
	Description             string   `json:"description,omitempty"`
	Status                  string   `json:"status,omitempty"`
	OwnerAgent              string   `json:"owner_agent,omitempty"`
	RunID                   string   `json:"run_id,omitempty"`
	TraceID                 string   `json:"trace_id,omitempty"`
	EvidenceNeeded          []string `json:"evidence_needed,omitempty"`
	ContextPackRefs         []string `json:"context_pack_refs,omitempty"`
	FilesToRead             []string `json:"files_to_read,omitempty"`
	FilesToEdit             []string `json:"files_to_edit,omitempty"`
	LikelyFilesAffected     []string `json:"likely_files_affected,omitempty"`
	DependencyTaskIDs       []string `json:"dependency_task_ids,omitempty"`
	VerificationRequirement string   `json:"verification_requirement"`
	GitOpsVerificationMode  string   `json:"gitops_verification_mode,omitempty"`
	ExpectedOutput          string   `json:"expected_output,omitempty"`
	FailureCriteria         string   `json:"failure_criteria,omitempty"`
	ReviewGate              string   `json:"review_gate,omitempty"`
	ResumeInstructions      string   `json:"resume_instructions,omitempty"`
	KnowledgeCandidateRefs  []string `json:"knowledge_candidate_refs,omitempty"`
	DecompositionQuality    string   `json:"decomposition_quality,omitempty"`
	AcceptanceCriteria      []string `json:"acceptance_criteria,omitempty"`
	StopConditions          []string `json:"stop_conditions,omitempty"`
	VerifierLadder          []string `json:"verifier_ladder,omitempty"`
	RegressionApplicability string   `json:"regression_test_applicability,omitempty"`
	DownstreamImpactRefs    []string `json:"downstream_impact_refs,omitempty"`
	OutputContract          string   `json:"output_contract,omitempty"`
}

type WorkTaskFilter struct {
	ProjectID      string `json:"project_id,omitempty"`
	PlanID         string `json:"plan_id,omitempty"`
	Status         string `json:"status,omitempty"`
	OwnerAgent     string `json:"owner_agent,omitempty"`
	ClaimedByRunID string `json:"claimed_by_run_id,omitempty"`
	PageSize       int    `json:"page_size,omitempty"`
	PageToken      string `json:"page_token,omitempty"`
}

type WorkTaskActionInput struct {
	ProjectID          string   `json:"project_id,omitempty"`
	TaskID             string   `json:"task_id"`
	OwnerAgent         string   `json:"owner_agent,omitempty"`
	RunID              string   `json:"run_id,omitempty"`
	TraceID            string   `json:"trace_id,omitempty"`
	Outcome            string   `json:"outcome,omitempty"`
	SafeNextAction     string   `json:"safe_next_action,omitempty"`
	BlockedReason      string   `json:"blocked_reason,omitempty"`
	BlockedByTaskIDs   []string `json:"blocked_by_task_ids,omitempty"`
	ContextPackRefs    []string `json:"context_pack_refs,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ClaimRefs          []string `json:"claim_refs,omitempty"`
	KnowledgeRefs      []string `json:"knowledge_candidate_refs,omitempty"`
	ResumeInstructions string   `json:"resume_instructions,omitempty"`
	VerifierResultRefs []string `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs   []string `json:"review_result_refs,omitempty"`
	ReviewExemptReason string   `json:"review_exempt_reason,omitempty"`
}

type UpdateWorkTaskStatusInput struct {
	WorkTaskActionInput
	Status string `json:"status"`
}

type ExpandWorkTaskScopeInput struct {
	ProjectID          string   `json:"project_id,omitempty"`
	TaskID             string   `json:"task_id"`
	FilesToEdit        []string `json:"files_to_edit,omitempty"`
	ResumeInstructions string   `json:"resume_instructions,omitempty"`
	RunID              string   `json:"run_id,omitempty"`
	TraceID            string   `json:"trace_id,omitempty"`
}

type GetNextWorkTaskInput struct {
	ProjectID          string `json:"project_id,omitempty"`
	PlanID             string `json:"plan_id,omitempty"`
	OwnerAgent         string `json:"owner_agent,omitempty"`
	RunID              string `json:"run_id,omitempty"`
	TraceID            string `json:"trace_id,omitempty"`
	IncludeClaimedByMe bool   `json:"include_claimed_by_me,omitempty"`
}

type DependencySummary struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
}

type GetNextWorkTaskResult struct {
	Found                bool                `json:"found"`
	Task                 WorkTask            `json:"task,omitempty"`
	Plan                 WorkPlan            `json:"plan,omitempty"`
	DependencySummary    []DependencySummary `json:"dependency_summary,omitempty"`
	EvidenceRefs         []string            `json:"evidence_refs,omitempty"`
	ContextPackRefs      []string            `json:"context_pack_refs,omitempty"`
	ResumeInstructions   string              `json:"resume_instructions,omitempty"`
	RequiredVerification string              `json:"required_verification,omitempty"`
	SafeReason           string              `json:"safe_reason,omitempty"`
	OpenCount            int                 `json:"open_count"`
	BlockedCount         int                 `json:"blocked_count"`
	ClaimedCount         int                 `json:"claimed_count"`
	Reason               string              `json:"reason,omitempty"`
}

type AttachInput struct {
	ProjectID       string `json:"project_id,omitempty"`
	TaskID          string `json:"task_id"`
	Ref             string `json:"ref"`
	AttachedByRunID string `json:"attached_by_run_id,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	Note            string `json:"note,omitempty"`
}
