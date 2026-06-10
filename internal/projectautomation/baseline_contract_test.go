package projectautomation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestBaselineAutomationLifecycleContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "automation statuses", []string{
		AutomationStatusDraft,
		AutomationStatusEnabled,
		AutomationStatusDisabled,
		AutomationStatusRunning,
		AutomationStatusPaused,
		AutomationStatusFailed,
		AutomationStatusCancelled,
		AutomationStatusSuperseded,
	}, []string{"draft", "enabled", "disabled", "running", "paused", "failed", "cancelled", "superseded"})

	assertExactSet(t, "automation run statuses", []string{
		RunStatusQueued,
		RunStatusClaiming,
		RunStatusStarting,
		RunStatusRunning,
		RunStatusVerifying,
		RunStatusCompleted,
		RunStatusFailed,
		RunStatusBlocked,
		RunStatusCancelled,
		RunStatusPolicyDenied,
		RunStatusRunnerUnavailable,
		RunStatusTimeout,
	}, []string{"queued", "claiming", "starting", "running", "verifying", "completed", "failed", "blocked", "cancelled", "policy_denied", "runner_unavailable", "timeout"})

	assertExactSet(t, "parallel batch statuses", []string{
		BatchStatusPlanned,
		BatchStatusRunning,
		BatchStatusCompleted,
		BatchStatusFailed,
		BatchStatusBlocked,
		BatchStatusCancelled,
	}, []string{"planned", "running", "completed", "failed", "blocked", "cancelled"})
}

func TestBaselineAutomationPolicyAndRunnerContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "runner kinds", []string{
		RunnerKindCodexCLI,
		RunnerKindManual,
	}, []string{"codex_cli", "manual"})
	assertExactSet(t, "runner execution modes", []string{
		RunnerExecutionInProcess,
		RunnerExecutionExternal,
		RunnerExecutionManaged,
	}, []string{"in_process", "external", "managed"})
	assertExactSet(t, "trigger kinds", []string{
		TriggerKindManual,
		TriggerKindAutomatic,
	}, []string{"manual", "automatic"})
	assertExactSet(t, "source kinds", []string{
		AutomationSourceManual,
		AutomationSourceWorkflow,
	}, []string{"manual", "workflow"})
}

func TestBaselineAutomationRunOperationJSONContract(t *testing.T) {
	t.Parallel()

	run := AutomationRun{
		ID:              "automation_run_1",
		ProjectID:       "project-1",
		AutomationID:    "automation_1",
		AgentID:         "worker",
		PlanID:          "work_plan_1",
		TaskID:          "work_task_1",
		WorkTaskStatus:  "in_progress",
		Status:          RunStatusRunning,
		RunnerKind:      RunnerKindCodexCLI,
		AttemptCount:    1,
		FailureCategory: "governed_closeout_invalid_json",
		SafeSummary:     "metadata_only_failure",
		ClaimID:         "claim-1",
		RunnerID:        "runner-1",
	}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal run: %v", err)
	}
	for _, key := range []string{
		"id", "project_id", "automation_id", "agent_id", "plan_id", "task_id",
		"work_task_status", "status", "runner_kind", "attempt_count",
		"failure_category", "safe_summary", "claim_id", "runner_id",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("automation run JSON omitted %q: %s", key, data)
		}
	}

	input := CompleteAttemptInput{
		ProjectID:          "project-1",
		RunID:              "automation_run_1",
		ClaimID:            "claim-1",
		RunnerID:           "runner-1",
		Status:             RunStatusCompleted,
		VerifierResultRefs: []string{"verifier:focused"},
		EvidenceRefs:       []string{"evidence:contract"},
		ClaimRefs:          []string{"claim:baseline"},
		ReviewRefs:         []string{"review:independent"},
		KnowledgeRefs:      []string{"knowledge:no_reusable"},
	}
	data, err = json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal complete attempt input: %v", err)
	}
	got = map[string]any{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal complete attempt input: %v", err)
	}
	for _, key := range []string{
		"project_id", "run_id", "claim_id", "runner_id", "status",
		"verifier_result_refs", "evidence_refs", "claim_refs",
		"review_result_refs", "knowledge_candidate_refs",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("complete attempt JSON omitted %q: %s", key, data)
		}
	}
}

func TestBaselineAutomationStatusTransitionBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := New(newTestStore(), &fakeWorkTasks{}, Options{Enabled: true, RunnerEnabled: true})
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:     "project-1",
		AutomationRef: "auto/status-transition",
		Title:         "Automation status transition",
		Purpose:       "Lock current automation status updates",
		Status:        AutomationStatusDraft,
		AgentID:       "worker",
		TriggerKind:   TriggerKindManual,
		PermissionRef: "permission_snapshot:status-transition",
	})
	if err != nil {
		t.Fatalf("create automation: %v", err)
	}
	for _, status := range []string{
		AutomationStatusEnabled,
		AutomationStatusRunning,
		AutomationStatusPaused,
		AutomationStatusDisabled,
		AutomationStatusFailed,
		AutomationStatusCancelled,
		AutomationStatusSuperseded,
	} {
		updated, err := svc.UpdateAutomationStatus(ctx, UpdateAutomationStatusInput{
			ProjectID:    "project-1",
			AutomationID: automation.ID,
			Status:       status,
			RunID:        "run-status-transition",
			TraceID:      "trace-status-transition",
		})
		if err != nil {
			t.Fatalf("update automation to %s: %v", status, err)
		}
		if updated.Status != status || updated.TraceID != "trace-status-transition" {
			t.Fatalf("automation status update lost transition metadata: %#v", updated)
		}
		automation = updated
	}
	if _, err := svc.UpdateAutomationStatus(ctx, UpdateAutomationStatusInput{
		ProjectID:    "project-1",
		AutomationID: automation.ID,
		Status:       "unsafe status",
	}); err == nil {
		t.Fatal("expected unsafe automation status to be rejected")
	}
}

func TestBaselineAutomationRunClaimLeaseHeartbeatAndCompletionBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore()
	task := readyTask("work_task_1", "task-implementation", []string{"internal/projectautomation"})
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	svc := New(store, fake, Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  RunnerExecutionExternal,
		MaxParallelTasks: 1,
		PermissionResolver: &fakePermissionResolver{metadata: PermissionSnapshotMetadata{
			PermissionRef:      "permission_snapshot:snapshot-worker",
			AllowedRunnerKinds: []string{RunnerKindCodexCLI},
		}},
	})
	svc.newID = deterministicAutomationIDs("automation_1", "automation_run_1", "claim_1", "attempt_1")
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/phase0",
		Title:           "Phase 0 automation",
		Purpose:         "Run current automation baseline",
		Status:          AutomationStatusEnabled,
		AgentID:         "worker",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SourceKind:      AutomationSourceWorkflow,
		PermissionRef:   "permission_snapshot:snapshot-worker",
	})
	if err != nil {
		t.Fatalf("create automation: %v", err)
	}
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{
		ProjectID:    "project-1",
		AutomationID: automation.ID,
		PlanID:       "plan-1",
		TaskID:       task.ID,
		RunnerKind:   RunnerKindCodexCLI,
	})
	if err != nil {
		t.Fatalf("submit run: %v", err)
	}
	if queued.Status != RunStatusQueued || queued.RunnerKind != RunnerKindCodexCLI || queued.WorkTaskStatus != projectworkplan.WorkTaskStatusReady {
		t.Fatalf("submit did not persist queued run contract: %#v", queued)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "worker", RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("claim next run: %v", err)
	}
	if claimed.Run.ID != queued.ID || claimed.Run.Status != RunStatusRunning || claimed.Run.ClaimID == "" || claimed.Run.RunnerID != "runner-1" || claimed.Run.LeaseExpiresAt.IsZero() {
		t.Fatalf("claim did not persist lease metadata: %#v", claimed.Run)
	}
	if claimed.CodexInput.ProjectID != "project-1" || claimed.CodexInput.AutomationRunID != queued.ID || claimed.CodexInput.TaskID != task.ID || claimed.TimeoutMS <= 0 {
		t.Fatalf("claim did not return complete runner handoff: %#v", claimed)
	}
	if _, err := svc.HeartbeatRun(ctx, HeartbeatRunInput{ProjectID: "project-1", RunID: claimed.Run.ID, ClaimID: "wrong-claim", RunnerID: "runner-1"}); err == nil || !strings.Contains(err.Error(), "claim_id does not match") {
		t.Fatalf("expected heartbeat to reject wrong claim, got %v", err)
	}
	if _, err := svc.HeartbeatRun(ctx, HeartbeatRunInput{ProjectID: "project-1", RunID: claimed.Run.ID, ClaimID: claimed.Run.ClaimID, RunnerID: "wrong-runner"}); err == nil || !strings.Contains(err.Error(), "runner_id does not match") {
		t.Fatalf("expected heartbeat to reject wrong runner, got %v", err)
	}
	heartbeat, err := svc.HeartbeatRun(ctx, HeartbeatRunInput{ProjectID: "project-1", RunID: claimed.Run.ID, ClaimID: claimed.Run.ClaimID, RunnerID: "runner-1"})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if heartbeat.Status != RunStatusRunning || heartbeat.LastHeartbeatAt.IsZero() || !heartbeat.LeaseExpiresAt.After(claimed.Run.LeaseExpiresAt) {
		t.Fatalf("heartbeat did not extend running lease: before=%#v after=%#v", claimed.Run, heartbeat)
	}
	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: "project-1", RunID: claimed.Run.ID, Status: RunStatusCompleted, ClaimID: "wrong-claim", RunnerID: "runner-1"}); err == nil || !strings.Contains(err.Error(), "claim_id does not match") {
		t.Fatalf("expected complete_attempt to reject wrong claim, got %v", err)
	}
	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: "project-1", RunID: claimed.Run.ID, Status: RunStatusCompleted, ClaimID: claimed.Run.ClaimID, RunnerID: "wrong-runner"}); err == nil || !strings.Contains(err.Error(), "runner_id does not match") {
		t.Fatalf("expected complete_attempt to reject wrong runner, got %v", err)
	}
	completed, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:          "project-1",
		RunID:              claimed.Run.ID,
		Status:             RunStatusCompleted,
		ClaimID:            claimed.Run.ClaimID,
		RunnerID:           "runner-1",
		DurationMS:         1234,
		VerifierResultRefs: []string{"verifier:phase0"},
		EvidenceRefs:       []string{"evidence:phase0"},
		ClaimRefs:          []string{"claim:phase0"},
		ReviewRefs:         []string{"review:phase0"},
		KnowledgeRefs:      []string{"knowledge:no_reusable"},
	})
	if err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	if completed.Status != RunStatusVerifying || completed.FinishedAt.IsZero() || completed.ClaimID != claimed.Run.ClaimID || completed.RunnerID != "runner-1" {
		t.Fatalf("complete_attempt did not persist verifying handoff state: %#v", completed)
	}
	attempts := store.attempts
	if len(attempts) != 1 {
		t.Fatalf("expected one attempt record, got %#v", attempts)
	}
	for _, attempt := range attempts {
		if attempt.Status != RunStatusCompleted || attempt.DurationMS != 1234 || !containsString(attempt.VerifierResultRefs, "verifier:phase0") || !containsString(attempt.EvidenceRefs, "evidence:phase0") || !containsString(attempt.ClaimRefs, "claim:phase0") || !containsString(attempt.KnowledgeRefs, "knowledge:no_reusable") {
			t.Fatalf("attempt lost safe closeout refs: %#v", attempt)
		}
	}
	duplicate, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID: "project-1",
		RunID:     claimed.Run.ID,
		Status:    RunStatusCompleted,
		ClaimID:   claimed.Run.ClaimID,
		RunnerID:  "runner-1",
	})
	if err != nil {
		t.Fatalf("duplicate complete_attempt: %v", err)
	}
	if duplicate.ID != completed.ID || duplicate.Status != completed.Status {
		t.Fatalf("duplicate complete_attempt was not idempotent: before=%#v after=%#v", completed, duplicate)
	}
}

func TestBaselineAutomationRunMissingRunnerIDCurrentBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore()
	task := readyTask("work_task_missing_runner", "task-missing-runner", []string{"internal/projectautomation"})
	fake := &fakeWorkTasks{
		plans: map[string]projectworkplan.WorkPlan{
			"plan-1": {ID: "plan-1", ProjectID: "project-1", Status: projectworkplan.WorkPlanStatusActive},
		},
		tasks: map[string]projectworkplan.WorkTask{task.ID: task},
	}
	svc := New(store, fake, Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  RunnerExecutionExternal,
		MaxParallelTasks: 1,
		PermissionResolver: &fakePermissionResolver{metadata: PermissionSnapshotMetadata{
			PermissionRef:      "permission_snapshot:snapshot-worker",
			AllowedRunnerKinds: []string{RunnerKindCodexCLI},
		}},
	})
	svc.newID = deterministicAutomationIDs("automation_1", "automation_run_1", "claim_1", "attempt_1")
	automation, err := svc.CreateAutomation(ctx, CreateAutomationInput{
		ProjectID:       "project-1",
		AutomationRef:   "auto/missing-runner",
		Title:           "Missing runner baseline",
		Purpose:         "Lock current missing runner behavior",
		Status:          AutomationStatusEnabled,
		AgentID:         "worker",
		PlanID:          "plan-1",
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     TriggerKindAutomatic,
		SourceKind:      AutomationSourceWorkflow,
		PermissionRef:   "permission_snapshot:snapshot-worker",
	})
	if err != nil {
		t.Fatalf("create automation: %v", err)
	}
	if _, err := svc.SubmitRun(ctx, SubmitRunInput{
		ProjectID:    "project-1",
		AutomationID: automation.ID,
		PlanID:       "plan-1",
		TaskID:       task.ID,
		RunnerKind:   RunnerKindCodexCLI,
	}); err != nil {
		t.Fatalf("submit run: %v", err)
	}

	claimed, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: "project-1", AgentID: "worker", RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("claim without runner_id should match current behavior: %v", err)
	}
	if claimed.Run.RunnerID != "" || claimed.Run.ClaimID == "" {
		t.Fatalf("claim without runner_id should persist claim token and empty runner_id: %#v", claimed.Run)
	}
	completed, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{
		ProjectID:          "project-1",
		RunID:              claimed.Run.ID,
		Status:             RunStatusCompleted,
		ClaimID:            "wrong-claim",
		VerifierResultRefs: []string{"verifier:missing-runner"},
		EvidenceRefs:       []string{"evidence:missing-runner"},
	})
	if err != nil {
		t.Fatalf("complete without runner_id and mismatched claim should match current behavior: %v", err)
	}
	if completed.Status != RunStatusVerifying || completed.RunnerID != "" {
		t.Fatalf("complete without runner_id changed current behavior: %#v", completed)
	}
}

func TestClaimNextRunReclaimsExpiredLeaseAndRejectsStaleCompletion(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	task := readyTask("task-lease-expiry", "lease-expiry-task", []string{"internal/foo.go"})
	fake := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{task.ID: task}}
	svc := New(store, fake, Options{Enabled: true, RunnerEnabled: true, RunnerExecution: RunnerExecutionExternal})
	// Anchor the injected clock after svc.startedAt so only lease expiry (not
	// the started-before-service reclamation path) drives the behavior.
	now := svc.startedAt.Add(time.Minute)
	svc.now = func() time.Time { return now }
	automation := createAutomaticTriggerAutomation(t, ctx, svc)
	queued, err := svc.SubmitRun(ctx, SubmitRunInput{ProjectID: automation.ProjectID, AutomationID: automation.ID, TaskID: task.ID, RunnerKind: RunnerKindCodexCLI})
	if err != nil {
		t.Fatalf("SubmitRun returned error: %v", err)
	}

	claimedA, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-a"})
	if err != nil {
		t.Fatalf("ClaimNextRun runner-a returned error: %v", err)
	}
	if claimedA.Run.ID != queued.ID || claimedA.Run.Status != RunStatusRunning || claimedA.Run.RunnerID != "runner-a" || claimedA.Run.ClaimID == "" || claimedA.Run.LeaseExpiresAt.IsZero() {
		t.Fatalf("expected runner-a to claim queued run with lease, got %#v", claimedA.Run)
	}

	// While runner-a's lease is still active, another runner gets nothing.
	now = claimedA.Run.LeaseExpiresAt.Add(-time.Second)
	if _, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-b"}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no claimable run while runner-a lease is active, got %v", err)
	}

	// Advance the injected clock past runner-a's lease expiry.
	now = claimedA.Run.LeaseExpiresAt.Add(time.Second)

	claimedB, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-b"})
	if err != nil {
		t.Fatalf("ClaimNextRun runner-b returned error: %v", err)
	}
	if claimedB.Run.ID == claimedA.Run.ID {
		t.Fatalf("current behavior queues a replacement run for the abandoned task instead of re-claiming run %q, got %#v", claimedA.Run.ID, claimedB.Run)
	}
	if claimedB.Run.TaskID != task.ID || claimedB.Run.Status != RunStatusRunning || claimedB.Run.RunnerID != "runner-b" || claimedB.Run.ClaimID == "" || claimedB.Run.ClaimID == claimedA.Run.ClaimID {
		t.Fatalf("expected runner-b to hold a fresh claim on the same task, got %#v", claimedB.Run)
	}
	abandoned, err := store.GetRun(ctx, automation.ProjectID, claimedA.Run.ID)
	if err != nil {
		t.Fatalf("GetRun abandoned returned error: %v", err)
	}
	if abandoned.Status != RunStatusTimeout || abandoned.FailureCategory != "external_runner_interrupted" {
		t.Fatalf("expected abandoned run to time out as interrupted, got %#v", abandoned)
	}
	if got := fake.tasks[task.ID].ClaimedByRunID; got != claimedB.Run.ID {
		t.Fatalf("expected task to be owned by runner-b's run, got claimed_by=%q", got)
	}

	// Runner-a's late heartbeat with the stale claim token fails safely.
	if _, err := svc.HeartbeatRun(ctx, HeartbeatRunInput{ProjectID: automation.ProjectID, RunID: claimedA.Run.ID, ClaimID: claimedA.Run.ClaimID, RunnerID: "runner-a"}); err == nil || !strings.Contains(err.Error(), "automation run is not active") {
		t.Fatalf("expected stale heartbeat to be rejected, got %v", err)
	}
	// Runner-a's late CompleteAttempt with the stale claim token is rejected.
	if _, err := svc.CompleteAttempt(ctx, CompleteAttemptInput{ProjectID: automation.ProjectID, RunID: claimedA.Run.ID, Status: RunStatusFailed, FailureCategory: "external_runner_interrupted", ClaimID: claimedA.Run.ClaimID, RunnerID: "runner-a"}); err == nil || !strings.Contains(err.Error(), "automation run is not externally claimed") {
		t.Fatalf("expected stale completion to be rejected, got %v", err)
	}

	// The expired lease is reclaimed exactly once: a third claim while
	// runner-b's fresh lease is active finds nothing.
	if claimedC, err := svc.ClaimNextRun(ctx, ClaimNextRunInput{ProjectID: automation.ProjectID, RunnerKind: RunnerKindCodexCLI, RunnerID: "runner-c"}); err == nil || !strings.Contains(err.Error(), "no queued automation run") {
		t.Fatalf("expected no claimable run for runner-c while runner-b lease is active, got run=%#v err=%v", claimedC.Run, err)
	}

	// No attempt records exist: claims do not record attempts and the stale
	// completion was rejected before recording one.
	if len(store.attempts) != 0 {
		t.Fatalf("expected no attempt records after rejected stale completion, got %#v", store.attempts)
	}
	if len(store.runs) != 2 {
		t.Fatalf("expected exactly the abandoned run and its replacement, got %#v", store.runs)
	}
}

func TestBaselineRemediationFromConfirmedFindingContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workTasks := &fakeWorkTasks{tasks: map[string]projectworkplan.WorkTask{}}
	svc := New(newTestStore(), workTasks, Options{
		Enabled:       true,
		RunnerEnabled: true,
		WorkPlanStatusTrigger: WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})

	_, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding:phase0",
		FindingStatus:           "suspected",
		Title:                   "Fix suspected finding",
		Summary:                 "Repair only confirmed findings.",
		PermissionSnapshotRef:   "permission_snapshot:remediation",
		VerificationRequirement: "Run focused regression tests.",
	})
	if err == nil || !strings.Contains(err.Error(), "finding_status must be confirmed") {
		t.Fatalf("expected remediation to reject unconfirmed finding, got %v", err)
	}

	result, err := svc.CreateRemediationFromFinding(ctx, CreateRemediationFromFindingInput{
		ProjectID:               "project-1",
		FindingRef:              "finding:phase0",
		FindingStatus:           "confirmed",
		Title:                   "Fix confirmed finding",
		Summary:                 "Repair the confirmed Phase 0 finding.",
		Severity:                "high",
		ImplementationAgentID:   "worker-a",
		PermissionSnapshotRef:   "permission_snapshot:remediation",
		GitBaseRef:              "main",
		GitBranchRef:            "fix-GENERIC-0000-phase0",
		GitWorktreeRef:          "wt-GENERIC-0000-phase0",
		FilesToRead:             []string{"internal/projectautomation/service.go"},
		FilesToEdit:             []string{"internal/projectautomation/service.go"},
		EvidenceRefs:            []string{"review:confirmed"},
		VerificationRequirement: "Run focused regression tests.",
		ActivatePlan:            true,
	})
	if err != nil {
		t.Fatalf("CreateRemediationFromFinding confirmed: %v", err)
	}
	if !result.Activated || result.WorkPlan.Status != projectworkplan.WorkPlanStatusActive {
		t.Fatalf("expected active remediation plan, got activated=%v plan=%#v", result.Activated, result.WorkPlan)
	}
	if result.WorkTask.Status != projectworkplan.WorkTaskStatusReady || result.WorkTask.OwnerAgent != "worker-a" || result.WorkTask.DecompositionQuality != "ready" {
		t.Fatalf("expected isolated ready remediation task, got %#v", result.WorkTask)
	}
	if result.ReviewTask.ID == "" || result.ReviewTask.OwnerAgent == "" || result.ReviewTask.OwnerAgent == result.WorkTask.OwnerAgent {
		t.Fatalf("expected independent remediation review task, got implementation=%q review=%#v", result.WorkTask.OwnerAgent, result.ReviewTask)
	}
	if result.Automation.Status != AutomationStatusEnabled || result.Automation.SourceKind != AutomationSourceWorkflow || result.Automation.PermissionRef != "permission_snapshot:remediation" {
		t.Fatalf("expected workflow remediation automation with permission snapshot, got %#v", result.Automation)
	}
	if result.ReviewAutomation.SchedulePolicy != "post_implementation_review" || result.ReviewAutomation.PermissionRef != "permission_snapshot:remediation" {
		t.Fatalf("expected post-implementation review automation with permission snapshot, got %#v", result.ReviewAutomation)
	}
	for _, value := range []string{result.WorkTask.VerificationRequirement, result.WorkTask.ExpectedOutput, result.WorkTask.FailureCriteria} {
		if !strings.Contains(value, "regression test") {
			t.Fatalf("remediation contract must require regression-test consideration, got %q", value)
		}
	}
	for _, forbidden := range []string{"raw_prompt", "raw_completion", "provider_payload", "api_key"} {
		if strings.Contains(result.WorkPlan.GoalSummary, forbidden) || strings.Contains(result.WorkTask.Description, forbidden) || strings.Contains(result.Automation.Purpose, forbidden) {
			t.Fatalf("remediation metadata leaked forbidden material %q", forbidden)
		}
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
