package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	knowledgestore "github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

type transitionRequest struct {
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
}

type submitOrgReviewRequest struct {
	OrgRef      string `json:"org_ref"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by"`
}

type promoteOrgRequest struct {
	Scope       string `json:"scope"`
	OrgRef      string `json:"org_ref"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by"`
}

type knowledgeListResponse struct {
	Knowledge     []projectknowledge.KnowledgeRecord `json:"knowledge"`
	NextPageToken string                             `json:"next_page_token,omitempty"`
}

func RegisterRoutes(mux *http.ServeMux, svc *projectknowledge.Service, adapter *projectknowledge.PromotionInputAdapter) {
	if svc == nil {
		return
	}
	mux.Handle("POST /api/v1/projects/{id}/knowledge/candidates", createCandidateHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/validate", validateCandidateHandler(svc, adapter))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/promote-project", promoteProjectHandler(svc, adapter))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/submit-org-review", submitOrgReviewHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/promote-org", promoteOrgHandler(svc, adapter))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/reject", rejectHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/supersede", supersedeHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/knowledge/{knowledge_id}/reuse-events", recordReuseEventHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/knowledge/{knowledge_id}", getKnowledgeHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/knowledge", listProjectKnowledgeHandler(svc))
	mux.Handle("GET /api/v1/orgs/{org_ref}/knowledge", listOrgKnowledgeHandler(svc))
}

func createCandidateHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !httpserver.RequireJSON(r) {
			httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
			return
		}
		var input projectknowledge.CreateCandidateInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		record, err := svc.CreateCandidate(r.Context(), input)
		writeResult(w, record, err, http.StatusCreated)
	})
}

func validateCandidateHandler(svc *projectknowledge.Service, adapter *projectknowledge.PromotionInputAdapter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ensureAdapter(w, adapter) || !requireJSON(w, r) {
			return
		}
		var input transitionRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		record, err := svc.ValidateCandidateWithInputs(r.Context(), adapter, projectknowledge.ValidateCandidateWithInputsInput{
			ProjectID:   projectID(r),
			KnowledgeID: knowledgeID(r),
			DecisionRef: input.DecisionRef,
			VerifierRef: input.VerifierRef,
			Rationale:   input.Rationale,
		})
		writeResult(w, record, err, http.StatusOK)
	})
}

func promoteProjectHandler(svc *projectknowledge.Service, adapter *projectknowledge.PromotionInputAdapter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ensureAdapter(w, adapter) || !requireJSON(w, r) {
			return
		}
		var input transitionRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		record, err := svc.PromoteProjectWithInputs(r.Context(), adapter, projectknowledge.PromoteProjectWithInputsInput{
			ProjectID:   projectID(r),
			KnowledgeID: knowledgeID(r),
			DecisionRef: input.DecisionRef,
			VerifierRef: input.VerifierRef,
			Rationale:   input.Rationale,
		})
		writeResult(w, record, err, http.StatusOK)
	})
}

func submitOrgReviewHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input submitOrgReviewRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if strings.TrimSpace(input.OrgRef) == "" {
			writeResult(w, nil, fmt.Errorf("%w: org_ref is required", projectknowledge.ErrInvalidInput), http.StatusOK)
			return
		}
		record, err := svc.SubmitOrgReview(r.Context(), projectknowledge.SubmitOrgReviewInput{
			ProjectID:   projectID(r),
			KnowledgeID: knowledgeID(r),
			OrgRef:      input.OrgRef,
			DecisionRef: input.DecisionRef,
			VerifierRef: input.VerifierRef,
			Rationale:   input.Rationale,
			DecidedBy:   input.DecidedBy,
		})
		writeResult(w, record, err, http.StatusOK)
	})
}

func promoteOrgHandler(svc *projectknowledge.Service, adapter *projectknowledge.PromotionInputAdapter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ensureAdapter(w, adapter) || !requireJSON(w, r) {
			return
		}
		var input promoteOrgRequest
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if strings.TrimSpace(input.OrgRef) == "" {
			writeResult(w, nil, fmt.Errorf("%w: org_ref is required", projectknowledge.ErrInvalidInput), http.StatusOK)
			return
		}
		record, err := svc.PromoteOrgWithInputs(r.Context(), adapter, projectknowledge.PromoteOrgWithInputsInput{
			ProjectID:   projectID(r),
			KnowledgeID: knowledgeID(r),
			Scope:       input.Scope,
			OrgRef:      input.OrgRef,
			DecisionRef: input.DecisionRef,
			VerifierRef: input.VerifierRef,
			Rationale:   input.Rationale,
			DecidedBy:   input.DecidedBy,
		})
		writeResult(w, record, err, http.StatusOK)
	})
}

func rejectHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectknowledge.RejectInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.KnowledgeID = knowledgeID(r)
		record, err := svc.Reject(r.Context(), input)
		writeResult(w, record, err, http.StatusOK)
	})
}

func supersedeHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectknowledge.SupersedeInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.KnowledgeID = knowledgeID(r)
		record, err := svc.Supersede(r.Context(), input)
		writeResult(w, record, err, http.StatusOK)
	})
}

func recordReuseEventHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectknowledge.RecordReuseEventInput
		if err := httpserver.DecodeJSON(r, &input); err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		input.ProjectID = projectID(r)
		input.KnowledgeID = knowledgeID(r)
		event, err := svc.RecordReuseEvent(r.Context(), input)
		writeResult(w, event, err, http.StatusCreated)
	})
}

func getKnowledgeHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record, err := svc.GetKnowledge(r.Context(), projectID(r), knowledgeID(r))
		writeResult(w, record, err, http.StatusOK)
	})
}

func listProjectKnowledgeHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageSize, pageToken, err := pagination(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		filter, err := knowledgeFilter(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		records, err := svc.ListKnowledge(r.Context(), projectID(r), filter)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		writeResult(w, paginateKnowledge(records, pageSize, pageToken), nil, http.StatusOK)
	})
}

func listOrgKnowledgeHandler(svc *projectknowledge.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageSize, pageToken, err := pagination(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		filter, err := orgKnowledgeFilter(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		records, err := svc.ListOrgKnowledge(r.Context(), orgRef(r), filter)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		writeResult(w, paginateKnowledge(records, pageSize, pageToken), nil, http.StatusOK)
	})
}

func knowledgeFilter(r *http.Request) (projectknowledge.KnowledgeFilter, error) {
	minConfidence, err := optionalConfidence(r, "min_confidence")
	if err != nil {
		return projectknowledge.KnowledgeFilter{}, err
	}
	maxConfidence, err := optionalConfidence(r, "max_confidence")
	if err != nil {
		return projectknowledge.KnowledgeFilter{}, err
	}
	return projectknowledge.KnowledgeFilter{
		Scope:          strings.TrimSpace(r.URL.Query().Get("scope")),
		State:          strings.TrimSpace(r.URL.Query().Get("state")),
		ClaimID:        strings.TrimSpace(r.URL.Query().Get("claim_id")),
		KnowledgeRef:   strings.TrimSpace(r.URL.Query().Get("knowledge_ref")),
		ConfidenceBand: strings.TrimSpace(r.URL.Query().Get("confidence_band")),
		MinConfidence:  minConfidence,
		MaxConfidence:  maxConfidence,
	}, nil
}

func orgKnowledgeFilter(r *http.Request) (projectknowledge.KnowledgeFilter, error) {
	minConfidence, err := optionalConfidence(r, "min_confidence")
	if err != nil {
		return projectknowledge.KnowledgeFilter{}, err
	}
	maxConfidence, err := optionalConfidence(r, "max_confidence")
	if err != nil {
		return projectknowledge.KnowledgeFilter{}, err
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state != "" && state != projectknowledge.StateOrgPromoted {
		return projectknowledge.KnowledgeFilter{}, fmt.Errorf("%w: org knowledge state must be org_promoted", projectknowledge.ErrInvalidInput)
	}
	return projectknowledge.KnowledgeFilter{
		Scope:          projectknowledge.ScopeOrg,
		State:          projectknowledge.StateOrgPromoted,
		ClaimID:        strings.TrimSpace(r.URL.Query().Get("claim_id")),
		KnowledgeRef:   strings.TrimSpace(r.URL.Query().Get("knowledge_ref")),
		ConfidenceBand: strings.TrimSpace(r.URL.Query().Get("confidence_band")),
		MinConfidence:  minConfidence,
		MaxConfidence:  maxConfidence,
	}, nil
}

func optionalConfidence(r *http.Request, name string) (*int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > 100 {
		return nil, fmt.Errorf("%w: %s is invalid", projectknowledge.ErrInvalidInput, name)
	}
	return &value, nil
}

func pagination(r *http.Request) (int, int, error) {
	pageSize := defaultPageSize
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxPageSize {
			return 0, 0, fmt.Errorf("%w: page_size is invalid", projectknowledge.ErrInvalidInput)
		}
		pageSize = parsed
	}
	pageToken := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("page_token")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("%w: page_token is invalid", projectknowledge.ErrInvalidInput)
		}
		pageToken = parsed
	}
	return pageSize, pageToken, nil
}

func paginateKnowledge(records []projectknowledge.KnowledgeRecord, pageSize int, pageToken int) knowledgeListResponse {
	if pageToken >= len(records) {
		return knowledgeListResponse{Knowledge: []projectknowledge.KnowledgeRecord{}}
	}
	end := pageToken + pageSize
	if end > len(records) {
		end = len(records)
	}
	out := knowledgeListResponse{Knowledge: records[pageToken:end]}
	if end < len(records) {
		out.NextPageToken = strconv.Itoa(end)
	}
	return out
}

func ensureAdapter(w http.ResponseWriter, adapter *projectknowledge.PromotionInputAdapter) bool {
	if adapter != nil {
		return true
	}
	httpserver.WriteError(w, http.StatusServiceUnavailable, "knowledge_inputs_unavailable", "project knowledge input adapter is unavailable")
	return false
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if httpserver.RequireJSON(r) {
		return true
	}
	httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
	return false
}

func writeResult(w http.ResponseWriter, body any, err error, successStatus int) {
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, knowledgestore.ErrNotFound) ||
		errors.Is(err, evidencestore.ErrNotFound) ||
		errors.Is(err, confidencestore.ErrNotFound) ||
		errors.Is(err, projectregistry.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project knowledge resource not found")
		return
	}
	if errors.Is(err, projectknowledge.ErrInvalidInput) || errors.Is(err, projectregistry.ErrInvalidInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_knowledge_request", "project knowledge request is invalid")
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func projectID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("id"))
}

func knowledgeID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("knowledge_id"))
}

func orgRef(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("org_ref"))
}
