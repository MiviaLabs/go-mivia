package projectworkflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestBaselineWorkflowMetadataContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "workflow statuses", []string{
		WorkflowStatusDraft,
		WorkflowStatusEnabled,
		WorkflowStatusDisabled,
		WorkflowStatusSuperseded,
	}, []string{"draft", "enabled", "disabled", "superseded"})

	assertExactSet(t, "workflow step kinds", []string{
		WorkflowStepKindWorkPlan,
		WorkflowStepKindWorkTask,
		WorkflowStepKindAutomation,
		WorkflowStepKindAutomationBatch,
		WorkflowStepKindReviewGate,
	}, []string{"work_plan", "work_task", "automation", "automation_batch", "review_gate"})

	assertExactSet(t, "review gate decisions", []string{
		ReviewGateDecisionApproved,
		ReviewGateDecisionRejected,
		ReviewGateDecisionNeedsChanges,
		ReviewGateDecisionBlocked,
	}, []string{"approved", "rejected", "needs_changes", "blocked"})
}

func TestBaselinePermissionSnapshotJSONContract(t *testing.T) {
	t.Parallel()

	snapshot := WorkflowPermissionSnapshot{
		ID:              "permission_snapshot:worker",
		ProjectID:       "project-1",
		AgentID:         "worker",
		WorkflowID:      "workflow-1",
		Instructions:    "Use bounded metadata refs only.",
		AllowedSkills:   []string{"mivia-mcp"},
		AllowedTools:    []string{"projects.workspace.file_read"},
		AllowedCommands: []string{"go test ./internal/projectworkflow"},
		DeniedCommands:  []string{"git push"},
		WorkspaceMode:   "edit",
		NetworkPolicy:   "disabled",
		SecretPolicy:    "deny",
		LogPolicy:       "metadata_only",
		MaxRuntime:      "10m",
		MaxRetries:      1,
		ContentHash:     "sha256-test",
		CreatedByRunID:  "run-1",
		TraceID:         "trace-1",
		CreatedAt:       time.Unix(1, 0).UTC(),
		UpdatedAt:       time.Unix(2, 0).UTC(),
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	for _, key := range []string{
		"id", "project_id", "agent_id", "workflow_id", "instructions",
		"allowed_skills", "allowed_tools", "allowed_commands", "denied_commands",
		"workspace_mode", "network_policy", "secret_policy", "log_policy",
		"max_runtime", "max_retries", "content_hash", "created_by_run_id", "trace_id",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("permission snapshot JSON omitted %q: %s", key, data)
		}
	}
}

func TestBaselineWorkflowCompileBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, workflowStore, workPlans, automations := newCompileFixture()
	workflowStore.seedWorkflow(baseCompileWorkflow())
	result, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{
		ProjectID:       "project-1",
		WorkflowID:      "workflow-1",
		UserRequestRef:  "jira:PROJ-1044",
		ContextPackRefs: []string{"jira-context:PROJ-1044:summary", "jira-context:PROJ-1044:scope"},
		CreatedByRunID:  "run-1",
		TraceID:         "trace-1",
	})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	if result.WorkPlanID == "" || len(result.WorkTaskIDs) == 0 || len(result.ReviewerTaskIDs) == 0 || len(result.AutomationIDs) == 0 || len(result.PermissionSnapshotIDs) == 0 {
		t.Fatalf("compile result missing governed output refs: %#v", result)
	}
	plans, err := workPlans.ListWorkPlans(ctx, projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans) != 1 || plans[0].ID != result.WorkPlanID || plans[0].IsolationMode != projectworkplan.WorkPlanIsolationDedicatedWorktree || plans[0].GitBranchRef == "" || plans[0].GitWorktreeRef == "" {
		t.Fatalf("compiled plan lost isolation/worktree refs: %#v", plans)
	}
	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: result.WorkPlanID})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	implementation := taskByRef(t, tasks, "implement-step")
	if implementation.OwnerAgent != "worker" || implementation.ReviewGate == "" || !containsString(implementation.ContextPackRefs, "jira-context:PROJ-1044:scope") || implementation.VerificationRequirement == "" || implementation.ExpectedOutput == "" || implementation.FailureCriteria == "" {
		t.Fatalf("compiled implementation task is not isolated-worker-ready: %#v", implementation)
	}
	reviewer := taskByRef(t, tasks, "review-implement-step-review-implement")
	if reviewer.OwnerAgent == implementation.OwnerAgent || len(reviewer.DependencyTaskIDs) != 1 || reviewer.DependencyTaskIDs[0] != implementation.ID {
		t.Fatalf("compiled review task lost independent reviewer dependency: impl=%#v reviewer=%#v", implementation, reviewer)
	}
	var workflowAutomation projectautomation.Automation
	for _, automationID := range result.AutomationIDs {
		automation, err := automations.GetAutomation(ctx, "project-1", automationID)
		if err != nil {
			t.Fatalf("get automation %s: %v", automationID, err)
		}
		if containsString(automation.AllowedTaskRefs, implementation.TaskRef) {
			workflowAutomation = automation
		}
	}
	if workflowAutomation.ID == "" || workflowAutomation.SourceKind != projectautomation.AutomationSourceWorkflow || workflowAutomation.TriggerKind != projectautomation.TriggerKindAutomatic || !strings.HasPrefix(workflowAutomation.PermissionRef, "permission_snapshot:") {
		t.Fatalf("compiled automation lost workflow trigger/source/permission contract: %#v", workflowAutomation)
	}
}

func TestBaselineWorkflowCompilePermissionSnapshotHashBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, workflowStore, _, _ := newCompileFixture()
	workflow := baseCompileWorkflow()
	workflowStore.seedWorkflow(workflow)

	first, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{
		ProjectID:      "project-1",
		WorkflowID:     "workflow-1",
		UserRequestRef: "jira:PROJ-1044",
		CreatedByRunID: "run-1",
		TraceID:        "trace-1",
	})
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	if len(first.PermissionSnapshotIDs) == 0 {
		t.Fatalf("compile did not create permission snapshot refs: %#v", first)
	}
	firstWorker := baselineSnapshotForAgent(t, workflowStore.snapshotList(), "worker")
	if !strings.HasPrefix(firstWorker.ContentHash, "sha256-") {
		t.Fatalf("permission snapshot content hash must be generated, got %#v", firstWorker)
	}

	second, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{
		ProjectID:      "project-1",
		WorkflowID:     "workflow-1",
		UserRequestRef: "jira:PROJ-1044",
		CreatedByRunID: "run-2",
		TraceID:        "trace-2",
	})
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	secondWorker := baselineSnapshotForAgent(t, workflowStore.snapshotList(), "worker")
	if firstWorker.ID != secondWorker.ID || firstWorker.ContentHash != secondWorker.ContentHash || len(second.PermissionSnapshotIDs) != len(first.PermissionSnapshotIDs) {
		t.Fatalf("unchanged permission snapshot should stay stable: first=%#v second=%#v", firstWorker, secondWorker)
	}

	workflow.Agents[0].AllowedTools = append(workflow.Agents[0].AllowedTools, "projects.workspace.git_diff")
	workflowStore.seedWorkflow(workflow)
	third, err := svc.CompileWorkflow(ctx, WorkflowCompileInput{
		ProjectID:      "project-1",
		WorkflowID:     "workflow-1",
		UserRequestRef: "jira:PROJ-1044",
		CreatedByRunID: "run-3",
		TraceID:        "trace-3",
	})
	if err != nil {
		t.Fatalf("third compile: %v", err)
	}
	thirdWorker := baselineSnapshotForAgent(t, workflowStore.snapshotList(), "worker")
	if firstWorker.ID != thirdWorker.ID || firstWorker.ContentHash == thirdWorker.ContentHash || !containsString(thirdWorker.AllowedTools, "projects.workspace.git_diff") {
		t.Fatalf("changed permission metadata must refresh snapshot hash in place: first=%#v third=%#v result=%#v", firstWorker, thirdWorker, third)
	}
}

func assertExactSet(t *testing.T, name string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s count mismatch: got %#v want %#v", name, got, want)
	}
	seen := map[string]int{}
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] != 1 {
			t.Fatalf("%s missing or duplicated %q in %#v", name, value, got)
		}
		delete(seen, value)
	}
	if len(seen) != 0 {
		t.Fatalf("%s has unexpected values: %#v", name, seen)
	}
}

func baselineSnapshotForAgent(t *testing.T, snapshots []WorkflowPermissionSnapshot, agentID string) WorkflowPermissionSnapshot {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.AgentID == agentID {
			return snapshot
		}
	}
	t.Fatalf("missing permission snapshot for agent %q in %#v", agentID, snapshots)
	return WorkflowPermissionSnapshot{}
}
