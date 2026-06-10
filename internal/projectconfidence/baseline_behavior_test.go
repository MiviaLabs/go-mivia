package projectconfidence

import (
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

func TestBaselineConfidenceScoringBehavior(t *testing.T) {
	t.Parallel()

	svc := testService()
	record := highRecord()
	record.Claim = projectevidence.Claim{ID: "claim_1", ProjectID: "project_1", ClaimRef: "claim/phase0", Summary: "metadata-only claim"}
	record.Evidence = []projectevidence.Evidence{
		{ID: "evidence-1", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "context_pack/phase0", EvidenceKind: projectevidence.EvidenceKindContextPack},
		{ID: "evidence-2", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "verifier/phase0", EvidenceKind: projectevidence.EvidenceKindVerifier},
		{ID: "evidence-3", ProjectID: "project_1", ClaimID: "claim_1", EvidenceRef: "claim_check/phase0", EvidenceKind: projectevidence.EvidenceKindClaimCheck},
	}
	record.Decisions = []projectevidence.Decision{{ID: "decision-1", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision/validated", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier/phase0", Rationale: "metadata verified"}}
	record.Actions = []projectevidence.Action{{ID: "action-1", ProjectID: "project_1", ClaimID: "claim_1", DecisionID: "decision-1", ActionRef: "action/phase0", ActionKind: projectevidence.ActionKindCodeChange}}
	record.Outcomes = []projectevidence.Outcome{{ID: "outcome-1", ProjectID: "project_1", ClaimID: "claim_1", ActionID: "action-1", OutcomeRef: "outcome/passed", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed}}
	assessment, err := svc.Score(record, readyHealth(fixedNow), verifiedClaims(), cleanImpact())
	if err != nil {
		t.Fatalf("score claim: %v", err)
	}
	if assessment.Band != ScoreBandHigh || assessment.Recommendation != RecommendationPromote || assessment.Inputs.EvidenceCount != 3 || assessment.Inputs.ClaimCheckVerified == 0 {
		t.Fatalf("confidence assessment lost promotion gates: %#v", assessment)
	}

	record.Decisions = append(record.Decisions, projectevidence.Decision{ID: "decision-reject", ProjectID: "project_1", ClaimID: "claim_1", DecisionRef: "decision/rejected", State: projectevidence.DecisionStateRejected, VerifierRef: "verifier/phase0", Rationale: "metadata rejected"})
	rejected, err := svc.Score(record, readyHealth(fixedNow), nil, nil)
	if err != nil {
		t.Fatalf("score rejected claim: %v", err)
	}
	if rejected.Recommendation != RecommendationReject {
		t.Fatalf("rejected evidence decision must dominate confidence recommendation: %#v", rejected)
	}
}
