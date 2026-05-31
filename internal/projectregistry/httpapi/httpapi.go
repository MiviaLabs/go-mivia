package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

func RegisterRoutes(mux *http.ServeMux, registry *projectregistry.Registry, digest *projectregistry.DigestService) {
	RegisterRoutesWithIngestion(mux, registry, digest, nil)
}

func RegisterRoutesWithIngestion(mux *http.ServeMux, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion projectingestion.API) {
	RegisterRoutesWithWorkspace(mux, registry, digest, ingestion, nil)
}

func RegisterRoutesWithWorkspace(mux *http.ServeMux, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion projectingestion.API, workspace projectworkspace.API) {
	mux.Handle("GET /api/v1/projects", listProjectsHandler(registry))
	mux.Handle("GET /api/v1/projects/{id}", getProjectHandler(registry))
	mux.Handle("POST /api/v1/projects/{id}/digest-runs", createDigestRunHandler(digest))
	if ingestion != nil {
		mux.Handle("POST /api/v1/projects/{id}/ingestion-runs", createIngestionRunHandler(ingestion))
		mux.Handle("POST /api/v1/projects/{id}/search-index/rebuild", rebuildSearchIndexHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/context-health", getContextHealthHandler(registry, ingestion, workspace))
		mux.Handle("POST /api/v1/projects/{id}/impact/analyze", analyzeImpactHandler(workspace))
		mux.Handle("POST /api/v1/projects/{id}/claims/check", checkClaimsHandler(workspace))
		mux.Handle("GET /api/v1/projects/{id}/ingestion-runs/latest", getLatestIngestionRunHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/ingestion-runs/{run_id}", getIngestionRunHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/files", listFilesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/files/{file_id}", getFileHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/files/{file_id}/chunks", listChunksHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/files/{file_id}/outline", getFileOutlineHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols", listSymbolsHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/text", searchTextHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/files", searchFilesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/symbols", searchSymbolsHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/references", searchReferencesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/calls", searchCallsHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/ast/queries", listASTQueriesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/search/ast", searchASTHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols/{symbol_id}/source", getSymbolSourceHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols/{symbol_id}/references", listSymbolReferencesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols/{symbol_id}/callers", listSymbolCallersHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols/{symbol_id}/callees", listSymbolCalleesHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/symbols/{symbol_id}/call-graph", getSymbolCallGraphHandler(ingestion))
		mux.Handle("GET /api/v1/projects/{id}/headings", listHeadingsHandler(ingestion))
	}
	if workspace != nil {
		mux.Handle("GET /api/v1/projects/{id}/workspace/git/status", workspaceGitStatusHandler(workspace))
		mux.Handle("GET /api/v1/projects/{id}/workspace/git/diff", workspaceGitDiffHandler(workspace))
		mux.Handle("GET /api/v1/projects/{id}/workspace/files/read", workspaceFileReadHandler(workspace))
		mux.Handle("POST /api/v1/projects/{id}/workspace/files/edit", workspaceFileEditHandler(workspace))
	}
}

func getContextHealthHandler(registry *projectregistry.Registry, ingestion projectingestion.API, workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		health, err := projectreliability.NewServiceFromAPIs(registry, ingestion, workspace, projectreliability.Options{}).ContextHealth(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeReliabilityResult(w, health, err, http.StatusOK)
	})
}

func analyzeImpactHandler(workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectreliability.ImpactAnalysisRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = strings.TrimSpace(r.PathValue("id"))
		impact, err := projectreliability.NewImpactAnalyzer(workspace).Analyze(r.Context(), input)
		writeReliabilityResult(w, impact, err, http.StatusOK)
	})
}

func checkClaimsHandler(workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectreliability.ClaimCheckRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = strings.TrimSpace(r.PathValue("id"))
		claims, err := projectreliability.NewClaimChecker(workspace).Check(r.Context(), input)
		writeReliabilityResult(w, claims, err, http.StatusOK)
	})
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

func createIngestionRunHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := ingestion.SubmitIngestProject(r.Context(), strings.TrimSpace(r.PathValue("id")), projectingestion.TriggerManual)
		writeIngestionResult(w, projectingestion.MetadataForRun(run), err, http.StatusCreated)
	})
}

func rebuildSearchIndexHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := ingestion.SubmitRebuildSearchIndex(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeIngestionResult(w, projectingestion.MetadataForRun(run), err, http.StatusAccepted)
	})
}

func getLatestIngestionRunHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := ingestion.LatestRunMetadata(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeIngestionResult(w, run, err, http.StatusOK)
	})
}

func getIngestionRunHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := ingestion.RunMetadata(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("run_id")))
		writeIngestionResult(w, run, err, http.StatusOK)
	})
}

func listFilesHandler(ingestion projectingestion.API) http.Handler {
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

func listChunksHandler(ingestion projectingestion.API) http.Handler {
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

func getFileHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := ingestion.GetFile(
			r.Context(),
			strings.TrimSpace(r.PathValue("id")),
			strings.TrimSpace(r.PathValue("file_id")),
		)
		writeIngestionResult(w, file, err, http.StatusOK)
	})
}

func listSymbolsHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		filter, err := symbolFilter(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		symbols, err := ingestion.ListSymbols(r.Context(), strings.TrimSpace(r.PathValue("id")), filter, pagination)
		writeIngestionResult(w, symbols, err, http.StatusOK)
	})
}

func searchTextHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := textSearchOptions(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		results, err := ingestion.SearchText(r.Context(), strings.TrimSpace(r.PathValue("id")), options)
		writeIngestionResult(w, results, err, http.StatusOK)
	})
}

func searchFilesHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := fileSearchOptions(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		files, err := ingestion.SearchFiles(r.Context(), strings.TrimSpace(r.PathValue("id")), options)
		writeIngestionResult(w, files, err, http.StatusOK)
	})
}

func searchSymbolsHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		filter, err := symbolFilter(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		symbols, err := ingestion.SearchSymbols(r.Context(), strings.TrimSpace(r.PathValue("id")), filter, pagination)
		writeIngestionResult(w, symbols, err, http.StatusOK)
	})
}

func searchReferencesHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := referenceSearchOptions(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		refs, err := ingestion.SearchReferences(r.Context(), strings.TrimSpace(r.PathValue("id")), options)
		writeIngestionResult(w, refs, err, http.StatusOK)
	})
}

func searchCallsHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := referenceSearchOptions(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		calls, err := ingestion.SearchCalls(r.Context(), strings.TrimSpace(r.PathValue("id")), options)
		writeIngestionResult(w, calls, err, http.StatusOK)
	})
}

func searchASTHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := astSearchOptions(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		results, err := ingestion.SearchAST(r.Context(), strings.TrimSpace(r.PathValue("id")), options)
		writeIngestionResult(w, results, err, http.StatusOK)
	})
}

func listASTQueriesHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catalog, err := ingestion.ListASTQueries(r.Context(), strings.TrimSpace(r.PathValue("id")))
		writeIngestionResult(w, catalog, err, http.StatusOK)
	})
}

func listHeadingsHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		headings, err := ingestion.ListHeadings(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.URL.Query().Get("file_id")), pagination)
		writeIngestionResult(w, headings, err, http.StatusOK)
	})
}

func getSymbolSourceHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxSourceBytes, err := positiveIntQuery(r, "max_source_bytes")
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		source, err := ingestion.GetSymbolSource(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("symbol_id")), projectingestion.SymbolSourceOptions{MaxSourceBytes: maxSourceBytes})
		writeIngestionResult(w, source, err, http.StatusOK)
	})
}

func listSymbolReferencesHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		refs, err := ingestion.ListSymbolReferences(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("symbol_id")), pagination)
		writeIngestionResult(w, refs, err, http.StatusOK)
	})
}

func listSymbolCallersHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		edges, err := ingestion.ListSymbolCallers(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("symbol_id")), pagination)
		writeIngestionResult(w, edges, err, http.StatusOK)
	})
}

func listSymbolCalleesHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pagination, err := paginationFromRequest(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		edges, err := ingestion.ListSymbolCallees(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("symbol_id")), pagination)
		writeIngestionResult(w, edges, err, http.StatusOK)
	})
}

func getSymbolCallGraphHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxDepth, err := positiveIntQuery(r, "max_depth")
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		maxNodes, err := positiveIntQuery(r, "max_nodes")
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		graph, err := ingestion.GetSymbolCallGraph(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("symbol_id")), projectingestion.CallGraphOptions{
			Direction: r.URL.Query().Get("direction"),
			MaxDepth:  maxDepth,
			MaxNodes:  maxNodes,
		})
		writeIngestionResult(w, graph, err, http.StatusOK)
	})
}

func getFileOutlineHandler(ingestion projectingestion.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		options, err := fileOutlineOptions(r)
		if err != nil {
			writeIngestionResult(w, nil, err, http.StatusOK)
			return
		}
		outline, err := ingestion.GetFileOutline(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("file_id")), options)
		writeIngestionResult(w, outline, err, http.StatusOK)
	})
}

func workspaceGitStatusHandler(workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		includeUntracked := true
		if raw := strings.TrimSpace(r.URL.Query().Get("include_untracked")); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				writeWorkspaceResult(w, nil, projectworkspace.ErrInvalidInput, http.StatusOK)
				return
			}
			includeUntracked = value
		}
		pageSize, err := positiveIntQuery(r, "page_size")
		if err != nil {
			writeWorkspaceResult(w, nil, err, http.StatusOK)
			return
		}
		status, err := workspace.GitStatus(r.Context(), strings.TrimSpace(r.PathValue("id")), projectworkspace.GitStatusOptions{
			IncludeUntracked: includeUntracked,
			PathPrefix:       r.URL.Query().Get("path_prefix"),
			PageSize:         pageSize,
			PageToken:        r.URL.Query().Get("page_token"),
		})
		writeWorkspaceResult(w, status, err, http.StatusOK)
	})
}

func workspaceGitDiffHandler(workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextLines, err := optionalNonNegativeIntQuery(r, "context_lines")
		if err != nil {
			writeWorkspaceResult(w, nil, err, http.StatusOK)
			return
		}
		maxDiffBytes, err := positiveIntQuery(r, "max_diff_bytes")
		if err != nil {
			writeWorkspaceResult(w, nil, err, http.StatusOK)
			return
		}
		diff, err := workspace.GitDiff(r.Context(), strings.TrimSpace(r.PathValue("id")), projectworkspace.GitDiffOptions{
			Scope:        r.URL.Query().Get("scope"),
			FileID:       r.URL.Query().Get("file_id"),
			RelativePath: r.URL.Query().Get("relative_path"),
			PathPrefix:   r.URL.Query().Get("path_prefix"),
			ContextLines: contextLines,
			MaxDiffBytes: maxDiffBytes,
			PageToken:    r.URL.Query().Get("page_token"),
		})
		writeWorkspaceResult(w, diff, err, http.StatusOK)
	})
}

func workspaceFileReadHandler(workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxBytes, err := positiveIntQuery(r, "max_bytes")
		if err != nil {
			writeWorkspaceResult(w, nil, err, http.StatusOK)
			return
		}
		file, err := workspace.ReadFile(r.Context(), strings.TrimSpace(r.PathValue("id")), projectworkspace.ReadFileOptions{
			FileID:       r.URL.Query().Get("file_id"),
			RelativePath: r.URL.Query().Get("relative_path"),
			MaxBytes:     maxBytes,
		})
		writeWorkspaceResult(w, file, err, http.StatusOK)
	})
}

func workspaceFileEditHandler(workspace projectworkspace.API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			FileID       string                       `json:"file_id,omitempty"`
			RelativePath string                       `json:"relative_path,omitempty"`
			EditToken    string                       `json:"edit_token"`
			DryRun       bool                         `json:"dry_run,omitempty"`
			Edits        []projectworkspace.ExactEdit `json:"edits"`
		}
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			writeWorkspaceResult(w, nil, projectworkspace.ErrInvalidInput, http.StatusOK)
			return
		}
		result, err := workspace.EditFile(r.Context(), strings.TrimSpace(r.PathValue("id")), projectworkspace.EditFileOptions{
			FileID:       input.FileID,
			RelativePath: input.RelativePath,
			EditToken:    input.EditToken,
			DryRun:       input.DryRun,
			Edits:        input.Edits,
		})
		writeWorkspaceResult(w, result, err, http.StatusOK)
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

func writeWorkspaceResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, projectworkspace.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project workspace resource not found")
		return
	}
	if errors.Is(err, projectworkspace.ErrGitUnavailable) {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "git_unavailable", "git is not available in the mivia-server runtime")
		return
	}
	if errors.Is(err, projectworkspace.ErrInvalidInput) ||
		errors.Is(err, projectworkspace.ErrWorkspaceDisabled) ||
		errors.Is(err, projectworkspace.ErrWorkspaceReadOnly) ||
		errors.Is(err, projectworkspace.ErrUnsafeContent) ||
		errors.Is(err, projectworkspace.ErrEditTokenInvalid) ||
		errors.Is(err, projectworkspace.ErrEditConflict) ||
		errors.Is(err, projectworkspace.ErrIngestionUnsupported) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_workspace_request", "project workspace request is invalid")
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func writeReliabilityResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, projectregistry.ErrProjectNotFound) || errors.Is(err, projectingestion.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project reliability resource not found")
		return
	}
	if errors.Is(err, projectregistry.ErrInvalidInput) ||
		errors.Is(err, projectingestion.ErrInvalidInput) ||
		errors.Is(err, projectingestion.ErrProjectDisabled) ||
		errors.Is(err, projectingestion.ErrUnsupportedIngest) ||
		errors.Is(err, projectworkspace.ErrWorkspaceDisabled) ||
		errors.Is(err, projectworkspace.ErrInvalidInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_reliability_request", "project reliability request is invalid")
		return
	}
	if errors.Is(err, projectworkspace.ErrGitUnavailable) {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "git_unavailable", "git is not available in the mivia-server runtime")
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
	pathPrefix, err := projectingestion.NormalizePathPrefix(r.URL.Query().Get("path_prefix"))
	if err != nil {
		return projectingestion.FileStateFilter{}, err
	}
	filter.PathPrefix = pathPrefix
	if skippedReason := strings.TrimSpace(r.URL.Query().Get("skipped_reason")); skippedReason != "" {
		filter.SkippedReason = projectingestion.SkipReason(skippedReason)
	}
	if presentRaw := strings.TrimSpace(r.URL.Query().Get("present")); presentRaw != "" {
		present, err := strconv.ParseBool(presentRaw)
		if err != nil {
			return projectingestion.FileStateFilter{}, projectregistry.ErrInvalidInput
		}
		filter.Present = &present
	}
	if modifiedSinceRaw := strings.TrimSpace(r.URL.Query().Get("modified_since")); modifiedSinceRaw != "" {
		modifiedSince, err := time.Parse(time.RFC3339, modifiedSinceRaw)
		if err != nil {
			return projectingestion.FileStateFilter{}, projectregistry.ErrInvalidInput
		}
		filter.ModifiedSince = modifiedSince.UTC()
	}
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

func symbolFilter(r *http.Request) (projectingestion.SymbolFilter, error) {
	caseSensitive, err := optionalBoolQuery(r, "case_sensitive")
	if err != nil {
		return projectingestion.SymbolFilter{}, err
	}
	return projectingestion.NormalizeSymbolFilter(projectingestion.SymbolFilter{
		Kind:          projectingestion.SymbolKind(strings.TrimSpace(r.URL.Query().Get("kind"))),
		NamePrefix:    r.URL.Query().Get("name_prefix"),
		NameContains:  r.URL.Query().Get("name_contains"),
		FileID:        strings.TrimSpace(r.URL.Query().Get("file_id")),
		Extension:     r.URL.Query().Get("extension"),
		Package:       r.URL.Query().Get("package"),
		Receiver:      r.URL.Query().Get("receiver"),
		CaseSensitive: caseSensitive,
	})
}

func textSearchOptions(r *http.Request) (projectingestion.TextSearchOptions, error) {
	pageSize, err := positiveIntQuery(r, "page_size")
	if err != nil {
		return projectingestion.TextSearchOptions{}, err
	}
	maxSnippetBytes, err := positiveIntQuery(r, "max_snippet_bytes")
	if err != nil {
		return projectingestion.TextSearchOptions{}, err
	}
	maxMatches, err := positiveIntQuery(r, "max_matches")
	if err != nil {
		return projectingestion.TextSearchOptions{}, err
	}
	caseSensitive, err := optionalBoolQuery(r, "case_sensitive")
	if err != nil {
		return projectingestion.TextSearchOptions{}, err
	}
	return projectingestion.NormalizeTextSearchOptions(projectingestion.TextSearchOptions{
		Query:           r.URL.Query().Get("query"),
		Mode:            r.URL.Query().Get("mode"),
		CaseSensitive:   caseSensitive,
		Extension:       r.URL.Query().Get("extension"),
		PathPrefix:      r.URL.Query().Get("path_prefix"),
		PageSize:        pageSize,
		PageToken:       r.URL.Query().Get("page_token"),
		MaxSnippetBytes: maxSnippetBytes,
		MaxMatches:      maxMatches,
	})
}

func fileSearchOptions(r *http.Request) (projectingestion.FileSearchOptions, error) {
	pageSize, err := positiveIntQuery(r, "page_size")
	if err != nil {
		return projectingestion.FileSearchOptions{}, err
	}
	caseSensitive, err := optionalBoolQuery(r, "case_sensitive")
	if err != nil {
		return projectingestion.FileSearchOptions{}, err
	}
	return projectingestion.NormalizeFileSearchOptions(projectingestion.FileSearchOptions{
		Extension:     r.URL.Query().Get("extension"),
		PathPrefix:    r.URL.Query().Get("path_prefix"),
		PathContains:  r.URL.Query().Get("path_contains"),
		CaseSensitive: caseSensitive,
		PageSize:      pageSize,
		PageToken:     r.URL.Query().Get("page_token"),
	})
}

func referenceSearchOptions(r *http.Request) (projectingestion.ReferenceSearchOptions, error) {
	pageSize, err := positiveIntQuery(r, "page_size")
	if err != nil {
		return projectingestion.ReferenceSearchOptions{}, err
	}
	caseSensitive, err := optionalBoolQuery(r, "case_sensitive")
	if err != nil {
		return projectingestion.ReferenceSearchOptions{}, err
	}
	return projectingestion.NormalizeReferenceSearchOptions(projectingestion.ReferenceSearchOptions{
		NameContains:       r.URL.Query().Get("name_contains"),
		TargetNameContains: r.URL.Query().Get("target_name_contains"),
		CallerNameContains: r.URL.Query().Get("caller_name_contains"),
		CalleeNameContains: r.URL.Query().Get("callee_name_contains"),
		EnclosingContains:  r.URL.Query().Get("enclosing_contains"),
		Extension:          r.URL.Query().Get("extension"),
		PathPrefix:         r.URL.Query().Get("path_prefix"),
		ResolutionStatus:   r.URL.Query().Get("resolution_status"),
		Confidence:         r.URL.Query().Get("confidence"),
		CaseSensitive:      caseSensitive,
		PageSize:           pageSize,
		PageToken:          r.URL.Query().Get("page_token"),
	})
}

func astSearchOptions(r *http.Request) (projectingestion.ASTSearchOptions, error) {
	pageSize, err := positiveIntQuery(r, "page_size")
	if err != nil {
		return projectingestion.ASTSearchOptions{}, err
	}
	maxSnippetBytes, err := positiveIntQuery(r, "max_snippet_bytes")
	if err != nil {
		return projectingestion.ASTSearchOptions{}, err
	}
	maxMatches, err := positiveIntQuery(r, "max_matches")
	if err != nil {
		return projectingestion.ASTSearchOptions{}, err
	}
	captures := []string(nil)
	if raw := strings.TrimSpace(r.URL.Query().Get("captures")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			captures = append(captures, strings.TrimSpace(part))
		}
	}
	return projectingestion.NormalizeASTSearchOptions(projectingestion.ASTSearchOptions{
		Language:        r.URL.Query().Get("language"),
		Query:           r.URL.Query().Get("query"),
		Captures:        captures,
		Extension:       r.URL.Query().Get("extension"),
		PathPrefix:      r.URL.Query().Get("path_prefix"),
		PageSize:        pageSize,
		PageToken:       r.URL.Query().Get("page_token"),
		MaxSnippetBytes: maxSnippetBytes,
		MaxMatches:      maxMatches,
	})
}

func fileOutlineOptions(r *http.Request) (projectingestion.FileOutlineOptions, error) {
	pageSize, err := positiveIntQuery(r, "symbol_page_size")
	if err != nil {
		return projectingestion.FileOutlineOptions{}, err
	}
	maxChunkBytes, err := positiveIntQuery(r, "max_chunk_bytes")
	if err != nil {
		return projectingestion.FileOutlineOptions{}, err
	}
	includeChunkText := false
	if raw := strings.TrimSpace(r.URL.Query().Get("include_chunk_text")); raw != "" {
		includeChunkText, err = strconv.ParseBool(raw)
		if err != nil {
			return projectingestion.FileOutlineOptions{}, projectregistry.ErrInvalidInput
		}
	}
	filter, err := projectingestion.NormalizeSymbolFilter(projectingestion.SymbolFilter{
		Kind:       projectingestion.SymbolKind(strings.TrimSpace(r.URL.Query().Get("kind"))),
		NamePrefix: r.URL.Query().Get("name_prefix"),
	})
	if err != nil {
		return projectingestion.FileOutlineOptions{}, err
	}
	return projectingestion.FileOutlineOptions{
		SymbolFilter:     filter,
		IncludeChunkText: includeChunkText,
		MaxChunkBytes:    maxChunkBytes,
		SymbolPagination: projectingestion.Pagination{
			PageSize:  pageSize,
			PageToken: r.URL.Query().Get("symbol_page_token"),
		},
	}, nil
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

func optionalNonNegativeIntQuery(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, projectregistry.ErrInvalidInput
	}
	return value, nil
}

func optionalBoolQuery(r *http.Request, name string) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, projectregistry.ErrInvalidInput
	}
	return value, nil
}
