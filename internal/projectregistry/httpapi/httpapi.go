package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func RegisterRoutes(mux *http.ServeMux, registry *projectregistry.Registry, digest *projectregistry.DigestService) {
	RegisterRoutesWithIngestion(mux, registry, digest, nil)
}

func RegisterRoutesWithIngestion(mux *http.ServeMux, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion *projectingestion.Service) {
	mux.Handle("GET /api/v1/projects", listProjectsHandler(registry))
	mux.Handle("GET /api/v1/projects/{id}", getProjectHandler(registry))
	mux.Handle("POST /api/v1/projects/{id}/digest-runs", createDigestRunHandler(digest))
	if ingestion != nil {
		mux.Handle("POST /api/v1/projects/{id}/ingestion-runs", createIngestionRunHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/ingestion-runs/{run_id}", getIngestionRunHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/files", listFilesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/files/{file_id}/chunks", listChunksHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols", listSymbolsHandler(ingestion))
	}
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

func createIngestionRunHandler(ingestion *projectingestion.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := ingestion.IngestProject(r.Context(), strings.TrimSpace(r.PathValue("id")), projectingestion.TriggerManual)
		writeIngestionResult(w, projectingestion.MetadataForRun(run), err, http.StatusCreated)
	})
}

func getIngestionRunHandler(ingestion *projectingestion.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := ingestion.RunMetadata(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("run_id")))
		writeIngestionResult(w, run, err, http.StatusOK)
	})
}

func listFilesHandler(ingestion *projectingestion.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter, err := fileFilter(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		files, err := ingestion.ListFiles(r.Context(), strings.TrimSpace(r.PathValue("id")), filter, pagination)
		writeIngestionResult(w, files, err, http.StatusOK)
	})
}

func listChunksHandler(ingestion *projectingestion.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxChunkBytes, err := positiveIntQuery(r, "max_chunk_bytes")
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		chunks, err := ingestion.ListChunks(
			r.Context(),
			strings.TrimSpace(r.PathValue("id")),
			strings.TrimSpace(r.PathValue("file_id")),
			pagination,
			maxChunkBytes,
		)
		writeIngestionResult(w, chunks, err, http.StatusOK)
	})
}

func listSymbolsHandler(ingestion *projectingestion.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		symbols, err := ingestion.ListSymbols(r.Context(), strings.TrimSpace(r.PathValue("id")), pagination)
		writeIngestionResult(w, symbols, err, http.StatusOK)
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

func writeIngestionResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, projectingestion.ErrProjectNotFound) ||
		errors.Is(err, projectingestion.ErrIngestionNotFound) ||
		errors.Is(err, projectingestion.ErrRunNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project ingestion resource not found")
		return
	}
	if errors.Is(err, projectingestion.ErrInvalidInput) ||
		errors.Is(err, projectingestion.ErrProjectDisabled) ||
		errors.Is(err, projectingestion.ErrUnsupportedIngest) ||
		errors.Is(err, projectingestion.ErrPathEscapesRoot) ||
		errors.Is(err, projectingestion.ErrPathNotProjectLocal) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_ingestion_request", "project ingestion request is invalid")
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func paginationFromRequest(r *http.Request) (projectingestion.Pagination, error) {
	pageSize, err := positiveIntQuery(r, "page_size")
	if err != nil {
		return projectingestion.Pagination{}, err
	}
	return projectingestion.Pagination{
		PageSize:  pageSize,
		PageToken: r.URL.Query().Get("page_token"),
	}, nil
}

func fileFilter(r *http.Request) (projectingestion.FileStateFilter, error) {
	filter := projectingestion.FileStateFilter{}
	extension, err := projectingestion.NormalizeFileExtension(r.URL.Query().Get("extension"))
	if err != nil {
		return projectingestion.FileStateFilter{}, err
	}
	filter.Extension = extension
	rawStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	if rawStatus == "" {
		return filter, nil
	}
	switch projectingestion.FileStatus(rawStatus) {
	case projectingestion.FileStatusEligible, projectingestion.FileStatusSkipped, projectingestion.FileStatusAbsent:
		filter.Status = projectingestion.FileStatus(rawStatus)
		return filter, nil
	default:
		return projectingestion.FileStateFilter{}, projectregistry.ErrInvalidInput
	}
}

func positiveIntQuery(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, projectregistry.ErrInvalidInput
	}
	return value, nil
}
