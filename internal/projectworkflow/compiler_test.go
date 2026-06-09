package projectworkflow

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
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

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-1", ContextPackRefs: []string{"jira-context:GENERIC-1044:summary", "jira-context:GENERIC-1044:scope"}, CreatedByRunID: "run-1", TraceID: "trace-1"})
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
	if len(result.AutomationIDs) != 3 {
		t.Fatalf("expected executor, task reviewer, and automation reviewer automations, got %#v", result.AutomationIDs)
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
	if plans[0].IsolationMode != projectworkplan.WorkPlanIsolationDedicatedWorktree || !strings.HasPrefix(plans[0].GitBranchRef, "workflow-ref-run-1-compile-") || !strings.HasPrefix(plans[0].GitWorktreeRef, "workflow/workflow-ref-run-1-compile-") {
		t.Fatalf("compiled workflow plan must use dedicated worktree isolation, got %#v", plans[0])
	}
	if plans[0].GitBaseRef != "" {
		t.Fatalf("compiled workflow plan must omit unverified git base ref, got %#v", plans[0])
	}

	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	impl := taskByRef(t, tasks, "implement-step")
	if impl.OwnerAgent != "worker" || impl.VerificationRequirement == "" || impl.ExpectedOutput == "" || impl.FailureCriteria == "" || impl.ResumeInstructions == "" {
		t.Fatalf("implementation task is not isolated-worker-ready: %#v", impl)
	}
	if !strings.Contains(impl.Description, "Agent instructions: Implement exactly one scoped task.") {
		t.Fatalf("implementation task missing owner agent instructions: %#v", impl)
	}
	if !containsString(impl.FilesToRead, "internal/projectworkflow/compiler.go") || !containsString(impl.FilesToEdit, "internal/projectworkflow/compiler.go") || impl.ReviewGate == "" {
		t.Fatalf("implementation task missing first-class task packet fields: %#v", impl)
	}
	if !containsString(impl.EvidenceNeeded, "review gate review-implement") {
		t.Fatalf("implementation task missing review requirement: %#v", impl.EvidenceNeeded)
	}
	if !containsString(impl.ContextPackRefs, "context-pack:workflow-compiler") || !containsString(impl.ContextPackRefs, "jira-context:GENERIC-1044:scope") {
		t.Fatalf("implementation task missing workflow or compile context refs: %#v", impl.ContextPackRefs)
	}
	reviewer := taskByRef(t, tasks, "review-implement-step-review-implement")
	if reviewer.OwnerAgent != "reviewer" {
		t.Fatalf("unexpected reviewer task owner: %#v", reviewer)
	}
	if !strings.Contains(reviewer.Description, "Agent instructions: Review independently before approval.") || !strings.Contains(reviewer.Description, "Gate instructions: Review changed files") {
		t.Fatalf("reviewer task missing reviewer agent or gate instructions: %#v", reviewer)
	}
	if len(reviewer.DependencyTaskIDs) != 1 || reviewer.DependencyTaskIDs[0] != impl.ID {
		t.Fatalf("reviewer task must depend on implementation task: %#v", reviewer)
	}
	if reviewer.VerificationRequirement != "attach review_result_ref" || !containsString(reviewer.EvidenceNeeded, "reviewed task id") {
		t.Fatalf("reviewer task missing required review metadata: %#v", reviewer)
	}
	if !containsString(reviewer.FilesToRead, "internal/projectworkflow/compiler.go") || reviewer.ReviewGate == "" {
		t.Fatalf("reviewer task missing reviewed file packet fields: %#v", reviewer)
	}
	automationReviewer := taskByRef(t, tasks, "review-automation-step-review-automation")
	if automationReviewer.OwnerAgent != "reviewer" {
		t.Fatalf("unexpected automation reviewer owner: %#v", automationReviewer)
	}
	if !strings.Contains(automationReviewer.Description, "Agent instructions: Review independently before approval.") || !strings.Contains(automationReviewer.Description, "Gate instructions: Review automation refs") {
		t.Fatalf("automation reviewer task missing reviewer agent or gate instructions: %#v", automationReviewer)
	}
	if len(automationReviewer.DependencyTaskIDs) != 0 {
		t.Fatalf("automation reviewer task must not depend on tasks it gates: %#v", automationReviewer)
	}
	if !containsString(automationReviewer.EvidenceNeeded, "automation ref") || !containsString(automationReviewer.EvidenceNeeded, "allowed task refs") {
		t.Fatalf("automation reviewer task missing required evidence: %#v", automationReviewer)
	}

	var createdAutomation projectautomation.Automation
	var taskReviewAutomation projectautomation.Automation
	var automationReviewAutomation projectautomation.Automation
	for _, automationID := range result.AutomationIDs {
		automation, err := automations.GetAutomation(ctx, "project-1", automationID)
		if err != nil {
			t.Fatalf("get automation %s: %v", automationID, err)
		}
		switch {
		case len(automation.AllowedTaskRefs) == 1 && automation.AllowedTaskRefs[0] == automationReviewer.TaskRef:
			automationReviewAutomation = automation
		case len(automation.AllowedTaskRefs) == 1 && automation.AllowedTaskRefs[0] == reviewer.TaskRef:
			taskReviewAutomation = automation
		default:
			createdAutomation = automation
		}
	}
	if createdAutomation.ID == "" || taskReviewAutomation.ID == "" || automationReviewAutomation.ID == "" {
		t.Fatalf("expected executor and review automations, got executor=%#v taskReview=%#v automationReview=%#v", createdAutomation, taskReviewAutomation, automationReviewAutomation)
	}
	if createdAutomation.PlanID != result.WorkPlanID || !strings.HasPrefix(createdAutomation.AutomationRef, "workflow-ref:run-1:compile-") || !strings.HasSuffix(createdAutomation.AutomationRef, ":automation-step") {
		t.Fatalf("unexpected automation refs: %#v", createdAutomation)
	}
	if len(createdAutomation.AllowedTaskRefs) != 1 || createdAutomation.AllowedTaskRefs[0] != "implement-step" {
		t.Fatalf("automation allowed task refs must come from step dependencies: %#v", createdAutomation.AllowedTaskRefs)
	}
	if len(createdAutomation.RequiredReviewTaskIDs) != 1 || createdAutomation.RequiredReviewTaskIDs[0] != automationReviewer.ID {
		t.Fatalf("automation must require its generated review task before execution: %#v", createdAutomation.RequiredReviewTaskIDs)
	}
	if !strings.HasPrefix(createdAutomation.PermissionRef, "permission_snapshot:") {
		t.Fatalf("automation missing permission snapshot ref: %#v", createdAutomation)
	}
	if createdAutomation.SourceKind != projectautomation.AutomationSourceWorkflow {
		t.Fatalf("automation must be marked workflow-sourced: %#v", createdAutomation)
	}
	if taskReviewAutomation.AgentID != "reviewer" || taskReviewAutomation.TriggerKind != projectautomation.TriggerKindAutomatic || taskReviewAutomation.Status != projectautomation.AutomationStatusEnabled {
		t.Fatalf("task review automation must be enabled automatic reviewer work: %#v", taskReviewAutomation)
	}
	if taskReviewAutomation.SchedulePolicy != "on-ready-task" {
		t.Fatalf("task review automation must use the ready-task scheduler: %#v", taskReviewAutomation)
	}
	if len(taskReviewAutomation.AllowedTaskRefs) != 1 || taskReviewAutomation.AllowedTaskRefs[0] != reviewer.TaskRef {
		t.Fatalf("task review automation must be scoped to generated review task ref: %#v", taskReviewAutomation.AllowedTaskRefs)
	}
	if automationReviewAutomation.PlanID != result.WorkPlanID || !strings.HasPrefix(automationReviewAutomation.AutomationRef, "workflow-ref:run-1:compile-") || !strings.HasSuffix(automationReviewAutomation.AutomationRef, ":review-automation-step-review-automation") {
		t.Fatalf("unexpected automation review automation refs: %#v", automationReviewAutomation)
	}
	if automationReviewAutomation.AgentID != "reviewer" || automationReviewAutomation.TriggerKind != projectautomation.TriggerKindAutomatic || automationReviewAutomation.Status != projectautomation.AutomationStatusEnabled {
		t.Fatalf("automation review automation must be enabled automatic reviewer work: %#v", automationReviewAutomation)
	}
	if automationReviewAutomation.SchedulePolicy != "on-ready-task" {
		t.Fatalf("automation review automation must use the ready-task scheduler: %#v", automationReviewAutomation)
	}
	if len(automationReviewAutomation.AllowedTaskRefs) != 1 || automationReviewAutomation.AllowedTaskRefs[0] != automationReviewer.TaskRef {
		t.Fatalf("automation review automation must be scoped to generated review task ref: %#v", automationReviewAutomation.AllowedTaskRefs)
	}
	if strings.Contains(automationReviewAutomation.Purpose, automationReviewer.TaskRef) || strings.Contains(automationReviewAutomation.Purpose, "raw_prompt") {
		t.Fatalf("automation review automation purpose must not embed task refs or unsafe markers: %#v", automationReviewAutomation)
	}
	if !strings.HasPrefix(taskReviewAutomation.PermissionRef, "permission_snapshot:") || taskReviewAutomation.SourceKind != projectautomation.AutomationSourceWorkflow {
		t.Fatalf("task review automation must have workflow source and permission snapshot: %#v", taskReviewAutomation)
	}
	if !strings.HasPrefix(automationReviewAutomation.PermissionRef, "permission_snapshot:") || automationReviewAutomation.SourceKind != projectautomation.AutomationSourceWorkflow {
		t.Fatalf("automation review automation must have workflow source and permission snapshot: %#v", automationReviewAutomation)
	}
}

func TestCompileWorkflowUsesProjectBranchPolicyOptions(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, _ := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())
	svc.SetCompileOptionsByProject(map[string]CompileOptions{
		"project-1": {BranchPrefix: "", BranchSummaryTemplate: "chore-{{ticket_ref}}-{{workflow_ref}}"},
	})

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "run-1"}); err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one plan, got %d", len(plans))
	}
	if got, wantPrefix := plans[0].GitBranchRef, "chore-GENERIC-1044-workflow-ref-compile-"; !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("GitBranchRef = %q, want prefix %q", got, wantPrefix)
	}
}

func TestCompileWorkflowUsesGenericTicketForTicketlessSmokeBranch(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.ProjectID = "project-1"
	workflowStore.seedWorkflow(workflow)
	svc.SetCompileOptionsByProject(map[string]CompileOptions{
		"project-1": {BranchPrefix: "", BranchSummaryTemplate: "chore-{{ticket_ref}}-{{workflow_ref}}"},
	})

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "input:smoke-20260608g", CreatedByRunID: "codex-manual-smoke"}); err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected one plan, got %d", len(plans))
	}
	if got, wantPrefix := plans[0].GitBranchRef, "chore-smoke-20260608g-workflow-ref-compile-"; !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("ticketless smoke branch must use generic input token, got %q want prefix %q", got, wantPrefix)
	}
	if strings.Contains(plans[0].GitBranchRef, "input-smoke") {
		t.Fatalf("ticketless smoke branch must not leak input ref into ticket slot, got %q", plans[0].GitBranchRef)
	}
}

func TestCompileWorkflowRendersSafeUserRequestFallbackWhenOmitted(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.Agents[0].Instructions = "Create exactly one line: smoke input ref: {{user_request_ref}}."
	workflow.Steps[0].Description = "Write smoke input ref: {{user_request_ref}}."
	workflowStore.seedWorkflow(workflow)

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	task := taskByRef(t, tasks, "implement-step")
	if !strings.Contains(task.Description, "smoke input ref: unspecified-request.") {
		t.Fatalf("expected safe fallback user request ref, got %q", task.Description)
	}
	if strings.Contains(task.Description, "smoke input ref: .") || strings.Contains(task.Description, "{{user_request_ref}}") {
		t.Fatalf("compiled task kept unsafe or unresolved user request placeholder: %q", task.Description)
	}
}

func TestCompileWorkflowProjectBranchPolicyStaysUniqueAcrossSameTicketCompiles(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, _ := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())
	svc.SetCompileOptionsByProject(map[string]CompileOptions{
		"project-1": {BranchPrefix: "", BranchSummaryTemplate: "chore-{{ticket_ref}}-{{workflow_ref}}"},
	})

	for i := 0; i < 2; i++ {
		if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "jira:GENERIC-1044", CreatedByRunID: "operator-real-ticket-run"}); err != nil {
			t.Fatalf("compile workflow %d: %v", i+1, err)
		}
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected two plans, got %d", len(plans))
	}
	seenBranches := map[string]struct{}{}
	seenWorktrees := map[string]struct{}{}
	for _, plan := range plans {
		if !strings.HasPrefix(plan.GitBranchRef, "chore-GENERIC-1044-workflow-ref-compile-") {
			t.Fatalf("GitBranchRef must preserve policy prefix and compile uniqueness, got %q", plan.GitBranchRef)
		}
		if _, exists := seenBranches[plan.GitBranchRef]; exists {
			t.Fatalf("GitBranchRef must be unique across same-ticket compiles, duplicate %q", plan.GitBranchRef)
		}
		seenBranches[plan.GitBranchRef] = struct{}{}
		if _, exists := seenWorktrees[plan.GitWorktreeRef]; exists {
			t.Fatalf("GitWorktreeRef must be unique across same-ticket compiles, duplicate %q", plan.GitWorktreeRef)
		}
		seenWorktrees[plan.GitWorktreeRef] = struct{}{}
	}
}

func TestCompileWorkflowRefreshesChangedPermissionSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflowStore.seedWorkflow(workflow)
	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-1"}); err != nil {
		t.Fatalf("initial compile workflow: %v", err)
	}
	initial := snapshotForAgent(t, workflowStore.snapshotList(), "worker")

	workflow.Agents[0].AllowedTools = append(workflow.Agents[0].AllowedTools, "projects.workspace.git_diff")
	workflowStore.seedWorkflow(workflow)
	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-2"}); err != nil {
		t.Fatalf("compile after permission change: %v", err)
	}
	updated := snapshotForAgent(t, workflowStore.snapshotList(), "worker")
	if initial.ID != updated.ID {
		t.Fatalf("expected stable snapshot id, got first=%q updated=%q", initial.ID, updated.ID)
	}
	if initial.ContentHash == updated.ContentHash || !containsString(updated.AllowedTools, "projects.workspace.git_diff") {
		t.Fatalf("expected compile to refresh changed snapshot: initial=%#v updated=%#v", initial, updated)
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
	if !result.DryRun || result.WorkPlanID == "" || len(result.WorkTaskIDs) != 1 || len(result.ReviewerTaskIDs) != 2 || len(result.AutomationIDs) != 3 {
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

func TestCompileWorkflowMaterializesAutomationStepDependencies(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, automations := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.Steps = append(workflow.Steps, WorkflowStep{
		ID:                 "mark-ready-after-review",
		Kind:               WorkflowStepKindWorkTask,
		Title:              "Mark Ready After Review",
		Agent:              "automation",
		DependsOn:          []string{"automation-step"},
		Description:        "Finalize only after automation-step task and review gates are done.",
		EvidenceNeeded:     []string{"review-result-ref"},
		FilesToRead:        []string{"internal/projectworkflow/compiler.go"},
		FilesToEdit:        []string{},
		ReviewGate:         "review-implement approval required before done",
		ResumeInstructions: "resume from compiler task metadata only",
	})
	workflow.ReviewGates[0].AppliesTo = append(workflow.ReviewGates[0].AppliesTo, "mark-ready-after-review")
	workflowStore.seedWorkflow(workflow)

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-1", CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}

	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	impl := taskByRef(t, tasks, "implement-step")
	implReview := taskByRef(t, tasks, "review-implement-step-review-implement")
	automationReview := taskByRef(t, tasks, "review-automation-step-review-automation")
	markReady := taskByRef(t, tasks, "mark-ready-after-review")
	for _, required := range []string{impl.ID, implReview.ID, automationReview.ID} {
		if !containsString(markReady.DependencyTaskIDs, required) {
			t.Fatalf("mark-ready task must depend on concrete automation prerequisite %s, got %#v", required, markReady.DependencyTaskIDs)
		}
	}

	var markReadyAutomation projectautomation.Automation
	for _, automationID := range result.AutomationIDs {
		automation, err := automations.GetAutomation(ctx, "project-1", automationID)
		if err != nil {
			t.Fatalf("get automation %s: %v", automationID, err)
		}
		if len(automation.AllowedTaskRefs) == 1 && automation.AllowedTaskRefs[0] == markReady.TaskRef {
			markReadyAutomation = automation
			break
		}
	}
	if markReadyAutomation.ID == "" || markReadyAutomation.Status != projectautomation.AutomationStatusEnabled || markReadyAutomation.TriggerKind != projectautomation.TriggerKindAutomatic {
		t.Fatalf("uncovered workflow task must get enabled automatic task automation, got %#v", markReadyAutomation)
	}
}

func TestCompileWorkflowMaterializesAutomationBatchAsExecutableTask(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, automations := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.Steps[1].Kind = WorkflowStepKindAutomationBatch
	workflow.Steps[1].ID = "run-implementation-batch"
	workflow.Steps[1].Title = "Run Implementation Batch"
	workflow.Steps = append(workflow.Steps, WorkflowStep{
		ID:                      "orchestrator-verification",
		Kind:                    WorkflowStepKindWorkTask,
		Title:                   "Orchestrator Verification",
		Agent:                   "automation",
		DependsOn:               []string{"run-implementation-batch"},
		Description:             "Verify only after implementation batch execution and review.",
		EvidenceNeeded:          []string{"focused-test-ref", "review-result-ref"},
		FilesToRead:             []string{"internal/projectworkflow/compiler.go"},
		VerificationRequirement: "run focused verifier after implementation review",
		ReviewGate:              "review-implement approval required before verification completion",
		ResumeInstructions:      "resume from implementation batch refs and review refs",
	})
	workflow.ReviewGates[1].AppliesTo = []string{"run-implementation-batch"}
	workflow.ReviewGates[0].AppliesTo = append(workflow.ReviewGates[0].AppliesTo, "orchestrator-verification")
	workflowStore.seedWorkflow(workflow)

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-1", CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}

	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	batch := taskByRef(t, tasks, "run-implementation-batch")
	batchReview := taskByRef(t, tasks, "review-run-implementation-batch-review-automation")
	verification := taskByRef(t, tasks, "orchestrator-verification")
	if batch.OwnerAgent != "automation" || batch.VerificationRequirement == "" {
		t.Fatalf("automation batch must compile into executable task metadata, got %#v", batch)
	}
	if len(batchReview.DependencyTaskIDs) != 1 || batchReview.DependencyTaskIDs[0] != batch.ID {
		t.Fatalf("automation batch review must depend on concrete batch task, got %#v", batchReview)
	}
	for _, required := range []string{batch.ID, batchReview.ID} {
		if !containsString(verification.DependencyTaskIDs, required) {
			t.Fatalf("verification must wait for batch execution and review %s, got %#v", required, verification.DependencyTaskIDs)
		}
	}

	var batchAutomation projectautomation.Automation
	for _, automationID := range result.AutomationIDs {
		automation, err := automations.GetAutomation(ctx, "project-1", automationID)
		if err != nil {
			t.Fatalf("get automation %s: %v", automationID, err)
		}
		if containsString(automation.AllowedTaskRefs, batch.TaskRef) && automation.AgentID == "automation" {
			batchAutomation = automation
			break
		}
	}
	if batchAutomation.ID == "" || batchAutomation.Status != projectautomation.AutomationStatusEnabled || batchAutomation.TriggerKind != projectautomation.TriggerKindAutomatic {
		t.Fatalf("automation batch task must have enabled automatic execution metadata, got %#v", batchAutomation)
	}
	if len(batchAutomation.RequiredReviewTaskIDs) != 0 {
		t.Fatalf("automation batch execution must not wait on its post-execution review task, got %#v", batchAutomation.RequiredReviewTaskIDs)
	}
}

func TestCompileWorkflowCreatesFallbackTaskAutomationWhenWorkflowAutomationIsManualDraft(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, automations := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflow.Steps[1].AutomationStatus = projectautomation.AutomationStatusDraft
	workflow.Steps[1].TriggerKind = projectautomation.TriggerKindManual
	workflow.Steps[1].SchedulePolicy = ""
	workflowStore.seedWorkflow(workflow)

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", UserRequestRef: "request-1", CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}

	var fallback projectautomation.Automation
	var manualStep projectautomation.Automation
	for _, automationID := range result.AutomationIDs {
		automation, err := automations.GetAutomation(ctx, "project-1", automationID)
		if err != nil {
			t.Fatalf("get automation %s: %v", automationID, err)
		}
		if len(automation.AllowedTaskRefs) != 1 || automation.AllowedTaskRefs[0] != "implement-step" {
			continue
		}
		if automation.AgentID == "worker" {
			fallback = automation
		}
		if automation.AgentID == "automation" {
			manualStep = automation
		}
	}
	if manualStep.ID == "" || manualStep.Status != projectautomation.AutomationStatusDraft || manualStep.TriggerKind != projectautomation.TriggerKindManual {
		t.Fatalf("expected manual draft workflow automation to remain metadata only, got %#v", manualStep)
	}
	if fallback.ID == "" || fallback.Status != projectautomation.AutomationStatusEnabled || fallback.TriggerKind != projectautomation.TriggerKindAutomatic || fallback.SchedulePolicy != "on-ready-task" {
		t.Fatalf("expected uncovered task fallback automation, got %#v", fallback)
	}
}

func TestCompileWorkflowToWorkPlanOmitsUnverifiedGitBaseRefOnOriginMasterRepo(t *testing.T) {
	if !hasLocalGitRef(t, "origin/master") {
		t.Skip("requires local origin/master ref")
	}
	if hasLocalGitRef(t, "origin/main") {
		t.Skip("requires origin/master repo without origin/main ref")
	}

	ctx := context.Background()
	svc, workflowStore, workPlans, _ := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())

	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list work plans: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != result.WorkPlanID {
		t.Fatalf("unexpected compiled plan: %#v", plans)
	}
	if plans[0].GitBaseRef == "main" {
		t.Fatalf("compile_to_work_plan must not hardcode main as git base ref: %#v", plans[0])
	}
	if plans[0].GitBaseRef != "" {
		t.Fatalf("compile_to_work_plan must omit unverified git base ref, got %q", plans[0].GitBaseRef)
	}
}

func hasLocalGitRef(t *testing.T, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", filepath.Join("..", ".."), "rev-parse", "--verify", "--quiet", ref)
	return cmd.Run() == nil
}

func TestCompileWorkflowAllowsRepeatedRuns(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, workPlans, automations := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())

	first, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("first compile workflow: %v", err)
	}
	second, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("second compile workflow: %v", err)
	}
	if first.WorkPlanID == second.WorkPlanID {
		t.Fatalf("expected distinct work plans: first=%s second=%s", first.WorkPlanID, second.WorkPlanID)
	}

	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list work plans: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected two compiled plans, got %#v", plans)
	}
	autos, err := automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list automations: %v", err)
	}
	if len(autos) != 6 {
		t.Fatalf("expected two executor and four reviewer automations, got %#v", autos)
	}
}

func TestCompileWorkflowRefreshesStalePermissionSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflowStore.seedWorkflow(workflow)
	staleSnapshotID := permissionSnapshotID(workflow.ID, "worker")
	workflowStore.snapshots[workflow.ProjectID+"\x00"+staleSnapshotID] = WorkflowPermissionSnapshot{
		ID:          staleSnapshotID,
		ProjectID:   workflow.ProjectID,
		WorkflowID:  workflow.ID,
		AgentID:     "worker",
		ContentHash: "sha256-stale",
	}

	if _, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: "project-1", WorkflowID: "workflow-1"}); err != nil {
		t.Fatalf("compile should refresh stale permission snapshot: %v", err)
	}
	updated := snapshotForAgent(t, workflowStore.snapshotList(), "worker")
	if updated.ContentHash == "sha256-stale" {
		t.Fatalf("stale permission snapshot was not refreshed: %#v", updated)
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
			{ID: "worker", Purpose: "Implement bounded task metadata.", Instructions: "Implement exactly one scoped task.", SecretPolicy: "deny", LogPolicy: "metadata_only", CreatedAt: now, UpdatedAt: now},
			{ID: "reviewer", Purpose: "Review implementation task evidence.", Instructions: "Review independently before approval.", SecretPolicy: "deny", LogPolicy: "metadata_only", CreatedAt: now, UpdatedAt: now},
			{ID: "automation", Purpose: "Run governed automation metadata.", Instructions: "Queue only reviewed ready task refs.", SecretPolicy: "deny", LogPolicy: "metadata_only", CreatedAt: now, UpdatedAt: now},
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
				FilesToRead:             []string{"internal/projectworkflow/compiler.go"},
				FilesToEdit:             []string{"internal/projectworkflow/compiler.go"},
				LikelyFilesAffected:     []string{"internal/projectworkflow/compiler.go"},
				VerificationRequirement: "orchestrator runs focused compiler tests",
				ExpectedOutput:          "compiler creates governed metadata",
				FailureCriteria:         "block if compiler persists unsafe metadata",
				ReviewGate:              "review-implement approval required before done",
				ResumeInstructions:      "resume from compiler task metadata only",
			},
			{
				ID:               "automation-step",
				Kind:             WorkflowStepKindAutomation,
				Title:            "Run Automation",
				Agent:            "automation",
				DependsOn:        []string{"implement-step"},
				Description:      "Queue governed automation for ready implementation tasks.",
				AutomationStatus: projectautomation.AutomationStatusEnabled,
				TriggerKind:      projectautomation.TriggerKindAutomatic,
				SchedulePolicy:   "on-ready-task",
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

func (store *compilerWorkflowStore) UpdatePermissionSnapshot(_ context.Context, snapshot WorkflowPermissionSnapshot) (WorkflowPermissionSnapshot, error) {
	key := snapshot.ProjectID + "\x00" + snapshot.ID
	if _, ok := store.snapshots[key]; !ok {
		return WorkflowPermissionSnapshot{}, errors.New("snapshot not found")
	}
	store.snapshots[key] = cloneTestSnapshot(snapshot)
	return cloneTestSnapshot(snapshot), nil
}

func (store *compilerWorkflowStore) snapshotList() []WorkflowPermissionSnapshot {
	out := make([]WorkflowPermissionSnapshot, 0, len(store.snapshots))
	for _, snapshot := range store.snapshots {
		out = append(out, cloneTestSnapshot(snapshot))
	}
	return out
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
		workflow.Steps[i].FilesToRead = append([]string(nil), workflow.Steps[i].FilesToRead...)
		workflow.Steps[i].FilesToEdit = append([]string(nil), workflow.Steps[i].FilesToEdit...)
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
