package projectautomation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
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

func TestCallAutomationToolCreateAcceptsCommonCompatibilityAliases(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true})

	value, err := svc.CallAutomationTool(ctx, "projects.automations.create", json.RawMessage(`{
		"id":"project-1",
		"automation_ref":"automation/alias",
		"title":"Alias automation",
		"expected_output":"Run a bounded implementation task.",
		"executor":"codex_cli",
		"runner_mode":"external",
		"work_plan_id":"plan-1",
		"work_task_id":"task-1",
		"allowed_work_task_ids":["task-2"],
		"required_review_task_ids":["review-1"],
		"trigger_mode":"automatic",
		"permission_snapshot_ref":"permission-1",
		"created_by_run_id":"run-1"
	}`))
	if err != nil {
		t.Fatalf("CallAutomationTool returned error: %v", err)
	}
	automation, ok := value.(Automation)
	if !ok {
		t.Fatalf("expected Automation result, got %T", value)
	}
	if automation.Purpose != "Run a bounded implementation task." || automation.AgentID != "codex_cli" || automation.PlanID != "plan-1" || automation.PermissionRef != "permission-1" || automation.TriggerKind != TriggerKindAutomatic {
		t.Fatalf("aliases were not normalized correctly: %+v", automation)
	}
	for _, want := range []string{"task-1", "task-2"} {
		if !containsString(automation.AllowedTaskRefs, want) {
			t.Fatalf("allowed task refs missing %q: %+v", want, automation.AllowedTaskRefs)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRenderCodexTaskPromptIncludesExecutionInstructions(t *testing.T) {
	prompt := RenderCodexTaskPrompt(CodexTaskInput{
		ProjectID:               "project-1",
		AutomationRunID:         "run-1",
		PlanID:                  "plan-1",
		TaskID:                  "task-1",
		TaskRef:                 "task/ref",
		Title:                   "Create smoke marker",
		Description:             "Create automation-smoke.txt with one line.",
		LikelyFilesAffected:     []string{"automation-smoke.txt"},
		VerificationRequirement: "orchestrator checks the file exists",
		RunnerInstructions: []string{
			"Do not run verifier commands unless this task explicitly allows worker verification.",
			"Leave verifier execution and task completion to the orchestrator.",
		},
	})

	for _, want := range []string{
		"Perform the task now",
		"Create smoke marker",
		"automation-smoke.txt",
		"Do not run full test suites",
		"Do not call projects.automation_runs.complete_attempt",
		"runner commits, pushes, and opens draft PRs",
		"Implementation workers must not self-review.",
		"Do not use colons, slashes, paths, commands, raw logs, or source snippets as refs.",
		"Leave verifier execution and task completion to the orchestrator.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rendered prompt missing %q:\n%s", want, prompt)
		}
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

func TestUpdateAutomationStatusDisablesExistingAutomation(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 2})
	automation := createTestAutomation(t, ctx, svc)

	updated, err := svc.UpdateAutomationStatus(ctx, UpdateAutomationStatusInput{
		ProjectID:    automation.ProjectID,
		AutomationID: automation.ID,
		Status:       AutomationStatusDisabled,
		RunID:        "run-disable",
		TraceID:      "trace-disable",
	})
	if err != nil {
		t.Fatalf("UpdateAutomationStatus returned error: %v", err)
	}
	if updated.Status != AutomationStatusDisabled {
		t.Fatalf("expected disabled status, got %q", updated.Status)
	}
	if updated.TraceID != "trace-disable" {
		t.Fatalf("expected trace to update, got %q", updated.TraceID)
	}
	if !updated.UpdatedAt.After(automation.UpdatedAt) && !updated.UpdatedAt.Equal(automation.UpdatedAt) {
		t.Fatalf("unexpected updated_at: before original")
	}
}

func TestUpdateAutomationStatusRejectsUnknownStatus(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, Options{Enabled: true, RunnerEnabled: true, MaxParallelTasks: 2})
	automation := createTestAutomation(t, ctx, svc)

	if _, err := svc.UpdateAutomationStatus(ctx, UpdateAutomationStatusInput{
		ProjectID:    automation.ProjectID,
		AutomationID: automation.ID,
		Status:       "deleted",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestWorkPlanStatusTriggerQueuesAutomaticRunsOnce(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "a", []string{"internal/foo.go"}),
	}}
	svc := New(newTestStore(), fake, Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: RunnerExecutionExternal,
		WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
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
	if runs[0].TaskID != "task-a" {
		t.Fatalf("expected automatic run to resolve ready task, got %q", runs[0].TaskID)
	}
}

func TestClaimNextRunRecoversRunningRunWhenTaskMovedToVerifying(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": {
			ID:                      "task-a",
			ProjectID:               "project-1",
			PlanID:                  "plan-1",
			TaskRef:                 "implement-a",
			Title:                   "Implement A",
			Status:                  projectworkplan.WorkTaskStatusVerifying,
			FilesToEdit:             []string{"internal/foo.go"},
			VerificationRequirement: "orchestrator verifies",
			VerifierResultRefs:      []string{"verifier-a"},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		},
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: "automation-a", AgentID: "agent-a",
		PlanID: "plan-1", TaskID: "task-a", Status: RunStatusRunning, RunnerKind: RunnerKindCodexCLI,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after recovery, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusVerifying {
		t.Fatalf("expected stale running run to recover to verifying, got %#v", run)
	}
	if run.WorkTaskStatus != projectworkplan.WorkTaskStatusVerifying || run.SafeSummary != "external_codex_cli_completed_verification_required" {
		t.Fatalf("unexpected recovered run metadata: %#v", run)
	}
}

func TestClaimNextRunReconcilesRunningRunMovedToVerifyingBeforeClaimingQueuedRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	taskA := readyTask("task-a", "scan-a", []string{"internal/foo.go"})
	taskA.Status = projectworkplan.WorkTaskStatusVerifying
	taskA.FilesToEdit = []string{"internal/foo.go"}
	taskA.VerifierResultRefs = []string{"verifier-a"}
	taskA.ClaimedByRunID = "run-a"
	taskB := readyTask("task-b", "scan-b", []string{"internal/bar.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		taskA.ID: taskA,
		taskB.ID: taskB,
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 10})
	svc.codexAvailable = func() bool { return false }
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createTestAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-a",
		ProjectID:      automation.ProjectID,
		AutomationID:   automation.ID,
		AgentID:        automation.AgentID,
		PlanID:         taskA.PlanID,
		TaskID:         taskA.ID,
		WorkTaskStatus: projectworkplan.WorkTaskStatusInProgress,
		Status:         RunStatusRunning,
		RunnerKind:     RunnerKindCodexCLI,
		CreatedAt:      time.Unix(100, 0).UTC(),
		UpdatedAt:      time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: taskB.ID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != queued.ID {
		t.Fatalf("expected queued run to be claimed after stale run reconciliation, got %q", claimed.Run.ID)
	}
	recovered, err := store.GetRun(ctx, automation.ProjectID, "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if recovered.Status != RunStatusVerifying || recovered.WorkTaskStatus != projectworkplan.WorkTaskStatusVerifying {
		t.Fatalf("expected stale running run to become verifying before queued claim, got %#v", recovered)
	}
	if recovered.SafeSummary != "external_codex_cli_completed_verification_required" {
		t.Fatalf("unexpected recovered safe summary: %q", recovered.SafeSummary)
	}
}

func TestClaimNextRunRecoversFailedWorktreeResolveForReadyTask(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": readyTask("task-a", "fix-a", []string{"internal/foo.go"}),
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: "task-a", Status: RunStatusFailed, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "worktree_resolve_failed",
		CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-a" || claimed.Run.Status != RunStatusRunning || claimed.Run.AttemptCount != 1 || claimed.Run.FailureCategory != "" {
		t.Fatalf("expected failed worktree resolve run to be reclaimed, got %#v", claimed.Run)
	}
}

func TestClaimNextRunRecoversFailedWorktreeResolveForInProgressTaskClaimedByRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: "task-a", Status: RunStatusFailed, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "worktree_resolve_failed",
		CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-a" || claimed.Run.Status != RunStatusRunning || claimed.Run.AttemptCount != 1 || claimed.Run.FailureCategory != "" {
		t.Fatalf("expected failed in-progress worktree resolve run to be reclaimed, got %#v", claimed.Run)
	}
}

func TestClaimNextRunRecoversGitOpsPreTaskFailureForReadyTask(t *testing.T) {
	for _, category := range []string{"worktree_prepare_failed", "gitops_pre_task_failed", "gitops_dirty_worktree"} {
		t.Run(category, func(t *testing.T) {
			ctx := context.Background()
			store := newTestStore()
			fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
				"task-a": readyTask("task-a", "fix-a", []string{"internal/foo.go"}),
			}}
			svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
			svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
			automation := createAutomaticTriggerAutomation(t, ctx, svc)
			if _, err := store.CreateRun(ctx, AutomationRun{
				ID: "run-a", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
				PlanID: "plan-1", TaskID: "task-a", Status: RunStatusFailed, RunnerKind: RunnerKindCodexCLI,
				FailureCategory: category,
				CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
			}); err != nil {
				t.Fatalf("CreateRun returned error: %v", err)
			}

			claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
			if err != nil {
				t.Fatalf("ClaimNextRun returned error: %v", err)
			}
			if claimed.Run.ID != "run-a" || claimed.Run.Status != RunStatusRunning || claimed.Run.AttemptCount != 1 || claimed.Run.FailureCategory != "" {
				t.Fatalf("expected gitops pre-task failure run to be reclaimed, got %#v", claimed.Run)
			}
		})
	}
}

func TestClaimNextRunDoesNotValidateStoredLongResumeInstructions(t *testing.T) {
	ctx := context.Background()
	workStore := workstore.NewMemoryStore()
	workSvc := projectworkplan.New(workStore)
	plan, err := workSvc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan-long-resume",
		Title:       "Long resume plan",
		GoalSummary: "Exercise runner claim with legacy task metadata.",
	})
	if err != nil {
		t.Fatalf("CreateWorkPlan returned error: %v", err)
	}
	task, err := workSvc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "task-long-resume",
		Title:                   "Long resume task",
		EvidenceNeeded:          []string{"source evidence"},
		ContextPackRefs:         []string{"context-pack-1"},
		LikelyFilesAffected:     []string{"internal/projectautomation/service.go"},
		VerificationRequirement: "focused tests",
		ExpectedOutput:          "claim succeeds",
		FailureCriteria:         "claim must not rewrite resume instructions",
		ResumeInstructions:      "resume from task metadata",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("CreateWorkTask returned error: %v", err)
	}
	task.ResumeInstructions = strings.Repeat("a", projectworkplan.MaxResumeInstructionsLength+25)
	if _, err := workStore.UpdateWorkTask(ctx, task); err != nil {
		t.Fatalf("UpdateWorkTask returned error: %v", err)
	}

	automationStore := newTestStore()
	svc := New(automationStore, workSvc, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/long-resume",
		Title:           "Long resume automation",
		Purpose:         "Claim legacy long resume task",
		Status:          AutomationStatusEnabled,
		AgentID:         "agent-1",
		PlanID:          plan.ID,
		AllowedTaskRefs: []string{task.ID},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: "project-1", AutomationID: automation.ID, TaskID: task.ID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != queued.ID || claimed.Run.TaskID != task.ID {
		t.Fatalf("expected queued long-resume task to be claimed, got run=%#v", claimed.Run)
	}
}

func TestClaimNextRunSetsClaimLeaseAndCompleteAttemptRequiresMatchingClaimToken(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-lease", "lease-task", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	now := time.Unix(1000, 0).UTC()
	svc.now = func() time.Time { return now }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: task.ID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != queued.ID || claimed.Run.ClaimID == "" || claimed.Run.RunnerID != "runner-1" || claimed.Run.LastHeartbeatAt.IsZero() || claimed.Run.LeaseExpiresAt.IsZero() {
		t.Fatalf("expected claim lease fields, got %+v", claimed.Run)
	}
	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: claimed.Run.ID, Status: RunStatusFailed, FailureCategory: "gitops_dirty_worktree", ClaimID: "wrong-claim", RunnerID: "runner-1"}); err == nil || !strings.Contains(err.Error(), "claim_id does not match") {
		t.Fatalf("expected wrong claim_id to be rejected, got %v", err)
	}
	completed, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: claimed.Run.ID, Status: RunStatusFailed, FailureCategory: "gitops_dirty_worktree", ClaimID: claimed.Run.ClaimID, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	duplicate, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: claimed.Run.ID, Status: RunStatusFailed, FailureCategory: "gitops_dirty_worktree", ClaimID: claimed.Run.ClaimID, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("duplicate CompleteAttempt returned error: %v", err)
	}
	if duplicate.ID != completed.ID || duplicate.Status != completed.Status {
		t.Fatalf("expected idempotent terminal duplicate, got completed=%+v duplicate=%+v", completed, duplicate)
	}
}

func TestHeartbeatRequiresMatchingClaimTokenAndExtendsLease(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-heartbeat", "heartbeat-task", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	now := time.Unix(1000, 0).UTC()
	svc.now = func() time.Time { return now }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: task.ID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if _, err := svc.HeartbeatRun(ctx, HeartbeatRunInput{ProjectID: automation.ProjectID, RunID: claimed.Run.ID, ClaimID: "wrong-claim", RunnerID: "runner-1"}); err == nil {
		t.Fatal("expected wrong heartbeat claim to fail")
	}
	now = now.Add(30 * time.Second)
	heartbeat, err := svc.HeartbeatRun(ctx, HeartbeatRunInput{ProjectID: automation.ProjectID, RunID: claimed.Run.ID, ClaimID: claimed.Run.ClaimID, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("HeartbeatRun returned error: %v", err)
	}
	if !heartbeat.LastHeartbeatAt.Equal(now) || !heartbeat.LeaseExpiresAt.After(claimed.Run.LeaseExpiresAt) {
		t.Fatalf("expected heartbeat to extend lease, before=%+v after=%+v", claimed.Run, heartbeat)
	}
}

func TestClaimNextRunSkipsPreExecutionRecoveryClaimedByAnotherRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-current"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-old", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: task.ID, Status: RunStatusFailed, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "gitops_dirty_worktree",
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected old failed run to stay skipped while task is claimed by another run, got %v", err)
	}
}

func TestClaimNextRunRequeuesExhaustedPreExecutionRecoveryClaimedByFailedRun(t *testing.T) {
	for _, category := range []string{"worktree_prepare_failed", "gitops_dirty_worktree", "gitops_pre_task_failed"} {
		for _, status := range []string{projectworkplan.WorkTaskStatusInProgress, projectworkplan.WorkTaskStatusClaimed, projectworkplan.WorkTaskStatusReady} {
			t.Run(category+"_"+status, func(t *testing.T) {
				ctx := context.Background()
				task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
				task.Status = status
				task.ClaimedByRunID = "run-a"
				fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
				store := newTestStore()
				svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
				svc.codexAvailable = func() bool { return false }
				automation := createAutomaticTriggerAutomation(t, ctx, svc)
				now := time.Now().UTC()
				if _, err := store.CreateRun(ctx, AutomationRun{
					ID:              "run-a",
					ProjectID:       automation.ProjectID,
					AutomationID:    automation.ID,
					AgentID:         automation.AgentID,
					PlanID:          task.PlanID,
					TaskID:          task.ID,
					WorkTaskStatus:  task.Status,
					Status:          RunStatusFailed,
					RunnerKind:      RunnerKindCodexCLI,
					AttemptCount:    defaultAutomationMaxRetries,
					SafeSummary:     "pre_execution_recovery",
					FailureCategory: category,
					CreatedAt:       now,
					UpdatedAt:       now,
				}); err != nil {
					t.Fatalf("CreateRun returned error: %v", err)
				}

				claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
				if err != nil {
					t.Fatalf("ClaimNextRun returned error: %v", err)
				}
				if claimed.Run.ID == "run-a" || claimed.Run.TaskID != task.ID || claimed.Run.Status != RunStatusRunning {
					t.Fatalf("expected replacement implementation run, got %+v", claimed.Run)
				}
				exhausted, err := store.GetRun(ctx, automation.ProjectID, "run-a")
				if err != nil {
					t.Fatalf("GetRun returned error: %v", err)
				}
				expectedSummary := "pre_execution_recovery_requeued_implementation_after_" + category
				if exhausted.FailureCategory != "pre_execution_recovery_failed_requires_implementation" || exhausted.SafeSummary != expectedSummary {
					t.Fatalf("expected exhausted run to be terminalized for implementation, got %+v", exhausted)
				}
				updatedTask := fake.tasks[task.ID]
				if updatedTask.Status != projectworkplan.WorkTaskStatusInProgress || updatedTask.ClaimedByRunID != claimed.Run.ID {
					t.Fatalf("expected replacement claim to restart task, got task=%+v run=%+v", updatedTask, claimed.Run)
				}
				if len(updatedTask.ResumeInstructions) > projectworkplan.MaxResumeInstructionsLength {
					t.Fatalf("expected bounded resume instructions, got length %d", len(updatedTask.ResumeInstructions))
				}
				if !strings.Contains(updatedTask.ResumeInstructions, "Pre-execution recovery failed with "+category) {
					t.Fatalf("expected pre-execution recovery instructions, got %q", updatedTask.ResumeInstructions)
				}
			})
		}
	}
}

func TestRequeuePreExecutionRecoveryExpandsScopeForDirtyPathsUnderLikelyFiles(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     "pre_execution_recovery",
		FailureCategory: "gitops_dirty_worktree_scope",
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	updated, err := svc.requeueTaskAfterPreExecutionRecoveryFailure(ctx, run, run.FailureCategory, []string{"gitops-dirty-path:apps/domain/src/module.ts"})
	if err != nil {
		t.Fatalf("requeueTaskAfterPreExecutionRecoveryFailure returned error: %v", err)
	}
	if updated.Status != RunStatusFailed || updated.FailureCategory != "pre_execution_recovery_failed_requires_implementation" {
		t.Fatalf("expected implementation requeue failure marker, got %+v", updated)
	}
	requeuedTask := fake.tasks[task.ID]
	if requeuedTask.Status != projectworkplan.WorkTaskStatusReady || requeuedTask.ClaimedByRunID != "" {
		t.Fatalf("expected ready task after pre-execution requeue, got %+v", requeuedTask)
	}
	if !containsString(requeuedTask.FilesToEdit, "apps/domain/src") {
		t.Fatalf("expected likely directory to be added to files_to_edit, got %+v", requeuedTask.FilesToEdit)
	}
	if !strings.Contains(requeuedTask.ResumeInstructions, "apps/domain/src/module.ts") {
		t.Fatalf("expected resume instructions to name dirty path, got %q", requeuedTask.ResumeInstructions)
	}
}

func TestRequeuePreExecutionRecoveryBlocksDirtyPathsOutsideLikelyFiles(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     "pre_execution_recovery",
		FailureCategory: "gitops_dirty_worktree_scope",
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	updated, err := svc.requeueTaskAfterPreExecutionRecoveryFailure(ctx, run, run.FailureCategory, []string{"gitops-dirty-path:apps/other/src/module.ts"})
	if err != nil {
		t.Fatalf("requeueTaskAfterPreExecutionRecoveryFailure returned error: %v", err)
	}
	if updated.Status != RunStatusFailed || updated.FailureCategory != "gitops_dirty_worktree_scope_requires_plan" {
		t.Fatalf("expected dirty scope to require a plan, got %+v", updated)
	}
	blockedTask := fake.tasks[task.ID]
	if blockedTask.Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected task blocked, got %+v", blockedTask)
	}
	if !strings.Contains(blockedTask.BlockedReason, "apps/other/src/module.ts") || !strings.Contains(blockedTask.ResumeInstructions, "new plan") {
		t.Fatalf("expected exact path and new-plan instructions, reason=%q resume=%q", blockedTask.BlockedReason, blockedTask.ResumeInstructions)
	}
}

func TestRequeuePreExecutionRecoveryBoundsDirtyPathBlockedReason(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     "pre_execution_recovery",
		FailureCategory: "gitops_dirty_worktree_scope",
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	refs := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		refs = append(refs, fmt.Sprintf("gitops-dirty-path:.unknown/skills/flutter-generated-long-path-%02d/SKILL.md", i))
	}

	updated, err := svc.requeueTaskAfterPreExecutionRecoveryFailure(ctx, run, run.FailureCategory, refs)
	if err != nil {
		t.Fatalf("requeueTaskAfterPreExecutionRecoveryFailure returned error: %v", err)
	}
	if updated.FailureCategory != "gitops_dirty_worktree_scope_requires_plan" {
		t.Fatalf("expected dirty scope to require a plan, got %+v", updated)
	}
	blockedTask := fake.tasks[task.ID]
	if len(blockedTask.BlockedReason) > 500 {
		t.Fatalf("expected blocked reason within service limit, got length %d: %q", len(blockedTask.BlockedReason), blockedTask.BlockedReason)
	}
	if !strings.Contains(blockedTask.BlockedReason, ".unknown/skills/flutter-generated-long-path-00/SKILL.md") {
		t.Fatalf("expected blocked reason to include the first dirty path, got %q", blockedTask.BlockedReason)
	}
	if !strings.Contains(blockedTask.BlockedReason, "more") {
		t.Fatalf("expected blocked reason to summarize omitted paths, got %q", blockedTask.BlockedReason)
	}
}

func TestRequeuePreExecutionRecoveryExpandsAutomationSupportDirtyPaths(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/frontend-mobile/lib"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/frontend-mobile/lib"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     "pre_execution_recovery",
		FailureCategory: "gitops_dirty_worktree_scope",
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	updated, err := svc.requeueTaskAfterPreExecutionRecoveryFailure(ctx, run, run.FailureCategory, []string{
		"gitops-dirty-path:.claude/rules/typescript.md",
		"gitops-dirty-path:.codex/skills/flutter-use-http-package/SKILL.md",
	})
	if err != nil {
		t.Fatalf("requeueTaskAfterPreExecutionRecoveryFailure returned error: %v", err)
	}
	if updated.FailureCategory != "pre_execution_recovery_failed_requires_implementation" {
		t.Fatalf("expected implementation requeue after support path expansion, got %+v", updated)
	}
	requeuedTask := fake.tasks[task.ID]
	for _, want := range []string{".claude", ".codex"} {
		if !containsString(requeuedTask.FilesToEdit, want) {
			t.Fatalf("expected %q to be added to files_to_edit, got %+v", want, requeuedTask.FilesToEdit)
		}
	}
	if requeuedTask.Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected task ready after support path expansion, got %+v", requeuedTask)
	}
}

func TestRequeuePreExecutionRecoveryDoesNotExpandLocalTaskDirtyPaths(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/frontend-mobile/lib"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/frontend-mobile/lib"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     "pre_execution_recovery",
		FailureCategory: "gitops_dirty_worktree_scope",
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	updated, err := svc.requeueTaskAfterPreExecutionRecoveryFailure(ctx, run, run.FailureCategory, []string{"gitops-dirty-path:.ai/tasks/active/local.md"})
	if err != nil {
		t.Fatalf("requeueTaskAfterPreExecutionRecoveryFailure returned error: %v", err)
	}
	if updated.FailureCategory != "gitops_dirty_worktree_scope_requires_plan" {
		t.Fatalf("expected local task path to require a plan, got %+v", updated)
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected local task path to block, got %+v", fake.tasks[task.ID])
	}
}

func TestClaimNextRunRequeuesExhaustedPreExecutionDirtyScopeWithTaskEvidence(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	task.EvidenceRefs = []string{"automation_run:run-a", "gitops-dirty-path:apps/domain/src/module.ts"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     "pre_execution_recovery",
		FailureCategory: "gitops_dirty_worktree_scope",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID == "run-a" || claimed.Run.TaskID != task.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected replacement implementation run to be claimed, got %+v", claimed.Run)
	}
	requeuedTask := fake.tasks[task.ID]
	if !containsString(requeuedTask.FilesToEdit, "apps/domain/src") {
		t.Fatalf("expected dirty path under likely scope to expand files_to_edit, got %+v", requeuedTask.FilesToEdit)
	}
	if !strings.Contains(requeuedTask.ResumeInstructions, "apps/domain/src/module.ts") {
		t.Fatalf("expected resume instructions to name dirty path, got %q", requeuedTask.ResumeInstructions)
	}
}

func TestClaimNextRunExpandsPreExecutionDirtyScopeBeforeRetry(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	task.EvidenceRefs = []string{"automation_run:run-a", "gitops-dirty-path:apps/domain/src/module.ts"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		SafeSummary:     "dependency_ready_automation_queued",
		FailureCategory: "gitops_dirty_worktree_scope",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-a" || claimed.Run.Status != RunStatusRunning || claimed.Run.SafeSummary != "pre_execution_recovery" {
		t.Fatalf("expected same run to be retried through pre-execution recovery, got %+v", claimed.Run)
	}
	retryingTask := fake.tasks[task.ID]
	if !containsString(retryingTask.FilesToEdit, "apps/domain/src") {
		t.Fatalf("expected dirty path under likely scope to expand files_to_edit before retry, got %+v", retryingTask.FilesToEdit)
	}
}

func TestClaimNextRunBlocksPreExecutionDirtyScopeOutsideLikelyBeforeRetry(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	task.EvidenceRefs = []string{"automation_run:run-a", "gitops-dirty-path:apps/other/src/module.ts"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		SafeSummary:     "dependency_ready_automation_queued",
		FailureCategory: "gitops_dirty_worktree_scope",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected dirty path outside likely scope to block without claiming a run, got %v", err)
	}
	blockedTask := fake.tasks[task.ID]
	if blockedTask.Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected task blocked, got %+v", blockedTask)
	}
	if !strings.Contains(blockedTask.BlockedReason, "apps/other/src/module.ts") {
		t.Fatalf("expected blocked reason to name dirty path, got %q", blockedTask.BlockedReason)
	}
}

func TestQueueReadyDependentAutomationBlocksAfterReplacementRetryLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i, category := range []string{
		"pre_execution_recovery_failed_requires_implementation",
		"gitops_recovery_failed_requires_implementation",
		"pre_execution_recovery_failed_requires_implementation",
	} {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: category,
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	updatedTask := fake.tasks[task.ID]
	if updatedTask.Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected task to block after replacement limit, got %#v", updatedTask)
	}
	if !strings.Contains(updatedTask.BlockedReason, "replacement retry limit") {
		t.Fatalf("expected retry-limit blocked reason, got %q", updatedTask.BlockedReason)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != defaultAutomationMaxReplacementRunsPerTask {
		t.Fatalf("expected no replacement run after limit, got %d runs", len(runs))
	}
}

func TestQueueReadyDependentAutomationIgnoresOldFailuresAfterSystemFixRecoveryMarker(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.UpdatedAt = time.Date(2026, 6, 6, 13, 11, 0, 0, time.UTC)
	task.AgentRunIDs = []string{"orchestrator-system-fix-b1b249b"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "pre_execution_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
			UpdatedAt:       task.UpdatedAt.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != defaultAutomationMaxReplacementRunsPerTask+1 {
		t.Fatalf("expected fresh replacement run after system fix marker, got %d runs", len(runs))
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected task to remain ready after system fix marker, got %#v", fake.tasks[task.ID])
	}
}

func TestQueueReadyDependentAutomationIgnoresOldFailuresAfterOrchestratorRequeueMarker(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.UpdatedAt = time.Date(2026, 6, 6, 17, 16, 0, 0, time.UTC)
	task.AgentRunIDs = []string{"orchestrator-requeue-sos-recovery-automation-20260607"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "gitops_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
			UpdatedAt:       task.UpdatedAt.Add(-time.Minute),
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != defaultAutomationMaxReplacementRunsPerTask+1 {
		t.Fatalf("expected fresh replacement run after orchestrator requeue marker, got %d runs", len(runs))
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected task to remain ready after orchestrator requeue marker, got %#v", fake.tasks[task.ID])
	}
}

func TestQueueReadyDependentAutomationDoesNotBlockAfterExternalRunnerInterruptions(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("interrupted-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusTimeout,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "external_runner_interrupted",
			SafeSummary:     "external_codex_cli_abandoned_after_restart",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	updatedTask := fake.tasks[task.ID]
	if updatedTask.Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected task to remain ready after external interruptions, got %#v", updatedTask)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != defaultAutomationMaxReplacementRunsPerTask+1 {
		t.Fatalf("expected replacement run after external interruptions, got %d runs", len(runs))
	}
}

func TestQueueReadyDependentAutomationAllowsReplacementBelowRetryLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask-1; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "pre_execution_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != defaultAutomationMaxReplacementRunsPerTask {
		t.Fatalf("expected replacement run below limit, got %d runs", len(runs))
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected task to remain ready below limit, got %#v", fake.tasks[task.ID])
	}
}

func TestQueueReadyDependentAutomationBlocksAfterReplacementRetryLimitWithStaleQueuedRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "pre_execution_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:                "stale-queued-run",
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           automation.AgentID,
		PlanID:            task.PlanID,
		TaskID:            task.ID,
		Status:            RunStatusQueued,
		RunnerKind:        RunnerKindCodexCLI,
		OrchestratorRunID: dependencyReadyRunID(task, automation),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	updatedTask := fake.tasks[task.ID]
	if updatedTask.Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected task to block after replacement limit, got %#v", updatedTask)
	}
	if !strings.Contains(updatedTask.BlockedReason, "replacement retry limit") {
		t.Fatalf("expected retry-limit blocked reason, got %q", updatedTask.BlockedReason)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != defaultAutomationMaxReplacementRunsPerTask+1 {
		t.Fatalf("expected no additional replacement run after limit, got %d runs", len(runs))
	}
}

func TestClaimNextRunBlocksStaleQueuedReplacementAfterRetryLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "gitops_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:                "stale-queued-run",
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           automation.AgentID,
		PlanID:            task.PlanID,
		TaskID:            task.ID,
		Status:            RunStatusQueued,
		RunnerKind:        RunnerKindCodexCLI,
		OrchestratorRunID: dependencyReadyRunID(task, automation),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued automation run after stale replacement is blocked, got %v", err)
	}
	blockedRun, err := store.GetRun(ctx, automation.ProjectID, "stale-queued-run")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if blockedRun.Status != RunStatusBlocked || blockedRun.FailureCategory != automationReplacementRetryLimitCategory {
		t.Fatalf("expected stale queued run blocked by retry limit, got %#v", blockedRun)
	}
	if blockedRun.AttemptCount != 0 {
		t.Fatalf("stale queued run must not be started, got attempt_count=%d", blockedRun.AttemptCount)
	}
	updatedTask := fake.tasks[task.ID]
	if updatedTask.Status != projectworkplan.WorkTaskStatusBlocked || updatedTask.ClaimedByRunID != "" {
		t.Fatalf("expected task blocked without claim/start, got %#v", updatedTask)
	}
	if !strings.Contains(updatedTask.BlockedReason, "replacement retry limit") {
		t.Fatalf("expected retry-limit blocked reason, got %q", updatedTask.BlockedReason)
	}
}

func TestClaimNextRunBlocksStaleQueuedReplacementWhenTaskAlreadyBlockedAfterRetryLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusBlocked
	task.BlockedReason = "Automation replacement retry limit reached after repeated GitOps, pre-execution, or external-runner recovery failures."
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "gitops_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:                "stale-queued-run",
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           automation.AgentID,
		PlanID:            task.PlanID,
		TaskID:            task.ID,
		Status:            RunStatusQueued,
		RunnerKind:        RunnerKindCodexCLI,
		OrchestratorRunID: dependencyReadyRunID(task, automation),
		SafeSummary:       "dependency_ready_automation_queued",
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued automation run after stale replacement is blocked, got %v", err)
	}
	blockedRun, err := store.GetRun(ctx, automation.ProjectID, "stale-queued-run")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if blockedRun.Status != RunStatusBlocked || blockedRun.FailureCategory != automationReplacementRetryLimitCategory {
		t.Fatalf("expected stale queued run blocked by retry limit, got %#v", blockedRun)
	}
	if blockedRun.Status == RunStatusPolicyDenied || strings.Contains(blockedRun.FailureCategory, "task_not_ready") {
		t.Fatalf("stale queued retry-limit run must not become task_not_ready policy denial: %#v", blockedRun)
	}
}

func TestClaimNextRunAllowsExplicitQueuedRunAfterReplacementRetryLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "pre_execution_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:                "operator-rerun",
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           automation.AgentID,
		PlanID:            task.PlanID,
		TaskID:            task.ID,
		Status:            RunStatusQueued,
		RunnerKind:        RunnerKindCodexCLI,
		OrchestratorRunID: "operator-rerun-after-fix",
		SafeSummary:       "operator_rerun_after_fix",
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "operator-rerun" || claimed.Run.Status != RunStatusRunning || claimed.Run.AttemptCount != 1 {
		t.Fatalf("expected operator rerun to start, got %#v", claimed.Run)
	}
	updatedTask := fake.tasks[task.ID]
	if updatedTask.Status != projectworkplan.WorkTaskStatusInProgress || updatedTask.ClaimedByRunID != "operator-rerun" {
		t.Fatalf("expected operator rerun to claim/start task, got %#v", updatedTask)
	}
}

func TestClaimNextRunDoesNotBlockExplicitQueuedRunDuringReadyReconcileAfterReplacementRetryLimit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{task.PlanID: {
			ID:        task.PlanID,
			ProjectID: task.ProjectID,
			Status:    projectworkplan.WorkPlanStatusActive,
		}},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	for i := 0; i < defaultAutomationMaxReplacementRunsPerTask; i++ {
		if _, err := store.CreateRun(ctx, AutomationRun{
			ID:              fmt.Sprintf("failed-run-%d", i),
			ProjectID:       automation.ProjectID,
			AutomationID:    automation.ID,
			AgentID:         automation.AgentID,
			PlanID:          task.PlanID,
			TaskID:          task.ID,
			Status:          RunStatusFailed,
			RunnerKind:      RunnerKindCodexCLI,
			FailureCategory: "gitops_recovery_failed_requires_implementation",
			SafeSummary:     "replacement_terminal_failure",
		}); err != nil {
			t.Fatalf("CreateRun returned error: %v", err)
		}
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:                "operator-rerun",
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           automation.AgentID,
		PlanID:            task.PlanID,
		TaskID:            task.ID,
		Status:            RunStatusQueued,
		RunnerKind:        RunnerKindCodexCLI,
		OrchestratorRunID: "operator-rerun-after-fix",
		SafeSummary:       "external_runner_queued",
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "operator-rerun" || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected explicit rerun to be claimed, got %#v", claimed.Run)
	}
	if updatedTask := fake.tasks[task.ID]; updatedTask.Status != projectworkplan.WorkTaskStatusInProgress || updatedTask.ClaimedByRunID != "operator-rerun" {
		t.Fatalf("expected task claimed by explicit rerun, got %#v", updatedTask)
	}
}

func TestClaimNextRunReconcilesExhaustedPreExecutionRecoveryForAdvancedTaskStates(t *testing.T) {
	for _, status := range []string{projectworkplan.WorkTaskStatusNeedsReview, projectworkplan.WorkTaskStatusVerifying, projectworkplan.WorkTaskStatusDone} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
			task.Status = status
			task.ClaimedByRunID = "run-a"
			fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
			store := newTestStore()
			svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
			svc.codexAvailable = func() bool { return false }
			automation := createAutomaticTriggerAutomation(t, ctx, svc)
			now := time.Now().UTC()
			if _, err := store.CreateRun(ctx, AutomationRun{
				ID:              "run-a",
				ProjectID:       automation.ProjectID,
				AutomationID:    automation.ID,
				AgentID:         automation.AgentID,
				PlanID:          task.PlanID,
				TaskID:          task.ID,
				WorkTaskStatus:  projectworkplan.WorkTaskStatusInProgress,
				Status:          RunStatusFailed,
				RunnerKind:      RunnerKindCodexCLI,
				AttemptCount:    defaultAutomationMaxRetries,
				SafeSummary:     "pre_execution_recovery",
				FailureCategory: "gitops_dirty_worktree",
				CreatedAt:       now,
				UpdatedAt:       now,
			}); err != nil {
				t.Fatalf("CreateRun returned error: %v", err)
			}

			if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
				t.Fatalf("expected no replacement run after advanced task reconciliation, got %v", err)
			}
			reconciled, err := store.GetRun(ctx, automation.ProjectID, "run-a")
			if err != nil {
				t.Fatalf("GetRun returned error: %v", err)
			}
			if status == projectworkplan.WorkTaskStatusDone {
				if reconciled.Status != RunStatusCompleted || reconciled.SafeSummary != RunSafeSummaryVerifiedTaskDone {
					t.Fatalf("expected done task to complete run, got %+v", reconciled)
				}
				return
			}
			if reconciled.Status != RunStatusVerifying || reconciled.WorkTaskStatus != status || reconciled.SafeSummary != "pre_execution_recovery_progressed_task_verifying" {
				t.Fatalf("expected advanced task to reconcile to verifying, got %+v", reconciled)
			}
		})
	}
}

func TestGitOpsRecoveryRequeueBoundsStoredLongResumeInstructions(t *testing.T) {
	ctx := context.Background()
	workStore := workstore.NewMemoryStore()
	workSvc := projectworkplan.New(workStore)
	plan, err := workSvc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan-gitops-long-resume",
		Title:       "GitOps long resume plan",
		GoalSummary: "Exercise recovery with legacy task metadata.",
	})
	if err != nil {
		t.Fatalf("CreateWorkPlan returned error: %v", err)
	}
	task, err := workSvc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "task-gitops-long-resume",
		Title:                   "GitOps long resume task",
		EvidenceNeeded:          []string{"source evidence"},
		ContextPackRefs:         []string{"context-pack-1"},
		FilesToEdit:             []string{"internal/projectautomation/service.go"},
		LikelyFilesAffected:     []string{"internal/projectautomation/service.go"},
		VerificationRequirement: "focused tests",
		ExpectedOutput:          "recovery succeeds",
		FailureCriteria:         "recovery must not persist over-limit resume instructions",
		ResumeInstructions:      "resume from task metadata",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("CreateWorkTask returned error: %v", err)
	}
	if _, err := workSvc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"}); err != nil {
		t.Fatalf("ClaimWorkTask returned error: %v", err)
	}
	if _, err := workSvc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"}); err != nil {
		t.Fatalf("StartWorkTask returned error: %v", err)
	}
	verifying, err := workSvc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"},
		Status:              projectworkplan.WorkTaskStatusVerifying,
	})
	if err != nil {
		t.Fatalf("UpdateWorkTaskStatus returned error: %v", err)
	}
	verifying.ResumeInstructions = strings.Repeat("b", projectworkplan.MaxResumeInstructionsLength+25)
	if _, err := workStore.UpdateWorkTask(ctx, verifying); err != nil {
		t.Fatalf("UpdateWorkTask returned error: %v", err)
	}

	automationStore := newTestStore()
	svc := New(automationStore, workSvc, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(300, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/gitops-long-resume",
		Title:           "GitOps recovery automation",
		Purpose:         "Recover GitOps failure",
		Status:          AutomationStatusEnabled,
		AgentID:         "agent-1",
		PlanID:          plan.ID,
		AllowedTaskRefs: []string{task.ID},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	run := AutomationRun{
		ID:              "run-gitops",
		ProjectID:       "project-1",
		AutomationID:    automation.ID,
		AgentID:         "agent-1",
		PlanID:          plan.ID,
		TaskID:          task.ID,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       time.Unix(100, 0).UTC(),
		UpdatedAt:       time.Unix(100, 0).UTC(),
	}
	if _, err := automationStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.requeueTaskAfterGitOpsRecoveryFailure(ctx, run, "gitops_post_task_failed", nil); err != nil {
		t.Fatalf("requeueTaskAfterGitOpsRecoveryFailure returned error: %v", err)
	}
	updatedTask, err := workSvc.GetWorkTask(ctx, "project-1", task.ID)
	if err != nil {
		t.Fatalf("GetWorkTask returned error: %v", err)
	}
	if updatedTask.Status != projectworkplan.WorkTaskStatusReady || updatedTask.ClaimedByRunID != "" {
		t.Fatalf("expected task to be ready and unclaimed, got %#v", updatedTask)
	}
	if len(updatedTask.ResumeInstructions) > projectworkplan.MaxResumeInstructionsLength {
		t.Fatalf("expected bounded resume instructions, got length %d", len(updatedTask.ResumeInstructions))
	}
	if !strings.Contains(updatedTask.ResumeInstructions, "GitOps recovery failed with gitops_post_task_failed") {
		t.Fatalf("expected recovery instructions, got %q", updatedTask.ResumeInstructions)
	}
	updatedRun, err := automationStore.GetRun(ctx, "project-1", "run-gitops")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updatedRun.FailureCategory != "gitops_recovery_failed_requires_implementation" {
		t.Fatalf("expected stable requeue failure category, got %q", updatedRun.FailureCategory)
	}
	if !strings.Contains(updatedRun.SafeSummary, "gitops_post_task_failed") {
		t.Fatalf("expected safe summary to preserve original failure category, got %q", updatedRun.SafeSummary)
	}
}

func TestGitOpsRecoveryRequeueUsesImplementationClaimForAttachedRecoveryRun(t *testing.T) {
	ctx := context.Background()
	workStore := workstore.NewMemoryStore()
	workSvc := projectworkplan.New(workStore)
	plan, err := workSvc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "plan-attached-gitops-recovery",
		Title:       "Attached GitOps recovery plan",
		GoalSummary: "Exercise recovery when the implementation run still owns the task claim.",
	})
	if err != nil {
		t.Fatalf("CreateWorkPlan returned error: %v", err)
	}
	task, err := workSvc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "task-attached-gitops-recovery",
		Title:                   "Attached GitOps recovery task",
		EvidenceNeeded:          []string{"source evidence"},
		ContextPackRefs:         []string{"context-pack-1"},
		FilesToEdit:             []string{"internal/projectautomation/service.go"},
		LikelyFilesAffected:     []string{"internal/projectautomation/service.go"},
		VerificationRequirement: "focused tests",
		ExpectedOutput:          "recovery requeues implementation",
		FailureCriteria:         "recovery must not require itself to own the task claim",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("CreateWorkTask returned error: %v", err)
	}
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = "implementation-run"
	task.AgentRunIDs = []string{"implementation-run", "gitops-recovery-run"}
	task.ReviewResultRefs = []string{"review-approved"}
	task.VerifierResultRefs = []string{"verifier-focused"}
	if _, err := workStore.UpdateWorkTask(ctx, task); err != nil {
		t.Fatalf("UpdateWorkTask returned error: %v", err)
	}

	automationStore := newTestStore()
	svc := New(automationStore, workSvc, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(300, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/attached-gitops-recovery",
		Title:           "Attached GitOps recovery automation",
		Purpose:         "Recover attached GitOps failure",
		Status:          AutomationStatusEnabled,
		AgentID:         "agent-1",
		PlanID:          plan.ID,
		AllowedTaskRefs: []string{task.ID},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	run := AutomationRun{
		ID:              "gitops-recovery-run",
		ProjectID:       "project-1",
		AutomationID:    automation.ID,
		AgentID:         "agent-1",
		PlanID:          plan.ID,
		TaskID:          task.ID,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       time.Unix(100, 0).UTC(),
		UpdatedAt:       time.Unix(100, 0).UTC(),
	}
	if _, err := automationStore.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.requeueTaskAfterGitOpsRecoveryFailure(ctx, run, "gitops_post_task_failed", nil); err != nil {
		t.Fatalf("requeueTaskAfterGitOpsRecoveryFailure returned error: %v", err)
	}
	updatedTask, err := workSvc.GetWorkTask(ctx, "project-1", task.ID)
	if err != nil {
		t.Fatalf("GetWorkTask returned error: %v", err)
	}
	if updatedTask.Status != projectworkplan.WorkTaskStatusReady || updatedTask.ClaimedByRunID != "" {
		t.Fatalf("expected task to be ready and unclaimed, got %#v", updatedTask)
	}
}

func TestGitOpsVerificationFailureIsRecoverable(t *testing.T) {
	if !isRecoverableGitOpsPostTaskFailure("gitops_verification_failed") {
		t.Fatal("expected gitops verification failures to be recoverable after config or verifier fixes")
	}
}

func TestClaimNextRunRecoversVerifyingWriteTaskMissingGitOpsRefs(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = "run-a"
	task.FilesToEdit = []string{"internal/foo.go"}
	task.ReviewResultRefs = []string{"review-approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: task.ID, WorkTaskStatus: task.Status, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI,
		SafeSummary: "external_codex_cli_completed_verification_required",
		CreatedAt:   time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-a" || claimed.Run.Status != RunStatusRunning || claimed.Run.SafeSummary != RunSafeSummaryGitOpsPostTaskRecovery || claimed.Run.FailureCategory != "gitops_post_task_failed" {
		t.Fatalf("expected GitOps recovery run, got %#v", claimed.Run)
	}
}

func TestClaimNextRunRecoversAttachedGitOpsRecoveryRunWhenTaskClaimedByImplementationRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "fix-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = "implementation-run"
	task.FilesToEdit = []string{"internal/foo.go"}
	task.ReviewResultRefs = []string{"review-approved"}
	task.VerifierResultRefs = []string{"verifier-focused"}
	task.AgentRunIDs = []string{"implementation-run", "gitops-recovery-run"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "gitops-recovery-run", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: task.ID, WorkTaskStatus: task.Status, Status: RunStatusFailed, RunnerKind: RunnerKindCodexCLI,
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "gitops-recovery-run" || claimed.Run.Status != RunStatusRunning || claimed.Run.SafeSummary != RunSafeSummaryGitOpsPostTaskRecovery {
		t.Fatalf("expected attached GitOps recovery run to be reclaimed, got %#v", claimed.Run)
	}
}

func TestClaimNextRunRecoversBlockedStartFailureForClaimedTask(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "scan-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusClaimed
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: "task-a", Status: RunStatusBlocked, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "start_failed",
		CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-a" || claimed.Run.Status != RunStatusRunning || claimed.Run.AttemptCount != 1 || claimed.Run.FailureCategory != "" {
		t.Fatalf("expected blocked start failure run to be reclaimed, got %#v", claimed.Run)
	}
	if got := fake.tasks["task-a"].Status; got != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected claimed task to be started during recovery, got %q", got)
	}
}

func TestClaimNextRunReconcilesRecoveredBlockedStartBeforeQueuedWork(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	recoveredTask := readyTask("task-recovered", "scan-recovered", []string{"internal/foo.go"})
	recoveredTask.Status = projectworkplan.WorkTaskStatusVerifying
	recoveredTask.OwnerAgent = "code-review-scanner"
	recoveredTask.ClaimedByRunID = "run-recovered"
	recoveredTask.EvidenceRefs = []string{"evidence-recovered"}
	recoveredTask.ClaimRefs = []string{"claim-recovered"}
	recoveredTask.VerifierResultRefs = []string{"verifier-recovered"}
	queuedTask := readyTask("task-queued", "scan-queued", []string{"internal/bar.go"})
	queuedTask.OwnerAgent = "code-review-scanner"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		recoveredTask.ID: recoveredTask,
		queuedTask.ID:    queuedTask,
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	recoveredAutomation := createAutomaticTriggerAutomation(t, ctx, svc)
	queuedAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/queued-scan",
		Title:           "Queued scan",
		Purpose:         "Run queued scan.",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{queuedTask.ID, queuedTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-recovered", ProjectID: "project-1", AutomationID: recoveredAutomation.ID, AgentID: recoveredAutomation.AgentID,
		PlanID: "plan-1", TaskID: recoveredTask.ID, Status: RunStatusBlocked, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "start_failed",
		CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-queued", ProjectID: "project-1", AutomationID: queuedAutomation.ID, AgentID: queuedAutomation.AgentID,
		PlanID: "plan-1", TaskID: queuedTask.ID, Status: RunStatusQueued, RunnerKind: RunnerKindCodexCLI,
		CreatedAt: time.Unix(150, 0).UTC(), UpdatedAt: time.Unix(150, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-queued" {
		t.Fatalf("expected queued run to be claimed after stale recovery closeout, got %#v", claimed.Run)
	}
	recoveredRun, err := store.GetRun(ctx, "project-1", "run-recovered")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if recoveredRun.Status != RunStatusCompleted || recoveredRun.FailureCategory != "" || fake.tasks[recoveredTask.ID].Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected stale recovered run to close before queued claim, run=%#v task=%#v", recoveredRun, fake.tasks[recoveredTask.ID])
	}
}

func TestClaimNextRunDoesNotRecoverOrdinaryBlockedRun(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-a", "scan-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusBlocked
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: automation.ID, AgentID: automation.AgentID,
		PlanID: "plan-1", TaskID: "task-a", Status: RunStatusBlocked, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "work_task_blocked",
		CreatedAt:       time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected ordinary blocked run to remain terminal, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusBlocked || run.FailureCategory != "work_task_blocked" {
		t.Fatalf("ordinary blocked run should not be recovered: %#v", run)
	}
}

func TestClaimNextRunDoesNotFailScannerWithConfirmedFindingRefs(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-scan": {
			ID:                      "task-scan",
			ProjectID:               "project-1",
			PlanID:                  "plan-1",
			TaskRef:                 "scan-for-candidate-bugs-audit-delta",
			Title:                   "Scan for candidate bugs audit delta",
			Status:                  projectworkplan.WorkTaskStatusVerifying,
			VerificationRequirement: "orchestrator verifies",
			ClaimRefs:               []string{"claim.family-pricing-promo-user.confirmed.20260605112847"},
			VerifierResultRefs:      []string{"verifier-audit"},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		},
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-scan", ProjectID: "project-1", AutomationID: "automation-scan", AgentID: "agent-audit",
		PlanID: "plan-1", TaskID: "task-scan", Status: RunStatusRunning, RunnerKind: RunnerKindCodexCLI,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after recovery, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-scan")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusCompleted || run.FailureCategory != "" {
		t.Fatalf("expected scanner run to complete after read-only verification, got %#v", run)
	}
	task, err := fake.GetWorkTask(ctx, "project-1", "task-scan")
	if err != nil {
		t.Fatalf("GetWorkTask returned error: %v", err)
	}
	if task.Status != projectworkplan.WorkTaskStatusDone || task.ReviewExemptReason == "" {
		t.Fatalf("expected scanner task done with read-only exemption, got %#v", task)
	}
}

func TestClaimNextRunAutoExemptCloseoutDropsStaleReviewRefs(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-review", "review-candidate-bugs-audit", nil)
	task.OwnerAgent = "bug-finding-reviewer"
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.VerifierResultRefs = []string{"verifier-review"}
	task.ReviewResultRefs = []string{"review-self"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-review",
		ProjectID:      "project-1",
		AutomationID:   "automation-review",
		AgentID:        "bug-finding-reviewer",
		PlanID:         task.PlanID,
		TaskID:         task.ID,
		Status:         RunStatusVerifying,
		RunnerKind:     RunnerKindCodexCLI,
		CreatedAt:      time.Unix(100, 0).UTC(),
		UpdatedAt:      time.Unix(100, 0).UTC(),
		WorkTaskStatus: task.Status,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after closeout, got %v", err)
	}
	if len(fake.completeActions) != 1 {
		t.Fatalf("expected one completion action, got %d", len(fake.completeActions))
	}
	action := fake.completeActions[0]
	if len(action.ReviewResultRefs) != 0 || action.ReviewExemptReason == "" {
		t.Fatalf("expected stale review refs to be dropped with exemption, got %#v", action)
	}
	run, err := store.GetRun(ctx, "project-1", "run-review")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("expected run completed, got %#v", run)
	}
}

func TestClaimNextRunAutoExemptsMetadataOnlyBugPlannerCloseout(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-plan", "create-confirmed-bug-work-plans", nil)
	task.OwnerAgent = "bug-plan-orchestrator"
	task.Title = "Create confirmed bug remediation plans"
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimRefs = []string{"candidate.example.confirmed"}
	task.EvidenceRefs = []string{"bug-work-plan.work_plan_example"}
	task.VerifierResultRefs = []string{"verifier-planner"}
	task.ReviewResultRefs = []string{"review-result-not-attached"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-plan",
		ProjectID:      "project-1",
		AutomationID:   "automation-plan",
		AgentID:        "bug-plan-orchestrator",
		PlanID:         task.PlanID,
		TaskID:         task.ID,
		Status:         RunStatusVerifying,
		RunnerKind:     RunnerKindCodexCLI,
		CreatedAt:      time.Unix(100, 0).UTC(),
		UpdatedAt:      time.Unix(100, 0).UTC(),
		WorkTaskStatus: task.Status,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after closeout, got %v", err)
	}
	if len(fake.completeActions) != 1 {
		t.Fatalf("expected one completion action, got %d", len(fake.completeActions))
	}
	action := fake.completeActions[0]
	if len(action.ReviewResultRefs) != 0 || action.ReviewExemptReason == "" {
		t.Fatalf("expected metadata-only planner to drop stale review refs with exemption, got %#v", action)
	}
	run, err := store.GetRun(ctx, "project-1", "run-plan")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("expected run completed, got %#v", run)
	}
}

func TestClaimNextRunFailsRemediationPlannerWithConfirmedFindingWithoutRemediation(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-audit": {
			ID:                      "task-audit",
			ProjectID:               "project-1",
			PlanID:                  "plan-1",
			TaskRef:                 "create-confirmed-bug-work-plans-audit-delta",
			Title:                   "Create confirmed bug Work Plans audit delta",
			Status:                  projectworkplan.WorkTaskStatusVerifying,
			VerificationRequirement: "orchestrator verifies",
			ClaimRefs:               []string{"claim.family-pricing-promo-user.confirmed.20260605112847"},
			VerifierResultRefs:      []string{"verifier-audit"},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		},
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-audit", ProjectID: "project-1", AutomationID: "automation-audit", AgentID: "agent-audit",
		PlanID: "plan-1", TaskID: "task-audit", Status: RunStatusRunning, RunnerKind: RunnerKindCodexCLI,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after recovery, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-audit")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "confirmed_finding_remediation_missing" {
		t.Fatalf("expected explicit remediation failure, got %#v", run)
	}
	task, err := fake.GetWorkTask(ctx, "project-1", "task-audit")
	if err != nil {
		t.Fatalf("GetWorkTask returned error: %v", err)
	}
	if task.Status != projectworkplan.WorkTaskStatusFailed || task.Outcome == "" {
		t.Fatalf("expected failed task with outcome, got %#v", task)
	}
}

func TestClaimNextRunDoesNotFailActiveLeasedAuditBeforeRunnerCompletion(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-audit": {
			ID:                      "task-audit",
			ProjectID:               "project-1",
			PlanID:                  "plan-1",
			TaskRef:                 "create-confirmed-bug-work-plans-audit-delta",
			Title:                   "Create confirmed bug Work Plans audit delta",
			Status:                  projectworkplan.WorkTaskStatusVerifying,
			ClaimedByRunID:          "run-audit",
			VerificationRequirement: "orchestrator verifies",
			ClaimRefs:               []string{"claim.family-pricing-promo-user.confirmed.20260605112847"},
			VerifierResultRefs:      []string{"verifier-audit"},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		},
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-audit", ProjectID: "project-1", AutomationID: "automation-audit", AgentID: "agent-audit",
		PlanID: "plan-1", TaskID: "task-audit", Status: RunStatusRunning, RunnerKind: RunnerKindCodexCLI,
		ClaimID: "claim-audit", RunnerID: "runner-1", ClaimedAt: time.Unix(100, 0).UTC(), LastHeartbeatAt: time.Unix(190, 0).UTC(), LeaseExpiresAt: time.Unix(290, 0).UTC(),
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(190, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run while active audit runner owns completion, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-audit")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusRunning || run.FailureCategory != "" {
		t.Fatalf("expected active leased audit to remain running, got %#v", run)
	}
	task, err := fake.GetWorkTask(ctx, "project-1", "task-audit")
	if err != nil {
		t.Fatalf("GetWorkTask returned error: %v", err)
	}
	if task.Status != projectworkplan.WorkTaskStatusVerifying {
		t.Fatalf("expected audit task not failed before runner completion, got %#v", task)
	}
}

func TestClaimNextRunClosesRemediationPlannerWithBugPlanHandoff(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-audit": {
			ID:                      "task-audit",
			ProjectID:               "project-1",
			PlanID:                  "plan-1",
			TaskRef:                 "create-confirmed-bug-work-plans-audit-delta",
			Title:                   "Create confirmed bug Work Plans audit delta",
			Status:                  projectworkplan.WorkTaskStatusVerifying,
			VerificationRequirement: "orchestrator verifies",
			ClaimRefs:               []string{"claim.family-pricing-promo-user.confirmed.20260605112847", "bug-work-plan.work_plan_family_pricing"},
			Outcome:                 "Created remediation Work Plan and automation for the confirmed finding.",
			VerifierResultRefs:      []string{"verifier-audit"},
			ReviewResultRefs:        []string{"review-audit"},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		},
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-audit", ProjectID: "project-1", AutomationID: "automation-audit", AgentID: "agent-audit",
		PlanID: "plan-1", TaskID: "task-audit", Status: RunStatusRunning, RunnerKind: RunnerKindCodexCLI,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after closeout, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-audit")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusCompleted || run.FailureCategory != "" {
		t.Fatalf("expected completed remediation planner run, got %#v", run)
	}
	task, err := fake.GetWorkTask(ctx, "project-1", "task-audit")
	if err != nil {
		t.Fatalf("GetWorkTask returned error: %v", err)
	}
	if task.Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected completed task, got %#v", task)
	}
}

func TestClaimNextRunMarksVerifyingRunBlockedWhenTaskBlocked(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-blocked": {
			ID:                      "task-blocked",
			ProjectID:               "project-1",
			PlanID:                  "plan-1",
			TaskRef:                 "audit-review-scan",
			Title:                   "Audit review scan",
			Status:                  projectworkplan.WorkTaskStatusBlocked,
			BlockedReason:           "remediation creation rejected safe metadata",
			VerificationRequirement: "orchestrator verifies",
			ClaimRefs:               []string{"claim.family-pricing-promo-user.confirmed.20260605112847"},
			VerifierResultRefs:      []string{"verifier-audit"},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		},
	}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-blocked", ProjectID: "project-1", AutomationID: "automation-blocked", AgentID: "agent-audit",
		PlanID: "plan-1", TaskID: "task-blocked", Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI,
		CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after recovery, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-blocked")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusBlocked || run.WorkTaskStatus != projectworkplan.WorkTaskStatusBlocked || run.FailureCategory != "work_task_blocked" {
		t.Fatalf("expected blocked automation run, got %#v", run)
	}
}

func TestCreateRemediationFromFindingRequiresConfirmedFinding(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestStore(), &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{}}, Options{Enabled: true, RunnerEnabled: true})

	_, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding-1",
		FindingStatus:           "suspected",
		Title:                   "Fix finding",
		Summary:                 "Fix confirmed issue.",
		VerificationRequirement: "Run focused tests.",
	})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "confirmed") {
		t.Fatalf("expected confirmed finding error, got %v", err)
	}
}

func TestCreateRemediationFromFindingRedactsTimestampRefsFromHumanText(t *testing.T) {
	ctx := context.Background()
	workTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{}}
	svc := New(newTestStore(), workTasks, Options{Enabled: true, RunnerEnabled: true})

	result, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "claim.family-pricing-promo-user.confirmed.20260605112847",
		FindingStatus:           "confirmed",
		Title:                   "Fix family pricing promo code user context",
		Summary:                 "Repair confirmed family pricing promo code user context handling.",
		Severity:                "medium",
		ImplementationAgentID:   "worker-a",
		GitBaseRef:              "main",
		GitBranchRef:            "mivia/MASS-0000-fix-family-pricing-promo-user",
		GitWorktreeRef:          "wt-MASS-0000-fix-family-pricing-promo-user",
		FilesToRead:             []string{"apps"},
		FilesToEdit:             []string{"apps"},
		EvidenceRefs:            []string{"review-confirmed"},
		VerificationRequirement: "Run focused regression tests.",
	})
	if err != nil {
		t.Fatalf("CreateRemediationFromFinding returned error: %v", err)
	}
	for field, value := range map[string]string{
		"work_plan_title":      result.WorkPlan.Title,
		"work_plan_goal":       result.WorkPlan.GoalSummary,
		"review_task_title":    result.ReviewTask.Title,
		"automation_title":     result.Automation.Title,
		"review_automation":    result.ReviewAutomation.Title,
		"task_expected_output": result.WorkTask.ExpectedOutput,
	} {
		if strings.Contains(value, "20260605112847") || phonePattern.MatchString(value) {
			t.Fatalf("%s leaked timestamp-like ref into human text: %q", field, value)
		}
	}
}

func TestCreateRemediationFromFindingResumesExistingPartialPlan(t *testing.T) {
	ctx := context.Background()
	findingRef := "claim.family-pricing-promo-user.confirmed.20260605112847"
	workTasks := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"work_plan_existing": {
				ID:          "work_plan_existing",
				ProjectID:   "project-1",
				PlanRef:     "remediate-" + findingRef,
				Title:       "Remediate confirmed finding claim.family-pricing-promo-user.confirmed.ref",
				GoalSummary: "Fix confirmed finding claim.family-pricing-promo-user.confirmed.ref.",
				Status:      projectworkplan.WorkPlanStatusPlanned,
			},
		},
		tasks: map[string]projectworkplan.WorkTask{},
	}
	svc := New(newTestStore(), workTasks, Options{Enabled: true, RunnerEnabled: true, WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{Enabled: true, Statuses: []string{projectworkplan.WorkPlanStatusActive}}})

	result, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              findingRef,
		FindingStatus:           "confirmed",
		Title:                   "Fix family pricing promo code user context",
		Summary:                 "Repair confirmed family pricing promo code user context handling.",
		Severity:                "medium",
		ImplementationAgentID:   "worker-a",
		GitBaseRef:              "main",
		GitBranchRef:            "mivia/MASS-0000-fix-family-pricing-promo-user",
		GitWorktreeRef:          "wt-MASS-0000-fix-family-pricing-promo-user",
		FilesToRead:             []string{"apps"},
		FilesToEdit:             []string{"apps"},
		EvidenceRefs:            []string{"review-confirmed"},
		VerificationRequirement: "Run focused regression tests.",
		ActivatePlan:            true,
	})
	if err != nil {
		t.Fatalf("CreateRemediationFromFinding returned error: %v", err)
	}
	if result.WorkPlan.ID != "work_plan_existing" || result.WorkPlan.Status != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("expected existing plan to be activated, got %#v", result.WorkPlan)
	}
	if result.WorkTask.ID == "" || result.ReviewTask.ID == "" || result.Automation.ID == "" || result.ReviewAutomation.ID == "" {
		t.Fatalf("expected missing remediation objects to be created, got %#v", result)
	}
}

func TestCreateRemediationFromFindingCreatesPlanTaskAndAutomaticAutomation(t *testing.T) {
	ctx := context.Background()
	workTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{}}
	svc := New(newTestStore(), workTasks, Options{Enabled: true, RunnerEnabled: true})

	result, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding:review-1",
		FindingStatus:           "confirmed",
		Title:                   "Fix confirmed review finding",
		Summary:                 "Repair the confirmed review finding.",
		Severity:                "high",
		ImplementationAgentID:   "worker-a",
		GitBaseRef:              "main",
		GitBranchRef:            "fix-MASS-0000-readme-structure-entry",
		GitWorktreeRef:          "fix-MASS-0000-readme-structure-entry",
		FilesToRead:             []string{"internal/projectautomation/service.go"},
		FilesToEdit:             []string{"internal/projectautomation/service.go"},
		EvidenceRefs:            []string{"review:confirmed"},
		VerificationRequirement: "Run focused automation tests.",
		ActivatePlan:            true,
	})
	if err != nil {
		t.Fatalf("CreateRemediationFromFinding returned error: %v", err)
	}
	if !result.Activated || result.WorkPlan.Status != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("expected activated active plan, got activated=%v plan=%#v", result.Activated, result.WorkPlan)
	}
	if result.WorkTask.Status != projectworkplan.WorkTaskStatusReady || result.WorkTask.OwnerAgent != "worker-a" {
		t.Fatalf("unexpected remediation task: %#v", result.WorkTask)
	}
	if result.ReviewTask.ID == "" || result.ReviewTask.Status != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("expected planned review task, got %#v", result.ReviewTask)
	}
	if result.ReviewTask.TaskRef != "review-"+result.WorkTask.TaskRef {
		t.Fatalf("expected review task ref for implementation task, got %q for %q", result.ReviewTask.TaskRef, result.WorkTask.TaskRef)
	}
	if result.ReviewTask.OwnerAgent == "" || result.ReviewTask.OwnerAgent == result.WorkTask.OwnerAgent {
		t.Fatalf("expected independent reviewer agent, got review=%q implementation=%q", result.ReviewTask.OwnerAgent, result.WorkTask.OwnerAgent)
	}
	if result.Automation.TriggerKind != TriggerKindAutomatic || result.Automation.Status != AutomationStatusEnabled {
		t.Fatalf("unexpected remediation automation: %#v", result.Automation)
	}
	if result.ReviewAutomation.TriggerKind != TriggerKindAutomatic || result.ReviewAutomation.Status != AutomationStatusEnabled || result.ReviewAutomation.SchedulePolicy != "post_implementation_review" {
		t.Fatalf("unexpected review automation: %#v", result.ReviewAutomation)
	}
	if !contains(result.ReviewAutomation.AllowedTaskRefs, result.ReviewTask.ID) || !contains(result.ReviewAutomation.AllowedTaskRefs, result.ReviewTask.TaskRef) {
		t.Fatalf("review automation must target review task id/ref, got %#v", result.ReviewAutomation.AllowedTaskRefs)
	}
	if result.WorkPlan.GitBaseRef != "main" || result.WorkPlan.GitBranchRef != "fix-MASS-0000-readme-structure-entry-finding-review-1" || result.WorkPlan.GitWorktreeRef != "fix-MASS-0000-readme-structure-entry-finding-review-1" {
		t.Fatalf("expected project-specific git refs, got base=%q branch=%q worktree=%q", result.WorkPlan.GitBaseRef, result.WorkPlan.GitBranchRef, result.WorkPlan.GitWorktreeRef)
	}
	wantEvidence := []string{"confirmed-finding-finding-review-1", "review-confirmed"}
	if strings.Join(result.WorkTask.EvidenceNeeded, ",") != strings.Join(wantEvidence, ",") {
		t.Fatalf("expected worker-safe evidence refs %v, got %v", wantEvidence, result.WorkTask.EvidenceNeeded)
	}
	if !strings.Contains(result.WorkTask.VerificationRequirement, "regression test") {
		t.Fatalf("expected remediation task to require regression-test consideration, got %q", result.WorkTask.VerificationRequirement)
	}
	if !strings.Contains(result.WorkTask.ExpectedOutput, "regression test") {
		t.Fatalf("expected remediation output to mention regression tests, got %q", result.WorkTask.ExpectedOutput)
	}
	if !strings.Contains(result.WorkTask.FailureCriteria, "regression test") {
		t.Fatalf("expected remediation failure criteria to reject omitted feasible regression tests, got %q", result.WorkTask.FailureCriteria)
	}
}

func TestCreateRemediationFromFindingInheritsBaseRefFromCreatorRunPlan(t *testing.T) {
	ctx := context.Background()
	workTasks := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"audit-plan": {
				ID:         "audit-plan",
				ProjectID:  "project-1",
				PlanRef:    "audit-plan",
				Title:      "Audit plan",
				Status:     projectworkplan.WorkPlanStatusDone,
				GitBaseRef: "main",
			},
		},
		tasks: map[string]projectworkplan.WorkTask{},
	}
	store := newTestStore()
	svc := New(store, workTasks, Options{Enabled: true, RunnerEnabled: true})
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:           "audit-run",
		ProjectID:    "project-1",
		AutomationID: "audit-automation",
		AgentID:      "code-review-scanner",
		PlanID:       "audit-plan",
		Status:       RunStatusCompleted,
		RunnerKind:   RunnerKindCodexCLI,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	result, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding.audit-base-inheritance",
		FindingStatus:           "confirmed",
		Title:                   "Fix audit base inheritance",
		Summary:                 "Repair deterministic remediation base ref selection.",
		CreatedByRunID:          "audit-run",
		GitBranchRef:            "mivia/remediate-audit-base-inheritance",
		GitWorktreeRef:          "wt-remediate-audit-base-inheritance",
		FilesToEdit:             []string{"internal/projectautomation/service.go"},
		VerificationRequirement: "Run focused automation tests.",
	})
	if err != nil {
		t.Fatalf("CreateRemediationFromFinding returned error: %v", err)
	}
	if result.WorkPlan.GitBaseRef != "main" {
		t.Fatalf("expected remediation plan to inherit creator run base ref, got %q", result.WorkPlan.GitBaseRef)
	}
}

func TestCreateRemediationFromFindingScopesSharedAuditGitRefsPerFinding(t *testing.T) {
	ctx := context.Background()
	workTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{}}
	svc := New(newTestStore(), workTasks, Options{Enabled: true, RunnerEnabled: true})

	first, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding.active-journey-plaintext-location",
		FindingStatus:           "confirmed",
		Title:                   "Fix active journey plaintext locations",
		Summary:                 "Repair active journey location storage.",
		GitBaseRef:              "main",
		GitBranchRef:            "mivia.audit-0606031743-domain-realtime-ops",
		GitWorktreeRef:          "audit.0606031743-domain-realtime-ops",
		FilesToEdit:             []string{"apps/domain-realtime-ops/src/infrastructure/database/schema.ts"},
		VerificationRequirement: "Run focused active journey test.",
	})
	if err != nil {
		t.Fatalf("first remediation returned error: %v", err)
	}
	second, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding.sos-activity-stubs-succeed",
		FindingStatus:           "confirmed",
		Title:                   "Fix SOS activity stubs",
		Summary:                 "Repair SOS activity failure behavior.",
		GitBaseRef:              "main",
		GitBranchRef:            "mivia.audit-0606031743-domain-realtime-ops",
		GitWorktreeRef:          "audit.0606031743-domain-realtime-ops",
		FilesToEdit:             []string{"apps/domain-realtime-ops/src/workflows/sos-activities.service.ts"},
		VerificationRequirement: "Run focused SOS workflow test.",
	})
	if err != nil {
		t.Fatalf("second remediation returned error: %v", err)
	}
	if first.WorkPlan.GitWorktreeRef == second.WorkPlan.GitWorktreeRef || first.WorkPlan.GitBranchRef == second.WorkPlan.GitBranchRef {
		t.Fatalf("expected per-finding git isolation, first=%q/%q second=%q/%q", first.WorkPlan.GitBranchRef, first.WorkPlan.GitWorktreeRef, second.WorkPlan.GitBranchRef, second.WorkPlan.GitWorktreeRef)
	}
	if !strings.Contains(first.WorkPlan.GitWorktreeRef, "finding.active-journey-plaintext-location") || !strings.Contains(second.WorkPlan.GitWorktreeRef, "finding.sos-activity-stubs-succeed") {
		t.Fatalf("expected finding tokens in worktree refs, first=%q second=%q", first.WorkPlan.GitWorktreeRef, second.WorkPlan.GitWorktreeRef)
	}
}

func TestWorkPlanStatusTriggerSkipsWhenNoReadyTask(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestStore(), &fakeWorkTasks{}, Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: RunnerExecutionExternal,
		WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID: "project-1",
		PlanID:    "plan-1",
		OldStatus: projectworkplan.WorkPlanStatusPlanned,
		NewStatus: projectworkplan.WorkPlanStatusActive,
		ChangedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("HandleWorkPlanStatusChanged returned error: %v", err)
	}
	runs, err := svc.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs without a ready task, got %d: %#v", len(runs), runs)
	}
}

func TestWorkPlanStatusTriggerQueuesRequiredReviewTask(t *testing.T) {
	ctx := context.Background()
	reviewTask := readyTask("automation-review", "automation-review", []string{"internal/review.go"})
	reviewTask.Status = projectworkplan.WorkTaskStatusPlanned
	reviewTask.OwnerAgent = "reviewer-1"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a":            readyTask("task-a", "a", []string{"internal/foo.go"}),
		"automation-review": reviewTask,
	}}
	svc := New(newTestStore(), fake, Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: RunnerExecutionExternal,
		WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	svc.now = func() time.Time { return time.Unix(100, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:             "project-1",
		AutomationRef:         "auto/review-trigger",
		Title:                 "Review gated automatic automation",
		Purpose:               "Queue review task before implementation",
		Status:                AutomationStatusEnabled,
		AgentID:               "agent-1",
		PlanID:                "plan-1",
		TriggerKind:           TriggerKindAutomatic,
		PermissionRef:         "permission/default",
		AllowedTaskRefs:       []string{"a"},
		RequiredReviewTaskIDs: []string{"automation-review"},
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID: "project-1",
		PlanID:    "plan-1",
		OldStatus: projectworkplan.WorkPlanStatusPlanned,
		NewStatus: projectworkplan.WorkPlanStatusActive,
		ChangedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("HandleWorkPlanStatusChanged returned error: %v", err)
	}
	runs, err := svc.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected review and implementation runs, got %d: %#v", len(runs), runs)
	}
	if fake.tasks["automation-review"].Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected planned review task to become ready, got %q", fake.tasks["automation-review"].Status)
	}
	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.TaskID != "automation-review" || claimed.Run.AgentID != "reviewer-1" {
		t.Fatalf("expected review run to claim first, got %#v", claimed.Run)
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
		prompt := string(data)
		for _, want := range []string{"Perform the task now", "Work Task ID: task-a", "Automation run ID: ", "Verification requirement:"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("rendered codex prompt missing %q:\n%s", want, prompt)
			}
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

func TestExternalRunNowQueuesExplicitGitOpsPostTaskRecovery(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)

	run, err := svc.RunNow(ctx, SubmitRunInput{
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		TaskID:            "task-a",
		OrchestratorRunID: "explicit-gitops-recovery",
		SafeNextAction:    RunSafeSummaryGitOpsPostTaskRecovery,
	})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "gitops_post_task_failed" || run.SafeSummary != RunSafeSummaryGitOpsPostTaskRecovery {
		t.Fatalf("expected gitops recovery candidate, got %+v", run)
	}
	updatedTask := fake.tasks["task-a"]
	if updatedTask.ClaimedByRunID != run.ID || !containsRef(updatedTask.AgentRunIDs, run.ID) {
		t.Fatalf("expected task to record recovery run ownership, got task=%+v run=%+v", updatedTask, run)
	}
	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != run.ID || claimed.Run.SafeSummary != RunSafeSummaryGitOpsPostTaskRecovery || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected explicit gitops recovery run to claim, got %+v", claimed.Run)
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
	if claimed.Run.WorkTaskStatus != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected claimed run work task status in_progress, got %q", claimed.Run.WorkTaskStatus)
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

func TestCompleteAttemptQueuesPostImplementationReview(t *testing.T) {
	ctx := context.Background()
	implementationTask := readyTask("task-a", "fix-finding-a", []string{"internal/foo.go"})
	implementationTask.FilesToEdit = []string{"internal/foo.go"}
	reviewTask := readyTask("review-task-a", "review-fix-finding-a", []string{"internal/foo.go"})
	reviewTask.Status = projectworkplan.WorkTaskStatusPlanned
	reviewTask.OwnerAgent = "codex-reviewer"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		implementationTask.ID: implementationTask,
		reviewTask.ID:         reviewTask,
	}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	implementationAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/remediate",
		Title:           "Remediate",
		Purpose:         "Run implementation",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-worker",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{implementationTask.ID, implementationTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	reviewAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-remediation",
		Title:           "Review remediation",
		Purpose:         "Review implementation",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{reviewTask.ID, reviewTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: implementationAutomation.ProjectID, AutomationID: implementationAutomation.ID, TaskID: implementationTask.ID})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: implementationAutomation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	implementationTask.Status = projectworkplan.WorkTaskStatusNeedsReview
	fake.tasks[implementationTask.ID] = implementationTask

	done, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: implementationAutomation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if done.Status != RunStatusVerifying {
		t.Fatalf("expected implementation run verifying, got %#v", done)
	}
	if fake.tasks[reviewTask.ID].Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected review task ready, got %#v", fake.tasks[reviewTask.ID])
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: reviewAutomation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one queued review run, got %d: %#v", len(runs), runs)
	}
	if runs[0].Status != RunStatusQueued || runs[0].TaskID != reviewTask.ID || runs[0].ParentRunID != queued.ID || runs[0].SafeSummary != RunSafeSummaryPostImplementationReviewQueued {
		t.Fatalf("unexpected review run: %#v", runs[0])
	}
}

func TestCompleteAttemptDoesNotQueuePostImplementationReviewForReadOnlyAuditTask(t *testing.T) {
	ctx := context.Background()
	auditTask := readyTask("task-audit", "scan-for-candidate-bugs", []string{"internal/foo.go"})
	auditTask.ReviewGate = "independent_review_required"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{auditTask.ID: auditTask}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/audit",
		Title:           "Audit",
		Purpose:         "Run read-only audit",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{auditTask.ID, auditTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: auditTask.ID})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	auditTask.Status = projectworkplan.WorkTaskStatusVerifying
	fake.tasks[auditTask.ID] = auditTask

	done, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if done.Status != RunStatusVerifying {
		t.Fatalf("expected audit run verifying, got %#v", done)
	}
	automations, err := store.ListAutomations(ctx, AutomationFilter{ProjectID: "project-1", Status: AutomationStatusEnabled})
	if err != nil {
		t.Fatalf("ListAutomations returned error: %v", err)
	}
	if len(automations) != 1 {
		t.Fatalf("expected no generated review automation for read-only audit, got %#v", automations)
	}
}

func TestClaimNextRunQueuesPostImplementationReviewForVerifyingTask(t *testing.T) {
	ctx := context.Background()
	implementationTask := readyTask("task-a", "fix-finding-a", []string{"internal/foo.go"})
	implementationTask.FilesToEdit = []string{"internal/foo.go"}
	implementationTask.Status = projectworkplan.WorkTaskStatusVerifying
	implementationTask.ReviewGate = "independent_review_required"
	reviewTask := readyTask("review-task-a", "review-fix-finding-a", []string{"internal/foo.go"})
	reviewTask.Status = projectworkplan.WorkTaskStatusPlanned
	reviewTask.OwnerAgent = "codex-reviewer"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		implementationTask.ID: implementationTask,
		reviewTask.ID:         reviewTask,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	_, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-remediation",
		Title:           "Review remediation",
		Purpose:         "Review implementation",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{reviewTask.ID, reviewTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	parentRun := AutomationRun{
		ID:           "automation_run_parent",
		ProjectID:    "project-1",
		AutomationID: "automation_impl",
		AgentID:      "codex-worker",
		PlanID:       "plan-1",
		TaskID:       implementationTask.ID,
		Status:       RunStatusVerifying,
		RunnerKind:   RunnerKindCodexCLI,
		CreatedAt:    svc.now(),
		UpdatedAt:    svc.now(),
	}
	if _, err := store.CreateRun(ctx, parentRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if fake.tasks[reviewTask.ID].Status != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected review task in progress after claim, got %#v", fake.tasks[reviewTask.ID])
	}
	if claimed.Run.TaskID != reviewTask.ID || claimed.Run.ParentRunID != parentRun.ID || claimed.Run.SafeSummary != RunSafeSummaryPostImplementationReviewQueued {
		t.Fatalf("unexpected claimed review run: %#v", claimed.Run)
	}
}

func TestReconcileReadyDependentAutomationsReadiesAndQueuesNextTask(t *testing.T) {
	ctx := context.Background()
	dependency := readyTask("task-a", "collect-scope", []string{"apps"})
	dependency.Status = projectworkplan.WorkTaskStatusDone
	next := readyTask("task-b", "scan-bugs", []string{"apps"})
	next.Status = projectworkplan.WorkTaskStatusPlanned
	next.DependencyTaskIDs = []string{dependency.ID}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		dependency.ID: dependency,
		next.ID:       next,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.now = func() time.Time { return time.Unix(500, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan-bugs",
		Title:           "Scan bugs",
		Purpose:         "Run scan after scope collection",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{next.ID, next.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}

	if err := svc.reconcileReadyDependentAutomations(ctx, "project-1", "plan-1", dependency.ID); err != nil {
		t.Fatalf("reconcileReadyDependentAutomations returned error: %v", err)
	}
	if got := fake.tasks[next.ID].Status; got != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected dependent task ready, got %q", got)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].TaskID != next.ID || runs[0].Status != RunStatusQueued || runs[0].SafeSummary != "dependency_ready_automation_queued" {
		t.Fatalf("expected one queued dependency-ready run, got %#v", runs)
	}

	if err := svc.reconcileReadyDependentAutomations(ctx, "project-1", "plan-1", dependency.ID); err != nil {
		t.Fatalf("second reconcile returned error: %v", err)
	}
	runs, err = store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected idempotent queueing, got %d runs: %#v", len(runs), runs)
	}
}

func TestReconcileReadyDependentAutomationsDoesNotReadyPlannedTaskWithoutDependencies(t *testing.T) {
	ctx := context.Background()
	reviewTask := readyTask("task-review", "review-fix-confirmed-bug", []string{"apps"})
	reviewTask.Status = projectworkplan.WorkTaskStatusPlanned
	reviewTask.FilesToEdit = nil
	reviewTask.ReviewGate = "independent-reviewer-must-not-be-worker-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		reviewTask.ID: reviewTask,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-remediation",
		Title:           "Review remediation",
		Purpose:         "Review implementation after it reaches needs_review",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{reviewTask.ID, reviewTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission/review",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}

	if err := svc.reconcileReadyAutomationsForProject(ctx, "project-1"); err != nil {
		t.Fatalf("reconcileReadyAutomationsForProject returned error: %v", err)
	}
	if got := fake.tasks[reviewTask.ID].Status; got != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("expected review task to remain planned, got %q", got)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no review run before implementation reaches review, got %#v", runs)
	}
}

func TestReconcileReadyDependentAutomationsDoesNotDuplicateAfterTerminalRun(t *testing.T) {
	ctx := context.Background()
	dependency := readyTask("task-a", "collect-scope", []string{"apps"})
	dependency.Status = projectworkplan.WorkTaskStatusDone
	next := readyTask("task-b", "scan-bugs", []string{"apps"})
	next.Status = projectworkplan.WorkTaskStatusReady
	next.DependencyTaskIDs = []string{dependency.ID}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		dependency.ID: dependency,
		next.ID:       next,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 6})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan-bugs",
		Title:           "Scan bugs",
		Purpose:         "Run scan after scope collection",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{next.ID, next.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-blocked", ProjectID: "project-1", AutomationID: automation.ID, AgentID: "code-review-scanner",
		PlanID: "plan-1", TaskID: next.ID, Status: RunStatusBlocked, RunnerKind: RunnerKindCodexCLI,
		FailureCategory: "automation_review_gate_open", CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if err := svc.reconcileReadyDependentAutomations(ctx, "project-1", "plan-1", dependency.ID); err != nil {
		t.Fatalf("reconcileReadyDependentAutomations returned error: %v", err)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-blocked" {
		t.Fatalf("expected stale terminal run to prevent duplicate queueing, got %#v", runs)
	}
}

func TestReconcileReadyDependentAutomationsWaitsForAllDependencies(t *testing.T) {
	ctx := context.Background()
	doneDependency := readyTask("task-a", "collect-scope", []string{"apps"})
	doneDependency.Status = projectworkplan.WorkTaskStatusDone
	blockedDependency := readyTask("task-blocked", "blocked-scope", []string{"apps"})
	blockedDependency.Status = projectworkplan.WorkTaskStatusBlocked
	next := readyTask("task-b", "scan-bugs", []string{"apps"})
	next.Status = projectworkplan.WorkTaskStatusPlanned
	next.DependencyTaskIDs = []string{doneDependency.ID, blockedDependency.ID}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		doneDependency.ID:    doneDependency,
		blockedDependency.ID: blockedDependency,
		next.ID:              next,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	_, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan-bugs",
		Title:           "Scan bugs",
		Purpose:         "Run scan after scope collection",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{next.ID, next.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}

	if err := svc.reconcileReadyDependentAutomations(ctx, "project-1", "plan-1", doneDependency.ID); err != nil {
		t.Fatalf("reconcileReadyDependentAutomations returned error: %v", err)
	}
	if got := fake.tasks[next.ID].Status; got != projectworkplan.WorkTaskStatusPlanned {
		t.Fatalf("expected dependent task to remain planned, got %q", got)
	}
	runs, err := store.ListRuns(ctx, RunFilter{ProjectID: "project-1", PlanID: "plan-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no queued runs while dependency blocked, got %#v", runs)
	}
}

func TestClaimNextRunRecoversPlannedDependentTaskAfterRestart(t *testing.T) {
	ctx := context.Background()
	dependency := readyTask("task-a", "collect-scope", []string{"apps"})
	dependency.Status = projectworkplan.WorkTaskStatusDone
	next := readyTask("task-b", "scan-bugs", []string{"apps"})
	next.Status = projectworkplan.WorkTaskStatusPlanned
	next.DependencyTaskIDs = []string{dependency.ID}
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{
			dependency.ID: dependency,
			next.ID:       next,
		},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan-bugs",
		Title:           "Scan bugs",
		Purpose:         "Run scan after scope collection",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{next.ID, next.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "code-review-scanner", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.AutomationID != automation.ID || claimed.Run.TaskID != next.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected recovered dependent task run, got %#v", claimed.Run)
	}
	if got := fake.tasks[next.ID].Status; got != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected dependent task in progress after claim, got %q", got)
	}
}

func TestClaimNextRunRequeuesAbandonedRunningRunAfterRestart(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "scan-bugs", []string{"apps"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "automation_run_old"
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.startedAt = time.Unix(200, 0).UTC()
	svc.now = func() time.Time { return time.Unix(210, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan-bugs",
		Title:           "Scan bugs",
		Purpose:         "Run scan after restart recovery",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	oldRun := AutomationRun{
		ID:           "automation_run_old",
		ProjectID:    "project-1",
		AutomationID: automation.ID,
		AgentID:      "code-review-scanner",
		PlanID:       "plan-1",
		TaskID:       task.ID,
		Status:       RunStatusRunning,
		RunnerKind:   RunnerKindCodexCLI,
		StartedAt:    time.Unix(100, 0).UTC(),
		UpdatedAt:    time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "code-review-scanner", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID == oldRun.ID || claimed.Run.TaskID != task.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected replacement run claimed, got %#v", claimed.Run)
	}
	updatedOld, err := store.GetRun(ctx, "project-1", oldRun.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updatedOld.Status != RunStatusTimeout || updatedOld.FailureCategory != "external_runner_interrupted" {
		t.Fatalf("expected old run timed out as interrupted, got %#v", updatedOld)
	}
	if fake.tasks[task.ID].ClaimedByRunID != claimed.Run.ID || fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected task claimed by replacement run, got %#v", fake.tasks[task.ID])
	}
}

func TestClaimNextRunRequeuesAbandonedRunningRunWithReadyTaskAfterRestart(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "review-candidate-bugs", []string{"libs/platform"})
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.startedAt = time.Unix(200, 0).UTC()
	svc.now = func() time.Time { return time.Unix(210, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-bugs",
		Title:           "Review bugs",
		Purpose:         "Review candidate bugs after scanner completion",
		Status:          AutomationStatusEnabled,
		AgentID:         "bug-finding-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	oldRun := AutomationRun{
		ID:                "automation_run_old",
		ProjectID:         "project-1",
		AutomationID:      automation.ID,
		AgentID:           "bug-finding-reviewer",
		PlanID:            "plan-1",
		TaskID:            task.ID,
		WorkTaskStatus:    projectworkplan.WorkTaskStatusReady,
		Status:            RunStatusRunning,
		RunnerKind:        RunnerKindCodexCLI,
		SafeSummary:       "dependency_ready_automation_queued",
		OrchestratorRunID: dependencyReadyRunID(task, automation),
		StartedAt:         time.Unix(100, 0).UTC(),
		UpdatedAt:         time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "bug-finding-reviewer", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID == oldRun.ID || claimed.Run.TaskID != task.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected replacement run claimed, got %#v", claimed.Run)
	}
	updatedOld, err := store.GetRun(ctx, "project-1", oldRun.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updatedOld.Status != RunStatusTimeout || updatedOld.FailureCategory != "external_runner_interrupted" {
		t.Fatalf("expected old running run timed out as interrupted, got %#v", updatedOld)
	}
	if fake.tasks[task.ID].ClaimedByRunID != claimed.Run.ID || fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected task claimed by replacement run, got %#v", fake.tasks[task.ID])
	}
}

func TestRequeueAbandonedRunningRunSkipsStaleSnapshotAfterDurableFailure(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "review-candidate-bugs", []string{"libs/platform"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	oldRun := AutomationRun{
		ID:             "run-a",
		ProjectID:      "project-1",
		AutomationID:   "automation-a",
		AgentID:        "bug-finding-reviewer",
		PlanID:         "plan-1",
		TaskID:         task.ID,
		WorkTaskStatus: task.Status,
		Status:         RunStatusRunning,
		RunnerKind:     RunnerKindCodexCLI,
		ClaimID:        "claim-a",
		RunnerID:       "runner-a",
		LeaseExpiresAt: time.Unix(200, 0).UTC(),
		StartedAt:      time.Unix(100, 0).UTC(),
		UpdatedAt:      time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	durableFailure := oldRun
	durableFailure.Status = RunStatusFailed
	durableFailure.FailureCategory = "gitops_post_task_failed"
	durableFailure.FinishedAt = time.Unix(150, 0).UTC()
	durableFailure.UpdatedAt = time.Unix(150, 0).UTC()
	if _, err := store.UpdateRun(ctx, durableFailure); err != nil {
		t.Fatalf("UpdateRun returned error: %v", err)
	}

	updated, err := svc.requeueAbandonedRunningRun(ctx, oldRun, task)
	if err != nil {
		t.Fatalf("requeueAbandonedRunningRun returned error: %v", err)
	}
	if updated.Status != RunStatusFailed || updated.FailureCategory != "gitops_post_task_failed" {
		t.Fatalf("expected durable failure to be preserved, got %#v", updated)
	}
	persisted, err := store.GetRun(ctx, "project-1", "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if persisted.Status != RunStatusFailed || persisted.FailureCategory != "gitops_post_task_failed" {
		t.Fatalf("expected persisted durable failure to be preserved, got %#v", persisted)
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected task status unchanged, got %#v", fake.tasks[task.ID])
	}
}

func TestClaimNextRunRequeuesAbandonedClaimingRunAfterRestart(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "create-confirmed-bug-work-plans", []string{"packages/contracts"})
	task.Status = projectworkplan.WorkTaskStatusReady
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.startedAt = time.Unix(200, 0).UTC()
	svc.now = func() time.Time { return time.Unix(210, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/plan-bugs",
		Title:           "Plan bugs",
		Purpose:         "Create remediation plans after review",
		Status:          AutomationStatusEnabled,
		AgentID:         "bug-plan-orchestrator",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	oldRun := AutomationRun{
		ID:                "automation_run_old",
		ProjectID:         "project-1",
		AutomationID:      automation.ID,
		AgentID:           "bug-plan-orchestrator",
		PlanID:            "plan-1",
		TaskID:            task.ID,
		WorkTaskStatus:    projectworkplan.WorkTaskStatusReady,
		Status:            RunStatusClaiming,
		RunnerKind:        RunnerKindCodexCLI,
		SafeSummary:       "dependency_ready_automation_queued",
		OrchestratorRunID: dependencyReadyRunID(task, automation),
		UpdatedAt:         time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "bug-plan-orchestrator", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID == oldRun.ID || claimed.Run.TaskID != task.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected replacement run claimed, got %#v", claimed.Run)
	}
	updatedOld, err := store.GetRun(ctx, "project-1", oldRun.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updatedOld.Status != RunStatusTimeout || updatedOld.FailureCategory != "external_runner_interrupted" {
		t.Fatalf("expected old claiming run timed out as interrupted, got %#v", updatedOld)
	}
	if fake.tasks[task.ID].ClaimedByRunID != claimed.Run.ID || fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected task claimed by replacement run, got %#v", fake.tasks[task.ID])
	}
}

func TestClaimNextRunSyncsStartingRunWhenTaskAlreadyVerifying(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "review-fix-candidate", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "automation_run_old"
	task.VerifierResultRefs = []string{"verifier-orchestrator-pending"}
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.startedAt = time.Unix(200, 0).UTC()
	svc.now = func() time.Time { return time.Unix(210, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-fix",
		Title:           "Review fix",
		Purpose:         "Review completed implementation",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	oldRun := AutomationRun{
		ID:                "automation_run_old",
		ProjectID:         "project-1",
		AutomationID:      automation.ID,
		AgentID:           "codex-reviewer",
		PlanID:            "plan-1",
		TaskID:            task.ID,
		WorkTaskStatus:    projectworkplan.WorkTaskStatusClaimed,
		Status:            RunStatusStarting,
		RunnerKind:        RunnerKindCodexCLI,
		SafeSummary:       "post_implementation_review_queued",
		OrchestratorRunID: "post-review:automation_run_parent",
		UpdatedAt:         time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "codex-reviewer", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after syncing stale starting run, got %v", err)
	}
	updatedOld, err := store.GetRun(ctx, "project-1", oldRun.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updatedOld.Status != RunStatusVerifying && updatedOld.Status != RunStatusCompleted {
		t.Fatalf("expected old starting run synced out of starting, got %#v", updatedOld)
	}
	if updatedOld.WorkTaskStatus != projectworkplan.WorkTaskStatusVerifying && updatedOld.WorkTaskStatus != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected old starting run to reflect task verification or completion, got %#v", updatedOld)
	}
}

func TestClaimNextRunReclaimsStartingRunWithClaimedTask(t *testing.T) {
	ctx := context.Background()
	task := readyTask("review-task-a", "review-fix-candidate", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusClaimed
	task.OwnerAgent = "codex-reviewer"
	task.ClaimedByRunID = "automation_run_old"
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.startedAt = time.Unix(200, 0).UTC()
	svc.now = func() time.Time { return time.Unix(210, 0).UTC() }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-fix",
		Title:           "Review fix",
		Purpose:         "Review completed implementation",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	oldRun := AutomationRun{
		ID:                "automation_run_old",
		ProjectID:         "project-1",
		AutomationID:      automation.ID,
		AgentID:           "codex-reviewer",
		PlanID:            "plan-1",
		TaskID:            task.ID,
		WorkTaskStatus:    projectworkplan.WorkTaskStatusClaimed,
		Status:            RunStatusStarting,
		RunnerKind:        RunnerKindCodexCLI,
		SafeSummary:       RunSafeSummaryPostImplementationReviewQueued,
		OrchestratorRunID: "post-review:automation_run_parent",
		UpdatedAt:         time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "codex-reviewer", RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != oldRun.ID || claimed.Run.Status != RunStatusRunning || claimed.Run.AttemptCount != 1 {
		t.Fatalf("expected stale starting run reclaimed, got %#v", claimed.Run)
	}
	if claimed.Run.ClaimID == "" || claimed.Run.RunnerID != "runner-1" || claimed.Run.LeaseExpiresAt.IsZero() {
		t.Fatalf("expected fresh external claim, got %#v", claimed.Run)
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusInProgress || fake.tasks[task.ID].ClaimedByRunID != oldRun.ID {
		t.Fatalf("expected task started by reclaimed run, got %#v", fake.tasks[task.ID])
	}
}

func TestClaimNextRunRecoversMissingPostImplementationReview(t *testing.T) {
	ctx := context.Background()
	implementationTask := readyTask("task-a", "fix-finding-a", []string{"internal/foo.go"})
	implementationTask.FilesToEdit = []string{"internal/foo.go"}
	implementationTask.Status = projectworkplan.WorkTaskStatusNeedsReview
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{implementationTask.ID: implementationTask}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	oldRun := AutomationRun{
		ID:           "automation_run_old",
		ProjectID:    "project-1",
		AutomationID: "automation-old",
		PlanID:       "plan-1",
		TaskID:       implementationTask.ID,
		Status:       RunStatusVerifying,
		RunnerKind:   RunnerKindCodexCLI,
		UpdatedAt:    time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, oldRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ParentRunID != oldRun.ID || claimed.Run.SafeSummary != RunSafeSummaryPostImplementationReviewQueued {
		t.Fatalf("expected recovered review run claimed, got %#v", claimed.Run)
	}
	if claimed.CodexInput.TaskRef != "review-"+implementationTask.TaskRef {
		t.Fatalf("expected review task input, got %#v", claimed.CodexInput)
	}
	reviewTask := fake.tasks[claimed.Run.TaskID]
	if reviewTask.Status != projectworkplan.WorkTaskStatusInProgress || reviewTask.OwnerAgent == "" || reviewTask.OwnerAgent == implementationTask.OwnerAgent {
		t.Fatalf("expected independent in-progress review task, got %#v", reviewTask)
	}
}

func TestClaimNextRunReclaimsFailedPostImplementationReviewGitOpsPreflight(t *testing.T) {
	ctx := context.Background()
	reviewTask := readyTask("review-task-a", "review-fix-finding-a", []string{"internal/foo.go"})
	reviewTask.OwnerAgent = "codex-reviewer"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{reviewTask.ID: reviewTask}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review-remediation",
		Title:           "Review remediation",
		Purpose:         "Review implementation output",
		Status:          AutomationStatusEnabled,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{reviewTask.ID, reviewTask.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SchedulePolicy:  "post_implementation_review",
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	failedRun := AutomationRun{
		ID:              "automation_run_failed_review",
		ProjectID:       "project-1",
		AutomationID:    automation.ID,
		AgentID:         "codex-reviewer",
		PlanID:          "plan-1",
		TaskID:          reviewTask.ID,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		FailureCategory: "gitops_dirty_worktree",
		SafeSummary:     RunSafeSummaryPostImplementationReviewQueued,
		UpdatedAt:       time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateRun(ctx, failedRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != failedRun.ID || claimed.Run.Status != RunStatusRunning || claimed.Run.FailureCategory != "" || claimed.Run.AttemptCount != 1 {
		t.Fatalf("expected failed review run reclaimed, got %#v", claimed.Run)
	}
	if !contains(claimed.CodexInput.RunnerInstructions, "Attach a review_result_ref to the implementation task before completing this review task.") {
		t.Fatalf("expected review closeout instruction, got %#v", claimed.CodexInput.RunnerInstructions)
	}
}

func TestClaimNextRunReconcilesVerifyingTaskToDoneAndCompletesPlan(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "fix-finding-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{
		plans:           map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks:           map[string]projectworkplan.WorkTask{task.ID: task},
		attachReviewErr: errors.New("must be attached by an independent run"),
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	run := AutomationRun{ID: "run-a", ProjectID: "project-1", PlanID: "plan-1", TaskID: task.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI, SafeSummary: "external_codex_cli_completed_verification_required"}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after reconciliation, got %v", err)
	}
	completed, err := svc.GetRun(ctx, "project-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.Status != RunStatusCompleted || completed.SafeSummary != RunSafeSummaryVerifiedTaskDone {
		t.Fatalf("expected completed run after reconciliation, got %#v", completed)
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected task done, got %#v", fake.tasks[task.ID])
	}
	if contains(fake.reviewRefs, "review/approved") {
		t.Fatalf("closeout must not reattach existing review ref, got %#v", fake.reviewRefs)
	}
	if fake.plans["plan-1"].Status != projectworkplan.WorkPlanStatusDone {
		t.Fatalf("expected plan done, got %#v", fake.plans["plan-1"])
	}
}

func TestClaimNextRunReconcilesReviewTaskWithoutSecondaryReview(t *testing.T) {
	ctx := context.Background()
	task := readyTask("review-task-a", "review-fix-finding-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.VerifierResultRefs = []string{"verifier/source-review"}
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	run := AutomationRun{ID: "run-review", ProjectID: "project-1", PlanID: "plan-1", TaskID: task.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI, SafeSummary: RunSafeSummaryPostImplementationReviewQueued}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after review reconciliation, got %v", err)
	}
	completed, err := svc.GetRun(ctx, "project-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.Status != RunStatusCompleted {
		t.Fatalf("expected completed review run, got %#v", completed)
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusDone || fake.tasks[task.ID].ReviewExemptReason == "" {
		t.Fatalf("expected review task done with exemption, got %#v", fake.tasks[task.ID])
	}
}

func TestClaimNextRunClosesReadOnlyScannerAndQueuesDependentReview(t *testing.T) {
	ctx := context.Background()
	scan := readyTask("scan-task-a", "scan-for-candidate-bugs-audit-alpha", []string{"repo-audit-scope"})
	scan.Status = projectworkplan.WorkTaskStatusVerifying
	scan.FilesToEdit = nil
	scan.VerifierResultRefs = []string{"verifier/audit-scan"}
	review := readyTask("review-task-a", "review-candidate-bugs-audit-alpha", []string{"repo-audit-scope"})
	review.Status = projectworkplan.WorkTaskStatusPlanned
	review.OwnerAgent = "bug-finding-reviewer"
	review.DependencyTaskIDs = []string{scan.ID}
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks: map[string]projectworkplan.WorkTask{scan.ID: scan, review.ID: review},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	scanAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan",
		Title:           "Scan",
		Purpose:         "Run scanner",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{scan.ID, scan.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation scan returned error: %v", err)
	}
	reviewAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review",
		Title:           "Review",
		Purpose:         "Run independent review",
		Status:          AutomationStatusEnabled,
		AgentID:         "bug-finding-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{review.ID, review.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation review returned error: %v", err)
	}
	run := AutomationRun{ID: "run-scan", ProjectID: "project-1", AutomationID: scanAutomation.ID, PlanID: "plan-1", TaskID: scan.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.AutomationID != reviewAutomation.ID || claimed.Run.TaskID != review.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected dependent review run claimed, got %#v", claimed.Run)
	}
	completed, err := store.GetRun(ctx, "project-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.Status != RunStatusCompleted {
		t.Fatalf("expected scanner run completed, got %#v", completed)
	}
	if fake.tasks[scan.ID].Status != projectworkplan.WorkTaskStatusDone || fake.tasks[scan.ID].ReviewExemptReason == "" {
		t.Fatalf("expected scanner task done with read-only exemption, got %#v", fake.tasks[scan.ID])
	}
}

func TestClaimNextRunClosesReadOnlyScannerWithCandidateRefsAndQueuesReview(t *testing.T) {
	ctx := context.Background()
	scan := readyTask("scan-task-a", "scan-for-candidate-bugs-audit-alpha", []string{"repo-audit-scope"})
	scan.Status = projectworkplan.WorkTaskStatusNeedsReview
	scan.FilesToEdit = nil
	scan.ClaimRefs = []string{"candidate.mobile.trip-summary-return-seat-crash"}
	scan.EvidenceRefs = []string{"anchor.mobile.trip-summary-seat-labels", "falsification.mobile-scan"}
	review := readyTask("review-task-a", "review-candidate-bugs-audit-alpha", []string{"repo-audit-scope"})
	review.Status = projectworkplan.WorkTaskStatusPlanned
	review.OwnerAgent = "bug-finding-reviewer"
	review.DependencyTaskIDs = []string{scan.ID}
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks: map[string]projectworkplan.WorkTask{scan.ID: scan, review.ID: review},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	scanAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/scan",
		Title:           "Scan",
		Purpose:         "Run scanner",
		Status:          AutomationStatusEnabled,
		AgentID:         "code-review-scanner",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{scan.ID, scan.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation scan returned error: %v", err)
	}
	reviewAutomation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/review",
		Title:           "Review",
		Purpose:         "Run independent review",
		Status:          AutomationStatusEnabled,
		AgentID:         "bug-finding-reviewer",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{review.ID, review.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		PermissionRef:   "permission/default",
	})
	if err != nil {
		t.Fatalf("CreateAutomation review returned error: %v", err)
	}
	run := AutomationRun{ID: "run-scan", ProjectID: "project-1", AutomationID: scanAutomation.ID, PlanID: "plan-1", TaskID: scan.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.AutomationID != reviewAutomation.ID || claimed.Run.TaskID != review.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected dependent review run claimed, got %#v", claimed.Run)
	}
	completed, err := store.GetRun(ctx, "project-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.Status != RunStatusCompleted {
		t.Fatalf("expected scanner run completed, got %#v", completed)
	}
	if fake.tasks[scan.ID].Status != projectworkplan.WorkTaskStatusDone || fake.tasks[scan.ID].ReviewExemptReason == "" {
		t.Fatalf("expected scanner task done with read-only exemption, got %#v", fake.tasks[scan.ID])
	}
	if !contains(fake.tasks[scan.ID].VerifierResultRefs, "verifier.automation.read-only-scanner-output") {
		t.Fatalf("expected scanner closeout verifier ref, got %#v", fake.tasks[scan.ID].VerifierResultRefs)
	}
}

func TestClaimNextRunDoesNotCloseReadOnlyScannerWithOnlyRunnerBookkeeping(t *testing.T) {
	ctx := context.Background()
	scan := readyTask("scan-task-a", "scan-for-candidate-bugs-audit-alpha", []string{"repo-audit-scope"})
	scan.Status = projectworkplan.WorkTaskStatusVerifying
	scan.FilesToEdit = nil
	scan.EvidenceRefs = []string{"automation_run:run-scan", "evidence/action-start"}
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks: map[string]projectworkplan.WorkTask{scan.ID: scan},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	if _, err := store.CreateRun(ctx, AutomationRun{ID: "run-scan", ProjectID: "project-1", AutomationID: "automation-scan", PlanID: "plan-1", TaskID: scan.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run and no closeout, got %v", err)
	}
	run, err := store.GetRun(ctx, "project-1", "run-scan")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if run.Status != RunStatusVerifying {
		t.Fatalf("expected scanner run to remain verifying, got %#v", run)
	}
	if fake.tasks[scan.ID].Status != projectworkplan.WorkTaskStatusVerifying {
		t.Fatalf("expected scanner task to remain verifying, got %#v", fake.tasks[scan.ID])
	}
}

func TestRemediationPlannerIsNotReadOnlyScannerCloseoutTask(t *testing.T) {
	task := readyTask("planner-task", "create-confirmed-bug-work-plans-audit-alpha", []string{"repo-audit-scope"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.OwnerAgent = "bug-plan-orchestrator"
	task.FilesToEdit = nil
	task.VerifierResultRefs = []string{"verifier/planner"}
	if isReadOnlyScannerTask(task) {
		t.Fatalf("planner task must not be treated as scanner closeout task: %#v", task)
	}
	if taskReadyForAutomationCloseout(task) {
		t.Fatalf("planner task without review refs or exemption must not close out: %#v", task)
	}
}

func TestClaimNextRunClosesNoConfirmedBugPlannerWithoutSecondaryReview(t *testing.T) {
	ctx := context.Background()
	task := readyTask("planner-task", "create-confirmed-bug-work-plans-audit-alpha", []string{"repo-audit-scope"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.OwnerAgent = "bug-plan-orchestrator"
	task.FilesToEdit = nil
	task.VerifierResultRefs = []string{"verifier/planner"}
	task.EvidenceRefs = []string{"audit-delta.no-confirmed-bugs", "audit-delta.retry2.no-remediation-work-plans", "confirmed-bug-refs-none", "review-gate-confirmed-bug-independent-review"}
	task.ClaimRefs = []string{"audit-delta.retry2.no-confirmed-bugs-planner"}
	task.Outcome = "No confirmed bugs were available from the completed independent review, so no bug Work Plans or remediation tasks were created."
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	run := AutomationRun{ID: "run-planner", ProjectID: "project-1", PlanID: "plan-1", TaskID: task.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI, SafeSummary: "external_codex_cli_completed_verification_required"}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after planner reconciliation, got %v", err)
	}
	completed, err := svc.GetRun(ctx, "project-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.Status != RunStatusCompleted || completed.WorkTaskStatus != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected completed planner run, got %#v", completed)
	}
	done := fake.tasks[task.ID]
	if done.Status != projectworkplan.WorkTaskStatusDone || done.ReviewExemptReason == "" {
		t.Fatalf("expected planner task done with no-confirmed-bug exemption, got %#v", done)
	}
	if !contains(done.ClaimRefs, "audit-delta.retry2.no-confirmed-bugs-planner") {
		t.Fatalf("expected no-confirmed-bug claim ref, got %#v", done.ClaimRefs)
	}
}

func TestClaimNextRunSyncsVerifyingRunWorkTaskStatusWhenNotReadyForCloseout(t *testing.T) {
	ctx := context.Background()
	task := readyTask("planner-task", "create-confirmed-bug-work-plans-audit-alpha", []string{"repo-audit-scope"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.OwnerAgent = "bug-plan-orchestrator"
	task.FilesToEdit = nil
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive}},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	run := AutomationRun{ID: "run-planner", ProjectID: "project-1", PlanID: "plan-1", TaskID: task.ID, WorkTaskStatus: projectworkplan.WorkTaskStatusInProgress, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI, SafeSummary: "external_codex_cli_completed_verification_required"}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no queued run after status sync, got %v", err)
	}
	updated, err := svc.GetRun(ctx, "project-1", run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updated.Status != RunStatusVerifying || updated.WorkTaskStatus != projectworkplan.WorkTaskStatusVerifying {
		t.Fatalf("expected verifying run to mirror task status, got %#v", updated)
	}
}

func TestGetRunReturnsPersistedAutomationMetadataWithoutWorkTaskProjection(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusCompleted})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if run.Status != RunStatusVerifying || run.WorkTaskStatus != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected pre-verifier run to retain in_progress/verifying, got %#v", run)
	}

	task.Status = projectworkplan.WorkTaskStatusDone
	fake.tasks["task-a"] = task
	persisted, err := svc.GetRun(ctx, automation.ProjectID, queued.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if persisted.Status != RunStatusVerifying {
		t.Fatalf("expected persisted verifying status, got %q", persisted.Status)
	}
	if persisted.WorkTaskStatus != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected persisted work task status, got %q", persisted.WorkTaskStatus)
	}
	if persisted.SafeSummary != "external_codex_cli_completed_verification_required" {
		t.Fatalf("unexpected summary: %q", persisted.SafeSummary)
	}
}

func TestListRunsReconcilesRunningWorkTaskStatusSnapshot(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.startedAt = time.Unix(100, 0).UTC()
	svc.now = func() time.Time { return time.Unix(210, 0).UTC() }
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-a",
		ProjectID:      "project-1",
		AutomationID:   "automation-a",
		AgentID:        "agent-a",
		PlanID:         "plan-1",
		TaskID:         "task-a",
		WorkTaskStatus: projectworkplan.WorkTaskStatusReady,
		Status:         RunStatusRunning,
		RunnerKind:     RunnerKindCodexCLI,
		StartedAt:      time.Unix(200, 0).UTC(),
		UpdatedAt:      time.Unix(200, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	runs, err := svc.ListRuns(ctx, RunFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].WorkTaskStatus != projectworkplan.WorkTaskStatusInProgress {
		t.Fatalf("expected reconciled in_progress run snapshot, got %#v", runs)
	}
}

func TestListRunsReconcilesPersistedVerifyingRunsBeforeFiltering(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusDone
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.now = func() time.Time { return time.Unix(200, 0).UTC() }
	automation := createTestAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:             "run-a",
		ProjectID:      automation.ProjectID,
		AutomationID:   automation.ID,
		AgentID:        automation.AgentID,
		PlanID:         automation.PlanID,
		TaskID:         "task-a",
		WorkTaskStatus: projectworkplan.WorkTaskStatusReady,
		Status:         RunStatusVerifying,
		RunnerKind:     RunnerKindCodexCLI,
		CreatedAt:      time.Unix(100, 0).UTC(),
		UpdatedAt:      time.Unix(100, 0).UTC(),
	}
	if _, err := svc.store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	runs, err := svc.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, Status: RunStatusCompleted})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusCompleted || runs[0].WorkTaskStatus != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected reconciled completed/done run, got %#v", runs)
	}
	runs, err = svc.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, Status: RunStatusVerifying})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no stale verifying runs after reconciliation, got %#v", runs)
	}
}

func TestGetRunDoesNotCallWorkTaskProjection(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{
		tasks:                map[string]projectworkplan.WorkTask{"task-a": readyTask("task-a", "a", []string{"internal/foo.go"})},
		blockGetWorkTaskDone: make(chan struct{}),
	}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation := createTestAutomation(t, ctx, svc)
	run := AutomationRun{
		ID:             "run-a",
		ProjectID:      automation.ProjectID,
		AutomationID:   automation.ID,
		AgentID:        automation.AgentID,
		PlanID:         automation.PlanID,
		TaskID:         "task-a",
		WorkTaskStatus: projectworkplan.WorkTaskStatusReady,
		Status:         RunStatusVerifying,
		RunnerKind:     RunnerKindCodexCLI,
		CreatedAt:      time.Unix(100, 0).UTC(),
		UpdatedAt:      time.Unix(100, 0).UTC(),
	}
	if _, err := svc.store.CreateRun(ctx, run); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	started := time.Now()
	got, err := svc.GetRun(ctx, automation.ProjectID, run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("GetRun blocked unexpectedly for %s", elapsed)
	}
	if got.ID != run.ID || got.Status != RunStatusVerifying {
		t.Fatalf("expected persisted run when projection is unavailable, got %#v", got)
	}
	close(fake.blockGetWorkTaskDone)
}

func TestSubmitRunQueuesRequiredAutomationReviewBeforeImplementation(t *testing.T) {
	ctx := context.Background()
	reviewTask := readyTask("automation-review", "automation-review", []string{"internal/foo.go"})
	reviewTask.OwnerAgent = "reviewer-1"
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

	implRun, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: "project-1", AutomationID: automation.ID, PlanID: "plan-1", TaskID: "task-a", RunnerKind: RunnerKindCodexCLI, OrchestratorRunID: "orch-1"})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}
	if implRun.Status != RunStatusBlocked || implRun.FailureCategory != "automation_review_gate_open" {
		t.Fatalf("expected implementation run blocked behind review gate, got %#v", implRun)
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected review and implementation runs, got %d: %#v", len(runs), runs)
	}
	claimedReview, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error for review run: %v", err)
	}
	if claimedReview.Run.TaskID != "automation-review" || claimedReview.Run.AgentID != "reviewer-1" {
		t.Fatalf("expected review run to claim first, got %#v", claimedReview.Run)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil {
		t.Fatal("expected no implementation run to be claimable until review completes")
	}
	blockedImpl, err := svc.GetRun(ctx, "project-1", implRun.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if blockedImpl.Status != RunStatusBlocked || blockedImpl.FailureCategory != "automation_review_gate_open" {
		t.Fatalf("expected implementation run blocked until review completes, got %#v", blockedImpl)
	}

	reviewTask.Status = projectworkplan.WorkTaskStatusDone
	fake.tasks["automation-review"] = reviewTask
	implRun.ID = "run-review-done"
	implRun.Status = RunStatusQueued
	implRun.FailureCategory = ""
	if _, err := svc.store.CreateRun(ctx, implRun); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error after review: %v", err)
	}
	if claimed.Run.ID != implRun.ID || claimed.Run.Status != RunStatusRunning {
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

func TestMCPCompleteAttemptCarriesReviewRefs(t *testing.T) {
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
	arguments, err := json.Marshal(map[string]any{
		"id":                 automation.ProjectID,
		"run_id":             queued.ID,
		"status":             RunStatusCompleted,
		"review_result_refs": []string{"review/approved"},
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	if _, err := svc.CallAutomationTool(ctx, "projects.automation_runs.complete_attempt", arguments); err != nil {
		t.Fatalf("CallAutomationTool returned error: %v", err)
	}
	if !contains(fakeTasks.reviewRefs, "review/approved") {
		t.Fatalf("expected MCP review ref attachment, got %#v", fakeTasks.reviewRefs)
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

func TestCompleteAttemptAcceptsAlreadyReconcilingVerifyingRun(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "scan-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.VerifierResultRefs = []string{"verifier-a"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: "automation-a", AgentID: "code-review-scanner",
		PlanID: task.PlanID, TaskID: task.ID, Status: RunStatusVerifying, RunnerKind: RunnerKindCodexCLI, AttemptCount: 1,
		SafeSummary: "external_codex_cli_completed_verification_required", CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: "project-1", RunID: "run-a", Status: RunStatusCompleted, DurationMS: 1234})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error for already-verifying run: %v", err)
	}
	if len(store.attempts) != 1 {
		t.Fatalf("expected completed attempt to be recorded, got %d", len(store.attempts))
	}
	if run.Status != RunStatusCompleted || fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected verifying race to close out cleanly, run=%#v task=%#v", run, fake.tasks[task.ID])
	}
}

func TestCompleteAttemptAcceptsRecoveredBlockedStartRunWhenTaskProgressed(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "scan-a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	task.EvidenceRefs = []string{"evidence-a"}
	task.ClaimRefs = []string{"claim-a"}
	task.VerifierResultRefs = []string{"verifier-a"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID: "run-a", ProjectID: "project-1", AutomationID: "automation-a", AgentID: "code-review-scanner",
		PlanID: task.PlanID, TaskID: task.ID, Status: RunStatusBlocked, RunnerKind: RunnerKindCodexCLI, AttemptCount: 1,
		FailureCategory: "start_failed", CreatedAt: time.Unix(100, 0).UTC(), UpdatedAt: time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: "project-1", RunID: "run-a", Status: RunStatusCompleted, DurationMS: 1234})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error for recovered blocked run: %v", err)
	}
	if len(store.attempts) != 1 {
		t.Fatalf("expected completed attempt to be recorded, got %d", len(store.attempts))
	}
	if run.Status != RunStatusCompleted || run.FailureCategory != "" || fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected recovered blocked run to close out cleanly, run=%#v task=%#v", run, fake.tasks[task.ID])
	}
}

func TestClaimNextRunReclaimsGitOpsPostTaskFailure(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	queued, err := svc.RunNow(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a"})
	if err != nil {
		t.Fatalf("RunNow returned error: %v", err)
	}
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: queued.ID, Status: RunStatusFailed, FailureCategory: "gitops_post_task_failed"}); err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = queued.ID
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	fake.tasks["task-a"] = task

	reclaimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun recovery returned error: %v", err)
	}
	if reclaimed.Run.ID != queued.ID || reclaimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected failed run to be reclaimed as running, got %+v", reclaimed.Run)
	}
	if reclaimed.Run.SafeSummary != RunSafeSummaryGitOpsPostTaskRecovery {
		t.Fatalf("expected gitops recovery summary, got %q", reclaimed.Run.SafeSummary)
	}
	if reclaimed.Run.AttemptCount != 2 || reclaimed.Run.WorkTaskStatus != projectworkplan.WorkTaskStatusNeedsReview {
		t.Fatalf("expected second recovery attempt with current task status, got %+v", reclaimed.Run)
	}
}

func TestClaimNextRunReclaimsGitOpsPostTaskFailureAcrossAgentFilter(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = "run-a"
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       time.Unix(100, 0).UTC(),
		UpdatedAt:       time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	nextTask := readyTask("task-b", "b", []string{"internal/bar.go"})
	nextTask.Status = projectworkplan.WorkTaskStatusReady
	fake.tasks[nextTask.ID] = nextTask
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:           "run-b",
		ProjectID:    automation.ProjectID,
		AutomationID: automation.ID,
		AgentID:      "bug-remediation-worker",
		PlanID:       nextTask.PlanID,
		TaskID:       nextTask.ID,
		Status:       RunStatusQueued,
		RunnerKind:   RunnerKindCodexCLI,
		CreatedAt:    time.Unix(101, 0).UTC(),
		UpdatedAt:    time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	reclaimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, AgentID: "bug-remediation-worker", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun recovery returned error: %v", err)
	}
	if reclaimed.Run.ID != "run-a" || reclaimed.Run.Status != RunStatusRunning || reclaimed.Run.SafeSummary != RunSafeSummaryGitOpsPostTaskRecovery {
		t.Fatalf("expected cross-agent GitOps recovery before queued work, got %+v", reclaimed.Run)
	}
}

func TestClaimNextRunSkipsStaleGitOpsPostTaskFailureForRequeuedTask(t *testing.T) {
	ctx := context.Background()
	staleTask := readyTask("task-a", "a", []string{"internal/foo.go"})
	staleTask.Status = projectworkplan.WorkTaskStatusReady
	staleTask.EvidenceRefs = []string{"implementation/evidence"}
	staleTask.VerifierResultRefs = []string{"verifier/focused"}
	nextTask := readyTask("task-b", "b", []string{"internal/bar.go"})
	nextTask.Status = projectworkplan.WorkTaskStatusReady
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": staleTask,
		"task-b": nextTask,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          staleTask.PlanID,
		TaskID:          staleTask.ID,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       time.Unix(100, 0).UTC(),
		UpdatedAt:       time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:           "run-b",
		ProjectID:    automation.ProjectID,
		AutomationID: automation.ID,
		AgentID:      automation.AgentID,
		PlanID:       nextTask.PlanID,
		TaskID:       nextTask.ID,
		Status:       RunStatusQueued,
		RunnerKind:   RunnerKindCodexCLI,
		CreatedAt:    time.Unix(101, 0).UTC(),
		UpdatedAt:    time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-b" || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected queued run-b to claim after stale recovery skip, got %+v", claimed.Run)
	}
}

func TestClaimNextRunSkipsExhaustedGitOpsPostTaskRecovery(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = "run-a"
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	nextTask := readyTask("task-b", "b", []string{"internal/bar.go"})
	nextTask.Status = projectworkplan.WorkTaskStatusReady
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a": task,
		"task-b": nextTask,
	}}
	store := newTestStore()
	svc := New(store, fake, Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  RunnerExecutionExternal,
		MaxParallelTasks: 1,
		Agents:           []AutomationAgent{{ID: "bug-fix-implementer", MaxRetries: 2}},
	})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         "bug-fix-implementer",
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    2,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       time.Unix(100, 0).UTC(),
		UpdatedAt:       time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:           "run-b",
		ProjectID:    automation.ProjectID,
		AutomationID: automation.ID,
		AgentID:      automation.AgentID,
		PlanID:       nextTask.PlanID,
		TaskID:       nextTask.ID,
		Status:       RunStatusQueued,
		RunnerKind:   RunnerKindCodexCLI,
		CreatedAt:    time.Unix(101, 0).UTC(),
		UpdatedAt:    time.Unix(101, 0).UTC(),
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID != "run-b" || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected queued run after exhausted recovery was skipped, got %+v", claimed.Run)
	}
	exhausted, err := store.GetRun(ctx, automation.ProjectID, "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if exhausted.Status != RunStatusFailed || exhausted.AttemptCount != 2 {
		t.Fatalf("expected exhausted recovery to remain unchanged, got %+v", exhausted)
	}
}

func TestClaimNextRunSerializesGitOpsPostTaskRecoveryClaims(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusNeedsReview
	task.ClaimedByRunID = "run-a"
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	var recoveryUpdates atomic.Int32
	releaseFirstUpdate := make(chan struct{})
	store.beforeUpdateRun = func(value AutomationRun) {
		if value.Status == RunStatusRunning && value.SafeSummary == RunSafeSummaryGitOpsPostTaskRecovery {
			if recoveryUpdates.Add(1) == 1 {
				<-releaseFirstUpdate
			}
		}
	}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	successes := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
			if err != nil {
				errs <- err
				return
			}
			successes <- claimed.Run.ID
		}()
	}
	deadline := time.After(time.Second)
	for recoveryUpdates.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first recovery update")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(releaseFirstUpdate)
	wg.Wait()
	close(successes)
	close(errs)

	if len(successes) != 1 {
		t.Fatalf("expected exactly one recovery claim, got %d successes and %d errors", len(successes), len(errs))
	}
	if recoveryUpdates.Load() != 1 {
		t.Fatalf("expected one recovery update, got %d", recoveryUpdates.Load())
	}
}

func TestCompleteAttemptClosesFailedGitOpsRecoveryWhenTaskAlreadyVerified(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	task.FilesToEdit = []string{"internal/foo.go"}
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    2,
		FailureCategory: "gitops_post_task_failed",
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusCompleted,
		EvidenceRefs:    []string{"git-no-changes"},
		FailureCategory: "",
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if run.Status != RunStatusCompleted || fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected failed recovery completion to close task, run=%#v task=%#v", run, fake.tasks[task.ID])
	}
}

func TestCompleteAttemptAcceptsStartingRunOwnedByWorkTask(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-a",
		ProjectID:      automation.ProjectID,
		AutomationID:   automation.ID,
		AgentID:        automation.AgentID,
		PlanID:         task.PlanID,
		TaskID:         task.ID,
		WorkTaskStatus: projectworkplan.WorkTaskStatusClaimed,
		Status:         RunStatusStarting,
		RunnerKind:     RunnerKindCodexCLI,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusFailed,
		FailureCategory: "worktree_resolve_failed",
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "worktree_resolve_failed" {
		t.Fatalf("expected starting run failure to be recorded, got %+v", run)
	}
}

func TestCompleteAttemptAcceptsDuplicateFailedGitOpsRecoveryReport(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    2,
		FailureCategory: "gitops_post_task_failed",
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusFailed,
		FailureCategory: "gitops_post_task_failed",
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error for duplicate failed report: %v", err)
	}
	if run.Status != RunStatusFailed || run.AttemptCount != 2 || run.FailureCategory != "gitops_post_task_failed" {
		t.Fatalf("expected duplicate failed report to return existing terminal run, got %+v", run)
	}
	if len(store.attempts) != 0 {
		t.Fatalf("expected duplicate terminal report to skip attempt write, got %d attempts", len(store.attempts))
	}
}

func TestCompleteAttemptAcceptsStaleFailedReportAfterRunAdvancedToVerifying(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createTestAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-a",
		ProjectID:      automation.ProjectID,
		AutomationID:   automation.ID,
		AgentID:        automation.AgentID,
		PlanID:         task.PlanID,
		TaskID:         task.ID,
		WorkTaskStatus: task.Status,
		Status:         RunStatusVerifying,
		RunnerKind:     RunnerKindCodexCLI,
		AttemptCount:   1,
		SafeSummary:    "external_codex_cli_completed_verification_required",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusFailed,
		FailureCategory: "gitops_post_task_failed",
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error for stale failed report: %v", err)
	}
	if run.Status != RunStatusVerifying || run.FailureCategory != "" {
		t.Fatalf("expected stale failed report to leave advanced run unchanged, got %+v", run)
	}
	if len(store.attempts) != 0 {
		t.Fatalf("expected stale failed report to skip attempt write, got %d attempts", len(store.attempts))
	}
}

func TestCompleteAttemptAcceptsCompletedReportAfterAuditReconciliationFailure(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "create-confirmed-bug-plan", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusFailed
	task.ClaimedByRunID = "run-a"
	task.ClaimRefs = []string{"claim.complete-attempt-review-ref-bypass.confirmed"}
	task.VerifierResultRefs = []string{"verifier.audit"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation := createTestAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		FailureCategory: "confirmed_finding_remediation_missing",
		ClaimID:         "claim-a",
		RunnerID:        "runner-1",
		ClaimedAt:       now.Add(-time.Minute),
		LastHeartbeatAt: now.Add(-time.Second),
		LeaseExpiresAt:  now.Add(time.Minute),
		StartedAt:       now.Add(-time.Minute),
		FinishedAt:      now,
		CreatedAt:       now.Add(-time.Minute),
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:  automation.ProjectID,
		RunID:      "run-a",
		Status:     RunStatusCompleted,
		ClaimID:    "claim-a",
		RunnerID:   "runner-1",
		DurationMS: 1234,
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error for reconciled audit failure race: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "confirmed_finding_remediation_missing" {
		t.Fatalf("expected terminal audit failure preserved, got %+v", run)
	}
	if len(store.attempts) != 0 {
		t.Fatalf("expected late completed report to skip duplicate attempt write, got %d attempts", len(store.attempts))
	}
}

func TestCompleteAttemptRequeuesImplementationAfterGitOpsRecoveryFailure(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusRunning,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    2,
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		FailureCategory: "",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusFailed,
		FailureCategory: "gitops_verification_failed",
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "gitops_recovery_failed_requires_implementation" || !strings.Contains(run.SafeSummary, "gitops_verification_failed") {
		t.Fatalf("expected terminal GitOps recovery reroute, got %+v", run)
	}
	requeuedTask := fake.tasks[task.ID]
	if requeuedTask.Status != projectworkplan.WorkTaskStatusReady || requeuedTask.ClaimedByRunID != "" {
		t.Fatalf("expected task to be ready for implementation, got %+v", requeuedTask)
	}
	var replacement AutomationRun
	for _, candidate := range store.runs {
		if candidate.ID != "run-a" && candidate.TaskID == task.ID && candidate.Status == RunStatusQueued {
			replacement = candidate
		}
	}
	if replacement.ID == "" {
		t.Fatalf("expected replacement queued implementation run, runs=%+v", store.runs)
	}
	if replacement.AgentID != automation.AgentID {
		t.Fatalf("expected replacement owner %q, got %+v", automation.AgentID, replacement)
	}
	if claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI}); err != nil || claimed.Run.ID != replacement.ID {
		t.Fatalf("expected replacement run to be claimed, got run=%+v err=%v", claimed.Run, err)
	}
}

func TestCompleteAttemptExpandsScopeForDirtyPathsUnderLikelyFiles(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	task.FilesToEdit = []string{"apps/domain/src/service.ts"}
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusRunning,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    2,
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		FailureCategory: "",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusFailed,
		FailureCategory: "gitops_dirty_worktree_scope",
		EvidenceRefs:    []string{"gitops-dirty-path:apps/domain/src/module.ts"},
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "gitops_recovery_failed_requires_implementation" {
		t.Fatalf("expected recovery implementation requeue, got %+v", run)
	}
	requeuedTask := fake.tasks[task.ID]
	if !containsString(requeuedTask.FilesToEdit, "apps/domain/src") {
		t.Fatalf("expected likely directory scope to be added to files_to_edit, got %+v", requeuedTask.FilesToEdit)
	}
	if !strings.Contains(requeuedTask.ResumeInstructions, "apps/domain/src/module.ts") {
		t.Fatalf("expected resume instructions to name dirty path, got %q", requeuedTask.ResumeInstructions)
	}
}

func TestCompleteAttemptBlocksDirtyPathsOutsideLikelyFiles(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"apps/domain/src/service.ts"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	task.FilesToEdit = []string{"apps/domain/src/service.ts"}
	task.LikelyFilesAffected = []string{"apps/domain/src"}
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusRunning,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    2,
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		FailureCategory: "",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	run, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:       automation.ProjectID,
		RunID:           "run-a",
		Status:          RunStatusFailed,
		FailureCategory: "gitops_dirty_worktree_scope",
		EvidenceRefs:    []string{"gitops-dirty-path:apps/other/src/module.ts"},
	})
	if err != nil {
		t.Fatalf("CompleteAttempt returned error: %v", err)
	}
	if run.Status != RunStatusFailed || run.FailureCategory != "gitops_dirty_worktree_scope_requires_plan" {
		t.Fatalf("expected dirty scope failure to require a new plan, got %+v", run)
	}
	blockedTask := fake.tasks[task.ID]
	if blockedTask.Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected task blocked, got %+v", blockedTask)
	}
	if !strings.Contains(blockedTask.BlockedReason, "apps/other/src/module.ts") || !strings.Contains(blockedTask.ResumeInstructions, "new plan") {
		t.Fatalf("expected exact dirty path and new-plan instruction, got reason=%q resume=%q", blockedTask.BlockedReason, blockedTask.ResumeInstructions)
	}
	for _, candidate := range store.runs {
		if candidate.ID != "run-a" && candidate.TaskID == task.ID && candidate.Status == RunStatusQueued {
			t.Fatalf("expected no replacement run after out-of-scope dirty path, got %+v", candidate)
		}
	}
}

func TestCreateRemediationFromFindingAddsLikelyDirectoriesToEditScope(t *testing.T) {
	ctx := context.Background()
	fake := &fakeWorkTasks{plans: map[string]projectworkplan.WorkPlan{}, tasks: map[string]projectworkplan.WorkTask{}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	result, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "confirmed-finding-scope",
		FindingStatus:           "confirmed",
		Title:                   "Fix scoped issue",
		Summary:                 "Fix confirmed behavior without broad unsafe edits.",
		ImplementationAgentID:   "codex-worker",
		FilesToRead:             []string{"apps/domain/src/service.ts"},
		FilesToEdit:             []string{"apps/domain/src/service.ts"},
		LikelyFilesAffected:     []string{"apps/domain/src", "apps/domain/test"},
		VerificationRequirement: "Focused regression must pass.",
	})
	if err != nil {
		t.Fatalf("CreateRemediationFromFinding returned error: %v", err)
	}
	if !containsString(result.WorkTask.FilesToEdit, "apps/domain/src") || !containsString(result.WorkTask.FilesToEdit, "apps/domain/test") {
		t.Fatalf("expected likely directories in files_to_edit, got %+v", result.WorkTask.FilesToEdit)
	}
}

func TestClaimNextRunRequeuesExhaustedGitOpsRecoveryAfterRestart(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusVerifying
	task.ClaimedByRunID = "run-a"
	task.EvidenceRefs = []string{"implementation/evidence"}
	task.VerifierResultRefs = []string{"verifier/focused"}
	task.ReviewResultRefs = []string{"review/approved"}
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-a",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  task.Status,
		Status:          RunStatusFailed,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    defaultAutomationMaxRetries,
		SafeSummary:     RunSafeSummaryGitOpsPostTaskRecovery,
		FailureCategory: "gitops_post_task_failed",
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error: %v", err)
	}
	if claimed.Run.ID == "run-a" || claimed.Run.TaskID != task.ID || claimed.Run.Status != RunStatusRunning {
		t.Fatalf("expected replacement implementation run, got %+v", claimed.Run)
	}
	exhausted, err := store.GetRun(ctx, automation.ProjectID, "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if exhausted.FailureCategory != "gitops_recovery_failed_requires_implementation" || !strings.Contains(exhausted.SafeSummary, "gitops_post_task_failed") {
		t.Fatalf("expected exhausted run to be terminalized for implementation, got %+v", exhausted)
	}
	if fake.tasks[task.ID].Status != projectworkplan.WorkTaskStatusInProgress || fake.tasks[task.ID].ClaimedByRunID != claimed.Run.ID {
		t.Fatalf("expected replacement claim to restart task, got task=%+v run=%+v", fake.tasks[task.ID], claimed.Run)
	}
}

func TestQueueReadyDependentAutomationReplacesBlockedWorkTaskRun(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	now := time.Now().UTC()
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:              "run-blocked",
		ProjectID:       automation.ProjectID,
		AutomationID:    automation.ID,
		AgentID:         automation.AgentID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		WorkTaskStatus:  projectworkplan.WorkTaskStatusBlocked,
		Status:          RunStatusBlocked,
		RunnerKind:      RunnerKindCodexCLI,
		FailureCategory: "work_task_blocked",
		SafeSummary:     "external_codex_cli_task_terminal",
		CreatedAt:       now,
		UpdatedAt:       now,
		FinishedAt:      now,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	if err := svc.queueReadyDependentAutomation(ctx, automation, task); err != nil {
		t.Fatalf("queueReadyDependentAutomation returned error: %v", err)
	}
	var queued int
	for _, run := range store.runs {
		if run.TaskID == task.ID && run.Status == RunStatusQueued {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("expected one replacement queued run, got %d runs=%+v", queued, store.runs)
	}
}

func TestClaimNextRunRecoversAbandonedRunningRunAfterRestart(t *testing.T) {
	ctx := context.Background()
	task := readyTask("task-a", "a", []string{"internal/foo.go"})
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = "run-a"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{"task-a": task}}
	store := newTestStore()
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1})
	svc.codexAvailable = func() bool { return false }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	startedBeforeRestart := svc.startedAt.Add(-time.Minute)
	if _, err := store.CreateRun(ctx, AutomationRun{
		ID:             "run-a",
		ProjectID:      automation.ProjectID,
		AutomationID:   automation.ID,
		AgentID:        automation.AgentID,
		PlanID:         task.PlanID,
		TaskID:         task.ID,
		WorkTaskStatus: task.Status,
		Status:         RunStatusRunning,
		RunnerKind:     RunnerKindCodexCLI,
		AttemptCount:   1,
		StartedAt:      startedBeforeRestart,
		CreatedAt:      startedBeforeRestart,
		UpdatedAt:      startedBeforeRestart,
	}); err != nil {
		t.Fatalf("CreateRun returned error: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("ClaimNextRun returned error after restart recovery: %v", err)
	}
	if claimed.Run.ID == "run-a" {
		t.Fatalf("expected abandoned run to be replaced by a fresh queued run, got %+v", claimed.Run)
	}
	if claimed.Run.Status != RunStatusRunning || claimed.Run.TaskID != task.ID {
		t.Fatalf("expected replacement run to be claimed for same task, got %+v", claimed.Run)
	}
	abandoned, err := store.GetRun(ctx, automation.ProjectID, "run-a")
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if abandoned.Status != RunStatusTimeout || abandoned.FailureCategory != "external_runner_interrupted" {
		t.Fatalf("expected abandoned run timeout marker, got %+v", abandoned)
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

func TestReviewGateRunUsesEffectiveAgentPermission(t *testing.T) {
	ctx := context.Background()
	reviewTask := readyTask("automation-review", "automation-review", []string{"internal/review.go"})
	reviewTask.OwnerAgent = "reviewer-1"
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{
		"task-a":            readyTask("task-a", "allowed-task", []string{"internal/foo.go"}),
		"automation-review": reviewTask,
	}}
	resolver := &fakePermissionResolver{metadata: PermissionSnapshotMetadata{AgentID: "agent-1", AllowedRunnerKinds: []string{RunnerKindCodexCLI}}}
	svc := New(newTestStore(), fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal, MaxParallelTasks: 1, PermissionResolver: resolver})
	svc.codexAvailable = func() bool { return true }
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:             "project-1",
		AutomationRef:         "workflow/review-gated",
		Title:                 "Workflow Review Automation",
		Purpose:               "Require independent review before implementation",
		AgentID:               "agent-1",
		PlanID:                "plan-1",
		AllowedTaskRefs:       []string{"allowed-task"},
		RequiredReviewTaskIDs: []string{"automation-review"},
		PermissionRef:         "permission_snapshot:snapshot-1",
		SourceKind:            AutomationSourceWorkflow,
	})
	if err != nil {
		t.Fatalf("CreateAutomation returned error: %v", err)
	}
	if _, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: "task-a", RunnerKind: RunnerKindCodexCLI}); err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}

	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", RunnerKind: RunnerKindCodexCLI}); err == nil {
		t.Fatal("expected reviewer permission mismatch to block claim")
	}
	runs, err := svc.store.ListRuns(ctx, RunFilter{ProjectID: "project-1", AutomationID: automation.ID})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	var reviewRun AutomationRun
	for _, run := range runs {
		if run.TaskID == "automation-review" {
			reviewRun = run
			break
		}
	}
	if reviewRun.ID == "" {
		t.Fatalf("expected queued review run, got %#v", runs)
	}
	updated, err := svc.GetRun(ctx, "project-1", reviewRun.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if updated.Status != RunStatusPolicyDenied || updated.FailureCategory != "invalid_project_automation_input:_permission_agent_mismatch" {
		t.Fatalf("expected reviewer permission mismatch denial, got %#v", updated)
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

func TestValidateAllowedTaskRefAcceptsTaskIDOrTaskRef(t *testing.T) {
	task := readyTask("work_task_123", "task/ref", []string{"internal/foo.go"})
	for _, allowed := range []string{"work_task_123", "task/ref"} {
		automation := Automation{AllowedTaskRefs: []string{allowed}}
		if err := validateAllowedTaskRef(automation, task); err != nil {
			t.Fatalf("expected allowed task ref %q to pass, got %v", allowed, err)
		}
	}
	if err := validateAllowedTaskRef(Automation{AllowedTaskRefs: []string{"other-task"}}, task); err == nil {
		t.Fatal("expected unrelated allowed task ref to fail")
	}
}

type fakeWorkTasks struct {
	mu                   sync.Mutex
	plans                map[string]projectworkplan.WorkPlan
	tasks                map[string]projectworkplan.WorkTask
	blockGetWorkTaskDone chan struct{}
	evidenceRefs         []string
	verifierRefs         []string
	reviewRefs           []string
	knowledgeRefs        []string
	attachReviewErr      error
	completeActions      []projectworkplan.WorkTaskActionInput
}

func (fake *fakeWorkTasks) CreateWorkPlan(_ context.Context, input projectworkplan.CreateWorkPlanInput) (projectworkplan.WorkPlan, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.plans == nil {
		fake.plans = map[string]projectworkplan.WorkPlan{}
	}
	plan := projectworkplan.WorkPlan{
		ID:               "work_plan_" + strings.ReplaceAll(input.PlanRef, ":", "_"),
		ProjectID:        input.ProjectID,
		PlanRef:          input.PlanRef,
		UserRequestRef:   input.UserRequestRef,
		Title:            input.Title,
		GoalSummary:      input.GoalSummary,
		Status:           projectworkplan.WorkPlanStatusPlanned,
		OwnerAgent:       input.OwnerAgent,
		CreatedByRunID:   input.CreatedByRunID,
		TraceID:          input.TraceID,
		ResumeSummary:    input.ResumeSummary,
		IsolationMode:    input.IsolationMode,
		ParallelGroupRef: input.ParallelGroupRef,
		WorkspaceRef:     input.WorkspaceRef,
		GitBaseRef:       input.GitBaseRef,
		GitBranchRef:     input.GitBranchRef,
		GitWorktreeRef:   input.GitWorktreeRef,
	}
	fake.plans[plan.ID] = plan
	return plan, nil
}

func (fake *fakeWorkTasks) ListWorkPlans(_ context.Context, filter projectworkplan.WorkPlanFilter) ([]projectworkplan.WorkPlan, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var out []projectworkplan.WorkPlan
	for _, plan := range fake.plans {
		if plan.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Status != "" && plan.Status != filter.Status {
			continue
		}
		if filter.OwnerAgent != "" && plan.OwnerAgent != filter.OwnerAgent {
			continue
		}
		out = append(out, plan)
	}
	return out, nil
}

func (fake *fakeWorkTasks) CreateWorkTask(_ context.Context, input projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.tasks == nil {
		fake.tasks = map[string]projectworkplan.WorkTask{}
	}
	task := projectworkplan.WorkTask{
		ID:                      "work_task_" + strings.ReplaceAll(input.TaskRef, ":", "_"),
		ProjectID:               input.ProjectID,
		PlanID:                  input.PlanID,
		TaskRef:                 input.TaskRef,
		Title:                   input.Title,
		Description:             input.Description,
		Status:                  input.Status,
		OwnerAgent:              input.OwnerAgent,
		TraceID:                 input.TraceID,
		EvidenceNeeded:          append([]string(nil), input.EvidenceNeeded...),
		FilesToRead:             append([]string(nil), input.FilesToRead...),
		FilesToEdit:             append([]string(nil), input.FilesToEdit...),
		LikelyFilesAffected:     append([]string(nil), input.LikelyFilesAffected...),
		VerificationRequirement: input.VerificationRequirement,
		ExpectedOutput:          input.ExpectedOutput,
		FailureCriteria:         input.FailureCriteria,
		ReviewGate:              input.ReviewGate,
		ResumeInstructions:      input.ResumeInstructions,
		DecompositionQuality:    input.DecompositionQuality,
	}
	fake.tasks[task.ID] = task
	return task, nil
}

func (fake *fakeWorkTasks) UpdateWorkPlanStatus(_ context.Context, input projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	plan, ok := fake.plans[input.PlanID]
	if !ok {
		return projectworkplan.WorkPlan{}, errors.New("not found")
	}
	plan.Status = input.Status
	plan.ResumeSummary = input.ResumeSummary
	fake.plans[plan.ID] = plan
	return plan, nil
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
	mu              sync.Mutex
	automation      map[string]Automation
	runs            map[string]AutomationRun
	batches         map[string]AutomationParallelBatch
	attempts        map[string]AutomationAttempt
	beforeUpdateRun func(AutomationRun)
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
	if store.beforeUpdateRun != nil {
		store.beforeUpdateRun(value)
	}
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

func (fake *fakeWorkTasks) GetWorkTask(ctx context.Context, _ string, taskID string) (projectworkplan.WorkTask, error) {
	if fake.blockGetWorkTaskDone != nil {
		select {
		case <-fake.blockGetWorkTaskDone:
		case <-ctx.Done():
			return projectworkplan.WorkTask{}, ctx.Err()
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
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
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var out []projectworkplan.WorkTask
	for _, task := range fake.tasks {
		if task.ProjectID != filter.ProjectID {
			continue
		}
		if filter.PlanID != "" && task.PlanID != filter.PlanID {
			continue
		}
		if filter.OwnerAgent != "" && task.OwnerAgent != filter.OwnerAgent {
			continue
		}
		switch task.Status {
		case projectworkplan.WorkTaskStatusDone, projectworkplan.WorkTaskStatusFailed, projectworkplan.WorkTaskStatusCancelled, projectworkplan.WorkTaskStatusSuperseded:
			continue
		}
		out = append(out, task)
	}
	return out, nil
}

func (fake *fakeWorkTasks) ClaimWorkTask(_ context.Context, input projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	task.Status = projectworkplan.WorkTaskStatusClaimed
	task.ClaimedByRunID = input.RunID
	fake.tasks[input.TaskID] = task
	return task, nil
}

func (fake *fakeWorkTasks) StartWorkTask(_ context.Context, input projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	task.Status = projectworkplan.WorkTaskStatusInProgress
	task.ClaimedByRunID = input.RunID
	fake.tasks[input.TaskID] = task
	return task, nil
}

func (fake *fakeWorkTasks) UpdateWorkTaskStatus(_ context.Context, input projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	gitOpsRecoveryRerun := task.Status == projectworkplan.WorkTaskStatusVerifying &&
		input.Status == projectworkplan.WorkTaskStatusReady &&
		strings.TrimSpace(input.SafeNextAction) == "gitops_recovery_failed_requeue_implementation" &&
		strings.TrimSpace(input.RunID) != ""
	if input.Status == projectworkplan.WorkTaskStatusReady && task.Status == projectworkplan.WorkTaskStatusVerifying && !gitOpsRecoveryRerun {
		return projectworkplan.WorkTask{}, errors.New("invalid work task transition verifying -> ready")
	}
	if gitOpsRecoveryRerun && task.ClaimedByRunID != "" && strings.TrimSpace(input.RunID) != task.ClaimedByRunID {
		return projectworkplan.WorkTask{}, errors.New("gitops recovery rerun requires current claimed run")
	}
	task.Status = input.Status
	if input.Status == projectworkplan.WorkTaskStatusReady {
		task.ClaimedByRunID = ""
	}
	if input.RunID != "" && strings.TrimSpace(input.SafeNextAction) == "explicit_gitops_post_task_recovery" {
		task.ClaimedByRunID = input.RunID
		if !containsRef(task.AgentRunIDs, input.RunID) {
			task.AgentRunIDs = append(task.AgentRunIDs, input.RunID)
		}
	}
	if input.ResumeInstructions != "" {
		task.ResumeInstructions = input.ResumeInstructions
	}
	if input.Status == projectworkplan.WorkTaskStatusBlocked {
		task.BlockedReason = input.BlockedReason
	}
	fake.tasks[input.TaskID] = task
	return task, nil
}

func (fake *fakeWorkTasks) ExpandWorkTaskScope(_ context.Context, input projectworkplan.ExpandWorkTaskScopeInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	for _, path := range input.FilesToEdit {
		if !containsString(task.FilesToEdit, path) {
			task.FilesToEdit = append(task.FilesToEdit, path)
		}
	}
	if input.ResumeInstructions != "" {
		task.ResumeInstructions = input.ResumeInstructions
	}
	fake.tasks[input.TaskID] = task
	return task, nil
}

func (fake *fakeWorkTasks) AttachEvidence(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.evidenceRefs = append(fake.evidenceRefs, input.Ref)
	if task, ok := fake.tasks[input.TaskID]; ok {
		task.EvidenceRefs = append(task.EvidenceRefs, input.Ref)
		fake.tasks[input.TaskID] = task
	}
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachVerifierResult(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.verifierRefs = append(fake.verifierRefs, input.Ref)
	if task, ok := fake.tasks[input.TaskID]; ok {
		task.VerifierResultRefs = append(task.VerifierResultRefs, input.Ref)
		fake.tasks[input.TaskID] = task
	}
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachReviewResult(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.attachReviewErr != nil {
		return projectworkplan.Attachment{}, fake.attachReviewErr
	}
	fake.reviewRefs = append(fake.reviewRefs, input.Ref)
	if task, ok := fake.tasks[input.TaskID]; ok {
		task.ReviewResultRefs = append(task.ReviewResultRefs, input.Ref)
		fake.tasks[input.TaskID] = task
	}
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachKnowledgeCandidate(_ context.Context, input projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.knowledgeRefs = append(fake.knowledgeRefs, input.Ref)
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) CompleteWorkTask(_ context.Context, input projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.completeActions = append(fake.completeActions, input)
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	if len(input.VerifierResultRefs) > 0 {
		task.VerifierResultRefs = append([]string(nil), input.VerifierResultRefs...)
	}
	if len(input.ReviewResultRefs) > 0 {
		task.ReviewResultRefs = append([]string(nil), input.ReviewResultRefs...)
	}
	if input.ReviewExemptReason != "" {
		task.ReviewExemptReason = input.ReviewExemptReason
		task.ReviewResultRefs = nil
	}
	if len(input.ClaimRefs) > 0 {
		task.ClaimRefs = append([]string(nil), input.ClaimRefs...)
	}
	if len(input.EvidenceRefs) > 0 {
		task.EvidenceRefs = append([]string(nil), input.EvidenceRefs...)
	}
	task.Outcome = input.Outcome
	task.Status = projectworkplan.WorkTaskStatusDone
	fake.tasks[input.TaskID] = task
	return task, nil
}

func (fake *fakeWorkTasks) FailWorkTask(_ context.Context, input projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	if len(input.VerifierResultRefs) > 0 {
		task.VerifierResultRefs = append([]string(nil), input.VerifierResultRefs...)
	}
	if len(input.ReviewResultRefs) > 0 {
		task.ReviewResultRefs = append([]string(nil), input.ReviewResultRefs...)
	}
	if len(input.ClaimRefs) > 0 {
		task.ClaimRefs = append([]string(nil), input.ClaimRefs...)
	}
	if len(input.EvidenceRefs) > 0 {
		task.EvidenceRefs = append([]string(nil), input.EvidenceRefs...)
	}
	task.Outcome = input.Outcome
	task.Status = projectworkplan.WorkTaskStatusFailed
	fake.tasks[input.TaskID] = task
	return task, nil
}

func (fake *fakeWorkTasks) BlockWorkTask(_ context.Context, input projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	task.Status = projectworkplan.WorkTaskStatusBlocked
	task.BlockedReason = input.BlockedReason
	task.ResumeInstructions = input.ResumeInstructions
	if input.TraceID != "" {
		task.TraceID = input.TraceID
	}
	fake.tasks[input.TaskID] = task
	return task, nil
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
