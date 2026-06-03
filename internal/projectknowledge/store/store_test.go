package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
)

func TestLadybugStoreMemoryGraphCreateGetListUpdate(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	record := knowledgeFixture("project-a", "knowledge-a")

	created, err := store.CreateKnowledge(ctx, record)
	if err != nil {
		t.Fatalf("create knowledge: %v", err)
	}
	assertKnowledge(t, created, record)

	got, err := store.GetKnowledge(ctx, "project-a", "knowledge-a")
	if err != nil {
		t.Fatalf("get knowledge: %v", err)
	}
	assertKnowledge(t, got, record)

	minConfidence := 90
	listed, err := store.ListKnowledge(ctx, "project-a", projectknowledge.KnowledgeFilter{Scope: projectknowledge.ScopeProject, State: projectknowledge.StateProjectPromoted, KnowledgeRef: "knowledge/ref/knowledge-a", ClaimID: "claim-knowledge-a", ConfidenceBand: "high", MinConfidence: &minConfidence})
	if err != nil {
		t.Fatalf("list knowledge: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != record.ID {
		t.Fatalf("expected one matching knowledge record, got %#v", listed)
	}

	record.State = projectknowledge.StateSuperseded
	record.SupersededByRef = "knowledge-next"
	record.UpdatedAt = record.UpdatedAt.Add(time.Minute)
	updated, err := store.UpdateKnowledge(ctx, record)
	if err != nil {
		t.Fatalf("update knowledge: %v", err)
	}
	if updated.State != projectknowledge.StateSuperseded || updated.SupersededByRef != "knowledge-next" {
		t.Fatalf("expected updated transition metadata, got %#v", updated)
	}

	if _, err := store.GetKnowledge(ctx, "project-a", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected missing knowledge to return ErrNotFound, got %v", err)
	}
}

func TestLadybugStoreStoresMetadataNodesAndRelationships(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	record := knowledgeFixture("project-a", "knowledge-a")
	if _, err := store.CreateKnowledge(ctx, record); err != nil {
		t.Fatalf("create knowledge: %v", err)
	}
	decision := decisionFixture(record, "decision-a", projectknowledge.StateProjectPromoted)
	if _, err := store.CreateDecision(ctx, decision); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	event := reuseEventFixture(record, "reuse-a")
	if _, err := store.CreateReuseEvent(ctx, event); err != nil {
		t.Fatalf("create reuse event: %v", err)
	}

	knowledgeRef := ladybug.NodeRef{Label: labelKnowledgeRecord, ID: graphID(record.ProjectID, record.ID)}
	assertRelationshipStored(t, ctx, graph, relProjectHasKnowledge, ladybug.NodeRef{Label: labelProject, ID: record.ProjectID}, knowledgeRef, record.ProjectID)
	assertRelationshipStored(t, ctx, graph, relClaimPromotedToKnowledge, ladybug.NodeRef{Label: labelClaim, ID: graphID(record.ProjectID, record.ClaimID)}, knowledgeRef, record.ProjectID)
	assertRelationshipStored(t, ctx, graph, relConfidenceSupportsKnowledge, ladybug.NodeRef{Label: labelConfidenceAssessment, ID: graphID(record.ProjectID, record.ConfidenceAssessmentID)}, knowledgeRef, record.ProjectID)
	assertRelationshipStored(t, ctx, graph, relKnowledgeHasPromotionDecision, knowledgeRef, ladybug.NodeRef{Label: labelKnowledgePromotionDecision, ID: graphID(decision.ProjectID, decision.ID)}, record.ProjectID)
	assertRelationshipStored(t, ctx, graph, relKnowledgeHasReuseEvent, knowledgeRef, ladybug.NodeRef{Label: labelKnowledgeReuseEvent, ID: graphID(event.ProjectID, event.ID)}, record.ProjectID)
	assertRelationshipStored(t, ctx, graph, relKnowledgeReusedByAgentRun, knowledgeRef, ladybug.NodeRef{Label: labelAgentRun, ID: event.AgentRunID}, record.ProjectID)

	node, err := graph.GetNode(ctx, labelKnowledgeRecord, graphID(record.ProjectID, record.ID))
	if err != nil {
		t.Fatalf("get knowledge node: %v", err)
	}
	for key, want := range map[string]string{
		"project_id":       record.ProjectID,
		"scope":            record.Scope,
		"state":            record.State,
		"knowledge_ref":    record.KnowledgeRef,
		"claim_id":         record.ClaimID,
		"claim_ref":        record.ClaimRef,
		"confidence_score": "91",
		"confidence_band":  record.ConfidenceBand,
		"org_ref":          record.OrgRef,
	} {
		if node.Properties[key] != want {
			t.Fatalf("expected knowledge property %s=%q, got %#v", key, want, node.Properties)
		}
	}
}

func TestLadybugStorePersistentReopenKnowledgeDecisionAndReuse(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph")
	graph, err := ladybug.OpenPebbleGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	store := bootstrappedStore(t, ctx, graph)
	record := knowledgeFixture("project-a", "knowledge-a")
	decision := decisionFixture(record, "decision-a", projectknowledge.StateProjectPromoted)
	event := reuseEventFixture(record, "reuse-a")
	if _, err := store.CreateKnowledge(ctx, record); err != nil {
		t.Fatalf("create knowledge: %v", err)
	}
	if _, err := store.CreateDecision(ctx, decision); err != nil {
		t.Fatalf("create decision: %v", err)
	}
	if _, err := store.CreateReuseEvent(ctx, event); err != nil {
		t.Fatalf("create reuse event: %v", err)
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
	got, err := reopenedStore.GetKnowledge(ctx, record.ProjectID, record.ID)
	if err != nil {
		t.Fatalf("get reopened knowledge: %v", err)
	}
	assertKnowledge(t, got, record)
	decisions, err := reopenedStore.ListDecisions(ctx, record.ProjectID, record.ID)
	if err != nil {
		t.Fatalf("list reopened decisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].ID != decision.ID {
		t.Fatalf("expected reopened decision, got %#v", decisions)
	}
	events, err := reopenedStore.ListReuseEvents(ctx, record.ProjectID, record.ID)
	if err != nil {
		t.Fatalf("list reopened reuse events: %v", err)
	}
	if len(events) != 1 || events[0].ID != event.ID || !events[0].Revalidated {
		t.Fatalf("expected reopened reuse event, got %#v", events)
	}
}

func TestLadybugStoreCrossProjectIsolation(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	recordA := knowledgeFixture("project-a", "shared-knowledge")
	recordB := knowledgeFixture("project-b", "shared-knowledge")
	recordB.ConfidenceScore = 42
	recordB.ConfidenceBand = "low"
	if _, err := store.CreateKnowledge(ctx, recordA); err != nil {
		t.Fatalf("create project-a knowledge: %v", err)
	}
	if _, err := store.CreateKnowledge(ctx, recordB); err != nil {
		t.Fatalf("create project-b knowledge: %v", err)
	}

	gotB, err := store.GetKnowledge(ctx, "project-b", "shared-knowledge")
	if err != nil {
		t.Fatalf("get project-b knowledge: %v", err)
	}
	if gotB.ProjectID != "project-b" || gotB.ConfidenceScore != 42 {
		t.Fatalf("expected project-b scoped knowledge, got %#v", gotB)
	}
	listedA, err := store.ListKnowledge(ctx, "project-a", projectknowledge.KnowledgeFilter{ConfidenceBand: "high"})
	if err != nil {
		t.Fatalf("list project-a knowledge: %v", err)
	}
	if len(listedA) != 1 || listedA[0].ProjectID != "project-a" {
		t.Fatalf("expected only project-a knowledge, got %#v", listedA)
	}

	knowledgeRefA := ladybug.NodeRef{Label: labelKnowledgeRecord, ID: graphID(recordA.ProjectID, recordA.ID)}
	rels, err := graph.ListRelationships(ctx, relProjectHasKnowledge, ladybug.RelationshipFilter{To: &knowledgeRefA, Properties: map[string]string{"project_id": "project-b"}})
	if err != nil {
		t.Fatalf("list cross-project knowledge relationships: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected no cross-project relationships, got %#v", rels)
	}
}

func TestLadybugStoreOrgListReturnsOnlyOrgPromotedKnowledge(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	projectRecord := knowledgeFixture("project-a", "project-promoted")
	orgReviewRecord := knowledgeFixture("project-a", "org-review")
	orgReviewRecord.Scope = projectknowledge.ScopeOrg
	orgReviewRecord.OrgRef = projectknowledge.DefaultOrgRef
	orgReviewRecord.State = projectknowledge.StateOrgReview
	orgPromotedRecord := knowledgeFixture("project-a", "org-promoted")
	orgPromotedRecord.Scope = projectknowledge.ScopeOrg
	orgPromotedRecord.OrgRef = projectknowledge.DefaultOrgRef
	orgPromotedRecord.State = projectknowledge.StateOrgPromoted
	for _, record := range []projectknowledge.KnowledgeRecord{projectRecord, orgReviewRecord, orgPromotedRecord} {
		if _, err := store.CreateKnowledge(ctx, record); err != nil {
			t.Fatalf("create %s: %v", record.ID, err)
		}
	}

	listed, err := store.ListKnowledge(ctx, "project-a", projectknowledge.KnowledgeFilter{Scope: projectknowledge.ScopeOrg, State: projectknowledge.StateOrgPromoted, OrgRef: projectknowledge.DefaultOrgRef})
	if err != nil {
		t.Fatalf("list org knowledge: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != orgPromotedRecord.ID {
		t.Fatalf("expected only org-promoted knowledge, got %#v", listed)
	}
}

func TestLadybugStoreSupersessionCreatesRelationshipWithoutDelete(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	store := bootstrappedStore(t, ctx, graph)
	oldRecord := knowledgeFixture("project-a", "knowledge-old")
	newRecord := knowledgeFixture("project-a", "knowledge-new")
	newRecord.SupersedesRef = oldRecord.ID
	if _, err := store.CreateKnowledge(ctx, oldRecord); err != nil {
		t.Fatalf("create old knowledge: %v", err)
	}
	if _, err := store.CreateKnowledge(ctx, newRecord); err != nil {
		t.Fatalf("create new knowledge: %v", err)
	}
	oldRecord.State = projectknowledge.StateSuperseded
	oldRecord.SupersededByRef = newRecord.ID
	if _, err := store.UpdateKnowledge(ctx, oldRecord); err != nil {
		t.Fatalf("update old knowledge: %v", err)
	}

	if _, err := store.GetKnowledge(ctx, oldRecord.ProjectID, oldRecord.ID); err != nil {
		t.Fatalf("expected old knowledge to remain fetchable: %v", err)
	}
	if _, err := store.GetKnowledge(ctx, newRecord.ProjectID, newRecord.ID); err != nil {
		t.Fatalf("expected new knowledge to remain fetchable: %v", err)
	}
	assertRelationshipStored(t, ctx, graph, relKnowledgeSupersedesKnowledge, ladybug.NodeRef{Label: labelKnowledgeRecord, ID: graphID(newRecord.ProjectID, newRecord.ID)}, ladybug.NodeRef{Label: labelKnowledgeRecord, ID: graphID(oldRecord.ProjectID, oldRecord.ID)}, oldRecord.ProjectID)
}

func bootstrappedStore(t *testing.T, ctx context.Context, graph ladybug.Graph) *LadybugStore {
	t.Helper()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return NewLadybugStore(graph)
}

func knowledgeFixture(projectID string, knowledgeID string) projectknowledge.KnowledgeRecord {
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	return projectknowledge.KnowledgeRecord{
		ID:                     knowledgeID,
		ProjectID:              projectID,
		Scope:                  projectknowledge.ScopeProject,
		KnowledgeRef:           "knowledge/ref/" + knowledgeID,
		ClaimID:                "claim-" + knowledgeID,
		ClaimRef:               "claim/ref/" + knowledgeID,
		ConfidenceAssessmentID: "confidence-" + knowledgeID,
		ConfidenceScore:        91,
		ConfidenceBand:         "high",
		State:                  projectknowledge.StateProjectPromoted,
		Summary:                "metadata only summary",
		ReuseGuidance:          "revalidate against current source before use",
		EvidenceRefs:           []string{"evidence/ref/a", "evidence/ref/b"},
		VerifierRefs:           []string{"verifier/ref/a"},
		OutcomeRefs:            []string{"outcome/ref/a"},
		PromotionRefs:          []string{"promotion/ref/a"},
		CreatedAt:              now,
		UpdatedAt:              now.Add(time.Minute),
		PromotedAt:             now.Add(2 * time.Minute),
	}
}

func decisionFixture(record projectknowledge.KnowledgeRecord, decisionID string, toState string) projectknowledge.PromotionDecision {
	return projectknowledge.PromotionDecision{
		ID:              decisionID,
		ProjectID:       record.ProjectID,
		KnowledgeID:     record.ID,
		FromState:       projectknowledge.StateValidated,
		ToState:         toState,
		Scope:           record.Scope,
		DecisionRef:     "decision/ref/" + decisionID,
		VerifierRef:     "verifier/ref/a",
		ConfidenceScore: record.ConfidenceScore,
		Rationale:       "metadata-only promotion rationale",
		DecidedBy:       "owner/ref/a",
		DecidedAt:       record.UpdatedAt.Add(time.Minute),
	}
}

func reuseEventFixture(record projectknowledge.KnowledgeRecord, eventID string) projectknowledge.KnowledgeReuseEvent {
	return projectknowledge.KnowledgeReuseEvent{
		ID:              eventID,
		ProjectID:       record.ProjectID,
		KnowledgeID:     record.ID,
		AgentRunID:      "agent_run_a",
		TraceID:         "trace_a",
		ReuseRef:        "reuse/ref/" + eventID,
		Revalidated:     true,
		RevalidationRef: "verifier/ref/revalidation",
		Outcome:         projectknowledge.ReuseOutcomeUsed,
		Summary:         "metadata-only reuse summary",
		CreatedAt:       record.UpdatedAt.Add(2 * time.Minute),
	}
}

func assertKnowledge(t *testing.T, got projectknowledge.KnowledgeRecord, want projectknowledge.KnowledgeRecord) {
	t.Helper()
	if got.ID != want.ID || got.ProjectID != want.ProjectID || got.Scope != want.Scope || got.OrgRef != want.OrgRef || got.KnowledgeRef != want.KnowledgeRef || got.ClaimID != want.ClaimID || got.ClaimRef != want.ClaimRef || got.ConfidenceAssessmentID != want.ConfidenceAssessmentID || got.ConfidenceScore != want.ConfidenceScore || got.ConfidenceBand != want.ConfidenceBand || got.State != want.State || got.Summary != want.Summary || got.ReuseGuidance != want.ReuseGuidance || got.SupersedesRef != want.SupersedesRef || got.SupersededByRef != want.SupersededByRef {
		t.Fatalf("unexpected knowledge metadata: got %#v want %#v", got, want)
	}
	assertStringSlice(t, "evidence refs", got.EvidenceRefs, want.EvidenceRefs)
	assertStringSlice(t, "verifier refs", got.VerifierRefs, want.VerifierRefs)
	assertStringSlice(t, "outcome refs", got.OutcomeRefs, want.OutcomeRefs)
	assertStringSlice(t, "promotion refs", got.PromotionRefs, want.PromotionRefs)
}

func assertStringSlice(t *testing.T, name string, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("unexpected %s: got %#v want %#v", name, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("unexpected %s: got %#v want %#v", name, got, want)
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
