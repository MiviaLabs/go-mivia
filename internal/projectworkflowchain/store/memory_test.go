package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
)

func TestMemoryStoreIndexesWorkPlansAndFiltersRuns(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	run := testRun("run-1", "queued")

	created, err := store.CreateChainRun(ctx, run)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	created.StageRuns[0].WorkTaskIDs[0] = "mutated"

	found, err := store.FindChainRunByWorkPlan(ctx, "project-1", "plan-decomposition")
	if err != nil {
		t.Fatalf("find by work plan: %v", err)
	}
	if found.ID != "run-1" || found.StageRuns[0].WorkTaskIDs[0] != "task-decomposition" {
		t.Fatalf("expected indexed cloned run, got %#v", found)
	}

	runs, err := store.ListChainRuns(ctx, projectworkflowchain.ChainFilter{ProjectID: "project-1", ChainRef: "chain-1", Status: "queued"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("unexpected filtered runs: %#v", runs)
	}
}

func TestMemoryStoreUpdateReindexesWorkPlans(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	run := testRun("run-1", "queued")
	if _, err := store.CreateChainRun(ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	run.StageRuns[0].WorkPlanID = "plan-implementation"
	run.WorkPlanIDs = []string{"plan-implementation"}
	if _, err := store.UpdateChainRun(ctx, run); err != nil {
		t.Fatalf("update run: %v", err)
	}

	if _, err := store.FindChainRunByWorkPlan(ctx, "project-1", "plan-decomposition"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected old plan index removed, got %v", err)
	}
	found, err := store.FindChainRunByWorkPlan(ctx, "project-1", "plan-implementation")
	if err != nil {
		t.Fatalf("find new plan index: %v", err)
	}
	if found.ID != "run-1" {
		t.Fatalf("unexpected run for new plan index: %#v", found)
	}
}

func testRun(id string, status string) projectworkflowchain.ChainRun {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	return projectworkflowchain.ChainRun{
		ID:          id,
		ProjectID:   "project-1",
		ChainRef:    "chain-1",
		InputRef:    "jira:MASS-1044",
		Status:      status,
		WorkPlanIDs: []string{"plan-decomposition"},
		StageRuns: []projectworkflowchain.StageRun{{
			StageRef:    "decomposition",
			WorkflowRef: "governed-decomposition-planning",
			Status:      projectworkflowchain.StageStatusQueued,
			WorkPlanID:  "plan-decomposition",
			WorkTaskIDs: []string{"task-decomposition"},
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
}
