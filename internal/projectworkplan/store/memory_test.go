package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestMemoryStoreCreateGetListPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	plan := testPlan("project-1", "plan-1")

	created, err := mem.CreateWorkPlan(ctx, plan)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	got, err := mem.GetWorkPlan(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.PlanRef != "plan-1" {
		t.Fatalf("unexpected plan: %+v", got)
	}
	list, err := mem.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one plan, got %d", len(list))
	}
}

func TestMemoryStoreProjectIsolationAndDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	if _, err := mem.CreateWorkPlan(ctx, testPlan("project-1", "plan-1")); err != nil {
		t.Fatalf("create project-1 plan: %v", err)
	}
	if _, err := mem.CreateWorkPlan(ctx, testPlan("project-2", "plan-1")); err != nil {
		t.Fatalf("same ref in another project should be allowed: %v", err)
	}
	if _, err := mem.CreateWorkPlan(ctx, testPlan("project-1", "plan-1")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate plan ref, got %v", err)
	}
	if _, err := mem.GetWorkPlan(ctx, "project-2", "plan-project-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected project isolation miss, got %v", err)
	}
}

func TestMemoryStoreCreateGetListTaskAndCopiesSlices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	plan, err := mem.CreateWorkPlan(ctx, testPlan("project-1", "plan-1"))
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task := testTask("project-1", plan.ID, "task-1")
	created, err := mem.CreateWorkTask(ctx, task)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	task.EvidenceNeeded[0] = "mutated"
	got, err := mem.GetWorkTask(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.EvidenceNeeded[0] == "mutated" {
		t.Fatal("store kept caller-owned slice")
	}
	got.EvidenceNeeded[0] = "mutated-read"
	again, err := mem.GetWorkTask(ctx, "project-1", created.ID)
	if err != nil {
		t.Fatalf("get task again: %v", err)
	}
	if again.EvidenceNeeded[0] == "mutated-read" {
		t.Fatal("store returned internal slice")
	}
	list, err := mem.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: plan.ID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one task, got %d", len(list))
	}
}

func TestMemoryStoreTaskDuplicateAndFilters(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	plan, err := mem.CreateWorkPlan(ctx, testPlan("project-1", "plan-1"))
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task := testTask("project-1", plan.ID, "task-1")
	task.OwnerAgent = "worker-1"
	if _, err := mem.CreateWorkTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := mem.CreateWorkTask(ctx, testTask("project-1", plan.ID, "task-1")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate task ref, got %v", err)
	}
	mine, err := mem.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", OwnerAgent: "worker-1"})
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	if len(mine) != 1 {
		t.Fatalf("expected owner filter match, got %d", len(mine))
	}
	blocked := testTask("project-1", plan.ID, "task-blocked")
	blocked.Status = projectworkplan.WorkTaskStatusBlocked
	if _, err := mem.CreateWorkTask(ctx, blocked); err != nil {
		t.Fatalf("create blocked task: %v", err)
	}
	blockedList, err := mem.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", Status: projectworkplan.WorkTaskStatusBlocked})
	if err != nil {
		t.Fatalf("list blocked: %v", err)
	}
	if len(blockedList) != 1 {
		t.Fatalf("expected blocked filter match, got %d", len(blockedList))
	}
}

func TestMemoryStoreAttachmentPersistence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mem := NewMemoryStore()
	plan, err := mem.CreateWorkPlan(ctx, testPlan("project-1", "plan-1"))
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	task, err := mem.CreateWorkTask(ctx, testTask("project-1", plan.ID, "task-1"))
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	attachment := projectworkplan.Attachment{
		ID:        "attachment-1",
		ProjectID: "project-1",
		PlanID:    plan.ID,
		TaskID:    task.ID,
		Kind:      "evidence_ref",
		Ref:       "evidence-1",
		CreatedAt: time.Now().UTC(),
	}
	if _, err := mem.CreateAttachment(ctx, attachment); err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	attachments, err := mem.ListAttachments(ctx, "project-1", task.ID)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Ref != "evidence-1" {
		t.Fatalf("unexpected attachments: %+v", attachments)
	}
}

func testPlan(projectID, ref string) projectworkplan.WorkPlan {
	now := time.Now().UTC()
	return projectworkplan.WorkPlan{
		ID:          "plan-" + projectID,
		ProjectID:   projectID,
		PlanRef:     ref,
		Title:       "Plan",
		GoalSummary: "Goal",
		Status:      projectworkplan.WorkPlanStatusPlanned,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func testTask(projectID, planID, ref string) projectworkplan.WorkTask {
	now := time.Now().UTC()
	return projectworkplan.WorkTask{
		ID:                      ref,
		ProjectID:               projectID,
		PlanID:                  planID,
		TaskRef:                 ref,
		Title:                   "Task",
		Status:                  projectworkplan.WorkTaskStatusReady,
		EvidenceNeeded:          []string{"source"},
		ContextPackRefs:         []string{"context-1"},
		LikelyFilesAffected:     []string{"internal/projectworkplan/model.go"},
		VerificationRequirement: "unit tests",
		ExpectedOutput:          "code",
		FailureCriteria:         "blocked",
		ResumeInstructions:      "resume",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
}
