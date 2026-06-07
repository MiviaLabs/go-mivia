package projectworkflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceImportValidTOMLCreatesWorkflow(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)

	result, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(validWorkflowTOML()), CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("import workflow TOML: %v", err)
	}
	if len(result.Workflows) != 1 {
		t.Fatalf("expected one workflow, got %#v", result.Workflows)
	}
	created := result.Workflows[0]
	if created.ID != "workflow-1" || created.Status != WorkflowStatusDraft || created.CreatedByRunID != "run-1" || created.TraceID != "trace-1" {
		t.Fatalf("unexpected created workflow: %#v", created)
	}
	got, err := svc.GetWorkflow(ctx, "mivialabs-agents-monorepo", "workflow-1")
	if err != nil {
		t.Fatalf("get imported workflow: %v", err)
	}
	if got.WorkflowRef != "workflow-parser-validator" {
		t.Fatalf("unexpected workflow ref: %#v", got)
	}
	if got.Agents[0].Instructions != "Use bounded task metadata only." {
		t.Fatalf("agent instructions were not persisted: %#v", got.Agents[0])
	}
}

func TestServiceValidateOnlyDoesNotPersist(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)

	result, err := svc.ValidateWorkflowTOML(ctx, ValidateWorkflowTOMLInput{Data: []byte(validWorkflowTOML())})
	if err != nil {
		t.Fatalf("validate workflow TOML: %v", err)
	}
	if len(result.Workflows) != 1 || len(result.Issues) != 0 {
		t.Fatalf("unexpected validation result: %#v", result)
	}
	list, err := svc.ListWorkflows(ctx, WorkflowFilter{ProjectID: "mivialabs-agents-monorepo"})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("validate-only persisted workflows: %#v", list)
	}
}

func TestServiceImportInvalidReviewGateFailsAndPersistsNothing(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)
	bad := strings.Replace(validWorkflowTOML(), `reviewer_agent = "reviewer"`, `reviewer_agent = "missing-reviewer"`, 1)

	result, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(bad)})
	if err == nil {
		t.Fatal("expected import to fail")
	}
	if !hasIssue(result.ValidationIssues, "unknown_reviewer_agent") {
		t.Fatalf("expected review gate issue, got %#v", result.ValidationIssues)
	}
	list, err := svc.ListWorkflows(ctx, WorkflowFilter{ProjectID: "mivialabs-agents-monorepo"})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(list) != 0 || len(store.snapshots) != 0 {
		t.Fatalf("invalid import persisted workflows=%#v snapshots=%#v", list, store.snapshots)
	}
}

func TestServiceImportRequiresProjectScope(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)

	if _, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{Data: []byte(validWorkflowTOML())}); err == nil {
		t.Fatal("expected import without project scope to fail")
	}
	list, err := svc.ListWorkflows(ctx, WorkflowFilter{ProjectID: "mivialabs-agents-monorepo"})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(list) != 0 || len(store.snapshots) != 0 {
		t.Fatalf("unscoped import persisted workflows=%#v snapshots=%#v", list, store.snapshots)
	}
}

func TestServiceImportCreatesPermissionSnapshotForEveryAgent(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)

	result, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(validWorkflowTOML())})
	if err != nil {
		t.Fatalf("import workflow TOML: %v", err)
	}
	if len(result.PermissionSnapshots) != 2 || len(result.PermissionSnapshotIDs) != 2 {
		t.Fatalf("expected two permission snapshots, got %#v", result)
	}
	seen := map[string]bool{}
	for _, snapshot := range result.PermissionSnapshots {
		seen[snapshot.AgentID] = true
		if snapshot.WorkflowID != "workflow-1" || snapshot.ProjectID != "mivialabs-agents-monorepo" || !strings.HasPrefix(snapshot.ContentHash, "sha256-") {
			t.Fatalf("unexpected snapshot: %#v", snapshot)
		}
		if snapshot.SecretPolicy != "deny" || snapshot.LogPolicy != "metadata_only" {
			t.Fatalf("snapshot did not enforce metadata-only defaults: %#v", snapshot)
		}
		if snapshot.AgentID == "worker" && snapshot.Instructions != "Use bounded task metadata only." {
			t.Fatalf("snapshot did not preserve agent instructions: %#v", snapshot)
		}
	}
	if !seen["worker"] || !seen["reviewer"] {
		t.Fatalf("missing agent snapshots: %#v", seen)
	}
}

func TestServiceImportConfigWorkflowDefinitions(t *testing.T) {
	ctx := context.Background()
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "workflows", "*.toml"))
	if err != nil {
		t.Fatalf("glob workflow definitions: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected workflow definitions")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read workflow definition: %v", err)
			}
			store := newWorkflowServiceStore()
			svc := testWorkflowService(store)
			result, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mass-monorepo", Data: data, CreatedByRunID: "run-import", TraceID: "trace-import"})
			if err != nil {
				t.Fatalf("import checked-in workflow: %v issues=%#v", err, result.ValidationIssues)
			}
			if len(result.Workflows) != 1 {
				t.Fatalf("expected one imported workflow, got %#v", result.Workflows)
			}
			if len(result.PermissionSnapshotIDs) == 0 {
				t.Fatalf("expected permission snapshots for imported workflow, got %#v", result)
			}
		})
	}
}

func TestServicePermissionSnapshotIDsDoNotCollideForDigitRefs(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)
	toml := strings.Replace(validWorkflowTOML(), `id = "worker"`, `id = "worker.1"`, 1)
	toml = strings.Replace(toml, `id = "reviewer"`, `id = "worker/1"`, 1)
	toml = strings.Replace(toml, `agent = "worker"`, `agent = "worker.1"`, 1)
	toml = strings.Replace(toml, `agent = "worker"`, `agent = "worker.1"`, 1)
	toml = strings.Replace(toml, `reviewer_agent = "reviewer"`, `reviewer_agent = "worker/1"`, 1)

	result, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(toml)})
	if err != nil {
		t.Fatalf("import workflow TOML: %v", err)
	}
	if len(result.PermissionSnapshots) != 2 {
		t.Fatalf("expected two snapshots, got %#v", result.PermissionSnapshots)
	}
	if result.PermissionSnapshots[0].ID == result.PermissionSnapshots[1].ID {
		t.Fatalf("snapshot ids collided: %#v", result.PermissionSnapshots)
	}
}

func TestServicePermissionSnapshotHashChangesWhenPermissionsChange(t *testing.T) {
	ctx := context.Background()
	firstStore := newWorkflowServiceStore()
	firstSvc := testWorkflowService(firstStore)
	first, err := firstSvc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(validWorkflowTOML())})
	if err != nil {
		t.Fatalf("import first workflow: %v", err)
	}
	changedTOML := strings.Replace(validWorkflowTOML(), `allowed_tools = ["projects.workspace.file_read"]`, `allowed_tools = ["projects.workspace.file_read", "projects.workspace.git_diff"]`, 1)
	secondStore := newWorkflowServiceStore()
	secondSvc := testWorkflowService(secondStore)
	second, err := secondSvc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(changedTOML)})
	if err != nil {
		t.Fatalf("import changed workflow: %v", err)
	}

	firstWorker := snapshotForAgent(t, first.PermissionSnapshots, "worker")
	secondWorker := snapshotForAgent(t, second.PermissionSnapshots, "worker")
	if firstWorker.ContentHash == secondWorker.ContentHash {
		t.Fatalf("expected permission hash to change, got %q", firstWorker.ContentHash)
	}
	instructionTOML := strings.Replace(validWorkflowTOML(), `instructions = "Use bounded task metadata only."`, `instructions = "Use only declared files and bounded evidence refs."`, 1)
	thirdStore := newWorkflowServiceStore()
	thirdSvc := testWorkflowService(thirdStore)
	third, err := thirdSvc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(instructionTOML)})
	if err != nil {
		t.Fatalf("import instruction-changed workflow: %v", err)
	}
	thirdWorker := snapshotForAgent(t, third.PermissionSnapshots, "worker")
	if firstWorker.ContentHash == thirdWorker.ContentHash {
		t.Fatalf("expected permission hash to change when agent instructions change, got %q", firstWorker.ContentHash)
	}
	firstWorker.AllowedTools = append(firstWorker.AllowedTools, "mutated")
	stored := snapshotForAgent(t, firstStore.snapshotList(), "worker")
	if len(stored.AllowedTools) != 1 {
		t.Fatalf("stored snapshot was mutable through returned value: %#v", stored)
	}
}

func TestServiceImportExistingWorkflowRefreshesDefinitionAndSnapshots(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)

	first, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(validWorkflowTOML()), CreatedByRunID: "run-1", TraceID: "trace-1"})
	if err != nil {
		t.Fatalf("import first workflow: %v", err)
	}
	changedTOML := strings.Replace(validWorkflowTOML(), `instructions = "Use bounded task metadata only."`, `instructions = "Use refreshed bounded task metadata only."`, 1)
	changedTOML = strings.Replace(changedTOML, `allowed_tools = ["projects.workspace.file_read"]`, `allowed_tools = ["projects.workspace.file_read", "projects.workspace.git_diff"]`, 1)

	second, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(changedTOML), CreatedByRunID: "run-2", TraceID: "trace-2"})
	if err != nil {
		t.Fatalf("re-import changed workflow: %v", err)
	}
	if len(second.Workflows) != 1 || second.Workflows[0].ID != first.Workflows[0].ID {
		t.Fatalf("expected same workflow to refresh, got %#v", second.Workflows)
	}
	stored, err := svc.GetWorkflow(ctx, "mivialabs-agents-monorepo", "workflow-1")
	if err != nil {
		t.Fatalf("get refreshed workflow: %v", err)
	}
	if stored.Agents[0].Instructions != "Use refreshed bounded task metadata only." {
		t.Fatalf("workflow definition was not refreshed: %#v", stored.Agents[0])
	}
	firstWorker := snapshotForAgent(t, first.PermissionSnapshots, "worker")
	secondWorker := snapshotForAgent(t, second.PermissionSnapshots, "worker")
	if firstWorker.ID != secondWorker.ID {
		t.Fatalf("snapshot id should be stable across refresh: first=%q second=%q", firstWorker.ID, secondWorker.ID)
	}
	if firstWorker.ContentHash == secondWorker.ContentHash {
		t.Fatalf("snapshot content hash was not refreshed: %q", firstWorker.ContentHash)
	}
	storedWorker := snapshotForAgent(t, store.snapshotList(), "worker")
	if storedWorker.ContentHash != secondWorker.ContentHash || !containsString(storedWorker.AllowedTools, "projects.workspace.git_diff") {
		t.Fatalf("stored snapshot was not refreshed: %#v", storedWorker)
	}
}

func TestServiceUpdateWorkflowStatusRejectsInvalidTransition(t *testing.T) {
	ctx := context.Background()
	store := newWorkflowServiceStore()
	svc := testWorkflowService(store)
	toml := strings.Replace(validWorkflowTOML(), `status = "draft"`, `status = "enabled"`, 1)
	if _, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(toml)}); err != nil {
		t.Fatalf("import workflow TOML: %v", err)
	}
	if _, err := svc.UpdateWorkflowStatus(ctx, UpdateWorkflowStatusInput{ProjectID: "mivialabs-agents-monorepo", WorkflowID: "workflow-1", Status: WorkflowStatusDraft}); err == nil {
		t.Fatal("expected enabled -> draft transition to fail")
	}
	got, err := svc.GetWorkflow(ctx, "mivialabs-agents-monorepo", "workflow-1")
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if got.Status != WorkflowStatusEnabled {
		t.Fatalf("invalid transition changed status: %#v", got)
	}
}

func TestServiceTextLimitAndUnsafeContentChecks(t *testing.T) {
	ctx := context.Background()
	svc := testWorkflowService(newWorkflowServiceStore())
	longTitle := strings.Replace(validWorkflowTOML(), `title = "Workflow Parser Validator"`, `title = "`+strings.Repeat("x", 201)+`"`, 1)
	result, err := svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(longTitle)})
	if err == nil || !hasIssue(result.ValidationIssues, "too_long") {
		t.Fatalf("expected too_long issue, err=%v issues=%#v", err, result.ValidationIssues)
	}
	unsafe := strings.Replace(validWorkflowTOML(), `purpose = "Coordinate metadata-only workflow execution."`, `purpose = "token=secret"`, 1)
	result, err = svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: "mivialabs-agents-monorepo", Data: []byte(unsafe)})
	if err == nil || !hasIssue(result.ValidationIssues, "unsafe_text") {
		t.Fatalf("expected unsafe_text issue, err=%v issues=%#v", err, result.ValidationIssues)
	}
}

func testWorkflowService(store *workflowServiceStore) *Service {
	svc := New(store)
	now := time.Date(2026, 6, 4, 2, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	seq := 0
	svc.newID = func(prefix string) string {
		seq++
		return prefix + "-test-" + string(rune('a'+seq))
	}
	return svc
}

type workflowServiceStore struct {
	workflows map[string]WorkflowDefinition
	snapshots map[string]WorkflowPermissionSnapshot
}

func newWorkflowServiceStore() *workflowServiceStore {
	return &workflowServiceStore{workflows: map[string]WorkflowDefinition{}, snapshots: map[string]WorkflowPermissionSnapshot{}}
}

func (store *workflowServiceStore) CreateWorkflow(_ context.Context, workflow WorkflowDefinition) (WorkflowDefinition, error) {
	key := workflow.ProjectID + "\x00" + workflow.ID
	if _, ok := store.workflows[key]; ok {
		return WorkflowDefinition{}, errors.New("duplicate workflow")
	}
	workflow = cloneTestWorkflow(workflow)
	store.workflows[key] = workflow
	return cloneTestWorkflow(workflow), nil
}

func (store *workflowServiceStore) GetWorkflow(_ context.Context, projectID, workflowID string) (WorkflowDefinition, error) {
	workflow, ok := store.workflows[projectID+"\x00"+workflowID]
	if !ok {
		return WorkflowDefinition{}, errors.New("workflow not found")
	}
	return cloneTestWorkflow(workflow), nil
}

func (store *workflowServiceStore) ListWorkflows(_ context.Context, filter WorkflowFilter) ([]WorkflowDefinition, error) {
	out := make([]WorkflowDefinition, 0)
	for _, workflow := range store.workflows {
		if workflow.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Status != "" && workflow.Status != filter.Status {
			continue
		}
		if filter.WorkflowRef != "" && workflow.WorkflowRef != filter.WorkflowRef {
			continue
		}
		out = append(out, cloneTestWorkflow(workflow))
	}
	return out, nil
}

func (store *workflowServiceStore) UpdateWorkflow(_ context.Context, workflow WorkflowDefinition) (WorkflowDefinition, error) {
	key := workflow.ProjectID + "\x00" + workflow.ID
	if _, ok := store.workflows[key]; !ok {
		return WorkflowDefinition{}, errors.New("workflow not found")
	}
	workflow = cloneTestWorkflow(workflow)
	store.workflows[key] = workflow
	return cloneTestWorkflow(workflow), nil
}

func (store *workflowServiceStore) CreatePermissionSnapshot(_ context.Context, snapshot WorkflowPermissionSnapshot) (WorkflowPermissionSnapshot, error) {
	key := snapshot.ProjectID + "\x00" + snapshot.ID
	if _, ok := store.snapshots[key]; ok {
		return WorkflowPermissionSnapshot{}, errors.New("duplicate snapshot")
	}
	snapshot = cloneTestSnapshot(snapshot)
	store.snapshots[key] = snapshot
	return cloneTestSnapshot(snapshot), nil
}

func (store *workflowServiceStore) UpdatePermissionSnapshot(_ context.Context, snapshot WorkflowPermissionSnapshot) (WorkflowPermissionSnapshot, error) {
	key := snapshot.ProjectID + "\x00" + snapshot.ID
	if _, ok := store.snapshots[key]; !ok {
		return WorkflowPermissionSnapshot{}, errors.New("snapshot not found")
	}
	snapshot = cloneTestSnapshot(snapshot)
	store.snapshots[key] = snapshot
	return cloneTestSnapshot(snapshot), nil
}

func (store *workflowServiceStore) GetPermissionSnapshot(_ context.Context, projectID, snapshotID string) (WorkflowPermissionSnapshot, error) {
	snapshot, ok := store.snapshots[projectID+"\x00"+snapshotID]
	if !ok {
		return WorkflowPermissionSnapshot{}, errors.New("snapshot not found")
	}
	return cloneTestSnapshot(snapshot), nil
}

func (store *workflowServiceStore) ListPermissionSnapshots(_ context.Context, filter PermissionSnapshotFilter) ([]WorkflowPermissionSnapshot, error) {
	out := make([]WorkflowPermissionSnapshot, 0)
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

func (store *workflowServiceStore) snapshotList() []WorkflowPermissionSnapshot {
	out := make([]WorkflowPermissionSnapshot, 0, len(store.snapshots))
	for _, snapshot := range store.snapshots {
		out = append(out, cloneTestSnapshot(snapshot))
	}
	return out
}

func cloneTestWorkflow(workflow WorkflowDefinition) WorkflowDefinition {
	workflow.Agents = append([]WorkflowAgentDefinition(nil), workflow.Agents...)
	for i := range workflow.Agents {
		workflow.Agents[i].AllowedSkills = append([]string(nil), workflow.Agents[i].AllowedSkills...)
		workflow.Agents[i].AllowedTools = append([]string(nil), workflow.Agents[i].AllowedTools...)
		workflow.Agents[i].AllowedCommands = append([]string(nil), workflow.Agents[i].AllowedCommands...)
		workflow.Agents[i].DeniedCommands = append([]string(nil), workflow.Agents[i].DeniedCommands...)
	}
	workflow.PermissionSnapshots = append([]WorkflowPermissionSnapshot(nil), workflow.PermissionSnapshots...)
	for i := range workflow.PermissionSnapshots {
		workflow.PermissionSnapshots[i] = cloneTestSnapshot(workflow.PermissionSnapshots[i])
	}
	return workflow
}

func cloneTestSnapshot(snapshot WorkflowPermissionSnapshot) WorkflowPermissionSnapshot {
	snapshot.AllowedSkills = append([]string(nil), snapshot.AllowedSkills...)
	snapshot.AllowedTools = append([]string(nil), snapshot.AllowedTools...)
	snapshot.AllowedCommands = append([]string(nil), snapshot.AllowedCommands...)
	snapshot.DeniedCommands = append([]string(nil), snapshot.DeniedCommands...)
	return snapshot
}

func hasIssue(issues []WorkflowValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func snapshotForAgent(t *testing.T, snapshots []WorkflowPermissionSnapshot, agentID string) WorkflowPermissionSnapshot {
	t.Helper()
	for _, snapshot := range snapshots {
		if snapshot.AgentID == agentID {
			return snapshot
		}
	}
	t.Fatalf("missing snapshot for agent %q in %#v", agentID, snapshots)
	return WorkflowPermissionSnapshot{}
}
