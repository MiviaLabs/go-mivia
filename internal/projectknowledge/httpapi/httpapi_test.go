package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge/httpapi"
	knowledgestore "github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

func TestKnowledgeRoutesLifecycle(t *testing.T) {
	harness := newHarness()
	claim := harness.createPromotableClaim(t, false)
	projectID := "project_1"

	candidate := postJSON[projectknowledge.KnowledgeRecord](t, harness.mux, "/api/v1/projects/"+projectID+"/knowledge/candidates", candidateBody(claim.Claim.ID, claim.Claim.ClaimRef), http.StatusCreated)
	if candidate.ProjectID != projectID || candidate.State != projectknowledge.StateCandidate || candidate.ClaimID != claim.Claim.ID {
		t.Fatalf("expected path project candidate, got %#v", candidate)
	}

	validated := postJSON[projectknowledge.KnowledgeRecord](t, harness.mux, knowledgePath(projectID, candidate.ID, "validate"), transitionBody("knowledge_validated"), http.StatusOK)
	if validated.State != projectknowledge.StateValidated || validated.ConfidenceScore < 85 || validated.ConfidenceAssessmentID == "" {
		t.Fatalf("expected validated metadata with confidence, got %#v", validated)
	}

	projectPromoted := postJSON[projectknowledge.KnowledgeRecord](t, harness.mux, knowledgePath(projectID, candidate.ID, "promote-project"), transitionBody("knowledge_project_promoted"), http.StatusOK)
	if projectPromoted.State != projectknowledge.StateProjectPromoted || projectPromoted.Scope != projectknowledge.ScopeProject {
		t.Fatalf("expected project promoted record, got %#v", projectPromoted)
	}

	unsupportedReviewOrg := postRaw(t, harness.mux, knowledgePath(projectID, candidate.ID, "submit-org-review"), `{"org_ref":"other","decision_ref":"org_review_other","verifier_ref":"verifier_ref","rationale":"org review requested","decided_by":"owner_review"}`)
	if unsupportedReviewOrg.Code != http.StatusBadRequest || !strings.Contains(unsupportedReviewOrg.Body.String(), "invalid_project_knowledge_request") {
		t.Fatalf("expected unsupported org review rejection, got %d: %s", unsupportedReviewOrg.Code, unsupportedReviewOrg.Body.String())
	}

	underReview := postJSON[projectknowledge.KnowledgeRecord](t, harness.mux, knowledgePath(projectID, candidate.ID, "submit-org-review"), `{"org_ref":"default","decision_ref":"org_review","verifier_ref":"verifier_ref","rationale":"org review requested","decided_by":"owner_review"}`, http.StatusOK)
	if underReview.State != projectknowledge.StateOrgReview || underReview.Scope != projectknowledge.ScopeOrg || underReview.OrgRef != projectknowledge.DefaultOrgRef {
		t.Fatalf("expected org review record, got %#v", underReview)
	}

	mismatchedPromotionOrg := postRaw(t, harness.mux, knowledgePath(projectID, candidate.ID, "promote-org"), `{"scope":"org","org_ref":"other","decision_ref":"org_promote_other","verifier_ref":"verifier_ref","rationale":"org gate checked","decided_by":"owner_review"}`)
	if mismatchedPromotionOrg.Code != http.StatusBadRequest || !strings.Contains(mismatchedPromotionOrg.Body.String(), "invalid_project_knowledge_request") {
		t.Fatalf("expected mismatched org promotion rejection, got %d: %s", mismatchedPromotionOrg.Code, mismatchedPromotionOrg.Body.String())
	}

	orgPromoted := postJSON[projectknowledge.KnowledgeRecord](t, harness.mux, knowledgePath(projectID, candidate.ID, "promote-org"), `{"scope":"org","org_ref":"default","decision_ref":"org_promote_decision","verifier_ref":"verifier_ref","rationale":"org gate checked","decided_by":"owner_review"}`, http.StatusOK)
	if orgPromoted.State != projectknowledge.StateOrgPromoted || orgPromoted.Scope != projectknowledge.ScopeOrg || orgPromoted.OrgRef != projectknowledge.DefaultOrgRef {
		t.Fatalf("expected org promoted record, got %#v", orgPromoted)
	}

	reuse := postJSON[projectknowledge.KnowledgeReuseEvent](t, harness.mux, knowledgePath(projectID, candidate.ID, "reuse-events"), `{"agent_run_id":"run_2","trace_id":"trace_2","reuse_ref":"reuse/ref_1","revalidated":true,"revalidation_ref":"verifier/recheck_1","outcome":"used","summary":"metadata-only reuse recorded"}`, http.StatusCreated)
	if reuse.KnowledgeID != candidate.ID || reuse.Outcome != projectknowledge.ReuseOutcomeUsed || !reuse.Revalidated {
		t.Fatalf("expected reuse event, got %#v", reuse)
	}

	getRes := httptest.NewRecorder()
	harness.mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/knowledge/"+candidate.ID, nil))
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), projectknowledge.StateOrgPromoted) {
		t.Fatalf("expected get knowledge, got %d: %s", getRes.Code, getRes.Body.String())
	}
	assertDoesNotLeak(t, getRes.Body.String())

	for _, path := range []string{
		"/api/v1/projects/" + projectID + "/knowledge?scope=org&state=org_promoted&claim_id=" + claim.Claim.ID + "&knowledge_ref=knowledge/ref_1&confidence_band=high&min_confidence=90&max_confidence=100&page_size=1",
		"/api/v1/orgs/default/knowledge?state=org_promoted&claim_id=" + claim.Claim.ID + "&knowledge_ref=knowledge/ref_1&confidence_band=high&min_confidence=90&max_confidence=100&page_size=1",
	} {
		res := httptest.NewRecorder()
		harness.mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), candidate.ID) {
			t.Fatalf("expected list response for %s, got %d: %s", path, res.Code, res.Body.String())
		}
		assertDoesNotLeak(t, res.Body.String())
		var listed knowledgeListResponse
		if err := json.Unmarshal(res.Body.Bytes(), &listed); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(listed.Knowledge) != 1 || listed.Knowledge[0].State != projectknowledge.StateOrgPromoted {
			t.Fatalf("expected metadata-only org promoted list, got %#v", listed)
		}
	}

	nonmatchingOrgFilter := httptest.NewRecorder()
	harness.mux.ServeHTTP(nonmatchingOrgFilter, httptest.NewRequest(http.MethodGet, "/api/v1/orgs/default/knowledge?state=org_promoted&claim_id=claim_other&min_confidence=99&max_confidence=100", nil))
	if nonmatchingOrgFilter.Code != http.StatusOK {
		t.Fatalf("expected nonmatching org filter success, got %d: %s", nonmatchingOrgFilter.Code, nonmatchingOrgFilter.Body.String())
	}
	var nonmatching knowledgeListResponse
	if err := json.Unmarshal(nonmatchingOrgFilter.Body.Bytes(), &nonmatching); err != nil {
		t.Fatalf("decode nonmatching org filter: %v", err)
	}
	if len(nonmatching.Knowledge) != 0 {
		t.Fatalf("expected nonmatching org filters to exclude knowledge, got %#v", nonmatching)
	}

	unsupportedOrgList := httptest.NewRecorder()
	harness.mux.ServeHTTP(unsupportedOrgList, httptest.NewRequest(http.MethodGet, "/api/v1/orgs/other/knowledge?state=org_promoted", nil))
	if unsupportedOrgList.Code != http.StatusBadRequest || !strings.Contains(unsupportedOrgList.Body.String(), "invalid_project_knowledge_request") {
		t.Fatalf("expected unsupported org list rejection, got %d: %s", unsupportedOrgList.Code, unsupportedOrgList.Body.String())
	}
}

func TestKnowledgeRoutesRejectBadRequests(t *testing.T) {
	harness := newHarness()
	claim := harness.createPromotableClaim(t, false)
	projectID := "project_1"

	unsupported := httptest.NewRecorder()
	harness.mux.ServeHTTP(unsupported, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/knowledge/candidates", bytes.NewBufferString(candidateBody(claim.Claim.ID, claim.Claim.ClaimRef))))
	if unsupported.Code != http.StatusUnsupportedMediaType || !strings.Contains(unsupported.Body.String(), "unsupported_media_type") {
		t.Fatalf("expected unsupported media type, got %d: %s", unsupported.Code, unsupported.Body.String())
	}

	invalidJSON := postRaw(t, harness.mux, "/api/v1/projects/"+projectID+"/knowledge/candidates", `{"knowledge_ref":`)
	if invalidJSON.Code != http.StatusBadRequest || !strings.Contains(invalidJSON.Body.String(), "invalid_json") {
		t.Fatalf("expected invalid_json, got %d: %s", invalidJSON.Code, invalidJSON.Body.String())
	}

	unsafeRef := postRaw(t, harness.mux, "/api/v1/projects/"+projectID+"/knowledge/candidates", `{"knowledge_ref":"docs/../unsafe","claim_id":"`+claim.Claim.ID+`","claim_ref":"`+claim.Claim.ClaimRef+`","summary":"metadata-only guidance","reuse_guidance":"revalidate before reuse"}`)
	if unsafeRef.Code != http.StatusBadRequest || !strings.Contains(unsafeRef.Body.String(), "invalid_project_knowledge_request") {
		t.Fatalf("expected unsafe ref rejection, got %d: %s", unsafeRef.Code, unsafeRef.Body.String())
	}

	for _, summary := range []string{"raw prompt marker", "raw source marker", "secret=bad", "owner" + "@" + "example" + ".invalid"} {
		body := `{"knowledge_ref":"knowledge/ref_unsafe","claim_id":"` + claim.Claim.ID + `","claim_ref":"` + claim.Claim.ClaimRef + `","summary":"` + summary + `","reuse_guidance":"revalidate before reuse"}`
		res := postRaw(t, harness.mux, "/api/v1/projects/"+projectID+"/knowledge/candidates", body)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_project_knowledge_request") {
			t.Fatalf("expected unsafe payload rejection for %q, got %d: %s", summary, res.Code, res.Body.String())
		}
		assertDoesNotLeak(t, res.Body.String())
	}

	candidate := postJSON[projectknowledge.KnowledgeRecord](t, harness.mux, "/api/v1/projects/"+projectID+"/knowledge/candidates", candidateBody(claim.Claim.ID, claim.Claim.ClaimRef), http.StatusCreated)
	invalidTransition := postRaw(t, harness.mux, knowledgePath(projectID, candidate.ID, "promote-project"), transitionBody("promote_before_validate"))
	if invalidTransition.Code != http.StatusBadRequest || !strings.Contains(invalidTransition.Body.String(), "invalid_project_knowledge_request") {
		t.Fatalf("expected invalid transition rejection, got %d: %s", invalidTransition.Code, invalidTransition.Body.String())
	}

	orgBeforeProject := postRaw(t, harness.mux, knowledgePath(projectID, candidate.ID, "promote-org"), `{"scope":"org","org_ref":"default","decision_ref":"org_promote_decision","verifier_ref":"verifier_ref","rationale":"org gate checked","decided_by":"owner_review"}`)
	if orgBeforeProject.Code != http.StatusBadRequest || !strings.Contains(orgBeforeProject.Body.String(), "invalid_project_knowledge_request") {
		t.Fatalf("expected org promotion before project promotion rejection, got %d: %s", orgBeforeProject.Code, orgBeforeProject.Body.String())
	}

	staleOrgGateInput := postRaw(t, harness.mux, knowledgePath(projectID, candidate.ID, "promote-org"), `{"scope":"org","org_ref":"default","decision_ref":"org_promote_decision","verifier_ref":"verifier_ref","rationale":"org gate checked","decided_by":"owner_review","claim_check_actionable":0}`)
	if staleOrgGateInput.Code != http.StatusBadRequest || !strings.Contains(staleOrgGateInput.Body.String(), "invalid_json") {
		t.Fatalf("expected stale org gate input rejection, got %d: %s", staleOrgGateInput.Code, staleOrgGateInput.Body.String())
	}

	crossProject := httptest.NewRecorder()
	harness.mux.ServeHTTP(crossProject, httptest.NewRequest(http.MethodGet, "/api/v1/projects/project_2/knowledge/"+candidate.ID, nil))
	if crossProject.Code != http.StatusNotFound || !strings.Contains(crossProject.Body.String(), "not_found") {
		t.Fatalf("expected cross-project lookup 404, got %d: %s", crossProject.Code, crossProject.Body.String())
	}
	assertDoesNotLeak(t, crossProject.Body.String())
}

type harness struct {
	mux        *http.ServeMux
	evidence   *projectevidence.Service
	confidence *projectconfidence.Service
}

func newHarness() harness {
	mux := http.NewServeMux()
	evidenceService := projectevidence.New(evidencestore.NewMemoryStore())
	confidenceService := projectconfidence.New(confidencestore.NewMemoryStore())
	knowledgeService := projectknowledge.New(knowledgestore.NewMemoryStore())
	adapter := projectknowledge.NewPromotionInputAdapter(evidenceService, confidenceService)
	httpapi.RegisterRoutes(mux, knowledgeService, adapter)
	return harness{mux: mux, evidence: evidenceService, confidence: confidenceService}
}

func (h harness) createPromotableClaim(t *testing.T, includeRejected bool) projectevidence.ClaimRecord {
	t.Helper()
	ctx := context.Background()
	claim, err := h.evidence.CreateClaim(ctx, projectevidence.CreateClaimInput{ProjectID: "project_1", RunID: "run_1", TraceID: "trace_1", ClaimRef: "claim/ref_1", Summary: "metadata-only claim summary", Status: projectevidence.ClaimStatusValidated})
	if err != nil {
		t.Fatalf("CreateClaim: %v", err)
	}
	if _, err := h.evidence.AppendEvidence(ctx, projectevidence.AppendEvidenceInput{ProjectID: "project_1", ClaimID: claim.ID, EvidenceRef: "evidence/context_pack", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "context_pack/ref_1"}); err != nil {
		t.Fatalf("AppendEvidence context pack: %v", err)
	}
	if _, err := h.evidence.AppendEvidence(ctx, projectevidence.AppendEvidenceInput{ProjectID: "project_1", ClaimID: claim.ID, EvidenceRef: "evidence/verifier", EvidenceKind: projectevidence.EvidenceKindVerifier, SourceRef: "verifier/ref_1"}); err != nil {
		t.Fatalf("AppendEvidence verifier: %v", err)
	}
	decision, err := h.evidence.CreateDecision(ctx, projectevidence.CreateDecisionInput{ProjectID: "project_1", ClaimID: claim.ID, DecisionRef: "decision/ref_1", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"})
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	if includeRejected {
		if _, err := h.evidence.CreateDecision(ctx, projectevidence.CreateDecisionInput{ProjectID: "project_1", ClaimID: claim.ID, DecisionRef: "decision/rejected", State: projectevidence.DecisionStateRejected, VerifierRef: "verifier_ref", Rationale: "metadata rejected"}); err != nil {
			t.Fatalf("CreateDecision rejected: %v", err)
		}
	}
	action, err := h.evidence.CreateAction(ctx, projectevidence.CreateActionInput{ProjectID: "project_1", ClaimID: claim.ID, DecisionID: decision.ID, ActionRef: "action/ref_1", ActionKind: projectevidence.ActionKindVerifierRun, ChangedFiles: []string{"internal/projectknowledge/httpapi/httpapi.go"}, RunID: "run_1"})
	if err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	outcome, err := h.evidence.CreateOutcome(ctx, projectevidence.CreateOutcomeInput{ProjectID: "project_1", ClaimID: claim.ID, ActionID: action.ID, OutcomeRef: "outcome/ref_1", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier_ref"})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}
	if _, err := h.evidence.LinkArtifact(ctx, projectevidence.LinkArtifactInput{ProjectID: "project_1", ClaimID: claim.ID, ArtifactRef: "artifact/ref_1", ArtifactKind: "knowledge_promotion", RunID: "run_1"}); err != nil {
		t.Fatalf("LinkArtifact: %v", err)
	}
	if _, err := h.evidence.LinkPromotion(ctx, projectevidence.LinkPromotionInput{ProjectID: "project_1", ClaimID: claim.ID, RunID: "run_1", ArtifactRef: "artifact/ref_1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion/source_1", VerifierRef: "verifier_ref", DecisionRef: decision.DecisionRef, ActionRef: action.ActionRef, OutcomeRef: outcome.OutcomeRef}); err != nil {
		t.Fatalf("LinkPromotion: %v", err)
	}
	record, err := h.evidence.GetClaim(ctx, "project_1", claim.ID)
	if err != nil {
		t.Fatalf("GetClaim: %v", err)
	}
	assessment, err := h.confidence.ScoreClaim(ctx, record, readyHealth(), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("ScoreClaim: %v", err)
	}
	if !includeRejected && (assessment.Score < 90 || assessment.Recommendation != projectconfidence.RecommendationPromote) {
		t.Fatalf("unexpected assessment: %#v", assessment)
	}
	return record
}

func candidateBody(claimID string, claimRef string) string {
	return `{"project_id":"body-project","knowledge_ref":"knowledge/ref_1","claim_id":"` + claimID + `","claim_ref":"` + claimRef + `","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse"}`
}

func transitionBody(decisionRef string) string {
	return `{"decision_ref":"` + decisionRef + `","verifier_ref":"verifier_ref","rationale":"metadata gate checked"}`
}

func knowledgePath(projectID string, knowledgeID string, suffix string) string {
	return "/api/v1/projects/" + projectID + "/knowledge/" + knowledgeID + "/" + suffix
}

func postRaw(t *testing.T, mux *http.ServeMux, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	return res
}

func postJSON[T any](t *testing.T, mux *http.ServeMux, path string, body string, status int) T {
	t.Helper()
	res := postRaw(t, mux, path, body)
	if res.Code != status {
		t.Fatalf("expected %d for %s, got %d: %s", status, path, res.Code, res.Body.String())
	}
	assertDoesNotLeak(t, res.Body.String())
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func assertDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, value := range []string{"raw prompt", "raw source", "package main", "Authorization:", "bearer ", "token=", "secret=", "/home/", "root_path", "provider payload"} {
		if value != "" && strings.Contains(body, value) {
			t.Fatalf("response leaked %q: %s", value, body)
		}
	}
}

func readyHealth() projectreliability.ContextHealth {
	now := time.Now().UTC()
	return projectreliability.ContextHealth{ProjectID: "project_1", Status: projectreliability.ContextHealthReady, StatusReason: "metadata_only", LatestRun: &projectreliability.RunSummary{ID: "ingest_1", Status: "completed", LastProgressAt: now.Add(-time.Hour)}, IndexedContentAvailable: true, CheckedAt: now}
}

func verifiedClaims() *projectreliability.ClaimCheckResult {
	return &projectreliability.ClaimCheckResult{ProjectID: "project_1", Summary: projectreliability.ClaimCheckSummary{Total: 2, Verified: 2, Actionable: 0}, AllVerified: true}
}

func cleanImpact() *projectreliability.ImpactAnalysis {
	return &projectreliability.ImpactAnalysis{ProjectID: "project_1"}
}

type knowledgeListResponse struct {
	Knowledge     []projectknowledge.KnowledgeRecord `json:"knowledge"`
	NextPageToken string                             `json:"next_page_token,omitempty"`
}
