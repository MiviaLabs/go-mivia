package projectworkflowchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectgitops"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestBaselineWorkflowChainContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "chain statuses", []string{
		ChainStatusPlanned,
		ChainStatusQueued,
		ChainStatusCompleted,
		ChainStatusPostValidationPassed,
		ChainStatusBlocked,
		ChainStatusFailed,
		ChainStatusCancelled,
		ChainStatusSuperseded,
	}, []string{"planned", "queued", "completed", "post_validation_passed", "blocked", "failed", "cancelled", "superseded"})

	assertExactSet(t, "stage statuses", []string{
		StageStatusPlanned,
		StageStatusQueued,
		StageStatusCompleted,
		StageStatusBlocked,
		StageStatusFailed,
		StageStatusCancelled,
		StageStatusSuperseded,
	}, []string{"planned", "queued", "completed", "blocked", "failed", "cancelled", "superseded"})

	assertExactSet(t, "gitops recovery statuses", []string{
		GitOpsRecoveryStatusRepairable,
		GitOpsRecoveryStatusTerminal,
		GitOpsRecoveryStatusCompleted,
	}, []string{"repairable", "terminal", "completed"})

	assertExactSet(t, "input kinds", []string{
		InputKindJiraIssueKey,
		InputKindObjectiveText,
		InputKindSafeRef,
	}, []string{"jira_issue_key", "objective_text", "safe_ref"})

	assertExactSet(t, "context providers", []string{
		ContextProviderJira,
		ContextProviderConfluence,
		ContextProviderIndexedRepo,
	}, []string{"jira", "confluence", "indexed_repo"})

	assertExactSet(t, "context modes", []string{
		ContextModeLocalIngested,
		ContextModeIndexed,
	}, []string{"local_ingested", "indexed"})
}

func TestBaselineWorkflowChainRecoveryReadModelContract(t *testing.T) {
	run := ChainRun{
		ID:                        "workflow_chain_run_1",
		ProjectID:                 "project-1",
		ChainRef:                  "chain-1",
		InputRef:                  "jira:PROJ-1044",
		Status:                    ChainStatusBlocked,
		GitOpsReady:               true,
		GitOpsAttemptCount:        2,
		GitOpsFailureCategory:     "gitops_verification_failed",
		GitOpsFailureEvidenceRefs: []string{"gitops-failure:gitops_verification_failed", "gitops-attempt:2"},
		GitOpsRecoveryStatus:      GitOpsRecoveryStatusRepairable,
		NextAction:                "chain GitOps recovery is repairable; retry_gitops may resume draft PR finalization",
		StageRuns: []StageRun{{
			StageRef:      "post-validation",
			WorkflowRef:   "governed-post-implementation-validation",
			Status:        StageStatusBlocked,
			WorkPlanID:    "plan-post-validation",
			WorkTaskIDs:   []string{"task-post-validation"},
			AutomationIDs: []string{"automation-post-validation"},
			BlockedReason: "gitops_verification_failed_repairable_attempt_2",
		}},
	}
	payload := mustBaselineJSON(t, run)
	for _, key := range []string{
		"gitops_ready",
		"gitops_attempt_count",
		"gitops_failure_category",
		"gitops_failure_evidence_refs",
		"gitops_recovery_status",
		"next_action",
		"blocked_reason",
	} {
		if !jsonHasKey(payload, key) {
			t.Fatalf("chain recovery read model missing %q in %s", key, payload)
		}
	}
}

func TestBaselineWorkflowChainGitOpsHandoffContract(t *testing.T) {
	input := GitOpsFinalizeInput{
		ProjectID:        "project-1",
		ChainRunID:       "workflow_chain_run_1",
		ChainRef:         "chain-1",
		InputRef:         "jira:PROJ-1044",
		WorkPlan:         projectworkplan.WorkPlan{ID: "plan-implementation", PlanRef: "plan-ref"},
		StageRuns:        []StageRun{{StageRef: "implementation", WorkTaskIDs: []string{"task-implementation"}}},
		AutomationIDs:    []string{"automation-implementation"},
		AllowedPathspecs: []string{"internal/projectworkflowchain/service.go"},
		ReviewRefs:       []string{"review:phase0"},
		VerifierRefs:     []string{"verifier:phase0"},
		TestResults:      []string{"phase0 verifier passed"},
		CreatedByRunID:   "run-phase0",
		TraceID:          "trace-phase0",
	}
	payload := mustBaselineJSON(t, input)
	for _, key := range []string{
		"ProjectID",
		"ChainRunID",
		"ChainRef",
		"InputRef",
		"WorkPlan",
		"StageRuns",
		"AutomationIDs",
		"AllowedPathspecs",
		"ReviewRefs",
		"VerifierRefs",
		"TestResults",
		"CreatedByRunID",
		"TraceID",
	} {
		if !jsonHasKey(payload, key) {
			t.Fatalf("GitOps handoff contract missing %q in %s", key, payload)
		}
	}
}

func TestBaselineWorkflowChainCheckpointReasonContract(t *testing.T) {
	assertExactSet(t, "activation checkpoint reasons", []string{
		"compile_first_stage_failed",
		"activate_first_stage_failed",
		"compile_next_stage_failed",
		"activate_next_stage_failed",
		"missing_carried_implementation_tasks",
		"unreviewed_carried_implementation_task",
	}, []string{
		"compile_first_stage_failed",
		"activate_first_stage_failed",
		"compile_next_stage_failed",
		"activate_next_stage_failed",
		"missing_carried_implementation_tasks",
		"unreviewed_carried_implementation_task",
	})
}

func TestBaselineWorkflowChainActivationCheckpointBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{
		ProjectID:      "project-1",
		ChainRef:       "chain-1",
		InputText:      "GENERIC-1044",
		CreatedByRunID: "run-phase0",
		TraceID:        "trace-phase0",
	})
	if err != nil {
		t.Fatalf("start chain: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get chain: %v", err)
	}
	if run.Status != ChainStatusQueued || run.NextAction == "" || len(run.StageRuns) != 3 || run.StageRuns[0].Status != StageStatusQueued {
		t.Fatalf("start did not persist queued activation read model: %#v", run)
	}
	if len(workPlans.events) < 2 || strings.Join(workPlans.events[:2], ",") != "release:task-decomposition,activate:plan-decomposition" {
		t.Fatalf("activation must release tasks before Work Plan activation, got %#v", workPlans.events)
	}
	if len(workPlans.taskStatusUpdates) != 1 || workPlans.taskStatusUpdates[0].SafeNextAction == "" || workPlans.taskStatusUpdates[0].RunID != "run-phase0" || workPlans.taskStatusUpdates[0].TraceID != "trace-phase0" {
		t.Fatalf("task release must persist run/trace/safe action metadata: %#v", workPlans.taskStatusUpdates)
	}
	if len(workPlans.statusUpdates) != 1 || workPlans.statusUpdates[0].SafeNextAction == "" || workPlans.statusUpdates[0].RunID != "run-phase0" || workPlans.statusUpdates[0].TraceID != "trace-phase0" {
		t.Fatalf("plan activation must persist run/trace/safe action metadata: %#v", workPlans.statusUpdates)
	}
}

func TestBaselineWorkflowChainGitOpsRecoveryRetryBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")
	finalizer := &fakeGitOpsFinalizer{err: fmt.Errorf("%w: abcdef123456", projectgitops.ErrVerificationFailed)}
	svc.SetGitOpsFinalizer(finalizer)

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-phase0", TraceID: "trace-phase0"})
	if err != nil {
		t.Fatalf("start chain: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance %s: %v", planID, err)
		}
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatal("expected GitOps verification failure")
	}
	blocked, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get blocked chain: %v", getErr)
	}
	if blocked.Status != ChainStatusBlocked || !blocked.GitOpsReady || blocked.GitOpsAttemptCount != 1 || blocked.GitOpsRecoveryStatus != GitOpsRecoveryStatusRepairable {
		t.Fatalf("GitOps failure did not persist repairable recovery metadata: %#v", blocked)
	}
	if blocked.GitOpsFailureCategory != "gitops_verification_failed_abcdef123456" || !containsString(blocked.GitOpsFailureEvidenceRefs, "gitops-attempt:1") || !strings.Contains(blocked.StageRuns[2].BlockedReason, "repairable_attempt_1") {
		t.Fatalf("GitOps recovery metadata missing exact category/evidence/block reason: %#v", blocked)
	}
	finalizer.err = nil
	finalizer.result = GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}
	retried, err := svc.RetryGitOps(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("retry GitOps: %v", err)
	}
	if retried.Status != ChainStatusCompleted || retried.GitOpsReady || retried.PullRequestRef != "github-pr-1044" || retried.GitOpsRecoveryStatus != GitOpsRecoveryStatusCompleted || retried.GitOpsFailureCategory != "" || len(retried.GitOpsFailureEvidenceRefs) != 0 {
		t.Fatalf("successful GitOps retry did not clear recovery metadata: %#v", retried)
	}
	if len(finalizer.inputs) != 2 || finalizer.inputs[1].WorkPlan.ID != "plan-implementation" || finalizer.inputs[1].TraceID != "trace-phase0" {
		t.Fatalf("GitOps retry lost handoff refs: %#v", finalizer.inputs)
	}
}

func TestBaselineWorkflowChainGitOpsRequiresPostValidationReviewAndVerifierRefs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, tc := range []struct {
		name string
		task projectworkplan.WorkTask
	}{
		{
			name: "missing-review",
			task: projectworkplan.WorkTask{
				ID:                 "task-post-validation",
				ProjectID:          "project-1",
				PlanID:             "plan-post-validation",
				TaskRef:            "task-post-validation",
				Status:             projectworkplan.WorkTaskStatusDone,
				VerifierResultRefs: []string{"verifier:task-post-validation"},
			},
		},
		{
			name: "missing-verifier",
			task: projectworkplan.WorkTask{
				ID:               "task-post-validation",
				ProjectID:        "project-1",
				PlanID:           "plan-post-validation",
				TaskRef:          "task-post-validation",
				Status:           projectworkplan.WorkTaskStatusDone,
				ReviewResultRefs: []string{"review:task-post-validation"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			workPlans := &fakeWorkPlans{tasksByID: map[string]projectworkplan.WorkTask{"task-post-validation": tc.task}}
			finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "github-pr-1044"}}
			svc := New(store, workflows, workPlans, []Config{testConfig()})
			svc.SetGitOpsFinalizer(finalizer)
			svc.newID = deterministicIDs("workflow_chain_run_1")

			result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-phase0", TraceID: "trace-phase0"})
			if err != nil {
				t.Fatalf("start chain: %v", err)
			}
			for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
				if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
					t.Fatalf("advance %s: %v", planID, err)
				}
			}
			if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected missing post-validation refs to block GitOps, got %v", err)
			}
			blocked, err := svc.Get(ctx, "project-1", result.ChainRunID)
			if err != nil {
				t.Fatalf("get blocked chain: %v", err)
			}
			if blocked.Status != ChainStatusBlocked || !blocked.GitOpsReady || blocked.PullRequestRef != "" || !strings.HasPrefix(blocked.StageRuns[2].BlockedReason, "gitops_finalize_failed_invalid_project_workflow_chain_input") {
				t.Fatalf("missing post-validation refs must block before PR finalization: %#v", blocked)
			}
			if len(finalizer.inputs) != 0 {
				t.Fatalf("GitOps finalizer must not run without review and verifier refs, got %#v", finalizer.inputs)
			}
		})
	}
}

func TestBaselineChainRecordsCompiledStageWithPlannedPlanAndNoQueuedRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{
		allTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {
				{ID: "task-decomposition", ProjectID: "project-1", PlanID: "plan-decomposition", TaskRef: "decompose-work-plan", Status: projectworkplan.WorkTaskStatusDone, DecompositionQuality: projectworkplan.DecompositionReady},
			},
		},
		openTasksByPlan: map[string][]projectworkplan.WorkTask{
			"plan-decomposition": {},
			"plan-implementation": {
				{ID: "task-implementation", ProjectID: "project-1", PlanID: "plan-implementation", TaskRef: "select-ready-tasks", Status: projectworkplan.WorkTaskStatusPlanned, DecompositionQuality: projectworkplan.DecompositionReady},
			},
		},
	}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "run-phase0", TraceID: "trace-phase0"})
	if err != nil {
		t.Fatalf("start chain: %v", err)
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil || !strings.Contains(err.Error(), "missing_carried_implementation_tasks") {
		t.Fatalf("expected next-stage activation failure after compile, got %v", err)
	}
	run, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get orphan chain: %v", getErr)
	}
	implementation := chainStageRunByRef(t, run, "implementation")
	if implementation.WorkPlanID != "plan-implementation" || !containsString(implementation.WorkTaskIDs, "task-implementation") || !containsString(implementation.AutomationIDs, "automation-implementation") {
		t.Fatalf("orphan compiled stage must keep compiled plan/task/automation refs: %#v", implementation)
	}
	if !containsString(run.WorkPlanIDs, "plan-implementation") || !containsString(run.AutomationIDs, "automation-implementation") {
		t.Fatalf("orphan chain run must keep compiled plan/automation refs: %#v", run)
	}
	if run.Status != ChainStatusBlocked || implementation.Status != StageStatusBlocked || implementation.BlockedReason != "activate_next_stage_failed_missing_carried_implementation_tasks" {
		t.Fatalf("orphan compiled stage lost exact blocked status/reason: %#v", run)
	}
	if run.NextAction != "chain blocked while activating next stage" {
		t.Fatalf("orphan compiled stage lost exact next action: %q", run.NextAction)
	}
	if run.GitOpsReady || run.GitOpsAttemptCount != 0 || run.GitOpsFailureCategory != "" || run.GitOpsRecoveryStatus != "" || len(run.GitOpsFailureEvidenceRefs) != 0 || run.PullRequestRef != "" {
		t.Fatalf("orphan compiled stage must keep zero-value GitOps fields: %#v", run)
	}
	if len(workPlans.activations) != 1 || workPlans.activations[0] != "plan-decomposition" {
		t.Fatalf("implementation Work Plan must stay planned (never activated), got activations %#v", workPlans.activations)
	}
	if len(workPlans.released) != 0 {
		t.Fatalf("no tasks may be released for the orphan stage (zero queued/running automation runs), got %#v", workPlans.released)
	}
}

func TestBaselineChainStartFailsClosedForDisabledChainAndUnknownWorkflowRef(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, tc := range []struct {
		name               string
		disabled           bool
		unknownWorkflowRef bool
		want               string
	}{
		{name: "disabled chain", disabled: true, want: "workflow chain config not found"},
		{name: "unknown workflow ref", unknownWorkflowRef: true, want: "must resolve to exactly one enabled workflow"},
	} {
		for _, mode := range []struct {
			name   string
			dryRun bool
		}{
			{name: "real", dryRun: false},
			{name: "dry-run", dryRun: true},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				cfg := testConfig()
				if tc.disabled {
					cfg.Enabled = false
				}
				definitions := enabledWorkflows()
				if tc.unknownWorkflowRef {
					definitions = definitions[:1]
				}
				store := newTestChainStore()
				workflows := &fakeWorkflowAPI{workflows: definitions}
				workPlans := &fakeWorkPlans{}
				svc := New(store, workflows, workPlans, []Config{cfg})

				_, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", DryRun: mode.dryRun})
				if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected fail-closed %q rejection, got %v", tc.want, err)
				}
				if len(store.runs) != 0 {
					t.Fatalf("fail-closed start must not persist chain runs: %#v", store.runs)
				}
				if len(workflows.compileInputs) != 0 {
					t.Fatalf("fail-closed start must not compile workflows: %#v", workflows.compileInputs)
				}
				if len(workPlans.activations) != 0 || len(workPlans.released) != 0 {
					t.Fatalf("fail-closed start must not activate plans or release tasks, activations=%#v released=%#v", workPlans.activations, workPlans.released)
				}
			})
		}
	}
}

func TestBaselineGovernedDeliveryJourneyPinsEveryBoundaryJira(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("GENERIC-1044", true)})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{
		CommitRef:      "commit/GENERIC-1044",
		PushRef:        "push/GENERIC-1044",
		PullRequestRef: "github-pr-1044",
		EvidenceRefs:   []string{"gitops-evidence:GENERIC-1044"},
	}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	// Boundary: chain start from local-ingested Jira context.
	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "GENERIC-1044", CreatedByRunID: "orchestrator-run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("start chain: %v", err)
	}
	if result.Status != ChainStatusQueued || result.InputRef != "jira:GENERIC-1044" || result.NextAction != "decomposition automation will run when planned tasks transition to ready" {
		t.Fatalf("start lost Jira input/status/next action: %#v", result)
	}
	for _, ref := range []string{
		"jira:GENERIC-1044",
		"jira-context:GENERIC-1044:summary",
		"jira-context:GENERIC-1044:scope",
		"jira-context:GENERIC-1044:implementation-evidence",
		"jira-context:GENERIC-1044:source-anchors",
		"jira-context:GENERIC-1044:verifier-scope",
	} {
		if !containsString(result.ContextRefs, ref) {
			t.Fatalf("start lost local-ingested context ref %q: %#v", ref, result.ContextRefs)
		}
	}
	assertChainStageHandoff(t, result.StageRuns[0], "decomposition", "workflow-decomposition", "plan-decomposition", "task-decomposition", "automation-decomposition", StageStatusQueued)
	if got, want := strings.Join(workPlans.events[:2], ","), "release:task-decomposition,activate:plan-decomposition"; got != want {
		t.Fatalf("decomposition activation handoff order mismatch: got %s want %s", got, want)
	}

	// Boundary: decomposition compile/activate completes and implementation queues.
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance after decomposition done: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get chain after decomposition: %v", err)
	}
	assertChainStageHandoff(t, run.StageRuns[0], "decomposition", "workflow-decomposition", "plan-decomposition", "task-decomposition", "automation-decomposition", StageStatusCompleted)
	assertChainStageHandoff(t, run.StageRuns[1], "implementation", "workflow-implementation", "plan-implementation", "task-implementation", "automation-implementation", StageStatusQueued)
	if got, want := strings.Join(workPlans.events[2:4], ","), "release:task-implementation,activate:plan-implementation"; got != want {
		t.Fatalf("implementation activation handoff order mismatch: got %s want %s", got, want)
	}

	// Boundary: implementation execute/closeout hands off to post-validation.
	// (The runner-level claim/execute/closeout state machine is pinned in
	// projectautomation's TestGenericWorkflowPipelineQueuesClaimsCompletesAndReviewsDependentHandoffs.)
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-implementation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance after implementation done: %v", err)
	}
	run, err = svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get chain after implementation: %v", err)
	}
	assertChainStageHandoff(t, run.StageRuns[1], "implementation", "workflow-implementation", "plan-implementation", "task-implementation", "automation-implementation", StageStatusCompleted)
	assertChainStageHandoff(t, run.StageRuns[2], "post-validation", "workflow-validation", "plan-post-validation", "task-post-validation", "automation-post-validation", StageStatusQueued)
	if got, want := strings.Join(workPlans.events[4:6], ","), "release:task-post-validation,activate:plan-post-validation"; got != want {
		t.Fatalf("post-validation activation handoff order mismatch: got %s want %s", got, want)
	}

	// Boundary: post-validation -> GitOps finalization -> persisted draft PR ref.
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("advance after post-validation done: %v", err)
	}
	run, err = svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get completed chain: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "github-pr-1044" || run.NextAction != "workflow chain completed with draft PR GitOps output" {
		t.Fatalf("completed chain lost final status/action/PR handoff: %#v", run)
	}
	if run.InputRef != "jira:GENERIC-1044" || run.CreatedByRunID != "orchestrator-run-1" || run.TraceID != "trace-1" {
		t.Fatalf("completed chain lost root refs: %#v", run)
	}
	if len(run.WorkPlanIDs) != 3 || len(run.AutomationIDs) != 3 || len(run.StageRuns) != 3 {
		t.Fatalf("completed chain lost plan/automation/stage refs: %#v", run)
	}
	assertChainStageHandoff(t, run.StageRuns[2], "post-validation", "workflow-validation", "plan-post-validation", "task-post-validation", "automation-post-validation", StageStatusCompleted)
	for _, ref := range []string{
		"jira-context:GENERIC-1044:summary",
		"jira-context:GENERIC-1044:scope",
		"jira-context:GENERIC-1044:implementation-evidence",
	} {
		if !containsString(run.ContextRefs, ref) {
			t.Fatalf("completed chain lost persisted Jira context ref %q: %#v", ref, run.ContextRefs)
		}
	}

	// Boundary: every stage compile preserved the local-ingested Jira handoff.
	if len(workflows.compileInputs) != 3 {
		t.Fatalf("expected all three stages compiled, got %#v", workflows.compileInputs)
	}
	for i, input := range workflows.compileInputs {
		if input.UserRequestRef != "jira:GENERIC-1044" || input.CreatedByRunID != "orchestrator-run-1" || input.TraceID != "trace-1" {
			t.Fatalf("compile input %d lost Jira/run/trace refs: %#v", i, input)
		}
		if !containsString(input.ContextPackRefs, "jira-context:GENERIC-1044:scope") || !containsString(input.ContextPackRefs, "jira-context:GENERIC-1044:implementation-evidence") {
			t.Fatalf("compile input %d lost local-ingested context refs: %#v", i, input.ContextPackRefs)
		}
	}

	// Boundary: Work Plan activation and Work Task release carried run/trace/safe-action metadata.
	if len(workPlans.statusUpdates) != 3 || len(workPlans.taskStatusUpdates) != 3 {
		t.Fatalf("expected one plan activation and one task release per stage, got plans=%#v tasks=%#v", workPlans.statusUpdates, workPlans.taskStatusUpdates)
	}
	for i := range workPlans.statusUpdates {
		if workPlans.statusUpdates[i].Status != projectworkplan.WorkPlanStatusActive || workPlans.statusUpdates[i].RunID != "orchestrator-run-1" || workPlans.statusUpdates[i].TraceID != "trace-1" || workPlans.statusUpdates[i].SafeNextAction == "" {
			t.Fatalf("plan activation %d lost run/trace/action metadata: %#v", i, workPlans.statusUpdates[i])
		}
		if workPlans.taskStatusUpdates[i].Status != projectworkplan.WorkTaskStatusReady || workPlans.taskStatusUpdates[i].RunID != "orchestrator-run-1" || workPlans.taskStatusUpdates[i].TraceID != "trace-1" || workPlans.taskStatusUpdates[i].SafeNextAction == "" {
			t.Fatalf("task release %d lost run/trace/action metadata: %#v", i, workPlans.taskStatusUpdates[i])
		}
	}

	// Boundary: GitOps finalization carried review/verifier refs and explicit edit scope.
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected exactly one GitOps finalization, got %#v", finalizer.inputs)
	}
	gitopsInput := finalizer.inputs[0]
	if gitopsInput.InputRef != "jira:GENERIC-1044" || gitopsInput.CreatedByRunID != "orchestrator-run-1" || gitopsInput.TraceID != "trace-1" {
		t.Fatalf("GitOps input lost Jira/run/trace refs: %#v", gitopsInput)
	}
	if gitopsInput.WorkPlan.ID != "plan-implementation" || len(gitopsInput.StageRuns) != 3 || len(gitopsInput.AutomationIDs) != 3 {
		t.Fatalf("GitOps input lost implementation plan or stage/automation refs: %#v", gitopsInput)
	}
	if !containsString(gitopsInput.ReviewRefs, "review:task-post-validation") || !containsString(gitopsInput.VerifierRefs, "verifier:task-post-validation") || !containsString(gitopsInput.TestResults, "task-post-validation verified by verifier:task-post-validation") {
		t.Fatalf("GitOps input lost validation review/verifier/test refs: %#v", gitopsInput)
	}
	if !containsString(gitopsInput.AllowedPathspecs, "internal/projectworkflowchain/service.go") || containsString(gitopsInput.AllowedPathspecs, "cmd/mivia-server") {
		t.Fatalf("GitOps input must use explicit implementation edit scope only: %#v", gitopsInput.AllowedPathspecs)
	}

	// Boundary: duplicate post-validation events stay idempotent.
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("idempotent post-validation event: %v", err)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected no duplicate GitOps finalization, got %d", len(finalizer.inputs))
	}
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

func mustBaselineJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal baseline JSON: %v", err)
	}
	return string(payload)
}

func jsonHasKey(payload string, key string) bool {
	var value map[string]any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return false
	}
	if _, ok := value[key]; ok {
		return true
	}
	for _, child := range value {
		switch typed := child.(type) {
		case map[string]any:
			if _, ok := typed[key]; ok {
				return true
			}
		case []any:
			for _, entry := range typed {
				if entryMap, ok := entry.(map[string]any); ok {
					if _, ok := entryMap[key]; ok {
						return true
					}
				}
			}
		}
	}
	return false
}
