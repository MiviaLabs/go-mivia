package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

func TestLadybugStorePersistentReopenAutomationRunGraph(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	graph, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	store := bootstrappedAutomationStore(t, ctx, graph)
	now := time.Unix(100, 0).UTC()

	automation := projectautomation.Automation{
		ID:                    "automation-1",
		ProjectID:             "project-a",
		AutomationRef:         "automation/ref/a",
		Title:                 "Implement task",
		Purpose:               "Run bounded worker task",
		Status:                projectautomation.AutomationStatusEnabled,
		AgentID:               "codex",
		PlanID:                "plan-1",
		AllowedTaskRefs:       []string{"task-1"},
		RequiredReviewTaskIDs: []string{"review-1"},
		TriggerKind:           projectautomation.TriggerKindAutomatic,
		SourceKind:            projectautomation.AutomationSourceWorkflow,
		PermissionRef:         "permission-1",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if _, err := store.CreateAutomation(ctx, automation); err != nil {
		t.Fatalf("create automation: %v", err)
	}
	run := projectautomation.AutomationRun{
		ID:                "run-1",
		ProjectID:         automation.ProjectID,
		AutomationID:      automation.ID,
		AgentID:           automation.AgentID,
		PlanID:            automation.PlanID,
		TaskID:            "task-1",
		Status:            projectautomation.RunStatusQueued,
		RunnerKind:        projectautomation.RunnerKindCodexCLI,
		OrchestratorRunID: "trigger-1",
		WorkerRunIDs:      []string{"worker-1"},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	attempt := projectautomation.AutomationAttempt{
		ID:                 "attempt-1",
		ProjectID:          automation.ProjectID,
		AutomationRunID:    run.ID,
		AttemptNumber:      1,
		RunnerKind:         projectautomation.RunnerKindCodexCLI,
		Status:             projectautomation.RunStatusCompleted,
		DurationMS:         123,
		VerifierResultRefs: []string{"verify-1"},
		CreatedAt:          now,
		FinishedAt:         now.Add(time.Second),
	}
	if _, err := store.CreateAttempt(ctx, attempt); err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	batch := projectautomation.AutomationParallelBatch{
		ID:                "batch-1",
		ProjectID:         automation.ProjectID,
		AutomationRunID:   run.ID,
		OrchestratorRunID: "trigger-1",
		PlanID:            automation.PlanID,
		TaskIDs:           []string{"task-1"},
		Status:            projectautomation.BatchStatusCompleted,
		SafetyReason:      "no overlapping files",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, err := store.CreateParallelBatch(ctx, batch); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if err := graph.Close(); err != nil {
		t.Fatalf("close graph: %v", err)
	}

	reopened, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	defer reopened.Close()
	reopenedStore := bootstrappedAutomationStore(t, ctx, reopened)
	gotRun, err := reopenedStore.GetRun(ctx, run.ProjectID, run.ID)
	if err != nil {
		t.Fatalf("get reopened run: %v", err)
	}
	if gotRun.AutomationID != automation.ID || gotRun.OrchestratorRunID != run.OrchestratorRunID || len(gotRun.WorkerRunIDs) != 1 {
		t.Fatalf("unexpected reopened run: %#v", gotRun)
	}
	runs, err := reopenedStore.ListRuns(ctx, projectautomation.RunFilter{ProjectID: automation.ProjectID, AutomationID: automation.ID, PlanID: automation.PlanID})
	if err != nil {
		t.Fatalf("list reopened runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("expected reopened run in list, got %#v", runs)
	}
	gotBatch, err := reopenedStore.GetParallelBatch(ctx, batch.ProjectID, batch.ID)
	if err != nil {
		t.Fatalf("get reopened batch: %v", err)
	}
	if len(gotBatch.TaskIDs) != 1 || gotBatch.TaskIDs[0] != "task-1" {
		t.Fatalf("unexpected reopened batch: %#v", gotBatch)
	}
	attemptNode, err := reopened.GetNode(ctx, labelProjectAutomationAttempt, graphID(attempt.ProjectID, attempt.ID))
	if err != nil {
		t.Fatalf("get reopened attempt node: %v", err)
	}
	if gotAttempt := nodeToAttempt(attemptNode); gotAttempt.DurationMS != attempt.DurationMS || len(gotAttempt.VerifierResultRefs) != 1 {
		t.Fatalf("unexpected reopened attempt: %#v", gotAttempt)
	}
}

func TestLadybugStoreRejectsDuplicateAutomationRefInProject(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedAutomationStore(t, ctx, graph)
	automation := projectautomation.Automation{
		ID:            "automation-1",
		ProjectID:     "project-a",
		AutomationRef: "automation/ref/a",
		Title:         "Implement task",
		Purpose:       "Run bounded worker task",
		Status:        projectautomation.AutomationStatusEnabled,
		AgentID:       "codex",
		TriggerKind:   projectautomation.TriggerKindAutomatic,
		PermissionRef: "permission-1",
		CreatedAt:     time.Unix(100, 0).UTC(),
		UpdatedAt:     time.Unix(100, 0).UTC(),
	}
	if _, err := store.CreateAutomation(ctx, automation); err != nil {
		t.Fatalf("create automation: %v", err)
	}
	automation.ID = "automation-2"
	if _, err := store.CreateAutomation(ctx, automation); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate automation ref, got %v", err)
	}
	automation.ProjectID = "project-b"
	if _, err := store.CreateAutomation(ctx, automation); err != nil {
		t.Fatalf("expected duplicate ref in another project to pass: %v", err)
	}
}

func TestLadybugStorePreservesAdvancedRunFromStaleRecoveryUpdate(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedAutomationStore(t, ctx, graph)
	now := time.Unix(100, 0).UTC()
	run := projectautomation.AutomationRun{
		ID:              "run-1",
		ProjectID:       "project-a",
		AutomationID:    "automation-1",
		AgentID:         "bug-fix-worker",
		PlanID:          "plan-1",
		TaskID:          "task-1",
		Status:          projectautomation.RunStatusVerifying,
		RunnerKind:      projectautomation.RunnerKindCodexCLI,
		SafeSummary:     "external_codex_cli_completed_verification_required",
		AttemptCount:    2,
		FailureCategory: "",
		CreatedAt:       now,
		UpdatedAt:       now.Add(time.Second),
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	stale := run
	stale.Status = projectautomation.RunStatusRunning
	stale.SafeSummary = projectautomation.RunSafeSummaryGitOpsPostTaskRecovery
	stale.FailureCategory = "gitops_post_task_failed"
	stale.AttemptCount = 3
	stale.StartedAt = now.Add(2 * time.Second)
	stale.FinishedAt = time.Time{}
	stale.UpdatedAt = stale.StartedAt

	got, err := store.UpdateRun(ctx, stale)
	if err != nil {
		t.Fatalf("update stale recovery run: %v", err)
	}
	if got.Status != projectautomation.RunStatusVerifying || got.SafeSummary != run.SafeSummary || got.AttemptCount != run.AttemptCount {
		t.Fatalf("expected advanced run to be preserved, got %#v", got)
	}
	persisted, err := store.GetRun(ctx, run.ProjectID, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if persisted.Status != projectautomation.RunStatusVerifying || persisted.AttemptCount != run.AttemptCount {
		t.Fatalf("expected persisted run to stay advanced, got %#v", persisted)
	}
}

func TestLadybugStorePersistsFirstExternalClaimForQueuedPostReviewRun(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedAutomationStore(t, ctx, graph)
	now := time.Unix(100, 0).UTC()
	run := projectautomation.AutomationRun{
		ID:           "run-review",
		ProjectID:    "project-a",
		AutomationID: "automation-review",
		AgentID:      "codex-reviewer",
		PlanID:       "plan-1",
		TaskID:       "review-task-1",
		Status:       projectautomation.RunStatusQueued,
		RunnerKind:   projectautomation.RunnerKindCodexCLI,
		SafeSummary:  projectautomation.RunSafeSummaryPostImplementationReviewQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := store.CreateRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	claimed := run
	claimed.Status = projectautomation.RunStatusRunning
	claimed.ClaimID = "claim-review"
	claimed.RunnerID = "runner-1"
	claimed.ClaimedAt = now.Add(time.Second)
	claimed.LastHeartbeatAt = claimed.ClaimedAt
	claimed.LeaseExpiresAt = claimed.ClaimedAt.Add(90 * time.Second)
	claimed.StartedAt = claimed.ClaimedAt
	claimed.UpdatedAt = claimed.ClaimedAt

	got, err := store.UpdateRun(ctx, claimed)
	if err != nil {
		t.Fatalf("update claimed review run: %v", err)
	}
	if got.Status != projectautomation.RunStatusRunning || got.ClaimID != claimed.ClaimID || got.RunnerID != claimed.RunnerID {
		t.Fatalf("expected first external claim to persist, got %#v", got)
	}
	persisted, err := store.GetRun(ctx, run.ProjectID, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if persisted.Status != projectautomation.RunStatusRunning || persisted.ClaimID != claimed.ClaimID || persisted.RunnerID != claimed.RunnerID {
		t.Fatalf("expected persisted run to keep claim fields, got %#v", persisted)
	}
}

func bootstrappedAutomationStore(t *testing.T, ctx context.Context, graph ladybug.Graph) *LadybugStore {
	t.Helper()
	store, err := NewBootstrappedLadybugStore(ctx, graph)
	if err != nil {
		t.Fatalf("bootstrap store: %v", err)
	}
	return store
}
