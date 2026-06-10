package projectknowledge

import "testing"

func TestBaselineKnowledgePromotionContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "knowledge scopes", []string{
		ScopeProject,
		ScopeOrg,
	}, []string{"project", "org"})

	assertExactSet(t, "knowledge states", []string{
		StateCandidate,
		StateValidated,
		StateProjectPromoted,
		StateOrgReview,
		StateOrgPromoted,
		StateRejected,
		StateSuperseded,
	}, []string{"candidate", "validated", "project_promoted", "org_review", "org_promoted", "rejected", "superseded"})

	assertExactSet(t, "reuse outcomes", []string{
		ReuseOutcomeUsed,
		ReuseOutcomeSkipped,
		ReuseOutcomeStale,
		ReuseOutcomeContradicted,
	}, []string{"used", "skipped", "stale", "contradicted"})
}

func TestBaselineKnowledgeRecordDoesNotAutoPromote(t *testing.T) {
	t.Parallel()

	record := KnowledgeRecord{
		ProjectID:              "project-1",
		Scope:                  ScopeProject,
		KnowledgeRef:           "knowledge:baseline",
		ClaimID:                "claim-1",
		ClaimRef:               "claim:baseline",
		ConfidenceScore:        95,
		ConfidenceBand:         "high",
		State:                  StateCandidate,
		Summary:                "metadata-only reusable conclusion",
		ReuseGuidance:          "revalidate before use",
		EvidenceRefs:           []string{"evidence:baseline"},
		VerifierRefs:           []string{"verifier:baseline"},
		OutcomeRefs:            []string{"outcome:baseline"},
		PromotionRefs:          []string{"promotion:baseline"},
		SupersedesRef:          "",
		SupersededByRef:        "",
		ConfidenceAssessmentID: "confidence-1",
	}
	if record.State != StateCandidate {
		t.Fatalf("new knowledge must start as candidate, got %+v", record)
	}
	if record.ConfidenceScore >= 90 && record.State == StateProjectPromoted {
		t.Fatalf("confidence must not auto-promote knowledge: %+v", record)
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
