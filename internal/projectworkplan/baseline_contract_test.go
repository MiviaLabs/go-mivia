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

func TestBaselineWorkTaskResumeUsesOnlyPersistedMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := projectworkplan.New(workplanstore.NewMemoryStore())

	// Build and block a task mid-flight, then keep ONLY the storage keys.
	// Every in-memory handle from the first flight stays scoped inside the
	// setup helper so the resume path below cannot use it.
	projectID, taskID := blockBaselineTaskMidFlight(t, ctx, svc)

	// Re-hydrate purely from the store with a fresh Get.
	rehydrated, err := svc.GetWorkTask(ctx, projectID, taskID)
	if err != nil {
		t.Fatalf("re-read blocked task from store: %v", err)
	}
	if rehydrated.Status != projectworkplan.WorkTaskStatusBlocked {
		t.Fatalf("expected blocked task after re-read, got %#v", rehydrated)
	}
	if rehydrated.ResumeInstructions == "" || len(rehydrated.ContextPackRefs) == 0 ||
		len(rehydrated.VerifierLadder) == 0 || len(rehydrated.AcceptanceCriteria) == 0 ||
		len(rehydrated.EvidenceRefs) == 0 || len(rehydrated.ClaimRefs) == 0 ||
		len(rehydrated.VerifierResultRefs) == 0 || len(rehydrated.ReviewResultRefs) == 0 ||
		len(rehydrated.KnowledgeCandidateRefs) == 0 || len(rehydrated.ArtifactRefs) == 0 {
		t.Fatalf("persisted task lost resume metadata needed by a fresh agent: %#v", rehydrated)
	}

	completed := resumeBaselineTaskFromPersistedMetadataOnly(t, ctx, svc, rehydrated)

	if completed.Status != projectworkplan.WorkTaskStatusDone || completed.CompletedAt.IsZero() {
		t.Fatalf("resumed task did not complete: %#v", completed)
	}
	for name, check := range map[string]struct {
		refs []string
		want string
	}{
		"evidence":     {completed.EvidenceRefs, "evidence:objective-resume"},
		"claim":        {completed.ClaimRefs, "claim:objective-resume"},
		"verifier":     {completed.VerifierResultRefs, "verifier:objective-resume"},
		"review":       {completed.ReviewResultRefs, "review:objective-resume"},
		"context pack": {completed.ContextPackRefs, "context_pack:objective-resume"},
		"knowledge":    {completed.KnowledgeCandidateRefs, "knowledge:objective-resume"},
		"artifact":     {completed.ArtifactRefs, "artifact:objective-resume"},
	} {
		if !containsBaselineString(check.refs, check.want) {
			t.Fatalf("completed task lost upstream %s ref %q: %#v", name, check.want, completed)
		}
	}
	if !containsBaselineString(completed.ContextPackRefs, "context_pack:objective-seed") {
		t.Fatalf("completed task lost seed context pack ref: %#v", completed)
	}
	if len(completed.AcceptanceCriteria) == 0 || len(completed.VerifierLadder) == 0 || completed.ResumeInstructions == "" {
		t.Fatalf("completed task lost planning metadata: %#v", completed)
	}
	if !containsBaselineString(completed.AgentRunIDs, "run-firstflight") ||
		!containsBaselineString(completed.AgentRunIDs, "run-resume-"+completed.TaskRef) {
		t.Fatalf("completed task lost run lineage across resume: %#v", completed)
	}
}

// blockBaselineTaskMidFlight drives a work task through claim -> start ->
// attachments -> block and returns only the storage keys, dropping every
// in-memory handle from the first flight.
func blockBaselineTaskMidFlight(t *testing.T, ctx context.Context, svc *projectworkplan.Service) (string, string) {
	t.Helper()
	plan, err := svc.CreateWorkPlan(ctx, projectworkplan.CreateWorkPlanInput{
		ProjectID:   "project-1",
		PlanRef:     "objective-resume-pilot",
		Title:       "Resume pilot plan",
		GoalSummary: "Lock resume-from-persisted-metadata behavior",
		OwnerAgent:  "orchestrator",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         plan.ID,
		Status:         projectworkplan.WorkPlanStatusActive,
		SafeNextAction: "claim ready task",
	}); err != nil {
		t.Fatalf("activate plan: %v", err)
	}
	task, err := svc.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  plan.ID,
		TaskRef:                 "task-objective-resume",
		Title:                   "Resume pilot task",
		Description:             "metadata-only resume pilot task",
		Status:                  projectworkplan.WorkTaskStatusReady,
		OwnerAgent:              "worker",
		ContextPackRefs:         []string{"context_pack:objective-seed"},
		VerificationRequirement: "go test ./internal/projectworkplan",
		ExpectedOutput:          "task completes from persisted metadata only",
		OutputContract:          "metadata-only closeout",
		ResumeInstructions:      "resume purely from persisted task metadata",
		ArtifactRefs:            []string{"artifact:objective-resume"},
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"acceptance:objective-resume"},
		StopConditions:          []string{"stop on unsafe metadata"},
		VerifierLadder:          []string{"go test ./internal/projectworkplan"},
		RegressionApplicability: "required",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, OwnerAgent: "worker", RunID: "run-firstflight"}); err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{ProjectID: "project-1", TaskID: task.ID, RunID: "run-firstflight"}); err != nil {
		t.Fatalf("start task: %v", err)
	}
	if _, err := svc.AttachEvidence(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "evidence:objective-resume", AttachedByRunID: "run-firstflight"}); err != nil {
		t.Fatalf("attach evidence: %v", err)
	}
	if _, err := svc.AttachClaim(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "claim:objective-resume", AttachedByRunID: "run-firstflight"}); err != nil {
		t.Fatalf("attach claim: %v", err)
	}
	if _, err := svc.AttachContextPack(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "context_pack:objective-resume", AttachedByRunID: "run-firstflight"}); err != nil {
		t.Fatalf("attach context pack: %v", err)
	}
	if _, err := svc.AttachKnowledgeCandidate(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "knowledge:objective-resume", AttachedByRunID: "run-firstflight"}); err != nil {
		t.Fatalf("attach knowledge candidate: %v", err)
	}
	// Attaching a verifier result while in_progress flips the task to
	// verifying (current behavior in addAttachmentRef).
	if _, err := svc.AttachVerifierResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "verifier:objective-resume", AttachedByRunID: "run-firstflight"}); err != nil {
		t.Fatalf("attach verifier result: %v", err)
	}
	if _, err := svc.AttachReviewResult(ctx, projectworkplan.AttachInput{ProjectID: "project-1", TaskID: task.ID, Ref: "review:objective-resume", AttachedByRunID: "run-reviewer-1"}); err != nil {
		t.Fatalf("attach review result: %v", err)
	}
	blocked, err := svc.BlockWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          "project-1",
		TaskID:             task.ID,
		BlockedReason:      "agent session ended mid-flight",
		ResumeInstructions: "resume blocked task using only fields re-read from the store",
		SafeNextAction:     "re-read task from store and resume",
	})
	if err != nil {
		t.Fatalf("block task mid-flight: %v", err)
	}
	return blocked.ProjectID, blocked.ID
}

// resumeBaselineTaskFromPersistedMetadataOnly drives resume -> claim ->
// start -> verify -> complete constructing every action input exclusively
// from the re-read task struct (and fresh Get calls between steps), never
// from variables captured during the first flight.
func resumeBaselineTaskFromPersistedMetadataOnly(t *testing.T, ctx context.Context, svc *projectworkplan.Service, persisted projectworkplan.WorkTask) projectworkplan.WorkTask {
	t.Helper()
	resumeRunID := "run-resume-" + persisted.TaskRef

	if _, err := svc.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
		WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
			ProjectID:      persisted.ProjectID,
			TaskID:         persisted.ID,
			SafeNextAction: persisted.ResumeInstructions,
		},
		Status: projectworkplan.WorkTaskStatusReady,
	}); err != nil {
		t.Fatalf("resume blocked task to ready: %v", err)
	}
	ready, err := svc.GetWorkTask(ctx, persisted.ProjectID, persisted.ID)
	if err != nil {
		t.Fatalf("re-read ready task: %v", err)
	}
	if ready.Status != projectworkplan.WorkTaskStatusReady || ready.ClaimedByRunID != "" {
		t.Fatalf("resume did not return task to unclaimed ready state: %#v", ready)
	}
	if _, err := svc.ClaimWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:  ready.ProjectID,
		TaskID:     ready.ID,
		OwnerAgent: ready.OwnerAgent,
		RunID:      resumeRunID,
	}); err != nil {
		t.Fatalf("claim resumed task: %v", err)
	}
	claimed, err := svc.GetWorkTask(ctx, ready.ProjectID, ready.ID)
	if err != nil {
		t.Fatalf("re-read claimed task: %v", err)
	}
	if _, err := svc.StartWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID: claimed.ProjectID,
		TaskID:    claimed.ID,
		RunID:     claimed.ClaimedByRunID,
	}); err != nil {
		t.Fatalf("start resumed task: %v", err)
	}
	started, err := svc.GetWorkTask(ctx, claimed.ProjectID, claimed.ID)
	if err != nil {
		t.Fatalf("re-read started task: %v", err)
	}
	// Re-attach the persisted verifier ref so the task reaches verifying;
	// done is not reachable directly from in_progress in current behavior.
	if _, err := svc.AttachVerifierResult(ctx, projectworkplan.AttachInput{
		ProjectID:       started.ProjectID,
		TaskID:          started.ID,
		Ref:             started.VerifierResultRefs[0],
		AttachedByRunID: started.ClaimedByRunID,
	}); err != nil {
		t.Fatalf("re-attach persisted verifier ref: %v", err)
	}
	verifying, err := svc.GetWorkTask(ctx, started.ProjectID, started.ID)
	if err != nil {
		t.Fatalf("re-read verifying task: %v", err)
	}
	completed, err := svc.CompleteWorkTask(ctx, projectworkplan.WorkTaskActionInput{
		ProjectID:          verifying.ProjectID,
		TaskID:             verifying.ID,
		RunID:              verifying.ClaimedByRunID,
		Outcome:            verifying.OutputContract,
		SafeNextAction:     verifying.VerifierLadder[0],
		ContextPackRefs:    verifying.ContextPackRefs,
		EvidenceRefs:       verifying.EvidenceRefs,
		ClaimRefs:          verifying.ClaimRefs,
		VerifierResultRefs: verifying.VerifierResultRefs,
		ReviewResultRefs:   verifying.ReviewResultRefs,
		KnowledgeRefs:      verifying.KnowledgeCandidateRefs,
	})
	if err != nil {
		t.Fatalf("complete resumed task: %v", err)
	}
	return completed
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
