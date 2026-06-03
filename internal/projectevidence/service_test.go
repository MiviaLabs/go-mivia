package projectevidence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
)

func TestCreateClaim_SafeMetadataOnly_PersistsClaim(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())

	claim, err := svc.CreateClaim(context.Background(), projectevidence.CreateClaimInput{
		ProjectID: "example-service",
		RunID:     "agent_run_1",
		TraceID:   "trace_1",
		ClaimRef:  "claim/build-passed",
		Summary:   "focused verifier reported passing status",
	})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}
	if claim.Status != projectevidence.ClaimStatusCandidate || claim.Summary != "focused verifier reported passing status" {
		t.Fatalf("unexpected claim: %#v", claim)
	}

	record, err := svc.GetClaim(context.Background(), "example-service", claim.ID)
	if err != nil {
		t.Fatalf("get claim: %v", err)
	}
	if record.Claim.ID != claim.ID || record.Claim.ProjectID != "example-service" {
		t.Fatalf("unexpected record: %#v", record)
	}
}

func TestCreateClaim_RejectsUnsafeTextAndRefs(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())

	cases := []projectevidence.CreateClaimInput{
		{ProjectID: "example-service", ClaimRef: "claim/raw", Summary: "raw prompt: inspect the project"},
		{ProjectID: "example-service", ClaimRef: "claim/source", Summary: "package main\nfunc main() {}"},
		{ProjectID: "example-service", ClaimRef: "claim/secret", Summary: "Authorization: bearer value"},
		{ProjectID: "example-service", ClaimRef: "claim/contact", Summary: "Contact owner at user@example.invalid"},
		{ProjectID: "example-service", ClaimRef: "claim/phone", Summary: "Call +1 555 123 4567"},
		{ProjectID: "/home/mac/project", ClaimRef: "claim/root", Summary: "bounded metadata"},
		{ProjectID: "wsl.localhost/Ubuntu/home/mac/project", ClaimRef: "claim/root", Summary: "bounded metadata"},
		{ProjectID: "example-service", ClaimRef: "../claim", Summary: "bounded metadata"},
	}
	for _, input := range cases {
		_, err := svc.CreateClaim(context.Background(), input)
		if !errors.Is(err, projectevidence.ErrInvalidInput) {
			t.Fatalf("expected invalid input for %#v, got %v", input, err)
		}
	}
}

func TestCreateDecision_RequiresEvidenceVerifierAndRationale(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim := createClaim(t, svc)

	_, err := svc.CreateDecision(context.Background(), projectevidence.CreateDecisionInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		DecisionRef: "decision/validated",
		State:       projectevidence.DecisionStateValidated,
		VerifierRef: "go/test/internal/projectevidence",
		Rationale:   "focused verifier passed",
	})
	if !errors.Is(err, projectevidence.ErrInvalidInput) {
		t.Fatalf("expected missing evidence to be invalid, got %v", err)
	}

	_, err = svc.AppendEvidence(context.Background(), projectevidence.AppendEvidenceInput{
		ProjectID:    claim.ProjectID,
		ClaimID:      claim.ID,
		EvidenceRef:  "context_pack/claim-1",
		EvidenceKind: projectevidence.EvidenceKindContextPack,
		Summary:      "bounded context pack selected relevant files",
	})
	if err != nil {
		t.Fatalf("append evidence: %v", err)
	}

	for _, input := range []projectevidence.CreateDecisionInput{
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, DecisionRef: "decision/no-verifier", State: projectevidence.DecisionStateValidated, Rationale: "focused verifier passed"},
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, DecisionRef: "decision/no-rationale", State: projectevidence.DecisionStateRejected, VerifierRef: "go/test/internal/projectevidence"},
	} {
		_, err := svc.CreateDecision(context.Background(), input)
		if !errors.Is(err, projectevidence.ErrInvalidInput) {
			t.Fatalf("expected decision input to be invalid for %#v, got %v", input, err)
		}
	}
}

func TestCreateOutcome_RequiresExistingAction(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim, decision := createClaimWithDecision(t, svc)

	_, err := svc.CreateOutcome(context.Background(), projectevidence.CreateOutcomeInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		ActionID:    "action_missing",
		OutcomeRef:  "outcome/test",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusPassed,
	})
	if !errors.Is(err, projectevidence.ErrInvalidInput) {
		t.Fatalf("expected missing action to be invalid, got %v", err)
	}

	action, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
		ProjectID:    claim.ProjectID,
		ClaimID:      claim.ID,
		DecisionID:   decision.ID,
		ActionRef:    "action/code-change",
		ActionKind:   projectevidence.ActionKindCodeChange,
		Summary:      "changed metadata validation only",
		ChangedFiles: []string{"internal/projectevidence/service.go"},
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}

	outcome, err := svc.CreateOutcome(context.Background(), projectevidence.CreateOutcomeInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		ActionID:    action.ID,
		OutcomeRef:  "outcome/test",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusPassed,
		VerifierRef: "go/test/internal/projectevidence",
		Summary:     "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("create outcome: %v", err)
	}
	if outcome.Status != projectevidence.OutcomeStatusPassed {
		t.Fatalf("unexpected outcome: %#v", outcome)
	}
}

func TestCreateAction_RejectsUnsafeChangedFiles(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim, decision := createClaimWithDecision(t, svc)

	for _, changedFile := range []string{
		"internal/projectevidence/service.go\nfunc main() {}",
		"internal/projectevidence/service.go\tsecret",
		"internal/projectevidence/service.go```",
		"internal/projectevidence/service.go;rm",
	} {
		_, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
			ProjectID:    claim.ProjectID,
			ClaimID:      claim.ID,
			DecisionID:   decision.ID,
			ActionRef:    "action/unsafe-file",
			ActionKind:   projectevidence.ActionKindCodeChange,
			Summary:      "changed metadata validation only",
			ChangedFiles: []string{changedFile},
		})
		if !errors.Is(err, projectevidence.ErrInvalidInput) {
			t.Fatalf("expected changed file %q to be invalid, got %v", changedFile, err)
		}
	}
}

func TestLinkPromotion_PromotedRequiresPassedOutcome(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim, decision := createClaimWithDecision(t, svc)
	action, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
		ProjectID:  claim.ProjectID,
		ClaimID:    claim.ID,
		DecisionID: decision.ID,
		ActionRef:  "action/verifier",
		ActionKind: projectevidence.ActionKindVerifierRun,
		Summary:    "recorded verifier metadata",
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	if _, err := svc.LinkArtifact(context.Background(), projectevidence.LinkArtifactInput{
		ProjectID:    claim.ProjectID,
		ClaimID:      claim.ID,
		ArtifactRef:  "artifact/finding-1",
		ArtifactKind: "finding",
		RunID:        "agent_run_1",
	}); err != nil {
		t.Fatalf("link artifact: %v", err)
	}

	_, err = svc.LinkPromotion(context.Background(), projectevidence.LinkPromotionInput{
		ProjectID:      claim.ProjectID,
		ClaimID:        claim.ID,
		ArtifactRef:    "artifact/finding-1",
		PromotionState: projectevidence.PromotionStatePromoted,
		SourceRef:      action.ID,
		VerifierRef:    "go/test/internal/projectevidence",
		DecisionRef:    decision.DecisionRef,
	})
	if !errors.Is(err, projectevidence.ErrInvalidInput) {
		t.Fatalf("expected promoted link without passed outcome to be invalid, got %v", err)
	}

	outcome, err := svc.CreateOutcome(context.Background(), projectevidence.CreateOutcomeInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		ActionID:    action.ID,
		OutcomeRef:  "outcome/focused-test",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusPassed,
		VerifierRef: "go/test/internal/projectevidence",
		Summary:     "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("create outcome: %v", err)
	}

	link, err := svc.LinkPromotion(context.Background(), projectevidence.LinkPromotionInput{
		ProjectID:      claim.ProjectID,
		ClaimID:        claim.ID,
		RunID:          "agent_run_1",
		ArtifactRef:    "artifact/finding-1",
		PromotionState: projectevidence.PromotionStatePromoted,
		SourceRef:      action.ID,
		VerifierRef:    "go/test/internal/projectevidence",
		DecisionRef:    decision.DecisionRef,
		ActionRef:      action.ActionRef,
		OutcomeRef:     outcome.OutcomeRef,
	})
	if err != nil {
		t.Fatalf("link promotion: %v", err)
	}
	if link.PromotionState != projectevidence.PromotionStatePromoted || link.RunID != "agent_run_1" || link.ActionRef != action.ActionRef || link.OutcomeRef != outcome.OutcomeRef {
		t.Fatalf("unexpected promotion link: %#v", link)
	}
}

func TestLinkPromotion_ValidatesActionAndOutcomeRefs(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim, decision := createClaimWithDecision(t, svc)
	action, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
		ProjectID:  claim.ProjectID,
		ClaimID:    claim.ID,
		DecisionID: decision.ID,
		ActionRef:  "action/verifier",
		ActionKind: projectevidence.ActionKindVerifierRun,
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	outcome, err := svc.CreateOutcome(context.Background(), projectevidence.CreateOutcomeInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		ActionID:    action.ID,
		OutcomeRef:  "outcome/focused-test",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusFailed,
	})
	if err != nil {
		t.Fatalf("create outcome: %v", err)
	}
	if _, err := svc.LinkArtifact(context.Background(), projectevidence.LinkArtifactInput{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1"}); err != nil {
		t.Fatalf("link artifact: %v", err)
	}

	for _, input := range []projectevidence.LinkPromotionInput{
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1", PromotionState: projectevidence.PromotionStateCandidate, SourceRef: "agent-run-promotion", ActionRef: "action/missing"},
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1", PromotionState: projectevidence.PromotionStateCandidate, SourceRef: "agent-run-promotion", OutcomeRef: "outcome/missing"},
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "agent-run-promotion", VerifierRef: "go/test/internal/projectevidence", DecisionRef: decision.DecisionRef, ActionRef: action.ActionRef, OutcomeRef: outcome.OutcomeRef},
	} {
		if _, err := svc.LinkPromotion(context.Background(), input); !errors.Is(err, projectevidence.ErrInvalidInput) {
			t.Fatalf("expected invalid promotion refs for %#v, got %v", input, err)
		}
	}
}

func TestLinkPromotion_RejectsMismatchedDecisionActionOutcomeChain(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim, decision := createClaimWithDecision(t, svc)
	action, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
		ProjectID:  claim.ProjectID,
		ClaimID:    claim.ID,
		DecisionID: decision.ID,
		ActionRef:  "action/verifier",
		ActionKind: projectevidence.ActionKindVerifierRun,
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	outcome, err := svc.CreateOutcome(context.Background(), projectevidence.CreateOutcomeInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		ActionID:    action.ID,
		OutcomeRef:  "outcome/focused-test",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusPassed,
	})
	if err != nil {
		t.Fatalf("create outcome: %v", err)
	}
	secondDecision, err := svc.CreateDecision(context.Background(), projectevidence.CreateDecisionInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		DecisionRef: "decision/second",
		State:       projectevidence.DecisionStateValidated,
		VerifierRef: "go/test/internal/projectevidence",
		Rationale:   "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("create second decision: %v", err)
	}
	secondAction, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
		ProjectID:  claim.ProjectID,
		ClaimID:    claim.ID,
		DecisionID: secondDecision.ID,
		ActionRef:  "action/second",
		ActionKind: projectevidence.ActionKindVerifierRun,
	})
	if err != nil {
		t.Fatalf("create second action: %v", err)
	}
	if _, err := svc.LinkArtifact(context.Background(), projectevidence.LinkArtifactInput{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1"}); err != nil {
		t.Fatalf("link artifact: %v", err)
	}

	for _, input := range []projectevidence.LinkPromotionInput{
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1", PromotionState: projectevidence.PromotionStateValidated, SourceRef: "agent-run-promotion", VerifierRef: "go/test/internal/projectevidence", DecisionRef: "decision/missing", ActionRef: action.ActionRef},
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "agent-run-promotion", VerifierRef: "go/test/internal/projectevidence", DecisionRef: secondDecision.DecisionRef, ActionRef: action.ActionRef, OutcomeRef: outcome.OutcomeRef},
		{ProjectID: claim.ProjectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-1", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: "agent-run-promotion", VerifierRef: "go/test/internal/projectevidence", DecisionRef: decision.DecisionRef, ActionRef: secondAction.ActionRef, OutcomeRef: outcome.OutcomeRef},
	} {
		if _, err := svc.LinkPromotion(context.Background(), input); !errors.Is(err, projectevidence.ErrInvalidInput) {
			t.Fatalf("expected mismatched promotion chain for %#v to be invalid, got %v", input, err)
		}
	}
}

func TestLinkPromotion_RequiresExistingArtifactLink(t *testing.T) {
	svc := projectevidence.New(store.NewMemoryStore())
	claim, decision := createClaimWithDecision(t, svc)
	action, err := svc.CreateAction(context.Background(), projectevidence.CreateActionInput{
		ProjectID:  claim.ProjectID,
		ClaimID:    claim.ID,
		DecisionID: decision.ID,
		ActionRef:  "action/verifier",
		ActionKind: projectevidence.ActionKindVerifierRun,
		Summary:    "recorded verifier metadata",
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	if _, err := svc.CreateOutcome(context.Background(), projectevidence.CreateOutcomeInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		ActionID:    action.ID,
		OutcomeRef:  "outcome/focused-test",
		OutcomeKind: projectevidence.OutcomeKindTest,
		Status:      projectevidence.OutcomeStatusPassed,
		VerifierRef: "go/test/internal/projectevidence",
		Summary:     "focused verifier passed",
	}); err != nil {
		t.Fatalf("create outcome: %v", err)
	}

	_, err = svc.LinkPromotion(context.Background(), projectevidence.LinkPromotionInput{
		ProjectID:      claim.ProjectID,
		ClaimID:        claim.ID,
		ArtifactRef:    "artifact/missing",
		PromotionState: projectevidence.PromotionStatePromoted,
		SourceRef:      action.ID,
		VerifierRef:    "go/test/internal/projectevidence",
		DecisionRef:    decision.DecisionRef,
		ActionRef:      action.ActionRef,
		OutcomeRef:     "outcome/focused-test",
	})
	if !errors.Is(err, projectevidence.ErrInvalidInput) {
		t.Fatalf("expected missing artifact link to be invalid, got %v", err)
	}
}

func createClaim(t *testing.T, svc *projectevidence.Service) projectevidence.Claim {
	t.Helper()
	claim, err := svc.CreateClaim(context.Background(), projectevidence.CreateClaimInput{
		ProjectID: "example-service",
		RunID:     "agent_run_1",
		TraceID:   "trace_1",
		ClaimRef:  "claim/1",
		Summary:   "bounded evidence graph metadata",
	})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}
	return claim
}

func createClaimWithDecision(t *testing.T, svc *projectevidence.Service) (projectevidence.Claim, projectevidence.Decision) {
	t.Helper()
	claim := createClaim(t, svc)
	_, err := svc.AppendEvidence(context.Background(), projectevidence.AppendEvidenceInput{
		ProjectID:    claim.ProjectID,
		ClaimID:      claim.ID,
		EvidenceRef:  "context_pack/claim-1",
		EvidenceKind: projectevidence.EvidenceKindContextPack,
		Summary:      "bounded context pack selected relevant files",
	})
	if err != nil {
		t.Fatalf("append evidence: %v", err)
	}
	decision, err := svc.CreateDecision(context.Background(), projectevidence.CreateDecisionInput{
		ProjectID:   claim.ProjectID,
		ClaimID:     claim.ID,
		DecisionRef: "decision/validated",
		State:       projectevidence.DecisionStateValidated,
		VerifierRef: "go/test/internal/projectevidence",
		Rationale:   "focused verifier passed",
	})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	return claim, decision
}
