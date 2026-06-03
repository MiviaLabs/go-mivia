package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type claimListResponse struct {
	Claims        []projectevidence.Claim `json:"claims"`
	NextPageToken string                  `json:"next_page_token,omitempty"`
}

func RegisterRoutes(mux *http.ServeMux, svc *projectevidence.Service) {
	if svc == nil {
		return
	}
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims", createClaimHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/evidence-graph/claims", listClaimsHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/evidence-graph/claims/{claim_id}", getClaimHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/evidence", appendEvidenceHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/decisions", createDecisionHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/actions", createActionHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/outcomes", createOutcomeHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/artifact-links", linkArtifactHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/evidence-graph/claims/{claim_id}/promotion-links", linkPromotionHandler(svc))
}

func createClaimHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.CreateClaimInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		claim, err := svc.CreateClaim(r.Context(), input)
		writeResult(w, claim, err, http.StatusCreated)
	})
}

func listClaimsHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageSize, pageToken, err := pagination(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		claims, err := svc.ListClaims(r.Context(), projectID(r), projectevidence.ClaimFilter{
			ArtifactRef:    strings.TrimSpace(r.URL.Query().Get("artifact_ref")),
			PromotionState: strings.TrimSpace(r.URL.Query().Get("promotion_state")),
			OutcomeStatus:  strings.TrimSpace(r.URL.Query().Get("outcome_status")),
			RunID:          strings.TrimSpace(r.URL.Query().Get("run_id")),
			TraceID:        strings.TrimSpace(r.URL.Query().Get("trace_id")),
		})
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		list := paginateClaims(claims, pageSize, pageToken)
		writeResult(w, list, nil, http.StatusOK)
	})
}

func getClaimHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record, err := svc.GetClaim(r.Context(), projectID(r), claimID(r))
		writeResult(w, record, err, http.StatusOK)
	})
}

func appendEvidenceHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.AppendEvidenceInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.ClaimID = claimID(r)
		evidence, err := svc.AppendEvidence(r.Context(), input)
		writeResult(w, evidence, err, http.StatusCreated)
	})
}

func createDecisionHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.CreateDecisionInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.ClaimID = claimID(r)
		decision, err := svc.CreateDecision(r.Context(), input)
		writeResult(w, decision, err, http.StatusCreated)
	})
}

func createActionHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.CreateActionInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.ClaimID = claimID(r)
		action, err := svc.CreateAction(r.Context(), input)
		writeResult(w, action, err, http.StatusCreated)
	})
}

func createOutcomeHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.CreateOutcomeInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.ClaimID = claimID(r)
		outcome, err := svc.CreateOutcome(r.Context(), input)
		writeResult(w, outcome, err, http.StatusCreated)
	})
}

func linkArtifactHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.LinkArtifactInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.ClaimID = claimID(r)
		link, err := svc.LinkArtifact(r.Context(), input)
		writeResult(w, link, err, http.StatusCreated)
	})
}

func linkPromotionHandler(svc *projectevidence.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectevidence.LinkPromotionInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.ClaimID = claimID(r)
		link, err := svc.LinkPromotion(r.Context(), input)
		writeResult(w, link, err, http.StatusCreated)
	})
}

func writeResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, projectregistry.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project evidence resource not found")
		return
	}
	if errors.Is(err, projectevidence.ErrInvalidInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_evidence_request", "project evidence request is invalid")
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

func pagination(r *http.Request) (int, int, error) {
	pageSize := defaultPageSize
	rawPageSize := strings.TrimSpace(r.URL.Query().Get("page_size"))
	if rawPageSize != "" {
		parsed, err := strconv.Atoi(rawPageSize)
		if err != nil || parsed < 1 || parsed > maxPageSize {
			return 0, 0, fmt.Errorf("%w: page_size is invalid", projectevidence.ErrInvalidInput)
		}
		pageSize = parsed
	}
	pageToken := 0
	rawPageToken := strings.TrimSpace(r.URL.Query().Get("page_token"))
	if rawPageToken != "" {
		parsed, err := strconv.Atoi(rawPageToken)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("%w: page_token is invalid", projectevidence.ErrInvalidInput)
		}
		pageToken = parsed
	}
	return pageSize, pageToken, nil
}

func paginateClaims(claims []projectevidence.Claim, pageSize int, pageToken int) claimListResponse {
	if pageToken >= len(claims) {
		return claimListResponse{Claims: []projectevidence.Claim{}}
	}
	end := pageToken + pageSize
	if end > len(claims) {
		end = len(claims)
	}
	out := claimListResponse{Claims: claims[pageToken:end]}
	if end < len(claims) {
		out.NextPageToken = strconv.Itoa(end)
	}
	return out
}
