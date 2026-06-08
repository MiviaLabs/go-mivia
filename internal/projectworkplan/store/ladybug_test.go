package store

import (
	"context"
	"errors"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	model "github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestLadybugStoreCreateGetListPlanAndTask(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedWorkPlanStore(t, ctx, graph)

	plan := createWorkPlan(t, ctx, store, "project-a", "plan/ref/a")
	task := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/a", nil)

	gotPlan, err := store.GetWorkPlan(ctx, plan.ProjectID, plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if gotPlan.PlanRef != plan.PlanRef || gotPlan.Status != "planned" {
		t.Fatalf("unexpected plan: %#v", gotPlan)
	}
	plans, err := store.ListWorkPlans(ctx, model.WorkPlanFilter{ProjectID: plan.ProjectID})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != plan.ID {
		t.Fatalf("expected one plan, got %#v", plans)
	}

	gotTask, err := store.GetWorkTask(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if gotTask.TaskRef != task.TaskRef || gotTask.VerificationRequirement == "" {
		t.Fatalf("unexpected task: %#v", gotTask)
	}
	if len(gotTask.AcceptanceCriteria) != 1 || len(gotTask.StopConditions) != 1 || len(gotTask.VerifierLadder) != 1 ||
		gotTask.RegressionApplicability == "" || len(gotTask.DownstreamImpactRefs) != 1 || gotTask.OutputContract == "" {
		t.Fatalf("expected governed task contract to persist, got %#v", gotTask)
	}
	tasks, err := store.ListWorkTasks(ctx, model.WorkTaskFilter{ProjectID: task.ProjectID, PlanID: task.PlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID {
		t.Fatalf("expected one task, got %#v", tasks)
	}
}

func TestLadybugStorePreservesCommaContainingTaskLists(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedWorkPlanStore(t, ctx, graph)
	plan := createWorkPlan(t, ctx, store, "project-a", "plan/ref/a")
	task := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/commas", nil)
	task.AcceptanceCriteria = []string{"verify parser, service, and route"}
	task.StopConditions = []string{"block when source, verifier, or dependency evidence is missing"}
	task.VerifierLadder = []string{"run parser test, service test, and route test"}
	task.DownstreamImpactRefs = []string{"impact/ref,with-comma"}
	if _, err := store.UpdateWorkTask(ctx, task); err != nil {
		t.Fatalf("update task with comma lists: %v", err)
	}

	got, err := store.GetWorkTask(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if len(got.AcceptanceCriteria) != 1 || got.AcceptanceCriteria[0] != task.AcceptanceCriteria[0] {
		t.Fatalf("acceptance criteria split on comma: %#v", got.AcceptanceCriteria)
	}
	if len(got.StopConditions) != 1 || got.StopConditions[0] != task.StopConditions[0] {
		t.Fatalf("stop conditions split on comma: %#v", got.StopConditions)
	}
	if len(got.VerifierLadder) != 1 || got.VerifierLadder[0] != task.VerifierLadder[0] {
		t.Fatalf("verifier ladder split on comma: %#v", got.VerifierLadder)
	}
	if len(got.DownstreamImpactRefs) != 1 || got.DownstreamImpactRefs[0] != task.DownstreamImpactRefs[0] {
		t.Fatalf("downstream refs split on comma: %#v", got.DownstreamImpactRefs)
	}
}

func TestLadybugStoreProjectIsolation(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedWorkPlanStore(t, ctx, graph)

	planA := createWorkPlan(t, ctx, store, "project-a", "shared/ref")
	planB := createWorkPlan(t, ctx, store, "project-b", "shared/ref")
	taskA := createWorkTask(t, ctx, store, planA.ProjectID, planA.ID, "shared/task", nil)
	taskB := createWorkTask(t, ctx, store, planB.ProjectID, planB.ID, "shared/task", nil)

	gotB, err := store.GetWorkTask(ctx, "project-b", taskA.ID)
	if err != nil {
		t.Fatalf("expected project-b shared task id: %v", err)
	}
	if gotB.ProjectID != taskB.ProjectID {
		t.Fatalf("expected project-b task, got %#v", gotB)
	}
	if _, err := store.GetWorkPlan(ctx, "project-c", planA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected unknown project not found, got %v", err)
	}
}

func TestLadybugStorePersistsDependenciesAndAttachments(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedWorkPlanStore(t, ctx, graph)
	plan := createWorkPlan(t, ctx, store, "project-a", "plan/ref/a")
	dependency := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/dependency", nil)
	task := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/blocked", []string{dependency.ID})

	assertRelationshipStored(t, ctx, graph, relWorkPlanHasWorkTask, nodeRef(labelWorkPlan, graphID(plan.ProjectID, plan.ID)), nodeRef(labelWorkTask, graphID(task.ProjectID, task.ID)), plan.ProjectID)
	assertRelationshipStored(t, ctx, graph, relWorkTaskDependsOn, nodeRef(labelWorkTask, graphID(task.ProjectID, task.ID)), nodeRef(labelWorkTask, graphID(dependency.ProjectID, dependency.ID)), plan.ProjectID)

	attachment, err := store.CreateAttachment(ctx, model.Attachment{
		ID:              "attachment-evidence",
		ProjectID:       task.ProjectID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		Kind:            "evidence_ref",
		Ref:             "evidence/ref/a",
		AttachedByRunID: "agent_run_a",
		TraceID:         "trace_a",
		Note:            "metadata only",
	})
	if err != nil {
		t.Fatalf("attach evidence: %v", err)
	}
	assertRelationshipStored(t, ctx, graph, relWorkTaskHasEvidenceAttachment, nodeRef(labelWorkTask, graphID(task.ProjectID, task.ID)), nodeRef(labelWorkTaskEvidenceAttachment, graphID(task.ProjectID, attachment.ID)), task.ProjectID)
	attachmentNode, err := graph.GetNode(ctx, labelWorkTaskEvidenceAttachment, graphID(task.ProjectID, attachment.ID))
	if err != nil {
		t.Fatalf("get evidence attachment node: %v", err)
	}
	if attachmentNode.Properties["evidence_ref"] != "evidence/ref/a" || attachmentNode.Properties["attached_by_run_id"] != "agent_run_a" {
		t.Fatalf("unexpected attachment metadata: %#v", attachmentNode.Properties)
	}
	reviewAttachment, err := store.CreateAttachment(ctx, model.Attachment{
		ID:              "attachment-review",
		ProjectID:       task.ProjectID,
		PlanID:          task.PlanID,
		TaskID:          task.ID,
		Kind:            "review_result_ref",
		Ref:             "review/result/a",
		AttachedByRunID: "agent_run_reviewer",
		TraceID:         "trace_review",
		Note:            "review metadata only",
	})
	if err != nil {
		t.Fatalf("attach review result: %v", err)
	}
	assertRelationshipStored(t, ctx, graph, relWorkTaskHasReviewAttachment, nodeRef(labelWorkTask, graphID(task.ProjectID, task.ID)), nodeRef(labelWorkTaskReviewResultAttachment, graphID(task.ProjectID, reviewAttachment.ID)), task.ProjectID)
	attachments, err := store.ListAttachments(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("list attachments: %v", err)
	}
	if !containsAttachment(attachments, "review_result_ref", "review/result/a", "agent_run_reviewer") {
		t.Fatalf("expected listed review attachment, got %#v", attachments)
	}

	gotTask, err := store.GetWorkTask(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("get task after attachment: %v", err)
	}
	if len(gotTask.DependencyTaskIDs) != 1 || gotTask.DependencyTaskIDs[0] != dependency.ID {
		t.Fatalf("expected dependency id, got %#v", gotTask.DependencyTaskIDs)
	}
	if len(gotTask.EvidenceRefs) != 1 || gotTask.EvidenceRefs[0] != "evidence/ref/a" {
		t.Fatalf("expected evidence ref aggregate, got %#v", gotTask.EvidenceRefs)
	}
	if len(gotTask.ReviewResultRefs) != 1 || gotTask.ReviewResultRefs[0] != "review/result/a" {
		t.Fatalf("expected review ref aggregate, got %#v", gotTask.ReviewResultRefs)
	}
}

func TestLadybugStoreListOpenMineAndBlockedFilters(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedWorkPlanStore(t, ctx, graph)
	plan := createWorkPlan(t, ctx, store, "project-a", "plan/ref/a")
	open := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/open", nil)
	mine := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/mine", nil)
	blocked := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/blocked", nil)

	open.Status = "ready"
	open.ExpectedOutput = "updated output remains persisted"
	open.FailureCriteria = "updated failure criteria remains persisted"
	open.DecompositionQuality = model.DecompositionReady
	mine.Status = "claimed"
	mine.ClaimedByRunID = "agent_run_mine"
	blocked.Status = "blocked"
	blocked.BlockedReason = "waiting on dependency"
	for _, task := range []model.WorkTask{open, mine, blocked} {
		if _, err := store.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("update task %s: %v", task.ID, err)
		}
	}

	ready, err := store.ListWorkTasks(ctx, model.WorkTaskFilter{ProjectID: plan.ProjectID, Status: "ready"})
	if err != nil {
		t.Fatalf("list ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != open.ID {
		t.Fatalf("expected ready task, got %#v", ready)
	}
	if ready[0].ExpectedOutput != open.ExpectedOutput || ready[0].FailureCriteria != open.FailureCriteria || ready[0].DecompositionQuality != model.DecompositionReady {
		t.Fatalf("expected update to preserve rich task fields, got %#v", ready[0])
	}
	claimed, err := store.ListWorkTasks(ctx, model.WorkTaskFilter{ProjectID: plan.ProjectID, ClaimedByRunID: "agent_run_mine"})
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != mine.ID {
		t.Fatalf("expected claimed task, got %#v", claimed)
	}
	blockedTasks, err := store.ListWorkTasks(ctx, model.WorkTaskFilter{ProjectID: plan.ProjectID, Status: "blocked"})
	if err != nil {
		t.Fatalf("list blocked: %v", err)
	}
	if len(blockedTasks) != 1 || blockedTasks[0].BlockedReason == "" {
		t.Fatalf("expected blocked task, got %#v", blockedTasks)
	}
}

func TestLadybugStorePersistentReopenPlanTaskGraph(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	graph, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	store := bootstrappedWorkPlanStore(t, ctx, graph)
	plan := createWorkPlan(t, ctx, store, "project-a", "plan/ref/a")
	plan.Outcome = "persisted plan outcome"
	plan, err = store.UpdateWorkPlan(ctx, plan)
	if err != nil {
		t.Fatalf("update plan outcome: %v", err)
	}
	dependency := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/dependency", nil)
	task := createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/blocked", []string{dependency.ID})
	if _, err := store.CreateAttachment(ctx, model.Attachment{ID: "attachment-claim", ProjectID: task.ProjectID, PlanID: task.PlanID, TaskID: task.ID, Kind: "claim_ref", Ref: "claim/ref/a", AttachedByRunID: "agent_run_a"}); err != nil {
		t.Fatalf("attach claim: %v", err)
	}
	if err := graph.Close(); err != nil {
		t.Fatalf("close graph: %v", err)
	}

	reopened, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	defer reopened.Close()
	reopenedStore := bootstrappedWorkPlanStore(t, ctx, reopened)
	gotPlan, err := reopenedStore.GetWorkPlan(ctx, plan.ProjectID, plan.ID)
	if err != nil {
		t.Fatalf("get reopened plan: %v", err)
	}
	gotTask, err := reopenedStore.GetWorkTask(ctx, task.ProjectID, task.ID)
	if err != nil {
		t.Fatalf("get reopened task: %v", err)
	}
	if gotPlan.PlanRef != plan.PlanRef || len(gotTask.DependencyTaskIDs) != 1 || gotTask.DependencyTaskIDs[0] != dependency.ID || len(gotTask.ClaimRefs) != 1 {
		t.Fatalf("unexpected reopened graph: plan=%#v task=%#v", gotPlan, gotTask)
	}
	if gotPlan.Outcome != "persisted plan outcome" {
		t.Fatalf("expected reopened plan outcome, got %#v", gotPlan)
	}
}

func TestLadybugStoreRejectsDuplicateRefsInScope(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedWorkPlanStore(t, ctx, graph)
	plan := createWorkPlan(t, ctx, store, "project-a", "plan/ref/a")
	if _, err := store.CreateWorkPlan(ctx, model.WorkPlan{ID: "duplicate-plan", ProjectID: "project-a", PlanRef: "plan/ref/a", Title: "Duplicate", GoalSummary: "same plan ref", Status: model.WorkPlanStatusPlanned}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate plan ref to return ErrDuplicate, got %v", err)
	}
	if _, err := store.CreateWorkPlan(ctx, model.WorkPlan{ID: "other-plan", ProjectID: "project-b", PlanRef: "plan/ref/a", Title: "Other project", GoalSummary: "same plan ref allowed", Status: model.WorkPlanStatusPlanned}); err != nil {
		t.Fatalf("expected duplicate plan ref in another project to pass: %v", err)
	}
	createWorkTask(t, ctx, store, plan.ProjectID, plan.ID, "task/ref/a", nil)
	if _, err := store.CreateWorkTask(ctx, model.WorkTask{ID: "duplicate-task", ProjectID: plan.ProjectID, PlanID: plan.ID, TaskRef: "task/ref/a", Title: "Duplicate", Status: model.WorkTaskStatusPlanned, VerificationRequirement: "run focused store tests"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected duplicate task ref to return ErrDuplicate, got %v", err)
	}
}

func bootstrappedWorkPlanStore(t *testing.T, ctx context.Context, graph ladybug.Graph) *LadybugStore {
	t.Helper()
	store, err := NewBootstrappedLadybugStore(ctx, graph)
	if err != nil {
		t.Fatalf("bootstrap store: %v", err)
	}
	return store
}

func createWorkPlan(t *testing.T, ctx context.Context, store *LadybugStore, projectID string, planRef string) model.WorkPlan {
	t.Helper()
	plan, err := store.CreateWorkPlan(ctx, model.WorkPlan{
		ID:             planRef,
		ProjectID:      projectID,
		PlanRef:        planRef,
		UserRequestRef: "request/ref",
		Title:          "Implement work plan store",
		GoalSummary:    "Persist bounded work plan metadata",
		OwnerAgent:     "worker-2",
		CreatedByRunID: "agent_run_a",
		TraceID:        "trace_a",
		Status:         model.WorkPlanStatusPlanned,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	return plan
}

func createWorkTask(t *testing.T, ctx context.Context, store *LadybugStore, projectID string, planID string, taskRef string, dependencyIDs []string) model.WorkTask {
	t.Helper()
	task, err := store.CreateWorkTask(ctx, model.WorkTask{
		ID:                      taskRef,
		ProjectID:               projectID,
		PlanID:                  planID,
		TaskRef:                 taskRef,
		Title:                   "Persist scoped Ladybug metadata",
		Description:             "metadata only",
		OwnerAgent:              "worker-2",
		TraceID:                 "trace_a",
		Status:                  model.WorkTaskStatusReady,
		EvidenceNeeded:          []string{"focused store test"},
		ContextPackRefs:         []string{"context/ref/a"},
		LikelyFilesAffected:     []string{"internal/projectworkplan/store/ladybug.go"},
		DependencyTaskIDs:       dependencyIDs,
		VerificationRequirement: "run focused store tests",
		ResumeInstructions:      "revalidate source before continuing",
		AcceptanceCriteria:      []string{"source-backed behavior is implemented"},
		StopConditions:          []string{"missing source evidence"},
		VerifierLadder:          []string{"focused store test"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"downstream.impact"},
		OutputContract:          "metadata contract plus verifier refs",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func assertRelationshipStored(t *testing.T, ctx context.Context, graph ladybug.Graph, relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string) {
	t.Helper()
	relationships, err := graph.ListRelationships(ctx, relationshipType, ladybug.RelationshipFilter{From: &from, To: &to, Properties: map[string]string{"project_id": projectID}})
	if err != nil {
		t.Fatalf("list %s relationship: %v", relationshipType, err)
	}
	if len(relationships) != 1 {
		t.Fatalf("expected one %s relationship, got %#v", relationshipType, relationships)
	}
}

func containsAttachment(attachments []model.Attachment, kind string, ref string, attachedByRunID string) bool {
	for _, attachment := range attachments {
		if attachment.Kind == kind && attachment.Ref == ref && attachment.AttachedByRunID == attachedByRunID {
			return true
		}
	}
	return false
}
