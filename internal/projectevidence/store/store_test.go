package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

func TestLadybugStoreMemoryGraphFullClaimChain(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	chain := writeClaimChain(t, ctx, store, "project-a", "claim-a")

	record, err := store.GetClaim(ctx, chain.Claim.ProjectID, chain.Claim.ID)
	if err != nil {
		t.Fatalf("get claim chain: %v", err)
	}
	assertFullChain(t, record)

	claims, err := store.ListClaims(ctx, chain.Claim.ProjectID, projectevidence.ClaimFilter{ArtifactRef: "artifact/finding-a"})
	if err != nil {
		t.Fatalf("list by artifact: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != chain.Claim.ID {
		t.Fatalf("expected artifact filter to return claim, got %#v", claims)
	}
	claims, err = store.ListClaims(ctx, chain.Claim.ProjectID, projectevidence.ClaimFilter{PromotionState: projectevidence.PromotionStatePromoted})
	if err != nil {
		t.Fatalf("list by promotion: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != chain.Claim.ID {
		t.Fatalf("expected promotion filter to return claim, got %#v", claims)
	}
	claims, err = store.ListClaims(ctx, chain.Claim.ProjectID, projectevidence.ClaimFilter{OutcomeStatus: projectevidence.OutcomeStatusPassed})
	if err != nil {
		t.Fatalf("list by outcome: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != chain.Claim.ID {
		t.Fatalf("expected outcome filter to return claim, got %#v", claims)
	}
}

func TestLadybugStoreRelationshipTraversalFromStoredEdges(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	chain := writeClaimChain(t, ctx, store, "project-a", "claim-a")

	claimRef := ladybug.NodeRef{Label: labelClaim, ID: graphID(chain.Claim.ProjectID, chain.Claim.ID)}
	evidenceRef := ladybug.NodeRef{Label: labelEvidence, ID: graphID(chain.Evidence.ProjectID, chain.Evidence.ID)}
	decisionRef := ladybug.NodeRef{Label: labelDecision, ID: graphID(chain.Decision.ProjectID, chain.Decision.ID)}
	actionRef := ladybug.NodeRef{Label: labelAction, ID: graphID(chain.Action.ProjectID, chain.Action.ID)}
	outcomeRef := ladybug.NodeRef{Label: labelOutcome, ID: graphID(chain.Outcome.ProjectID, chain.Outcome.ID)}
	artifactRef := ladybug.NodeRef{Label: labelArtifact, ID: artifactID(chain.Claim.ProjectID, chain.Claim.ID, "artifact/finding-a")}
	promotionRef := ladybug.NodeRef{Label: labelPromotion, ID: promotionID(chain.Claim.ProjectID, chain.Claim.ID, "artifact/finding-a", projectevidence.PromotionStatePromoted, chain.Action.ID, chain.Action.ActionRef, chain.Outcome.OutcomeRef)}

	assertRelationshipStored(t, ctx, graph, relProjectHasClaim, ladybug.NodeRef{Label: labelProject, ID: chain.Claim.ProjectID}, claimRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relAgentRunMadeClaim, ladybug.NodeRef{Label: labelAgentRun, ID: chain.Claim.RunID}, claimRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relClaimHasEvidence, claimRef, evidenceRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relEvidenceSupportsDecision, evidenceRef, decisionRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relClaimHasDecision, claimRef, decisionRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relDecisionProducedAction, decisionRef, actionRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relActionProducedOutcome, actionRef, outcomeRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relActionProducedArtifact, actionRef, artifactRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relArtifactHasPromotion, artifactRef, promotionRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relPromotionDecidesClaim, promotionRef, claimRef, chain.Claim.ProjectID)
	assertRelationshipStored(t, ctx, graph, relOutcomeSupportsPromotion, outcomeRef, promotionRef, chain.Claim.ProjectID)
}

func TestLadybugStoreCrossProjectIsolation(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	chainA := writeClaimChain(t, ctx, store, "project-a", "shared-claim")
	chainB := writeClaimChain(t, ctx, store, "project-b", "shared-claim")

	recordB, err := store.GetClaim(ctx, "project-b", chainA.Claim.ID)
	if err != nil {
		t.Fatalf("expected project-b claim with shared id: %v", err)
	}
	if recordB.Claim.ProjectID != chainB.Claim.ProjectID {
		t.Fatalf("expected project-b scoped claim, got %#v", recordB.Claim)
	}
	if _, err := store.GetClaim(ctx, "project-c", chainA.Claim.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected unknown project not to read shared claim id, got %v", err)
	}
	claims, err := store.ListClaims(ctx, "project-a", projectevidence.ClaimFilter{PromotionState: projectevidence.PromotionStatePromoted})
	if err != nil {
		t.Fatalf("list project-a claims: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != chainA.Claim.ID {
		t.Fatalf("expected only project-a claim, got %#v", claims)
	}
	claims, err = store.ListClaims(ctx, "project-b", projectevidence.ClaimFilter{OutcomeStatus: projectevidence.OutcomeStatusPassed})
	if err != nil {
		t.Fatalf("list project-b claims: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != chainB.Claim.ID {
		t.Fatalf("expected only project-b claim, got %#v", claims)
	}
	claimRefA := ladybug.NodeRef{Label: labelClaim, ID: graphID(chainA.Claim.ProjectID, chainA.Claim.ID)}
	rels, err := graph.ListRelationships(ctx, relClaimHasEvidence, ladybug.RelationshipFilter{From: &claimRefA, Properties: map[string]string{"project_id": "project-b"}})
	if err != nil {
		t.Fatalf("list cross-project relationships: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected no cross-project relationships, got %#v", rels)
	}
}

func TestLadybugStoreLinkPromotionRequiresExistingArtifact(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	claim, err := store.CreateClaim(ctx, projectevidence.Claim{ID: "claim-a", ProjectID: "project-a", RunID: "agent_run_1", TraceID: "trace_1", ClaimRef: "claim/ref", Summary: "bounded metadata only", Status: projectevidence.ClaimStatusCandidate, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}

	_, err = store.LinkPromotion(ctx, projectevidence.PromotionLink{
		ProjectID:      claim.ProjectID,
		ClaimID:        claim.ID,
		RunID:          "agent_run_1",
		ArtifactRef:    "artifact/missing",
		PromotionState: projectevidence.PromotionStatePromoted,
		SourceRef:      "action/ref",
		VerifierRef:    "verifier/ref",
		DecisionRef:    "decision/ref",
		ActionRef:      "action/ref",
		OutcomeRef:     "outcome/ref",
		DecidedAt:      now.Add(time.Minute),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing artifact to fail, got %v", err)
	}
	promotionNodes, err := graph.ListNodes(ctx, labelPromotion, map[string]string{"project_id": claim.ProjectID})
	if err != nil {
		t.Fatalf("list promotion nodes: %v", err)
	}
	if len(promotionNodes) != 0 {
		t.Fatalf("expected no dangling promotion node, got %#v", promotionNodes)
	}
}

func TestLadybugStorePersistentReopenClaimChain(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir()
	graph, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	store := bootstrappedStore(t, ctx, graph)
	chain := writeClaimChain(t, ctx, store, "project-a", "claim-a")
	if err := graph.Close(); err != nil {
		t.Fatalf("close graph: %v", err)
	}

	reopened, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	defer reopened.Close()
	reopenedStore := bootstrappedStore(t, ctx, reopened)
	record, err := reopenedStore.GetClaim(ctx, chain.Claim.ProjectID, chain.Claim.ID)
	if err != nil {
		t.Fatalf("get reopened chain: %v", err)
	}
	assertFullChain(t, record)
}

type claimChain struct {
	Claim    projectevidence.Claim
	Evidence projectevidence.Evidence
	Decision projectevidence.Decision
	Action   projectevidence.Action
	Outcome  projectevidence.Outcome
}

func bootstrappedStore(t *testing.T, ctx context.Context, graph ladybug.Graph) *LadybugStore {
	t.Helper()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return NewLadybugStore(graph)
}

func assertRelationshipStored(t *testing.T, ctx context.Context, graph ladybug.Graph, relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string) {
	t.Helper()
	relationships, err := graph.ListRelationships(ctx, relationshipType, ladybug.RelationshipFilter{From: &from, To: &to, Properties: map[string]string{"project_id": projectID}})
	if err != nil {
		t.Fatalf("list %s relationship: %v", relationshipType, err)
	}
	if len(relationships) != 1 {
		t.Fatalf("expected one %s relationship, got %#v", relationshipType, relationships)
	}
	if relationships[0].Properties["project_id"] != projectID {
		t.Fatalf("expected %s relationship project_id %q, got %#v", relationshipType, projectID, relationships[0].Properties)
	}
}

func writeClaimChain(t *testing.T, ctx context.Context, store *LadybugStore, projectID string, claimID string) claimChain {
	t.Helper()
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	claim, err := store.CreateClaim(ctx, projectevidence.Claim{ID: claimID, ProjectID: projectID, RunID: "agent_run_1", TraceID: "trace_1", ClaimRef: "claim/ref", Summary: "bounded metadata only", Status: projectevidence.ClaimStatusCandidate, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}
	evidence, err := store.AppendEvidence(ctx, projectevidence.Evidence{ID: claimID + "-evidence", ProjectID: projectID, ClaimID: claim.ID, EvidenceRef: "context_pack/ref", EvidenceKind: projectevidence.EvidenceKindContextPack, SourceRef: "source/ref", Summary: "bounded context only", CreatedAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("append evidence: %v", err)
	}
	decision, err := store.CreateDecision(ctx, projectevidence.Decision{ID: claimID + "-decision", ProjectID: projectID, ClaimID: claim.ID, DecisionRef: "decision/ref", State: projectevidence.DecisionStateValidated, VerifierRef: "verifier/ref", Rationale: "focused verifier passed", DecidedAt: now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	action, err := store.CreateAction(ctx, projectevidence.Action{ID: claimID + "-action", ProjectID: projectID, ClaimID: claim.ID, DecisionID: decision.ID, ActionRef: "action/ref", ActionKind: projectevidence.ActionKindCodeChange, Summary: "changed safe metadata", ChangedFiles: []string{"internal/projectevidence/store/ladybug.go"}, RunID: "agent_run_2", CreatedAt: now.Add(3 * time.Minute)})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	outcome, err := store.CreateOutcome(ctx, projectevidence.Outcome{ID: claimID + "-outcome", ProjectID: projectID, ClaimID: claim.ID, ActionID: action.ID, OutcomeRef: "outcome/ref", OutcomeKind: projectevidence.OutcomeKindTest, Status: projectevidence.OutcomeStatusPassed, VerifierRef: "verifier/ref", Summary: "focused verifier passed", CreatedAt: now.Add(4 * time.Minute)})
	if err != nil {
		t.Fatalf("create outcome: %v", err)
	}
	if _, err := store.LinkArtifact(ctx, projectevidence.ArtifactLink{ProjectID: projectID, ClaimID: claim.ID, ArtifactRef: "artifact/finding-a", ArtifactKind: "finding", RunID: "agent_run_2"}); err != nil {
		t.Fatalf("link artifact: %v", err)
	}
	if _, err := store.LinkPromotion(ctx, projectevidence.PromotionLink{ProjectID: projectID, ClaimID: claim.ID, RunID: "agent_run_2", ArtifactRef: "artifact/finding-a", PromotionState: projectevidence.PromotionStatePromoted, SourceRef: action.ID, VerifierRef: "verifier/ref", DecisionRef: decision.DecisionRef, ActionRef: action.ActionRef, OutcomeRef: outcome.OutcomeRef, DecidedAt: now.Add(5 * time.Minute)}); err != nil {
		t.Fatalf("link promotion: %v", err)
	}
	return claimChain{Claim: claim, Evidence: evidence, Decision: decision, Action: action, Outcome: outcome}
}

func assertFullChain(t *testing.T, record projectevidence.ClaimRecord) {
	t.Helper()
	if len(record.Evidence) != 1 || len(record.Decisions) != 1 || len(record.Actions) != 1 || len(record.Outcomes) != 1 || len(record.ArtifactLinks) != 1 || len(record.PromotionLinks) != 1 {
		t.Fatalf("expected complete chain, got %#v", record)
	}
	if record.Evidence[0].ProjectID != record.Claim.ProjectID || record.Decisions[0].ProjectID != record.Claim.ProjectID || record.Actions[0].ProjectID != record.Claim.ProjectID || record.Outcomes[0].ProjectID != record.Claim.ProjectID {
		t.Fatalf("expected all chain nodes to stay project scoped, got %#v", record)
	}
	if record.ArtifactLinks[0].ArtifactRef != "artifact/finding-a" || record.PromotionLinks[0].PromotionState != projectevidence.PromotionStatePromoted {
		t.Fatalf("expected artifact and promotion links, got %#v", record)
	}
	if record.PromotionLinks[0].RunID != "agent_run_2" || record.PromotionLinks[0].ActionRef != record.Actions[0].ActionRef || record.PromotionLinks[0].OutcomeRef != record.Outcomes[0].OutcomeRef {
		t.Fatalf("expected promotion to preserve run, action, and outcome refs, got %#v", record.PromotionLinks[0])
	}
}
