package parity

import "testing"

func TestPhase8GitOpsParityScenarios(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
		assert func(*testing.T, Snapshot)
	}{
		{
			name: "draft-pr-handoff-requires-review-verifier-evidence-and-pr-ref",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "verifier:post-pr", "review:post-pr", "evidence:generated-artifacts")
				s.WorkTasks[0].VerifierRefs = append(s.WorkTasks[0].VerifierRefs, "verifier:post-pr")
				s.WorkTasks[0].ReviewRefs = append(s.WorkTasks[0].ReviewRefs, "review:post-pr")
				s.WorkTasks[0].EvidenceRefs = append(s.WorkTasks[0].EvidenceRefs, "evidence:generated-artifacts")
				s.Chain.PullRequestRefs = []string{"pr:draft-1"}
				s.GitOps.Refs = []string{"gitops:commit", "gitops:push", "gitops:pr"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].VerifierRefs, "verifier:post-pr")
				requireRefs(t, s.WorkTasks[0].ReviewRefs, "review:post-pr")
				requireRefs(t, s.WorkTasks[0].EvidenceRefs, "evidence:generated-artifacts")
				requireRefs(t, s.Chain.PullRequestRefs, "pr:draft-1")
				requireRefs(t, s.GitOps.Refs, "gitops:commit", "gitops:push", "gitops:pr")
			},
		},
		{
			name: "no-diff-output-blocks-before-pr",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "gitops:no-diff")
				s.Automation.Status = "blocked"
				s.WorkPlan.Status = "blocked"
				s.WorkPlan.SafeNextAction = "produce bounded diff before draft pr"
				s.WorkTasks[0].Status = "blocked"
				s.GitOps.Refs = nil
				s.GitOps.FailureCategories = []string{"gitops:no-diff"}
				s.Chain.GitOpsReady = false
				s.Chain.PullRequestRefs = nil
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.GitOps.FailureCategories, "gitops:no-diff")
				if len(s.GitOps.Refs) != 0 {
					t.Fatalf("no-diff scenario must not expose GitOps refs: %v", s.GitOps.Refs)
				}
				if len(s.Chain.PullRequestRefs) != 0 {
					t.Fatalf("no-diff scenario must not expose PR refs: %v", s.Chain.PullRequestRefs)
				}
			},
		},
		{
			name: "dirty-worktree-recovery-keeps-safe-category-and-retry-ref",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "gitops:dirty-scope", "recovery:dirty-scope")
				s.Automation.Status = "blocked"
				s.WorkPlan.Status = "blocked"
				s.WorkPlan.SafeNextAction = "clean allowed scope before retry"
				s.GitOps.FailureCategories = []string{"gitops:dirty-scope"}
				s.GitOps.Refs = append(s.GitOps.Refs, "recovery:dirty-scope")
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.GitOps.FailureCategories, "gitops:dirty-scope")
				requireRefs(t, s.GitOps.Refs, "recovery:dirty-scope")
			},
		},
		{
			name: "commit-push-pr-retry-exhaustion-is-terminal-and-metadata-only",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "gitops:retry-exhausted")
				s.Automation.Status = "failed"
				s.WorkPlan.Status = "failed"
				s.WorkPlan.SafeNextAction = "operator intervention required"
				s.WorkTasks[0].Status = "failed"
				s.Chain.Status = "failed"
				s.GitOps.FailureCategories = []string{"gitops:retry-exhausted"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.GitOps.FailureCategories, "gitops:retry-exhausted")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := fullPhase8Snapshot()
			durable := fullPhase8Snapshot()
			tc.mutate(&current)
			tc.mutate(&durable)
			comparison, err := CompareSnapshots(tc.name, current, durable)
			if err != nil {
				t.Fatalf("CompareSnapshots returned error: %v", err)
			}
			if !comparison.Equal() {
				t.Fatalf("expected equivalent gitops snapshots, got %v", comparison.Divergences)
			}
			tc.assert(t, current)
			mutatedDurable := durable
			mutatedDurable.GitOps.FailureCategories = append(mutatedDurable.GitOps.FailureCategories, "gitops:push-failed")
			comparison, err = CompareSnapshots(tc.name+"-changed-gitops-failure-category", current, mutatedDurable)
			if err != nil {
				t.Fatalf("CompareSnapshots returned error for divergent snapshot: %v", err)
			}
			if !contains(comparison.Divergences, "gitops") {
				t.Fatalf("expected gitops divergence, got %v", comparison.Divergences)
			}
		})
	}
}
