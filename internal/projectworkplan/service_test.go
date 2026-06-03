package projectworkplan_test

import (
	"context"
	"encoding/json"
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
	rich.Description = "Metadata-only task; no raw prompts, completions, source dumps, raw stderr, provider payloads, secrets, roots, or PII."
	rich.FailureCriteria = "Block if metadata would expose credentials, roots, paths, raw prompts, source dumps, or provider payloads."
	rich.ContextPackRefs = []string{"context-pack:manifest:68c3ee2ad1556459"}
	rich.LikelyFilesAffected = []string{"tmp/mivia-workplan-smoke"}
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

	releaseTarget, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-release"))
	if err != nil {
		t.Fatalf("create release target: %v", err)
	}
	if _, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: releaseTarget.ID}); err == nil {
		t.Fatal("expected release from ready to fail")
	}

	blockTarget, err := svc.CreateWorkTask(ctx, readyTaskInput(plan.ID, "task-block"))
	if err != nil {
		t.Fatalf("create block target: %v", err)
	}
	if _, err := svc.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: blockTarget.ID, BlockedReason: "missing dependency"}); err == nil {
		t.Fatal("expected block without resume instructions to fail")
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
	completed := callWorkPlanTool(t, svc, "projects.work_tasks.complete", map[string]any{"id": "project-1", "task_id": task.ID, "outcome": "focused verifier passed", "safe_next_action": "get next task", "run_id": "run-1", "evidence_refs": []string{"evidence:focused-test"}, "claim_refs": []string{"claim:focused-test"}, "knowledge_candidate_refs": []string{"knowledge:focused-test"}, "verifier_result_refs": []string{"verifier:focused-test"}}).(projectworkplan.WorkTask)
	if completed.Status != projectworkplan.WorkTaskStatusDone {
		t.Fatalf("expected done task, got %#v", completed)
	}
	if !contains(completed.EvidenceRefs, "evidence:focused-test") || !contains(completed.ClaimRefs, "claim:focused-test") || !contains(completed.KnowledgeCandidateRefs, "knowledge:focused-test") {
		t.Fatalf("expected documented action refs to be preserved, got evidence=%#v claims=%#v knowledge=%#v", completed.EvidenceRefs, completed.ClaimRefs, completed.KnowledgeCandidateRefs)
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
