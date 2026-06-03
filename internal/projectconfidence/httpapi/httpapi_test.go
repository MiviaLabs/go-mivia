package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence/httpapi"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	evidencehttpapi "github.com/MiviaLabs/go-mivia/internal/projectevidence/httpapi"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

func TestConfidenceRoutesLifecycle(t *testing.T) {
	mux := http.NewServeMux()
	evidenceService := projectevidence.New(evidencestore.NewMemoryStore())
	confidenceService := projectconfidence.New(confidencestore.NewMemoryStore())
	adapter := projectconfidence.NewReliabilityInputAdapter(evidenceService, &fakeHealthProvider{health: readyHealth()}, nil, nil)
	evidencehttpapi.RegisterRoutes(mux, evidenceService)
	httpapi.RegisterRoutes(mux, confidenceService, adapter)
	projectID := "example-service"

	claim := postJSON[projectevidence.Claim](t, mux, "/api/v1/projects/"+projectID+"/evidence-graph/claims", `{"project_id":"body-project","run_id":"agent_run_1","trace_id":"trace_1","claim_ref":"claim/ref","summary":"bounded metadata only"}`, http.StatusCreated)
	postJSON[projectevidence.Evidence](t, mux, evidencePath(projectID, claim.ID, "evidence"), `{"evidence_ref":"context_pack/ref","evidence_kind":"context_pack","source_ref":"source/ref","summary":"bounded context metadata"}`, http.StatusCreated)
	decision := postJSON[projectevidence.Decision](t, mux, evidencePath(projectID, claim.ID, "decisions"), `{"decision_ref":"decision/ref","state":"validated","verifier_ref":"verifier/ref","rationale":"focused verifier passed"}`, http.StatusCreated)
	action := postJSON[projectevidence.Action](t, mux, evidencePath(projectID, claim.ID, "actions"), `{"decision_id":"`+decision.ID+`","action_ref":"action/ref","action_kind":"code_change","summary":"changed safe metadata","changed_files":["internal/projectconfidence/httpapi/httpapi.go"],"run_id":"agent_run_2"}`, http.StatusCreated)
	postJSON[projectevidence.Outcome](t, mux, evidencePath(projectID, claim.ID, "outcomes"), `{"action_id":"`+action.ID+`","outcome_ref":"outcome/ref","outcome_kind":"test","status":"passed","verifier_ref":"verifier/ref","summary":"focused verifier passed"}`, http.StatusCreated)
	postJSON[projectevidence.ArtifactLink](t, mux, evidencePath(projectID, claim.ID, "artifact-links"), `{"artifact_ref":"artifact/finding-a","artifact_kind":"finding","run_id":"agent_run_2"}`, http.StatusCreated)
	postJSON[projectevidence.PromotionLink](t, mux, evidencePath(projectID, claim.ID, "promotion-links"), `{"run_id":"agent_run_2","artifact_ref":"artifact/finding-a","promotion_state":"promoted","source_ref":"agent-run-promotion","verifier_ref":"verifier/ref","decision_ref":"decision/ref","action_ref":"action/ref","outcome_ref":"outcome/ref"}`, http.StatusCreated)

	scored := postJSON[projectconfidence.ScoreClaimResponse](t, mux, confidenceScorePath(projectID, claim.ID), `{}`, http.StatusOK)
	if scored.Assessment.ProjectID != projectID || scored.Assessment.ClaimID != claim.ID || scored.Assessment.ClaimRef != "claim/ref" {
		t.Fatalf("expected path ids and claim metadata in score response, got %#v", scored.Assessment)
	}
	if scored.Assessment.Band != projectconfidence.ScoreBandHigh || scored.Assessment.Recommendation != projectconfidence.RecommendationVerify || len(scored.Assessment.Factors) == 0 {
		t.Fatalf("unexpected assessment: %#v", scored.Assessment)
	}
	assertDoesNotLeak(t, marshalString(t, scored), "raw prompt", "package main", "Authorization:", "bearer ", "token=", "secret=", "/home/", "root_path")

	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/confidence/claims/"+claim.ID, nil))
	if getRes.Code != http.StatusOK || !strings.Contains(getRes.Body.String(), claim.ID) {
		t.Fatalf("expected get assessment, got %d: %s", getRes.Code, getRes.Body.String())
	}
	assertDoesNotLeak(t, getRes.Body.String(), "raw prompt", "package main", "Authorization:", "bearer ", "token=", "secret=", "/home/", "root_path")

	for _, query := range []string{"band=high", "recommendation=verify", "min_score=85&max_score=100", "run_id=agent_run_1", "trace_id=trace_1", "page_size=1"} {
		listRes := httptest.NewRecorder()
		mux.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/confidence/claims?"+query, nil))
		if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), claim.ID) {
			t.Fatalf("expected filtered list for %q, got %d: %s", query, listRes.Code, listRes.Body.String())
		}
	}
}

func TestConfidenceRoutesRejectInvalidJSON(t *testing.T) {
	mux := newConfidenceMux(&fakeEvidenceReader{record: baseRecord()}, &fakeHealthProvider{health: readyHealth()})
	req := httptest.NewRequest(http.MethodPost, confidenceScorePath("project_1", "claim_1"), bytes.NewBufferString(`{"changed_paths":[`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_json") {
		t.Fatalf("expected invalid_json 400, got %d: %s", res.Code, res.Body.String())
	}
}

func TestConfidenceRoutesRejectUnsupportedMediaType(t *testing.T) {
	mux := newConfidenceMux(&fakeEvidenceReader{record: baseRecord()}, &fakeHealthProvider{health: readyHealth()})
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, confidenceScorePath("project_1", "claim_1"), bytes.NewBufferString(`{}`)))
	if res.Code != http.StatusUnsupportedMediaType || !strings.Contains(res.Body.String(), "unsupported_media_type") {
		t.Fatalf("expected unsupported media type, got %d: %s", res.Code, res.Body.String())
	}
}

func TestConfidenceRoutesRejectUnsafeClaimID(t *testing.T) {
	mux := newConfidenceMux(&fakeEvidenceReader{record: baseRecord()}, &fakeHealthProvider{health: readyHealth()})
	res := postRaw(t, mux, confidenceScorePath("project_1", "claim..unsafe"), `{}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_project_confidence_request") {
		t.Fatalf("expected unsafe claim id rejection, got %d: %s", res.Code, res.Body.String())
	}
}

func TestConfidenceRoutesUnknownProjectOrClaimReturnsNotFound(t *testing.T) {
	mux := newConfidenceMux(&fakeEvidenceReader{err: evidencestore.ErrNotFound}, &fakeHealthProvider{health: readyHealth()})
	res := postRaw(t, mux, confidenceScorePath("missing-project", "missing-claim"), `{}`)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "not_found") {
		t.Fatalf("expected unknown project/claim 404, got %d: %s", res.Code, res.Body.String())
	}
	assertDoesNotLeak(t, res.Body.String(), "internal_error", "/home/", "root_path")
}

func TestConfidenceRoutesRejectUnsafeFactorOrSourceMetadata(t *testing.T) {
	record := baseRecord()
	record.Evidence = []projectevidence.Evidence{{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "evidence/ref", EvidenceKind: projectevidence.EvidenceKindFile, SourceRef: "raw prompt", Summary: "bounded metadata only"}}
	mux := newConfidenceMux(&fakeEvidenceReader{record: record}, &fakeHealthProvider{health: readyHealth()})
	res := postRaw(t, mux, confidenceScorePath("project_1", "claim_1"), `{}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_project_confidence_request") {
		t.Fatalf("expected unsafe source metadata rejection, got %d: %s", res.Code, res.Body.String())
	}

	unsafeChangedPath := postRaw(t, mux, confidenceScorePath("project_1", "claim_1"), `{"changed_paths":["../secret.txt"]}`)
	if unsafeChangedPath.Code != http.StatusBadRequest || !strings.Contains(unsafeChangedPath.Body.String(), "invalid_project_confidence_request") {
		t.Fatalf("expected unsafe request metadata rejection, got %d: %s", unsafeChangedPath.Code, unsafeChangedPath.Body.String())
	}
}

func newConfidenceMux(evidence projectconfidence.EvidenceClaimReader, health projectconfidence.ContextHealthProvider) *http.ServeMux {
	mux := http.NewServeMux()
	confidenceService := projectconfidence.New(confidencestore.NewMemoryStore())
	adapter := projectconfidence.NewReliabilityInputAdapter(evidence, health, nil, nil)
	httpapi.RegisterRoutes(mux, confidenceService, adapter)
	return mux
}

func baseRecord() projectevidence.ClaimRecord {
	return projectevidence.ClaimRecord{Claim: projectevidence.Claim{ID: "claim_1", ProjectID: "project_1", RunID: "agent_run_1", TraceID: "trace_1", ClaimRef: "claim/ref", Summary: "bounded metadata only", Status: projectevidence.ClaimStatusCandidate}}
}

func readyHealth() projectreliability.ContextHealth {
	now := time.Now().UTC()
	return projectreliability.ContextHealth{
		Status:    projectreliability.ContextHealthReady,
		LatestRun: &projectreliability.RunSummary{ID: "ingest_1", Status: "completed", LastProgressAt: now.Add(-time.Minute)},
	}
}

type fakeEvidenceReader struct {
	record projectevidence.ClaimRecord
	err    error
}

func (fake *fakeEvidenceReader) GetClaim(_ context.Context, projectID string, claimID string) (projectevidence.ClaimRecord, error) {
	if fake.err != nil {
		return projectevidence.ClaimRecord{}, fake.err
	}
	record := fake.record
	if record.Claim.ProjectID == "" {
		record.Claim.ProjectID = projectID
	}
	if record.Claim.ID == "" {
		record.Claim.ID = claimID
	}
	return record, nil
}

type fakeHealthProvider struct {
	health projectreliability.ContextHealth
	err    error
}

func (fake *fakeHealthProvider) ContextHealth(_ context.Context, projectID string) (projectreliability.ContextHealth, error) {
	if fake.err != nil {
		return projectreliability.ContextHealth{}, fake.err
	}
	health := fake.health
	if health.ProjectID == "" {
		health.ProjectID = projectID
	}
	return health, nil
}

func evidencePath(projectID string, claimID string, suffix string) string {
	return "/api/v1/projects/" + projectID + "/evidence-graph/claims/" + claimID + "/" + suffix
}

func confidenceScorePath(projectID string, claimID string) string {
	return "/api/v1/projects/" + projectID + "/confidence/claims/" + claimID + "/score"
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
	assertDoesNotLeak(t, res.Body.String(), "raw prompt", "package main", "Authorization:", "bearer ", "token=", "secret=", "/home/", "root_path")
	var out T
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func marshalString(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

func assertDoesNotLeak(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(body, value) {
			t.Fatalf("response leaked %q: %s", value, body)
		}
	}
}

func TestConfidenceRoutesInternalErrorsDoNotLeak(t *testing.T) {
	mux := newConfidenceMux(&fakeEvidenceReader{err: errors.New("raw prompt /home/mac secret=bad")}, &fakeHealthProvider{health: readyHealth()})
	res := postRaw(t, mux, confidenceScorePath("project_1", "claim_1"), `{}`)
	if res.Code != http.StatusInternalServerError || strings.Contains(res.Body.String(), "/home/") || strings.Contains(res.Body.String(), "secret=") {
		t.Fatalf("expected redacted internal error, got %d: %s", res.Code, res.Body.String())
	}
}
