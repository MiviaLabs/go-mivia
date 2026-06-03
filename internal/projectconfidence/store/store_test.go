package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
)

func TestLadybugStoreMemoryGraphCreateGetList(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	writeClaimFixture(t, ctx, graph, "project-a", "claim-a")
	writeClaimFixture(t, ctx, graph, "project-a", "claim-b")

	assessment := assessmentFixture("project-a", "claim-a", 92, projectconfidence.ScoreBandHigh, projectconfidence.RecommendationPromote)
	created, err := store.CreateAssessment(ctx, assessment)
	if err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	if len(created.Factors) != 2 {
		t.Fatalf("expected created factors to be preserved, got %#v", created.Factors)
	}

	got, err := store.GetAssessment(ctx, "project-a", "claim-a")
	if err != nil {
		t.Fatalf("get assessment: %v", err)
	}
	assertAssessment(t, got, assessment)

	minScore := 90
	listed, err := store.ListAssessments(ctx, "project-a", projectconfidence.AssessmentFilter{Band: projectconfidence.ScoreBandHigh, MinScore: &minScore, Recommendation: projectconfidence.RecommendationPromote, RunID: "agent_run_1", TraceID: "trace_1"})
	if err != nil {
		t.Fatalf("list assessments: %v", err)
	}
	if len(listed) != 1 || listed[0].ClaimID != "claim-a" {
		t.Fatalf("expected one matching assessment, got %#v", listed)
	}

	if _, err := store.GetAssessment(ctx, "project-a", "claim-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing assessment to return ErrNotFound, got %v", err)
	}
}

func TestLadybugStoreStoresFactorsAndLinksFromRelationships(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	writeClaimFixture(t, ctx, graph, "project-a", "claim-a")

	assessment := assessmentFixture("project-a", "claim-a", 72, projectconfidence.ScoreBandMedium, projectconfidence.RecommendationVerify)
	if _, err := store.CreateAssessment(ctx, assessment); err != nil {
		t.Fatalf("create assessment: %v", err)
	}

	claimRef := ladybug.NodeRef{Label: labelClaim, ID: graphID("project-a", "claim-a")}
	assessmentRef := ladybug.NodeRef{Label: labelConfidenceAssessment, ID: graphID("project-a", assessment.ID)}
	assertRelationshipStored(t, ctx, graph, relClaimHasConfidence, claimRef, assessmentRef, "project-a")
	assertRelationshipStored(t, ctx, graph, relConfidenceUsedEvidence, assessmentRef, ladybug.NodeRef{Label: labelEvidence, ID: graphID("project-a", "claim-a-evidence")}, "project-a")
	assertRelationshipStored(t, ctx, graph, relConfidenceUsedDecision, assessmentRef, ladybug.NodeRef{Label: labelDecision, ID: graphID("project-a", "claim-a-decision")}, "project-a")
	assertRelationshipStored(t, ctx, graph, relConfidenceUsedOutcome, assessmentRef, ladybug.NodeRef{Label: labelOutcome, ID: graphID("project-a", "claim-a-outcome")}, "project-a")

	factorRels, err := graph.ListRelationships(ctx, relConfidenceHasFactor, ladybug.RelationshipFilter{From: &assessmentRef, Properties: map[string]string{"project_id": "project-a"}})
	if err != nil {
		t.Fatalf("list factor relationships: %v", err)
	}
	if len(factorRels) != len(assessment.Factors) {
		t.Fatalf("expected factors to be linked through relationships, got %#v", factorRels)
	}
	got, err := store.GetAssessment(ctx, "project-a", "claim-a")
	if err != nil {
		t.Fatalf("get assessment: %v", err)
	}
	if len(got.Factors) != len(assessment.Factors) || got.Factors[0].Name != assessment.Factors[0].Name || got.Factors[1].Name != assessment.Factors[1].Name {
		t.Fatalf("expected factors to be fetched from linked nodes, got %#v", got.Factors)
	}
}

func TestLadybugStoreDoesNotLinkOrphanClaimPropertyNodes(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	writeClaimFixture(t, ctx, graph, "project-a", "claim-a")
	for _, node := range []ladybug.Node{
		{Label: labelEvidence, ID: graphID("project-a", "orphan-evidence"), Properties: map[string]string{"id": "orphan-evidence", "project_id": "project-a", "claim_id": "claim-a", "evidence_ref": "orphan/evidence", "evidence_kind": "file"}},
		{Label: labelDecision, ID: graphID("project-a", "orphan-decision"), Properties: map[string]string{"id": "orphan-decision", "project_id": "project-a", "claim_id": "claim-a", "decision_ref": "orphan/decision", "state": "validated", "verifier_ref": "verifier/ref", "rationale": "metadata only"}},
		{Label: labelOutcome, ID: graphID("project-a", "orphan-outcome"), Properties: map[string]string{"id": "orphan-outcome", "project_id": "project-a", "claim_id": "claim-a", "action_id": "orphan-action", "outcome_ref": "orphan/outcome", "outcome_kind": "test", "status": "passed"}},
	} {
		if err := graph.PutNode(ctx, node); err != nil {
			t.Fatalf("put orphan %s node: %v", node.Label, err)
		}
	}
	assessment := assessmentFixture("project-a", "claim-a", 72, projectconfidence.ScoreBandMedium, projectconfidence.RecommendationVerify)
	if _, err := store.CreateAssessment(ctx, assessment); err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	assessmentRef := ladybug.NodeRef{Label: labelConfidenceAssessment, ID: graphID("project-a", assessment.ID)}
	assertNoRelationshipTo(t, ctx, graph, relConfidenceUsedEvidence, assessmentRef, ladybug.NodeRef{Label: labelEvidence, ID: graphID("project-a", "orphan-evidence")}, "project-a")
	assertNoRelationshipTo(t, ctx, graph, relConfidenceUsedDecision, assessmentRef, ladybug.NodeRef{Label: labelDecision, ID: graphID("project-a", "orphan-decision")}, "project-a")
	assertNoRelationshipTo(t, ctx, graph, relConfidenceUsedOutcome, assessmentRef, ladybug.NodeRef{Label: labelOutcome, ID: graphID("project-a", "orphan-outcome")}, "project-a")
}

func TestLadybugStorePersistentReopenAssessmentWithFactorsAndClaim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph")
	graph, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	store := bootstrappedStore(t, ctx, graph)
	writeClaimFixture(t, ctx, graph, "project-a", "claim-a")
	assessment := assessmentFixture("project-a", "claim-a", 88, projectconfidence.ScoreBandHigh, projectconfidence.RecommendationPromote)
	if _, err := store.CreateAssessment(ctx, assessment); err != nil {
		t.Fatalf("create assessment: %v", err)
	}
	if err := graph.Close(); err != nil {
		t.Fatalf("close graph: %v", err)
	}

	reopened, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	defer reopened.Close()
	reopenedStore := bootstrappedStore(t, ctx, reopened)
	got, err := reopenedStore.GetAssessment(ctx, "project-a", "claim-a")
	if err != nil {
		t.Fatalf("get reopened assessment: %v", err)
	}
	assertAssessment(t, got, assessment)

	claimRef := ladybug.NodeRef{Label: labelClaim, ID: graphID("project-a", "claim-a")}
	assessmentRef := ladybug.NodeRef{Label: labelConfidenceAssessment, ID: graphID("project-a", assessment.ID)}
	assertRelationshipStored(t, ctx, reopened, relClaimHasConfidence, claimRef, assessmentRef, "project-a")
}

func TestLadybugStoreCrossProjectIsolation(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	writeClaimFixture(t, ctx, graph, "project-a", "shared-claim")
	writeClaimFixture(t, ctx, graph, "project-b", "shared-claim")

	assessmentA := assessmentFixture("project-a", "shared-claim", 91, projectconfidence.ScoreBandHigh, projectconfidence.RecommendationPromote)
	assessmentB := assessmentFixture("project-b", "shared-claim", 35, projectconfidence.ScoreBandLow, projectconfidence.RecommendationReview)
	if _, err := store.CreateAssessment(ctx, assessmentA); err != nil {
		t.Fatalf("create project-a assessment: %v", err)
	}
	if _, err := store.CreateAssessment(ctx, assessmentB); err != nil {
		t.Fatalf("create project-b assessment: %v", err)
	}

	gotB, err := store.GetAssessment(ctx, "project-b", "shared-claim")
	if err != nil {
		t.Fatalf("get project-b assessment: %v", err)
	}
	if gotB.ProjectID != "project-b" || gotB.Score != 35 {
		t.Fatalf("expected project-b scoped assessment, got %#v", gotB)
	}
	listed, err := store.ListAssessments(ctx, "project-a", projectconfidence.AssessmentFilter{Band: projectconfidence.ScoreBandHigh})
	if err != nil {
		t.Fatalf("list project-a assessments: %v", err)
	}
	if len(listed) != 1 || listed[0].ProjectID != "project-a" || listed[0].Score != 91 {
		t.Fatalf("expected only project-a high assessment, got %#v", listed)
	}

	assessmentRefA := ladybug.NodeRef{Label: labelConfidenceAssessment, ID: graphID("project-a", assessmentA.ID)}
	rels, err := graph.ListRelationships(ctx, relConfidenceHasFactor, ladybug.RelationshipFilter{From: &assessmentRefA, Properties: map[string]string{"project_id": "project-b"}})
	if err != nil {
		t.Fatalf("list cross-project factor relationships: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected no cross-project factor relationships, got %#v", rels)
	}
}

func bootstrappedStore(t *testing.T, ctx context.Context, graph ladybug.Graph) *LadybugStore {
	t.Helper()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return NewLadybugStore(graph)
}

func writeClaimFixture(t *testing.T, ctx context.Context, graph ladybug.Graph, projectID string, claimID string) {
	t.Helper()
	now := time.Date(2026, 6, 3, 8, 0, 0, 0, time.UTC)
	for _, node := range []ladybug.Node{
		{Label: labelClaim, ID: graphID(projectID, claimID), Properties: map[string]string{"id": claimID, "project_id": projectID, "run_id": "agent_run_1", "trace_id": "trace_1", "claim_ref": "claim/ref", "summary": "bounded metadata only", "status": "candidate", "created_at": formatTime(now), "updated_at": formatTime(now)}},
		{Label: labelEvidence, ID: graphID(projectID, claimID+"-evidence"), Properties: map[string]string{"id": claimID + "-evidence", "project_id": projectID, "claim_id": claimID, "evidence_ref": "evidence/ref", "evidence_kind": "context_pack", "source_ref": "context_pack/ref", "summary": "metadata ref only", "created_at": formatTime(now.Add(time.Minute))}},
		{Label: labelDecision, ID: graphID(projectID, claimID+"-decision"), Properties: map[string]string{"id": claimID + "-decision", "project_id": projectID, "claim_id": claimID, "decision_ref": "decision/ref", "state": "validated", "verifier_ref": "verifier/ref", "rationale": "metadata verifier passed", "decided_at": formatTime(now.Add(2 * time.Minute))}},
		{Label: labelAction, ID: graphID(projectID, claimID+"-action"), Properties: map[string]string{"id": claimID + "-action", "project_id": projectID, "claim_id": claimID, "decision_id": claimID + "-decision", "action_ref": "action/ref", "action_kind": "verifier_run", "summary": "metadata action", "run_id": "agent_run_1", "created_at": formatTime(now.Add(3 * time.Minute))}},
		{Label: labelOutcome, ID: graphID(projectID, claimID+"-outcome"), Properties: map[string]string{"id": claimID + "-outcome", "project_id": projectID, "claim_id": claimID, "action_id": claimID + "-action", "outcome_ref": "outcome/ref", "outcome_kind": "test", "status": "passed", "verifier_ref": "verifier/ref", "summary": "metadata verifier passed", "created_at": formatTime(now.Add(3 * time.Minute))}},
	} {
		if err := graph.PutNode(ctx, node); err != nil {
			t.Fatalf("put %s node: %v", node.Label, err)
		}
	}
	claimRef := ladybug.NodeRef{Label: labelClaim, ID: graphID(projectID, claimID)}
	evidenceRef := ladybug.NodeRef{Label: labelEvidence, ID: graphID(projectID, claimID+"-evidence")}
	decisionRef := ladybug.NodeRef{Label: labelDecision, ID: graphID(projectID, claimID+"-decision")}
	actionRef := ladybug.NodeRef{Label: labelAction, ID: graphID(projectID, claimID+"-action")}
	outcomeRef := ladybug.NodeRef{Label: labelOutcome, ID: graphID(projectID, claimID+"-outcome")}
	for _, rel := range []ladybug.Relationship{
		relationship(relClaimHasEvidence, claimRef, evidenceRef, projectID, claimID),
		relationship(relClaimHasDecision, claimRef, decisionRef, projectID, claimID),
		relationship(relDecisionProducedAction, decisionRef, actionRef, projectID, claimID),
		relationship(relActionProducedOutcome, actionRef, outcomeRef, projectID, claimID),
	} {
		if err := graph.PutRelationship(ctx, rel); err != nil {
			t.Fatalf("put %s relationship: %v", rel.Type, err)
		}
	}
}

func assessmentFixture(projectID string, claimID string, score int, band string, recommendation string) projectconfidence.ConfidenceAssessment {
	now := time.Date(2026, 6, 3, 8, 30, 0, 0, time.UTC)
	return projectconfidence.ConfidenceAssessment{
		ID:             "confidence-" + claimID,
		ProjectID:      projectID,
		ClaimID:        claimID,
		ClaimRef:       "claim/ref",
		RunID:          "agent_run_1",
		TraceID:        "trace_1",
		Score:          score,
		Band:           band,
		Recommendation: recommendation,
		Inputs: projectconfidence.ConfidenceInputs{
			EvidenceCount:              2,
			EvidenceKinds:              []string{"context_pack", "verifier"},
			DecisionCount:              1,
			ActionCount:                1,
			PassedOutcomeCount:         1,
			ContextHealthStatus:        "ready",
			LatestRunAgeSeconds:        60,
			ClaimCheckVerified:         1,
			ImpactResidualUnknownCount: 0,
		},
		Factors: []projectconfidence.ConfidenceFactor{
			{Name: "evidence_count", ScoreDelta: 10, Weight: 1, Status: projectconfidence.FactorStatusPositive, Summary: "multiple metadata evidence refs", SourceRef: "evidence/ref"},
			{Name: "context_health", ScoreDelta: 10, Weight: 1, Status: projectconfidence.FactorStatusPositive, Summary: "context graph ready", SourceRef: "context_health"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func assertAssessment(t *testing.T, got projectconfidence.ConfidenceAssessment, want projectconfidence.ConfidenceAssessment) {
	t.Helper()
	if got.ProjectID != want.ProjectID || got.ClaimID != want.ClaimID || got.Score != want.Score || got.Band != want.Band || got.Recommendation != want.Recommendation || got.RunID != want.RunID || got.TraceID != want.TraceID {
		t.Fatalf("unexpected assessment metadata: got %#v want %#v", got, want)
	}
	if len(got.Factors) != len(want.Factors) {
		t.Fatalf("expected %d factors, got %#v", len(want.Factors), got.Factors)
	}
	for index := range want.Factors {
		if got.Factors[index] != want.Factors[index] {
			t.Fatalf("unexpected factor %d: got %#v want %#v", index, got.Factors[index], want.Factors[index])
		}
	}
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

func assertNoRelationshipTo(t *testing.T, ctx context.Context, graph ladybug.Graph, relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string) {
	t.Helper()
	relationships, err := graph.ListRelationships(ctx, relationshipType, ladybug.RelationshipFilter{From: &from, To: &to, Properties: map[string]string{"project_id": projectID}})
	if err != nil {
		t.Fatalf("list %s relationship: %v", relationshipType, err)
	}
	if len(relationships) != 0 {
		t.Fatalf("expected no %s relationship to %#v, got %#v", relationshipType, to, relationships)
	}
}

func relationship(relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string, claimID string) ladybug.Relationship {
	return ladybug.Relationship{Type: relationshipType, From: from, To: to, Properties: map[string]string{"project_id": projectID, "claim_id": claimID, "run_id": "agent_run_1"}}
}
