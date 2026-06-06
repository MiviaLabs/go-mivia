package store

import (
	"context"
	"errors"
	"testing"

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
