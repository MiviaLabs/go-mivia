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
		Description:             "Implement generic HTTP boundary behavior.",
		Status:                  projectworkplan.WorkTaskStatusReady,
		EvidenceNeeded:          []string{"evidence:http-source"},
		ContextPackRefs:         []string{"context:http-generic"},
		LikelyFilesAffected:     []string{"internal/generic/http.go"},
		VerificationRequirement: "orchestrator runs focused verifier",
		ExpectedOutput:          "bounded implementation and verifier evidence",
		FailureCriteria:         "block on missing HTTP boundary evidence",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"HTTP claim response includes usable Codex data"},
		StopConditions:          []string{"missing claim or runner refs"},
		VerifierLadder:          []string{"focused HTTP automation regression"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"downstream.http-boundary"},
		OutputContract:          "REST claim and completion preserve live runner handoff metadata",
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
		Description:             "Implement generic HTTP boundary behavior.",
		Status:                  projectworkplan.WorkTaskStatusReady,
		EvidenceNeeded:          []string{"evidence:http-source"},
		ContextPackRefs:         []string{"context:http-generic"},
		LikelyFilesAffected:     []string{"internal/generic/http.go"},
		VerificationRequirement: "orchestrator runs focused verifier",
		ExpectedOutput:          "bounded implementation and verifier evidence",
		FailureCriteria:         "block on missing HTTP boundary evidence",
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"HTTP claim response includes usable Codex data"},
		StopConditions:          []string{"missing claim or runner refs"},
		VerifierLadder:          []string{"focused HTTP automation regression"},
		RegressionApplicability: "required",
		DownstreamImpactRefs:    []string{"downstream.http-boundary"},
		OutputContract:          "REST claim and completion preserve live runner handoff metadata",
	}}
	automationStore := automationstore.NewMemoryStore()
	svc := projectautomation.New(automationStore, workTasks, projectautomation.Options{
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
		"runner_id":   "runner-http-1",
	}, http.StatusOK)
	if claimed.Run.ID != queued.ID || claimed.CodexInput.TaskID != "task-a" {
		t.Fatalf("unexpected claim: %+v", claimed)
	}
	if claimed.Run.Status != projectautomation.RunStatusRunning || claimed.Run.ClaimID == "" || claimed.Run.RunnerID != "runner-http-1" || claimed.Run.ClaimedAt.IsZero() || claimed.Run.LastHeartbeatAt.IsZero() || claimed.Run.LeaseExpiresAt.IsZero() {
		t.Fatalf("claim response lost external runner lifecycle fields: %+v", claimed.Run)
	}
	if claimed.Run.WorkTaskStatus != projectworkplan.WorkTaskStatusInProgress || claimed.Run.SafeSummary != "external_runner_queued" {
		t.Fatalf("claim response lost work task status/action summary: %+v", claimed.Run)
	}
	if claimed.CodexInput.ProjectID != "project-1" || claimed.CodexInput.AutomationRunID != queued.ID || claimed.CodexInput.PlanID != "plan-1" || claimed.CodexInput.TaskID != "task-a" || claimed.CodexInput.TaskRef != "task/a" {
		t.Fatalf("claim response lost Codex run/task refs: %+v", claimed.CodexInput)
	}
	for _, want := range []string{
		"Implement generic HTTP boundary behavior.",
		"evidence:http-source",
		"context:http-generic",
		"internal/generic/http.go",
		"orchestrator runs focused verifier",
		"bounded implementation and verifier evidence",
		"block on missing HTTP boundary evidence",
		"HTTP claim response includes usable Codex data",
		"missing claim or runner refs",
		"focused HTTP automation regression",
		"required",
		"downstream.http-boundary",
		"REST claim and completion preserve live runner handoff metadata",
	} {
		if !codexInputContains(claimed.CodexInput, want) {
			t.Fatalf("claim response Codex input lost %q: %+v", want, claimed.CodexInput)
		}
	}

	heartbeat := requestJSON[projectautomation.AutomationRun](t, mux, http.MethodPost, "/api/v1/projects/project-1/automation-runs/"+queued.ID+"/heartbeat", map[string]any{
		"claim_id":  claimed.Run.ClaimID,
		"runner_id": "runner-http-1",
	}, http.StatusOK)
	if heartbeat.Status != projectautomation.RunStatusRunning || heartbeat.ClaimID != claimed.Run.ClaimID || heartbeat.RunnerID != "runner-http-1" || heartbeat.LastHeartbeatAt.IsZero() || heartbeat.LeaseExpiresAt.IsZero() {
		t.Fatalf("heartbeat response lost claim/lease handoff fields: %+v", heartbeat)
	}

	done := requestJSON[projectautomation.AutomationRun](t, mux, http.MethodPost, "/api/v1/projects/project-1/automation-runs/"+queued.ID+"/attempt-result", map[string]any{
		"claim_id":             claimed.Run.ClaimID,
		"runner_id":            "runner-http-1",
		"status":               projectautomation.RunStatusCompleted,
		"duration_ms":          100,
		"evidence_refs":        []string{"evidence:http-worker"},
		"claim_refs":           []string{"claim:http-worker"},
		"verifier_result_refs": []string{"verifier:http-worker"},
		"review_result_refs":   []string{"review:http-worker"},
	}, http.StatusOK)
	if done.Status != projectautomation.RunStatusVerifying || done.ClaimID != claimed.Run.ClaimID || done.RunnerID != "runner-http-1" || done.FinishedAt.IsZero() {
		t.Fatalf("completion response lost verifying claim/runner handoff fields: %+v", done)
	}
	if done.SafeSummary != "external_codex_cli_completed_verification_required" {
		t.Fatalf("completion response lost safe next action summary: %+v", done)
	}
	gotRun := requestJSON[projectautomation.AutomationRun](t, mux, http.MethodGet, "/api/v1/projects/project-1/automation-runs/"+queued.ID, nil, http.StatusOK)
	if gotRun.Status != projectautomation.RunStatusVerifying || gotRun.ClaimID != claimed.Run.ClaimID || gotRun.RunnerID != "runner-http-1" || gotRun.SafeSummary != done.SafeSummary {
		t.Fatalf("get run response lost completed external handoff fields: %+v", gotRun)
	}
	list := requestJSON[runListResponse](t, mux, http.MethodGet, "/api/v1/projects/project-1/automation-runs?status="+projectautomation.RunStatusVerifying, nil, http.StatusOK)
	if len(list.AutomationRuns) != 1 || list.AutomationRuns[0].ID != queued.ID || list.AutomationRuns[0].ClaimID != claimed.Run.ClaimID || list.AutomationRuns[0].RunnerID != "runner-http-1" {
		t.Fatalf("list run response lost completed external handoff fields: %+v", list)
	}
}

func codexInputContains(input projectautomation.CodexTaskInput, want string) bool {
	data, err := json.Marshal(input)
	return err == nil && bytes.Contains(data, []byte(want))
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
	fake.task = claimed
	return claimed, nil
}

func (fake *fakeWorkTasks) StartWorkTask(context.Context, projectworkplan.WorkTaskActionInput) (projectworkplan.WorkTask, error) {
	started := fake.task
	started.Status = projectworkplan.WorkTaskStatusInProgress
	fake.task = started
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
