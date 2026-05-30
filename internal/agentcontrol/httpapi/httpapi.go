package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/service"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
)

func RegisterRoutes(mux *http.ServeMux, svc *service.Service) {
	mux.Handle("POST /api/v1/tasks", createTaskHandler(svc))
	mux.Handle("GET /api/v1/tasks/{id}", getTaskHandler(svc))
	mux.Handle("POST /api/v1/research-runs", createResearchRunHandler(svc))
	mux.Handle("GET /api/v1/research-runs/{id}", getResearchRunHandler(svc))
}

func createTaskHandler(svc *service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input model.CreateTaskInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		task, err := svc.CreateTask(r.Context(), input)
		writeResult(w, task, err, http.StatusCreated)
	})
}

func getTaskHandler(svc *service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task, err := svc.GetTask(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeResult(w, task, err, http.StatusOK)
	})
}

func createResearchRunHandler(svc *service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input model.CreateResearchRunInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		run, err := svc.CreateResearchRun(r.Context(), input)
		writeResult(w, run, err, http.StatusCreated)
	})
}

func getResearchRunHandler(svc *service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := svc.GetResearchRun(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeResult(w, run, err, http.StatusOK)
	})
}

func writeResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if errors.Is(err, service.ErrInvalidInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
