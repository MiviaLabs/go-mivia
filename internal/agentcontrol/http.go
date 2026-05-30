package agentcontrol

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
)

func RegisterRESTRoutes(mux *http.ServeMux, service *Service) {
	mux.Handle("POST /api/v1/tasks", createTaskHandler(service))
	mux.Handle("GET /api/v1/tasks/{id}", getTaskHandler(service))
}

func createTaskHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}

		var req CreateTaskRequest
		if err := httpserver.DecodeJSON(r, &req); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}

		task, err := service.CreateTask(r.Context(), req)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_task", err.Error())
			return
		}

		httpserver.WriteJSON(w, http.StatusCreated, task)
	})
}

func getTaskHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		task, err := service.GetTask(r.Context(), id)
		if err == nil {
			httpserver.WriteJSON(w, http.StatusOK, task)
			return
		}
		if errors.Is(err, ErrTaskNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "task_not_found", "task not found")
			return
		}
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_task", err.Error())
	})
}
