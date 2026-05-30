package agentcontrol

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRESTTasks_CreateAndGet(t *testing.T) {
	service := NewService(NewMemoryStore())
	mux := http.NewServeMux()
	RegisterRESTRoutes(mux, service)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"title":"Bootstrap task"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRes := httptest.NewRecorder()

	mux.ServeHTTP(createRes, createReq)

	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}
}

func TestRESTTasks_RejectsRawQueryField(t *testing.T) {
	service := NewService(NewMemoryStore())
	mux := http.NewServeMux()
	RegisterRESTRoutes(mux, service)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"title":"Task","query":"MATCH (n)"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown raw query field to be rejected, got %d", res.Code)
	}
}

func TestRESTTasks_RejectsTrailingJSON(t *testing.T) {
	service := NewService(NewMemoryStore())
	mux := http.NewServeMux()
	RegisterRESTRoutes(mux, service)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(`{"title":"Task"}{"title":"Second"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	mux.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected trailing JSON to be rejected, got %d", res.Code)
	}
}
