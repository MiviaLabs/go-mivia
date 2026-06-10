package parity

import "testing"

func TestPhase8WorkflowChainParityScenarios(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
		assert func(*testing.T, Snapshot)
	}{
		{
			name: "jira-local-ingested-journey-preserves-context-and-carried-task-refs",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs,
					"context:jira-summary", "context:jira-scope", "context:jira-evidence", "context:jira-anchors", "context:jira-verifier")
				s.WorkTasks[0].ContextRefs = []string{
					"context:jira-summary", "context:jira-scope", "context:jira-evidence", "context:jira-anchors", "context:jira-verifier",
				}
				s.Chain.StageStatuses["stage:post-validation"] = "done"
				s.Chain.StageRefs = append(s.Chain.StageRefs, "stage:post-validation")
				s.Chain.CarriedTaskRefs = []string{"task:implement", "task:post-validation"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ContextRefs,
					"context:jira-summary", "context:jira-scope", "context:jira-evidence", "context:jira-anchors", "context:jira-verifier")
				requireRefs(t, s.Chain.CarriedTaskRefs, "task:implement", "task:post-validation")
			},
		},
		{
			name: "safe-ref-journey-preserves-indexed-context-pack",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "context:indexed-safe-ref")
				s.WorkTasks[0].ContextRefs = []string{"context:indexed-safe-ref", "context:pack-1"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ContextRefs, "context:indexed-safe-ref", "context:pack-1")
			},
		},
		{
			name: "free-text-objective-journey-uses-derived-safe-ref-only",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "objective:7f5e3a9c2b11", "context:objective-pack")
				s.WorkTasks[0].ContextRefs = []string{"objective:7f5e3a9c2b11", "context:objective-pack"}
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ContextRefs, "objective:7f5e3a9c2b11", "context:objective-pack")
			},
		},
		{
			name: "activation-checkpoint-blocks-with-exact-recovery-metadata",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "checkpoint:queue-observe", "recovery:activation-retry")
				s.Automation.Status = "blocked"
				s.WorkPlan.Status = "blocked"
				s.WorkPlan.SafeNextAction = "retry activation checkpoint queue observe"
				s.WorkTasks[0].Status = "blocked"
				s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "checkpoint:queue-observe", "recovery:activation-retry")
				s.Chain.Status = "blocked"
				s.Chain.StageStatuses["stage:implementation"] = "blocked"
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ArtifactRefs, "checkpoint:queue-observe", "recovery:activation-retry")
			},
		},
		{
			name: "orphan-compiled-stage-blocks-with-repair-packet",
			mutate: func(s *Snapshot) {
				s.KnownRefs = append(s.KnownRefs, "checkpoint:compiled-not-activated", "recovery:release-or-retry")
				s.WorkPlan.Status = "planned"
				s.WorkPlan.SafeNextAction = "repair compiled stage before activation"
				s.Chain.Status = "blocked"
				s.Chain.StageStatuses["stage:implementation"] = "blocked"
				s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "checkpoint:compiled-not-activated", "recovery:release-or-retry")
			},
			assert: func(t *testing.T, s Snapshot) {
				requireRefs(t, s.WorkTasks[0].ArtifactRefs, "checkpoint:compiled-not-activated", "recovery:release-or-retry")
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
				t.Fatalf("expected equivalent workflow-chain snapshots, got %v", comparison.Divergences)
			}
			tc.assert(t, current)
			mutatedDurable := durable
			mutatedDurable.Chain.CarriedTaskRefs = nil
			comparison, err = CompareSnapshots(tc.name+"-missing-carried-refs", current, mutatedDurable)
			if err != nil {
				t.Fatalf("CompareSnapshots returned error for divergent snapshot: %v", err)
			}
			if !contains(comparison.Divergences, "workflow_chain") {
				t.Fatalf("expected workflow_chain divergence, got %v", comparison.Divergences)
			}
		})
	}
}
