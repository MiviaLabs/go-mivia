package store

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	model "github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestMigrateLadybugMetadataCopiesExistingPlanTaskAndAttachments(t *testing.T) {
	ctx := context.Background()
	source := ladybug.NewMemoryGraph()
	target := ladybug.NewMemoryGraph()
	sourceStore := bootstrappedWorkPlanStore(t, ctx, source)
	targetStore := bootstrappedWorkPlanStore(t, ctx, target)
	plan := createWorkPlan(t, ctx, sourceStore, "project-a", "plan/ref/a")
	task := createWorkTask(t, ctx, sourceStore, plan.ProjectID, plan.ID, "task/ref/a", nil)
	if _, err := sourceStore.CreateAttachment(ctx, model.Attachment{
		ID:              "attachment-evidence",
		ProjectID:       task.ProjectID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		Kind:            "evidence_ref",
		Ref:             "evidence-ref-a",
		AttachedByRunID: "run-a",
	}); err != nil {
		t.Fatalf("create attachment: %v", err)
	}

	if err := MigrateLadybugMetadata(ctx, source, target, []string{"project-a"}); err != nil {
		t.Fatalf("migrate metadata: %v", err)
	}
	if _, err := targetStore.GetWorkPlan(ctx, plan.ProjectID, plan.ID); err != nil {
		t.Fatalf("get migrated plan: %v", err)
	}
	gotTask, err := targetStore.GetWorkTask(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("get migrated task: %v", err)
	}
	if len(gotTask.EvidenceRefs) != 1 || gotTask.EvidenceRefs[0] != "evidence-ref-a" {
		t.Fatalf("expected migrated task evidence aggregate, got %#v", gotTask.EvidenceRefs)
	}
	attachments, err := targetStore.ListAttachments(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("list migrated attachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Ref != "evidence-ref-a" {
		t.Fatalf("expected migrated attachment, got %#v", attachments)
	}
}
