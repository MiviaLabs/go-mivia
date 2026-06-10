package projectevidence_test

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
)

func TestBaselineEvidenceGraphBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := projectevidence.New(store.NewMemoryStore())
	claim, err := svc.CreateClaim(ctx, projectevidence.CreateClaimInput{
		ProjectID: "project-1",
		RunID:     "run-1",
		TraceID:   "trace-1",
		ClaimRef:  "claim/phase0",
		Summary:   "metadata-only claim",
	})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}
	if _, err := svc.AppendEvidence(ctx, projectevidence.AppendEvidenceInput{
		ProjectID:    "project-1",
		ClaimID:      claim.ID,
		EvidenceRef:  "evidence/context",
		EvidenceKind: projectevidence.EvidenceKindContextPack,
		Summary:      "bounded context ref",
	}); err != nil {
		t.Fatalf("append context evidence: %v", err)
	}
	decision, err := svc.CreateDecision(ctx, projectevidence.CreateDecisionInput{
		ProjectID:   "project-1",
		ClaimID:     claim.ID,
		DecisionRef: "decision/validated",
		State:       projectevidence.DecisionStateValidated,
		VerifierRef: "verifier/phase0",
		Rationale:   "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	action, err := svc.CreateAction(ctx, projectevidence.CreateActionInput{
		ProjectID:    "project-1",
		ClaimID:      claim.ID,
		DecisionID:   decision.ID,
		ActionRef:    "action/code-change",
		ActionKind:   projectevidence.ActionKindCodeChange,
		Summary:      "bounded metadata action",
		ChangedFiles: []string{"internal/projectevidence/baseline_behavior_test.go"},
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	outcome, err := svc.CreateOutcome(ctx, projectevidence.CreateOutcomeInput{
		ProjectID:   "project-1",
		ClaimID:     claim.ID,
		ActionID:    action.ID,
		OutcomeRef:  "outcome/passed",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusPassed,
		VerifierRef: "verifier/phase0",
		Summary:     "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("create outcome: %v", err)
	}
	if _, err := svc.LinkArtifact(ctx, projectevidence.LinkArtifactInput{
		ProjectID:    "project-1",
		ClaimID:      claim.ID,
		ArtifactRef:  "artifact/phase0",
		ArtifactKind: "baseline",
		RunID:        "run-1",
	}); err != nil {
		t.Fatalf("link artifact: %v", err)
	}
	if _, err := svc.LinkPromotion(ctx, projectevidence.LinkPromotionInput{
		ProjectID:      "project-1",
		ClaimID:        claim.ID,
		ArtifactRef:    "artifact/phase0",
		PromotionState: projectevidence.PromotionStatePromoted,
		SourceRef:      action.ID,
		VerifierRef:    "verifier/phase0",
		DecisionRef:    decision.DecisionRef,
		OutcomeRef:     outcome.OutcomeRef,
	}); err != nil {
		t.Fatalf("link promotion: %v", err)
	}
	record, err := svc.GetClaim(ctx, "project-1", claim.ID)
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if len(record.Evidence) != 1 || len(record.Decisions) != 1 || len(record.Actions) != 1 || len(record.Outcomes) != 1 || len(record.ArtifactLinks) != 1 || len(record.PromotionLinks) != 1 {
		t.Fatalf("evidence graph record lost linked refs: %#v", record)
	}
}
