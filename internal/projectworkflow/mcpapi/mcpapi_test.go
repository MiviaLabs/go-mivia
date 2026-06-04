package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestToolDefinitionsExposeWorkflowToolsAndGovernanceDescriptions(t *testing.T) {
	definitions := ToolDefinitions()
	seen := map[string]string{}
	for _, definition := range definitions {
		name, _ := definition["name"].(string)
		description, _ := definition["description"].(string)
		seen[name] = description
	}
	for _, name := range []string{
		"projects.workflows.validate_toml",
		"projects.workflows.import_toml",
		"projects.workflows.get",
		"projects.workflows.list",
		"projects.workflows.update_status",
		"projects.workflows.compile_to_work_plan",
		"projects.agent_definitions.list",
		"projects.agent_definitions.get",
		"projects.permission_snapshots.get",
		"projects.permission_snapshots.list",
	} {
		if seen[name] == "" {
			t.Fatalf("missing workflow tool %s", name)
		}
		if !IsWorkflowTool(name) || !IsWorkflowTool(strings.ReplaceAll(name, ".", "_")) {
			t.Fatalf("workflow tool aliases not accepted for %s", name)
		}
	}
	compileDescription := seen["projects.workflows.compile_to_work_plan"]
	for _, required := range []string{"Work Plan", "Work Task", "review", "verifier", "evidence", "knowledge", "Codex CLI"} {
		if !strings.Contains(compileDescription, required) {
			t.Fatalf("compile description missing %q: %q", required, compileDescription)
		}
	}
	if !strings.Contains(seen["projects.workflows.validate_toml"], "never executes raw prompts") {
		t.Fatalf("validate description does not enforce metadata-only TOML: %q", seen["projects.workflows.validate_toml"])
	}
}

func TestCallToolValidateTOMLReturnsIssuesWithoutPersistence(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	bad := strings.Replace(workflowMCPValidTOML(), `reviewer_agent = "reviewer"`, `reviewer_agent = "missing-reviewer"`, 1)
	result, err := CallTool(ctx, svc, "projects.workflows.validate_toml", mustArgs(t, map[string]any{"id": "project-1", "toml": bad}))
	if err != nil {
		t.Fatalf("validate TOML: %v", err)
	}
	structured := result["structuredContent"].(projectworkflow.ValidateWorkflowTOMLResult)
	if len(structured.Issues) == 0 {
		t.Fatalf("expected validation issues: %#v", structured)
	}
	workflows, err := svc.ListWorkflows(ctx, projectworkflow.WorkflowFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(workflows) != 0 {
		t.Fatalf("validate persisted workflow: %#v", workflows)
	}
}

func TestCallToolValidateCheckedInWorkflowTOML(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	for _, path := range checkedInWorkflowTOMLPaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workflow fixture %s: %v", path, err)
		}
		result, err := CallTool(ctx, svc, "projects.workflows.validate_toml", mustArgs(t, map[string]any{"id": "mivialabs-agents-monorepo", "toml": string(data)}))
		if err != nil {
			t.Fatalf("validate checked-in TOML %s: %v", path, err)
		}
		structured := result["structuredContent"].(projectworkflow.ValidateWorkflowTOMLResult)
		if len(structured.Workflows) != 1 || len(structured.Issues) != 0 {
			t.Fatalf("unexpected validation result for %s: %#v", path, structured)
		}
	}
}

func TestCallToolImportMutatedCheckedInWorkflowTOML(t *testing.T) {
	ctx := context.Background()
	for _, path := range checkedInWorkflowTOMLPaths(t) {
		svc := projectworkflow.New(workflowstore.NewMemoryStore())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workflow fixture %s: %v", path, err)
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		toml := mutateTopLevelWorkflowRefs(string(data), "workflow-import-"+name, "import-"+name)

		validated, err := CallTool(ctx, svc, "projects.workflows.validate_toml", mustArgs(t, map[string]any{"id": "mivialabs-agents-monorepo", "toml": toml}))
		if err != nil {
			t.Fatalf("validate mutated checked-in TOML %s: %v", path, err)
		}
		validation := validated["structuredContent"].(projectworkflow.ValidateWorkflowTOMLResult)
		if len(validation.Workflows) != 1 || len(validation.Issues) != 0 {
			t.Fatalf("unexpected validation result for %s: %#v", path, validation)
		}

		imported, err := CallTool(ctx, svc, "projects.workflows.import_toml", mustArgs(t, map[string]any{"id": "mivialabs-agents-monorepo", "toml": toml, "created_by_run_id": "run-import"}))
		if err != nil {
			t.Fatalf("import mutated checked-in TOML %s: %v", path, err)
		}
		result := imported["structuredContent"].(projectworkflow.ImportWorkflowTOMLResult)
		if len(result.Workflows) != 1 || len(result.PermissionSnapshotIDs) == 0 {
			t.Fatalf("unexpected import result for %s: %#v", path, result)
		}
	}
}

func TestCallToolImportValidationFailureReturnsStructuredToolError(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	bad := strings.Replace(workflowMCPValidTOML(), `reviewer_agent = "reviewer"`, `reviewer_agent = "missing-reviewer"`, 1)

	result, err := CallTool(ctx, svc, "projects.workflows.import_toml", mustArgs(t, map[string]any{"id": "project-1", "toml": bad}))
	if err != nil {
		t.Fatalf("expected structured tool error result, got transport error: %v", err)
	}
	if result["isError"] != true {
		t.Fatalf("expected isError result: %#v", result)
	}
	structured := result["structuredContent"].(projectworkflow.ImportWorkflowTOMLResult)
	if !hasWorkflowIssue(structured.ValidationIssues, "unknown_reviewer_agent") {
		t.Fatalf("expected validation issues in structured content: %#v", structured.ValidationIssues)
	}
	content := result["content"].([]map[string]string)[0]["text"]
	if !strings.Contains(content, "unknown_reviewer_agent") || strings.Contains(content, "token=") {
		t.Fatalf("unexpected error content: %s", content)
	}
}

func TestCallToolCompileValidationFailureReturnsStructuredToolError(t *testing.T) {
	api := workflowValidationFailureAPI{
		value: projectworkflow.WorkflowCompileResult{
			WorkflowID: "workflow-1",
			ValidationIssues: []projectworkflow.WorkflowValidationIssue{{
				Code:      "missing_review_gate",
				Severity:  "error",
				FieldPath: "review_gates",
				Message:   "workflow requires review gate",
			}},
		},
		err: errors.New("invalid project workflow input: workflow validation failed"),
	}

	result, err := CallTool(context.Background(), api, "projects.workflows.compile_to_work_plan", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1"}))
	if err != nil {
		t.Fatalf("expected structured tool error result, got transport error: %v", err)
	}
	if result["isError"] != true {
		t.Fatalf("expected isError result: %#v", result)
	}
	structured := result["structuredContent"].(projectworkflow.WorkflowCompileResult)
	if !hasWorkflowIssue(structured.ValidationIssues, "missing_review_gate") {
		t.Fatalf("expected validation issues in structured content: %#v", structured.ValidationIssues)
	}
}

func TestCallToolValidateTOMLDropsUnsafeParsedWorkflow(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	unsafe := strings.Replace(workflowMCPValidTOML(), `purpose = "Compile bounded metadata into governed tasks."`, `purpose = "raw prompt token=secret"`, 1)
	result, err := CallTool(ctx, svc, "projects.workflows.validate_toml", mustArgs(t, map[string]any{"id": "project-1", "toml": unsafe}))
	if err != nil {
		t.Fatalf("validate TOML: %v", err)
	}
	if strings.Contains(result["content"].([]map[string]string)[0]["text"], "raw prompt") || strings.Contains(result["content"].([]map[string]string)[0]["text"], "token=secret") {
		t.Fatalf("validate result leaked unsafe content: %#v", result["content"])
	}
	structured := result["structuredContent"].(projectworkflow.ValidateWorkflowTOMLResult)
	if len(structured.Workflows) != 0 || len(structured.Issues) == 0 {
		t.Fatalf("expected issues without parsed unsafe workflows: %#v", structured)
	}
}

func checkedInWorkflowTOMLPaths(t *testing.T) []string {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", "..", "..", "configs", "workflows"))
	paths, err := filepath.Glob(filepath.Join(root, "*.toml"))
	if err != nil {
		t.Fatalf("glob workflow fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected checked-in workflow TOML fixtures")
	}
	return paths
}

func mutateTopLevelWorkflowRefs(toml, id, workflowRef string) string {
	lines := strings.Split(toml, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "id = ") {
			lines[i] = `id = "` + id + `"`
			break
		}
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "workflow_ref = ") {
			lines[i] = `workflow_ref = "` + workflowRef + `"`
			break
		}
	}
	return strings.Join(lines, "\n")
}

func hasWorkflowIssue(issues []projectworkflow.WorkflowValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

type workflowValidationFailureAPI struct {
	value any
	err   error
}

func (api workflowValidationFailureAPI) CallWorkflowTool(context.Context, string, json.RawMessage) (any, error) {
	return api.value, api.err
}

func TestCallToolImportGetListAndPermissionSnapshots(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	result, err := CallTool(ctx, svc, "projects.workflows.import_toml", mustArgs(t, map[string]any{"id": "project-1", "toml": workflowMCPValidTOML(), "created_by_run_id": "run-1"}))
	if err != nil {
		t.Fatalf("import TOML: %v", err)
	}
	imported := result["structuredContent"].(projectworkflow.ImportWorkflowTOMLResult)
	if len(imported.Workflows) != 1 || len(imported.PermissionSnapshots) != 3 {
		t.Fatalf("unexpected import result: %#v", imported)
	}
	if _, err := CallTool(ctx, svc, "projects.workflows.get", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1"})); err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	listed, err := CallTool(ctx, svc, "projects.agent_definitions.list", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1"}))
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(listed["structuredContent"].([]projectworkflow.WorkflowAgentDefinition)) != 3 {
		t.Fatalf("unexpected agent list: %#v", listed)
	}
	snapshots, err := CallTool(ctx, svc, "projects.permission_snapshots.list", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1"}))
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapshots["structuredContent"].([]projectworkflow.WorkflowPermissionSnapshot)) != 3 {
		t.Fatalf("unexpected snapshot list: %#v", snapshots)
	}
	if _, err := CallTool(ctx, svc, "projects.workflows.list", mustArgs(t, map[string]any{"id": "project-1", "page_size": 10, "page_token": "0"})); err != nil {
		t.Fatalf("list workflows with paging fields: %v", err)
	}
	if _, err := CallTool(ctx, svc, "projects.agent_definitions.list", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1", "page_size": 10, "page_token": "0"})); err != nil {
		t.Fatalf("list agents with paging fields: %v", err)
	}
	if _, err := CallTool(ctx, svc, "projects.permission_snapshots.list", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1", "page_size": 10, "page_token": "0"})); err != nil {
		t.Fatalf("list snapshots with paging fields: %v", err)
	}
}

func TestCallToolImportRebindsWorkflowToRequestProject(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	result, err := CallTool(ctx, svc, "projects.workflows.import_toml", mustArgs(t, map[string]any{"id": "project-2", "toml": workflowMCPValidTOML()}))
	if err != nil {
		t.Fatalf("expected project-scoped import to rebind workflow: %v", err)
	}
	imported := result["structuredContent"].(projectworkflow.ImportWorkflowTOMLResult)
	if len(imported.Workflows) != 1 || imported.Workflows[0].ProjectID != "project-2" {
		t.Fatalf("expected import to use request project, got %#v", imported)
	}
	workflows, err := svc.ListWorkflows(ctx, projectworkflow.WorkflowFilter{ProjectID: "project-2"})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(workflows) != 1 || workflows[0].ProjectID != "project-2" {
		t.Fatalf("expected rebound workflow to persist under request project: %#v", workflows)
	}
}

func TestCallToolCompileCreatesGovernedRefs(t *testing.T) {
	ctx := context.Background()
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{AllowManualRunner: true})
	svc.SetCompilerDependencies(workPlans, automations)
	if _, err := CallTool(ctx, svc, "projects.workflows.import_toml", mustArgs(t, map[string]any{"id": "project-1", "toml": workflowMCPValidTOML()})); err != nil {
		t.Fatalf("import TOML: %v", err)
	}
	result, err := CallTool(ctx, svc, "projects.workflows.compile_to_work_plan", mustArgs(t, map[string]any{"id": "project-1", "workflow_id": "workflow-1", "user_request_ref": "request-1"}))
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	compiled := result["structuredContent"].(projectworkflow.WorkflowCompileResult)
	if compiled.WorkPlanID == "" || len(compiled.WorkTaskIDs) != 1 || len(compiled.ReviewerTaskIDs) != 2 || len(compiled.AutomationIDs) != 1 {
		t.Fatalf("unexpected compile result: %#v", compiled)
	}
}

func TestCallToolImportSnapshotIDsRemainSafeAcrossRuns(t *testing.T) {
	for i := 0; i < 100; i++ {
		ctx := context.Background()
		svc := projectworkflow.New(workflowstore.NewMemoryStore())
		if _, err := CallTool(ctx, svc, "projects.workflows.import_toml", mustArgs(t, map[string]any{"id": "project-1", "toml": workflowMCPValidTOML()})); err != nil {
			t.Fatalf("import TOML iteration %d: %v", i, err)
		}
	}
}

func TestCallToolRejectsUnknownFields(t *testing.T) {
	_, err := CallTool(context.Background(), projectworkflow.New(workflowstore.NewMemoryStore()), "projects.workflows.get", json.RawMessage(`{"id":"project-1","workflow_id":"workflow-1","extra":"nope"}`))
	if err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func mustArgs(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return encoded
}

func workflowMCPValidTOML() string {
	return `[[workflows]]
id = "workflow-1"
project_id = "project-1"
workflow_ref = "workflow-ref"
title = "Governed Workflow"
purpose = "Compile bounded metadata into governed tasks."
status = "enabled"

[[workflows.agents]]
id = "worker"
display_name = "Worker"
purpose = "Implement one bounded task."
allowed_tools = ["projects.work_tasks.start"]
secret_policy = "deny"
log_policy = "metadata_only"

[[workflows.agents]]
id = "reviewer"
display_name = "Reviewer"
purpose = "Review bounded task evidence."
allowed_tools = ["projects.work_tasks.attach_review_result"]
secret_policy = "deny"
log_policy = "metadata_only"

[[workflows.agents]]
id = "automation"
display_name = "Automation"
purpose = "Queue governed automation."
allowed_tools = ["projects.automations.run"]
secret_policy = "deny"
log_policy = "metadata_only"

[[workflows.steps]]
id = "implement-step"
kind = "work_task"
title = "Implement Step"
agent = "worker"
description = "Implement bounded behavior."
evidence_needed = ["source-anchors-read"]
likely_files_affected = ["internal/projectworkflow/mcpapi/mcpapi.go"]
verification_requirement = "orchestrator runs focused workflow tests"
expected_output = "workflow MCP refs"
failure_criteria = "block if metadata safety fails"
resume_instructions = "resume from task metadata only"

[[workflows.steps]]
id = "automation-step"
kind = "automation"
title = "Run Automation"
agent = "automation"
depends_on = ["implement-step"]
description = "Queue governed automation."

[[workflows.review_gates]]
id = "review-implementation"
applies_to = ["implement-step"]
reviewer_agent = "reviewer"
required = true
independent_from_owner = true
required_artifacts = ["changed_files", "verifier_result_refs"]
allowed_actions = ["approved", "rejected"]
instructions = "Review changed files and verifier refs before deciding."

[[workflows.review_gates]]
id = "review-automation"
applies_to = ["automation-step"]
reviewer_agent = "reviewer"
required = true
independent_from_owner = true
required_artifacts = ["automation_ref", "permission_snapshot_ref"]
allowed_actions = ["approved", "rejected"]
instructions = "Review automation refs before execution."
`
}
