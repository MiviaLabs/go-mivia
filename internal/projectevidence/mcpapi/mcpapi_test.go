package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
)

func TestToolDefinitionsExposeEvidenceGraphTools(t *testing.T) {
	encoded, err := json.Marshal(ToolDefinitions())
	if err != nil {
		t.Fatalf("marshal definitions: %v", err)
	}
	for _, name := range evidenceGraphTools {
		if !bytes.Contains(encoded, []byte(`"name":"`+name+`"`)) {
			t.Fatalf("missing tool %s in %s", name, string(encoded))
		}
		if !IsEvidenceGraphTool(name) || !IsEvidenceGraphTool(dottedToUnderscore(name)) {
			t.Fatalf("expected dotted and underscore aliases for %s", name)
		}
	}
	if !bytes.Contains(encoded, []byte(`"additionalProperties":false`)) {
		t.Fatalf("expected strict schemas, got %s", string(encoded))
	}
}

func TestCallToolLifecycleReturnsStructuredContentAndJSONText(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	ctx := context.Background()
	claim := call(t, ctx, svc, "projects.evidence_graph.claims.create", `{"id":"example-service","claim_ref":"claim/ref","summary":"bounded claim metadata","run_id":"run-1","trace_id":"trace-1"}`)
	claimID := structuredString(t, claim, "id")
	evidence := call(t, ctx, svc, "projects.evidence_graph.evidence.append", `{"id":"example-service","claim_id":"`+claimID+`","evidence_ref":"context-pack-1","evidence_kind":"context_pack","source_ref":"pack-1","summary":"bounded evidence metadata"}`)
	decision := call(t, ctx, svc, "projects.evidence_graph.decisions.create", `{"id":"example-service","claim_id":"`+claimID+`","decision_ref":"decision-1","state":"validated","verifier_ref":"claim-check-1","rationale":"evidence supports the claim"}`)
	decisionID := structuredString(t, decision, "id")
	action := call(t, ctx, svc, "projects.evidence_graph.actions.create", `{"id":"example-service","claim_id":"`+claimID+`","decision_id":"`+decisionID+`","action_ref":"action-1","action_kind":"code_change","summary":"updated metadata surface","changed_files":["internal/projectevidence/mcpapi/mcpapi.go"],"run_id":"run-1"}`)
	actionID := structuredString(t, action, "id")
	outcome := call(t, ctx, svc, "projects.evidence_graph.outcomes.create", `{"id":"example-service","claim_id":"`+claimID+`","action_id":"`+actionID+`","outcome_ref":"outcome-1","outcome_kind":"claim_check","status":"passed","verifier_ref":"claims-check","summary":"claim check passed"}`)
	artifact := call(t, ctx, svc, "projects.evidence_graph.artifacts.link", `{"id":"example-service","claim_id":"`+claimID+`","artifact_ref":"artifact-1","artifact_kind":"handoff","run_id":"run-1"}`)
	promotion := call(t, ctx, svc, "projects.evidence_graph.promotions.link", `{"id":"example-service","claim_id":"`+claimID+`","run_id":"run-1","artifact_ref":"artifact-1","promotion_state":"promoted","source_ref":"agent-run-promotion","verifier_ref":"claims-check","decision_ref":"decision-1","action_ref":"action-1","outcome_ref":"outcome-1"}`)
	record := call(t, ctx, svc, "projects.evidence_graph.claims.get", `{"id":"example-service","claim_id":"`+claimID+`"}`)

	for _, result := range []map[string]any{claim, evidence, decision, action, outcome, artifact, promotion, record} {
		if result["structuredContent"] == nil {
			t.Fatalf("missing structuredContent in %#v", result)
		}
		content := result["content"].([]map[string]string)
		if len(content) != 1 || content[0]["type"] != "text" || !json.Valid([]byte(content[0]["text"])) {
			t.Fatalf("expected JSON text content, got %#v", result["content"])
		}
		for _, forbidden := range []string{"raw prompt", "package main", "provider payload", "/home/mac", "token=secret"} {
			if bytes.Contains([]byte(content[0]["text"]), []byte(forbidden)) {
				t.Fatalf("response leaked forbidden marker %q: %s", forbidden, content[0]["text"])
			}
		}
	}
	if structuredString(t, evidence, "claim_id") != claimID || structuredString(t, outcome, "action_id") != actionID {
		t.Fatalf("lifecycle did not preserve chain ids")
	}
	if structuredString(t, promotion, "run_id") != "run-1" || structuredString(t, promotion, "action_ref") != "action-1" || structuredString(t, promotion, "outcome_ref") != "outcome-1" {
		t.Fatalf("promotion did not preserve Phase 5 refs: %#v", promotion)
	}
}

func TestCallToolAllowsUnderscoreAliases(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	result := call(t, context.Background(), svc, "projects_evidence_graph_claims_create", `{"id":"example-service","claim_ref":"claim/ref","summary":"bounded claim metadata"}`)
	if structuredString(t, result, "id") == "" {
		t.Fatalf("expected claim id, got %#v", result)
	}
}

func TestCallToolListClaimsIsPaginated(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	ctx := context.Background()
	for _, ref := range []string{"claim/one", "claim/two", "claim/three"} {
		call(t, ctx, svc, "projects.evidence_graph.claims.create", `{"id":"example-service","claim_ref":"`+ref+`","summary":"bounded claim metadata"}`)
	}

	first := call(t, ctx, svc, "projects.evidence_graph.claims.list", `{"id":"example-service","page_size":2}`)
	firstList := claimList(t, first)
	if len(firstList.Claims) != 2 || firstList.NextPageToken == "" {
		t.Fatalf("expected first page with next token, got %#v", firstList)
	}
	second := call(t, ctx, svc, "projects.evidence_graph.claims.list", `{"id":"example-service","page_size":2,"page_token":"`+firstList.NextPageToken+`"}`)
	secondList := claimList(t, second)
	if len(secondList.Claims) != 1 || secondList.NextPageToken != "" {
		t.Fatalf("expected final page, got %#v", secondList)
	}
}

func TestCallToolListClaimsRejectsInvalidPagination(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	if _, err := CallTool(context.Background(), svc, "projects.evidence_graph.claims.list", json.RawMessage(`{"id":"example-service","page_size":101}`)); err == nil {
		t.Fatal("expected invalid page_size rejection")
	}
	if _, err := CallTool(context.Background(), svc, "projects.evidence_graph.claims.list", json.RawMessage(`{"id":"example-service","page_token":"not-a-page"}`)); err == nil {
		t.Fatal("expected invalid page_token rejection")
	}
}

func TestCallToolRejectsUnknownFields(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	if _, err := CallTool(context.Background(), svc, "projects.evidence_graph.claims.create", json.RawMessage(`{"id":"example-service","claim_ref":"claim/ref","summary":"bounded claim metadata","query":"MATCH (n)"}`)); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}

func TestCallToolRejectsUnsafePayload(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	if _, err := CallTool(context.Background(), svc, "projects.evidence_graph.claims.create", json.RawMessage(`{"id":"example-service","claim_ref":"claim/ref","summary":"raw prompt: token=secret"}`)); err == nil {
		t.Fatal("expected unsafe payload rejection")
	}
	if _, err := CallTool(context.Background(), svc, "projects.evidence_graph.claims.create", json.RawMessage(`{"id":"wsl.localhost/Ubuntu/home/mac/project","claim_ref":"claim/ref","summary":"bounded claim metadata"}`)); err == nil {
		t.Fatal("expected root-shaped ref rejection")
	}
}

func TestCallToolRejectsMismatchedPromotionChain(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	ctx := context.Background()
	claim := call(t, ctx, svc, "projects.evidence_graph.claims.create", `{"id":"example-service","claim_ref":"claim/ref","summary":"bounded claim metadata"}`)
	claimID := structuredString(t, claim, "id")
	call(t, ctx, svc, "projects.evidence_graph.evidence.append", `{"id":"example-service","claim_id":"`+claimID+`","evidence_ref":"context-pack-1","evidence_kind":"context_pack"}`)
	firstDecision := call(t, ctx, svc, "projects.evidence_graph.decisions.create", `{"id":"example-service","claim_id":"`+claimID+`","decision_ref":"decision-1","state":"validated","verifier_ref":"claim-check-1","rationale":"evidence supports the claim"}`)
	secondDecision := call(t, ctx, svc, "projects.evidence_graph.decisions.create", `{"id":"example-service","claim_id":"`+claimID+`","decision_ref":"decision-2","state":"validated","verifier_ref":"claim-check-2","rationale":"evidence supports the claim"}`)
	action := call(t, ctx, svc, "projects.evidence_graph.actions.create", `{"id":"example-service","claim_id":"`+claimID+`","decision_id":"`+structuredString(t, firstDecision, "id")+`","action_ref":"action-1","action_kind":"code_change"}`)
	outcome := call(t, ctx, svc, "projects.evidence_graph.outcomes.create", `{"id":"example-service","claim_id":"`+claimID+`","action_id":"`+structuredString(t, action, "id")+`","outcome_ref":"outcome-1","outcome_kind":"claim_check","status":"passed"}`)
	call(t, ctx, svc, "projects.evidence_graph.artifacts.link", `{"id":"example-service","claim_id":"`+claimID+`","artifact_ref":"artifact-1"}`)

	body := `{"id":"example-service","claim_id":"` + claimID + `","artifact_ref":"artifact-1","promotion_state":"promoted","source_ref":"agent-run-promotion","verifier_ref":"claim-check","decision_ref":"` + structuredString(t, secondDecision, "decision_ref") + `","action_ref":"action-1","outcome_ref":"` + structuredString(t, outcome, "outcome_ref") + `"}`
	if _, err := CallTool(ctx, svc, "projects.evidence_graph.promotions.link", json.RawMessage(body)); err == nil {
		t.Fatal("expected mismatched promotion chain rejection")
	}
}

func call(t *testing.T, ctx context.Context, svc *projectevidence.Service, name string, body string) map[string]any {
	t.Helper()
	result, err := CallTool(ctx, svc, name, json.RawMessage(body))
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func structuredString(t *testing.T, result map[string]any, field string) string {
	t.Helper()
	encoded, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	value, _ := object[field].(string)
	return value
}

func claimList(t *testing.T, result map[string]any) ClaimList {
	t.Helper()
	encoded, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var list ClaimList
	if err := json.Unmarshal(encoded, &list); err != nil {
		t.Fatalf("decode claim list: %v", err)
	}
	return list
}

func dottedToUnderscore(value string) string {
	return strings.ReplaceAll(value, ".", "_")
}
