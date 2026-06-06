package store

import (
	"context"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
)

func TestMemoryStoreCreateGetWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	workflow := testWorkflow("project-1", "workflow-1", "workflow-ref-1")

	created, err := mem.CreateWorkflow(ctx, workflow)
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	got, err := mem.GetWorkflow(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if got.WorkflowRef != "workflow-ref-1" || got.Title != "Workflow" {
		t.Fatalf("unexpected workflow: %#v", got)
	}
}

func TestMemoryStoreCompositeKeysDoNotCollide(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()

	first := testWorkflow("project\x00workflow", "one", "ref")
	second := testWorkflow("project", "workflow\x00one", "other-ref")
	if _, err := mem.CreateWorkflow(ctx, first); err != nil {
		t.Fatalf("create first workflow: %v", err)
	}
	if _, err := mem.CreateWorkflow(ctx, second); err != nil {
		t.Fatalf("create second workflow with ambiguous concatenation key: %v", err)
	}

	snapshotA := testSnapshot("project\x00snapshot", "one", "workflow-1", "agent-1")
	snapshotB := testSnapshot("project", "snapshot\x00one", "workflow-2", "agent-2")
	if _, err := mem.CreatePermissionSnapshot(ctx, snapshotA); err != nil {
		t.Fatalf("create first snapshot: %v", err)
	}
	if _, err := mem.CreatePermissionSnapshot(ctx, snapshotB); err != nil {
		t.Fatalf("create second snapshot with ambiguous concatenation key: %v", err)
	}
}

func TestMemoryStoreListWorkflowsByProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	if _, err := mem.CreateWorkflow(ctx, testWorkflow("project-1", "workflow-1", "workflow-ref-1")); err != nil {
		t.Fatalf("create project-1 workflow: %v", err)
	}
	if _, err := mem.CreateWorkflow(ctx, testWorkflow("project-2", "workflow-1", "workflow-ref-1")); err != nil {
		t.Fatalf("create project-2 workflow: %v", err)
	}

	list, err := mem.ListWorkflows(ctx, WorkflowFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(list) != 1 || list[0].ProjectID != "project-1" {
		t.Fatalf("expected project-1 workflow only, got %#v", list)
	}
}

func TestMemoryStoreListWorkflowsByStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	enabled := testWorkflow("project-1", "workflow-1", "workflow-ref-1")
	disabled := testWorkflow("project-1", "workflow-2", "workflow-ref-2")
	disabled.Status = projectworkflow.WorkflowStatusDisabled
	if _, err := mem.CreateWorkflow(ctx, enabled); err != nil {
		t.Fatalf("create enabled workflow: %v", err)
	}
	if _, err := mem.CreateWorkflow(ctx, disabled); err != nil {
		t.Fatalf("create disabled workflow: %v", err)
	}

	list, err := mem.ListWorkflows(ctx, WorkflowFilter{ProjectID: "project-1", Status: projectworkflow.WorkflowStatusDisabled})
	if err != nil {
		t.Fatalf("list disabled workflows: %v", err)
	}
	if len(list) != 1 || list[0].ID != "workflow-2" {
		t.Fatalf("expected disabled workflow, got %#v", list)
	}
}

func TestMemoryStoreUpdateWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	workflow := testWorkflow("project-1", "workflow-1", "workflow-ref-1")
	if _, err := mem.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	workflow.Status = projectworkflow.WorkflowStatusDisabled
	workflow.Title = "Updated Workflow"

	updated, err := mem.UpdateWorkflow(ctx, workflow)
	if err != nil {
		t.Fatalf("update workflow: %v", err)
	}
	if updated.Status != projectworkflow.WorkflowStatusDisabled || updated.Title != "Updated Workflow" {
		t.Fatalf("unexpected updated workflow: %#v", updated)
	}
	got, err := mem.GetWorkflow(ctx, "project-1", "workflow-1")
	if err != nil {
		t.Fatalf("get updated workflow: %v", err)
	}
	if got.Status != projectworkflow.WorkflowStatusDisabled || got.Title != "Updated Workflow" {
		t.Fatalf("stored workflow was not updated: %#v", got)
	}
}

func TestMemoryStoreCreateGetPermissionSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	snapshot := testSnapshot("project-1", "snapshot-1", "workflow-1", "agent-1")

	created, err := mem.CreatePermissionSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatalf("create permission snapshot: %v", err)
	}
	got, err := mem.GetPermissionSnapshot(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get permission snapshot: %v", err)
	}
	if got.WorkflowID != "workflow-1" || got.AgentID != "agent-1" || got.ContentHash != "hash-snapshot-1" {
		t.Fatalf("unexpected permission snapshot: %#v", got)
	}
	list, err := mem.ListPermissionSnapshots(ctx, PermissionSnapshotFilter{ProjectID: "project-1", WorkflowID: "workflow-1", AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("list permission snapshots: %v", err)
	}
	if len(list) != 1 || list[0].ID != "snapshot-1" {
		t.Fatalf("expected one permission snapshot, got %#v", list)
	}
}

func TestMemoryStoreReturnedSlicesAreCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	workflow := testWorkflow("project-1", "workflow-1", "workflow-ref-1")
	created, err := mem.CreateWorkflow(ctx, workflow)
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	workflow.Agents[0].AllowedSkills[0] = "mutated-caller"
	workflow.Steps[0].EvidenceNeeded[0] = "mutated-caller"
	workflow.Steps[0].FilesToRead[0] = "mutated-caller"
	workflow.Steps[0].FilesToEdit[0] = "mutated-caller"
	workflow.ReviewGates[0].AppliesTo[0] = "mutated-caller"
	workflow.PermissionSnapshots[0].AllowedTools[0] = "mutated-caller"

	got, err := mem.GetWorkflow(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if got.Agents[0].AllowedSkills[0] == "mutated-caller" || got.Steps[0].EvidenceNeeded[0] == "mutated-caller" || got.Steps[0].FilesToRead[0] == "mutated-caller" || got.Steps[0].FilesToEdit[0] == "mutated-caller" || got.ReviewGates[0].AppliesTo[0] == "mutated-caller" || got.PermissionSnapshots[0].AllowedTools[0] == "mutated-caller" {
		t.Fatalf("store kept caller-owned workflow slices: %#v", got)
	}
	got.Agents[0].AllowedSkills[0] = "mutated-read"
	got.Steps[0].EvidenceNeeded[0] = "mutated-read"
	got.Steps[0].FilesToRead[0] = "mutated-read"
	got.Steps[0].FilesToEdit[0] = "mutated-read"
	got.ReviewGates[0].AppliesTo[0] = "mutated-read"
	got.PermissionSnapshots[0].AllowedTools[0] = "mutated-read"
	again, err := mem.GetWorkflow(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get workflow again: %v", err)
	}
	if again.Agents[0].AllowedSkills[0] == "mutated-read" || again.Steps[0].EvidenceNeeded[0] == "mutated-read" || again.Steps[0].FilesToRead[0] == "mutated-read" || again.Steps[0].FilesToEdit[0] == "mutated-read" || again.ReviewGates[0].AppliesTo[0] == "mutated-read" || again.PermissionSnapshots[0].AllowedTools[0] == "mutated-read" {
		t.Fatalf("store returned internal workflow slices: %#v", again)
	}

	snapshot := testSnapshot("project-1", "snapshot-1", "workflow-1", "agent-1")
	createdSnapshot, err := mem.CreatePermissionSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatalf("create permission snapshot: %v", err)
	}
	snapshot.AllowedSkills[0] = "mutated-caller"
	gotSnapshot, err := mem.GetPermissionSnapshot(ctx, "project-1", createdSnapshot.ID)
	if err != nil {
		t.Fatalf("get permission snapshot: %v", err)
	}
	if gotSnapshot.AllowedSkills[0] == "mutated-caller" {
		t.Fatal("store kept caller-owned permission snapshot slice")
	}
	gotSnapshot.AllowedSkills[0] = "mutated-read"
	againSnapshot, err := mem.GetPermissionSnapshot(ctx, "project-1", createdSnapshot.ID)
	if err != nil {
		t.Fatalf("get permission snapshot again: %v", err)
	}
	if againSnapshot.AllowedSkills[0] == "mutated-read" {
		t.Fatal("store returned internal permission snapshot slice")
	}
}

func testWorkflow(projectID, id, ref string) projectworkflow.WorkflowDefinition {
	now := time.Now().UTC()
	return projectworkflow.WorkflowDefinition{
		ID:          id,
		ProjectID:   projectID,
		WorkflowRef: ref,
		Title:       "Workflow",
		Purpose:     "metadata-only workflow",
		Status:      projectworkflow.WorkflowStatusEnabled,
		Agents: []projectworkflow.WorkflowAgentDefinition{{
			ID:              "agent-1",
			DisplayName:     "Agent",
			Purpose:         "metadata-only work",
			AllowedSkills:   []string{"skill-1"},
			AllowedTools:    []string{"tool-1"},
			AllowedCommands: []string{"go test"},
			DeniedCommands:  []string{"git push"},
			WorkspaceMode:   "edit",
			NetworkPolicy:   "disabled",
			SecretPolicy:    "deny",
			LogPolicy:       "metadata_only",
			MaxRuntime:      "10m",
			MaxRetries:      1,
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
		Steps: []projectworkflow.WorkflowStep{{
			ID:                      "step-1",
			Kind:                    projectworkflow.WorkflowStepKindWorkTask,
			Title:                   "Step",
			Agent:                   "agent-1",
			DependsOn:               []string{"step-0"},
			EvidenceNeeded:          []string{"source"},
			ContextPackRefs:         []string{"context-1"},
			FilesToRead:             []string{"internal/projectworkflow/model.go"},
			FilesToEdit:             []string{"internal/projectworkflow/store/memory.go"},
			LikelyFilesAffected:     []string{"internal/projectworkflow/store/memory.go"},
			VerificationRequirement: "go test ./internal/projectworkflow/...",
		}},
		ReviewGates: []projectworkflow.WorkflowReviewGate{{
			ID:                   "review-1",
			AppliesTo:            []string{"step-1"},
			ReviewerAgent:        "reviewer",
			Required:             true,
			IndependentFromOwner: true,
			RequiredArtifacts:    []string{"tests"},
			AllowedActions:       []string{"approve"},
			Instructions:         "review metadata only",
		}},
		PermissionSnapshots: []projectworkflow.WorkflowPermissionSnapshot{
			testSnapshot(projectID, "embedded-snapshot-1", id, "agent-1"),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func testSnapshot(projectID, id, workflowID, agentID string) projectworkflow.WorkflowPermissionSnapshot {
	now := time.Now().UTC()
	return projectworkflow.WorkflowPermissionSnapshot{
		ID:              id,
		ProjectID:       projectID,
		AgentID:         agentID,
		WorkflowID:      workflowID,
		AllowedSkills:   []string{"skill-1"},
		AllowedTools:    []string{"tool-1"},
		AllowedCommands: []string{"go test"},
		DeniedCommands:  []string{"git push"},
		WorkspaceMode:   "edit",
		NetworkPolicy:   "disabled",
		SecretPolicy:    "deny",
		LogPolicy:       "metadata_only",
		MaxRuntime:      "10m",
		MaxRetries:      1,
		ContentHash:     "hash-" + id,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}
