package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestWorkflowRoutesValidateImportListGetStatusAndCompile(t *testing.T) {
	svc, workPlans, automations := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)

	validation := requestJSON[projectworkflow.ValidateWorkflowTOMLResult](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/validate-toml", map[string]any{
		"toml": validWorkflowTOML(),
	}, http.StatusOK)
	if len(validation.Workflows) != 1 || len(validation.Issues) != 0 {
		t.Fatalf("unexpected validation result: %#v", validation)
	}

	imported := requestJSON[projectworkflow.ImportWorkflowTOMLResult](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/import-toml", map[string]any{
		"toml":              validWorkflowTOML(),
		"created_by_run_id": "run-1",
		"trace_id":          "trace-1",
	}, http.StatusCreated)
	if len(imported.Workflows) != 1 || imported.Workflows[0].ProjectID != "project-1" || imported.Workflows[0].CreatedByRunID != "run-1" {
		t.Fatalf("unexpected import result: %#v", imported)
	}
	if len(imported.PermissionSnapshotIDs) != 2 {
		t.Fatalf("expected permission snapshots for both agents: %#v", imported.PermissionSnapshotIDs)
	}

	list := requestJSON[workflowListResponse](t, mux, http.MethodGet, "/api/v1/projects/project-1/workflows", nil, http.StatusOK)
	if len(list.Workflows) != 1 || list.Workflows[0].ID != "workflow-1" {
		t.Fatalf("unexpected workflow list: %#v", list)
	}
	got := requestJSON[projectworkflow.WorkflowDefinition](t, mux, http.MethodGet, "/api/v1/projects/project-1/workflows/workflow-1", nil, http.StatusOK)
	if got.WorkflowRef != "workflow-rest" || got.ProjectID != "project-1" {
		t.Fatalf("unexpected workflow get: %#v", got)
	}

	updated := requestJSON[projectworkflow.WorkflowDefinition](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/workflow-1/status", map[string]any{
		"status": projectworkflow.WorkflowStatusDisabled,
	}, http.StatusOK)
	if updated.Status != projectworkflow.WorkflowStatusDisabled {
		t.Fatalf("unexpected workflow status: %#v", updated)
	}
	updated = requestJSON[projectworkflow.WorkflowDefinition](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/workflow-1/status", map[string]any{
		"status": projectworkflow.WorkflowStatusEnabled,
	}, http.StatusOK)
	if updated.Status != projectworkflow.WorkflowStatusEnabled {
		t.Fatalf("unexpected workflow status re-enable: %#v", updated)
	}

	compiled := requestJSON[projectworkflow.WorkflowCompileResult](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/workflow-1/compile", map[string]any{
		"user_request_ref": "request-1",
	}, http.StatusOK)
	if compiled.WorkPlanID == "" || len(compiled.WorkTaskIDs) != 1 || len(compiled.ReviewerTaskIDs) != 1 || len(compiled.AutomationIDs) != 0 {
		t.Fatalf("unexpected compile result: %#v", compiled)
	}
	plans, err := workPlans.ListWorkPlans(t.Context(), projectworkplan.WorkPlanFilter{ProjectID: "project-1"})
	if err != nil || len(plans) != 1 {
		t.Fatalf("expected one compiled work plan, plans=%#v err=%v", plans, err)
	}
	runs, err := automations.ListRuns(t.Context(), projectautomation.RunFilter{ProjectID: "project-1"})
	if err != nil || len(runs) != 0 {
		t.Fatalf("compile route must not start automation runs, runs=%#v err=%v", runs, err)
	}
}

func TestWorkflowRoutesValidateCheckedInWorkflowTOML(t *testing.T) {
	svc, _, _ := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)
	for _, path := range checkedInWorkflowTOMLPaths(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workflow fixture %s: %v", path, err)
		}
		validation := requestJSON[projectworkflow.ValidateWorkflowTOMLResult](t, mux, http.MethodPost, "/api/v1/projects/mivialabs-agents-monorepo/workflows/validate-toml", map[string]any{
			"toml": string(data),
		}, http.StatusOK)
		if len(validation.Workflows) != 1 || len(validation.Issues) != 0 {
			t.Fatalf("unexpected validation result for %s: %#v", path, validation)
		}
	}
}

func TestWorkflowRoutesAgentDefinitionsAndPermissionSnapshots(t *testing.T) {
	svc, _, _ := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)
	requestJSON[projectworkflow.ImportWorkflowTOMLResult](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/import-toml", map[string]any{
		"toml": validWorkflowTOML(),
	}, http.StatusCreated)

	agents := requestJSON[agentDefinitionListResponse](t, mux, http.MethodGet, "/api/v1/projects/project-1/workflows/workflow-1/agent-definitions", nil, http.StatusOK)
	if len(agents.AgentDefinitions) != 2 {
		t.Fatalf("unexpected agent definition list: %#v", agents)
	}
	agent := requestJSON[projectworkflow.WorkflowAgentDefinition](t, mux, http.MethodGet, "/api/v1/projects/project-1/workflows/workflow-1/agent-definitions/worker", nil, http.StatusOK)
	if agent.ID != "worker" || agent.LogPolicy != "metadata_only" {
		t.Fatalf("unexpected agent definition: %#v", agent)
	}

	snapshots := requestJSON[permissionSnapshotListResponse](t, mux, http.MethodGet, "/api/v1/projects/project-1/permission-snapshots?workflow_id=workflow-1", nil, http.StatusOK)
	if len(snapshots.PermissionSnapshots) != 2 {
		t.Fatalf("unexpected permission snapshots: %#v", snapshots)
	}
	snapshot := requestJSON[projectworkflow.WorkflowPermissionSnapshot](t, mux, http.MethodGet, "/api/v1/projects/project-1/permission-snapshots/permission-snapshot-workflow-b-worker", nil, http.StatusOK)
	if snapshot.AgentID != "worker" || snapshot.WorkflowID != "workflow-1" || !strings.HasPrefix(snapshot.ContentHash, "sha256-") {
		t.Fatalf("unexpected permission snapshot: %#v", snapshot)
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

func TestWorkflowRoutesRejectUnknownJSONFields(t *testing.T) {
	svc, _, _ := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)

	response := requestRaw(t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/import-toml", map[string]any{
		"toml":    validWorkflowTOML(),
		"unknown": "field",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for unknown field, got %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "unknown") {
		t.Fatalf("unknown field response should stay generic: %s", response.Body.String())
	}
}

func TestWorkflowRoutesRejectTrailingJSON(t *testing.T) {
	svc, _, _ := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)

	body, err := json.Marshal(map[string]string{"toml": validWorkflowTOML()})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/project-1/workflows/validate-toml", strings.NewReader(string(body)+" {}"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for trailing JSON, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestWorkflowRoutesRebindImportToRequestProject(t *testing.T) {
	svc, _, _ := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)
	toml := strings.Replace(validWorkflowTOML(), `project_id = "project-1"`, `project_id = "other-project"`, 1)

	imported := requestJSON[projectworkflow.ImportWorkflowTOMLResult](t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/import-toml", map[string]any{
		"toml": toml,
	}, http.StatusCreated)
	if len(imported.Workflows) != 1 || imported.Workflows[0].ProjectID != "project-1" {
		t.Fatalf("expected imported workflow to use request project: %#v", imported)
	}
	list := requestJSON[workflowListResponse](t, mux, http.MethodGet, "/api/v1/projects/project-1/workflows", nil, http.StatusOK)
	if len(list.Workflows) != 1 || list.Workflows[0].ProjectID != "project-1" {
		t.Fatalf("expected workflow under request project: %#v", list)
	}
}

func TestWorkflowRoutesRejectUnsafeInputWithoutEchoingRawTOML(t *testing.T) {
	svc, _, _ := newWorkflowHTTPFixture()
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)
	toml := strings.Replace(validWorkflowTOML(), `purpose = "metadata-only workflow for REST tests"`, `purpose = "token=secret"`, 1)

	response := requestRaw(t, mux, http.MethodPost, "/api/v1/projects/project-1/workflows/import-toml", map[string]any{
		"toml": toml,
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe TOML rejection, got %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "token=secret") || strings.Contains(response.Body.String(), toml) {
		t.Fatalf("unsafe response echoed raw TOML: %s", response.Body.String())
	}
}

func newWorkflowHTTPFixture() (*projectworkflow.Service, *projectworkplan.Service, *projectautomation.Service) {
	workPlans := projectworkplan.New(workplanstore.NewMemoryStore())
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:           true,
		RunnerEnabled:     true,
		AllowManualRunner: true,
		MaxParallelTasks:  1,
	})
	svc := projectworkflow.New(workflowstore.NewMemoryStore())
	svc.SetCompilerDependencies(workPlans, automations)
	return svc, workPlans, automations
}

func requestJSON[T any](t *testing.T, handler http.Handler, method string, path string, body any, wantStatus int) T {
	t.Helper()
	response := requestRaw(t, handler, method, path, body)
	if response.Code != wantStatus {
		t.Fatalf("expected status %d, got %d body=%s", wantStatus, response.Code, response.Body.String())
	}
	var decoded T
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
	return decoded
}

func requestRaw(t *testing.T, handler http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func validWorkflowTOML() string {
	return `
[[workflows]]
id = "workflow-1"
project_id = "project-1"
workflow_ref = "workflow-rest"
title = "Workflow REST"
purpose = "metadata-only workflow for REST tests"
status = "enabled"

[[workflows.agents]]
id = "worker"
display_name = "Worker"
purpose = "Implement one bounded task"
allowed_skills = ["mivia-mcp"]
allowed_tools = ["projects.workspace.file_read"]
denied_commands = ["git push"]
workspace_mode = "dedicated_worktree"
network_policy = "disabled"
secret_policy = "deny"
log_policy = "metadata_only"
max_runtime = "30m"
max_retries = 1

[[workflows.agents]]
id = "reviewer"
display_name = "Reviewer"
purpose = "Review one bounded task"
allowed_skills = ["mivia-mcp"]
allowed_tools = ["projects.work_tasks.attach_review_result"]
denied_commands = ["go test"]
workspace_mode = "read_only"
network_policy = "disabled"
secret_policy = "deny"
log_policy = "metadata_only"
max_runtime = "20m"
max_retries = 0

[[workflows.steps]]
id = "implement"
kind = "work_task"
agent = "worker"
title = "Implement"
description = "Implement REST workflow routes"
evidence_needed = ["changed_files"]
verification_requirement = "orchestrator runs focused verifier"
expected_output = "REST routes"
failure_criteria = "block if route project scope can be bypassed"
resume_instructions = "resume from task metadata"

[[workflows.review_gates]]
id = "review-implement"
applies_to = ["implement"]
reviewer_agent = "reviewer"
required = true
independent_from_owner = true
required_artifacts = ["changed_files"]
allowed_actions = ["approved", "rejected", "needs_changes", "blocked"]
instructions = "Review changed files and verifier refs."
`
}
