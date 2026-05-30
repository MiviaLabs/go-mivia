package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func RegisterRoutes(mux *http.ServeMux, registry *projectregistry.Registry, digest *projectregistry.DigestService) {
	mux.Handle("GET /api/v1/projects", listProjectsHandler(registry))
	mux.Handle("GET /api/v1/projects/{id}", getProjectHandler(registry))
	mux.Handle("POST /api/v1/projects/{id}/digest-runs", createDigestRunHandler(digest))
}

func listProjectsHandler(registry *projectregistry.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]any{
			"projects": projectregistry.MetadataForProjects(registry.List()),
		})
	})
}

func getProjectHandler(registry *projectregistry.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project, ok := registry.Get(strings.TrimSpace(r.PathValue("id")))
		if !ok {
			writeResult(w, nil, projectregistry.ErrProjectNotFound, http.StatusOK)
			return
		}
		writeResult(w, projectregistry.MetadataForProject(project), nil, http.StatusOK)
	})
}

func createDigestRunHandler(digest *projectregistry.DigestService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := digest.DigestProject(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeResult(w, projectregistry.MetadataForDigestRun(run), err, http.StatusCreated)
	})
}

func writeResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, projectregistry.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if errors.Is(err, projectregistry.ErrInvalidInput) ||
		errors.Is(err, projectregistry.ErrDigestProjectDisabled) ||
		errors.Is(err, projectregistry.ErrDigestUnsupported) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_request", "project request is invalid")
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}
