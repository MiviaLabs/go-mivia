package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type scoreClaimRequest struct {
	ChangedPaths    []string `json:"changed_paths,omitempty"`
	ClaimCheckPaths []string `json:"claim_check_paths,omitempty"`
	IncludeVerified bool     `json:"include_verified,omitempty"`
}

type assessmentListResponse struct {
	Assessments   []projectconfidence.ConfidenceAssessment `json:"assessments"`
	NextPageToken string                                   `json:"next_page_token,omitempty"`
}

func RegisterRoutes(mux *http.ServeMux, svc *projectconfidence.Service, adapter *projectconfidence.ReliabilityInputAdapter) {
	if svc == nil {
		return
	}
	mux.Handle("POST /api/v1/projects/{id}/confidence/claims/{claim_id}/score", scoreClaimHandler(svc, adapter))
	mux.Handle("GET /api/v1/projects/{id}/confidence/claims", listAssessmentsHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/confidence/claims/{claim_id}", getAssessmentHandler(svc))
}

func scoreClaimHandler(svc *projectconfidence.Service, adapter *projectconfidence.ReliabilityInputAdapter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if adapter == nil {
			httpserver.WriteError(w, http.StatusServiceUnavailable, "confidence_inputs_unavailable", "confidence input adapter is unavailable")
			return
		}
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input scoreClaimRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		assessment, err := svc.ScoreClaimWithInputs(r.Context(), adapter, projectconfidence.ReliabilityInputOptions{
			ProjectID:       projectID(r),
			ClaimID:         claimID(r),
			ChangedPaths:    input.ChangedPaths,
			ClaimCheckPaths: input.ClaimCheckPaths,
			IncludeVerified: input.IncludeVerified,
		})
		writeResult(w, projectconfidence.ScoreClaimResponse{Assessment: assessment}, err, http.StatusOK)
	})
}

func getAssessmentHandler(svc *projectconfidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assessment, err := svc.GetAssessment(r.Context(), projectID(r), claimID(r))
		writeResult(w, assessment, err, http.StatusOK)
	})
}

func listAssessmentsHandler(svc *projectconfidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageSize, pageToken, err := pagination(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		filter, err := assessmentFilter(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		assessments, err := svc.ListAssessments(r.Context(), projectID(r), filter)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		writeResult(w, paginateAssessments(assessments, pageSize, pageToken), nil, http.StatusOK)
	})
}

func assessmentFilter(r *http.Request) (projectconfidence.AssessmentFilter, error) {
	minScore, err := optionalScore(r, "min_score")
	if err != nil {
		return projectconfidence.AssessmentFilter{}, err
	}
	maxScore, err := optionalScore(r, "max_score")
	if err != nil {
		return projectconfidence.AssessmentFilter{}, err
	}
	return projectconfidence.AssessmentFilter{
		Band:           strings.TrimSpace(r.URL.Query().Get("band")),
		MinScore:       minScore,
		MaxScore:       maxScore,
		Recommendation: strings.TrimSpace(r.URL.Query().Get("recommendation")),
		RunID:          strings.TrimSpace(r.URL.Query().Get("run_id")),
		TraceID:        strings.TrimSpace(r.URL.Query().Get("trace_id")),
	}, nil
}

func optionalScore(r *http.Request, name string) (*int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > 100 {
		return nil, fmt.Errorf("%w: %s is invalid", projectconfidence.ErrInvalidInput, name)
	}
	return &value, nil
}

func pagination(r *http.Request) (int, int, error) {
	pageSize := defaultPageSize
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPageSize {
			return 0, 0, fmt.Errorf("%w: page_size is invalid", projectconfidence.ErrInvalidInput)
		}
		pageSize = parsed
	}
	pageToken := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("page_token")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("%w: page_token is invalid", projectconfidence.ErrInvalidInput)
		}
		pageToken = parsed
	}
	return pageSize, pageToken, nil
}

func paginateAssessments(assessments []projectconfidence.ConfidenceAssessment, pageSize int, pageToken int) assessmentListResponse {
	if pageToken >= len(assessments) {
		return assessmentListResponse{Assessments: []projectconfidence.ConfidenceAssessment{}}
	}
	end := pageToken + pageSize
	if end > len(assessments) {
		end = len(assessments)
	}
	out := assessmentListResponse{Assessments: assessments[pageToken:end]}
	if end < len(assessments) {
		out.NextPageToken = strconv.Itoa(end)
	}
	return out
}

func writeResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, confidencestore.ErrNotFound) ||
		errors.Is(err, evidencestore.ErrNotFound) ||
		errors.Is(err, projectregistry.ErrProjectNotFound) ||
		errors.Is(err, projectingestion.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project confidence resource not found")
		return
	}
	if errors.Is(err, projectworkspace.ErrGitUnavailable) {
		httpserver.WriteError(w, http.StatusServiceUnavailable, "git_unavailable", "git is not available in the mivia-server runtime")
		return
	}
	if errors.Is(err, projectconfidence.ErrInvalidInput) ||
		errors.Is(err, projectregistry.ErrInvalidInput) ||
		errors.Is(err, projectingestion.ErrInvalidInput) ||
		errors.Is(err, projectingestion.ErrProjectDisabled) ||
		errors.Is(err, projectworkspace.ErrInvalidInput) ||
		errors.Is(err, projectworkspace.ErrWorkspaceDisabled) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_confidence_request", "project confidence request is invalid")
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func projectID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("id"))
}

func claimID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("claim_id"))
}
