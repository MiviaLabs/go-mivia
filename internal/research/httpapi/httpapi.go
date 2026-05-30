package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/store"
)

func RegisterRoutes(mux *http.ServeMux, svc *research.Service) {
	mux.Handle("POST /api/v1/research-runs/{id}/sources", createSourceHandler(svc))
	mux.Handle("GET /api/v1/research-runs/{id}/sources/{source_id}", getSourceHandler(svc))
}

func createSourceHandler(svc *research.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input research.CreateSourceInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ResearchRunID = strings.TrimSpace(r.PathValue("id"))
		source, err := svc.CreateSource(r.Context(), input)
		writeResult(w, source, err, http.StatusCreated)
	})
}

func getSourceHandler(svc *research.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source, err := svc.GetSource(r.Context(), strings.TrimSpace(r.PathValue("source_id")))
		if err == nil && source.ResearchRunID != strings.TrimSpace(r.PathValue("id")) {
			err = store.ErrNotFound
		}
		writeResult(w, source, err, http.StatusOK)
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
	if errors.Is(err, research.ErrInvalidInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
