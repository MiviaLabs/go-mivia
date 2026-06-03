package mcpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence/mcpapi"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

func TestToolDefinitionsExposeConfidenceToolsWithStrictSchemas(t *testing.T) {
	defs := mcpapi.ToolDefinitions()
	for _, name := range []string{"projects.confidence.claims.score", "projects.confidence.claims.get", "projects.confidence.claims.list"} {
		def := findTool(t, defs, name)
		schema := def["inputSchema"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("expected strict schema for %s: %#v", name, schema)
		}
	}
	if !mcpapi.IsConfidenceTool("projects.confidence.claims.score") || !mcpapi.IsConfidenceTool("projects_confidence_claims_score") {
		t.Fatalf("expected dotted and underscore score tool names")
	}
}

func TestCallToolScoreGetListClaimScore(t *testing.T) {
	svc := projectconfidence.New(confidencestore.NewMemoryStore())
	adapter := projectconfidence.NewReliabilityInputAdapter(&fakeEvidenceReader{record: highRecord()}, &fakeHealthProvider{health: readyHealth()}, nil, nil)

	score := callTool(t, svc, adapter, "projects.confidence.claims.score", `{"id":"project_1","claim_id":"claim_1"}`)
	if score["isError"] == true {
		t.Fatalf("expected score success: %#v", score)
	}
	structured := score["structuredContent"].(projectconfidence.ScoreClaimResponse)
	if structured.Assessment.ProjectID != "project_1" || structured.Assessment.ClaimID != "claim_1" || structured.Assessment.Score == 0 || len(structured.Assessment.Factors) == 0 {
		t.Fatalf("unexpected score response: %#v", structured.Assessment)
	}
	assertDoesNotLeak(t, marshalString(t, score), "raw prompt", "package main", "Authorization:", "bearer ", "token=", "secret=", "/home/", "root_path")

	get := callTool(t, svc, adapter, "projects_confidence_claims_get", `{"id":"project_1","claim_id":"claim_1"}`)
	got := get["structuredContent"].(projectconfidence.ConfidenceAssessment)
	if got.ClaimID != "claim_1" || got.ProjectID != "project_1" {
		t.Fatalf("unexpected get response: %#v", got)
	}

	list := callTool(t, svc, adapter, "projects.confidence.claims.list", `{"id":"project_1","band":"high","page_size":1}`)
	listed := list["structuredContent"].(mcpapi.ClaimScoreList)
	if len(listed.Assessments) != 1 || listed.Assessments[0].ClaimID != "claim_1" {
		t.Fatalf("unexpected list response: %#v", listed)
	}
}

func TestCallToolAliasScoreClaim(t *testing.T) {
	svc := projectconfidence.New(confidencestore.NewMemoryStore())
	adapter := projectconfidence.NewReliabilityInputAdapter(&fakeEvidenceReader{record: highRecord()}, &fakeHealthProvider{health: readyHealth()}, nil, nil)
	result := callTool(t, svc, adapter, "projects_confidence_claims_score", `{"id":"project_1","claim_id":"claim_1"}`)
	if result["structuredContent"].(projectconfidence.ScoreClaimResponse).Assessment.ClaimID != "claim_1" {
		t.Fatalf("unexpected alias result: %#v", result)
	}
}

func TestCallToolRejectsUnknownFieldsAndUnsafeMetadata(t *testing.T) {
	svc := projectconfidence.New(confidencestore.NewMemoryStore())
	adapter := projectconfidence.NewReliabilityInputAdapter(&fakeEvidenceReader{record: highRecord()}, &fakeHealthProvider{health: readyHealth()}, nil, nil)
	for _, body := range []string{
		`{"id":"project_1","claim_id":"claim_1","unknown":"field"}`,
		`{"id":"project_1","claim_id":"claim_1","_meta":{"source":"codex"}}`,
		`{"id":"project_1","claim_id":"claim_1","changed_paths":["../secret.txt"]}`,
	} {
		_, err := mcpapi.CallTool(context.Background(), svc, adapter, "projects.confidence.claims.score", json.RawMessage(body))
		if err == nil || !strings.Contains(err.Error(), projectconfidence.ErrInvalidInput.Error()) {
			t.Fatalf("expected invalid input for %s, got %v", body, err)
		}
	}
}

func TestCallToolRejectsUnsafeClaimMetadata(t *testing.T) {
	record := highRecord()
	record.Evidence[0].SourceRef = "raw prompt"
	svc := projectconfidence.New(confidencestore.NewMemoryStore())
	adapter := projectconfidence.NewReliabilityInputAdapter(&fakeEvidenceReader{record: record}, &fakeHealthProvider{health: readyHealth()}, nil, nil)
	_, err := mcpapi.CallTool(context.Background(), svc, adapter, "projects.confidence.claims.score", json.RawMessage(`{"id":"project_1","claim_id":"claim_1"}`))
	if err == nil || !strings.Contains(err.Error(), projectconfidence.ErrInvalidInput.Error()) {
		t.Fatalf("expected unsafe metadata rejection, got %v", err)
	}
}

func callTool(t *testing.T, svc *projectconfidence.Service, adapter *projectconfidence.ReliabilityInputAdapter, name string, body string) map[string]any {
	t.Helper()
	result, err := mcpapi.CallTool(context.Background(), svc, adapter, name, json.RawMessage(body))
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func findTool(t *testing.T, defs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, def := range defs {
		if def["name"] == name {
			return def
		}
	}
	t.Fatalf("missing tool %s in %#v", name, defs)
	return nil
}

func highRecord() projectevidence.ClaimRecord {
	return projectevidence.ClaimRecord{
		Claim: projectevidence.Claim{ID: "claim_1", ProjectID: "project_1", RunID: "agent_run_1", TraceID: "trace_1", ClaimRef: "claim/ref", Summary: "bounded metadata only", Status: projectevidence.ClaimStatusValidated},
		Evidence: []projectevidence.Evidence{
			{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "context_pack/ref", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "context_pack/ref", Summary: "bounded context metadata"},
			{ID: "evidence_2", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "verifier/ref", EvidenceKind: projectevidence.EvidenceKindVerifier, SourceRef: "verifier/ref", Summary: "bounded verifier metadata"},
			{ID: "evidence_3", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "claim_check/ref", EvidenceKind: projectevidence.EvidenceKindClaimCheck, SourceRef: "claim_check/ref", Summary: "bounded claim check metadata"},
		},
		Decisions:      []projectevidence.Decision{{ID: "decision_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision/ref", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier/ref", Rationale: "focused verifier passed"}},
		Actions:        []projectevidence.Action{{ID: "action_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionID: "decision_1", ActionRef: "action/ref", ActionKind: projectevidence.ActionKindCodeChange, ChangedFiles: []string{"internal/projectconfidence/mcpapi/mcpapi.go"}, RunID: "agent_run_1"}},
		Outcomes:       []projectevidence.Outcome{{ID: "outcome_1", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action_1", OutcomeRef: "outcome/ref", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier/ref", Summary: "focused verifier passed"}},
		PromotionLinks: []projectevidence.PromotionLink{{ProjectID: "project_1", ClaimID: "claim_1", RunID: "agent_run_1", ArtifactRef: "artifact/ref", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion/ref", VerifierRef: "verifier/ref", DecisionRef: "decision/ref", ActionRef: "action/ref", OutcomeRef: "outcome/ref"}},
	}
}

func readyHealth() projectreliability.ContextHealth {
	now := time.Now().UTC()
	return projectreliability.ContextHealth{ProjectID: "project_1", Status: projectreliability.ContextHealthReady, LatestRun: &projectreliability.RunSummary{ID: "ingest_1", Status: "completed", LastProgressAt: now.Add(-time.Minute)}}
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
	record.Claim.ProjectID = projectID
	record.Claim.ID = claimID
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
	health.ProjectID = projectID
	return health, nil
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
