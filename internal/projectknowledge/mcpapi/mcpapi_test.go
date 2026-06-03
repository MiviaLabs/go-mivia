package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
)

var testNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestToolDefinitionsExposeKnowledgeToolsWithStrictSchemas(t *testing.T) {
	encoded, err := json.Marshal(ToolDefinitions())
	if err != nil {
		t.Fatalf("marshal definitions: %v", err)
	}
	for _, name := range knowledgeTools {
		if !bytes.Contains(encoded, []byte(`"name":"`+name+`"`)) {
			t.Fatalf("missing tool %s in %s", name, string(encoded))
		}
		if !IsKnowledgeTool(name) || !IsKnowledgeTool(strings.ReplaceAll(name, ".", "_")) {
			t.Fatalf("expected dotted and underscore aliases for %s", name)
		}
	}
	if !bytes.Contains(encoded, []byte(`"additionalProperties":false`)) {
		t.Fatalf("expected strict schemas, got %s", string(encoded))
	}
	for _, name := range []string{
		"projects.knowledge.validate",
		"projects.knowledge.promote_project",
		"projects.knowledge.submit_org_review",
		"projects.knowledge.promote_org",
		"projects.knowledge.reject",
		"projects.knowledge.supersede",
	} {
		if got := rationaleMaxLength(t, name); got != 1000 {
			t.Fatalf("expected %s rationale maxLength 1000, got %d", name, got)
		}
	}
}

func TestCallToolLifecycleReturnsStructuredContentAndJSONText(t *testing.T) {
	ctx := context.Background()
	svc := projectknowledge.New(store.NewMemoryStore())
	adapter := projectknowledge.NewPromotionInputAdapter(fakeEvidenceReader{claim: highClaim()}, fakeConfidenceReader{assessment: highConfidence()})

	candidate := call(t, ctx, svc, adapter, "projects.knowledge.candidates.create", `{"id":"project_1","knowledge_ref":"knowledge/ref_1","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse","evidence_refs":["evidence/context_pack"],"verifier_refs":["verifier/ref_1"]}`)
	knowledgeID := structuredString(t, candidate, "id")
	validated := call(t, ctx, svc, adapter, "projects.knowledge.validate", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","decision_ref":"knowledge_validated","verifier_ref":"verifier_ref","rationale":"metadata gate passed"}`)
	promoted := call(t, ctx, svc, adapter, "projects.knowledge.promote_project", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","decision_ref":"knowledge_project_promoted","verifier_ref":"verifier_ref","rationale":"project gate passed"}`)
	review := call(t, ctx, svc, adapter, "projects.knowledge.submit_org_review", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","org_ref":"default","decision_ref":"org_review","verifier_ref":"verifier_ref","rationale":"org review requested","decided_by":"owner_review"}`)
	orgPromoted := call(t, ctx, svc, adapter, "projects.knowledge.promote_org", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","scope":"org","org_ref":"default","decision_ref":"org_promote_decision","verifier_ref":"verifier_ref","rationale":"org gate passed","decided_by":"owner_review"}`)
	reuse := call(t, ctx, svc, adapter, "projects.knowledge.reuse_events.record", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","reuse_ref":"reuse/ref_1","revalidated":true,"revalidation_ref":"verifier/ref_2","outcome":"used","summary":"metadata reuse event"}`)
	got := call(t, ctx, svc, adapter, "projects.knowledge.get", `{"id":"project_1","knowledge_id":"`+knowledgeID+`"}`)
	list := call(t, ctx, svc, adapter, "projects.knowledge.list", `{"id":"project_1","page_size":1}`)
	orgList := call(t, ctx, svc, adapter, "orgs.knowledge.list", `{"org_ref":"default","page_size":1}`)

	for _, result := range []map[string]any{candidate, validated, promoted, review, orgPromoted, reuse, got, list, orgList} {
		assertToolResult(t, result)
	}
	if structuredString(t, orgPromoted, "state") != projectknowledge.StateOrgPromoted || structuredString(t, orgPromoted, "scope") != projectknowledge.ScopeOrg {
		t.Fatalf("unexpected org promotion: %#v", orgPromoted)
	}
}

func TestPromoteOrgUsesTrustedConfidenceClaimCheckActionable(t *testing.T) {
	ctx := context.Background()
	svc := projectknowledge.New(store.NewMemoryStore())
	confidence := highConfidence()
	confidence.Inputs.ClaimCheckActionable = 1
	adapter := projectknowledge.NewPromotionInputAdapter(fakeEvidenceReader{claim: highClaim()}, fakeConfidenceReader{assessment: confidence})

	candidate := call(t, ctx, svc, adapter, "projects.knowledge.candidates.create", `{"id":"project_1","knowledge_ref":"knowledge/ref_1","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse"}`)
	knowledgeID := structuredString(t, candidate, "id")
	call(t, ctx, svc, adapter, "projects.knowledge.validate", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","decision_ref":"knowledge_validated","verifier_ref":"verifier_ref","rationale":"metadata gate passed"}`)
	call(t, ctx, svc, adapter, "projects.knowledge.promote_project", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","decision_ref":"knowledge_project_promoted","verifier_ref":"verifier_ref","rationale":"project gate passed"}`)
	call(t, ctx, svc, adapter, "projects.knowledge.submit_org_review", `{"id":"project_1","knowledge_id":"`+knowledgeID+`","org_ref":"default","decision_ref":"org_review","verifier_ref":"verifier_ref","rationale":"org review requested","decided_by":"owner_review"}`)

	_, err := CallTool(ctx, svc, adapter, "projects.knowledge.promote_org", json.RawMessage(`{"id":"project_1","knowledge_id":"`+knowledgeID+`","scope":"org","org_ref":"default","decision_ref":"org_promote_decision","verifier_ref":"verifier_ref","rationale":"org gate passed","decided_by":"owner_review"}`))
	if err == nil || !strings.Contains(err.Error(), "actionable claim-check findings block org promotion") {
		t.Fatalf("expected trusted claim-check actionable rejection, got %v", err)
	}
}

func TestCallToolAliasesRejectSupersedeAndPagination(t *testing.T) {
	ctx := context.Background()
	svc := projectknowledge.New(store.NewMemoryStore())
	adapter := projectknowledge.NewPromotionInputAdapter(fakeEvidenceReader{claim: highClaim()}, fakeConfidenceReader{assessment: highConfidence()})

	rejected := call(t, ctx, svc, adapter, "projects_knowledge_candidates_create", `{"id":"project_1","knowledge_ref":"knowledge/reject","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse"}`)
	rejectedID := structuredString(t, rejected, "id")
	reject := call(t, ctx, svc, adapter, "projects_knowledge_reject", `{"id":"project_1","knowledge_id":"`+rejectedID+`","decision_ref":"reject_decision","verifier_ref":"verifier_ref","rationale":"candidate rejected","decided_by":"owner_review"}`)
	if structuredString(t, reject, "state") != projectknowledge.StateRejected {
		t.Fatalf("unexpected rejected record: %#v", reject)
	}

	first := call(t, ctx, svc, adapter, "projects.knowledge.candidates.create", `{"id":"project_1","knowledge_ref":"knowledge/supersede","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse"}`)
	firstID := structuredString(t, first, "id")
	call(t, ctx, svc, adapter, "projects_knowledge_validate", `{"id":"project_1","knowledge_id":"`+firstID+`","decision_ref":"knowledge_validated","verifier_ref":"verifier_ref","rationale":"metadata gate passed"}`)
	call(t, ctx, svc, adapter, "projects_knowledge_promote_project", `{"id":"project_1","knowledge_id":"`+firstID+`","decision_ref":"knowledge_project_promoted","verifier_ref":"verifier_ref","rationale":"project gate passed"}`)
	superseded := call(t, ctx, svc, adapter, "projects_knowledge_supersede", `{"id":"project_1","knowledge_id":"`+firstID+`","superseded_by_ref":"knowledge/newer_record","decision_ref":"supersede_decision","verifier_ref":"verifier_ref","rationale":"newer evidence supersedes this record","decided_by":"owner_review"}`)
	if structuredString(t, superseded, "superseded_by_ref") != "knowledge/newer_record" {
		t.Fatalf("unexpected superseded record: %#v", superseded)
	}
	page := call(t, ctx, svc, adapter, "projects_knowledge_list", `{"id":"project_1","page_size":1}`)
	if page["structuredContent"].(KnowledgeList).NextPageToken == "" {
		t.Fatalf("expected pagination token: %#v", page)
	}
}

func TestCallToolRejectsUnknownFieldsAndUnsafePayload(t *testing.T) {
	svc := projectknowledge.New(store.NewMemoryStore())
	for _, body := range []string{
		`{"id":"project_1","knowledge_ref":"knowledge/ref_1","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse","query":"MATCH (n)"}`,
		`{"id":"project_1","knowledge_id":"knowledge_1","scope":"org","org_ref":"default","decision_ref":"org_promote_decision","verifier_ref":"verifier_ref","rationale":"org gate passed","decided_by":"owner_review","claim_check_actionable":0}`,
		`{"id":"project_1","knowledge_ref":"knowledge/ref_1","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"raw prompt: token=secret","reuse_guidance":"revalidate against current source before reuse"}`,
		`{"id":"/home/mac/project","knowledge_ref":"knowledge/ref_1","claim_id":"claim_1","claim_ref":"claim/ref_1","summary":"metadata-only implementation guidance","reuse_guidance":"revalidate against current source before reuse"}`,
	} {
		toolName := "projects.knowledge.candidates.create"
		if strings.Contains(body, "claim_check_actionable") {
			toolName = "projects.knowledge.promote_org"
		}
		_, err := CallTool(context.Background(), svc, nil, toolName, json.RawMessage(body))
		if err == nil || !strings.Contains(err.Error(), projectknowledge.ErrInvalidInput.Error()) {
			t.Fatalf("expected invalid input for %s, got %v", body, err)
		}
	}
}

func call(t *testing.T, ctx context.Context, svc *projectknowledge.Service, adapter *projectknowledge.PromotionInputAdapter, name string, body string) map[string]any {
	t.Helper()
	result, err := CallTool(ctx, svc, adapter, name, json.RawMessage(body))
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func assertToolResult(t *testing.T, result map[string]any) {
	t.Helper()
	if result["structuredContent"] == nil {
		t.Fatalf("missing structuredContent in %#v", result)
	}
	content := result["content"].([]map[string]string)
	if len(content) != 1 || content[0]["type"] != "text" || !json.Valid([]byte(content[0]["text"])) {
		t.Fatalf("expected JSON text content, got %#v", result["content"])
	}
	for _, forbidden := range []string{"raw prompt", "package main", "provider payload", "/home/mac", "token=secret", "https://"} {
		if strings.Contains(content[0]["text"], forbidden) {
			t.Fatalf("response leaked forbidden marker %q: %s", forbidden, content[0]["text"])
		}
	}
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

func rationaleMaxLength(t *testing.T, name string) int {
	t.Helper()
	for _, definition := range ToolDefinitions() {
		if definition["name"] != name {
			continue
		}
		schema := definition["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		rationale := properties["rationale"].(map[string]any)
		maxLength, ok := rationale["maxLength"].(int)
		if !ok {
			t.Fatalf("missing integer maxLength for %s rationale: %#v", name, rationale)
		}
		return maxLength
	}
	t.Fatalf("missing tool definition %s", name)
	return 0
}

type fakeEvidenceReader struct {
	claim projectevidence.ClaimRecord
}

func (reader fakeEvidenceReader) GetClaim(context.Context, string, string) (projectevidence.ClaimRecord, error) {
	return reader.claim, nil
}

type fakeConfidenceReader struct {
	assessment projectconfidence.ConfidenceAssessment
}

func (reader fakeConfidenceReader) GetAssessment(context.Context, string, string) (projectconfidence.ConfidenceAssessment, error) {
	return reader.assessment, nil
}

func highClaim() projectevidence.ClaimRecord {
	return projectevidence.ClaimRecord{
		Claim: projectevidence.Claim{ID: "claim_1", ProjectID: "project_1", RunID: "run_1", TraceID: "trace_1", ClaimRef: "claim/ref_1", Summary: "metadata-only claim summary", Status: projectevidence.ClaimStatusValidated},
		Evidence: []projectevidence.Evidence{
			{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "evidence/context_pack", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "context_pack/ref_1"},
			{ID: "evidence_2", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "evidence/verifier", EvidenceKind: projectevidence.EvidenceKindVerifier, SourceRef: "verifier/ref_1"},
		},
		Decisions:      []projectevidence.Decision{{ID: "decision_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision/ref_1", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"}},
		Actions:        []projectevidence.Action{{ID: "action_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionID: "decision_1", ActionRef: "action/ref_1", ActionKind: projectevidence.ActionKindVerifierRun, RunID: "run_1", ChangedFiles: []string{"internal/projectknowledge/mcpapi/mcpapi.go"}}},
		Outcomes:       []projectevidence.Outcome{{ID: "outcome_1", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action_1", OutcomeRef: "outcome/ref_1", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier_ref", CreatedAt: testNow}},
		PromotionLinks: []projectevidence.PromotionLink{{ProjectID: "project_1", ClaimID: "claim_1", RunID: "run_1", ArtifactRef: "artifact/ref_1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion/source_1", VerifierRef: "verifier_ref", DecisionRef: "decision/ref_1", ActionRef: "action/ref_1", OutcomeRef: "outcome/ref_1"}},
	}
}

func highConfidence() projectconfidence.ConfidenceAssessment {
	return projectconfidence.ConfidenceAssessment{ID: "confidence_1", ProjectID: "project_1", ClaimID: "claim_1", ClaimRef: "claim/ref_1", Score: 95, Band: projectconfidence.ScoreBandHigh, Recommendation: projectconfidence.RecommendationPromote, Inputs: projectconfidence.ConfidenceInputs{EvidenceKinds: []string{projectevidence.EvidenceKindContextPack, projectevidence.EvidenceKindVerifier}}}
}
