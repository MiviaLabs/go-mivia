package workflows

// Phase 4 test-only durable execution tests. The harness wires the REAL
// projectautomation.Service (with the REAL projectworkplan.Service and the
// exported in-memory stores) behind in-test adapters that implement the
// projectdurable execution ports. Importing the current services in _test.go
// files is allowed and expected; the durable production code never does.
//
// The harness mirrors TestCompleteAttemptQueuesRecoveryPostImplementationReviewAutomation
// (internal/projectautomation/service_test.go) for the plan/task/automation
// setup and TestBaselineAutomationRunClaimLeaseHeartbeatAndCompletionBehavior
// (internal/projectautomation/baseline_contract_test.go) for the external
// claim/complete contract. No real Codex runs and no git state is touched:
// the runner is simulated by the completion adapter (external execution
// mode), and all fixtures are neutral.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/client"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

// --- real-service harness ----------------------------------------------------

// recordingAutomationStore wraps the exported in-memory automation store and
// records every created attempt so tests can assert the attempt shape the
// current service persisted (the Store interface exposes no attempt reads).
type recordingAutomationStore struct {
	*automationstore.MemoryStore
	mu       sync.Mutex
	attempts []projectautomation.AutomationAttempt
}

func (s *recordingAutomationStore) CreateAttempt(ctx context.Context, value projectautomation.AutomationAttempt) (projectautomation.AutomationAttempt, error) {
	created, err := s.MemoryStore.CreateAttempt(ctx, value)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.attempts = append(s.attempts, created)
	s.mu.Unlock()
	return created, nil
}

func (s *recordingAutomationStore) attemptsForRun(runID string) []projectautomation.AutomationAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []projectautomation.AutomationAttempt
	for _, attempt := range s.attempts {
		if attempt.AutomationRunID == runID {
			out = append(out, attempt)
		}
	}
	return out
}

type allowAllPermissionResolver struct{}

func (allowAllPermissionResolver) CheckAutomationPermission(_ context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      input.PermissionRef,
		AgentID:            input.AgentID,
		AllowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
	}, nil
}

// executionHarness owns one fully real service stack plus one queued
// external automation run ready to claim.
type executionHarness struct {
	store      *recordingAutomationStore
	svc        *projectautomation.Service
	workSvc    *projectworkplan.Service
	plan       projectworkplan.WorkPlan
	task       projectworkplan.WorkTask
	automation projectautomation.Automation
	run        projectautomation.AutomationRun
}

func newExecutionHarness(t *testing.T) *executionHarness {
	t.Helper()
	ctx := context.Background()

	workStore := workplanstore.NewMemoryStore()
	workSvc := projectworkplan.New(workStore)
	plan, err := workSvc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan-durable-exec",
		Title:       "Durable execution pilot plan",
		GoalSummary: "Exercise Phase 4 test-only durable execution against the current service.",
	})
	if err != nil {
		t.Fatalf("CreateWorkPlan: %v", err)
	}
	plan, err = workSvc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         plan.ID,
		Status:         projectworkplan.WorkPlanStatusActive,
		SafeNextAction: "activate durable execution test plan",
	})
	if err != nil {
		t.Fatalf("UpdateWorkPlanStatus: %v", err)
	}
	// Task fields mirror the proven setup in
	// TestCompleteAttemptQueuesRecoveryPostImplementationReviewAutomation:
	// FilesToEdit is non-empty and no review refs exist, so completing the
	// run while the task is needs_review makes the current service queue a
	// post-implementation review.
	task, err := workSvc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "smoke-draft-pr",
		Title:                   "Smoke Draft PR",
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              "smoke-gitops-worker",
		Description:             "Create one bounded smoke marker file.",
		EvidenceNeeded:          []string{"gitops-smoke-ref"},
		FilesToRead:             []string{"configs/workflows/generic/governed-smoke-gitops.toml"},
		FilesToEdit:             []string{".agentic/automation-smoke.md"},
		LikelyFilesAffected:     []string{".agentic/automation-smoke.md"},
		VerificationRequirement: "runner verifies bounded diff and GitOps refs",
		ExpectedOutput:          "one safe smoke marker file update committed and pushed by runner GitOps",
		FailureCriteria:         "fail if any file outside the smoke marker changes",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("CreateWorkTask: %v", err)
	}

	store := &recordingAutomationStore{MemoryStore: automationstore.NewMemoryStore()}
	svc := projectautomation.New(store, workSvc, projectautomation.Options{
		Enabled:            true,
		RunnerEnabled:      true,
		RunnerExecution:    projectautomation.RunnerExecutionExternal,
		MaxParallelTasks:   1,
		PermissionResolver: allowAllPermissionResolver{},
	})
	automation, err := svc.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/durable-exec",
		Title:           "Durable execution pilot automation",
		Purpose:         "Drive the current external claim contract from the durable pilot.",
		Status:          projectautomation.AutomationStatusEnabled,
		AgentID:         "smoke-gitops-worker",
		PlanID:          plan.ID,
		AllowedTaskRefs: []string{task.TaskRef},
		TriggerKind:     projectautomation.TriggerKindAutomatic,
		PermissionRef:   "permission_snapshot:snapshot-worker",
		SourceKind:      projectautomation.AutomationSourceWorkflow,
	})
	if err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	queued, err := svc.SubmitRun(ctx, projectautomation.SubmitRunInput{
		ProjectID:    "project-1",
		AutomationID: automation.ID,
		TaskID:       task.ID,
		RunnerKind:   projectautomation.RunnerKindCodexCLI,
	})
	if err != nil {
		t.Fatalf("SubmitRun: %v", err)
	}
	if queued.Status != projectautomation.RunStatusQueued {
		t.Fatalf("expected queued run, got %#v", queued)
	}

	return &executionHarness{
		store:      store,
		svc:        svc,
		workSvc:    workSvc,
		plan:       plan,
		task:       task,
		automation: automation,
		run:        queued,
	}
}

// runnerCloseout simulates the external runner's governed closeout before it
// reports completion: the work task moves to needs_review with the required
// evidence ref, exactly as in the mirrored service test. It is idempotent so
// duplicate completion reports do not re-transition the task.
func (h *executionHarness) runnerCloseout(ctx context.Context, runID string) error {
	task, err := h.workSvc.GetWorkTask(ctx, "project-1", h.task.ID)
	if err != nil {
		return err
	}
	if task.Status != projectworkplan.WorkTaskStatusInProgress {
		return nil
	}
	_, err = h.workSvc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:      "project-1",
			TaskID:         h.task.ID,
			RunID:          runID,
			TraceID:        runID,
			SafeNextAction: "worker closeout",
			EvidenceRefs:   []string{"gitops-smoke-ref"},
		},
		Status: projectworkplan.WorkTaskStatusNeedsReview,
	})
	return err
}

// --- port adapters over the real service --------------------------------------

// serviceExecutionPorts implements the projectdurable observe + execution
// ports as thin test adapters over the current services. Snapshots carry
// metadata only: run ids/status/category/claim plus the ref lists aggregated
// on the work task.
type serviceExecutionPorts struct {
	harness *executionHarness

	mu            sync.Mutex
	claimCalls    int
	completeCalls int
}

func (p *serviceExecutionPorts) claimCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.claimCalls
}

func (p *serviceExecutionPorts) completeCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.completeCalls
}

// durableCategory narrows the current service failure category to the
// mirrored safe set; unmirrored categories degrade to none (metadata-only
// narrowing for the pilot, never widened).
func durableCategory(category string) projectdurable.DurableFailureCategory {
	c := projectdurable.DurableFailureCategory(category)
	if c.Validate() == nil {
		return c
	}
	return projectdurable.FailureCategoryNone
}

func (p *serviceExecutionPorts) snapshotForRun(ctx context.Context, ref projectdurable.SafeAutomationRunRef) (projectdurable.DurableRunSnapshot, error) {
	run, err := p.harness.svc.GetRun(ctx, ref.ProjectID, ref.RunID)
	if err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	snap := projectdurable.DurableRunSnapshot{
		Run: projectdurable.SafeAutomationRunRef{
			ProjectID:    run.ProjectID,
			AutomationID: run.AutomationID,
			RunID:        run.ID,
			TaskID:       run.TaskID,
			TraceID:      run.TraceID,
		},
		Status:          run.Status,
		FailureCategory: durableCategory(run.FailureCategory),
		AttemptCount:    run.AttemptCount,
		ClaimID:         run.ClaimID,
		RunnerID:        run.RunnerID,
		ObservedAt:      time.Now().UTC(),
	}
	if run.TaskID != "" {
		task, taskErr := p.harness.workSvc.GetWorkTask(ctx, run.ProjectID, run.TaskID)
		if taskErr == nil {
			snap.VerifierResultRefs = append([]string(nil), task.VerifierResultRefs...)
			snap.ReviewResultRefs = append([]string(nil), task.ReviewResultRefs...)
			snap.EvidenceRefs = append([]string(nil), task.EvidenceRefs...)
			snap.ClaimRefs = append([]string(nil), task.ClaimRefs...)
		}
	}
	return snap, nil
}

func (p *serviceExecutionPorts) LoadRunSnapshot(ctx context.Context, ref projectdurable.SafeAutomationRunRef) (projectdurable.DurableRunSnapshot, error) {
	return p.snapshotForRun(ctx, ref)
}

func (p *serviceExecutionPorts) ClaimRun(ctx context.Context, ref projectdurable.SafeAutomationRunRef, runnerID string) (projectdurable.DurableRunSnapshot, string, error) {
	p.mu.Lock()
	p.claimCalls++
	p.mu.Unlock()
	claimed, err := p.harness.svc.ClaimNextRun(ctx, projectautomation.ClaimNextRunInput{
		ProjectID:  ref.ProjectID,
		AgentID:    p.harness.automation.AgentID,
		RunnerKind: projectautomation.RunnerKindCodexCLI,
		RunnerID:   runnerID,
	})
	if err != nil {
		return projectdurable.DurableRunSnapshot{}, "", err
	}
	if claimed.Run.ID != ref.RunID {
		return projectdurable.DurableRunSnapshot{}, "", fmt.Errorf("claimed run %q does not match requested run %q", claimed.Run.ID, ref.RunID)
	}
	snap, err := p.snapshotForRun(ctx, ref)
	if err != nil {
		return projectdurable.DurableRunSnapshot{}, "", err
	}
	return snap, claimed.Run.ClaimID, nil
}

func (p *serviceExecutionPorts) CompleteAttempt(ctx context.Context, ref projectdurable.SafeAutomationRunRef, outcome projectdurable.DurableAttemptOutcome) (projectdurable.DurableRunSnapshot, error) {
	p.mu.Lock()
	p.completeCalls++
	p.mu.Unlock()
	// Simulated external runner closeout (governed task handoff) before the
	// completion report, mirroring the current runner contract.
	if err := p.harness.runnerCloseout(ctx, ref.RunID); err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	if _, err := p.harness.svc.CompleteAttempt(ctx, projectautomation.CompleteAttemptInput{
		ProjectID:          ref.ProjectID,
		RunID:              ref.RunID,
		ClaimID:            outcome.ClaimID,
		RunnerID:           outcome.RunnerID,
		Status:             outcome.Status,
		FailureCategory:    string(outcome.FailureCategory),
		VerifierResultRefs: outcome.VerifierResultRefs,
		EvidenceRefs:       outcome.EvidenceRefs,
		ClaimRefs:          outcome.ClaimRefs,
		ReviewRefs:         outcome.ReviewResultRefs,
		KnowledgeRefs:      outcome.KnowledgeCandidateRefs,
	}); err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	return p.snapshotForRun(ctx, ref)
}

// --- workflow runner ------------------------------------------------------------

func executionInput(h *executionHarness, outcome projectdurable.DurableAttemptOutcome) TestExecutionWorkflowInput {
	return TestExecutionWorkflowInput{
		ProjectID:    "project-1",
		AutomationID: h.automation.ID,
		RunID:        h.run.ID,
		TaskID:       h.task.ID,
		RunnerID:     "runner-durable",
		Outcome:      outcome,
	}
}

func runExecutionWorkflow(t *testing.T, h *executionHarness, ports *serviceExecutionPorts, shadow *fakeShadowWriter, input TestExecutionWorkflowInput) (ExecutionTrace, error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := projectdurable.NewMemoryEngine()
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("close engine: %v", err)
		}
	}()

	if err := engine.Orchestrator.RegisterWorkflow(MiviaAutomationRunTestExecutionWorkflow); err != nil {
		t.Fatalf("register workflow: %v", err)
	}
	// The execution workflow reuses the Phase 3 observe activities, so both
	// structs are registered on the same worker.
	if err := engine.Orchestrator.RegisterActivity(&activities.AutomationRunActivities{
		Runs:   ports,
		Shadow: shadow,
	}); err != nil {
		t.Fatalf("register observe activities: %v", err)
	}
	if err := engine.Orchestrator.RegisterActivity(&activities.AutomationRunExecutionActivities{
		Claim:    ports,
		Complete: ports,
		Runs:     ports,
		Shadow:   shadow,
	}); err != nil {
		t.Fatalf("register execution activities: %v", err)
	}
	if err := engine.Orchestrator.Start(ctx); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}

	instance, err := engine.Orchestrator.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: "exec-instance-" + input.RunID + "-" + t.Name(),
	}, MiviaAutomationRunTestExecutionWorkflow, input)
	if err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}

	return client.GetWorkflowResult[ExecutionTrace](ctx, engine.Orchestrator.Client, instance, 10*time.Second)
}

func findExecutionStep(t *testing.T, trace ExecutionTrace, activity string) projectdurable.DurableActivityResult {
	t.Helper()
	for _, step := range trace.Steps {
		if step.Activity == activity {
			return step
		}
	}
	t.Fatalf("execution trace has no step for activity %q", activity)
	return projectdurable.DurableActivityResult{}
}

// completedExternalOutcome is the shared neutral completion fixture: a
// completed external attempt with verifier/evidence/claim/knowledge refs and
// no review refs (the independent review is queued by the service, never
// self-attached).
func completedExternalOutcome() projectdurable.DurableAttemptOutcome {
	return projectdurable.DurableAttemptOutcome{
		Status:                 projectautomation.RunStatusCompleted,
		VerifierResultRefs:     []string{"verifier.smoke.bounded-diff"},
		EvidenceRefs:           []string{"gitops-commit:abc", "gitops-push:abc", "gitops-pr:draft"},
		ClaimRefs:              []string{"claim:durable-exec"},
		KnowledgeCandidateRefs: []string{"knowledge:no_reusable"},
	}
}

// driveCurrentServiceDirectly executes the identical twin scenario through
// plain service calls (no durable engine): claim, runner closeout, complete.
func driveCurrentServiceDirectly(t *testing.T, h *executionHarness, outcome projectdurable.DurableAttemptOutcome) projectautomation.AutomationRun {
	t.Helper()
	ctx := context.Background()
	claimed, err := h.svc.ClaimNextRun(ctx, projectautomation.ClaimNextRunInput{
		ProjectID:  "project-1",
		AgentID:    h.automation.AgentID,
		RunnerKind: projectautomation.RunnerKindCodexCLI,
		RunnerID:   "runner-durable",
	})
	if err != nil {
		t.Fatalf("direct ClaimNextRun: %v", err)
	}
	if claimed.Run.ID != h.run.ID {
		t.Fatalf("direct claim got run %q, want %q", claimed.Run.ID, h.run.ID)
	}
	if err := h.runnerCloseout(ctx, claimed.Run.ID); err != nil {
		t.Fatalf("direct runner closeout: %v", err)
	}
	completed, err := h.svc.CompleteAttempt(ctx, projectautomation.CompleteAttemptInput{
		ProjectID:          "project-1",
		RunID:              claimed.Run.ID,
		ClaimID:            claimed.Run.ClaimID,
		RunnerID:           claimed.Run.RunnerID,
		Status:             outcome.Status,
		FailureCategory:    string(outcome.FailureCategory),
		VerifierResultRefs: outcome.VerifierResultRefs,
		EvidenceRefs:       outcome.EvidenceRefs,
		ClaimRefs:          outcome.ClaimRefs,
		ReviewRefs:         outcome.ReviewResultRefs,
		KnowledgeRefs:      outcome.KnowledgeCandidateRefs,
	})
	if err != nil {
		t.Fatalf("direct CompleteAttempt: %v", err)
	}
	return completed
}

// normalizeAttempt zeroes per-run identity and timing so two attempts from
// twin runs compare on shape only.
func normalizeAttempt(attempt projectautomation.AutomationAttempt, runID string) projectautomation.AutomationAttempt {
	attempt.ID = ""
	attempt.AutomationRunID = ""
	attempt.CreatedAt = time.Time{}
	attempt.FinishedAt = time.Time{}
	attempt.CommandRef = strings.ReplaceAll(attempt.CommandRef, runID, "RUN")
	normalized := make([]string, 0, len(attempt.EvidenceRefs))
	for _, ref := range attempt.EvidenceRefs {
		normalized = append(normalized, strings.ReplaceAll(ref, runID, "RUN"))
	}
	attempt.EvidenceRefs = normalized
	return attempt
}

// --- scenario a: same attempt shape as the current service ------------------------

func TestDurableExecutionCreatesSameAttemptShapeAsCurrentService(t *testing.T) {
	outcome := completedExternalOutcome()

	// Twin A: identical scenario driven through plain service calls.
	direct := newExecutionHarness(t)
	driveCurrentServiceDirectly(t, direct, outcome)
	directAttempts := direct.store.attemptsForRun(direct.run.ID)
	if len(directAttempts) != 1 {
		t.Fatalf("direct path expected one attempt, got %#v", directAttempts)
	}

	// Twin B: identical scenario driven through the durable workflow.
	durable := newExecutionHarness(t)
	ports := &serviceExecutionPorts{harness: durable}
	trace, err := runExecutionWorkflow(t, durable, ports, &fakeShadowWriter{}, executionInput(durable, outcome))
	if err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}
	completeStep := findExecutionStep(t, trace, activities.ActivityCompleteRunAttempt)
	if completeStep.Status != projectdurable.ActivityStatusOK {
		t.Fatalf("complete step not ok: %#v", completeStep)
	}
	durableAttempts := durable.store.attemptsForRun(durable.run.ID)
	if len(durableAttempts) != 1 {
		t.Fatalf("durable path expected one attempt, got %#v", durableAttempts)
	}

	got := normalizeAttempt(durableAttempts[0], durable.run.ID)
	want := normalizeAttempt(directAttempts[0], direct.run.ID)
	if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", want) {
		t.Fatalf("attempt shape diverged between durable and direct paths:\n durable: %#v\n direct:  %#v", got, want)
	}
	if got.Status != projectautomation.RunStatusCompleted || got.RunnerKind != projectautomation.RunnerKindCodexCLI || got.AttemptNumber != want.AttemptNumber {
		t.Fatalf("normalized attempt lost contract fields: %#v", got)
	}
}

// --- scenario b: completed external attempt parks at verifying ---------------------

func TestDurableExecutionMovesCompletedExternalAttemptToVerifying(t *testing.T) {
	h := newExecutionHarness(t)
	ports := &serviceExecutionPorts{harness: h}

	trace, err := runExecutionWorkflow(t, h, ports, &fakeShadowWriter{}, executionInput(h, completedExternalOutcome()))
	if err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}

	run, err := h.svc.GetRun(context.Background(), "project-1", h.run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != projectautomation.RunStatusVerifying {
		t.Fatalf("completed external attempt must park at verifying, got %q", run.Status)
	}
	if run.Status == projectautomation.RunStatusCompleted {
		t.Fatalf("run must not be completed without independent review")
	}
	if trace.FinalStatus != projectautomation.RunStatusVerifying {
		t.Fatalf("trace final status = %q, want verifying", trace.FinalStatus)
	}
}

// --- scenario c: post-implementation review queued like the current code ----------

func TestDurableExecutionQueuesPostImplementationReviewWhenCurrentCodeWould(t *testing.T) {
	ctx := context.Background()
	outcome := completedExternalOutcome()

	// Prove the current code queues the review for the identical twin first.
	direct := newExecutionHarness(t)
	driveCurrentServiceDirectly(t, direct, outcome)
	directReviewAutomation, directReviewRuns := findQueuedReview(t, direct)

	// Then the durable path.
	durable := newExecutionHarness(t)
	ports := &serviceExecutionPorts{harness: durable}
	if _, err := runExecutionWorkflow(t, durable, ports, &fakeShadowWriter{}, executionInput(durable, outcome)); err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}
	durableReviewAutomation, durableReviewRuns := findQueuedReview(t, durable)

	if durableReviewAutomation.SchedulePolicy != directReviewAutomation.SchedulePolicy ||
		durableReviewAutomation.TriggerKind != directReviewAutomation.TriggerKind {
		t.Fatalf("review automation diverged: durable=%#v direct=%#v", durableReviewAutomation, directReviewAutomation)
	}
	if len(durableReviewRuns) != len(directReviewRuns) {
		t.Fatalf("queued review runs diverged: durable=%#v direct=%#v", durableReviewRuns, directReviewRuns)
	}
	if durableReviewRuns[0].Status != projectautomation.RunStatusQueued ||
		durableReviewRuns[0].ParentRunID != durable.run.ID {
		t.Fatalf("expected queued review run parented to the worker run, got %#v", durableReviewRuns[0])
	}
	reviewTask, err := durable.workSvc.GetWorkTask(ctx, "project-1", durableReviewRuns[0].TaskID)
	if err != nil {
		t.Fatalf("GetWorkTask review: %v", err)
	}
	if !strings.HasPrefix(reviewTask.TaskRef, "review-smoke-draft-pr") {
		t.Fatalf("expected review task for smoke-draft-pr, got %#v", reviewTask)
	}
}

// findQueuedReview locates the post-implementation review automation and its
// queued runs the current service created for the harness plan.
func findQueuedReview(t *testing.T, h *executionHarness) (projectautomation.Automation, []projectautomation.AutomationRun) {
	t.Helper()
	ctx := context.Background()
	automations, err := h.store.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListAutomations: %v", err)
	}
	for _, candidate := range automations {
		if candidate.ID == h.automation.ID || candidate.PlanID != h.plan.ID {
			continue
		}
		if candidate.SchedulePolicy != "post_implementation_review" {
			continue
		}
		runs, err := h.store.ListRuns(ctx, projectautomation.RunFilter{ProjectID: "project-1", AutomationID: candidate.ID, PlanID: h.plan.ID})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if len(runs) == 0 {
			t.Fatalf("review automation %q has no queued run", candidate.ID)
		}
		return candidate, runs
	}
	t.Fatalf("no post-implementation review automation queued, got %#v", automations)
	return projectautomation.Automation{}, nil
}

// --- scenario d: missing review/verifier refs classify blocked ---------------------

func TestDurableExecutionBlocksOnMissingReviewOrVerifierRefs(t *testing.T) {
	h := newExecutionHarness(t)
	ports := &serviceExecutionPorts{harness: h}

	// Completion without verifier refs (and review refs never self-attach):
	// the run parks at verifying with neither gate satisfied.
	outcome := completedExternalOutcome()
	outcome.VerifierResultRefs = nil

	trace, err := runExecutionWorkflow(t, h, ports, &fakeShadowWriter{}, executionInput(h, outcome))
	if err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}

	reviewStep := findExecutionStep(t, trace, activities.ActivityObserveReviewQueue)
	if reviewStep.Status != projectdurable.ActivityStatusBlocked || reviewStep.FailureCategory != projectdurable.FailureCategoryVerificationRequired {
		t.Fatalf("review step = %q/%q, want blocked/verification_required", reviewStep.Status, reviewStep.FailureCategory)
	}
	verifierStep := findExecutionStep(t, trace, activities.ActivityObserveVerifierState)
	if verifierStep.Status != projectdurable.ActivityStatusBlocked || verifierStep.FailureCategory != projectdurable.FailureCategoryVerificationRequired {
		t.Fatalf("verifier step = %q/%q, want blocked/verification_required", verifierStep.Status, verifierStep.FailureCategory)
	}
	if trace.FinalStatus == projectautomation.RunStatusCompleted {
		t.Fatalf("trace must never report completed without review/verifier refs")
	}
	if trace.FailureCategory != projectdurable.FailureCategoryVerificationRequired {
		t.Fatalf("trace category = %q, want verification_required", trace.FailureCategory)
	}
	// Blocked observation steps do not short-circuit: the comparison still ran.
	comparisonStep := findExecutionStep(t, trace, activities.ActivityWriteExecutionComparison)
	if comparisonStep.Status != projectdurable.ActivityStatusOK {
		t.Fatalf("comparison step = %q, want ok", comparisonStep.Status)
	}
}

// --- scenario e: unsafe refs fail closed before the completion port ----------------

func TestDurableExecutionFailsClosedOnUnsafeRefs(t *testing.T) {
	h := newExecutionHarness(t)
	ports := &serviceExecutionPorts{harness: h}

	outcome := completedExternalOutcome()
	outcome.EvidenceRefs = append(outcome.EvidenceRefs, "/home/leak")

	trace, err := runExecutionWorkflow(t, h, ports, &fakeShadowWriter{}, executionInput(h, outcome))
	if err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}

	if ports.completeCallCount() != 0 {
		t.Fatalf("completion port called %d times, want 0 (fail closed before the port)", ports.completeCallCount())
	}
	if ports.claimCallCount() != 1 {
		t.Fatalf("claim port called %d times, want 1", ports.claimCallCount())
	}
	completeStep := findExecutionStep(t, trace, activities.ActivityCompleteRunAttempt)
	if completeStep.Status != projectdurable.ActivityStatusFailed {
		t.Fatalf("complete step = %q, want failed", completeStep.Status)
	}
	if trace.FinalStatus == projectautomation.RunStatusCompleted {
		t.Fatalf("trace must not report completed after a rejected outcome")
	}
	run, err := h.svc.GetRun(context.Background(), "project-1", h.run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != projectautomation.RunStatusRunning {
		t.Fatalf("run state changed despite rejected completion: %#v", run)
	}
	if len(h.store.attemptsForRun(h.run.ID)) != 0 {
		t.Fatalf("no attempt may be recorded for a rejected outcome")
	}
}

// --- scenario f: duplicate completion is idempotent --------------------------------

func TestDurableExecutionDuplicateCompletionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newExecutionHarness(t)
	ports := &serviceExecutionPorts{harness: h}
	outcome := completedExternalOutcome()

	if _, err := runExecutionWorkflow(t, h, ports, &fakeShadowWriter{}, executionInput(h, outcome)); err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}
	firstRun, err := h.svc.GetRun(ctx, "project-1", h.run.ID)
	if err != nil {
		t.Fatalf("GetRun after first completion: %v", err)
	}
	if firstRun.Status != projectautomation.RunStatusVerifying {
		t.Fatalf("first completion must park at verifying, got %#v", firstRun)
	}
	attemptsAfterFirst := h.store.attemptsForRun(h.run.ID)
	if len(attemptsAfterFirst) != 1 {
		t.Fatalf("expected one attempt after first completion, got %#v", attemptsAfterFirst)
	}

	// Replay the completion activity path with the same claim token, exactly
	// as a redelivered external completion report would arrive. The minimal
	// duplicate report mirrors what the baseline contract test pinned.
	duplicate := projectdurable.DurableAttemptOutcome{
		Status:   projectautomation.RunStatusCompleted,
		ClaimID:  firstRun.ClaimID,
		RunnerID: firstRun.RunnerID,
	}
	exec := &activities.AutomationRunExecutionActivities{Claim: ports, Complete: ports, Runs: ports, Shadow: &fakeShadowWriter{}}
	ref := projectdurable.SafeAutomationRunRef{
		ProjectID:    "project-1",
		AutomationID: h.automation.ID,
		RunID:        h.run.ID,
		TaskID:       h.task.ID,
	}
	snap, err := exec.CompleteRunAttempt(ctx, ref, duplicate)
	if err != nil {
		t.Fatalf("duplicate completion must be accepted idempotently, got %v", err)
	}
	if snap.Status != firstRun.Status {
		t.Fatalf("duplicate completion changed run status: %q -> %q", firstRun.Status, snap.Status)
	}
	secondRun, err := h.svc.GetRun(ctx, "project-1", h.run.ID)
	if err != nil {
		t.Fatalf("GetRun after duplicate completion: %v", err)
	}
	if secondRun.ID != firstRun.ID || secondRun.Status != firstRun.Status || secondRun.ClaimID != firstRun.ClaimID {
		t.Fatalf("duplicate completion was not idempotent: before=%#v after=%#v", firstRun, secondRun)
	}
	// Pinned CURRENT-service contract (verified against the live service, not
	// assumed): run state and attempt counter are idempotent, but every
	// accepted completion report persists its own attempt ROW. The baseline
	// contract test pinned only run-level idempotency; this pins the rest so
	// any future change in duplicate handling surfaces here.
	attemptsAfterDuplicate := h.store.attemptsForRun(h.run.ID)
	if len(attemptsAfterDuplicate) != 2 {
		t.Fatalf("current service records one attempt row per accepted completion report, got %#v", attemptsAfterDuplicate)
	}
	if attemptsAfterDuplicate[1].AttemptNumber != attemptsAfterDuplicate[0].AttemptNumber {
		t.Fatalf("duplicate completion must not advance the attempt counter: %#v", attemptsAfterDuplicate)
	}
	if secondRun.AttemptCount != firstRun.AttemptCount {
		t.Fatalf("duplicate completion changed run attempt count: before=%d after=%d", firstRun.AttemptCount, secondRun.AttemptCount)
	}
	// The duplicate must not queue a second post-implementation review run.
	_, reviewRuns := findQueuedReview(t, h)
	if len(reviewRuns) != 1 {
		t.Fatalf("duplicate completion duplicated the queued review run: %#v", reviewRuns)
	}
}
