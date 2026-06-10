package parity

import (
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
)

func TestPhase8HarnessComparesEveryRequiredParitySurface(t *testing.T) {
	current := fullPhase8Snapshot()
	durable := fullPhase8Snapshot()
	comparison, err := CompareSnapshots("phase8-full-success", current, durable)
	if err != nil {
		t.Fatalf("CompareSnapshots returned error: %v", err)
	}
	if !comparison.Equal() {
		t.Fatalf("expected equivalent snapshots, got divergences %v", comparison.Divergences)
	}

	cases := []struct {
		name       string
		mutate     func(*Snapshot)
		divergence string
	}{
		{"automation status", func(s *Snapshot) { s.Automation.Status = "failed" }, "automation"},
		{"automation failure category", func(s *Snapshot) { s.Automation.FailureCategory = string(projectdurable.FailureCategoryTimeout) }, "automation"},
		{"automation attempts", func(s *Snapshot) { s.Automation.AttemptCount++ }, "automation"},
		{"automation claim refs", func(s *Snapshot) { s.Automation.ClaimRefs = append(s.Automation.ClaimRefs, "claim:retry-1") }, "automation"},
		{"work plan status", func(s *Snapshot) { s.WorkPlan.Status = "blocked" }, "work_plan"},
		{"work plan next action", func(s *Snapshot) { s.WorkPlan.SafeNextAction = "operator repair required" }, "work_plan"},
		{"work task status", func(s *Snapshot) { s.WorkTasks[0].Status = "failed" }, "work_tasks"},
		{"work task dependency refs", func(s *Snapshot) {
			s.WorkTasks[0].DependencyRefs = append(s.WorkTasks[0].DependencyRefs, "task:bootstrap")
		}, "work_tasks"},
		{"work task verifier refs", func(s *Snapshot) { s.WorkTasks[0].VerifierRefs = append(s.WorkTasks[0].VerifierRefs, "verifier:retry") }, "work_tasks"},
		{"work task review refs", func(s *Snapshot) { s.WorkTasks[0].ReviewRefs = append(s.WorkTasks[0].ReviewRefs, "review:retry") }, "work_tasks"},
		{"work task evidence refs", func(s *Snapshot) { s.WorkTasks[0].EvidenceRefs = append(s.WorkTasks[0].EvidenceRefs, "evidence:retry") }, "work_tasks"},
		{"work task context refs", func(s *Snapshot) { s.WorkTasks[0].ContextRefs = append(s.WorkTasks[0].ContextRefs, "context:retry") }, "work_tasks"},
		{"work task artifact refs", func(s *Snapshot) { s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "artifact:retry") }, "work_tasks"},
		{"chain status", func(s *Snapshot) { s.Chain.Status = "blocked" }, "workflow_chain"},
		{"chain stage status", func(s *Snapshot) { s.Chain.StageStatuses["stage:implementation"] = "blocked" }, "workflow_chain"},
		{"chain stage refs", func(s *Snapshot) { s.Chain.StageRefs = append(s.Chain.StageRefs, "stage:recovery") }, "workflow_chain"},
		{"chain carried task refs", func(s *Snapshot) { s.Chain.CarriedTaskRefs = append(s.Chain.CarriedTaskRefs, "task:post-validation") }, "workflow_chain"},
		{"chain gitops readiness", func(s *Snapshot) { s.Chain.GitOpsReady = false }, "workflow_chain"},
		{"chain PR refs", func(s *Snapshot) { s.Chain.PullRequestRefs = append(s.Chain.PullRequestRefs, "pr:draft-2") }, "workflow_chain"},
		{"gitops refs", func(s *Snapshot) { s.GitOps.Refs = append(s.GitOps.Refs, "gitops:recovery") }, "gitops"},
		{"gitops failure categories", func(s *Snapshot) {
			s.GitOps.FailureCategories = append(s.GitOps.FailureCategories, "gitops:push-failed")
		}, "gitops"},
		{"evidence refs", func(s *Snapshot) { s.Evidence.Refs = append(s.Evidence.Refs, "evidence:outcome-2") }, "evidence"},
		{"confidence refs", func(s *Snapshot) { s.Confidence.Refs = append(s.Confidence.Refs, "confidence:medium") }, "confidence"},
		{"knowledge refs", func(s *Snapshot) { s.Knowledge.Refs = append(s.Knowledge.Refs, "knowledge:candidate-2") }, "knowledge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := fullPhase8Snapshot()
			tc.mutate(&mutated)
			comparison, err := CompareSnapshots(tc.name, current, mutated)
			if err != nil {
				t.Fatalf("CompareSnapshots returned error: %v", err)
			}
			if !contains(comparison.Divergences, tc.divergence) {
				t.Fatalf("expected divergence %q, got %v", tc.divergence, comparison.Divergences)
			}
		})
	}
}

func TestPhase8HarnessRejectsInvalidMissingDuplicateAndStaleMetadata(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"invalid status whitespace", func(s *Snapshot) { s.Automation.Status = "queued now" }},
		{"missing work plan status", func(s *Snapshot) { s.WorkPlan.Status = "" }},
		{"missing work task metadata", func(s *Snapshot) { s.WorkTasks = nil }},
		{"duplicate dependency ref", func(s *Snapshot) { s.WorkTasks[0].DependencyRefs = []string{"task:decompose", "task:decompose"} }},
		{"duplicate known ref", func(s *Snapshot) { s.KnownRefs = append(s.KnownRefs, "task:implement") }},
		{"stale ref", func(s *Snapshot) { s.WorkTasks[0].ArtifactRefs = append(s.WorkTasks[0].ArtifactRefs, "artifact:stale") }},
		{"blocked terminal state without safe summary", func(s *Snapshot) { s.WorkPlan.SafeNextAction = "/home/operator/private" }},
		{"unknown failure category", func(s *Snapshot) { s.Automation.FailureCategory = "provider-raw-error" }},
		{"negative attempt count", func(s *Snapshot) { s.Automation.AttemptCount = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := fullPhase8Snapshot()
			tc.mutate(&snapshot)
			if err := snapshot.Validate(); err == nil {
				t.Fatalf("expected validation to fail")
			}
		})
	}
}

func TestPhase8HarnessRejectsUnsafeMetadataEverywhere(t *testing.T) {
	unsafeValues := map[string]string{
		"raw prompt":       "raw_prompt: build the app",
		"completion":       "raw_completion: done",
		"raw stderr":       "raw_stderr: stack trace",
		"source dump":      "source_dump package main",
		"provider payload": "provider_payload json",
		"secret token":     "token=abc123",
		"filesystem root":  "/home/operator/private",
		"external URL":     "https://example.invalid/pr/1",
		"email PII":        "owner@example.com",
	}
	for name, value := range unsafeValues {
		t.Run(name+" summary", func(t *testing.T) {
			snapshot := fullPhase8Snapshot()
			snapshot.Automation.SafeSummary = value
			if err := snapshot.Validate(); err == nil {
				t.Fatalf("expected unsafe summary to fail")
			}
		})
		t.Run(name+" ref", func(t *testing.T) {
			snapshot := fullPhase8Snapshot()
			snapshot.KnownRefs = nil
			snapshot.WorkTasks[0].EvidenceRefs = []string{strings.ReplaceAll(value, " ", "_")}
			if err := snapshot.Validate(); err == nil {
				t.Fatalf("expected unsafe ref to fail")
			}
		})
	}
}

func TestPhase8HarnessProvesControlFlowIsShadowOnlyAndComparisonLast(t *testing.T) {
	valid := fullPhase8Snapshot()
	valid.Control = ControlFlowSnapshot{
		WorkerEnabled: true,
		Events: []TraceEvent{
			{Kind: "runner_claim"},
			{Kind: "runner_execute"},
			{Kind: "runner_heartbeat"},
			{Kind: "runner_report"},
			{Kind: "durable_comparison"},
		},
		DurableMutations: []Mutation{{Ref: "shadow:comparison-write", ApprovedShadowPort: true, MatchesCurrentPath: true}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid shadow-only control flow failed: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"comparison not last", func(s *Snapshot) {
			s.Control.Events = append(s.Control.Events, TraceEvent{Kind: "authoritative_write"})
		}},
		{"comparison failure changed authoritative result", func(s *Snapshot) {
			s.Control.ComparisonError = "comparison failed"
			s.Control.AuthoritativeResultChanged = true
		}},
		{"worker enabled durable authoritative", func(s *Snapshot) { s.Control.DurableExecutionAuthoritative = true }},
		{"durable mutation without approved port", func(s *Snapshot) { s.Control.DurableMutations[0].ApprovedShadowPort = false }},
		{"durable mutation without current path match", func(s *Snapshot) { s.Control.DurableMutations[0].MatchesCurrentPath = false }},
		{"runner order changed", func(s *Snapshot) {
			s.Control.Events = []TraceEvent{{Kind: "runner_claim"}, {Kind: "runner_report"}, {Kind: "runner_execute"}, {Kind: "durable_comparison"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := valid
			tc.mutate(&snapshot)
			if err := snapshot.Validate(); err == nil {
				t.Fatalf("expected control-flow validation to fail")
			}
		})
	}
}

func TestPhase8HarnessRepresentsFailureRecoveryAndDownstreamHandoffs(t *testing.T) {
	current := fullPhase8Snapshot()
	current.Automation.Status = "blocked"
	current.Automation.FailureCategory = string(projectdurable.FailureCategoryRunnerUnavailable)
	current.Automation.SafeSummary = "runner unavailable retry is bounded"
	current.WorkPlan.Status = "blocked"
	current.WorkPlan.SafeNextAction = "retry runner claim after lease expiry"
	current.WorkTasks[0].Status = "blocked"
	current.Chain.Status = "blocked"
	current.Chain.StageStatuses["stage:implementation"] = "blocked"
	current.GitOps.FailureCategories = append(current.GitOps.FailureCategories, "gitops:retryable")

	durable := current
	comparison, err := CompareSnapshots("phase8-failure-recovery", current, durable)
	if err != nil {
		t.Fatalf("CompareSnapshots returned error: %v", err)
	}
	if !comparison.Equal() {
		t.Fatalf("expected failure/recovery representation to match, got %v", comparison.Divergences)
	}
	if len(current.Chain.CarriedTaskRefs) == 0 || len(current.WorkTasks[0].ArtifactRefs) == 0 {
		t.Fatalf("scenario must preserve downstream handoff refs: %#v", current)
	}
}

func fullPhase8Snapshot() Snapshot {
	refs := []string{
		"queued", "active", "done", "completed", "failed", "blocked",
		"claim:run-1", "runner:external-1", "task:bootstrap", "task:decompose", "task:implement", "task:post-validation",
		"verifier:unit", "review:independent", "evidence:commit", "evidence:outcome", "context:pack-1", "artifact:handoff", "artifact:retry-packet",
		"stage:decomposition", "stage:implementation", "stage:post-validation", "pr:draft-1", "pr:draft-2", "gitops:commit", "gitops:push", "gitops:pr", "gitops:retryable", "gitops:recovery", "gitops:push-failed",
		"confidence:high", "confidence:medium", "knowledge:candidate-1", "knowledge:candidate-2", "shadow:comparison-write", "verifier:retry", "review:retry", "evidence:retry", "evidence:outcome-2", "context:retry", "artifact:retry", "claim:retry-1", "stage:recovery",
	}
	return Snapshot{
		KnownRefs: refs,
		Automation: AutomationRunSnapshot{
			Status:       "completed",
			SafeSummary:  "completed with metadata only",
			AttemptCount: 2,
			ClaimRefs:    []string{"claim:run-1"},
			RunnerRefs:   []string{"runner:external-1"},
		},
		WorkPlan: WorkPlanSnapshot{Status: "done", SafeNextAction: "ready for operator review"},
		WorkTasks: []WorkTaskSnapshot{{
			TaskRef:        "task:implement",
			Status:         "done",
			DependencyRefs: []string{"task:decompose"},
			VerifierRefs:   []string{"verifier:unit"},
			ReviewRefs:     []string{"review:independent"},
			EvidenceRefs:   []string{"evidence:commit", "evidence:outcome"},
			ContextRefs:    []string{"context:pack-1"},
			ArtifactRefs:   []string{"artifact:handoff", "artifact:retry-packet"},
		}},
		Chain: ChainSnapshot{
			Status: "completed",
			StageStatuses: map[string]string{
				"stage:decomposition":  "done",
				"stage:implementation": "done",
			},
			StageRefs:       []string{"stage:decomposition", "stage:implementation"},
			CarriedTaskRefs: []string{"task:implement"},
			GitOpsReady:     true,
			PullRequestRefs: []string{"pr:draft-1"},
		},
		GitOps:     GitOpsSnapshot{Refs: []string{"gitops:commit", "gitops:push", "gitops:pr"}},
		Evidence:   EvidenceSnapshot{Refs: []string{"evidence:commit", "evidence:outcome"}},
		Confidence: ConfidenceSnapshot{Refs: []string{"confidence:high"}},
		Knowledge:  KnowledgeSnapshot{Refs: []string{"knowledge:candidate-1"}},
		Control: ControlFlowSnapshot{Events: []TraceEvent{
			{Kind: "runner_claim"},
			{Kind: "runner_execute"},
			{Kind: "runner_heartbeat"},
			{Kind: "runner_report"},
			{Kind: "durable_comparison"},
		}},
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
