package projectknowledge_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	knowledgestore "github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

var errMissingIntegrationInput = errors.New("missing integration input")

func TestPromotionIntegrationDenials(t *testing.T) {
	ctx := context.Background()
	claim := integrationClaim()
	confidence := integrationConfidence()

	tests := []struct {
		name       string
		evidence   projectknowledge.EvidenceClaimReader
		confidence projectknowledge.ConfidenceAssessmentReader
		wantErr    error
	}{
		{
			name:       "missing claim",
			evidence:   fakeEvidenceReader{err: errMissingIntegrationInput},
			confidence: fakeConfidenceReader{assessment: confidence},
			wantErr:    errMissingIntegrationInput,
		},
		{
			name:       "missing confidence",
			evidence:   fakeEvidenceReader{claim: claim},
			confidence: fakeConfidenceReader{err: errMissingIntegrationInput},
			wantErr:    errMissingIntegrationInput,
		},
		{
			name:       "low confidence",
			evidence:   fakeEvidenceReader{claim: claim},
			confidence: fakeConfidenceReader{assessment: withConfidenceScore(confidence, 84, projectconfidence.RecommendationVerify)},
			wantErr:    projectknowledge.ErrInvalidInput,
		},
		{
			name:       "missing passed outcome",
			evidence:   fakeEvidenceReader{claim: withoutPassedOutcome(claim)},
			confidence: fakeConfidenceReader{assessment: confidence},
			wantErr:    projectknowledge.ErrInvalidInput,
		},
		{
			name:       "rejected decision",
			evidence:   fakeEvidenceReader{claim: withRejectedDecision(claim)},
			confidence: fakeConfidenceReader{assessment: confidence},
			wantErr:    projectknowledge.ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := projectknowledge.New(knowledgestore.NewMemoryStore())
			candidate := mustCreateIntegrationCandidate(t, ctx, svc)
			adapter := projectknowledge.NewPromotionInputAdapter(tt.evidence, tt.confidence)

			_, err := svc.ValidateCandidateWithInputs(ctx, adapter, projectknowledge.ValidateCandidateWithInputsInput{
				ProjectID:   "project_1",
				KnowledgeID: candidate.ID,
				DecisionRef: "knowledge_validated",
				VerifierRef: "verifier_ref",
				Rationale:   "metadata gate checked",
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestPromotionIntegrationProjectPromotesWithEvidenceAndConfidenceOutputs(t *testing.T) {
	ctx := context.Background()
	evidenceSvc, confidenceSvc, claim := realEvidenceAndConfidence(t, ctx, false)
	knowledgeSvc := projectknowledge.New(knowledgestore.NewMemoryStore())
	candidate := mustCreateCandidateForClaim(t, ctx, knowledgeSvc, claim.Claim.ID, claim.Claim.ClaimRef)
	adapter := projectknowledge.NewPromotionInputAdapter(evidenceSvc, confidenceSvc)

	validated, err := knowledgeSvc.ValidateCandidateWithInputs(ctx, adapter, projectknowledge.ValidateCandidateWithInputsInput{
		ProjectID:   "project_1",
		KnowledgeID: candidate.ID,
		DecisionRef: "knowledge_validated",
		VerifierRef: "verifier_ref",
		Rationale:   "metadata gate checked",
	})
	if err != nil {
		t.Fatalf("ValidateCandidateWithInputs returned error: %v", err)
	}
	if validated.State != projectknowledge.StateValidated || validated.ConfidenceScore < 85 {
		t.Fatalf("unexpected validated record: %+v", validated)
	}

	promoted, err := knowledgeSvc.PromoteProjectWithInputs(ctx, adapter, projectknowledge.PromoteProjectWithInputsInput{
		ProjectID:   "project_1",
		KnowledgeID: candidate.ID,
		DecisionRef: "knowledge_project_promoted",
		VerifierRef: "verifier_ref",
		Rationale:   "project gate checked",
	})
	if err != nil {
		t.Fatalf("PromoteProjectWithInputs returned error: %v", err)
	}
	if promoted.State != projectknowledge.StateProjectPromoted || promoted.Scope != projectknowledge.ScopeProject {
		t.Fatalf("unexpected promoted record: %+v", promoted)
	}
}

func TestPromotionIntegrationOrgPromotesWithProjectKnowledgeAndConfidenceOutput(t *testing.T) {
	ctx := context.Background()
	evidenceSvc, confidenceSvc, claim := realEvidenceAndConfidence(t, ctx, false)
	knowledgeSvc := projectknowledge.New(knowledgestore.NewMemoryStore())
	candidate := mustCreateCandidateForClaim(t, ctx, knowledgeSvc, claim.Claim.ID, claim.Claim.ClaimRef)
	adapter := projectknowledge.NewPromotionInputAdapter(evidenceSvc, confidenceSvc)

	if _, err := knowledgeSvc.ValidateCandidateWithInputs(ctx, adapter, projectknowledge.ValidateCandidateWithInputsInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "knowledge_validated", VerifierRef: "verifier_ref", Rationale: "metadata gate checked"}); err != nil {
		t.Fatalf("ValidateCandidateWithInputs returned error: %v", err)
	}
	projectPromoted, err := knowledgeSvc.PromoteProjectWithInputs(ctx, adapter, projectknowledge.PromoteProjectWithInputsInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "knowledge_project_promoted", VerifierRef: "verifier_ref", Rationale: "project gate checked"})
	if err != nil {
		t.Fatalf("PromoteProjectWithInputs returned error: %v", err)
	}
	if _, err := knowledgeSvc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{ProjectID: "project_1", KnowledgeID: projectPromoted.ID, DecisionRef: "org_review", VerifierRef: "verifier_ref", Rationale: "org review requested", DecidedBy: "owner_review"}); err != nil {
		t.Fatalf("SubmitOrgReview returned error: %v", err)
	}

	orgPromoted, err := knowledgeSvc.PromoteOrgWithInputs(ctx, adapter, projectknowledge.PromoteOrgWithInputsInput{
		ProjectID:   "project_1",
		KnowledgeID: projectPromoted.ID,
		Scope:       projectknowledge.ScopeOrg,
		OrgRef:      projectknowledge.DefaultOrgRef,
		DecisionRef: "org_promote_decision",
		VerifierRef: "verifier_ref",
		Rationale:   "org gate checked",
		DecidedBy:   "owner_review",
	})
	if err != nil {
		t.Fatalf("PromoteOrgWithInputs returned error: %v", err)
	}
	if orgPromoted.State != projectknowledge.StateOrgPromoted || orgPromoted.Scope != projectknowledge.ScopeOrg {
		t.Fatalf("unexpected org promoted record: %+v", orgPromoted)
	}
}

func TestPromotionIntegrationOrgRejectsRejectedDecision(t *testing.T) {
	ctx := context.Background()
	evidenceSvc, confidenceSvc, claim := realEvidenceAndConfidence(t, ctx, true)
	knowledgeSvc := projectknowledge.New(knowledgestore.NewMemoryStore())
	candidate := mustCreateCandidateForClaim(t, ctx, knowledgeSvc, claim.Claim.ID, claim.Claim.ClaimRef)
	adapter := projectknowledge.NewPromotionInputAdapter(evidenceSvc, confidenceSvc)

	_, err := knowledgeSvc.ValidateCandidateWithInputs(ctx, adapter, projectknowledge.ValidateCandidateWithInputsInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "knowledge_validated", VerifierRef: "verifier_ref", Rationale: "metadata gate checked"})
	if !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected rejected decision denial, got %v", err)
	}
}

type fakeEvidenceReader struct {
	claim projectevidence.ClaimRecord
	err   error
}

func (reader fakeEvidenceReader) GetClaim(context.Context, string, string) (projectevidence.ClaimRecord, error) {
	if reader.err != nil {
		return projectevidence.ClaimRecord{}, reader.err
	}
	return reader.claim, nil
}

type fakeConfidenceReader struct {
	assessment projectconfidence.ConfidenceAssessment
	err        error
}

func (reader fakeConfidenceReader) GetAssessment(context.Context, string, string) (projectconfidence.ConfidenceAssessment, error) {
	if reader.err != nil {
		return projectconfidence.ConfidenceAssessment{}, reader.err
	}
	return reader.assessment, nil
}

func realEvidenceAndConfidence(t *testing.T, ctx context.Context, includeRejected bool) (*projectevidence.Service, *projectconfidence.Service, projectevidence.ClaimRecord) {
	t.Helper()

	evidenceSvc := projectevidence.New(evidencestore.NewMemoryStore())
	claim, err := evidenceSvc.CreateClaim(ctx, projectevidence.CreateClaimInput{ProjectID: "project_1", RunID: "run_1", TraceID: "trace_1", ClaimRef: "claim/ref_1", Summary: "metadata-only claim summary", Status: projectevidence.ClaimStatusValidated})
	if err != nil {
		t.Fatalf("CreateClaim returned error: %v", err)
	}
	if _, err := evidenceSvc.AppendEvidence(ctx, projectevidence.AppendEvidenceInput{ProjectID: "project_1", ClaimID: claim.ID, EvidenceRef: "evidence/context_pack", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "context_pack/ref_1"}); err != nil {
		t.Fatalf("AppendEvidence returned error: %v", err)
	}
	if _, err := evidenceSvc.AppendEvidence(ctx, projectevidence.AppendEvidenceInput{ProjectID: "project_1", ClaimID: claim.ID, EvidenceRef: "evidence/verifier", EvidenceKind: projectevidence.EvidenceKindVerifier, SourceRef: "verifier/ref_1"}); err != nil {
		t.Fatalf("AppendEvidence returned error: %v", err)
	}
	decision, err := evidenceSvc.CreateDecision(ctx, projectevidence.CreateDecisionInput{ProjectID: "project_1", ClaimID: claim.ID, DecisionRef: "decision/ref_1", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"})
	if err != nil {
		t.Fatalf("CreateDecision returned error: %v", err)
	}
	if includeRejected {
		if _, err := evidenceSvc.CreateDecision(ctx, projectevidence.CreateDecisionInput{ProjectID: "project_1", ClaimID: claim.ID, DecisionRef: "decision/rejected", State: projectevidence.DecisionStateRejected, VerifierRef: "verifier_ref", Rationale: "metadata rejected"}); err != nil {
			t.Fatalf("CreateDecision rejected returned error: %v", err)
		}
	}
	action, err := evidenceSvc.CreateAction(ctx, projectevidence.CreateActionInput{ProjectID: "project_1", ClaimID: claim.ID, DecisionID: decision.ID, ActionRef: "action/ref_1", ActionKind: projectevidence.ActionKindVerifierRun, ChangedFiles: []string{"internal/projectknowledge/integration.go"}, RunID: "run_1"})
	if err != nil {
		t.Fatalf("CreateAction returned error: %v", err)
	}
	outcome, err := evidenceSvc.CreateOutcome(ctx, projectevidence.CreateOutcomeInput{ProjectID: "project_1", ClaimID: claim.ID, ActionID: action.ID, OutcomeRef: "outcome/ref_1", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier_ref"})
	if err != nil {
		t.Fatalf("CreateOutcome returned error: %v", err)
	}
	if _, err := evidenceSvc.LinkArtifact(ctx, projectevidence.LinkArtifactInput{ProjectID: "project_1", ClaimID: claim.ID, ArtifactRef: "artifact/ref_1", ArtifactKind: "knowledge_promotion", RunID: "run_1"}); err != nil {
		t.Fatalf("LinkArtifact returned error: %v", err)
	}
	if _, err := evidenceSvc.LinkPromotion(ctx, projectevidence.LinkPromotionInput{ProjectID: "project_1", ClaimID: claim.ID, RunID: "run_1", ArtifactRef: "artifact/ref_1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion/source_1", VerifierRef: "verifier_ref", DecisionRef: decision.DecisionRef, ActionRef: action.ActionRef, OutcomeRef: outcome.OutcomeRef}); err != nil {
		t.Fatalf("LinkPromotion returned error: %v", err)
	}

	record, err := evidenceSvc.GetClaim(ctx, "project_1", claim.ID)
	if err != nil {
		t.Fatalf("GetClaim returned error: %v", err)
	}
	confidenceSvc := projectconfidence.New(confidencestore.NewMemoryStore())
	assessment, err := confidenceSvc.ScoreClaim(ctx, record, readyHealth(), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("ScoreClaim returned error: %v", err)
	}
	if !includeRejected && (assessment.Score < 90 || assessment.Recommendation != projectconfidence.RecommendationPromote) {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
	return evidenceSvc, confidenceSvc, record
}

func mustCreateIntegrationCandidate(t *testing.T, ctx context.Context, svc *projectknowledge.Service) projectknowledge.KnowledgeRecord {
	t.Helper()
	return mustCreateCandidateForClaim(t, ctx, svc, "claim_a", "claim/ref_a")
}

func mustCreateCandidateForClaim(t *testing.T, ctx context.Context, svc *projectknowledge.Service, claimID string, claimRef string) projectknowledge.KnowledgeRecord {
	t.Helper()
	record, err := svc.CreateCandidate(ctx, projectknowledge.CreateCandidateInput{ProjectID: "project_1", KnowledgeRef: "knowledge/ref_1", ClaimID: claimID, ClaimRef: claimRef, Summary: "metadata-only implementation guidance", ReuseGuidance: "revalidate against current source before reuse"})
	if err != nil {
		t.Fatalf("CreateCandidate returned error: %v", err)
	}
	return record
}

func integrationClaim() projectevidence.ClaimRecord {
	return projectevidence.ClaimRecord{
		Claim: projectevidence.Claim{ID: "claim_a", ProjectID: "project_1", RunID: "run_a", TraceID: "trace_a", ClaimRef: "claim/ref_a", Summary: "metadata-only claim summary", Status: projectevidence.ClaimStatusValidated},
		Evidence: []projectevidence.Evidence{
			{ID: "evidence_a", ProjectID: "project_1", ClaimID: "claim_a", EvidenceRef: "evidence/context_pack", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "context_pack/ref_a"},
			{ID: "evidence_b", ProjectID: "project_1", ClaimID: "claim_a", EvidenceRef: "evidence/verifier", EvidenceKind: projectevidence.EvidenceKindVerifier, SourceRef: "verifier/ref_a"},
		},
		Decisions:      []projectevidence.Decision{{ID: "decision_a", ProjectID: "project_1", ClaimID: "claim_a", DecisionRef: "decision/ref_a", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"}},
		Actions:        []projectevidence.Action{{ID: "action_a", ProjectID: "project_1", ClaimID: "claim_a", DecisionID: "decision_a", ActionRef: "action/ref_a", ActionKind: projectevidence.ActionKindVerifierRun, RunID: "run_a", ChangedFiles: []string{"internal/projectknowledge/integration.go"}}},
		Outcomes:       []projectevidence.Outcome{{ID: "outcome_a", ProjectID: "project_1", ClaimID: "claim_a", ActionID: "action_a", OutcomeRef: "outcome/ref_a", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier_ref", CreatedAt: time.Now().UTC()}},
		PromotionLinks: []projectevidence.PromotionLink{{ProjectID: "project_1", ClaimID: "claim_a", RunID: "run_a", ArtifactRef: "artifact/ref_a", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion/source_a", VerifierRef: "verifier_ref", DecisionRef: "decision/ref_a", ActionRef: "action/ref_a", OutcomeRef: "outcome/ref_a"}},
	}
}

func integrationConfidence() projectconfidence.ConfidenceAssessment {
	return projectconfidence.ConfidenceAssessment{
		ID:             "confidence_a",
		ProjectID:      "project_1",
		ClaimID:        "claim_a",
		ClaimRef:       "claim/ref_a",
		Score:          95,
		Band:           projectconfidence.ScoreBandHigh,
		Recommendation: projectconfidence.RecommendationPromote,
		Factors:        []projectconfidence.ConfidenceFactor{{Name: "evidence_graph_promoted_link", ScoreDelta: 15, Weight: 15, Status: projectconfidence.FactorStatusPositive, Summary: "claim has a promoted link with a passed outcome", SourceRef: "claim.promotions"}},
		Inputs:         projectconfidence.ConfidenceInputs{EvidenceKinds: []string{projectevidence.EvidenceKindContextPack, projectevidence.EvidenceKindVerifier}, PassedOutcomeCount: 1, PromotionState: projectevidence.PromotionStatePromoted},
	}
}

func withConfidenceScore(assessment projectconfidence.ConfidenceAssessment, score int, recommendation string) projectconfidence.ConfidenceAssessment {
	assessment.Score = score
	assessment.Recommendation = recommendation
	if score >= 85 {
		assessment.Band = projectconfidence.ScoreBandHigh
	} else if score >= 60 {
		assessment.Band = projectconfidence.ScoreBandMedium
	} else {
		assessment.Band = projectconfidence.ScoreBandLow
	}
	return assessment
}

func withoutPassedOutcome(record projectevidence.ClaimRecord) projectevidence.ClaimRecord {
	record.Outcomes = nil
	return record
}

func withRejectedDecision(record projectevidence.ClaimRecord) projectevidence.ClaimRecord {
	record.Decisions = append(record.Decisions, projectevidence.Decision{ID: "decision_rejected", ProjectID: "project_1", ClaimID: "claim_a", DecisionRef: "decision/rejected", State: projectevidence.DecisionStateRejected, VerifierRef: "verifier_ref", Rationale: "metadata rejected"})
	return record
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
