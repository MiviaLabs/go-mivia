package projectconfidence

import "testing"

func TestBaselineConfidenceContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "confidence bands", []string{
		ScoreBandHigh,
		ScoreBandMedium,
		ScoreBandLow,
		ScoreBandUnknown,
	}, []string{"high", "medium", "low", "unknown"})

	assertExactSet(t, "recommendations", []string{
		RecommendationPromote,
		RecommendationVerify,
		RecommendationReview,
		RecommendationReject,
		RecommendationInsufficientEvidence,
	}, []string{"promote", "verify", "review", "reject", "insufficient_evidence"})

	assertExactSet(t, "factor statuses", []string{
		FactorStatusPositive,
		FactorStatusNeutral,
		FactorStatusNegative,
	}, []string{"positive", "neutral", "negative"})
}

func TestBaselineConfidenceInputsCoverPromotionGates(t *testing.T) {
	t.Parallel()

	inputs := ConfidenceInputs{
		EvidenceCount:              3,
		EvidenceKinds:              []string{"context_pack", "verifier", "claim_check"},
		DecisionCount:              1,
		ActionCount:                1,
		PassedOutcomeCount:         1,
		FailedOutcomeCount:         0,
		PromotionState:             "candidate",
		ContextHealthStatus:        "ready",
		ContextHealthReason:        "indexed_content_available",
		LatestRunAgeSeconds:        60,
		ClaimCheckVerified:         1,
		ClaimCheckActionable:       1,
		ImpactPartial:              false,
		ImpactResidualUnknownCount: 0,
		ImpactSecurityFlagCount:    0,
	}
	if inputs.EvidenceCount == 0 || inputs.DecisionCount == 0 || inputs.ActionCount == 0 || inputs.PassedOutcomeCount == 0 {
		t.Fatalf("baseline confidence inputs must cover evidence, decision, action, and outcome gates: %+v", inputs)
	}
	if inputs.ContextHealthStatus == "" || inputs.ClaimCheckVerified == 0 {
		t.Fatalf("baseline confidence inputs must include context health and claim checks: %+v", inputs)
	}
}

func assertExactSet(t *testing.T, name string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s count mismatch: got %#v want %#v", name, got, want)
	}
	seen := map[string]int{}
	for _, value := range got {
		seen[value]++
	}
	for _, value := range want {
		if seen[value] != 1 {
			t.Fatalf("%s missing or duplicated %q in %#v", name, value, got)
		}
		delete(seen, value)
	}
	if len(seen) != 0 {
		t.Fatalf("%s has unexpected values: %#v", name, seen)
	}
}
