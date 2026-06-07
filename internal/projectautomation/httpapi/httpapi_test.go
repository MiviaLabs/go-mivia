package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestAutomationRoutesCreateRunAndList(t *testing.T) {
	workTasks := &fakeWorkTasks{task: projectworkplan.WorkTask{
		ID:                      "task-a",
		ProjectID:               "project-1",
		PlanID:                  "plan-1",
		TaskRef:                 "task/a",
		Title:                   "Task A",
		Status:                  projectworkplan.WorkTaskStatusReady,
		VerificationRequirement: "orchestrator runs focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}}
	svc := projectautomation.New(automationstore.NewMemoryStore(), workTasks, projectautomation.Options{
		Enabled:                   true,
		RunnerEnabled:             true,
		RequireCodexWhenAvailable: false,
		AllowManualRunner:         true,
		MaxParallelTasks:          1,
	})
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)

	created := requestJSON[projectautomation.Automation](t, mux, http.MethodPost, "/api/v1/projects/project-1/automations", map[string]any{
		"automation_ref": "automation/ref",
		"title":          "Automation",
		"purpose":        "Run constrained work tasks",
		"agent_id":       "agent-1",
		"plan_id":        "plan-1",
		"permission_ref": "permission/default",
	}, http.StatusCreated)
	if created.ProjectID != "project-1" || created.ID == "" {
		t.Fatalf("unexpected automation: %+v", created)
	}

	disabled := requestJSON[projectautomation.Automation](t, mux, http.MethodPost, "/api/v1/projects/project-1/automations/"+created.ID+"/status", map[string]any{
		"status": projectautomation.AutomationStatusDisabled,
		"run_id": "run-disable",
	}, http.StatusOK)
	if disabled.Status != projectautomation.AutomationStatusDisabled {
		t.Fatalf("expected disabled automation, got %+v", disabled)
	}

	reenabled := requestJSON[projectautomation.Automation](t, mux, http.MethodPost, "/api/v1/projects/project-1/automations/"+created.ID+"/status", map[string]any{
		"status": projectautomation.AutomationStatusEnabled,
	}, http.StatusOK)
	if reenabled.Status != projectautomation.AutomationStatusEnabled {
		t.Fatalf("expected enabled automation, got %+v", reenabled)
	}

	run := requestJSON[projectautomation.AutomationRun](t, mux, http.MethodPost, "/api/v1/projects/project-1/automations/"+created.ID+"/runs", map[string]any{
		"plan_id":          "plan-1",
		"task_id":          "task-a",
		"owner_agent":      "agent-1",
		"runner_kind":      projectautomation.RunnerKindManual,
		"safe_next_action": "Claim the next ready task.",
	}, http.StatusCreated)
	if run.AutomationID != created.ID || run.Status != projectautomation.RunStatusVerifying {
		t.Fatalf("unexpected run: %+v", run)
	}

	list := requestJSON[runListResponse](t, mux, http.MethodGet, "/api/v1/projects/project-1/automation-runs", nil, http.StatusOK)
	if len(list.AutomationRuns) != 1 || list.AutomationRuns[0].ID != run.ID {
		t.Fatalf("unexpected run list: %+v", list)
	}
}

func TestAutomationExternalClaimAndCompleteRoutes(t *testing.T) {
	workTasks := &fakeWorkTasks{task: projectworkplan.WorkTask{
		ID:                      "task-a",
		ProjectID:               "project-1",
		PlanID:                  "plan-1",
		TaskRef:                 "task/a",
		Title:                   "Task A",
		Status:                  projectworkplan.WorkTaskStatusReady,
		VerificationRequirement: "orchestrator runs focused verifier",
		DecompositionQuality:    projectworkplan.DecompositionReady,
	}}
	svc := projectautomation.New(automationstore.NewMemoryStore(), workTasks, projectautomation.Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  projectautomation.RunnerExecutionExternal,
		MaxParallelTasks: 1,
	})
	mux := http.NewServeMux()
	RegisterRoutes(mux, svc)

	created := requestJSON[projectautomation.Automation](t, mux, http.MethodPost, "/api/v1/projects/project-1/automations", map[string]any{
		"automation_ref": "automation/ref",
		"title":          "Automation",
		"purpose":        "Run constrained work tasks",
		"agent_id":       "agent-1",
		"plan_id":        "plan-1",
		"permission_ref": "permission/default",
	}, http.StatusCreated)
	queued := requestJSON[projectautomation.AutomationRun](t, mux, http.MethodPost, "/api/v1/projects/project-1/automations/"+created.ID+"/runs", map[string]any{
		"task_id": "task-a",
	}, http.StatusCreated)
	if queued.Status != projectautomation.RunStatusQueued {
		t.Fatalf("expected queued external run, got %+v", queued)
	}

	claimed := requestJSON[projectautomation.ClaimedRun](t, mux, http.MethodPost, "/api/v1/projects/project-1/automation-runs/claim-next", map[string]any{
		"runner_kind": projectautomation.RunnerKindCodexCLI,
	}, http.StatusOK)
	if claimed.Run.ID != queued.ID || claimed.CodexInput.TaskID != "task-a" {
		t.Fatalf("unexpected claim: %+v", claimed)
	}

	done := requestJSON[projectautomation.AutomationRun](t, mux, http.MethodPost, "/api/v1/projects/project-1/automation-runs/"+queued.ID+"/attempt-result", map[string]any{
		"status":      projectautomation.RunStatusCompleted,
		"duration_ms": 100,
	}, http.StatusOK)
	if done.Status != projectautomation.RunStatusVerifying {
		t.Fatalf("expected verifier-required status, got %+v", done)
	}
}

func requestJSON[T any](t *testing.T, handler http.Handler, method string, path string, body any, wantStatus int) T {
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
	if recorder.Code != wantStatus {
		t.Fatalf("expected status %d, got %d body=%s", wantStatus, recorder.Code, recorder.Body.String())
	}
	var decoded T
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v body=%s", err, recorder.Body.String())
	}
	return decoded
}

type fakeWorkTasks struct {
	task projectworkplan.WorkTask
}

func (fake *fakeWorkTasks) GetWorkPlan(_ context.Context, projectID string, planID string) (projectworkplan.WorkPlan, error) {
	return projectworkplan.WorkPlan{ID: planID, ProjectID: projectID, Status: projectworkplan.WorkPlanStatusActive}, nil
}

func (fake *fakeWorkTasks) GetWorkTask(_ context.Context, _ string, taskID string) (projectworkplan.WorkTask, error) {
	if fake.task.ID != taskID {
		return projectworkplan.WorkTask{}, errors.New("not found")
	}
	return fake.task, nil
}

func (fake *fakeWorkTasks) ListOpenWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	return []projectworkplan.WorkTask{fake.task}, nil
}

func (fake *fakeWorkTasks) ClaimWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	claimed := fake.task
	claimed.Status = projectworkplan.WorkTaskStatusClaimed
	return claimed, nil
}

func (fake *fakeWorkTasks) StartWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	started := fake.task
	started.Status = projectworkplan.WorkTaskStatusInProgress
	return started, nil
}

func (fake *fakeWorkTasks) AttachEvidence(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachVerifierResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) AttachReviewResult(context.Context, projectworkplan.AttachInput) (projectworkplan.Attachment, error) {
	return projectworkplan.Attachment{}, nil
}

func (fake *fakeWorkTasks) CompleteWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) FailWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}

func (fake *fakeWorkTasks) BlockWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	return projectworkplan.WorkTask{}, nil
}
