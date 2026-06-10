package projectdurable

import "context"

// Ports for the durable pilot phases (Phases 3-5). Durable activities call
// the CURRENT services only through these narrow interfaces: observe-only
// ports for the shadow phases, plus the Phase 4 test-only execution ports at
// the bottom of this file.
// This file defines no implementations and imports no other internal
// packages: ports are expressed purely in terms of the metadata-only DTOs in
// this package, context, and plain strings, so the durable pilot stays
// decoupled. Adapters live beside the current services and arrive in later
// phases. Activities may only pass and receive metadata - never prompts,
// completions, stderr, source, roots, secrets, or PII.

// AutomationRunObserver loads a metadata-only snapshot of an automation run.
// Implementations live beside the projectautomation service; they must
// return only ids, refs, statuses, counters, and bounded safe summaries.
type AutomationRunObserver interface {
	LoadRunSnapshot(ctx context.Context, ref SafeAutomationRunRef) (DurableRunSnapshot, error)
}

// SafeTaskSnapshot is the metadata-only view of a work task that durable
// activities are allowed to observe. Refs groups ref slices by kind (for
// example "evidence_refs", "verifier_result_refs"); every key and every
// entry must pass ValidateSafeRef.
type SafeTaskSnapshot struct {
	Ref    SafeWorkTaskRef     `json:"ref"`
	Status string              `json:"status"`
	Refs   map[string][]string `json:"refs,omitempty"`
}

// WorkTaskObserver loads a metadata-only snapshot of a work task.
// Implementations live beside the projectworkplan service; they must return
// only ids, refs, and statuses.
type WorkTaskObserver interface {
	LoadTaskStatus(ctx context.Context, ref SafeWorkTaskRef) (SafeTaskSnapshot, error)
}

// SafeStageSnapshot is the metadata-only view of a workflow chain stage that
// durable activities are allowed to observe. BlockedReason and NextAction
// must pass ValidateSafeSummary.
type SafeStageSnapshot struct {
	Ref           SafeWorkflowStageRef `json:"ref"`
	Status        string               `json:"status"`
	BlockedReason string               `json:"blocked_reason,omitempty"`
	NextAction    string               `json:"next_action,omitempty"`
}

// ChainStageObserver loads a metadata-only snapshot of a workflow chain
// stage. Implementations live beside the projectworkflowchain service; they
// must return only ids, refs, statuses, and bounded safe summaries.
type ChainStageObserver interface {
	LoadStageStatus(ctx context.Context, ref SafeWorkflowStageRef) (SafeStageSnapshot, error)
}

// ShadowComparisonWriter records shadow-mode comparison fields for one
// automation run. Every value in fields must pass ValidateSafeSummary (and
// keys must pass ValidateSafeRef); implementations live beside the current
// services and must reject anything else fail-closed.
type ShadowComparisonWriter interface {
	WriteShadowComparison(ctx context.Context, runRef SafeAutomationRunRef, fields map[string]string) error
}

// Execution ports (Phase 4, test-only durable execution). Like the observe
// ports above, these are metadata-only: ids, refs, statuses, counters, and
// bounded safe summaries cross the boundary - never prompts, completions,
// stderr, source, roots, secrets, or PII. Until cutover approval the ONLY
// implementations are test adapters that drive the CURRENT
// projectautomation.Service; durable code never imports that service, and no
// production configuration constructs these ports.

// DurableAttemptOutcome is the metadata-only completion report a durable
// activity hands to the AttemptCompletionPort. It mirrors the safe subset of
// the current CompleteAttemptInput contract (status, claim/runner ids, safe
// failure category, bounded summary, ref lists) without importing it.
type DurableAttemptOutcome struct {
	Status                 string                 `json:"status"`
	ClaimID                string                 `json:"claim_id,omitempty"`
	RunnerID               string                 `json:"runner_id,omitempty"`
	FailureCategory        DurableFailureCategory `json:"failure_category,omitempty"`
	SafeSummary            string                 `json:"safe_summary,omitempty"`
	EvidenceRefs           []string               `json:"evidence_refs,omitempty"`
	VerifierResultRefs     []string               `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs       []string               `json:"review_result_refs,omitempty"`
	ClaimRefs              []string               `json:"claim_refs,omitempty"`
	KnowledgeCandidateRefs []string               `json:"knowledge_candidate_refs,omitempty"`
}

// Validate applies the local safe-ref and safe-summary rules to every field,
// fail-closed: an outcome that does not validate must never reach a port.
func (o DurableAttemptOutcome) Validate() error {
	if err := validateRequiredRef(o.Status, "status"); err != nil {
		return err
	}
	if err := validateOptionalRef(o.ClaimID, "claim_id"); err != nil {
		return err
	}
	if err := validateOptionalRef(o.RunnerID, "runner_id"); err != nil {
		return err
	}
	if err := o.FailureCategory.Validate(); err != nil {
		return err
	}
	if err := ValidateSafeSummary(o.SafeSummary); err != nil {
		return err
	}
	for name, refs := range map[string][]string{
		"evidence_refs":            o.EvidenceRefs,
		"verifier_result_refs":     o.VerifierResultRefs,
		"review_result_refs":       o.ReviewResultRefs,
		"claim_refs":               o.ClaimRefs,
		"knowledge_candidate_refs": o.KnowledgeCandidateRefs,
	} {
		if err := validateRefSlice(refs, name); err != nil {
			return err
		}
	}
	return nil
}

// WorkTaskClaimPort claims the referenced automation run for execution on
// behalf of runnerID and returns the post-claim run snapshot plus the opaque
// claim token. Test adapters implement it over the current service's
// ClaimNextRun; they must fail when the claimed run is not the referenced
// run, and must return metadata only.
type WorkTaskClaimPort interface {
	ClaimRun(ctx context.Context, ref SafeAutomationRunRef, runnerID string) (DurableRunSnapshot, string, error)
}

// AttemptCompletionPort reports one attempt outcome for the referenced run
// and returns the post-completion run snapshot. Test adapters implement it
// over the current service's CompleteAttempt and must return metadata only.
type AttemptCompletionPort interface {
	CompleteAttempt(ctx context.Context, ref SafeAutomationRunRef, outcome DurableAttemptOutcome) (DurableRunSnapshot, error)
}

// SafeChainStagePlan is the metadata-only stage descriptor a durable chain
// workflow may consume. Production durable code knows only stage/workflow refs;
// test adapters resolve these from the current workflow-chain config.
type SafeChainStagePlan struct {
	StageRef    string `json:"stage_ref"`
	WorkflowRef string `json:"workflow_ref"`
}

// Validate checks both refs fail-closed.
func (p SafeChainStagePlan) Validate() error {
	if err := validateRequiredRef(p.StageRef, "stage_ref"); err != nil {
		return err
	}
	return validateRequiredRef(p.WorkflowRef, "workflow_ref")
}

// SafeStageCompileOutcome is the metadata-only result of compiling one chain
// stage. Test adapters implement compile-or-get idempotency: when a stage has
// already been compiled for the same chain ref, CompileStage returns the
// existing outcome instead of creating duplicate Work Plans, tasks,
// automations, or permission snapshots.
type SafeStageCompileOutcome struct {
	PlanID                string   `json:"plan_id"`
	TaskIDs               []string `json:"task_ids,omitempty"`
	ReviewerTaskIDs       []string `json:"reviewer_task_ids,omitempty"`
	AutomationIDs         []string `json:"automation_ids,omitempty"`
	PermissionSnapshotIDs []string `json:"permission_snapshot_ids,omitempty"`
	ContextRefs           []string `json:"context_refs,omitempty"`
}

// Validate applies safe-ref checks to the plan id and every returned ref.
func (o SafeStageCompileOutcome) Validate() error {
	if err := validateRequiredRef(o.PlanID, "plan_id"); err != nil {
		return err
	}
	for name, refs := range map[string][]string{
		"task_ids":                o.TaskIDs,
		"reviewer_task_ids":       o.ReviewerTaskIDs,
		"automation_ids":          o.AutomationIDs,
		"permission_snapshot_ids": o.PermissionSnapshotIDs,
		"context_refs":            o.ContextRefs,
	} {
		if err := validateRefSlice(refs, name); err != nil {
			return err
		}
	}
	return nil
}

// ChainPipelinePort is the cohesive metadata-only port used by the Phase 5
// workflow-chain shadow. Implementations are test adapters over the CURRENT
// workflow, work-plan, automation, and workflow-chain services until cutover
// approval. CompileStage must be compile-or-get idempotent for the tuple
// (project_id, chain_ref, stage_ref) so durable resume never duplicates
// current-system state.
type ChainPipelinePort interface {
	ResolveChainStages(ctx context.Context, projectID, chainRef string) ([]SafeChainStagePlan, error)
	CompileStage(ctx context.Context, projectID, chainRef string, stage SafeChainStagePlan, inputRef string, carriedTaskIDs []string) (SafeStageCompileOutcome, error)
	ReleaseCompiledTasks(ctx context.Context, planID string) error
	ActivateWorkPlan(ctx context.Context, planID string) error
	ObserveStagePlanStatus(ctx context.Context, planID string) (status string, taskStatuses map[string]string, err error)
	CarryForwardOutputs(ctx context.Context, fromPlanID, toStageRef string) (concreteTaskIDs []string, err error)
	ObserveGitOps(ctx context.Context, chainRef string) (gitOpsReady bool, prRef string, recoveryStatus string, blockedReason string, err error)
}

// ChainRunComparator loads the current workflow-chain run read model as safe
// comparison fields. It is separate from ChainPipelinePort so tests can prove
// comparison-last behavior even when no current chain run exists.
type ChainRunComparator interface {
	LoadCurrentChainRun(ctx context.Context, projectID, chainRef string) (fields map[string]string, found bool, err error)
}
