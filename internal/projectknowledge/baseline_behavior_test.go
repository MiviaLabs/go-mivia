package projectknowledge_test

import (
	"context"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
)

func TestBaselineKnowledgePromotionBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService()
	candidate := mustCreateCandidate(t, ctx, svc)
	if candidate.State != projectknowledge.StateCandidate {
		t.Fatalf("candidate should not auto-promote: %#v", candidate)
	}
	validated := mustValidate(t, ctx, svc, candidate.ID)
	if validated.State != projectknowledge.StateValidated {
		t.Fatalf("validate did not persist state: %#v", validated)
	}
	promoted, err := svc.PromoteProject(ctx, projectknowledge.PromoteProjectInput{
		ProjectID:   "project_1",
		KnowledgeID: candidate.ID,
		DecisionRef: "knowledge_project_promoted",
		VerifierRef: "verifier_ref",
		Rationale:   "project gate passed",
		Gate:        projectknowledge.ProjectGateInput{Claim: highClaim(), Confidence: highConfidence()},
	})
	if err != nil {
		t.Fatalf("promote project: %v", err)
	}
	if promoted.State != projectknowledge.StateProjectPromoted || promoted.Scope != projectknowledge.ScopeProject {
		t.Fatalf("project promotion did not persist scope/state: %#v", promoted)
	}
	reuse, err := svc.RecordReuseEvent(ctx, projectknowledge.RecordReuseEventInput{
		ProjectID:       "project_1",
		KnowledgeID:     promoted.ID,
		AgentRunID:      "run_phase0",
		ReuseRef:        "reuse_phase0",
		Revalidated:     true,
		RevalidationRef: "verifier_phase0",
		Outcome:         projectknowledge.ReuseOutcomeUsed,
		Summary:         "metadata-only reuse",
	})
	if err != nil {
		t.Fatalf("record reuse: %v", err)
	}
	if reuse.Outcome != projectknowledge.ReuseOutcomeUsed {
		t.Fatalf("reuse event lost outcome: %#v", reuse)
	}
	underReview, err := svc.SubmitOrgReview(ctx, projectknowledge.SubmitOrgReviewInput{
		ProjectID:   "project_1",
		KnowledgeID: promoted.ID,
		DecisionRef: "org_review/phase0",
		VerifierRef: "verifier/phase0",
		Rationale:   "org review requested",
		DecidedBy:   "owner_review",
	})
	if err != nil {
		t.Fatalf("submit org review: %v", err)
	}
	if underReview.State != projectknowledge.StateOrgReview || underReview.Scope != projectknowledge.ScopeOrg {
		t.Fatalf("org review did not persist org gate state: %#v", underReview)
	}
	orgPromoted, err := svc.PromoteOrg(ctx, projectknowledge.PromoteOrgInput{ProjectID: "project_1", KnowledgeID: promoted.ID, Gate: orgGate(0)})
	if err != nil {
		t.Fatalf("promote org: %v", err)
	}
	if orgPromoted.State != projectknowledge.StateOrgPromoted || orgPromoted.Scope != projectknowledge.ScopeOrg {
		t.Fatalf("org promotion did not persist final state: %#v", orgPromoted)
	}
}
