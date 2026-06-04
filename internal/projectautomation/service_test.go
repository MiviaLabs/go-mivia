package projectautomation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestSubmitRunRequiresCodexWhenAvailable(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, AllowManualRunner: true, MaxParallelTasks: 2})
	svc.codexAvailable = func() bool { return true }
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, RunnerKind: RunnerKindManual})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.Status != RunStatusPolicyDenied {
		t.Fatalf("expected policy_denied, got %q", run.Status)
	}
	if run.FailureCategory != "invalid_project_automation_input:_codex_cli_required" {
		t.Fatalf("unexpected failure category: %q", run.FailureCategory)
	}
}

func TestSubmitRunDefaultsToCodexWhenAvailable(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, MaxParallelTasks: 2})
	svc.codexAvailable = func() bool { return true }
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		t.Fatalf("expected codex runner, got %q", run.RunnerKind)
	}
	if run.Status != RunStatusQueued {
		t.Fatalf("expected queued, got %q", run.Status)
	}
}

func TestWorkPlanStatusTriggerQueuesAutomaticRunsOnce(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: RunnerExecutionExternal,
		WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	event := projectworkplan.WorkPlanStatusChange{
		ProjectID: "project-1",
		PlanID:    "plan-1",
		PlanRef:   "plan/ref",
		OldStatus: projectworkplan.WorkPlanStatusPlanned,
		NewStatus: projectworkplan.WorkPlanStatusActive,
		ChangedAt: time.Unix(100, 0).UTC(),
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, event); err != nil {
		t.Fatalf("HandleWorkPlanStatusChanged returned error: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, event); err != nil {
		t.Fatalf("duplicate HandleWorkPlanStatusChanged returned error: %v", err)
	}
	runs, err := svc.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one queued run, got %d", len(runs))
	}
	if runs[0].Status != RunStatusQueued || runs[0].RunnerKind != RunnerKindCodexCLI {
		t.Fatalf("unexpected run: %+v", runs[0])
	}
	if runs[0].OrchestratorRunID == "" {
		t.Fatal("expected trigger idempotency key")
	}
}

func TestWorkPlanStatusTriggerIgnoresUnconfiguredStatus(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: RunnerExecutionExternal,
		WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID: "project-1",
		PlanID:    "plan-1",
		NewStatus: projectworkplan.WorkPlanStatusNeedsReview,
	}); err != nil {
		t.Fatalf("HandleWorkPlanStatusChanged returned error: %v", err)
	}
	runs, err := svc.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs, got %d", len(runs))
	}
}

func TestComputeParallelBatchRejectsConflictingFiles(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
		"task-b": readyTask("task-b", "b", []string{"internal/foo.go"}),
		"task-c": readyTask("task-c", "c", []string{"internal/bar.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 3})
	svc.codexAvailable = func() bool { return true }

	batch, err := svc.ComputeParallelBatch(ctx, ComputeParallelBatchInput{ProjectID: "project-1", PlanID: "plan-1", OrchestratorRunID: "run-orchestrator"})
	if err != nil {
		t.Fatalf("ComputeParallelBatch returned error: %v", err)
	}
	if len(batch.TaskIDs) != 2 {
		t.Fatalf("expected 2 non-conflicting tasks, got %#v", batch.TaskIDs)
	}
	seen := map[string]bool{}
	for _, id := range batch.TaskIDs {
		seen[id] = true
	}
	if seen["task-a"] && seen["task-b"] {
		t.Fatalf("conflicting tasks were batched together: %#v", batch.TaskIDs)
	}
	if !seen["task-c"] {
		t.Fatalf("expected non-conflicting task-c in batch: %#v", batch.TaskIDs)
	}
}

func TestComputeParallelBatchSkipsNotReadyTasks(t *testing.T) {
	ctx := context.Background()
	notReady := readyTask("task-a", "a", []string{"internal/foo.go"})
	notReady.DecompositionQuality = projectworkplan.DecompositionMissingVerification
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": notReady}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 2})

	if _, err := svc.ComputeParallelBatch(ctx, ComputeParallelBatchInput{ProjectID: "project-1", PlanID: "plan-1", OrchestratorRunID: "run-orchestrator"}); err == nil {
		t.Fatal("expected no parallel-safe tasks error")
	}
}

func TestComputeParallelBatchSkipsTasksWithOpenDependencies(t *testing.T) {
	ctx := context.Background()
	dependent := readyTask("task-b", "b", []string{"internal/bar.go"})
	dependent.DependencyTaskIDs = []string{"task-a"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
		"task-b": dependent,
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 2})

	batch, err := svc.ComputeParallelBatch(ctx, ComputeParallelBatchInput{ProjectID: "project-1", PlanID: "plan-1", OrchestratorRunID: "run-orchestrator"})
	if err != nil {
		t.Fatalf("ComputeParallelBatch returned error: %v", err)
	}
	if len(batch.TaskIDs) != 1 || batch.TaskIDs[0] != "task-a" {
		t.Fatalf("expected only dependency-free task, got %#v", batch.TaskIDs)
	}
}

func TestRunNowExecutesCodexCLIAndLeavesTaskForVerification(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, MaxParallelTasks: 1, DefaultMaxRuntime: 3 * time.Minute})
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	svc.codexAvailable = func() bool { return true }
	svc.codexPath = func() (string, bool) { return "/usr/local/bin/codex", true }
	var sawInput bool
	svc.codexRunner = func(_ context.Context, command CodexCommand, maxOutputBytes int64) (CodexRunResult, error) {
		if command.Path != "/usr/local/bin/codex" {
			t.Fatalf("unexpected codex path: %q", command.Path)
		}
		if command.Timeout != 3*time.Minute {
			t.Fatalf("unexpected timeout: %s", command.Timeout)
		}
		if maxOutputBytes != 64*1024 {
			t.Fatalf("unexpected output cap: %d", maxOutputBytes)
		}
		inputPath := command.StdinFile
		data, err := os.ReadFile(inputPath)
		if err != nil {
			t.Fatalf("expected transient input file: %v", err)
		}
		var payload CodexTaskInput
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("decode input: %v", err)
		}
		if payload.TaskID != "task-a" || payload.AutomationRunID == "" || payload.VerificationRequirement == "" {
			t.Fatalf("unexpected codex input: %+v", payload)
		}
		sawInput = true
		return CodexRunResult{ExitCode: 0, Duration: 2 * time.Second}, nil
	}
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if !sawInput {
		t.Fatal("expected codex runner to execute")
	}
	if run.Status != RunStatusVerifying {
		t.Fatalf("expected verifier-required status, got %q", run.Status)
	}
	if run.SafeSummary != "codex_cli_completed_verification_required" {
		t.Fatalf("unexpected safe summary: %q", run.SafeSummary)
	}
	if len(svc.store.(*testStore).attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(svc.store.(*testStore).attempts))
	}
}

func TestExternalRunNowQueuesWithoutServerSideCodex(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	svc.codexRunner = func(context.Context, CodexCommand, int64) (CodexRunResult, error) {
		t.Fatal("external mode must not execute codex in the server process")
		return CodexRunResult{}, nil
	}
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if run.RunnerKind != RunnerKindCodexCLI {
		t.Fatalf("expected codex runner in external mode, got %q", run.RunnerKind)
	}
	if run.Status != RunStatusQueued {
		t.Fatalf("expected queued run, got %q", run.Status)
	}
	if run.SafeSummary != "external_runner_queued" {
		t.Fatalf("unexpected summary: %q", run.SafeSummary)
	}
}

func TestExternalClaimAndCompleteAttempt(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1, DefaultMaxRuntime: 7 * time.Minute})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != queued.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("unexpected claimed run: %+v", claimed.Run)
	}
	if claimed.CodexInput.TaskID != "task-a" || claimed.CodexInput.AutomationRunID != queued.ID {
		t.Fatalf("unexpected codex input: %+v", claimed.CodexInput)
	}
	if claimed.TimeoutMS != (7 * time.Minute).Milliseconds() {
		t.Fatalf("unexpected timeout: %d", claimed.TimeoutMS)
	}

	done, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted, DurationMS: 1234})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if done.Status != RunStatusVerifying {
		t.Fatalf("expected verifying after external completion, got %q", done.Status)
	}
	if len(svc.store.(*testStore).attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(svc.store.(*testStore).attempts))
	}
}

func TestExternalClaimBlocksUntilRequiredAutomationReviewDone(t *testing.T) {
	ctx := context.Background()
	reviewTask := readyTask("automation-review", "automation-review", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a":            readyTask("task-a", "a", []string{"internal/foo.go"}),
		"automation-review": reviewTask,
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:             "project-1",
		AutomationRef:         "auto/review-gated",
		Title:                 "Review gated automation",
		Purpose:               "Require review before external claim",
		AgentID:               "agent-1",
		PlanID:                "plan-1",
		RequiredReviewTaskIDs: []string{"automation-review"},
		PermissionRef:         "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	now := time.Unix(100, 0).UTC()
	queued := AutomationRun{ID: "run-review-gated", ProjectID: "project-1", AutomationID: automation.ID, AgentID: "agent-1", PlanID: "plan-1", TaskID: "task-a", Status: RunStatusQueued, RunnerKind: RunnerKindCodexCLI, CreatedAt: now, UpdatedAt: now}
	if _, err := svc.store.CreateRun(ctx, queued); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil {
		t.Fatal("expected open automation review gate to block external claim")
	}
	blocked, err := svc.GetRun(ctx, "project-1", queued.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if blocked.Status != RunStatusBlocked || blocked.FailureCategory != "automation_review_gate_open" {
		t.Fatalf("expected blocked review gate run, got %#v", blocked)
	}

	reviewTask.Status = projectworkplan.WorkTaskStatusDone
	fake.tasks["automation-review"] = reviewTask
	queued.ID = "run-review-done"
	queued.Status = RunStatusQueued
	if _, err := svc.store.CreateRun(ctx, queued); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error after review: %v", err)
	}
	if claimed.Run.ID != queued.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("unexpected claimed run after review: %#v", claimed.Run)
	}
}

func TestRunStartAttachesGovernanceActionEvidence(t *testing.T) {
	ctx := context.Background()
	fakeTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	governance := &fakeGovernance{actionRef: "evidence/action-start"}
	svc := New(newTestStore(), fakeTasks, Options{
		Enabled: true, RunnerEnabled: true, RequireCodexWhenAvailable: true, MaxParallelTasks: 1,
		Governance: GovernanceOptions{Evidence: governance},
	})
	svc.codexAvailable = func() bool { return true }
	svc.codexPath = func() (string, bool) { return "/usr/local/bin/codex", true }
	svc.codexRunner = func(context.Context, CodexCommand, int64) (CodexRunResult, error) {
		return CodexRunResult{ExitCode: 0, Duration: time.Second}, nil
	}
	automation := createTestAutomation(t, ctx, svc)

	if _, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"}); err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if governance.actionCalls != 1 {
		t.Fatalf("expected one action call, got %d", governance.actionCalls)
	}
	if !contains(fakeTasks.evidenceRefs, "evidence/action-start") {
		t.Fatalf("expected action evidence ref attachment, got %#v", fakeTasks.evidenceRefs)
	}
}

func TestCompleteAttemptAttachesGovernanceOutcomeAndRefs(t *testing.T) {
	ctx := context.Background()
	fakeTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	governance := &fakeGovernance{outcomeRef: "evidence/outcome-pass"}
	svc := New(newTestStore(), fakeTasks, Options{
		Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1,
		Governance: GovernanceOptions{Evidence: governance},
	})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}

	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted, DurationMS: 1234, VerifierResultRefs: []string{"verifier/pass"}, ReviewRefs: []string{"review/approved"}}); err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if governance.outcomeCalls != 1 {
		t.Fatalf("expected one outcome call, got %d", governance.outcomeCalls)
	}
	if !contains(fakeTasks.evidenceRefs, "evidence/outcome-pass") {
		t.Fatalf("expected outcome evidence ref attachment, got %#v", fakeTasks.evidenceRefs)
	}
	if !contains(fakeTasks.verifierRefs, "verifier/pass") {
		t.Fatalf("expected verifier ref attachment, got %#v", fakeTasks.verifierRefs)
	}
	if !contains(fakeTasks.reviewRefs, "review/approved") {
		t.Fatalf("expected review ref attachment, got %#v", fakeTasks.reviewRefs)
	}
}

func TestNilGovernanceHooksDoNotPanic(t *testing.T) {
	ctx := context.Background()
	fakeTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fakeTasks, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted}); err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
}

func TestKnowledgeCandidateRequiresVerifierAndReviewRefs(t *testing.T) {
	ctx := context.Background()
	runCase := func(t *testing.T, verifierRefs []string, reviewRefs []string) *fakeGovernance {
		t.Helper()
		fakeTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
			"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
		}}
		governance := &fakeGovernance{outcomeRef: "evidence/outcome-pass", confidenceRef: "confidence/score", candidateRef: "knowledge/candidate"}
		svc := New(newTestStore(), fakeTasks, Options{
			Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1,
			Governance: GovernanceOptions{Evidence: governance, Confidence: governance, Knowledge: governance},
		})
		svc.codexAvailable = func() bool { return false }
		automation := createTestAutomation(t, ctx, svc)
		queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
		if err != nil {
			t.Fatalf("RunNow returned error: %v", err)
		}
		if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
			t.Fatalf("ClaimNextRun returned error: %v", err)
		}
		if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted, ClaimRefs: []string{"claim/ref"}, VerifierResultRefs: verifierRefs, ReviewRefs: reviewRefs}); err != nil {
			t.Fatalf("CompleteAttempt returned error: %v", err)
		}
		return governance
	}

	if governance := runCase(t, []string{"verifier/pass"}, nil); governance.candidateCalls != 0 {
		t.Fatalf("expected no candidate without review refs, got %d", governance.candidateCalls)
	}
	if governance := runCase(t, nil, []string{"review/approved"}); governance.candidateCalls != 0 {
		t.Fatalf("expected no candidate without verifier refs, got %d", governance.candidateCalls)
	}
	if governance := runCase(t, []string{"verifier/pass"}, []string{"review/approved"}); governance.candidateCalls != 1 {
		t.Fatalf("expected one candidate with verifier and review refs, got %d", governance.candidateCalls)
	}
}

func TestCompleteAttemptRejectsUnclaimedQueuedRun(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}

	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted}); err == nil {
		t.Fatal("expected unclaimed queued run completion to fail")
	}
	if len(svc.store.(*testStore).attempts) != 0 {
		t.Fatalf("expected no attempt for rejected completion, got %d", len(svc.store.(*testStore).attempts))
	}
}

func TestCreateWorkflowAutomationRequiresPermissionSnapshotRef(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{AllowManualRunner: true})

	if _, err := svc.CreateAutomation(ctx, CreateAutomationInput{ProjectID: "project-1", AutomationRef: "auto/ref", Title: "Automation", Purpose: "Run safe work tasks", AgentID: "agent-1", PlanID: "plan-1", SourceKind: AutomationSourceWorkflow}); err == nil {
		t.Fatal("expected missing workflow permission ref to fail")
	}
	if _, err := svc.CreateAutomation(ctx, CreateAutomationInput{ProjectID: "project-1", AutomationRef: "auto/ref", Title: "Automation", Purpose: "Run safe work tasks", AgentID: "agent-1", PlanID: "plan-1", SourceKind: AutomationSourceWorkflow, PermissionRef: "permission/default"}); err == nil {
		t.Fatal("expected malformed workflow permission ref to fail")
	}
}

func TestSubmitWorkflowAutomationRejectsPermissionAgentMismatch(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "allowed-task", []string{"internal/foo.go"}),
	}}
	resolver := &fakePermissionResolver{metadata: PermissionSnapshotMetadata{AgentID: "other-agent", AllowedRunnerKinds: []string{RunnerKindCodexCLI}}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1, PermissionResolver: resolver})
	svc.codexAvailable = func() bool { return true }
	automation := createWorkflowAutomation(t, ctx, svc, []string{"allowed-task"}, "agent-1")

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.Status != RunStatusPolicyDenied || run.FailureCategory != "invalid_project_automation_input:_permission_agent_mismatch" {
		t.Fatalf("expected permission mismatch denial, got %#v", run)
	}
}

func TestSubmitWorkflowAutomationRejectsTaskOutsideAllowedRefs(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "outside-task", []string{"internal/foo.go"}),
	}}
	resolver := &fakePermissionResolver{metadata: PermissionSnapshotMetadata{AgentID: "agent-1", AllowedRunnerKinds: []string{RunnerKindCodexCLI}}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1, PermissionResolver: resolver})
	svc.codexAvailable = func() bool { return true }
	automation := createWorkflowAutomation(t, ctx, svc, []string{"allowed-task"}, "agent-1")

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.Status != RunStatusPolicyDenied || run.FailureCategory != "invalid_project_automation_input:_task_ref_not_allowed" {
		t.Fatalf("expected task ref denial, got %#v", run)
	}
}

func TestRunNowWorkflowAutomationRejectsUnspecifiedTaskOutsideAllowedRefs(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "outside-task", []string{"internal/foo.go"}),
	}}
	resolver := &fakePermissionResolver{metadata: PermissionSnapshotMetadata{AgentID: "agent-1", AllowedRunnerKinds: []string{RunnerKindCodexCLI}}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1, PermissionResolver: resolver})
	svc.codexAvailable = func() bool { return true }
	svc.codexPath = func() (string, bool) { return "/usr/local/bin/codex", true }
	automation := createWorkflowAutomation(t, ctx, svc, []string{"allowed-task"}, "agent-1")

	run, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, RunnerKind: RunnerKindCodexCLI})
	if err == nil {
		t.Fatal("expected RunNow to return the blocked start error")
	}
	if run.Status != RunStatusBlocked || run.FailureCategory != "task_unavailable" {
		t.Fatalf("expected unresolved allowed task to block start, got %#v", run)
	}
}

func TestSubmitWorkflowAutomationWithValidPermissionAndAllowedTaskQueues(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "allowed-task", []string{"internal/foo.go"}),
	}}
	resolver := &fakePermissionResolver{metadata: PermissionSnapshotMetadata{AgentID: "agent-1", AllowedRunnerKinds: []string{RunnerKindCodexCLI}}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 1, PermissionResolver: resolver})
	svc.codexAvailable = func() bool { return true }
	automation := createWorkflowAutomation(t, ctx, svc, []string{"allowed-task"}, "agent-1")

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.Status != RunStatusQueued || run.RunnerKind != RunnerKindCodexCLI {
		t.Fatalf("expected valid workflow run to queue, got %#v", run)
	}
}

func TestSubmitManualAutomationWithoutPermissionRemainsCompatible(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{AllowManualRunner: true, MaxParallelTasks: 1})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{ProjectID: "project-1", AutomationRef: "manual/ref", Title: "Manual", Purpose: "Manual metadata run", AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}

	run, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, RunnerKind: RunnerKindManual})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if run.Status != RunStatusQueued || run.RunnerKind != RunnerKindManual {
		t.Fatalf("expected manual run to remain compatible, got %#v", run)
	}
}

func newTestService(t *testing.T, options Options) *Service {
	t.Helper()
	svc := New(newTestStore(), &fakeWorkTasks{}, options)
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	return svc
}

func createTestAutomation(t *testing.T, ctx context.Context, svc *Service) Automation {
	t.Helper()
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{ProjectID: "project-1", AutomationRef: "auto/ref", Title: "Automation", Purpose: "Run safe work tasks", AgentID: "agent-1", PlanID: "plan-1", PermissionRef: "permission/default"})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	return automation
}

func createAutomaticTriggerAutomation(t *testing.T, ctx context.Context, svc *Service) Automation {
	t.Helper()
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:     "project-1",
		AutomationRef: "auto/status-trigger",
		Title:         "Status trigger automation",
		Purpose:       "Run safe work tasks after plan status changes",
		Status:        AutomationStatusEnabled,
		AgentID:       "agent-1",
		PlanID:        "plan-1",
		TriggerKind:   TriggerKindAutomatic,
		PermissionRef: "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	return automation
}

func createWorkflowAutomation(t *testing.T, ctx context.Context, svc *Service, allowedTaskRefs []string, agentID string) Automation {
	t.Helper()
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "workflow/ref",
		Title:           "Workflow Automation",
		Purpose:         "Run governed workflow task metadata",
		AgentID:         agentID,
		PlanID:          "plan-1",
		AllowedTaskRefs: allowedTaskRefs,
		PermissionRef:   "permission_snapshot:snapshot-1",
		SourceKind:      AutomationSourceWorkflow,
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	return automation
}

func readyTask(id string, ref string, files []string) projectworkplan.WorkTask {
	return projectworkplan.WorkTask{
		ID:                      id,
		ProjectID:               "project-1",
		PlanID:                  "plan-1",
		TaskRef:                 ref,
		Title:                   "Task " + ref,
		Status:                  projectworkplan.WorkTaskStatusReady,
		LikelyFilesAffected:     files,
		VerificationRequirement: "orchestrator runs focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}
}

type fakeWorkTasks struct {
	tasks         map[string]projectworkplan.WorkTask
	evidenceRefs  []string
	verifierRefs  []string
	reviewRefs    []string
	knowledgeRefs []string
}

type fakePermissionResolver struct {
	metadata PermissionSnapshotMetadata
	err      error
}

func (fake *fakePermissionResolver) CheckAutomationPermission(context.Context, PermissionCheckInput) (PermissionSnapshotMetadata, error) {
	if fake.err != nil {
		return PermissionSnapshotMetadata{}, fake.err
	}
	return fake.metadata, nil
}

type testStore struct {
	mu         sync.Mutex
	automation map[string]Automation
	runs       map[string]AutomationRun
	batches    map[string]AutomationParallelBatch
	attempts   map[string]AutomationAttempt
}

func newTestStore() *testStore {
	return &testStore{automation: map[string]Automation{}, runs: map[string]AutomationRun{}, batches: map[string]AutomationParallelBatch{}, attempts: map[string]AutomationAttempt{}}
}

func (store *testStore) CreateAutomation(_ context.Context, value Automation) (Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.automation[value.ID] = value
	return value, nil
}

func (store *testStore) GetAutomation(_ context.Context, projectID string, automationID string) (Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.automation[automationID]
	if !ok || value.ProjectID != projectID {
		return Automation{}, errors.New("not found")
	}
	return value, nil
}

func (store *testStore) ListAutomations(_ context.Context, filter AutomationFilter) ([]Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []Automation
	for _, value := range store.automation {
		if value.ProjectID == filter.ProjectID {
			out = append(out, value)
		}
	}
	return out, nil
}

func (store *testStore) UpdateAutomation(_ context.Context, value Automation) (Automation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.automation[value.ID] = value
	return value, nil
}

func (store *testStore) CreateRun(_ context.Context, value AutomationRun) (AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.runs[value.ID] = value
	return value, nil
}

func (store *testStore) GetRun(_ context.Context, projectID string, runID string) (AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.runs[runID]
	if !ok || value.ProjectID != projectID {
		return AutomationRun{}, errors.New("not found")
	}
	return value, nil
}

func (store *testStore) ListRuns(_ context.Context, filter RunFilter) ([]AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []AutomationRun
	for _, value := range store.runs {
		if value.ProjectID != filter.ProjectID {
			continue
		}
		if filter.AutomationID != "" && value.AutomationID != filter.AutomationID {
			continue
		}
		if filter.PlanID != "" && value.PlanID != filter.PlanID {
			continue
		}
		if filter.Status != "" && value.Status != filter.Status {
			continue
		}
		if filter.OrchestratorRunID != "" && value.OrchestratorRunID != filter.OrchestratorRunID {
			continue
		}
		out = append(out, value)
	}
	return out, nil
}

func (store *testStore) UpdateRun(_ context.Context, value AutomationRun) (AutomationRun, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.runs[value.ID] = value
	return value, nil
}

func (store *testStore) CreateAttempt(_ context.Context, value AutomationAttempt) (AutomationAttempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.attempts[value.ID] = value
	return value, nil
}

func (store *testStore) CreateParallelBatch(_ context.Context, value AutomationParallelBatch) (AutomationParallelBatch, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.batches[value.ID] = value
	return value, nil
}

func (store *testStore) GetParallelBatch(_ context.Context, projectID string, batchID string) (AutomationParallelBatch, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.batches[batchID]
	if !ok || value.ProjectID != projectID {
		return AutomationParallelBatch{}, errors.New("not found")
	}
	return value, nil
}

func (fake *fakeWorkTasks) GetWorkTask(_ context.Context, _ string, taskID string) (projectworkplan.WorkTask, error) {
	if fake.tasks == nil {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	task, ok := fake.tasks[taskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	return task, nil
}

func (fake *fakeWorkTasks) ListOpenWorkTasks(_ context.Context, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	var out []projectworkplan.WorkTask
	for _, task := range fake.tasks {
		if task.ProjectID != filter.ProjectID {
			continue
		}
		if filter.PlanID != "" && task.PlanID != filter.PlanID {
			continue
		}
		out = append(out, task)
	}
	return out, nil
}

func (fake *fakeWorkTasks) ClaimWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) StartWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) AttachEvidence(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.evidenceRefs = append(fake.evidenceRefs, input.Ref)
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachVerifierResult(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.verifierRefs = append(fake.verifierRefs, input.Ref)
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachReviewResult(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.reviewRefs = append(fake.reviewRefs, input.Ref)
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachKnowledgeCandidate(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.knowledgeRefs = append(fake.knowledgeRefs, input.Ref)
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) CompleteWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) FailWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) BlockWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

type fakeGovernance struct {
	actionRef       string
	outcomeRef      string
	confidenceRef   string
	candidateRef    string
	actionCalls     int
	outcomeCalls    int
	confidenceCalls int
	candidateCalls  int
}

func (fake *fakeGovernance) CreateActionRef(context.Context, GovernanceActionInput) (string, error) {
	fake.actionCalls++
	return fake.actionRef, nil
}

func (fake *fakeGovernance) CreateOutcomeRef(context.Context, GovernanceOutcomeInput) (string, error) {
	fake.outcomeCalls++
	return fake.outcomeRef, nil
}

func (fake *fakeGovernance) RecordConfidenceRef(context.Context, GovernanceConfidenceInput) (string, error) {
	fake.confidenceCalls++
	return fake.confidenceRef, nil
}

func (fake *fakeGovernance) CreateCandidateRef(context.Context, GovernanceKnowledgeCandidateInput) (string, error) {
	fake.candidateCalls++
	return fake.candidateRef, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
