// Package activities hosts the durable activities for the go-workflows
// pilot: the observe-only shadow activities (Phase 3) and the test-only
// execution activities (Phase 4). Activities touch the current system only
// through the narrow ports defined in internal/projectdurable. The Phase 3
// observe activities write nothing except shadow-comparison metadata; the
// Phase 4 execution activities additionally drive the current service's
// claim/complete contract through ports, but only test adapters implement
// those ports until cutover approval - no production path constructs them.
//
// Shared workflow DTOs (AutomationRunWorkflowInput, ShadowRunTrace) live in
// this package rather than in the workflows package: the
// WriteShadowComparison activity takes the trace as input, so placing the
// types here lets workflows import activities (one clean direction) without
// an import cycle. The workflows package re-exports them via type aliases.
package activities

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
)

// Activity step names recorded in durable traces and shadow comparisons.
// They must pass projectdurable.ValidateSafeRef.
const (
	ActivityLoadRunSnapshot      = "load-run-snapshot"
	ActivityValidateExecutable   = "validate-run-executable"
	ActivityObserveWorkTask      = "observe-work-task"
	ActivityObserveCodexAttempt  = "observe-codex-attempt"
	ActivityObserveCloseout      = "observe-governed-closeout"
	ActivityObserveReviewQueue   = "observe-review-queue"
	ActivityObserveVerifierState = "observe-verifier-state"
	ActivityWriteShadow          = "write-shadow-comparison"
)

// Run status strings mirrored as literals from
// internal/projectautomation/model.go (RunStatus* constants). The durable
// tree must stay decoupled from projectautomation, so the contract values
// are mirrored here with this source comment instead of an import.
const (
	runStatusQueued            = "queued"
	runStatusClaiming          = "claiming"
	runStatusStarting          = "starting"
	runStatusRunning           = "running"
	runStatusVerifying         = "verifying"
	runStatusCompleted         = "completed"
	runStatusPolicyDenied      = "policy_denied"
	runStatusRunnerUnavailable = "runner_unavailable"
	runStatusTimeout           = "timeout"
)

// AutomationRunWorkflowInput is the metadata-only input of the shadow
// workflow. JSON-serializable ids and a shadow flag - never prompts, source,
// roots, or secrets.
type AutomationRunWorkflowInput struct {
	ProjectID    string `json:"project_id"`
	AutomationID string `json:"automation_id"`
	RunID        string `json:"run_id"`
	TaskID       string `json:"task_id,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
	ShadowOnly   bool   `json:"shadow_only"`
}

// ShadowRunTrace is the workflow result: the bounded, safe, metadata-only
// record of every observation step. FinalStatus mirrors the snapshot's run
// status; FailureCategory is the first non-none category any step produced.
type ShadowRunTrace struct {
	Input           AutomationRunWorkflowInput             `json:"input"`
	Steps           []projectdurable.DurableActivityResult `json:"steps"`
	FinalStatus     string                                 `json:"final_status"`
	FailureCategory projectdurable.DurableFailureCategory  `json:"failure_category,omitempty"`
}

// AutomationRunActivities groups the observe-only activities and their
// ports. Register one instance per worker; methods are stateless beyond the
// injected ports and safe for concurrent execution.
type AutomationRunActivities struct {
	Runs   projectdurable.AutomationRunObserver
	Tasks  projectdurable.WorkTaskObserver
	Shadow projectdurable.ShadowComparisonWriter
}

// LoadAutomationRunSnapshot loads the metadata-only run snapshot through the
// Runs port. Both the input ref and the returned snapshot are validated
// fail-closed before entering durable history.
func (a *AutomationRunActivities) LoadAutomationRunSnapshot(ctx context.Context, ref projectdurable.SafeAutomationRunRef) (projectdurable.DurableRunSnapshot, error) {
	if err := ref.Validate(); err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	snap, err := a.Runs.LoadRunSnapshot(ctx, ref)
	if err != nil {
		// Never echo port error content into durable history.
		return projectdurable.DurableRunSnapshot{}, fmt.Errorf("load run snapshot failed")
	}
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	return snap, nil
}

// ValidateAutomationRunExecutable is a pure classification activity: it maps
// the snapshot status and failure category to an activity result without
// touching any port. Executable-in-shadow statuses (queued, claiming,
// starting, running, verifying) classify ok; any snapshot carrying a safe
// failure category - or a terminal/unknown status - classifies blocked.
func (a *AutomationRunActivities) ValidateAutomationRunExecutable(ctx context.Context, snap projectdurable.DurableRunSnapshot) (projectdurable.DurableActivityResult, error) {
	_ = ctx
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	refs := []string{"run-status:" + snap.Status}
	if snap.FailureCategory != projectdurable.FailureCategoryNone {
		return newResult(ActivityValidateExecutable, projectdurable.ActivityStatusBlocked, snap.FailureCategory,
			"run carries a safe failure category", refs)
	}
	switch snap.Status {
	case runStatusQueued, runStatusClaiming, runStatusStarting, runStatusRunning, runStatusVerifying:
		return newResult(ActivityValidateExecutable, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
			"run is executable in shadow scope", refs)
	case runStatusPolicyDenied:
		return newResult(ActivityValidateExecutable, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryPolicyDenied,
			"run is policy denied", refs)
	case runStatusRunnerUnavailable:
		return newResult(ActivityValidateExecutable, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryRunnerUnavailable,
			"runner unavailable", refs)
	case runStatusTimeout:
		return newResult(ActivityValidateExecutable, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryTimeout,
			"run timed out", refs)
	default:
		return newResult(ActivityValidateExecutable, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryNone,
			"run status is not executable in shadow scope", refs)
	}
}

// ObserveWorkTask reads the work-task snapshot through the Tasks port and
// reports the task status plus bounded ref counts. Observe-only: no claiming
// in this phase (the plan's ObserveOrClaimWorkTask claim path is Phase 4).
// Result refs carry "task-status:<status>" and one "<kind>:<count>" entry
// per ref kind, with underscores dashed (e.g. "verifier-result-refs:2").
func (a *AutomationRunActivities) ObserveWorkTask(ctx context.Context, ref projectdurable.SafeWorkTaskRef) (projectdurable.DurableActivityResult, error) {
	if err := ref.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	snap, err := a.Tasks.LoadTaskStatus(ctx, ref)
	if err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("load task status failed")
	}
	if err := snap.Ref.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	if err := projectdurable.ValidateSafeRef(snap.Status); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	refs := []string{"task-status:" + snap.Status}
	kinds := make([]string, 0, len(snap.Refs))
	for kind := range snap.Refs {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		for _, entry := range snap.Refs[kind] {
			if err := projectdurable.ValidateSafeRef(entry); err != nil {
				return projectdurable.DurableActivityResult{}, err
			}
		}
		refs = append(refs, strings.ReplaceAll(kind, "_", "-")+":"+strconv.Itoa(len(snap.Refs[kind])))
	}
	return newResult(ActivityObserveWorkTask, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
		"work task observed without claiming", refs)
}

// ObserveCodexAttempt classifies the run's attempt outcome from the snapshot
// into safe categories only. A snapshot carrying a safe failure category
// (timeout, codex auth unavailable, schema invalid, ...) classifies blocked
// with that category; completed/verifying classify ok with a verifying
// expectation; in-flight statuses classify ok; anything else fails closed to
// blocked without inventing a category.
func (a *AutomationRunActivities) ObserveCodexAttempt(ctx context.Context, snap projectdurable.DurableRunSnapshot) (projectdurable.DurableActivityResult, error) {
	_ = ctx
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	refs := []string{"run-status:" + snap.Status, "attempt-count:" + strconv.Itoa(snap.AttemptCount)}
	if snap.FailureCategory != projectdurable.FailureCategoryNone {
		return newResult(ActivityObserveCodexAttempt, projectdurable.ActivityStatusBlocked, snap.FailureCategory,
			"attempt classified by safe failure category", refs)
	}
	switch snap.Status {
	case runStatusCompleted, runStatusVerifying:
		return newResult(ActivityObserveCodexAttempt, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
			"attempt completed; verification expected", refs)
	case runStatusQueued, runStatusClaiming, runStatusStarting, runStatusRunning:
		return newResult(ActivityObserveCodexAttempt, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
			"attempt in progress", refs)
	default:
		return newResult(ActivityObserveCodexAttempt, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryNone,
			"attempt ended without a safe failure category", refs)
	}
}

// ObserveGovernedCloseout is metadata-only: it reports the closeout-related
// ref counts present on the snapshot (evidence, claim, knowledge-candidate
// refs). It never performs a closeout.
func (a *AutomationRunActivities) ObserveGovernedCloseout(ctx context.Context, snap projectdurable.DurableRunSnapshot) (projectdurable.DurableActivityResult, error) {
	_ = ctx
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	refs := []string{
		"evidence-refs:" + strconv.Itoa(len(snap.EvidenceRefs)),
		"claim-refs:" + strconv.Itoa(len(snap.ClaimRefs)),
		"knowledge-refs:" + strconv.Itoa(len(snap.KnowledgeCandidateRefs)),
	}
	return newResult(ActivityObserveCloseout, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
		"closeout refs observed", refs)
}

// ObserveReviewQueue reports review-result ref presence. Convention
// (documented per the Phase 3 spec): review is REQUIRED exactly when the
// snapshot status is "verifying" - the verifying parking state is the
// review/verification gate in the current run contract - and the requirement
// is satisfied via snapshot refs (ReviewResultRefs non-empty). Required but
// absent classifies blocked with FailureCategoryVerificationRequired.
func (a *AutomationRunActivities) ObserveReviewQueue(ctx context.Context, snap projectdurable.DurableRunSnapshot) (projectdurable.DurableActivityResult, error) {
	_ = ctx
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	refs := []string{"review-refs:" + strconv.Itoa(len(snap.ReviewResultRefs))}
	if snap.Status == runStatusVerifying && len(snap.ReviewResultRefs) == 0 {
		return newResult(ActivityObserveReviewQueue, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryVerificationRequired,
			"review required but no review result refs present", refs)
	}
	return newResult(ActivityObserveReviewQueue, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
		"review refs observed", refs)
}

// ObserveVerifierState classifies ok only when verifier result refs exist on
// the snapshot. Otherwise it classifies blocked with
// FailureCategoryVerificationRequired: a run with status verifying and no
// verifier refs must never classify as complete.
func (a *AutomationRunActivities) ObserveVerifierState(ctx context.Context, snap projectdurable.DurableRunSnapshot) (projectdurable.DurableActivityResult, error) {
	_ = ctx
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	refs := []string{"verifier-refs:" + strconv.Itoa(len(snap.VerifierResultRefs))}
	if len(snap.VerifierResultRefs) == 0 {
		return newResult(ActivityObserveVerifierState, projectdurable.ActivityStatusBlocked, projectdurable.FailureCategoryVerificationRequired,
			"verifier results required before completion", refs)
	}
	return newResult(ActivityObserveVerifierState, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
		"verifier results present", refs)
}

// WriteShadowComparison flattens the trace into bounded key->value fields
// (every key passes ValidateSafeRef, every value passes ValidateSafeSummary;
// keys are validated in sorted order for deterministic failures) and writes
// them through the Shadow port. This is the only write in the shadow phase.
func (a *AutomationRunActivities) WriteShadowComparison(ctx context.Context, ref projectdurable.SafeAutomationRunRef, trace ShadowRunTrace) (projectdurable.DurableActivityResult, error) {
	if err := ref.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	fields := flattenShadowTrace(trace)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := projectdurable.ValidateSafeRef(key); err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (shadow field key)", err)
		}
		if err := projectdurable.ValidateSafeSummary(fields[key]); err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (shadow field value)", err)
		}
	}
	if err := a.Shadow.WriteShadowComparison(ctx, ref, fields); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("write shadow comparison failed")
	}
	return newResult(ActivityWriteShadow, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
		"shadow comparison written", []string{"shadow-fields:" + strconv.Itoa(len(fields))})
}

// flattenShadowTrace converts the trace into bounded fields. Step keys are
// index-prefixed ("step_00_load-run-snapshot") so ordering survives the flat
// map; values carry only status and safe category.
func flattenShadowTrace(trace ShadowRunTrace) map[string]string {
	fields := map[string]string{
		"final_status":     trace.FinalStatus,
		"failure_category": string(trace.FailureCategory),
		"shadow_only":      strconv.FormatBool(trace.Input.ShadowOnly),
		"step_count":       strconv.Itoa(len(trace.Steps)),
	}
	for i, step := range trace.Steps {
		key := fmt.Sprintf("step_%02d_%s", i, step.Activity)
		fields[key] = fmt.Sprintf("status=%s category=%s", step.Status, string(step.FailureCategory))
	}
	return fields
}

// --- Phase 4: test-only execution activities ---------------------------------

// Activity step names for the Phase 4 test-only execution workflow.
const (
	ActivityClaimRunForExecution     = "claim-run-for-execution"
	ActivityCompleteRunAttempt       = "complete-run-attempt"
	ActivityWriteExecutionComparison = "write-execution-comparison"
)

// TestExecutionWorkflowInput is the metadata-only input of the Phase 4
// TEST-ONLY execution workflow. No production path constructs it: there is
// no configuration that enables durable execution, and the only construction
// sites are tests. Outcome carries the attempt completion the simulated
// runner will report; the workflow injects the claim token after claiming.
type TestExecutionWorkflowInput struct {
	ProjectID    string                               `json:"project_id"`
	AutomationID string                               `json:"automation_id"`
	RunID        string                               `json:"run_id"`
	TaskID       string                               `json:"task_id,omitempty"`
	TraceID      string                               `json:"trace_id,omitempty"`
	RunnerID     string                               `json:"runner_id"`
	Outcome      projectdurable.DurableAttemptOutcome `json:"outcome"`
}

// ExecutionTrace is the Phase 4 workflow result. It mirrors ShadowRunTrace:
// bounded, safe, metadata-only records of every step plus the mirrored final
// run status and the first non-none failure category.
type ExecutionTrace struct {
	Input           TestExecutionWorkflowInput             `json:"input"`
	Steps           []projectdurable.DurableActivityResult `json:"steps"`
	FinalStatus     string                                 `json:"final_status"`
	FailureCategory projectdurable.DurableFailureCategory  `json:"failure_category,omitempty"`
}

// ClaimedRunResult is the metadata-only result of ClaimRunForExecution: the
// post-claim run snapshot plus the opaque claim token the completion report
// must echo.
type ClaimedRunResult struct {
	Snapshot projectdurable.DurableRunSnapshot `json:"snapshot"`
	ClaimID  string                            `json:"claim_id"`
}

// AutomationRunExecutionActivities groups the Phase 4 test-only execution
// activities and their ports. Implementations of Claim and Complete are test
// adapters over the CURRENT projectautomation service until cutover approval;
// durable code never imports that service. Runs is used for the durable
// post-completion snapshot re-read; Shadow records the execution comparison.
// Register one instance per worker alongside AutomationRunActivities (whose
// observe methods the execution workflow reuses instead of duplicating).
type AutomationRunExecutionActivities struct {
	Claim    projectdurable.WorkTaskClaimPort
	Complete projectdurable.AttemptCompletionPort
	Runs     projectdurable.AutomationRunObserver
	Shadow   projectdurable.ShadowComparisonWriter
}

// ClaimRunForExecution claims the referenced run through the Claim port on
// behalf of runnerID. Everything is validated fail-closed: the input ref and
// runner id before the port call, the returned snapshot and claim token
// after it. Port errors are genericized so no port error text enters durable
// history.
func (a *AutomationRunExecutionActivities) ClaimRunForExecution(ctx context.Context, ref projectdurable.SafeAutomationRunRef, runnerID string) (ClaimedRunResult, error) {
	if err := ref.Validate(); err != nil {
		return ClaimedRunResult{}, err
	}
	if err := projectdurable.ValidateSafeRef(runnerID); err != nil {
		return ClaimedRunResult{}, fmt.Errorf("%w (field runner_id)", err)
	}
	snap, claimID, err := a.Claim.ClaimRun(ctx, ref, runnerID)
	if err != nil {
		// Never echo port error content into durable history.
		return ClaimedRunResult{}, fmt.Errorf("claim run failed")
	}
	if err := snap.Validate(); err != nil {
		return ClaimedRunResult{}, err
	}
	if err := projectdurable.ValidateSafeRef(claimID); err != nil {
		return ClaimedRunResult{}, fmt.Errorf("%w (field claim_id)", err)
	}
	if snap.Run.RunID != ref.RunID {
		return ClaimedRunResult{}, fmt.Errorf("claimed run does not match requested run")
	}
	return ClaimedRunResult{Snapshot: snap, ClaimID: claimID}, nil
}

// CompleteRunAttempt reports one attempt outcome through the Complete port.
// The outcome is validated fail-closed BEFORE the port is invoked: an
// outcome carrying unsafe refs (roots, markers, URLs, emails, ...) must never
// reach the current service. When the Runs observer is configured, the
// post-completion snapshot is re-read through it (mirroring the current
// service's durable re-read after completion) and the re-read wins.
func (a *AutomationRunExecutionActivities) CompleteRunAttempt(ctx context.Context, ref projectdurable.SafeAutomationRunRef, outcome projectdurable.DurableAttemptOutcome) (projectdurable.DurableRunSnapshot, error) {
	if err := ref.Validate(); err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	if err := outcome.Validate(); err != nil {
		// Fail closed before touching the port.
		return projectdurable.DurableRunSnapshot{}, err
	}
	snap, err := a.Complete.CompleteAttempt(ctx, ref, outcome)
	if err != nil {
		return projectdurable.DurableRunSnapshot{}, fmt.Errorf("complete attempt failed")
	}
	if err := snap.Validate(); err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	if snap.Run.RunID != ref.RunID {
		return projectdurable.DurableRunSnapshot{}, fmt.Errorf("completed run does not match requested run")
	}
	if a.Runs != nil {
		reread, err := a.Runs.LoadRunSnapshot(ctx, ref)
		if err != nil {
			return projectdurable.DurableRunSnapshot{}, fmt.Errorf("post-completion snapshot re-read failed")
		}
		if err := reread.Validate(); err != nil {
			return projectdurable.DurableRunSnapshot{}, err
		}
		if reread.Run.RunID != ref.RunID {
			return projectdurable.DurableRunSnapshot{}, fmt.Errorf("post-completion snapshot does not match requested run")
		}
		return reread, nil
	}
	return snap, nil
}

// WriteExecutionComparison flattens the execution trace into bounded
// key->value fields and writes them through the Shadow port, exactly like
// the Phase 3 WriteShadowComparison (keys validated in sorted order for
// deterministic failures). This is the only Shadow write in the execution
// workflow and it always runs last.
func (a *AutomationRunExecutionActivities) WriteExecutionComparison(ctx context.Context, ref projectdurable.SafeAutomationRunRef, trace ExecutionTrace) (projectdurable.DurableActivityResult, error) {
	if err := ref.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	fields := flattenExecutionTrace(trace)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := projectdurable.ValidateSafeRef(key); err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (execution field key)", err)
		}
		if err := projectdurable.ValidateSafeSummary(fields[key]); err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (execution field value)", err)
		}
	}
	if err := a.Shadow.WriteShadowComparison(ctx, ref, fields); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("write execution comparison failed")
	}
	return newResult(ActivityWriteExecutionComparison, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone,
		"execution comparison written", []string{"execution-fields:" + strconv.Itoa(len(fields))})
}

// flattenExecutionTrace converts the execution trace into bounded fields,
// mirroring flattenShadowTrace with a mode marker instead of a shadow flag.
func flattenExecutionTrace(trace ExecutionTrace) map[string]string {
	fields := map[string]string{
		"mode":             "test_execution",
		"final_status":     trace.FinalStatus,
		"failure_category": string(trace.FailureCategory),
		"step_count":       strconv.Itoa(len(trace.Steps)),
	}
	for i, step := range trace.Steps {
		key := fmt.Sprintf("step_%02d_%s", i, step.Activity)
		fields[key] = fmt.Sprintf("status=%s category=%s", step.Status, string(step.FailureCategory))
	}
	return fields
}

// newResult builds and fail-closed-validates one activity result so nothing
// invalid ever enters durable history.
func newResult(activity string, status string, category projectdurable.DurableFailureCategory, summary string, refs []string) (projectdurable.DurableActivityResult, error) {
	result := projectdurable.DurableActivityResult{
		Activity:        activity,
		Status:          status,
		FailureCategory: category,
		SafeSummary:     summary,
		Refs:            refs,
		CompletedAt:     time.Now().UTC(),
	}
	if err := result.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	return result, nil
}
