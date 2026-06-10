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
		InputKindSafeRef,
	}, []string{"jira_issue_key", "safe_ref"})

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
