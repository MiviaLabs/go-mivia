// Package parity seeds the Phase 8 parity harness pattern: one executable
// scenario driven twice from identical inputs - once through the plain
// CURRENT projectautomation.Service path, once through the durable
// test-execution workflow - with the terminal observable state compared
// field by field. The directory is test-only (no non-test Go files); Phase 8
// extracts the comparison helper into harness.go and adds scenario families.
//
// Seeded scenario: completed-external-attempt-to-verifying.
package parity

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/client"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/workflows"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

// parityOutcome is the bounded observable state both paths must agree on.
type parityOutcome struct {
	TerminalRunStatus   string
	TaskStatus          string
	RunAttemptCount     int
	AttemptRows         int
	VerifierRefsPresent bool
	ReviewRefsPresent   bool
	FailureCategory     string
	ReviewRunQueued     bool
}

// compareParityOutcome is the seed comparison helper Phase 8 will extract
// into harness.go. It fails on the first divergent field, naming it.
func compareParityOutcome(t *testing.T, scenario string, current parityOutcome, durable parityOutcome) {
	t.Helper()
	if current != durable {
		t.Fatalf("parity scenario %q diverged:\n current: %#v\n durable: %#v", scenario, current, durable)
	}
}

// TestParityCompletedExternalAttemptToVerifying drives the seed scenario
// through both paths from identical inputs and compares terminal run status,
// task status, attempt counters/rows, review/verifier ref presence, failure
// category, and review-run queueing.
func TestParityCompletedExternalAttemptToVerifying(t *testing.T) {
	current := scenarioCurrent(t)
	durable := scenarioDurable(t)
	compareParityOutcome(t, "completed-external-attempt-to-verifying", current, durable)

	// Guard the scenario itself against silently passing on the wrong state:
	// both paths must have parked at verifying with verifier refs attached
	// and an independent review queued but not yet attached.
	if current.TerminalRunStatus != projectautomation.RunStatusVerifying {
		t.Fatalf("scenario must park at verifying, got %#v", current)
	}
	if !current.VerifierRefsPresent || current.ReviewRefsPresent || !current.ReviewRunQueued {
		t.Fatalf("scenario must hold verifier refs with a queued (unattached) review, got %#v", current)
	}
}

// scenarioCurrent drives the scenario through plain service calls.
func scenarioCurrent(t *testing.T) parityOutcome {
	t.Helper()
	ctx := context.Background()
	h := newParityHarness(t)
	outcome := parityCompletionInput()

	claimed, err := h.svc.ClaimNextRun(ctx, projectautomation.ClaimNextRunInput{
		ProjectID:  "project-1",
		AgentID:    h.automation.AgentID,
		RunnerKind: projectautomation.RunnerKindCodexCLI,
		RunnerID:   "runner-parity",
	})
	if err != nil {
		t.Fatalf("current path ClaimNextRun: %v", err)
	}
	if claimed.Run.ID != h.run.ID {
		t.Fatalf("current path claimed run %q, want %q", claimed.Run.ID, h.run.ID)
	}
	if err := h.runnerCloseout(ctx, claimed.Run.ID); err != nil {
		t.Fatalf("current path runner closeout: %v", err)
	}
	outcome.ClaimID = claimed.Run.ClaimID
	outcome.RunnerID = claimed.Run.RunnerID
	if _, err := h.svc.CompleteAttempt(ctx, projectautomation.CompleteAttemptInput{
		ProjectID:          "project-1",
		RunID:              claimed.Run.ID,
		ClaimID:            outcome.ClaimID,
		RunnerID:           outcome.RunnerID,
		Status:             outcome.Status,
		VerifierResultRefs: outcome.VerifierResultRefs,
		EvidenceRefs:       outcome.EvidenceRefs,
		ClaimRefs:          outcome.ClaimRefs,
		KnowledgeRefs:      outcome.KnowledgeCandidateRefs,
	}); err != nil {
		t.Fatalf("current path CompleteAttempt: %v", err)
	}
	return h.observe(t)
}

// scenarioDurable drives the identical scenario through the durable
// test-execution workflow on the memory engine.
func scenarioDurable(t *testing.T) parityOutcome {
	t.Helper()
	h := newParityHarness(t)
	ports := &parityPorts{harness: h}
	shadow := &parityShadowWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := projectdurable.NewMemoryEngine()
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("close engine: %v", err)
		}
	}()
	if err := engine.Orchestrator.RegisterWorkflow(workflows.MiviaAutomationRunTestExecutionWorkflow); err != nil {
		t.Fatalf("register workflow: %v", err)
	}
	if err := engine.Orchestrator.RegisterActivity(&activities.AutomationRunActivities{Runs: ports, Shadow: shadow}); err != nil {
		t.Fatalf("register observe activities: %v", err)
	}
	if err := engine.Orchestrator.RegisterActivity(&activities.AutomationRunExecutionActivities{Claim: ports, Complete: ports, Runs: ports, Shadow: shadow}); err != nil {
		t.Fatalf("register execution activities: %v", err)
	}
	if err := engine.Orchestrator.Start(ctx); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	instance, err := engine.Orchestrator.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: "parity-instance-" + h.run.ID,
	}, workflows.MiviaAutomationRunTestExecutionWorkflow, workflows.TestExecutionWorkflowInput{
		ProjectID:    "project-1",
		AutomationID: h.automation.ID,
		RunID:        h.run.ID,
		TaskID:       h.task.ID,
		RunnerID:     "runner-parity",
		Outcome:      parityCompletionInput(),
	})
	if err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}
	trace, err := client.GetWorkflowResult[workflows.ExecutionTrace](ctx, engine.Orchestrator.Client, instance, 10*time.Second)
	if err != nil {
		t.Fatalf("durable workflow failed: %v", err)
	}
	if trace.FinalStatus != projectautomation.RunStatusVerifying {
		t.Fatalf("durable trace final status = %q, want verifying", trace.FinalStatus)
	}
	return h.observe(t)
}

// parityCompletionInput is the shared neutral completion report both paths
// submit (review refs never self-attach in the current contract).
func parityCompletionInput() projectdurable.DurableAttemptOutcome {
	return projectdurable.DurableAttemptOutcome{
		Status:                 projectautomation.RunStatusCompleted,
		VerifierResultRefs:     []string{"verifier.smoke.bounded-diff"},
		EvidenceRefs:           []string{"gitops-commit:abc", "gitops-push:abc", "gitops-pr:draft"},
		ClaimRefs:              []string{"claim:parity-seed"},
		KnowledgeCandidateRefs: []string{"knowledge:no_reusable"},
	}
}

// --- compact real-service harness (mirrors the workflows package harness) -----

type attemptCountingStore struct {
	*automationstore.MemoryStore
	mu       sync.Mutex
	attempts []projectautomation.AutomationAttempt
}

func (s *attemptCountingStore) CreateAttempt(ctx context.Context, value projectautomation.AutomationAttempt) (projectautomation.AutomationAttempt, error) {
	created, err := s.MemoryStore.CreateAttempt(ctx, value)
	if err != nil {
		return created, err
	}
	s.mu.Lock()
	s.attempts = append(s.attempts, created)
	s.mu.Unlock()
	return created, nil
}

func (s *attemptCountingStore) attemptRows(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, attempt := range s.attempts {
		if attempt.AutomationRunID == runID {
			count++
		}
	}
	return count
}

type parityPermissionResolver struct{}

func (parityPermissionResolver) CheckAutomationPermission(_ context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      input.PermissionRef,
		AgentID:            input.AgentID,
		AllowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
	}, nil
}

type parityHarness struct {
	store      *attemptCountingStore
	svc        *projectautomation.Service
	workSvc    *projectworkplan.Service
	plan       projectworkplan.WorkPlan
	task       projectworkplan.WorkTask
	automation projectautomation.Automation
	run        projectautomation.AutomationRun
}

func newParityHarness(t *testing.T) *parityHarness {
	t.Helper()
	ctx := context.Background()

	workStore := workplanstore.NewMemoryStore()
	workSvc := projectworkplan.New(workStore)
	plan, err := workSvc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan-parity-seed",
		Title:       "Durable parity seed plan",
		GoalSummary: "Compare the current and durable execution paths from identical inputs.",
	})
	if err != nil {
		t.Fatalf("CreateWorkPlan: %v", err)
	}
	plan, err = workSvc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         plan.ID,
		Status:         projectworkplan.WorkPlanStatusActive,
		SafeNextAction: "activate parity seed plan",
	})
	if err != nil {
		t.Fatalf("UpdateWorkPlanStatus: %v", err)
	}
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

	store := &attemptCountingStore{MemoryStore: automationstore.NewMemoryStore()}
	svc := projectautomation.New(store, workSvc, projectautomation.Options{
		Enabled:            true,
		RunnerEnabled:      true,
		RunnerExecution:    projectautomation.RunnerExecutionExternal,
		MaxParallelTasks:   1,
		PermissionResolver: parityPermissionResolver{},
	})
	automation, err := svc.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/parity-seed",
		Title:           "Durable parity seed automation",
		Purpose:         "Drive the current external claim contract for the parity seed.",
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

	return &parityHarness{store: store, svc: svc, workSvc: workSvc, plan: plan, task: task, automation: automation, run: queued}
}

func (h *parityHarness) runnerCloseout(ctx context.Context, runID string) error {
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

// observe reads the terminal observable state both paths are compared on.
func (h *parityHarness) observe(t *testing.T) parityOutcome {
	t.Helper()
	ctx := context.Background()
	run, err := h.svc.GetRun(ctx, "project-1", h.run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	task, err := h.workSvc.GetWorkTask(ctx, "project-1", h.task.ID)
	if err != nil {
		t.Fatalf("GetWorkTask: %v", err)
	}
	reviewQueued := false
	automations, err := h.store.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListAutomations: %v", err)
	}
	for _, candidate := range automations {
		if candidate.ID == h.automation.ID || candidate.PlanID != h.plan.ID || candidate.SchedulePolicy != "post_implementation_review" {
			continue
		}
		runs, err := h.store.ListRuns(ctx, projectautomation.RunFilter{ProjectID: "project-1", AutomationID: candidate.ID, PlanID: h.plan.ID})
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		for _, reviewRun := range runs {
			if reviewRun.Status == projectautomation.RunStatusQueued {
				reviewQueued = true
			}
		}
	}
	return parityOutcome{
		TerminalRunStatus:   run.Status,
		TaskStatus:          task.Status,
		RunAttemptCount:     run.AttemptCount,
		AttemptRows:         h.store.attemptRows(h.run.ID),
		VerifierRefsPresent: len(task.VerifierResultRefs) > 0,
		ReviewRefsPresent:   len(task.ReviewResultRefs) > 0,
		FailureCategory:     run.FailureCategory,
		ReviewRunQueued:     reviewQueued,
	}
}

// --- port adapters over the real service (durable path) -------------------------

type parityPorts struct {
	harness *parityHarness
}

func (p *parityPorts) snapshotForRun(ctx context.Context, ref projectdurable.SafeAutomationRunRef) (projectdurable.DurableRunSnapshot, error) {
	run, err := p.harness.svc.GetRun(ctx, ref.ProjectID, ref.RunID)
	if err != nil {
		return projectdurable.DurableRunSnapshot{}, err
	}
	category := projectdurable.DurableFailureCategory(run.FailureCategory)
	if category.Validate() != nil {
		category = projectdurable.FailureCategoryNone
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
		FailureCategory: category,
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

func (p *parityPorts) LoadRunSnapshot(ctx context.Context, ref projectdurable.SafeAutomationRunRef) (projectdurable.DurableRunSnapshot, error) {
	return p.snapshotForRun(ctx, ref)
}

func (p *parityPorts) ClaimRun(ctx context.Context, ref projectdurable.SafeAutomationRunRef, runnerID string) (projectdurable.DurableRunSnapshot, string, error) {
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

func (p *parityPorts) CompleteAttempt(ctx context.Context, ref projectdurable.SafeAutomationRunRef, outcome projectdurable.DurableAttemptOutcome) (projectdurable.DurableRunSnapshot, error) {
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

type parityShadowWriter struct {
	mu     sync.Mutex
	fields map[string]string
}

func (w *parityShadowWriter) WriteShadowComparison(_ context.Context, _ projectdurable.SafeAutomationRunRef, fields map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	copied := make(map[string]string, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	w.fields = copied
	return nil
}
