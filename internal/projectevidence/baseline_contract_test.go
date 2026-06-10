package projectevidence

import "testing"

func TestBaselineEvidenceGraphContract(t *testing.T) {
	t.Parallel()

	assertExactSet(t, "claim statuses", []string{
		ClaimStatusCandidate,
		ClaimStatusValidated,
		ClaimStatusPromoted,
		ClaimStatusRejected,
	}, []string{"candidate", "validated", "promoted", "rejected"})

	assertExactSet(t, "evidence kinds", []string{
		EvidenceKindContextPack,
		EvidenceKindFile,
		EvidenceKindChunk,
		EvidenceKindSymbol,
		EvidenceKindVerifier,
		EvidenceKindClaimCheck,
		EvidenceKindArtifact,
		EvidenceKindOther,
	}, []string{"context_pack", "file", "chunk", "symbol", "verifier", "claim_check", "artifact", "other"})

	assertExactSet(t, "decision states", []string{
		DecisionStateValidated,
		DecisionStatePromoted,
		DecisionStateRejected,
	}, []string{"validated", "promoted", "rejected"})

	assertExactSet(t, "action kinds", []string{
		ActionKindCodeChange,
		ActionKindDocChange,
		ActionKindVerifierRun,
		ActionKindConfigChange,
		ActionKindReviewComment,
		ActionKindOther,
	}, []string{"code_change", "doc_change", "verifier_run", "config_change", "review_comment", "other"})

	assertExactSet(t, "outcome statuses", []string{
		OutcomeStatusPassed,
		OutcomeStatusFailed,
		OutcomeStatusBlocked,
		OutcomeStatusUnknown,
	}, []string{"passed", "failed", "blocked", "unknown"})
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
