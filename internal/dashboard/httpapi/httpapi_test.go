package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutes_RootRedirectsToDashboard(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))

	if res.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", res.Code)
	}
	if got := res.Header().Get("Location"); got != "/dashboard" {
		t.Fatalf("expected /dashboard redirect, got %q", got)
	}
}

func TestRoutes_DashboardServesEmbeddedAssets(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	index := httptest.NewRecorder()
	mux.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if index.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", index.Code, index.Body.String())
	}
	body := index.Body.String()
	for _, want := range []string{"Mivia Dashboard", "./app.js", "./styles.css"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected index to contain %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"root_path", "content_sha256", "api_token", "/home/", "\\\\wsl.localhost"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("dashboard index leaked forbidden marker %q", forbidden)
		}
	}

	app := httptest.NewRecorder()
	mux.ServeHTTP(app, httptest.NewRequest(http.MethodGet, "/dashboard/app.js", nil))
	if app.Code != http.StatusOK {
		t.Fatalf("expected app asset 200, got %d", app.Code)
	}
	if !strings.Contains(app.Body.String(), "/api/v1/projects") {
		t.Fatalf("expected app to fetch project metadata")
	}
	for _, want := range []string{"contextHealthPill", "projectValidationPill", "project enabled", "config ${status}", "latest run ${status}", "EventSource", "agent-activity/stream", "Call summary", "Full payload", "visibleActivityRows", "policy_category", "grouped_event_count", "Context-pack manifest", "manifest only", "Manifest details"} {
		if !strings.Contains(app.Body.String(), want) {
			t.Fatalf("expected dashboard app to contain explicit status label %q", want)
		}
	}
	for _, want := range []string{"Indexed integration items", "Recent Jira issues", "Confluence pages", "Search indexed Jira and Confluence", "/integrations/search", "jira/issues", "confluence/pages", "openIntegrationDrawer", "integration-detail", "integrationItemTitle", "Load more", "next_page_token"} {
		if !strings.Contains(app.Body.String(), want) {
			t.Fatalf("expected dashboard app to contain integration browser/search affordance %q", want)
		}
	}
	for _, want := range []string{"Evidence Graph", "tabEvidenceGraph(project.id)", "projectSubview = \"evidence-graph\"", "/evidence-graph/claims?", "page_size", "page_token", "artifact_ref", "promotion_state", "outcome_status", "run_id", "trace_id", "loadEvidenceClaimByID", "/evidence-graph/claims/${encodeURIComponent(claimID)}", "Claim", "Evidence", "Decision", "Action", "Outcome", "artifact_links", "promotion_links", "raw prompt", "package main"} {
		if !strings.Contains(app.Body.String(), want) {
			t.Fatalf("expected dashboard app to contain project evidence affordance %q", want)
		}
	}
	for _, want := range []string{"Confidence", "tabConfidence(project.id)", "projectSubview = \"confidence\"", "/confidence/claims?", "/confidence/claims/${encodeURIComponent(claimID)}", "/score", "band", "min_score", "max_score", "recommendation", "Score claim", "Score explanation", "confidenceAssessmentDetail", "confidenceFactorRow", "Open Evidence Graph claim"} {
		if !strings.Contains(app.Body.String(), want) {
			t.Fatalf("expected dashboard app to contain project confidence affordance %q", want)
		}
	}
	for _, want := range []string{
		"Knowledge Promotion", "tabKnowledgePromotion(project.id)", "projectSubview = \"knowledge-promotion\"",
		"/knowledge?", "/api/v1/orgs/default/knowledge?", "/knowledge/${encodeURIComponent(knowledgeID)}", "/reuse-events",
		"Project-level knowledge", "Org-level knowledge", "Org promotion requires explicit review and is never automatic.",
		"scope", "state", "claim_id", "knowledge_ref", "confidence_band", "min_confidence", "max_confidence",
		"safe promotion metadata", "Reuse guidance", "Evidence refs", "Verifier refs", "Outcome refs",
		"Promotion decisions", "Supersession state", "Reuse events", "Record reuse",
	} {
		if !strings.Contains(app.Body.String(), want) {
			t.Fatalf("expected dashboard app to contain knowledge promotion affordance %q", want)
		}
	}
	styles := httptest.NewRecorder()
	mux.ServeHTTP(styles, httptest.NewRequest(http.MethodGet, "/dashboard/styles.css", nil))
	if styles.Code != http.StatusOK {
		t.Fatalf("expected styles asset 200, got %d", styles.Code)
	}
	for _, want := range []string{"knowledge-layout", "knowledge-filters", "knowledge-row--project", "knowledge-row--org", "scope-pill--project", "scope-pill--org"} {
		if !strings.Contains(styles.Body.String(), want) {
			t.Fatalf("expected dashboard styles to contain knowledge promotion affordance %q", want)
		}
	}
	for _, forbidden := range []string{"Evidence Graph\" },\n  { id: \"overview\"", "raw graph data", "source bodies", "provider payloads"} {
		if strings.Contains(app.Body.String(), forbidden) {
			t.Fatalf("dashboard app contained forbidden evidence marker %q", forbidden)
		}
	}
}

func TestRoutes_UnknownRootSubpathNotFound(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.Code)
	}
}

func TestRoutes_CanCoexistWithMethodlessMCPRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected MCP route to remain reachable, got %d", res.Code)
	}
}
