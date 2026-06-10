package parity

import "testing"

func TestPhase8WorkPlanParityScenarios(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
		assert func(*testing.T, Snapshot)
	}{
		{
			name: "isolated-workplan-and-task-contract-refs-survive-resume",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs,
					"isolation:dedicated-worktree", "workspace:wt-1", "branch:mivia-durable-parity", "acceptance:criteria", "stop:condition", "downstream:impact")
				s.WorkPlan.SafeNextAction = "resume isolated task from persisted metadata"
				s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs,
					"isolation:dedicated-worktree", "workspace:wt-1", "branch:mivia-durable-parity")
				s.WorkTasks[0].ContextRefs = append(s.WorkTasks[0].ContextRefs, "acceptance:criteria", "stop:condition", "downstream:impact")
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ArtifactRefs, "isolation:dedicated-worktree", "workspace:wt-1", "branch:mivia-durable-parity")
				requireRefs(t, s.WorkTasks[0].ContextRefs, "acceptance:criteria", "stop:condition", "downstream:impact")
			},
		},
		{
			name: "dependency-release-and-attachment-aggregation",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "claim:dependency-release", "knowledge:no-reusable")
				s.WorkTasks[0].DependencyRefs = []string{"task:decompose", "task:bootstrap"}
				s.Automation.ClaimRefs = []string{"claim:dependency-release"}
				s.Knowledge.Refs = []string{"knowledge:no-reusable"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].DependencyRefs, "task:decompose", "task:bootstrap")
				requireRefs(t, s.Automation.ClaimRefs, "claim:dependency-release")
				requireRefs(t, s.Knowledge.Refs, "knowledge:no-reusable")
			},
		},
		{
			name: "review-and-verifier-gates-block-before-done",
			mutate: func(s *Snapshot) {
				s.Automation.Status = "verifying"
				s.WorkPlan.Status = "needs_review"
				s.WorkPlan.SafeNextAction = "await independent review and verifier refs"
				s.WorkTasks[0].Status = "verifying"
				s.WorkTasks[0].VerifierRefs = []string{"verifier:unit"}
				s.WorkTasks[0].ReviewRefs = []string{"review:independent"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].VerifierRefs, "verifier:unit")
				requireRefs(t, s.WorkTasks[0].ReviewRefs, "review:independent")
			},
		},
		{
			name: "parallel-batch-conflict-and-missing-ladder-blocked",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "parallel:conflict", "verifier:missing-ladder")
				s.Automation.Status = "blocked"
				s.WorkPlan.Status = "blocked"
				s.WorkPlan.SafeNextAction = "split conflicting parallel batch before retry"
				s.WorkTasks[0].Status = "blocked"
				s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "parallel:conflict", "verifier:missing-ladder")
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ArtifactRefs, "parallel:conflict", "verifier:missing-ladder")
			},
		},
		{
			name: "parallel-batch-non-disjoint-promotion-blocked",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "parallel:non-disjoint-promotion", "knowledge:shared-candidate")
				s.Automation.Status = "blocked"
				s.WorkPlan.Status = "blocked"
				s.WorkPlan.SafeNextAction = "split non disjoint knowledge promotion before retry"
				s.WorkTasks[0].Status = "blocked"
				s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "parallel:non-disjoint-promotion")
				s.Knowledge.Refs = []string{"knowledge:shared-candidate"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ArtifactRefs, "parallel:non-disjoint-promotion")
				requireRefs(t, s.Knowledge.Refs, "knowledge:shared-candidate")
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
				t.Fatalf("expected equivalent workplan snapshots, got %v", comparison.Divergences)
			}
			tc.assert(t, current)
			mutatedDurable := durable
			mutatedDurable.WorkTasks[0].VerifierRefs = nil
			comparison, err = CompareSnapshots(tc.name+"-missing-verifier-refs", current, mutatedDurable)
			if err != nil {
				t.Fatalf("CompareSnapshots returned error for divergent snapshot: %v", err)
			}
			if !contains(comparison.Divergences, "work_tasks") {
				t.Fatalf("expected work_tasks divergence, got %v", comparison.Divergences)
			}
		})
	}
}
