package projectworkplan_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestServiceCreateWorkPlanValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()

	plan, err := createPlan(ctx, t, svc, "plan-1")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.ProjectID != "project-1" || plan.Status != projectworkplan.WorkPlanStatusPlanned {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	isolated, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:        "project-1",
		PlanRef:          "plan-isolated",
		Title:            "Isolated plan",
		GoalSummary:      "Plan uses safe metadata refs for parallel execution.",
		ParallelGroupRef: "parallel/group-1",
		WorkspaceRef:     "workspace/project-1",
		GitBaseRef:       "main",
		GitBranchRef:     "codex/work-plan-1",
		GitWorktreeRef:   "worktree/work-plan-1",
	})
	if err != nil {
		t.Fatalf("create isolated plan: %v", err)
	}
	if isolated.IsolationMode != projectworkplan.WorkPlanIsolationDedicatedWorktree || isolated.GitWorktreeRef != "worktree/work-plan-1" {
		t.Fatalf("expected dedicated worktree metadata, got %+v", isolated)
	}

	if _, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{PlanRef: "bad", Title: "Title", GoalSummary: "Goal"}); err == nil {
		t.Fatal("expected empty project id to fail")
	}
	if _, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{ProjectID: "project-1", PlanRef: "plan-absolute", Title: "Title", GoalSummary: "Goal", GitWorktreeRef: "/tmp/worktree"}); err == nil {
		t.Fatal("expected unsafe worktree ref to fail")
	}
	if _, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{ProjectID: "project-1", PlanRef: "raw_prompt", Title: "Title", GoalSummary: "Goal"}); err == nil {
		t.Fatal("expected raw prompt marker to fail")
	}
	if _, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{ProjectID: "project-1", PlanRef: "plan-secret", Title: "OPENAI_API_KEY=bad", GoalSummary: "Goal"}); err == nil {
		t.Fatal("expected secret marker to fail")
	}
}

func TestServiceUpdateWorkPlanStatusAcceptsCorrelationMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-status")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	updated, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         plan.ID,
		Status:         projectworkplan.WorkPlanStatusActive,
		SafeNextAction: "get next ready task",
		RunID:          "agent_run_status",
		TraceID:        "trace_status",
	})
	if err != nil {
		t.Fatalf("update status with correlation metadata: %v", err)
	}
	if updated.Status != projectworkplan.WorkPlanStatusActive || updated.TraceID != "trace_status" {
		t.Fatalf("expected active status with trace metadata, got %+v", updated)
	}
	if _, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         updated.ID,
		Status:         projectworkplan.WorkPlanStatusDone,
		SafeNextAction: "raw_prompt",
	}); err == nil {
		t.Fatal("expected unsafe safe_next_action to fail")
	}
}

func TestServiceMCPInvalidArgumentDiagnostics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()

	cases := []struct {
		name     string
		tool     string
		body     string
		contains string
	}{
		{
			name:     "unknown task field",
			tool:     "projects.work_tasks.create",
			body:     `{"id":"project-1","plan_id":"plan-1","task_ref":"task-1","title":"Task","evidence_needed":["source"],"verification_requirement":"focused tests","resume_instructions":"continue","raw_prompt":"secret"}`,
			contains: "field raw_prompt is not accepted for work task",
		},
		{
			name:     "bad task field type",
			tool:     "projects.work_tasks.create",
			body:     `{"id":"project-1","plan_id":"plan-1","task_ref":"task-1","title":"Task","evidence_needed":"source","verification_requirement":"focused tests","resume_instructions":"continue"}`,
			contains: "evidence_needed has invalid type for work task",
		},
		{
			name:     "unknown attach field",
			tool:     "projects.work_tasks.attach_evidence",
			body:     `{"id":"project-1","task_id":"task-1","evidence_ref":"evidence-1","root":"/home/mac/project"}`,
			contains: "field root is not accepted for work task",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.CallWorkPlanTool(ctx, tc.tool, json.RawMessage(tc.body))
			if err == nil {
				t.Fatal("expected invalid argument error")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("expected %q in error, got %q", tc.contains, err.Error())
			}
			for _, forbidden := range []string{"secret", "/home/mac/project"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked forbidden value %q: %q", forbidden, err.Error())
				}
			}
		})
	}
}

func TestServiceCreateWorkTaskValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-1")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-1"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != projectworkplan.WorkTaskStatusReady || task.DecompositionQuality != projectworkplan.DecompositionReady {
		t.Fatalf("unexpected task readiness: %+v", task)
	}

	rich := readyTaskInput(plan.ID, "task-rich")
	rich.Description = "Read data/dashboard-operations-redesign-plan-2026-06-04.md before editing internal/dashboard/httpapi/assets/app.js."
	rich.FailureCriteria = "Block if metadata would expose credentials, roots, paths, raw prompts, source dumps, or provider payloads."
	rich.ContextPackRefs = []string{"context-pack:manifest:68c3ee2ad1556459"}
	rich.FilesToRead = []string{"data/dashboard-operations-redesign-plan-2026-06-04.md"}
	rich.FilesToEdit = []string{"internal/dashboard/httpapi/assets/app.js"}
	rich.LikelyFilesAffected = []string{"tmp/mivia-workplan-smoke"}
	rich.ReviewGate = "independent review required before completion"
	rich.Status = projectworkplan.WorkTaskStatusReady
	rich.RunID = "agent_run_1"
	createdRich, err := svc.CreateWorkTask(ctx, rich)
	if err != nil {
		t.Fatalf("create rich task: %v", err)
	}
	if createdRich.Status != projectworkplan.WorkTaskStatusReady || createdRich.DecompositionQuality != projectworkplan.DecompositionReady {
		t.Fatalf("expected rich task to be ready, got %+v", createdRich)
	}
	if len(createdRich.AgentRunIDs) != 1 || createdRich.AgentRunIDs[0] != "agent_run_1" {
		t.Fatalf("expected run linkage on created task, got %+v", createdRich.AgentRunIDs)
	}
	if got := createdRich.FilesToRead; len(got) != 1 || got[0] != "data/dashboard-operations-redesign-plan-2026-06-04.md" {
		t.Fatalf("expected files_to_read to persist, got %+v", got)
	}
	if got := createdRich.FilesToEdit; len(got) != 1 || got[0] != "internal/dashboard/httpapi/assets/app.js" {
		t.Fatalf("expected files_to_edit to persist, got %+v", got)
	}
	if createdRich.ReviewGate != "independent review required before completion" {
		t.Fatalf("expected review gate to persist, got %q", createdRich.ReviewGate)
	}

	terminal := readyTaskInput(plan.ID, "task-terminal")
	terminal.Status = projectworkplan.WorkTaskStatusDone
	if _, err := svc.CreateWorkTask(ctx, terminal); err == nil {
		t.Fatal("expected terminal create status to fail")
	}

	absolute := readyTaskInput(plan.ID, "task-absolute")
	absolute.LikelyFilesAffected = []string{"/etc/passwd"}
	if _, err := svc.CreateWorkTask(ctx, absolute); err == nil {
		t.Fatal("expected absolute path to fail")
	}

	traversal := readyTaskInput(plan.ID, "task-traversal")
	traversal.LikelyFilesAffected = []string{"../secret.txt"}
	if _, err := svc.CreateWorkTask(ctx, traversal); err == nil {
		t.Fatal("expected traversal path to fail")
	}

	unsafeText := readyTaskInput(plan.ID, "task-unsafe")
	unsafeText.Description = "contains raw_prompt marker"
	if _, err := svc.CreateWorkTask(ctx, unsafeText); err == nil {
		t.Fatal("expected unsafe text to fail")
	}

	dateRef := readyTaskInput(plan.ID, "task-20260603-125429")
	if _, err := svc.CreateWorkTask(ctx, dateRef); err != nil {
		t.Fatalf("expected generated date-like ref to be accepted: %v", err)
	}
}

func TestServiceCreateWorkTaskAllowsLongSafeResumeInstructions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-long-create-resume")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	input := readyTaskInput(plan.ID, "task-long-create-resume")
	longResume := strings.Repeat("resume safely. ", 1500) + "done"
	input.ResumeInstructions = longResume

	task, err := svc.CreateWorkTask(ctx, input)
	if err != nil {
		t.Fatalf("create task with long resume instructions: %v", err)
	}
	if task.ResumeInstructions != longResume {
		t.Fatalf("expected long resume instructions to persist, got length %d", len(task.ResumeInstructions))
	}
}

func TestServiceTaskTransitions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-1")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-1"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	plannedInput := readyTaskInput(plan.ID, "task-planned")
	plannedInput.Status = projectworkplan.WorkTaskStatusPlanned
	planned, err := svc.CreateWorkTask(ctx, plannedInput)
	if err != nil {
		t.Fatalf("create planned task: %v", err)
	}
	ready, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: planned.ID},
		Status:              projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		t.Fatalf("planned to ready: %v", err)
	}
	if ready.Status != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("expected ready, got %s", ready.Status)
	}

	claimed, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1"})
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if claimed.Status != projectworkplan.WorkTaskStatusClaimed {
		t.Fatalf("expected claimed, got %s", claimed.Status)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-2"}); err == nil {
		t.Fatal("expected claim by another run to fail")
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, VerifierResultRefs: []string{"verifier-1"}}); err == nil {
		t.Fatal("expected in_progress -> done to fail")
	}
	released, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1"})
	if err != nil {
		t.Fatalf("release in-progress task: %v", err)
	}
	if released.Status != projectworkplan.WorkTaskStatusReady || released.ClaimedByRunID != "" {
		t.Fatalf("expected released in-progress task to be ready and unclaimed, got %#v", released)
	}

	releaseTarget, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-release"))
	if err != nil {
		t.Fatalf("create release target: %v", err)
	}
	releasedReady, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: releaseTarget.ID})
	if err != nil {
		t.Fatalf("release ready task should be idempotent: %v", err)
	}
	if releasedReady.Status != projectworkplan.WorkTaskStatusReady || releasedReady.ClaimedByRunID != "" {
		t.Fatalf("expected idempotent release to keep task ready and unclaimed, got %#v", releasedReady)
	}

	blockTarget, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-block"))
	if err != nil {
		t.Fatalf("create block target: %v", err)
	}
	if _, err := svc.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: blockTarget.ID, BlockedReason: "missing dependency"}); err == nil {
		t.Fatal("expected block without resume instructions to fail")
	}
}

func TestServiceIntentionalResumeInstructionUpdatesAllowLongSafeTextAndRejectUnsafeText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-long-update-resume")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-long-update-resume"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	longResume := strings.Repeat("continue safely. ", 1500) + "done"
	blocked, err := svc.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             task.ID,
		BlockedReason:      "waiting for a safe recovery handoff",
		ResumeInstructions: longResume,
	})
	if err != nil {
		t.Fatalf("block task with long resume instructions: %v", err)
	}
	if blocked.Status != projectworkplan.WorkTaskStatusBlocked || blocked.ResumeInstructions != longResume {
		t.Fatalf("expected blocked task with persisted long resume instructions, got length %d", len(blocked.ResumeInstructions))
	}

	if _, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID}); err != nil {
		t.Fatalf("release blocked task: %v", err)
	}
	if _, err := svc.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             task.ID,
		BlockedReason:      "waiting for redacted recovery handoff",
		ResumeInstructions: "retry after token=secret is removed",
	}); err == nil || !strings.Contains(err.Error(), "resume_instructions contains unsafe content") {
		t.Fatalf("expected unsafe resume update to fail deterministically, got %v", err)
	}
}

func TestServiceClaimAndReleaseIgnoreStoredLongResumeInstructions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemoryStore()
	svc := projectworkplan.New(mem)
	plan, err := createPlan(ctx, t, svc, "plan-long-resume")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-long-resume"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.ResumeInstructions = strings.Repeat("x", projectworkplan.MaxResumeInstructionsLength+25)
	if _, err := mem.UpdateWorkTask(ctx, task); err != nil {
		t.Fatalf("store long resume task: %v", err)
	}

	claimed, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-long-resume"})
	if err != nil {
		t.Fatalf("claim task with stored long resume: %v", err)
	}
	if claimed.Status != projectworkplan.WorkTaskStatusClaimed {
		t.Fatalf("expected claimed task, got %#v", claimed)
	}
	released, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-long-resume"})
	if err != nil {
		t.Fatalf("release task with stored long resume: %v", err)
	}
	if released.Status != projectworkplan.WorkTaskStatusReady || released.ClaimedByRunID != "" {
		t.Fatalf("expected ready unclaimed task, got %#v", released)
	}
	if len(released.ResumeInstructions) <= projectworkplan.MaxResumeInstructionsLength {
		t.Fatalf("expected stored legacy resume instructions to be preserved, got length %d", len(released.ResumeInstructions))
	}
}

func TestServiceReleaseWorkTaskIsNoopForLaterStates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-release-stale")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	verifying, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-verifying"))
	if err != nil {
		t.Fatalf("create verifying task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: verifying.ID, RunID: "run-verifying"}); err != nil {
		t.Fatalf("claim verifying task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: verifying.ID, RunID: "run-verifying"}); err != nil {
		t.Fatalf("start verifying task: %v", err)
	}
	verifying, err = svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: verifying.ID, RunID: "run-verifying"},
		Status:              projectworkplan.WorkTaskStatusVerifying,
	})
	if err != nil {
		t.Fatalf("move task to verifying: %v", err)
	}
	releasedVerifying, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: verifying.ID, RunID: "run-verifying"})
	if err != nil {
		t.Fatalf("stale release from verifying should be noop: %v", err)
	}
	if releasedVerifying.Status != projectworkplan.WorkTaskStatusVerifying {
		t.Fatalf("expected stale release to preserve verifying status, got %#v", releasedVerifying)
	}

	failed, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-failed"))
	if err != nil {
		t.Fatalf("create failed task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: failed.ID, RunID: "run-failed"}); err != nil {
		t.Fatalf("claim failed task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: failed.ID, RunID: "run-failed"}); err != nil {
		t.Fatalf("start failed task: %v", err)
	}
	failed, err = svc.FailWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: failed.ID, RunID: "run-failed"})
	if err != nil {
		t.Fatalf("fail task: %v", err)
	}
	releasedFailed, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: failed.ID, RunID: "run-failed"})
	if err != nil {
		t.Fatalf("stale release from failed should be noop: %v", err)
	}
	if releasedFailed.Status != projectworkplan.WorkTaskStatusFailed {
		t.Fatalf("expected stale release to preserve failed status, got %#v", releasedFailed)
	}
}

func TestServiceAllowsGitOpsRecoveryRerunFromVerifying(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-gitops-rerun")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-gitops-rerun"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"},
		Status:              projectworkplan.WorkTaskStatusVerifying,
	}); err != nil {
		t.Fatalf("move task to verifying: %v", err)
	}
	if _, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:      "project-1",
			TaskID:         task.ID,
			RunID:          "wrong-run",
			SafeNextAction: "gitops_recovery_failed_requeue_implementation",
		},
		Status: projectworkplan.WorkTaskStatusReady,
	}); err == nil {
		t.Fatal("expected gitops rerun reset with wrong run to fail")
	}
	genericRelease, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"},
		Status:              projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		t.Fatalf("generic verifying release should remain stale no-op, got error: %v", err)
	}
	if genericRelease.Status != projectworkplan.WorkTaskStatusVerifying {
		t.Fatalf("expected generic verifying release to preserve status, got %#v", genericRelease)
	}
	reset, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:      "project-1",
			TaskID:         task.ID,
			RunID:          "run-gitops",
			SafeNextAction: "gitops_recovery_failed_requeue_implementation",
		},
		Status: projectworkplan.WorkTaskStatusReady,
	})
	if err != nil {
		t.Fatalf("gitops rerun reset returned error: %v", err)
	}
	if reset.Status != projectworkplan.WorkTaskStatusReady || reset.ClaimedByRunID != "" {
		t.Fatalf("expected task ready and unclaimed, got %#v", reset)
	}
}

func TestServiceUpdateWorkPlanStatusNotifiesHandler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-status-hook")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	handler := &statusChangeRecorder{}
	svc.SetStatusChangeHandler(handler)

	updated, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{ProjectID: "project-1", PlanID: plan.ID, Status: projectworkplan.WorkPlanStatusActive, Outcome: "plan is ready for implementation"})
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if updated.Status != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("expected active status, got %q", updated.Status)
	}
	if updated.Outcome != "plan is ready for implementation" {
		t.Fatalf("expected outcome to persist, got %q", updated.Outcome)
	}
	if len(handler.events) != 1 {
		t.Fatalf("expected one status change event, got %d", len(handler.events))
	}
	event := handler.events[0]
	if event.PlanID != plan.ID || event.OldStatus != projectworkplan.WorkPlanStatusPlanned || event.NewStatus != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestServiceStartTaskAcceptsContextPackRefs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-1")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-context-start"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1"}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	started, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-1", ContextPackRefs: []string{"context-pack:manifest:68c3ee2ad1556459"}})
	if err != nil {
		t.Fatalf("start task with context refs: %v", err)
	}
	if !contains(started.ContextPackRefs, "context-pack:manifest:68c3ee2ad1556459") {
		t.Fatalf("expected context ref to be preserved, got %#v", started.ContextPackRefs)
	}
}

func TestServiceCallWorkPlanToolUpdateStatusAcceptsOutcome(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-mcp-outcome")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	result, err := svc.CallWorkPlanTool(ctx, "projects.work_plans.update_status", json.RawMessage(`{"id":"project-1","plan_id":"`+plan.ID+`","status":"active","safe_next_action":"create bounded tasks","outcome":"implementation completed"}`))
	if err != nil {
		t.Fatalf("update status via MCP adapter: %v", err)
	}
	updated, ok := result.(projectworkplan.WorkPlan)
	if !ok {
		t.Fatalf("expected work plan result, got %T", result)
	}
	if updated.Outcome != "implementation completed" {
		t.Fatalf("expected outcome from MCP adapter, got %+v", updated)
	}
}

func TestServiceCallWorkPlanToolUpdateStatusCarriesSafeNextActionForGitOpsRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-mcp-gitops-rerun")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-mcp-gitops-rerun"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	callWorkPlanTool(t, svc, "projects.work_tasks.claim", map[string]any{"id": "project-1", "task_id": task.ID, "run_id": "run-gitops"})
	callWorkPlanTool(t, svc, "projects.work_tasks.start", map[string]any{"id": "project-1", "task_id": task.ID, "run_id": "run-gitops"})
	if _, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-gitops"},
		Status:              projectworkplan.WorkTaskStatusVerifying,
	}); err != nil {
		t.Fatalf("move task to verifying: %v", err)
	}

	reset := callWorkPlanTool(t, svc, "projects.work_tasks.update_status", map[string]any{
		"id":               "project-1",
		"task_id":          task.ID,
		"status":           "ready",
		"run_id":           "run-gitops",
		"safe_next_action": "gitops_recovery_failed_requeue_implementation",
	}).(projectworkplan.WorkTask)
	if reset.Status != projectworkplan.WorkTaskStatusReady || reset.ClaimedByRunID != "" {
		t.Fatalf("expected MCP gitops recovery to return ready unclaimed task, got %#v", reset)
	}
}

type statusChangeRecorder struct {
	events []projectworkplan.WorkPlanStatusChange
}

func (recorder *statusChangeRecorder) HandleWorkPlanStatusChanged(_ context.Context, event projectworkplan.WorkPlanStatusChange) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func TestServiceOpenAndNextIgnoreTerminalPlanTasks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	activePlan, err := createPlan(ctx, t, svc, "plan-active")
	if err != nil {
		t.Fatalf("create active plan: %v", err)
	}
	cancelledPlan, err := createPlan(ctx, t, svc, "plan-cancelled")
	if err != nil {
		t.Fatalf("create cancelled plan: %v", err)
	}
	if _, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{ProjectID: "project-1", PlanID: cancelledPlan.ID, Status: projectworkplan.WorkPlanStatusCancelled}); err != nil {
		t.Fatalf("cancel plan: %v", err)
	}
	activeTask, err := svc.CreateWorkTask(ctx, readyTaskInput(activePlan.ID, "task-active"))
	if err != nil {
		t.Fatalf("create active task: %v", err)
	}
	cancelledTask, err := svc.CreateWorkTask(ctx, readyTaskInput(cancelledPlan.ID, "task-cancelled-plan"))
	if err != nil {
		t.Fatalf("create cancelled-plan task: %v", err)
	}

	open, err := svc.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if !hasTask(open, activeTask.ID) || hasTask(open, cancelledTask.ID) {
		t.Fatalf("expected only active-plan task open, got %+v", open)
	}
	next, err := svc.GetNextWorkTask(ctx, projectworkplan.GetNextWorkTaskInput{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	if !next.Found || next.Task.ID != activeTask.ID {
		t.Fatalf("expected active-plan task as next, got %+v", next)
	}
}

func TestServiceMCPAdapterAcceptsDocumentedActionFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-1")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-mcp-actions"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	callWorkPlanTool(t, svc, "projects.work_tasks.claim", map[string]any{"id": "project-1", "task_id": task.ID, "owner_agent": "worker-1", "run_id": "run-1"})
	callWorkPlanTool(t, svc, "projects.work_tasks.start", map[string]any{"id": "project-1", "task_id": task.ID, "run_id": "run-1", "context_pack_refs": []string{"context-pack:manifest:68c3ee2ad1556459"}})
	callWorkPlanTool(t, svc, "projects.work_tasks.attach_verifier_result", map[string]any{"id": "project-1", "task_id": task.ID, "verifier_result_ref": "verifier:focused-test", "status": "passed", "attached_by_run_id": "run-1"})
	callWorkPlanTool(t, svc, "projects.work_tasks.attach_review_result", map[string]any{"id": "project-1", "task_id": task.ID, "review_result_ref": "review:focused-test", "status": "passed", "attached_by_run_id": "run-review-1"})
	completed := callWorkPlanTool(t, svc, "projects.work_tasks.complete", map[string]any{"id": "project-1", "task_id": task.ID, "outcome": "focused verifier passed", "safe_next_action": "get next task", "run_id": "run-1", "evidence_refs": []string{"evidence:focused-test"}, "claim_refs": []string{"claim:focused-test"}, "knowledge_candidate_refs": []string{"knowledge:focused-test"}, "verifier_result_refs": []string{"verifier:focused-test"}, "review_result_refs": []string{"review:focused-test"}}).(projectworkplan.WorkTask)
	if completed.Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected done task, got %#v", completed)
	}
	if !contains(completed.EvidenceRefs, "evidence:focused-test") || !contains(completed.ClaimRefs, "claim:focused-test") || !contains(completed.KnowledgeCandidateRefs, "knowledge:focused-test") || !contains(completed.ReviewResultRefs, "review:focused-test") {
		t.Fatalf("expected documented action refs to be preserved, got evidence=%#v claims=%#v knowledge=%#v reviews=%#v", completed.EvidenceRefs, completed.ClaimRefs, completed.KnowledgeCandidateRefs, completed.ReviewResultRefs)
	}
}

func TestServiceCompletionRequiresIndependentReviewOrExemption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newService()
	plan, err := createPlan(ctx, t, svc, "plan-review")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-review"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-impl"}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-impl"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := svc.AttachVerifierResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "verifier:review-gate", AttachedByRunID: "run-impl"}); err != nil {
		t.Fatalf("attach verifier: %v", err)
	}
	if _, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, Outcome: "verified but not reviewed", VerifierResultRefs: []string{"verifier:review-gate"}}); err == nil {
		t.Fatal("expected completion without review to fail")
	}
	if _, err := svc.AttachReviewResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "review:self", AttachedByRunID: "run-impl"}); err == nil {
		t.Fatal("expected self-review to fail")
	}
	if _, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, Outcome: "attempted completion with unattached self-review ref", SafeNextAction: "get next task", VerifierResultRefs: []string{"verifier:review-gate"}, ReviewResultRefs: []string{"review:self"}}); err == nil {
		t.Fatal("expected completion with unattached review ref to fail")
	}
	if _, err := svc.AttachReviewResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "review:independent", AttachedByRunID: "run-review"}); err != nil {
		t.Fatalf("attach independent review: %v", err)
	}
	completed, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, Outcome: "verified and independently reviewed", SafeNextAction: "get next task", VerifierResultRefs: []string{"verifier:review-gate"}, ReviewResultRefs: []string{"review:independent"}})
	if err != nil {
		t.Fatalf("complete with review: %v", err)
	}
	if completed.Status != projectworkplan.WorkTaskStatusDone || !contains(completed.ReviewResultRefs, "review:independent") {
		t.Fatalf("expected reviewed done task, got %+v", completed)
	}

	exemptTask, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-review-exempt"))
	if err != nil {
		t.Fatalf("create exempt task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: exemptTask.ID, RunID: "run-exempt"}); err != nil {
		t.Fatalf("claim exempt task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: exemptTask.ID, RunID: "run-exempt"}); err != nil {
		t.Fatalf("start exempt task: %v", err)
	}
	if _, err := svc.AttachVerifierResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: exemptTask.ID, Ref: "verifier:exempt", AttachedByRunID: "run-exempt"}); err != nil {
		t.Fatalf("attach exempt verifier: %v", err)
	}
	exempt, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: exemptTask.ID, Outcome: "no reusable project knowledge; mechanical metadata-only change", SafeNextAction: "get next task", VerifierResultRefs: []string{"verifier:exempt"}, ReviewExemptReason: "mechanical metadata-only docs typo"})
	if err != nil {
		t.Fatalf("complete exempt task: %v", err)
	}
	if exempt.ReviewExemptReason == "" {
		t.Fatalf("expected review exemption reason, got %+v", exempt)
	}

	staleReviewTask, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-stale-review-exempt"))
	if err != nil {
		t.Fatalf("create stale review exempt task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: staleReviewTask.ID, RunID: "run-stale-exempt"}); err != nil {
		t.Fatalf("claim stale review exempt task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: staleReviewTask.ID, RunID: "run-stale-exempt"}); err != nil {
		t.Fatalf("start stale review exempt task: %v", err)
	}
	if _, err := svc.AttachVerifierResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: staleReviewTask.ID, Ref: "verifier:stale-exempt", AttachedByRunID: "run-stale-exempt"}); err != nil {
		t.Fatalf("attach stale exempt verifier: %v", err)
	}
	staleExempt, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             staleReviewTask.ID,
		Outcome:            "metadata-only closeout with stale review ref ignored",
		SafeNextAction:     "get next task",
		VerifierResultRefs: []string{"verifier:stale-exempt"},
		ReviewResultRefs:   []string{"review:not-attached"},
		ReviewExemptReason: "metadata-only automation task; no repository writes require secondary review",
	})
	if err != nil {
		t.Fatalf("complete with exemption and stale review ref: %v", err)
	}
	if len(staleExempt.ReviewResultRefs) != 0 || staleExempt.ReviewExemptReason == "" {
		t.Fatalf("expected exemption to clear stale review refs, got %+v", staleExempt)
	}
}

func TestServiceGetNextWorkTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := store.NewMemoryStore()
	svc := projectworkplan.New(mem)
	plan, err := createPlan(ctx, t, svc, "plan-1")
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	dep, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-dep"))
	if err != nil {
		t.Fatalf("create dep: %v", err)
	}
	dep.Status = projectworkplan.WorkTaskStatusDone
	dep.ClaimedByRunID = "run-completed"
	dep.VerifierResultRefs = []string{"verifier-dep"}
	if _, err := mem.UpdateWorkTask(ctx, dep); err != nil {
		t.Fatalf("mark dep done: %v", err)
	}
	owned := readyTaskInput(plan.ID, "task-owned")
	owned.OwnerAgent = "worker-1"
	owned.DependencyTaskIDs = []string{dep.ID}
	if _, err := svc.CreateWorkTask(ctx, owned); err != nil {
		t.Fatalf("create owned task: %v", err)
	}
	blocked := readyTaskInput(plan.ID, "task-incomplete")
	blocked.DependencyTaskIDs = []string{"missing-dep"}
	if _, err := svc.CreateWorkTask(ctx, blocked); err != nil {
		t.Fatalf("create incomplete task: %v", err)
	}

	next, err := svc.GetNextWorkTask(ctx, projectworkplan.GetNextWorkTaskInput{ProjectID: "project-1", OwnerAgent: "worker-1"})
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	if !next.Found || next.Task.TaskRef != "task-owned" || next.RequiredVerification == "" {
		t.Fatalf("unexpected next result: %+v", next)
	}

	empty, err := svc.GetNextWorkTask(ctx, projectworkplan.GetNextWorkTaskInput{ProjectID: "project-1", OwnerAgent: "other-worker"})
	if err != nil {
		t.Fatalf("get empty next: %v", err)
	}
	if empty.Found || empty.Reason == "" {
		t.Fatalf("expected structured empty result: %+v", empty)
	}
	if empty.ClaimedCount != 0 {
		t.Fatalf("completed claimed task must not count as active claimed work: %+v", empty)
	}
}

func newService() *projectworkplan.Service {
	return projectworkplan.New(store.NewMemoryStore())
}

func createPlan(ctx context.Context, t *testing.T, svc *projectworkplan.Service, ref string) (projectworkplan.WorkPlan, error) {
	t.Helper()
	return svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     ref,
		Title:       "Implement project work plan metadata",
		GoalSummary: "Create metadata-only work plan records and task state.",
	})
}

func readyTaskInput(planID, ref string) projectworkplan.CreateWorkTaskInput {
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  planID,
		TaskRef:                 ref,
		Title:                   "Create scoped metadata behavior",
		Description:             "Safe metadata-only task description.",
		EvidenceNeeded:          []string{"current source and focused tests"},
		ContextPackRefs:         []string{"context-pack-1"},
		LikelyFilesAffected:     []string{"internal/projectworkplan/model.go"},
		VerificationRequirement: "Focused unit test coverage for state behavior.",
		ExpectedOutput:          "Changed code and tests.",
		FailureCriteria:         "Stop if source scope changes.",
		ResumeInstructions:      "Resume from the next failing assertion.",
		KnowledgeCandidateRefs:  []string{"knowledge-candidate-1"},
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasTask(tasks []projectworkplan.WorkTask, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}

func callWorkPlanTool(t *testing.T, svc *projectworkplan.Service, name string, args map[string]any) any {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	out, err := svc.CallWorkPlanTool(context.Background(), name, encoded)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return out
}
