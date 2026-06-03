package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestEvidenceGraphRoutesLifecycle(t *testing.T) {
	mux := newMux()
	projectID := "example-service"

	claim := postJSON[projectevidence.Claim](t, mux, "/api/v1/projects/"+projectID+"/evidence-graph/claims", `{"project_id":"body-project","run_id":"agent_run_1","trace_id":"trace_1","claim_ref":"claim/ref","summary":"bounded metadata only"}`, http.StatusCreated)
	if claim.ProjectID != projectID || claim.RunID != "agent_run_1" || claim.TraceID != "trace_1" {
		t.Fatalf("expected path project id to win, got %#v", claim)
	}
	evidence := postJSON[projectevidence.Evidence](t, mux, claimPath(projectID, claim.ID, "evidence"), `{"claim_id":"body-claim","evidence_ref":"context_pack/ref","evidence_kind":"context_pack","source_ref":"source/ref","summary":"bounded context metadata"}`, http.StatusCreated)
	if evidence.ProjectID != projectID || evidence.ClaimID != claim.ID {
		t.Fatalf("expected path ids on evidence, got %#v", evidence)
	}
	decision := postJSON[projectevidence.Decision](t, mux, claimPath(projectID, claim.ID, "decisions"), `{"decision_ref":"decision/ref","state":"validated","verifier_ref":"verifier/ref","rationale":"focused verifier passed"}`, http.StatusCreated)
	action := postJSON[projectevidence.Action](t, mux, claimPath(projectID, claim.ID, "actions"), `{"decision_id":"`+decision.ID+`","action_ref":"action/ref","action_kind":"code_change","summary":"changed safe metadata","changed_files":["internal/projectevidence/httpapi/httpapi.go"],"run_id":"agent_run_2"}`, http.StatusCreated)
	outcome := postJSON[projectevidence.Outcome](t, mux, claimPath(projectID, claim.ID, "outcomes"), `{"action_id":"`+action.ID+`","outcome_ref":"outcome/ref","outcome_kind":"test","status":"passed","verifier_ref":"verifier/ref","summary":"focused verifier passed"}`, http.StatusCreated)
	if outcome.Status != projectevidence.OutcomeStatusPassed {
		t.Fatalf("expected passed outcome, got %#v", outcome)
	}
	artifact := postJSON[projectevidence.ArtifactLink](t, mux, claimPath(projectID, claim.ID, "artifact-links"), `{"artifact_ref":"artifact/finding-a","artifact_kind":"finding","run_id":"agent_run_2"}`, http.StatusCreated)
	if artifact.ArtifactRef != "artifact/finding-a" {
		t.Fatalf("unexpected artifact link: %#v", artifact)
	}
	promotion := postJSON[projectevidence.PromotionLink](t, mux, claimPath(projectID, claim.ID, "promotion-links"), `{"run_id":"agent_run_2","artifact_ref":"artifact/finding-a","promotion_state":"promoted","source_ref":"agent-run-promotion","verifier_ref":"verifier/ref","decision_ref":"decision/ref","action_ref":"action/ref","outcome_ref":"outcome/ref"}`, http.StatusCreated)
	if promotion.PromotionState != projectevidence.PromotionStatePromoted || promotion.RunID != "agent_run_2" || promotion.ActionRef != "action/ref" || promotion.OutcomeRef != "outcome/ref" {
		t.Fatalf("unexpected promotion link: %#v", promotion)
	}

	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/evidence-graph/claims/"+claim.ID, nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}
	assertDoesNotLeak(t, getRes.Body.String(), "raw prompt", "package main", "Authorization:", "token=", "/home/", "root_path")
	var record projectevidence.ClaimRecord
	if err := json.Unmarshal(getRes.Body.Bytes(), &record); err != nil {
		t.Fatalf("decode claim record: %v", err)
	}
	if record.Claim.ID != claim.ID || len(record.Evidence) != 1 || len(record.Decisions) != 1 || len(record.Actions) != 1 || len(record.Outcomes) != 1 || len(record.ArtifactLinks) != 1 || len(record.PromotionLinks) != 1 {
		t.Fatalf("expected complete claim chain, got %#v", record)
	}

	for _, query := range []string{"artifact_ref=artifact/finding-a", "promotion_state=promoted", "outcome_status=passed", "run_id=agent_run_1", "trace_id=trace_1"} {
		listRes := httptest.NewRecorder()
		mux.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/evidence-graph/claims?"+query, nil))
		if listRes.Code != http.StatusOK || !strings.Contains(listRes.Body.String(), claim.ID) {
			t.Fatalf("expected filtered list for %q, got %d: %s", query, listRes.Code, listRes.Body.String())
		}
	}
}

func TestEvidenceGraphRoutesRejectInvalidJSON(t *testing.T) {
	mux := newMux()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/evidence-graph/claims", bytes.NewBufferString(`{"claim_ref":"claim/ref"`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_json") {
		t.Fatalf("expected invalid_json 400, got %d: %s", res.Code, res.Body.String())
	}
}

func TestEvidenceGraphRoutesRejectUnsupportedMediaType(t *testing.T) {
	mux := newMux()
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/evidence-graph/claims", bytes.NewBufferString(`{"claim_ref":"claim/ref","summary":"bounded metadata"}`)))
	if res.Code != http.StatusUnsupportedMediaType || !strings.Contains(res.Body.String(), "unsupported_media_type") {
		t.Fatalf("expected unsupported media type, got %d: %s", res.Code, res.Body.String())
	}
}

func TestEvidenceGraphRoutesListClaimsIsPaginated(t *testing.T) {
	mux := newMux()
	projectID := "example-service"
	for _, ref := range []string{"claim/one", "claim/two", "claim/three"} {
		postJSON[projectevidence.Claim](t, mux, "/api/v1/projects/"+projectID+"/evidence-graph/claims", `{"claim_ref":"`+ref+`","summary":"bounded metadata only"}`, http.StatusCreated)
	}

	firstRes := httptest.NewRecorder()
	mux.ServeHTTP(firstRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/evidence-graph/claims?page_size=2", nil))
	if firstRes.Code != http.StatusOK {
		t.Fatalf("expected first page 200, got %d: %s", firstRes.Code, firstRes.Body.String())
	}
	var first claimListResponse
	if err := json.Unmarshal(firstRes.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Claims) != 2 || first.NextPageToken == "" {
		t.Fatalf("expected first page with next token, got %#v", first)
	}

	secondRes := httptest.NewRecorder()
	mux.ServeHTTP(secondRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/evidence-graph/claims?page_size=2&page_token="+first.NextPageToken, nil))
	if secondRes.Code != http.StatusOK {
		t.Fatalf("expected second page 200, got %d: %s", secondRes.Code, secondRes.Body.String())
	}
	var second claimListResponse
	if err := json.Unmarshal(secondRes.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(second.Claims) != 1 || second.NextPageToken != "" {
		t.Fatalf("expected final page, got %#v", second)
	}
}

func TestEvidenceGraphRoutesListClaimsRejectsInvalidPagination(t *testing.T) {
	mux := newMux()
	for _, query := range []string{"page_size=0", "page_size=101", "page_token=not-a-page", "page_token=-1"} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/evidence-graph/claims?"+query, nil))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_project_evidence_request") {
			t.Fatalf("expected invalid pagination rejection for %q, got %d: %s", query, res.Code, res.Body.String())
		}
	}
}

func TestEvidenceGraphRoutesRejectMissingOrUnsafeProjectIDAndPayload(t *testing.T) {
	mux := newMux()

	missingProject := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/%20/evidence-graph/claims", bytes.NewBufferString(`{"claim_ref":"claim/ref","summary":"bounded metadata"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(missingProject, req)
	if missingProject.Code != http.StatusBadRequest {
		t.Fatalf("expected missing project id rejection, got %d: %s", missingProject.Code, missingProject.Body.String())
	}

	missingPayload := postRaw(t, mux, "/api/v1/projects/example-service/evidence-graph/claims", `{"claim_ref":"claim/ref"}`)
	if missingPayload.Code != http.StatusBadRequest {
		t.Fatalf("expected missing payload rejection, got %d: %s", missingPayload.Code, missingPayload.Body.String())
	}

	unsafePayload := postRaw(t, mux, "/api/v1/projects/example-service/evidence-graph/claims", `{"claim_ref":"claim/ref","summary":"raw prompt: use token=secret"}`)
	if unsafePayload.Code != http.StatusBadRequest || !strings.Contains(unsafePayload.Body.String(), "invalid_project_evidence_request") {
		t.Fatalf("expected unsafe payload rejection, got %d: %s", unsafePayload.Code, unsafePayload.Body.String())
	}

	unsafePath := postRaw(t, mux, "/api/v1/projects/example-service/evidence-graph/claims", `{"claim_ref":"../claim","summary":"bounded metadata"}`)
	if unsafePath.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe ref rejection, got %d: %s", unsafePath.Code, unsafePath.Body.String())
	}

	unsafeRootRef := postRaw(t, mux, "/api/v1/projects/example-service/evidence-graph/claims", `{"claim_ref":"wsl.localhost/Ubuntu/home/mac/project","summary":"bounded metadata"}`)
	if unsafeRootRef.Code != http.StatusBadRequest {
		t.Fatalf("expected root-shaped ref rejection, got %d: %s", unsafeRootRef.Code, unsafeRootRef.Body.String())
	}
}

func TestEvidenceGraphRoutesRejectMismatchedPromotionChain(t *testing.T) {
	mux := newMux()
	projectID := "example-service"
	claim := postJSON[projectevidence.Claim](t, mux, "/api/v1/projects/"+projectID+"/evidence-graph/claims", `{"claim_ref":"claim/ref","summary":"bounded metadata only"}`, http.StatusCreated)
	postJSON[projectevidence.Evidence](t, mux, claimPath(projectID, claim.ID, "evidence"), `{"evidence_ref":"context_pack/ref","evidence_kind":"context_pack"}`, http.StatusCreated)
	firstDecision := postJSON[projectevidence.Decision](t, mux, claimPath(projectID, claim.ID, "decisions"), `{"decision_ref":"decision/ref","state":"validated","verifier_ref":"verifier/ref","rationale":"focused verifier passed"}`, http.StatusCreated)
	secondDecision := postJSON[projectevidence.Decision](t, mux, claimPath(projectID, claim.ID, "decisions"), `{"decision_ref":"decision/second","state":"validated","verifier_ref":"verifier/ref","rationale":"focused verifier passed"}`, http.StatusCreated)
	action := postJSON[projectevidence.Action](t, mux, claimPath(projectID, claim.ID, "actions"), `{"decision_id":"`+firstDecision.ID+`","action_ref":"action/ref","action_kind":"code_change"}`, http.StatusCreated)
	postJSON[projectevidence.Outcome](t, mux, claimPath(projectID, claim.ID, "outcomes"), `{"action_id":"`+action.ID+`","outcome_ref":"outcome/ref","outcome_kind":"test","status":"passed"}`, http.StatusCreated)
	postJSON[projectevidence.ArtifactLink](t, mux, claimPath(projectID, claim.ID, "artifact-links"), `{"artifact_ref":"artifact/finding-a"}`, http.StatusCreated)

	res := postRaw(t, mux, claimPath(projectID, claim.ID, "promotion-links"), `{"artifact_ref":"artifact/finding-a","promotion_state":"promoted","source_ref":"agent-run-promotion","verifier_ref":"verifier/ref","decision_ref":"`+secondDecision.DecisionRef+`","action_ref":"action/ref","outcome_ref":"outcome/ref"}`)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_project_evidence_request") {
		t.Fatalf("expected mismatched promotion chain rejection, got %d: %s", res.Code, res.Body.String())
	}
}

func TestEvidenceGraphRoutesUnknownProjectReturnsNotFound(t *testing.T) {
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "example-service",
		DisplayName:    "Example Service",
		RootPath:       t.TempDir(),
		Enabled:        true,
		GraphNamespace: "example-service",
		GraphStorage:   projectregistry.GraphStorageInMemory,
		DigestMode:     projectregistry.DigestModeContentGraph,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  filepath.Join(t.TempDir(), "graph.lbug"),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	router := projectregistry.NewProjectScopedGraphRouter(registry, graph, nil)
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, projectevidence.New(store.NewLadybugStore(router)))

	res := postRaw(t, mux, "/api/v1/projects/missing-project/evidence-graph/claims", `{"claim_ref":"claim/ref","summary":"bounded metadata"}`)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), "not_found") {
		t.Fatalf("expected unknown project 404, got %d: %s", res.Code, res.Body.String())
	}
	assertDoesNotLeak(t, res.Body.String(), "internal_error", "/home/", "root_path")
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, projectevidence.New(store.NewMemoryStore()))
	return mux
}

func claimPath(projectID string, claimID string, suffix string) string {
	return "/api/v1/projects/" + projectID + "/evidence-graph/claims/" + claimID + "/" + suffix
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

func assertDoesNotLeak(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(body, value) {
			t.Fatalf("response leaked %q: %s", value, body)
		}
	}
}

type claimListResponse struct {
	Claims        []projectevidence.Claim `json:"claims"`
	NextPageToken string                  `json:"next_page_token,omitempty"`
}
