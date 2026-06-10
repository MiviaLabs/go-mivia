package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
)

func TestLadybugStorePersistsChainRunsAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	first := NewLadybugStore(graph)
	run := testRun("run-1", projectworkflowchain.ChainStatusQueued)
	run.GitOpsReady = true
	run.GitOpsAttemptCount = 2
	run.GitOpsFailureCategory = "gitops_verification_failed_abcdef123456"
	run.GitOpsFailureEvidenceRefs = []string{"gitops-failure:gitops_verification_failed_abcdef123456", "gitops-attempt:2"}
	run.GitOpsRecoveryStatus = projectworkflowchain.GitOpsRecoveryStatusRepairable
	run.PullRequestRef = "pr/GENERIC-1044"
	run.NextAction = "workflow chain completed with draft PR GitOps output"
	run.AutomationIDs = []string{"automation-decomposition", "automation-implementation", "automation-validation"}
	run.StageRuns[0].Status = projectworkflowchain.StageStatusCompleted
	run.StageRuns[0].AutomationIDs = []string{"automation-decomposition"}
	run.StageRuns[0].WorkflowID = "workflow-decomposition"
	run.StageRuns[0].CompletedAt = time.Unix(101, 0).UTC()
	run.StageRuns = append(run.StageRuns, projectworkflowchain.StageRun{
		StageRef:      "post-validation",
		WorkflowRef:   "governed-post-implementation-validation",
		WorkflowID:    "workflow-validation",
		Status:        projectworkflowchain.StageStatusBlocked,
		WorkPlanID:    "plan-post-validation",
		WorkTaskIDs:   []string{"task-post-validation"},
		AutomationIDs: []string{"automation-validation"},
		StartedAt:     time.Unix(102, 0).UTC(),
		CompletedAt:   time.Unix(103, 0).UTC(),
		BlockedCode:   projectworkflowchain.BlockedCodeActivationFailed,
		BlockedReason: "gitops_finalize_failed_gitops_runtime_failed",
	})
	run.WorkPlanIDs = append(run.WorkPlanIDs, "plan-post-validation")

	if _, err := first.CreateChainRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	second := NewLadybugStore(graph)
	found, err := second.GetChainRun(ctx, "project-1", "run-1")
	if err != nil {
		t.Fatalf("get run through fresh store: %v", err)
	}
	if found.ID != "run-1" || !found.GitOpsReady || found.StageRuns[0].WorkTaskIDs[0] != "task-decomposition" {
		t.Fatalf("unexpected persisted run: %#v", found)
	}
	if found.PullRequestRef != run.PullRequestRef || found.NextAction != run.NextAction || len(found.AutomationIDs) != 3 {
		t.Fatalf("persisted chain lost GitOps handoff refs/actions: %#v", found)
	}
	if found.GitOpsAttemptCount != 2 || found.GitOpsFailureCategory != run.GitOpsFailureCategory || found.GitOpsRecoveryStatus != projectworkflowchain.GitOpsRecoveryStatusRepairable || len(found.GitOpsFailureEvidenceRefs) != 2 {
		t.Fatalf("persisted chain lost GitOps recovery metadata: %#v", found)
	}
	if len(found.StageRuns) != 2 || found.StageRuns[1].BlockedCode != projectworkflowchain.BlockedCodeActivationFailed || found.StageRuns[1].BlockedReason != "gitops_finalize_failed_gitops_runtime_failed" || found.StageRuns[1].CompletedAt.IsZero() || len(found.StageRuns[1].AutomationIDs) != 1 {
		t.Fatalf("persisted chain lost stage handoff metadata: %#v", found.StageRuns)
	}
}

func TestLadybugStoreFindsRunsByWorkPlanAndFilters(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewLadybugStore(graph)
	if _, err := store.CreateChainRun(ctx, testRun("run-1", projectworkflowchain.ChainStatusQueued)); err != nil {
		t.Fatalf("create run: %v", err)
	}

	found, err := store.FindChainRunByWorkPlan(ctx, "project-1", "plan-decomposition")
	if err != nil {
		t.Fatalf("find by work plan: %v", err)
	}
	if found.ID != "run-1" {
		t.Fatalf("unexpected found run: %#v", found)
	}
	runs, err := store.ListChainRuns(ctx, projectworkflowchain.ChainFilter{ProjectID: "project-1", ChainRef: "chain-1", Status: projectworkflowchain.ChainStatusQueued})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("unexpected filtered runs: %#v", runs)
	}
	if _, err := store.FindChainRunByWorkPlan(ctx, "project-1", "missing-plan"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing work plan not found, got %v", err)
	}
}
