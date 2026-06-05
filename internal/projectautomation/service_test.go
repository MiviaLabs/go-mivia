package projectautomation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
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
	if result.Automation.TriggerKind != TriggerKindAutomatic || result.Automation.Status != AutomationStatusEnabled {
		t.Fatalf("unexpected remediation automation: %#v", result.Automation)
	}
	if result.WorkPlan.GitBaseRef != "main" || result.WorkPlan.GitBranchRef != "fix-MASS-0000-readme-structure-entry" || result.WorkPlan.GitWorktreeRef != "fix-MASS-0000-readme-structure-entry" {
		t.Fatalf("expected project-specific git refs, got base=%q branch=%q worktree=%q", result.WorkPlan.GitBaseRef, result.WorkPlan.GitBranchRef, result.WorkPlan.GitWorktreeRef)
	}
	wantEvidence := []string{"confirmed-finding-finding-review-1", "review-confirmed"}
	if strings.Join(result.WorkTask.EvidenceNeeded, ",") != strings.Join(wantEvidence, ",") {
		t.Fatalf("expected worker-safe evidence refs %v, got %v", wantEvidence, result.WorkTask.EvidenceNeeded)
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
	if run.Status != RunStatusVerifying || run.WorkTaskStatus != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected pre-verifier run to retain ready/verifying, got %#v", run)
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
	if persisted.WorkTaskStatus != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected persisted work task status, got %q", persisted.WorkTaskStatus)
	}
	if persisted.SafeSummary != "external_codex_cli_completed_verification_required" {
		t.Fatalf("unexpected summary: %q", persisted.SafeSummary)
	}
}

func TestListRunsFiltersPersistedAutomationMetadataOnly(t *testing.T) {
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
	if len(runs) != 0 {
		t.Fatalf("expected no completed persisted runs, got %#v", runs)
	}
	runs, err = svc.ListRuns(ctx, RunFilter{ProjectID: automation.ProjectID, Status: RunStatusVerifying})
	if err != nil {
		t.Fatalf("ListRuns returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusVerifying || runs[0].WorkTaskStatus != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected persisted verifying/ready run, got %#v", runs)
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
	plans                map[string]projectworkplan.WorkPlan
	tasks                map[string]projectworkplan.WorkTask
	blockGetWorkTaskDone chan struct{}
	evidenceRefs         []string
	verifierRefs         []string
	reviewRefs           []string
	knowledgeRefs        []string
}

func (fake *fakeWorkTasks) CreateWorkPlan(_ context.Context, input projectworkplan.CreateWorkPlanInput) (projectworkplan.WorkPlan, error) {
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

func (fake *fakeWorkTasks) CreateWorkTask(_ context.Context, input projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error) {
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

func (fake *fakeWorkTasks) GetWorkTask(ctx context.Context, _ string, taskID string) (projectworkplan.WorkTask, error) {
	if fake.blockGetWorkTaskDone != nil {
		select {
		case <-fake.blockGetWorkTaskDone:
		case <-ctx.Done():
			return projectworkplan.WorkTask{}, ctx.Err()
		}
	}
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
		if filter.OwnerAgent != "" && task.OwnerAgent != filter.OwnerAgent {
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

func (fake *fakeWorkTasks) UpdateWorkTaskStatus(_ context.Context, input projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error) {
	task, ok := fake.tasks[input.TaskID]
	if !ok {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	task.Status = input.Status
	fake.tasks[input.TaskID] = task
	return task, nil
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
