package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
)

func TestLadybugStorePersistsWorkflowsAndPermissionSnapshotsAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	first := NewLadybugStore(graph)
	workflow := testLadybugWorkflow("workflow-1", "compile-and-verify")
	snapshot := testLadybugPermissionSnapshot("snapshot-1", workflow.ID, "implementation-worker")
	workflow.PermissionSnapshots = []projectworkflow.WorkflowPermissionSnapshot{snapshot}

	if _, err := first.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, err := first.CreatePermissionSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	second := NewLadybugStore(graph)
	foundWorkflow, err := second.GetWorkflow(ctx, "project-1", "workflow-1")
	if err != nil {
		t.Fatalf("get workflow through fresh store: %v", err)
	}
	if foundWorkflow.WorkflowRef != "compile-and-verify" || foundWorkflow.Steps[0].FilesToEdit[0] != "internal/service.go" || foundWorkflow.PermissionSnapshots[0].ID != "snapshot-1" {
		t.Fatalf("persisted workflow lost payload: %#v", foundWorkflow)
	}
	foundSnapshot, err := second.GetPermissionSnapshot(ctx, "project-1", "snapshot-1")
	if err != nil {
		t.Fatalf("get snapshot through fresh store: %v", err)
	}
	if foundSnapshot.AgentID != "implementation-worker" || foundSnapshot.AllowedCommands[0] != "go test ./..." || foundSnapshot.WorkspaceMode != "dedicated_worktree" {
		t.Fatalf("persisted snapshot lost permission payload: %#v", foundSnapshot)
	}
}

func TestLadybugStoreFiltersAndUpdatesWorkflowPermissionSnapshots(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewLadybugStore(graph)
	workflow := testLadybugWorkflow("workflow-1", "compile-and-verify")
	if _, err := store.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	snapshot := testLadybugPermissionSnapshot("snapshot-1", workflow.ID, "implementation-worker")
	if _, err := store.CreatePermissionSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	workflows, err := store.ListWorkflows(ctx, WorkflowFilter{ProjectID: "project-1", WorkflowRef: "compile-and-verify", Status: projectworkflow.WorkflowStatusEnabled})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(workflows) != 1 || workflows[0].ID != workflow.ID {
		t.Fatalf("unexpected workflow filter result: %#v", workflows)
	}
	snapshots, err := store.ListPermissionSnapshots(ctx, PermissionSnapshotFilter{ProjectID: "project-1", WorkflowID: workflow.ID, AgentID: "implementation-worker"})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != snapshot.ID {
		t.Fatalf("unexpected snapshot filter result: %#v", snapshots)
	}

	snapshot.AllowedCommands = append(snapshot.AllowedCommands, "go test ./internal/projectworkflow")
	if _, err := store.UpdatePermissionSnapshot(ctx, snapshot); err != nil {
		t.Fatalf("update snapshot: %v", err)
	}
	updated, err := store.GetPermissionSnapshot(ctx, "project-1", "snapshot-1")
	if err != nil {
		t.Fatalf("get updated snapshot: %v", err)
	}
	if len(updated.AllowedCommands) != 2 {
		t.Fatalf("snapshot update did not persist: %#v", updated)
	}
	workflow.Status = projectworkflow.WorkflowStatusDisabled
	if _, err := store.UpdateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("update workflow: %v", err)
	}
	disabled, err := store.ListWorkflows(ctx, WorkflowFilter{ProjectID: "project-1", Status: projectworkflow.WorkflowStatusDisabled})
	if err != nil {
		t.Fatalf("list disabled workflows: %v", err)
	}
	if len(disabled) != 1 || disabled[0].ID != workflow.ID {
		t.Fatalf("workflow update did not persist: %#v", disabled)
	}
}

func TestLadybugStoreRejectsDuplicateWorkflowRefsAndMissingUpdates(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewLadybugStore(graph)
	workflow := testLadybugWorkflow("workflow-1", "compile-and-verify")
	if _, err := store.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	duplicate := testLadybugWorkflow("workflow-2", "compile-and-verify")
	if _, err := store.CreateWorkflow(ctx, duplicate); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate workflow ref, got %v", err)
	}
	missingSnapshot := testLadybugPermissionSnapshot("missing", workflow.ID, "implementation-worker")
	if _, err := store.UpdatePermissionSnapshot(ctx, missingSnapshot); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing snapshot not found, got %v", err)
	}
}

func testLadybugWorkflow(id string, ref string) projectworkflow.WorkflowDefinition {
	now := time.Unix(100, 0).UTC()
	return projectworkflow.WorkflowDefinition{
		ID:          id,
		ProjectID:   "project-1",
		WorkflowRef: ref,
		Title:       "Compile and verify",
		Purpose:     "Exercise generic automation handoff persistence",
		Status:      projectworkflow.WorkflowStatusEnabled,
		Agents: []projectworkflow.WorkflowAgentDefinition{{
			ID:              "implementation-worker",
			DisplayName:     "Implementation Worker",
			Purpose:         "Edit and verify source changes",
			AllowedCommands: []string{"go test ./..."},
			WorkspaceMode:   "dedicated_worktree",
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
		Steps: []projectworkflow.WorkflowStep{{
			ID:                   "implement-change",
			Kind:                 projectworkflow.WorkflowStepKindWorkTask,
			Title:                "Implement change",
			Agent:                "implementation-worker",
			FilesToRead:          []string{"internal/service.go"},
			FilesToEdit:          []string{"internal/service.go"},
			EvidenceNeeded:       []string{"tests pass"},
			AcceptanceCriteria:   []string{"handoff includes live refs"},
			VerifierLadder:       []string{"go test ./..."},
			OutputContract:       "source changes plus verification summary",
			AutomationStatus:     "enabled",
			TriggerKind:          "automatic",
			SchedulePolicy:       "on_ready",
			MaxParallelTasks:     1,
			StopConditions:       []string{"permission snapshot missing"},
			DownstreamImpactRefs: []string{"review-gate"},
		}},
		ReviewGates: []projectworkflow.WorkflowReviewGate{{
			ID:                   "review-gate",
			AppliesTo:            []string{"implement-change"},
			ReviewerAgent:        "reviewer",
			Required:             true,
			IndependentFromOwner: true,
			RequiredArtifacts:    []string{"diff", "test-results"},
			AllowedActions:       []string{"approve", "request_changes"},
			Instructions:         "Review implementation output.",
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func testLadybugPermissionSnapshot(id string, workflowID string, agentID string) projectworkflow.WorkflowPermissionSnapshot {
	now := time.Unix(101, 0).UTC()
	return projectworkflow.WorkflowPermissionSnapshot{
		ID:              id,
		ProjectID:       "project-1",
		WorkflowID:      workflowID,
		AgentID:         agentID,
		Instructions:    "Use scoped workspace permissions.",
		AllowedSkills:   []string{"write-tests"},
		AllowedTools:    []string{"shell", "git"},
		AllowedCommands: []string{"go test ./..."},
		DeniedCommands:  []string{"git push origin main"},
		WorkspaceMode:   "dedicated_worktree",
		NetworkPolicy:   "disabled",
		SecretPolicy:    "none",
		LogPolicy:       "redacted",
		MaxRuntime:      "30m",
		MaxRetries:      1,
		ContentHash:     "sha256:generic",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}
