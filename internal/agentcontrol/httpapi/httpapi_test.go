package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/service"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
)

func TestTaskRoutes_CreateAndGet(t *testing.T) {
	mux := newMux()
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"title":"REST task"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()

	mux.ServeHTTP(createRes, createReq)

	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}
	var task model.Task
	if err := json.Unmarshal(createRes.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID, nil)
	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, getReq)

	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}
}

func TestTaskRoutes_RejectRawQueryExposure(t *testing.T) {
	mux := newMux()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"title":"Task","query":"MATCH (n)"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected raw query field rejection, got %d", res.Code)
	}
}

func TestResearchRunRoutes_CreateAndGet(t *testing.T) {
	mux := newMux()
	task := createTask(t, mux)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/research-runs", bytes.NewBufferString(`{"task_id":"`+task.ID+`","goal_summary":"Fixture-only metadata summary"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()

	mux.ServeHTTP(createRes, createReq)

	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}
	var run model.ResearchRun
	if err := json.Unmarshal(createRes.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode research run: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/research-runs/"+run.ID, nil)
	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, getReq)

	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}
}

func TestAgentRunRoutes_CreateAppendCompleteAndGet(t *testing.T) {
	mux := newMux()
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runs", bytes.NewBufferString(`{"project_id":"example-service","summary":"bounded run metadata","changed_files":["internal/agentcontrol/model/model.go"]}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()
	mux.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}
	var run model.AgentRun
	if err := json.Unmarshal(createRes.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}

	stepReq := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runs/"+run.ID+"/steps", bytes.NewBufferString(`{"tool_name":"go","tool_category":"test","status":"completed","notes":"focused verifier passed"}`))
	stepReq.Header.Set("Content-Type", "application/json")
	stepRes := httptest.NewRecorder()
	mux.ServeHTTP(stepRes, stepReq)
	if stepRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", stepRes.Code, stepRes.Body.String())
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runs/"+run.ID+"/complete", bytes.NewBufferString(`{"status":"completed"}`))
	completeReq.Header.Set("Content-Type", "application/json")
	completeRes := httptest.NewRecorder()
	mux.ServeHTTP(completeRes, completeReq)
	if completeRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", completeRes.Code, completeRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runs/"+run.ID, nil)
	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK || bytes.Contains(getRes.Body.Bytes(), []byte("raw prompt")) || bytes.Contains(getRes.Body.Bytes(), []byte("package main")) {
		t.Fatalf("unexpected get response: %d %s", getRes.Code, getRes.Body.String())
	}
}

func TestAgentRunRoutes_RejectUnsafePayload(t *testing.T) {
	mux := newMux()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runs", bytes.NewBufferString(`{"project_id":"example-service","summary":"raw prompt: use token=secret"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe payload rejection, got %d: %s", res.Code, res.Body.String())
	}
}

func newMux() *http.ServeMux {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, svc)
	return mux
}

func createTask(t *testing.T, mux *http.ServeMux) model.Task {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"title":"Parent task"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("create task failed: %d %s", res.Code, res.Body.String())
	}
	var task model.Task
	if err := json.Unmarshal(res.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	return task
}
