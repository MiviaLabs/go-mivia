package parity

import "testing"

func TestPhase8GovernanceParityScenarios(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
		assert func(*testing.T, Snapshot)
	}{
		{
			name: "evidence-confidence-knowledge-refs-preserved-with-no-auto-promotion",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs,
					"evidence:claim", "evidence:decision", "evidence:action", "evidence:outcome-verified", "confidence:promotion-gate", "knowledge:validated")
				s.Evidence.Refs = []string{"evidence:claim", "evidence:decision", "evidence:action", "evidence:outcome-verified"}
				s.Confidence.Refs = []string{"confidence:promotion-gate"}
				s.Knowledge.Refs = []string{"knowledge:validated"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.Evidence.Refs, "evidence:claim", "evidence:decision", "evidence:action", "evidence:outcome-verified")
				requireRefs(t, s.Confidence.Refs, "confidence:promotion-gate")
				requireRefs(t, s.Knowledge.Refs, "knowledge:validated")
			},
		},
		{
			name: "permission-snapshot-and-allowed-task-scope-stay-bounded",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "permission:snapshot-worker", "scope:allowed-task-refs")
				s.WorkTasks[0].ContextRefs = append(s.WorkTasks[0].ContextRefs, "permission:snapshot-worker", "scope:allowed-task-refs")
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ContextRefs, "permission:snapshot-worker", "scope:allowed-task-refs")
			},
		},
		{
			name: "runner-closeout-preserves-safe-handoff-and-recovery-packet",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "handoff:worker-closeout", "recovery:operator-packet")
				s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "handoff:worker-closeout", "recovery:operator-packet")
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ArtifactRefs, "handoff:worker-closeout", "recovery:operator-packet")
			},
		},
		{
			name: "metadata-only-blocker-summary-has-safe-next-action",
			mutate: func(s *Snapshot) {
				s.Automation.Status = "blocked"
				s.Automation.SafeSummary = "verifier failed with bounded category"
				s.WorkPlan.Status = "blocked"
				s.WorkPlan.SafeNextAction = "review verifier refs and retry bounded repair"
				s.WorkTasks[0].Status = "blocked"
			},
			assert: func(t *testing.T, s Snapshot) {
				if s.Automation.SafeSummary == "" || s.WorkPlan.SafeNextAction == "" {
					t.Fatalf("blocked governance scenario must preserve safe summary and next action: %#v", s)
				}
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
				t.Fatalf("expected equivalent governance snapshots, got %v", comparison.Divergences)
			}
			tc.assert(t, current)
			mutatedDurable := durable
			mutatedDurable.Evidence.Refs = nil
			comparison, err = CompareSnapshots(tc.name+"-missing-evidence-refs", current, mutatedDurable)
			if err != nil {
				t.Fatalf("CompareSnapshots returned error for divergent snapshot: %v", err)
			}
			if !contains(comparison.Divergences, "evidence") {
				t.Fatalf("expected evidence divergence, got %v", comparison.Divergences)
			}
		})
	}
}
