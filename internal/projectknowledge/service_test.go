package projectknowledge_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
)

var testNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestProjectPromotionGateRequiresEvidenceAndConfidence(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	candidate := mustCreateCandidate(t, ctx, svc)

	validated, err := svc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "knowledge_validated", VerifierRef: "verifier_ref", Rationale: "metadata gate passed", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}})
	if err != nil {
		t.Fatalf("ValidateCandidate returned error: %v", err)
	}
	if validated.State != projectknowledge.StateValidated || validated.ConfidenceScore != 95 {
		t.Fatalf("unexpected validated record: %+v", validated)
	}

	promoted, err := svc.PromoteProject(ctx, projectknowledge.PromoteProjectInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "knowledge_project_promoted", VerifierRef: "verifier_ref", Rationale: "project gate passed", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}})
	if err != nil {
		t.Fatalf("PromoteProject returned error: %v", err)
	}
	if promoted.State != projectknowledge.StateProjectPromoted || promoted.Scope != projectknowledge.ScopeProject {
		t.Fatalf("unexpected promoted record: %+v", promoted)
	}

	lowSvc := newService()
	lowCandidate := mustCreateCandidate(t, ctx, lowSvc)
	low := highConfidence()
	low.Score = 84
	low.Recommendation = projectconfidence.RecommendationVerify
	if _, err := lowSvc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: lowCandidate.ID, DecisionRef: "low_validation", VerifierRef: "verifier_ref", Rationale: "low confidence", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: low}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected low confidence rejection, got %v", err)
	}

	noEvidenceSvc := newService()
	noEvidenceCandidate := mustCreateCandidate(t, ctx, noEvidenceSvc)
	claim := highClaim()
	claim.Evidence = nil
	if _, err := noEvidenceSvc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: noEvidenceCandidate.ID, DecisionRef: "no_evidence_validation", VerifierRef: "verifier_ref", Rationale: "missing evidence", Gate: projectknowledge.ProjectGateInput{Claim: claim, Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected missing evidence rejection, got %v", err)
	}

	noPromotionLinkSvc := newService()
	noPromotionLinkCandidate := mustCreateCandidate(t, ctx, noPromotionLinkSvc)
	claim = highClaim()
	claim.PromotionLinks = nil
	if _, err := noPromotionLinkSvc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: noPromotionLinkCandidate.ID, DecisionRef: "no_promotion_link_validation", VerifierRef: "verifier_ref", Rationale: "missing promotion link", Gate: projectknowledge.ProjectGateInput{Claim: claim, Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected missing promotion link rejection, got %v", err)
	}

	foreignPromotionLinkSvc := newService()
	foreignPromotionLinkCandidate := mustCreateCandidate(t, ctx, foreignPromotionLinkSvc)
	claim = highClaim()
	claim.PromotionLinks[0].ProjectID = "project_2"
	claim.PromotionLinks[0].ClaimID = "claim_2"
	if _, err := foreignPromotionLinkSvc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: foreignPromotionLinkCandidate.ID, DecisionRef: "foreign_promotion_link_validation", VerifierRef: "verifier_ref", Rationale: "foreign promotion link", Gate: projectknowledge.ProjectGateInput{Claim: claim, Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected foreign promotion link rejection, got %v", err)
	}
}

func TestOrgPromotionGateDenials(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	candidate := mustCreateCandidate(t, ctx, svc)
	if _, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: candidate.ID, Gate: orgGate(0)}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected org promotion before project promotion rejection, got %v", err)
	}

	promoted := mustProjectPromote(t, ctx, svc, candidate.ID)
	if _, err := svc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{ProjectID: "project_1", KnowledgeID: promoted.ID, DecisionRef: "org_review_empty_actor", VerifierRef: "verifier_ref", Rationale: "org review requested"}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected empty org reviewer rejection, got %v", err)
	}
	if _, err := svc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{ProjectID: "project_1", KnowledgeID: promoted.ID, DecisionRef: "org_review_email_actor", VerifierRef: "verifier_ref", Rationale: "org review requested", DecidedBy: "owner" + "@" + "example" + ".invalid"}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected unsafe org reviewer rejection, got %v", err)
	}
	if _, err := svc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{ProjectID: "project_1", KnowledgeID: promoted.ID, OrgRef: "other", DecisionRef: "org_review_other", VerifierRef: "verifier_ref", Rationale: "org review requested", DecidedBy: "owner_review"}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected unsupported org review rejection, got %v", err)
	}
	underReview, err := svc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{ProjectID: "project_1", KnowledgeID: promoted.ID, DecisionRef: "org_review", VerifierRef: "verifier_ref", Rationale: "org review requested", DecidedBy: "owner_review"})
	if err != nil {
		t.Fatalf("SubmitOrgReview returned error: %v", err)
	}
	if underReview.State != projectknowledge.StateOrgReview || underReview.Scope != projectknowledge.ScopeOrg || underReview.OrgRef != projectknowledge.DefaultOrgRef {
		t.Fatalf("unexpected org review record: %+v", underReview)
	}
	if _, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: promoted.ID, Gate: orgGate(1)}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected actionable claim-check rejection, got %v", err)
	}
	low := orgGate(0)
	low.Confidence.Score = 89
	if _, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: promoted.ID, Gate: low}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected low org confidence rejection, got %v", err)
	}
	badScope := orgGate(0)
	badScope.Scope = projectknowledge.ScopeProject
	if _, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: promoted.ID, Gate: badScope}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected non-org scope rejection, got %v", err)
	}
	badOrg := orgGate(0)
	badOrg.OrgRef = "other"
	if _, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: promoted.ID, Gate: badOrg}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected mismatched org_ref rejection, got %v", err)
	}

	orgPromoted, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: promoted.ID, Gate: orgGate(0)})
	if err != nil {
		t.Fatalf("PromoteOrg returned error: %v", err)
	}
	if orgPromoted.State != projectknowledge.StateOrgPromoted || orgPromoted.Scope != projectknowledge.ScopeOrg {
		t.Fatalf("unexpected org promoted record: %+v", orgPromoted)
	}
}

func TestInvalidTransitionsAreDenied(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	candidate := mustCreateCandidate(t, ctx, svc)
	if _, err := svc.PromoteProject(ctx, projectknowledge.PromoteProjectInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "promote_before_validate", VerifierRef: "verifier_ref", Rationale: "invalid transition", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected promote before validate rejection, got %v", err)
	}
	mustValidate(t, ctx, svc, candidate.ID)
	if _, err := svc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "validate_twice", VerifierRef: "verifier_ref", Rationale: "invalid transition", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected second validation rejection, got %v", err)
	}
	if _, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: candidate.ID, Gate: orgGate(0)}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected org promotion before org review rejection, got %v", err)
	}

	rejectSvc := newService()
	rejectCandidate := mustCreateCandidate(t, ctx, rejectSvc)
	rejected, err := rejectSvc.Reject(ctx, projectknowledge.RejectInput{ProjectID: "project_1", KnowledgeID: rejectCandidate.ID, DecisionRef: "reject_candidate", VerifierRef: "verifier_ref", Rationale: "candidate rejected"})
	if err != nil {
		t.Fatalf("Reject returned error: %v", err)
	}
	if rejected.State != projectknowledge.StateRejected {
		t.Fatalf("unexpected rejected state: %+v", rejected)
	}
	if _, err := rejectSvc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: rejectCandidate.ID, DecisionRef: "validate_rejected", VerifierRef: "verifier_ref", Rationale: "invalid transition", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected rejected final-state transition denial, got %v", err)
	}
}

func TestSupersessionDoesNotDeleteKnowledge(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	candidate := mustCreateCandidate(t, ctx, svc)
	promoted := mustProjectPromote(t, ctx, svc, candidate.ID)
	superseded, err := svc.Supersede(ctx, projectknowledge.SupersedeInput{ProjectID: "project_1", KnowledgeID: promoted.ID, SupersededByRef: "knowledge/newer_record", DecisionRef: "supersede_decision", VerifierRef: "verifier_ref", Rationale: "newer evidence supersedes this record", DecidedBy: "owner_review"})
	if err != nil {
		t.Fatalf("Supersede returned error: %v", err)
	}
	if superseded.State != projectknowledge.StateSuperseded || superseded.SupersededByRef != "knowledge/newer_record" {
		t.Fatalf("unexpected superseded record: %+v", superseded)
	}
	got, err := svc.GetKnowledge(ctx, "project_1", promoted.ID)
	if err != nil {
		t.Fatalf("GetKnowledge after supersede returned error: %v", err)
	}
	if got.State != projectknowledge.StateSuperseded {
		t.Fatalf("record was not retained as superseded: %+v", got)
	}
	listed, err := svc.ListKnowledge(ctx, "project_1", projectknowledge.KnowledgeFilter{State: projectknowledge.StateSuperseded})
	if err != nil {
		t.Fatalf("ListKnowledge returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != promoted.ID {
		t.Fatalf("unexpected superseded list: %+v", listed)
	}
}

func TestUnsafeMetadataRejection(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	unsafeCandidates := []projectknowledge.CreateCandidateInput{
		candidateInputWithSummary("raw" + " prompt marker"),
		candidateInputWithSummary("provider" + " payload marker"),
		candidateInputWithSummary("package" + " main"),
		candidateInputWithSummary("owner" + "@" + "example" + ".invalid"),
		candidateInputWithKnowledgeRef("https:" + "//" + "invalid.example/ref"),
		candidateInputWithKnowledgeRef("/" + "home" + "/mac/project/ref"),
		candidateInputWithKnowledgeRef("docs/" + ".." + "/unsafe"),
	}
	for _, input := range unsafeCandidates {
		if _, err := svc.CreateCandidate(ctx, input); !errors.Is(err, projectknowledge.ErrInvalidInput) {
			t.Fatalf("expected unsafe candidate rejection for %+v, got %v", input, err)
		}
	}

	candidate := mustCreateCandidate(t, ctx, svc)
	claim := highClaim()
	claim.Evidence[0].SourceRef = "https:" + "//" + "invalid.example/source"
	if _, err := svc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: candidate.ID, DecisionRef: "unsafe_gate", VerifierRef: "verifier_ref", Rationale: "unsafe source", Gate: projectknowledge.ProjectGateInput{Claim: claim, Confidence: highConfidence()}}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected unsafe gate metadata rejection, got %v", err)
	}

	if _, err := svc.RecordReuseEvent(ctx, projectknowledge.RecordReuseEventInput{ProjectID: "project_1", KnowledgeID: candidate.ID, ReuseRef: "reuse_ref", Outcome: projectknowledge.ReuseOutcomeUsed, Summary: "raw" + " source marker"}); !errors.Is(err, projectknowledge.ErrInvalidInput) {
		t.Fatalf("expected unsafe reuse summary rejection, got %v", err)
	}
}

func newService() *projectknowledge.Service {
	return projectknowledge.New(store.NewMemoryStore())
}

func mustCreateCandidate(t *testing.T, ctx context.Context, svc *projectknowledge.Service) projectknowledge.KnowledgeRecord {
	t.Helper()
	record, err := svc.CreateCandidate(ctx, candidateInput())
	if err != nil {
		t.Fatalf("CreateCandidate returned error: %v", err)
	}
	return record
}

func mustValidate(t *testing.T, ctx context.Context, svc *projectknowledge.Service, id string) projectknowledge.KnowledgeRecord {
	t.Helper()
	record, err := svc.ValidateCandidate(ctx, projectknowledge.ValidateCandidateInput{ProjectID: "project_1", KnowledgeID: id, DecisionRef: "knowledge_validated", VerifierRef: "verifier_ref", Rationale: "metadata gate passed", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}})
	if err != nil {
		t.Fatalf("ValidateCandidate returned error: %v", err)
	}
	return record
}

func mustProjectPromote(t *testing.T, ctx context.Context, svc *projectknowledge.Service, id string) projectknowledge.KnowledgeRecord {
	t.Helper()
	mustValidate(t, ctx, svc, id)
	record, err := svc.PromoteProject(ctx, projectknowledge.PromoteProjectInput{ProjectID: "project_1", KnowledgeID: id, DecisionRef: "knowledge_project_promoted", VerifierRef: "verifier_ref", Rationale: "project gate passed", Gate: projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()}})
	if err != nil {
		t.Fatalf("PromoteProject returned error: %v", err)
	}
	return record
}

func candidateInput() projectknowledge.CreateCandidateInput {
	return projectknowledge.CreateCandidateInput{ProjectID: "project_1", KnowledgeRef: "knowledge/ref_1", ClaimID: "claim_1", ClaimRef: "claim/ref_1", Summary: "metadata-only implementation guidance", ReuseGuidance: "revalidate against current source before reuse"}
}

func candidateInputWithSummary(summary string) projectknowledge.CreateCandidateInput {
	input := candidateInput()
	input.Summary = summary
	return input
}

func candidateInputWithKnowledgeRef(ref string) projectknowledge.CreateCandidateInput {
	input := candidateInput()
	input.KnowledgeRef = ref
	return input
}

func highClaim() projectevidence.ClaimRecord {
	return projectevidence.ClaimRecord{
		Claim: projectevidence.Claim{ID: "claim_1", ProjectID: "project_1", RunID: "run_1", TraceID: "trace_1", ClaimRef: "claim/ref_1", Summary: "metadata-only claim summary", Status: projectevidence.ClaimStatusValidated},
		Evidence: []projectevidence.Evidence{
			{ID: "evidence_1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "evidence/context_pack", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "context_pack/ref_1"},
			{ID: "evidence_2", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "evidence/verifier", EvidenceKind: projectevidence.EvidenceKindVerifier, SourceRef: "verifier/ref_1"},
		},
		Decisions:      []projectevidence.Decision{{ID: "decision_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision/ref_1", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier_ref", Rationale: "metadata verified"}},
		Actions:        []projectevidence.Action{{ID: "action_1", ProjectID: "project_1", ClaimID: "claim_1", DecisionID: "decision_1", ActionRef: "action/ref_1", ActionKind: projectevidence.ActionKindVerifierRun, RunID: "run_1", ChangedFiles: []string{"internal/projectknowledge/service.go"}}},
		Outcomes:       []projectevidence.Outcome{{ID: "outcome_1", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action_1", OutcomeRef: "outcome/ref_1", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier_ref", CreatedAt: testNow}},
		PromotionLinks: []projectevidence.PromotionLink{{ProjectID: "project_1", ClaimID: "claim_1", RunID: "run_1", ArtifactRef: "artifact/ref_1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "promotion/source_1", VerifierRef: "verifier_ref", DecisionRef: "decision/ref_1", ActionRef: "action/ref_1", OutcomeRef: "outcome/ref_1"}},
	}
}

func highConfidence() projectconfidence.ConfidenceAssessment {
	return projectconfidence.ConfidenceAssessment{ID: "confidence_1", ProjectID: "project_1", ClaimID: "claim_1", ClaimRef: "claim/ref_1", Score: 95, Band: projectconfidence.ScoreBandHigh, Recommendation: projectconfidence.RecommendationPromote, Inputs: projectconfidence.ConfidenceInputs{EvidenceKinds: []string{projectevidence.EvidenceKindContextPack, projectevidence.EvidenceKindVerifier}}}
}

func orgGate(actionable int) projectknowledge.OrgGateInput {
	return projectknowledge.OrgGateInput{Claim: highClaim(), Confidence: highConfidence(), ClaimCheckActionable: actionable, Scope: projectknowledge.ScopeOrg, OrgRef: projectknowledge.DefaultOrgRef, DecisionRef: "org_promote_decision", VerifierRef: "verifier_ref", Rationale: "org gate passed", DecidedBy: "owner_review"}
}
