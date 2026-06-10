package projectworkplan_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestBaselineWorkPlanAndTaskLifecycleContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "work plan statuses", []string{
		projectworkplan.WorkPlanStatusPlanned,
		projectworkplan.WorkPlanStatusActive,
		projectworkplan.WorkPlanStatusBlocked,
		projectworkplan.WorkPlanStatusNeedsReview,
		projectworkplan.WorkPlanStatusDone,
		projectworkplan.WorkPlanStatusFailed,
		projectworkplan.WorkPlanStatusCancelled,
		projectworkplan.WorkPlanStatusSuperseded,
	}, []string{"planned", "active", "blocked", "needs_review", "done", "failed", "cancelled", "superseded"})

	assertExactSet(t, "work task statuses", []string{
		projectworkplan.WorkTaskStatusPlanned,
		projectworkplan.WorkTaskStatusReady,
		projectworkplan.WorkTaskStatusClaimed,
		projectworkplan.WorkTaskStatusInProgress,
		projectworkplan.WorkTaskStatusBlocked,
		projectworkplan.WorkTaskStatusNeedsReview,
		projectworkplan.WorkTaskStatusVerifying,
		projectworkplan.WorkTaskStatusDone,
		projectworkplan.WorkTaskStatusFailed,
		projectworkplan.WorkTaskStatusCancelled,
		projectworkplan.WorkTaskStatusSuperseded,
	}, []string{"planned", "ready", "claimed", "in_progress", "blocked", "needs_review", "verifying", "done", "failed", "cancelled", "superseded"})

	assertExactSet(t, "work plan isolation modes", []string{
		projectworkplan.WorkPlanIsolationShared,
		projectworkplan.WorkPlanIsolationDedicatedWorktree,
		projectworkplan.WorkPlanIsolationUnavailable,
	}, []string{"shared", "dedicated_worktree", "unavailable"})

	assertExactSet(t, "decomposition quality values", []string{
		projectworkplan.DecompositionDraft,
		projectworkplan.DecompositionReady,
		projectworkplan.DecompositionTooBroad,
		projectworkplan.DecompositionMissingEvidence,
		projectworkplan.DecompositionMissingContext,
		projectworkplan.DecompositionMissingVerification,
		projectworkplan.DecompositionMissingResume,
	}, []string{"draft", "ready", "too_broad", "missing_evidence", "missing_context", "missing_verification", "missing_resume"})
}

func TestBaselineWorkTaskContractFieldsJSON(t *testing.T) {
	t.Parallel()

	task := projectworkplan.WorkTask{
		ID:                      "work_task_1",
		ProjectID:               "project-1",
		PlanID:                  "work_plan_1",
		TaskRef:                 "task-1",
		Title:                   "Implementation task",
		Status:                  projectworkplan.WorkTaskStatusReady,
		AcceptanceCriteria:      []string{"contract:acceptance"},
		StopConditions:          []string{"contract:stop"},
		VerifierLadder:          []string{"go test ./internal/projectworkplan"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"impact:downstream"},
		OutputContract:          "metadata-only closeout",
		ArtifactRefs:            []string{"artifact:test"},
		AgentRunIDs:             []string{"run-1"},
		ContextPackRefs:         []string{"context_pack:baseline"},
		EvidenceRefs:            []string{"evidence:baseline"},
		ClaimRefs:               []string{"claim:baseline"},
		VerifierResultRefs:      []string{"verifier:baseline"},
		ReviewResultRefs:        []string{"review:baseline"},
		KnowledgeCandidateRefs:  []string{"knowledge:no_reusable"},
		DecompositionQuality:    projectworkplan.DecompositionReady,
		VerificationRequirement: "focused package test",
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	for _, key := range []string{
		"acceptance_criteria", "stop_conditions", "verifier_ladder",
		"regression_test_applicability", "downstream_impact_refs", "output_contract",
		"artifact_refs", "agent_run_ids", "context_pack_refs", "evidence_refs",
		"claim_refs", "verifier_result_refs", "review_result_refs",
		"knowledge_candidate_refs", "decomposition_quality",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("work task JSON omitted %q: %s", key, data)
		}
	}
}

func TestBaselineWorkTaskOperationInputsJSON(t *testing.T) {
	t.Parallel()

	action := projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             "work_task_1",
		OwnerAgent:         "worker",
		RunID:              "run-1",
		TraceID:            "trace-1",
		Outcome:            "metadata-only outcome",
		SafeNextAction:     "run verifier",
		BlockedReason:      "blocked safely",
		BlockedByTaskIDs:   []string{"work_task_blocker"},
		ContextPackRefs:    []string{"context_pack:baseline"},
		EvidenceRefs:       []string{"evidence:baseline"},
		ClaimRefs:          []string{"claim:baseline"},
		KnowledgeRefs:      []string{"knowledge:no_reusable"},
		ResumeInstructions: "resume from task metadata",
		VerifierResultRefs: []string{"verifier:baseline"},
		ReviewResultRefs:   []string{"review:baseline"},
		ReviewExemptReason: "tiny metadata-only task",
	}
	data, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal action: %v", err)
	}
	for _, key := range []string{
		"task_id", "run_id", "safe_next_action", "blocked_reason",
		"blocked_by_task_ids", "context_pack_refs", "evidence_refs",
		"claim_refs", "knowledge_candidate_refs", "resume_instructions",
		"verifier_result_refs", "review_result_refs", "review_exempt_reason",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("work task action JSON omitted %q: %s", key, data)
		}
	}
}

func TestBaselineWorkPlanTaskLifecycleBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := projectworkplan.New(workplanstore.NewMemoryStore())
	plan, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:        "project-1",
		PlanRef:          "phase0-baseline",
		Title:            "Phase 0 baseline",
		GoalSummary:      "Lock current Work Plan behavior",
		OwnerAgent:       "orchestrator",
		IsolationMode:    projectworkplan.WorkPlanIsolationDedicatedWorktree,
		WorkspaceRef:     "workspace:phase0",
		GitBaseRef:       "git:base",
		GitBranchRef:     "git:branch",
		GitWorktreeRef:   "git:worktree",
		ResumeSummary:    "resume from current Work Plan metadata",
		CreatedByRunID:   "run-create",
		TraceID:          "trace-phase0",
		ParallelGroupRef: "parallel:phase0",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.Status != projectworkplan.WorkPlanStatusPlanned || plan.IsolationMode != projectworkplan.WorkPlanIsolationDedicatedWorktree || plan.GitWorktreeRef == "" {
		t.Fatalf("plan did not preserve lifecycle/isolation refs: %#v", plan)
	}
	resumed, err := svc.ResumeWorkPlan(ctx, projectworkplan.ResumeWorkPlanInput{ProjectID: "project-1", PlanID: plan.ID})
	if err != nil {
		t.Fatalf("resume plan: %v", err)
	}
	if resumed.ID != plan.ID || resumed.ResumeSummary == "" {
		t.Fatalf("resume lost plan metadata: %#v", resumed)
	}
	active, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         plan.ID,
		Status:         projectworkplan.WorkPlanStatusActive,
		SafeNextAction: "claim ready task",
	})
	if err != nil {
		t.Fatalf("activate plan: %v", err)
	}
	if active.Status != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("expected active plan, got %#v", active)
	}

	task, err := svc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "task-implementation",
		Title:                   "Implementation task",
		Description:             "metadata-only task",
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              "worker",
		RunID:                   "run-create",
		TraceID:                 "trace-phase0",
		EvidenceNeeded:          []string{"evidence:needed"},
		ContextPackRefs:         []string{"context_pack:baseline"},
		FilesToRead:             []string{"internal/projectworkplan/service.go"},
		FilesToEdit:             []string{"internal/projectworkplan/baseline_contract_test.go"},
		LikelyFilesAffected:     []string{"internal/projectworkplan"},
		VerificationRequirement: "go test ./internal/projectworkplan",
		ExpectedOutput:          "baseline test behavior remains stable",
		FailureCriteria:         "block when lifecycle metadata is missing",
		ReviewGate:              "independent review required",
		ResumeInstructions:      "resume from task metadata",
		KnowledgeCandidateRefs:  []string{"knowledge:no_reusable"},
		EvidenceRefs:            []string{"evidence:seed"},
		ClaimRefs:               []string{"claim:seed"},
		VerifierResultRefs:      []string{"verifier:seed"},
		ReviewResultRefs:        []string{"review:seed"},
		ArtifactRefs:            []string{"artifact:seed"},
		AgentRunIDs:             []string{"run-create"},
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"service lifecycle succeeds"},
		StopConditions:          []string{"stop on unsafe metadata"},
		VerifierLadder:          []string{"go test ./internal/projectworkplan"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"impact:baseline"},
		OutputContract:          "metadata-only refs",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	next, err := svc.GetNextWorkTask(ctx, projectworkplan.GetNextWorkTaskInput{ProjectID: "project-1", PlanID: plan.ID, OwnerAgent: "worker"})
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	if !next.Found || next.Task.ID != task.ID || next.RequiredVerification == "" || next.SafeReason == "" {
		t.Fatalf("get_next did not return safe ready task metadata: %#v", next)
	}
	claimed, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, OwnerAgent: "worker", RunID: "run-claim"})
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if claimed.Status != projectworkplan.WorkTaskStatusClaimed || claimed.ClaimedByRunID != "run-claim" {
		t.Fatalf("claim lost run ownership: %#v", claimed)
	}
	released, err := svc.ReleaseWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, OwnerAgent: "worker", RunID: "run-claim"})
	if err != nil {
		t.Fatalf("release task: %v", err)
	}
	if released.Status != projectworkplan.WorkTaskStatusReady || released.ClaimedByRunID != "" {
		t.Fatalf("release did not return task to ready queue: %#v", released)
	}
	reclaimed, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, OwnerAgent: "worker", RunID: "run-exec"})
	if err != nil {
		t.Fatalf("reclaim task: %v", err)
	}
	started, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: reclaimed.ID, RunID: "run-exec"})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if started.Status != projectworkplan.WorkTaskStatusInProgress || started.StartedAt.IsZero() {
		t.Fatalf("start did not mark execution metadata: %#v", started)
	}
	if _, err := svc.AttachEvidence(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "evidence:baseline", AttachedByRunID: "run-exec"}); err != nil {
		t.Fatalf("attach evidence: %v", err)
	}
	if _, err := svc.AttachClaim(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "claim:baseline", AttachedByRunID: "run-exec"}); err != nil {
		t.Fatalf("attach claim: %v", err)
	}
	if _, err := svc.AttachVerifierResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "verifier:baseline", AttachedByRunID: "run-exec"}); err != nil {
		t.Fatalf("attach verifier: %v", err)
	}
	if _, err := svc.AttachReviewResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "review:baseline", AttachedByRunID: "run-review"}); err != nil {
		t.Fatalf("attach review: %v", err)
	}
	completed, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             task.ID,
		RunID:              "run-exec",
		Outcome:            "metadata-only closeout",
		SafeNextAction:     "run final verifier",
		EvidenceRefs:       []string{"evidence:baseline"},
		ClaimRefs:          []string{"claim:baseline"},
		VerifierResultRefs: []string{"verifier:baseline"},
		ReviewResultRefs:   []string{"review:baseline"},
		KnowledgeRefs:      []string{"knowledge:no_reusable"},
	})
	if err != nil {
		t.Fatalf("complete task: %v", err)
	}
	if completed.Status != projectworkplan.WorkTaskStatusDone || completed.CompletedAt.IsZero() || len(completed.EvidenceRefs) == 0 || len(completed.ReviewResultRefs) == 0 {
		t.Fatalf("complete did not persist closeout refs: %#v", completed)
	}

	blockTarget := createBaselineReadyTask(t, ctx, svc, plan.ID, "task-block")
	blocked, err := svc.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             blockTarget.ID,
		BlockedReason:      "dependency checkpoint missing",
		BlockedByTaskIDs:   []string{task.ID},
		ResumeInstructions: "resume after checkpoint evidence exists",
		SafeNextAction:     "wait for dependency checkpoint",
	})
	if err != nil {
		t.Fatalf("block task: %v", err)
	}
	if blocked.Status != projectworkplan.WorkTaskStatusBlocked || blocked.BlockedReason == "" || len(blocked.BlockedByTaskIDs) != 1 || blocked.ResumeInstructions == "" {
		t.Fatalf("block did not preserve exact blocker metadata: %#v", blocked)
	}
	updated, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:      "project-1",
			TaskID:         blockTarget.ID,
			SafeNextAction: "cancel stale blocked task",
			Outcome:        "cancelled by baseline test",
		},
		Status: projectworkplan.WorkTaskStatusCancelled,
	})
	if err != nil {
		t.Fatalf("update task status: %v", err)
	}
	if updated.Status != projectworkplan.WorkTaskStatusCancelled {
		t.Fatalf("expected cancelled task, got %#v", updated)
	}

	expandTarget := createBaselineReadyTask(t, ctx, svc, plan.ID, "task-expand")
	expanded, err := svc.ExpandWorkTaskScope(ctx, projectworkplan.ExpandWorkTaskScopeInput{
		ProjectID:          "project-1",
		TaskID:             expandTarget.ID,
		FilesToEdit:        []string{"internal/projectworkplan/baseline_contract_test.go"},
		ResumeInstructions: "resume with expanded scoped file",
		RunID:              "run-expand",
	})
	if err != nil {
		t.Fatalf("expand task scope: %v", err)
	}
	if len(expanded.FilesToEdit) == 0 || expanded.ResumeInstructions == "" {
		t.Fatalf("expand did not persist scoped metadata: %#v", expanded)
	}

	failTarget := createBaselineReadyTask(t, ctx, svc, plan.ID, "task-fail")
	failTarget, err = svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: failTarget.ID, OwnerAgent: "worker", RunID: "run-fail"})
	if err != nil {
		t.Fatalf("claim fail target: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: failTarget.ID, RunID: "run-fail"}); err != nil {
		t.Fatalf("start fail target: %v", err)
	}
	failed, err := svc.FailWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:      "project-1",
		TaskID:         failTarget.ID,
		RunID:          "run-fail",
		Outcome:        "focused verifier failed",
		SafeNextAction: "inspect failure category",
	})
	if err != nil {
		t.Fatalf("fail task: %v", err)
	}
	if failed.Status != projectworkplan.WorkTaskStatusFailed || failed.Outcome == "" {
		t.Fatalf("fail did not persist terminal failure metadata: %#v", failed)
	}
}

func TestBaselineWorkPlanStatusTransitionBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := projectworkplan.New(workplanstore.NewMemoryStore())
	for _, tc := range []struct {
		name        string
		transitions []string
	}{
		{name: "active-blocked-active-needs-review-done-superseded", transitions: []string{
			projectworkplan.WorkPlanStatusActive,
			projectworkplan.WorkPlanStatusBlocked,
			projectworkplan.WorkPlanStatusActive,
			projectworkplan.WorkPlanStatusNeedsReview,
			projectworkplan.WorkPlanStatusDone,
			projectworkplan.WorkPlanStatusSuperseded,
		}},
		{name: "active-failed-superseded", transitions: []string{
			projectworkplan.WorkPlanStatusActive,
			projectworkplan.WorkPlanStatusFailed,
			projectworkplan.WorkPlanStatusSuperseded,
		}},
		{name: "planned-cancelled-superseded", transitions: []string{
			projectworkplan.WorkPlanStatusCancelled,
			projectworkplan.WorkPlanStatusSuperseded,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
				ProjectID:      "project-1",
				PlanRef:        "transition-" + tc.name,
				Title:          "Transition " + tc.name,
				GoalSummary:    "Lock Work Plan transition behavior",
				OwnerAgent:     "orchestrator",
				CreatedByRunID: "run-create",
				TraceID:        "trace-transition",
			})
			if err != nil {
				t.Fatalf("create plan: %v", err)
			}
			for _, status := range tc.transitions {
				updated, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
					ProjectID:      "project-1",
					PlanID:         plan.ID,
					Status:         status,
					SafeNextAction: "baseline transition " + status,
					RunID:          "run-transition",
					TraceID:        "trace-transition",
					Outcome:        "transitioned to " + status,
					ResumeSummary:  "resume after " + status,
				})
				if err != nil {
					t.Fatalf("transition %s -> %s: %v", plan.Status, status, err)
				}
				if updated.Status != status || updated.TraceID != "trace-transition" || updated.Outcome == "" || updated.ResumeSummary == "" {
					t.Fatalf("transition lost status/action metadata: %#v", updated)
				}
				plan = updated
			}
		})
	}
}

func TestBaselineWorkTaskStatusTransitionBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := projectworkplan.New(workplanstore.NewMemoryStore())
	plan, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "task-transition-plan",
		Title:       "Task transition plan",
		GoalSummary: "Lock Work Task transition behavior",
		OwnerAgent:  "orchestrator",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	t.Run("ready-claimed-in-progress-needs-review-verifying-done-superseded", func(t *testing.T) {
		task, err := svc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
			ProjectID:               "project-1",
			PlanID:                  plan.ID,
			TaskRef:                 "task-transition-main",
			Title:                   "Task transition main",
			Status:                  projectworkplan.WorkTaskStatusReady,
			OwnerAgent:              "worker",
			VerificationRequirement: "go test ./internal/projectworkplan",
			ResumeInstructions:      "resume from task transition metadata",
			DecompositionQuality:    projectworkplan.DecompositionReady,
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		for _, step := range []struct {
			status string
			input  projectworkplan.WorkTaskActionInput
		}{
			{status: projectworkplan.WorkTaskStatusClaimed, input: projectworkplan.WorkTaskActionInput{RunID: "run-task-transition", OwnerAgent: "worker"}},
			{status: projectworkplan.WorkTaskStatusInProgress, input: projectworkplan.WorkTaskActionInput{RunID: "run-task-transition", TraceID: "trace-task-transition"}},
			{status: projectworkplan.WorkTaskStatusNeedsReview, input: projectworkplan.WorkTaskActionInput{RunID: "run-task-transition", Outcome: "implementation ready for review", SafeNextAction: "review task output"}},
			{status: projectworkplan.WorkTaskStatusVerifying, input: projectworkplan.WorkTaskActionInput{RunID: "run-task-transition", VerifierResultRefs: []string{"verifier:task-transition"}}},
			{status: projectworkplan.WorkTaskStatusDone, input: projectworkplan.WorkTaskActionInput{RunID: "run-task-transition", Outcome: "verified metadata-only completion", ReviewExemptReason: "baseline status transition test"}},
			{status: projectworkplan.WorkTaskStatusSuperseded, input: projectworkplan.WorkTaskActionInput{RunID: "run-task-transition", Outcome: "superseded by newer task"}},
		} {
			step.input.ProjectID = "project-1"
			step.input.TaskID = task.ID
			updated, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{WorkTaskActionInput: step.input, Status: step.status})
			if err != nil {
				t.Fatalf("transition %s -> %s: %v", task.Status, step.status, err)
			}
			if updated.Status != step.status || (step.input.RunID != "" && !containsBaselineString(updated.AgentRunIDs, step.input.RunID)) {
				t.Fatalf("task transition lost state metadata: %#v", updated)
			}
			task = updated
		}
	})

	t.Run("blocked-ready-cancelled-superseded", func(t *testing.T) {
		task := createBaselineReadyTask(t, ctx, svc, plan.ID, "task-transition-blocked")
		blocked, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
				ProjectID:          "project-1",
				TaskID:             task.ID,
				BlockedReason:      "waiting for dependency evidence",
				ResumeInstructions: "resume after dependency evidence exists",
				SafeNextAction:     "wait for dependency evidence",
			},
			Status: projectworkplan.WorkTaskStatusBlocked,
		})
		if err != nil {
			t.Fatalf("block task: %v", err)
		}
		ready, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: blocked.ID, SafeNextAction: "resume ready task"},
			Status:              projectworkplan.WorkTaskStatusReady,
		})
		if err != nil {
			t.Fatalf("blocked -> ready: %v", err)
		}
		cancelled, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: ready.ID, Outcome: "cancelled stale task"},
			Status:              projectworkplan.WorkTaskStatusCancelled,
		})
		if err != nil {
			t.Fatalf("ready -> cancelled: %v", err)
		}
		superseded, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: cancelled.ID, Outcome: "superseded cancelled task"},
			Status:              projectworkplan.WorkTaskStatusSuperseded,
		})
		if err != nil {
			t.Fatalf("cancelled -> superseded: %v", err)
		}
		if superseded.Status != projectworkplan.WorkTaskStatusSuperseded || superseded.Outcome == "" {
			t.Fatalf("terminal task transition lost metadata: %#v", superseded)
		}
	})
}

func createBaselineReadyTask(t *testing.T, ctx context.Context, svc *projectworkplan.Service, planID string, taskRef string) projectworkplan.WorkTask {
	t.Helper()
	task, err := svc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  planID,
		TaskRef:                 taskRef,
		Title:                   taskRef,
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              "worker",
		VerificationRequirement: "go test ./internal/projectworkplan",
		ResumeInstructions:      "resume from task metadata",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	})
	if err != nil {
		t.Fatalf("create %s: %v", taskRef, err)
	}
	return task
}

func containsBaselineString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func assertExactSet(t *testing.T, name string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s count mismatch: got %#v want %#v", name, got, want)
	}
	seen := map[string]int{}
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] != 1 {
			t.Fatalf("%s missing or duplicated %q in %#v", name, value, got)
		}
		delete(seen, value)
	}
	if len(seen) != 0 {
		t.Fatalf("%s has unexpected values: %#v", name, seen)
	}
}
