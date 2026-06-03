package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestWorkPlanRoutesLifecycle(t *testing.T) {
	mux := newMux()
	projectID := "example-service"

	plan := postJSON[projectworkplan.WorkPlan](t, mux, "/api/v1/projects/"+projectID+"/work-plans", `{"project_id":"body-project","plan_ref":"plan/rest","title":"REST wiring","goal_summary":"wire metadata-only work plan routes","owner_agent":"worker-3","created_by_run_id":"agent_run_1","trace_id":"trace_1","resume_summary":"continue with route tests","parallel_group_ref":"parallel/rest","workspace_ref":"workspace/rest","git_base_ref":"main","git_branch_ref":"codex/rest-workplan","git_worktree_ref":"worktree/rest-workplan"}`, http.StatusCreated)
	if plan.ProjectID != projectID || plan.PlanRef != "plan/rest" || plan.TraceID != "trace_1" {
		t.Fatalf("expected path project id and metadata fields, got %#v", plan)
	}
	if plan.IsolationMode != "dedicated_worktree" || plan.GitWorktreeRef != "worktree/rest-workplan" {
		t.Fatalf("expected isolation metadata, got %#v", plan)
	}

	listRes := get(t, mux, "/api/v1/projects/"+projectID+"/work-plans?page_size=10")
	if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), plan.ID) {
		t.Fatalf("expected listed plan, got %d: %s", listRes.Code, listRes.Body.String())
	}
	assertNoStore(t, listRes)

	getRes := get(t, mux, "/api/v1/projects/"+projectID+"/work-plans/"+plan.ID)
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), plan.PlanRef) {
		t.Fatalf("expected get plan, got %d: %s", getRes.Code, getRes.Body.String())
	}
	assertDoesNotLeak(t, getRes.Body.String(), "raw_prompt", "raw completion", "provider_payload", "token=", "/home/")

	active := postJSON[projectworkplan.WorkPlan](t, mux, "/api/v1/projects/"+projectID+"/work-plans/"+plan.ID+"/status", `{"status":"active","resume_summary":"ready for task execution"}`, http.StatusOK)
	if active.Status != "active" {
		t.Fatalf("expected active status, got %#v", active)
	}

	resume := get(t, mux, "/api/v1/projects/"+projectID+"/work-plans/"+plan.ID+"/resume?owner_agent=worker-3&run_id=agent_run_1&trace_id=trace_1")
	if resume.Code != http.StatusOK || !strings.Contains(resume.Body.String(), plan.ID) {
		t.Fatalf("expected resume payload, got %d: %s", resume.Code, resume.Body.String())
	}
}

func TestWorkTaskRoutesLifecycleAndLists(t *testing.T) {
	mux := newMux()
	projectID := "example-service"
	plan := createPlan(t, mux, projectID)

	task := createTask(t, mux, projectID, plan.ID, "task/rest-1")
	if task.ProjectID != projectID || task.PlanID != plan.ID || task.Status != "ready" {
		t.Fatalf("expected created ready task, got %#v", task)
	}
	getRes := get(t, mux, "/api/v1/projects/"+projectID+"/work-tasks/"+task.ID)
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), task.TaskRef) {
		t.Fatalf("expected get task, got %d: %s", getRes.Code, getRes.Body.String())
	}

	staleTask := createTask(t, mux, projectID, plan.ID, "task/stale")
	cancelled := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, staleTask.ID, "status"), `{"status":"cancelled","safe_next_action":"stale metadata retained"}`, http.StatusOK)
	if cancelled.Status != "cancelled" {
		t.Fatalf("expected cancelled task, got %#v", cancelled)
	}

	claimed := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "claim"), `{"owner_agent":"worker-3","run_id":"agent_run_1","trace_id":"trace_1"}`, http.StatusOK)
	if claimed.Status != "claimed" || claimed.OwnerAgent != "worker-3" {
		t.Fatalf("expected claimed task, got %#v", claimed)
	}
	started := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "start"), `{"run_id":"agent_run_1","trace_id":"trace_1"}`, http.StatusOK)
	if started.Status != "in_progress" {
		t.Fatalf("expected in_progress task, got %#v", started)
	}

	postJSON[projectworkplan.Attachment](t, mux, workTaskPath(projectID, task.ID, "evidence"), `{"ref":"evidence/ref","attached_by_run_id":"agent_run_1","trace_id":"trace_1","note":"metadata-only evidence ref"}`, http.StatusCreated)
	postJSON[projectworkplan.Attachment](t, mux, workTaskPath(projectID, task.ID, "context-packs"), `{"ref":"context-pack/ref","attached_by_run_id":"agent_run_1","trace_id":"trace_1","note":"bounded context pack ref"}`, http.StatusCreated)
	postJSON[projectworkplan.Attachment](t, mux, workTaskPath(projectID, task.ID, "claims"), `{"ref":"claim/ref","attached_by_run_id":"agent_run_1","trace_id":"trace_1","note":"claim metadata ref"}`, http.StatusCreated)
	postJSON[projectworkplan.Attachment](t, mux, workTaskPath(projectID, task.ID, "verifier-results"), `{"ref":"verifier/ref","attached_by_run_id":"agent_run_1","trace_id":"trace_1","note":"verifier metadata ref"}`, http.StatusCreated)
	postJSON[projectworkplan.Attachment](t, mux, workTaskPath(projectID, task.ID, "knowledge-candidates"), `{"ref":"knowledge/ref","attached_by_run_id":"agent_run_1","trace_id":"trace_1","note":"candidate link only"}`, http.StatusCreated)

	completed := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "complete"), `{"run_id":"agent_run_1","trace_id":"trace_1","outcome":"routes wired","verifier_result_refs":["verifier/ref"]}`, http.StatusOK)
	if completed.Status != "done" {
		t.Fatalf("expected done task, got %#v", completed)
	}

	blockedTask := createTask(t, mux, projectID, plan.ID, "task/rest-2")
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, blockedTask.ID, "claim"), `{"owner_agent":"worker-3","run_id":"agent_run_2"}`, http.StatusOK)
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, blockedTask.ID, "start"), `{"run_id":"agent_run_2"}`, http.StatusOK)
	blocked := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, blockedTask.ID, "block"), `{"run_id":"agent_run_2","blocked_reason":"service package pending","resume_instructions":"retry after model service lands"}`, http.StatusOK)
	if blocked.Status != "blocked" {
		t.Fatalf("expected blocked task, got %#v", blocked)
	}

	failedTask := createTask(t, mux, projectID, plan.ID, "task/rest-3")
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, failedTask.ID, "claim"), `{"owner_agent":"worker-3","run_id":"agent_run_3"}`, http.StatusOK)
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, failedTask.ID, "start"), `{"run_id":"agent_run_3"}`, http.StatusOK)
	failed := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, failedTask.ID, "fail"), `{"run_id":"agent_run_3","outcome":"metadata verifier failed","resume_instructions":"fix route contract"}`, http.StatusOK)
	if failed.Status != "failed" {
		t.Fatalf("expected failed task, got %#v", failed)
	}

	nextTask := createTask(t, mux, projectID, plan.ID, "task/rest-4")
	for _, path := range []string{
		"/api/v1/projects/" + projectID + "/work-tasks?plan_id=" + plan.ID,
		"/api/v1/projects/" + projectID + "/work-tasks/open?plan_id=" + plan.ID,
		"/api/v1/projects/" + projectID + "/work-tasks/mine?owner_agent=worker-3",
		"/api/v1/projects/" + projectID + "/work-tasks/blocked?plan_id=" + plan.ID,
		"/api/v1/projects/" + projectID + "/work-tasks/next?plan_id=" + plan.ID + "&owner_agent=worker-3&run_id=agent_run_4&include_claimed_by_me=true",
	} {
		res := get(t, mux, path)
		if res.Code != http.StatusOK {
			t.Fatalf("expected %s to return 200, got %d: %s", path, res.Code, res.Body.String())
		}
		if strings.Contains(path, "/next?") && !strings.Contains(res.Body.String(), nextTask.ID) {
			t.Fatalf("expected next task response to include %s, got %s", nextTask.ID, res.Body.String())
		}
		assertNoStore(t, res)
		assertDoesNotLeak(t, res.Body.String(), "raw_prompt", "provider_payload", "token=", "/home/")
	}
}

func TestWorkTaskReleaseRoute(t *testing.T) {
	mux := newMux()
	projectID := "example-service"
	plan := createPlan(t, mux, projectID)
	task := createTask(t, mux, projectID, plan.ID, "task/release")
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "claim"), `{"owner_agent":"worker-3","run_id":"agent_run_1"}`, http.StatusOK)

	released := postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "release"), `{"run_id":"agent_run_1","resume_instructions":"available for another worker"}`, http.StatusOK)
	if released.Status != "ready" {
		t.Fatalf("expected ready task after release, got %#v", released)
	}
}

func TestWorkPlanRoutesRejectUnsafeInputs(t *testing.T) {
	mux := newMux()

	unsafeProject := postRaw(t, mux, "/api/v1/projects/%20/work-plans", `{"plan_ref":"plan/ref","title":"REST wiring","goal_summary":"metadata only"}`)
	if unsafeProject.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe project id rejection, got %d: %s", unsafeProject.Code, unsafeProject.Body.String())
	}

	unsafePayload := postRaw(t, mux, "/api/v1/projects/example-service/work-plans", `{"plan_ref":"plan/ref","title":"REST wiring","goal_summary":"raw prompt: secret=token"}`)
	if unsafePayload.Code != http.StatusBadRequest || !strings.Contains(unsafePayload.Body.String(), "invalid_project_workplan_request") {
		t.Fatalf("expected unsafe payload rejection, got %d: %s", unsafePayload.Code, unsafePayload.Body.String())
	}

	invalidJSON := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/work-plans", bytes.NewBufferString(`{"plan_ref":"plan/ref"`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(invalidJSON, req)
	if invalidJSON.Code != http.StatusBadRequest || !strings.Contains(invalidJSON.Body.String(), "invalid_json") {
		t.Fatalf("expected invalid json rejection, got %d: %s", invalidJSON.Code, invalidJSON.Body.String())
	}

	unsupported := httptest.NewRecorder()
	mux.ServeHTTP(unsupported, httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/work-plans", bytes.NewBufferString(`{"plan_ref":"plan/ref"}`)))
	if unsupported.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected unsupported media type, got %d: %s", unsupported.Code, unsupported.Body.String())
	}
}

func TestWorkTaskRoutesRejectInvalidTransition(t *testing.T) {
	mux := newMux()
	projectID := "example-service"
	plan := createPlan(t, mux, projectID)
	task := createTask(t, mux, projectID, plan.ID, "task/invalid-transition")
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "claim"), `{"owner_agent":"worker-3","run_id":"agent_run_1"}`, http.StatusOK)
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "start"), `{"run_id":"agent_run_1"}`, http.StatusOK)
	postJSON[projectworkplan.Attachment](t, mux, workTaskPath(projectID, task.ID, "verifier-results"), `{"ref":"verifier/ref","attached_by_run_id":"agent_run_1"}`, http.StatusCreated)
	postJSON[projectworkplan.WorkTask](t, mux, workTaskPath(projectID, task.ID, "complete"), `{"run_id":"agent_run_1","outcome":"done","verifier_result_refs":["verifier/ref"]}`, http.StatusOK)

	res := postRaw(t, mux, workTaskPath(projectID, task.ID, "start"), `{"run_id":"agent_run_1"}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_project_workplan_request") {
		t.Fatalf("expected invalid transition rejection, got %d: %s", res.Code, res.Body.String())
	}
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, projectworkplan.New(store.NewMemoryStore()))
	return mux
}

func createPlan(t *testing.T, mux *http.ServeMux, projectID string) projectworkplan.WorkPlan {
	t.Helper()
	return postJSON[projectworkplan.WorkPlan](t, mux, "/api/v1/projects/"+projectID+"/work-plans", `{"plan_ref":"plan/test","title":"REST wiring","goal_summary":"wire metadata-only routes","owner_agent":"worker-3"}`, http.StatusCreated)
}

func createTask(t *testing.T, mux *http.ServeMux, projectID string, planID string, ref string) projectworkplan.WorkTask {
	t.Helper()
	return postJSON[projectworkplan.WorkTask](t, mux, "/api/v1/projects/"+projectID+"/work-plans/"+planID+"/tasks", `{"task_ref":"`+ref+`","title":"Wire one route","description":"metadata-only REST task","owner_agent":"worker-3","evidence_needed":["route test"],"context_pack_refs":["context-pack/ref"],"likely_files_affected":["internal/projectworkplan/httpapi/httpapi.go"],"verification_requirement":"focused REST test","expected_output":"route returns metadata JSON","failure_criteria":"block on unsafe payload or invalid transition","resume_instructions":"continue with the next handler"}`, http.StatusCreated)
}

func workTaskPath(projectID string, taskID string, suffix string) string {
	return "/api/v1/projects/" + projectID + "/work-tasks/" + taskID + "/" + suffix
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
	return res
}

func postRaw(t *testing.T, mux *http.ServeMux, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func postJSON[T any](t *testing.T, mux *http.ServeMux, path string, body string, status int) T {
	t.Helper()
	res := postRaw(t, mux, path, body)
	if res.Code != status {
		t.Fatalf("expected %d for %s, got %d: %s", status, path, res.Code, res.Body.String())
	}
	assertNoStore(t, res)
	assertDoesNotLeak(t, res.Body.String(), "raw_prompt", "raw_completion", "provider_payload", "Authorization:", "bearer ", "token=", "secret=", "/home/", "root_path")
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func assertNoStore(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	if cacheControl := res.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", cacheControl)
	}
}

func assertDoesNotLeak(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(body, value) {
			t.Fatalf("response leaked %q: %s", value, body)
		}
	}
}
