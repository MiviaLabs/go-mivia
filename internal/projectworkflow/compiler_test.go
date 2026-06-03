package projectworkflow

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestCompileWorkflowCreatesGovernedObjects(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, automations := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-1", CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	if result.WorkPlanID == "" {
		t.Fatalf("expected work plan id: %#v", result)
	}
	if len(result.WorkTaskIDs) != 1 {
		t.Fatalf("expected one implementation task, got %#v", result.WorkTaskIDs)
	}
	if len(result.ReviewerTaskIDs) != 2 {
		t.Fatalf("expected implementation and automation reviewer tasks, got %#v", result.ReviewerTaskIDs)
	}
	if len(result.AutomationIDs) != 1 {
		t.Fatalf("expected one automation, got %#v", result.AutomationIDs)
	}
	if len(result.PermissionSnapshotIDs) != 3 {
		t.Fatalf("expected agent permission snapshots, got %#v", result.PermissionSnapshotIDs)
	}

	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list work plans: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != result.WorkPlanID || plans[0].UserRequestRef != "request-1" {
		t.Fatalf("unexpected compiled plan: %#v", plans)
	}

	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	impl := taskByRef(t, tasks, "implement-step")
	if impl.OwnerAgent != "worker" || impl.VerificationRequirement == "" || impl.ExpectedOutput == "" || impl.FailureCriteria == "" || impl.ResumeInstructions == "" {
		t.Fatalf("implementation task is not isolated-worker-ready: %#v", impl)
	}
	if !containsString(impl.EvidenceNeeded, "review gate review-implement") {
		t.Fatalf("implementation task missing review requirement: %#v", impl.EvidenceNeeded)
	}
	reviewer := taskByRef(t, tasks, "implement-step-review-review-implement")
	if reviewer.OwnerAgent != "reviewer" {
		t.Fatalf("unexpected reviewer task owner: %#v", reviewer)
	}
	if len(reviewer.DependencyTaskIDs) != 1 || reviewer.DependencyTaskIDs[0] != impl.ID {
		t.Fatalf("reviewer task must depend on implementation task: %#v", reviewer)
	}
	if reviewer.VerificationRequirement != "attach review_result_ref" || !containsString(reviewer.EvidenceNeeded, "reviewed task id") {
		t.Fatalf("reviewer task missing required review metadata: %#v", reviewer)
	}
	automationReviewer := taskByRef(t, tasks, "automation-step-review-review-automation")
	if automationReviewer.OwnerAgent != "reviewer" {
		t.Fatalf("unexpected automation reviewer owner: %#v", automationReviewer)
	}
	if len(automationReviewer.DependencyTaskIDs) != 1 || automationReviewer.DependencyTaskIDs[0] != impl.ID {
		t.Fatalf("automation reviewer task must depend on implementation task: %#v", automationReviewer)
	}
	if !containsString(automationReviewer.EvidenceNeeded, "automation ref") || !containsString(automationReviewer.EvidenceNeeded, "allowed task refs") {
		t.Fatalf("automation reviewer task missing required evidence: %#v", automationReviewer)
	}

	createdAutomation, err := automations.GetAutomation(ctx, "project-1", result.AutomationIDs[0])
	if err != nil {
		t.Fatalf("get automation: %v", err)
	}
	if createdAutomation.PlanID != result.WorkPlanID || createdAutomation.AutomationRef != "workflow-ref:automation-step" {
		t.Fatalf("unexpected automation refs: %#v", createdAutomation)
	}
	if len(createdAutomation.AllowedTaskRefs) != 1 || createdAutomation.AllowedTaskRefs[0] != "implement-step" {
		t.Fatalf("automation allowed task refs must come from step dependencies: %#v", createdAutomation.AllowedTaskRefs)
	}
	if !strings.HasPrefix(createdAutomation.PermissionRef, "permission_snapshot:") {
		t.Fatalf("automation missing permission snapshot ref: %#v", createdAutomation)
	}
	if createdAutomation.SourceKind != projectautomation.AutomationSourceWorkflow {
		t.Fatalf("automation must be marked workflow-sourced: %#v", createdAutomation)
	}
}

func TestCompileWorkflowDryRunPersistsNothing(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, automations := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run compile workflow: %v", err)
	}
	if !result.DryRun || result.WorkPlanID == "" || len(result.WorkTaskIDs) != 1 || len(result.ReviewerTaskIDs) != 2 || len(result.AutomationIDs) != 1 {
		t.Fatalf("unexpected dry-run summary: %#v", result)
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	autos, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list automations: %v", err)
	}
	if len(plans) != 0 || len(autos) != 0 || len(workflowStore.snapshots) != 0 {
		t.Fatalf("dry run persisted plans=%#v automations=%#v snapshots=%#v", plans, autos, workflowStore.snapshots)
	}
}

func TestCompileWorkflowMissingAutomationReviewGateFails(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.ReviewGates = workflow.ReviewGates[:1]
	workflowStore.seedWorkflow(workflow)

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1"}); err == nil {
		t.Fatal("expected missing automation review gate to fail")
	}
}

func TestCompileWorkflowRejectsIndependentSelfReview(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.ReviewGates[0].ReviewerAgent = "worker"
	workflowStore.seedWorkflow(workflow)

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1"}); err == nil {
		t.Fatal("expected independent self-review config to fail")
	}
}

func TestCompileWorkflowRejectsAutomationWithoutTaskDependency(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.Steps[1].DependsOn = nil
	workflowStore.seedWorkflow(workflow)

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1"}); err == nil {
		t.Fatal("expected automation without task dependency to fail")
	}
}

func TestCompileWorkflowAllowsAutomationBeforeTaskDependency(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.Steps[0], workflow.Steps[1] = workflow.Steps[1], workflow.Steps[0]
	workflowStore.seedWorkflow(workflow)

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1"}); err != nil {
		t.Fatalf("expected automation dependency order to compile: %v", err)
	}
}

func newCompileFixture() (*Service, *compilerWorkflowStore, *projectworkplan.Service, *projectautomation.Service) {
	workflowStore := newCompilerWorkflowStore()
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true, MaxParallelTasks: 2})
	svc := New(workflowStore)
	svc.SetCompilerDependencies(workPlans, automations)
	now := time.Date(2026, 6, 4, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	seq := 0
	svc.newID = func(prefix string) string {
		seq++
		return prefix + "-compile-" + string(rune('a'+seq))
	}
	return svc, workflowStore, workPlans, automations
}

func baseCompileWorkflow() WorkflowDefinition {
	now := time.Date(2026, 6, 4, 2, 0, 0, 0, time.UTC)
	return WorkflowDefinition{
		ID:          "workflow-1",
		ProjectID:   "project-1",
		WorkflowRef: "workflow-ref",
		Title:       "Compile Workflow",
		Purpose:     "Compile workflow metadata into governed execution objects.",
		Status:      WorkflowStatusEnabled,
		CreatedAt:   now,
		UpdatedAt:   now,
		Agents: []WorkflowAgentDefinition{
			{ID: "worker", Purpose: "Implement bounded task metadata.", SecretPolicy: "deny", LogPolicy: "metadata_only", CreatedAt: now, UpdatedAt: now},
			{ID: "reviewer", Purpose: "Review implementation task evidence.", SecretPolicy: "deny", LogPolicy: "metadata_only", CreatedAt: now, UpdatedAt: now},
			{ID: "automation", Purpose: "Run governed automation metadata.", SecretPolicy: "deny", LogPolicy: "metadata_only", CreatedAt: now, UpdatedAt: now},
		},
		Steps: []WorkflowStep{
			{
				ID:                      "implement-step",
				Kind:                    WorkflowStepKindWorkTask,
				Title:                   "Implement Step",
				Agent:                   "worker",
				Description:             "Implement bounded workflow compiler behavior.",
				EvidenceNeeded:          []string{"source-anchors-read"},
				ContextPackRefs:         []string{"context-pack:workflow-compiler"},
				LikelyFilesAffected:     []string{"internal/projectworkflow/compiler.go"},
				VerificationRequirement: "orchestrator runs focused compiler tests",
				ExpectedOutput:          "compiler creates governed metadata",
				FailureCriteria:         "block if compiler persists unsafe metadata",
				ResumeInstructions:      "resume from compiler task metadata only",
			},
			{
				ID:          "automation-step",
				Kind:        WorkflowStepKindAutomation,
				Title:       "Run Automation",
				Agent:       "automation",
				DependsOn:   []string{"implement-step"},
				Description: "Queue governed automation for ready implementation tasks.",
			},
		},
		ReviewGates: []WorkflowReviewGate{
			{ID: "review-implement", AppliesTo: []string{"implement-step"}, ReviewerAgent: "reviewer", Required: true, IndependentFromOwner: true, RequiredArtifacts: []string{"changed_files"}, AllowedActions: []string{ReviewGateDecisionApproved, ReviewGateDecisionRejected}, Instructions: "Review changed files and verifier refs before deciding."},
			{ID: "review-automation", AppliesTo: []string{"automation-step"}, ReviewerAgent: "reviewer", Required: true, IndependentFromOwner: true, RequiredArtifacts: []string{"automation_ref"}, AllowedActions: []string{ReviewGateDecisionApproved, ReviewGateDecisionRejected}, Instructions: "Review automation refs before execution."},
		},
	}
}

type compilerWorkflowStore struct {
	workflows map[string]WorkflowDefinition
	snapshots map[string]WorkflowPermissionSnapshot
}

func newCompilerWorkflowStore() *compilerWorkflowStore {
	return &compilerWorkflowStore{workflows: map[string]WorkflowDefinition{}, snapshots: map[string]WorkflowPermissionSnapshot{}}
}

func (store *compilerWorkflowStore) seedWorkflow(workflow WorkflowDefinition) {
	store.workflows[workflow.ProjectID+"\x00"+workflow.ID] = cloneCompileWorkflow(workflow)
}

func (store *compilerWorkflowStore) CreateWorkflow(_ context.Context, workflow WorkflowDefinition) (WorkflowDefinition, error) {
	store.seedWorkflow(workflow)
	return cloneCompileWorkflow(workflow), nil
}

func (store *compilerWorkflowStore) GetWorkflow(_ context.Context, projectID, workflowID string) (WorkflowDefinition, error) {
	workflow, ok := store.workflows[projectID+"\x00"+workflowID]
	if !ok {
		return WorkflowDefinition{}, errors.New("workflow not found")
	}
	return cloneCompileWorkflow(workflow), nil
}

func (store *compilerWorkflowStore) ListWorkflows(_ context.Context, filter WorkflowFilter) ([]WorkflowDefinition, error) {
	var out []WorkflowDefinition
	for _, workflow := range store.workflows {
		if workflow.ProjectID == filter.ProjectID {
			out = append(out, cloneCompileWorkflow(workflow))
		}
	}
	return out, nil
}

func (store *compilerWorkflowStore) UpdateWorkflow(_ context.Context, workflow WorkflowDefinition) (WorkflowDefinition, error) {
	store.seedWorkflow(workflow)
	return cloneCompileWorkflow(workflow), nil
}

func (store *compilerWorkflowStore) CreatePermissionSnapshot(_ context.Context, snapshot WorkflowPermissionSnapshot) (WorkflowPermissionSnapshot, error) {
	store.snapshots[snapshot.ProjectID+"\x00"+snapshot.ID] = cloneTestSnapshot(snapshot)
	return cloneTestSnapshot(snapshot), nil
}

func (store *compilerWorkflowStore) GetPermissionSnapshot(_ context.Context, projectID, snapshotID string) (WorkflowPermissionSnapshot, error) {
	snapshot, ok := store.snapshots[projectID+"\x00"+snapshotID]
	if !ok {
		return WorkflowPermissionSnapshot{}, errors.New("snapshot not found")
	}
	return cloneTestSnapshot(snapshot), nil
}

func (store *compilerWorkflowStore) ListPermissionSnapshots(_ context.Context, filter PermissionSnapshotFilter) ([]WorkflowPermissionSnapshot, error) {
	var out []WorkflowPermissionSnapshot
	for _, snapshot := range store.snapshots {
		if snapshot.ProjectID != filter.ProjectID {
			continue
		}
		if filter.WorkflowID != "" && snapshot.WorkflowID != filter.WorkflowID {
			continue
		}
		if filter.AgentID != "" && snapshot.AgentID != filter.AgentID {
			continue
		}
		out = append(out, cloneTestSnapshot(snapshot))
	}
	return out, nil
}

func cloneCompileWorkflow(workflow WorkflowDefinition) WorkflowDefinition {
	workflow = cloneTestWorkflow(workflow)
	workflow.Steps = append([]WorkflowStep(nil), workflow.Steps...)
	for i := range workflow.Steps {
		workflow.Steps[i].DependsOn = append([]string(nil), workflow.Steps[i].DependsOn...)
		workflow.Steps[i].EvidenceNeeded = append([]string(nil), workflow.Steps[i].EvidenceNeeded...)
		workflow.Steps[i].ContextPackRefs = append([]string(nil), workflow.Steps[i].ContextPackRefs...)
		workflow.Steps[i].LikelyFilesAffected = append([]string(nil), workflow.Steps[i].LikelyFilesAffected...)
	}
	workflow.ReviewGates = append([]WorkflowReviewGate(nil), workflow.ReviewGates...)
	for i := range workflow.ReviewGates {
		workflow.ReviewGates[i].AppliesTo = append([]string(nil), workflow.ReviewGates[i].AppliesTo...)
		workflow.ReviewGates[i].RequiredArtifacts = append([]string(nil), workflow.ReviewGates[i].RequiredArtifacts...)
		workflow.ReviewGates[i].AllowedActions = append([]string(nil), workflow.ReviewGates[i].AllowedActions...)
	}
	return workflow
}

func taskByRef(t *testing.T, tasks []projectworkplan.WorkTask, ref string) projectworkplan.WorkTask {
	t.Helper()
	for _, task := range tasks {
		if task.TaskRef == ref {
			return task
		}
	}
	t.Fatalf("missing task ref %q in %#v", ref, tasks)
	return projectworkplan.WorkTask{}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
