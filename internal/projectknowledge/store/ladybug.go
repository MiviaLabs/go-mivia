package store

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
)

const (
	labelProject                    = "Project"
	labelAgentRun                   = "AgentRun"
	labelClaim                      = "Claim"
	labelConfidenceAssessment       = "ConfidenceAssessment"
	labelKnowledgeRecord            = "KnowledgeRecord"
	labelKnowledgePromotionDecision = "KnowledgePromotionDecision"
	labelKnowledgeReuseEvent        = "KnowledgeReuseEvent"

	relProjectHasKnowledge           = "PROJECT_HAS_KNOWLEDGE"
	relClaimPromotedToKnowledge      = "CLAIM_PROMOTED_TO_KNOWLEDGE"
	relConfidenceSupportsKnowledge   = "CONFIDENCE_SUPPORTS_KNOWLEDGE"
	relKnowledgeHasPromotionDecision = "KNOWLEDGE_HAS_PROMOTION_DECISION"
	relKnowledgeSupersedesKnowledge  = "KNOWLEDGE_SUPERSEDES_KNOWLEDGE"
	relKnowledgeReusedByAgentRun     = "KNOWLEDGE_REUSED_BY_AGENT_RUN"
	relKnowledgeHasReuseEvent        = "KNOWLEDGE_HAS_REUSE_EVENT"
)

type LadybugStore struct {
	graph ladybug.Graph
}

func NewLadybugStore(graph ladybug.Graph) *LadybugStore {
	return &LadybugStore{graph: graph}
}

func NewBootstrappedLadybugStore(ctx context.Context, graph ladybug.Graph) (*LadybugStore, error) {
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		return nil, err
	}
	return NewLadybugStore(graph), nil
}

func (store *LadybugStore) CreateKnowledge(ctx context.Context, record projectknowledge.KnowledgeRecord) (projectknowledge.KnowledgeRecord, error) {
	record = cloneKnowledge(record)
	return cloneKnowledge(record), store.write(ctx, func(graph ladybug.Graph) error {
		return store.putKnowledge(ctx, graph, record)
	})
}

func (store *LadybugStore) GetKnowledge(ctx context.Context, projectID string, knowledgeID string) (projectknowledge.KnowledgeRecord, error) {
	node, err := store.graph.GetNode(ctx, labelKnowledgeRecord, graphID(projectID, knowledgeID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectknowledge.KnowledgeRecord{}, ErrNotFound
	}
	if err != nil {
		return projectknowledge.KnowledgeRecord{}, err
	}
	record := nodeToKnowledge(node)
	if record.ProjectID != projectID {
		return projectknowledge.KnowledgeRecord{}, ErrNotFound
	}
	return record, nil
}

func (store *LadybugStore) ListKnowledge(ctx context.Context, projectID string, filter projectknowledge.KnowledgeFilter) ([]projectknowledge.KnowledgeRecord, error) {
	nodeFilter := map[string]string{"project_id": projectID}
	if filter.Scope != "" {
		nodeFilter["scope"] = filter.Scope
	}
	if filter.State != "" {
		nodeFilter["state"] = filter.State
	}
	if filter.KnowledgeRef != "" {
		nodeFilter["knowledge_ref"] = filter.KnowledgeRef
	}
	if filter.ClaimID != "" {
		nodeFilter["claim_id"] = filter.ClaimID
	}
	if filter.ConfidenceBand != "" {
		nodeFilter["confidence_band"] = filter.ConfidenceBand
	}
	if filter.OrgRef != "" {
		nodeFilter["org_ref"] = filter.OrgRef
	}
	nodes, err := store.graph.ListNodes(ctx, labelKnowledgeRecord, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectknowledge.KnowledgeRecord, 0, len(nodes))
	for _, node := range nodes {
		record := nodeToKnowledge(node)
		if record.ProjectID != projectID || !matchesKnowledgeFilter(record, filter) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *LadybugStore) ListOrgKnowledge(ctx context.Context, orgRef string, filter projectknowledge.KnowledgeFilter) ([]projectknowledge.KnowledgeRecord, error) {
	nodeFilter := map[string]string{"org_ref": orgRef}
	if filter.Scope != "" {
		nodeFilter["scope"] = filter.Scope
	}
	if filter.State != "" {
		nodeFilter["state"] = filter.State
	}
	if filter.KnowledgeRef != "" {
		nodeFilter["knowledge_ref"] = filter.KnowledgeRef
	}
	if filter.ConfidenceBand != "" {
		nodeFilter["confidence_band"] = filter.ConfidenceBand
	}
	nodes, err := store.graph.ListNodes(ctx, labelKnowledgeRecord, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectknowledge.KnowledgeRecord, 0, len(nodes))
	for _, node := range nodes {
		record := nodeToKnowledge(node)
		if record.OrgRef != orgRef || !matchesKnowledgeFilter(record, filter) {
			continue
		}
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *LadybugStore) UpdateKnowledge(ctx context.Context, record projectknowledge.KnowledgeRecord) (projectknowledge.KnowledgeRecord, error) {
	record = cloneKnowledge(record)
	if _, err := store.GetKnowledge(ctx, record.ProjectID, record.ID); err != nil {
		return projectknowledge.KnowledgeRecord{}, err
	}
	return cloneKnowledge(record), store.write(ctx, func(graph ladybug.Graph) error {
		return store.putKnowledge(ctx, graph, record)
	})
}

func (store *LadybugStore) CreateDecision(ctx context.Context, decision projectknowledge.PromotionDecision) (projectknowledge.PromotionDecision, error) {
	if _, err := store.GetKnowledge(ctx, decision.ProjectID, decision.KnowledgeID); err != nil {
		return projectknowledge.PromotionDecision{}, err
	}
	return decision, store.write(ctx, func(graph ladybug.Graph) error {
		knowledgeRef := nodeRef(labelKnowledgeRecord, graphID(decision.ProjectID, decision.KnowledgeID))
		decisionRef := nodeRef(labelKnowledgePromotionDecision, graphID(decision.ProjectID, decision.ID))
		if err := graph.PutNode(ctx, decisionNode(decision)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relKnowledgeHasPromotionDecision, knowledgeRef, decisionRef, decision.ProjectID, decision.KnowledgeID))
	})
}

func (store *LadybugStore) ListDecisions(ctx context.Context, projectID string, knowledgeID string) ([]projectknowledge.PromotionDecision, error) {
	if _, err := store.GetKnowledge(ctx, projectID, knowledgeID); err != nil {
		return nil, err
	}
	knowledgeRef := nodeRef(labelKnowledgeRecord, graphID(projectID, knowledgeID))
	rels, err := store.graph.ListRelationships(ctx, relKnowledgeHasPromotionDecision, ladybug.RelationshipFilter{From: &knowledgeRef, Properties: map[string]string{"project_id": projectID}})
	if err != nil {
		return nil, err
	}
	out := make([]projectknowledge.PromotionDecision, 0, len(rels))
	for _, rel := range rels {
		node, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
		if err != nil {
			return nil, err
		}
		decision := nodeToDecision(node)
		if decision.ProjectID == projectID && decision.KnowledgeID == knowledgeID {
			out = append(out, decision)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DecidedAt.Before(out[j].DecidedAt) })
	return out, nil
}

func (store *LadybugStore) CreateReuseEvent(ctx context.Context, event projectknowledge.KnowledgeReuseEvent) (projectknowledge.KnowledgeReuseEvent, error) {
	if _, err := store.GetKnowledge(ctx, event.ProjectID, event.KnowledgeID); err != nil {
		return projectknowledge.KnowledgeReuseEvent{}, err
	}
	return event, store.write(ctx, func(graph ladybug.Graph) error {
		knowledgeRef := nodeRef(labelKnowledgeRecord, graphID(event.ProjectID, event.KnowledgeID))
		eventRef := nodeRef(labelKnowledgeReuseEvent, graphID(event.ProjectID, event.ID))
		if err := graph.PutNode(ctx, reuseEventNode(event)); err != nil {
			return err
		}
		if err := graph.PutRelationship(ctx, relationship(relKnowledgeHasReuseEvent, knowledgeRef, eventRef, event.ProjectID, event.KnowledgeID)); err != nil {
			return err
		}
		if event.AgentRunID != "" {
			return graph.PutRelationship(ctx, relationship(relKnowledgeReusedByAgentRun, knowledgeRef, nodeRef(labelAgentRun, event.AgentRunID), event.ProjectID, event.KnowledgeID))
		}
		return nil
	})
}

func (store *LadybugStore) ListReuseEvents(ctx context.Context, projectID string, knowledgeID string) ([]projectknowledge.KnowledgeReuseEvent, error) {
	if _, err := store.GetKnowledge(ctx, projectID, knowledgeID); err != nil {
		return nil, err
	}
	knowledgeRef := nodeRef(labelKnowledgeRecord, graphID(projectID, knowledgeID))
	rels, err := store.graph.ListRelationships(ctx, relKnowledgeHasReuseEvent, ladybug.RelationshipFilter{From: &knowledgeRef, Properties: map[string]string{"project_id": projectID}})
	if err != nil {
		return nil, err
	}
	out := make([]projectknowledge.KnowledgeReuseEvent, 0, len(rels))
	for _, rel := range rels {
		node, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
		if err != nil {
			return nil, err
		}
		event := nodeToReuseEvent(node)
		if event.ProjectID == projectID && event.KnowledgeID == knowledgeID {
			out = append(out, event)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	if batch, ok := store.graph.(ladybug.BatchGraph); ok {
		return batch.Batch(ctx, fn)
	}
	return fn(store.graph)
}

func (store *LadybugStore) putKnowledge(ctx context.Context, graph ladybug.Graph, record projectknowledge.KnowledgeRecord) error {
	knowledgeRef := nodeRef(labelKnowledgeRecord, graphID(record.ProjectID, record.ID))
	if err := graph.PutNode(ctx, knowledgeNode(record)); err != nil {
		return err
	}
	if err := graph.PutRelationship(ctx, relationship(relProjectHasKnowledge, nodeRef(labelProject, record.ProjectID), knowledgeRef, record.ProjectID, record.ID)); err != nil {
		return err
	}
	if err := graph.PutRelationship(ctx, relationship(relClaimPromotedToKnowledge, nodeRef(labelClaim, graphID(record.ProjectID, record.ClaimID)), knowledgeRef, record.ProjectID, record.ID)); err != nil {
		return err
	}
	if record.ConfidenceAssessmentID != "" {
		if err := graph.PutRelationship(ctx, relationship(relConfidenceSupportsKnowledge, nodeRef(labelConfidenceAssessment, graphID(record.ProjectID, record.ConfidenceAssessmentID)), knowledgeRef, record.ProjectID, record.ID)); err != nil {
			return err
		}
	}
	if record.SupersedesRef != "" {
		if err := graph.PutRelationship(ctx, relationship(relKnowledgeSupersedesKnowledge, knowledgeRef, nodeRef(labelKnowledgeRecord, graphID(record.ProjectID, record.SupersedesRef)), record.ProjectID, record.ID)); err != nil {
			return err
		}
	}
	if record.SupersededByRef != "" {
		if err := graph.PutRelationship(ctx, relationship(relKnowledgeSupersedesKnowledge, nodeRef(labelKnowledgeRecord, graphID(record.ProjectID, record.SupersededByRef)), knowledgeRef, record.ProjectID, record.SupersededByRef)); err != nil {
			return err
		}
	}
	return nil
}

func knowledgeNode(record projectknowledge.KnowledgeRecord) ladybug.Node {
	return ladybug.Node{Label: labelKnowledgeRecord, ID: graphID(record.ProjectID, record.ID), Properties: map[string]string{
		"id":                       record.ID,
		"project_id":               record.ProjectID,
		"scope":                    record.Scope,
		"org_ref":                  record.OrgRef,
		"knowledge_ref":            record.KnowledgeRef,
		"claim_id":                 record.ClaimID,
		"claim_ref":                record.ClaimRef,
		"confidence_assessment_id": record.ConfidenceAssessmentID,
		"confidence_score":         strconv.Itoa(record.ConfidenceScore),
		"confidence_band":          record.ConfidenceBand,
		"state":                    record.State,
		"summary":                  record.Summary,
		"reuse_guidance":           record.ReuseGuidance,
		"evidence_refs":            strings.Join(record.EvidenceRefs, ","),
		"verifier_refs":            strings.Join(record.VerifierRefs, ","),
		"outcome_refs":             strings.Join(record.OutcomeRefs, ","),
		"promotion_refs":           strings.Join(record.PromotionRefs, ","),
		"supersedes_ref":           record.SupersedesRef,
		"superseded_by_ref":        record.SupersededByRef,
		"created_at":               formatTime(record.CreatedAt),
		"updated_at":               formatTime(record.UpdatedAt),
		"promoted_at":              formatTime(record.PromotedAt),
	}}
}

func decisionNode(decision projectknowledge.PromotionDecision) ladybug.Node {
	return ladybug.Node{Label: labelKnowledgePromotionDecision, ID: graphID(decision.ProjectID, decision.ID), Properties: map[string]string{
		"id":               decision.ID,
		"project_id":       decision.ProjectID,
		"knowledge_id":     decision.KnowledgeID,
		"from_state":       decision.FromState,
		"to_state":         decision.ToState,
		"scope":            decision.Scope,
		"decision_ref":     decision.DecisionRef,
		"verifier_ref":     decision.VerifierRef,
		"confidence_score": strconv.Itoa(decision.ConfidenceScore),
		"rationale":        decision.Rationale,
		"decided_by":       decision.DecidedBy,
		"decided_at":       formatTime(decision.DecidedAt),
	}}
}

func reuseEventNode(event projectknowledge.KnowledgeReuseEvent) ladybug.Node {
	return ladybug.Node{Label: labelKnowledgeReuseEvent, ID: graphID(event.ProjectID, event.ID), Properties: map[string]string{
		"id":               event.ID,
		"project_id":       event.ProjectID,
		"knowledge_id":     event.KnowledgeID,
		"agent_run_id":     event.AgentRunID,
		"trace_id":         event.TraceID,
		"reuse_ref":        event.ReuseRef,
		"revalidated":      strconv.FormatBool(event.Revalidated),
		"revalidation_ref": event.RevalidationRef,
		"outcome":          event.Outcome,
		"summary":          event.Summary,
		"created_at":       formatTime(event.CreatedAt),
	}}
}

func nodeToKnowledge(node ladybug.Node) projectknowledge.KnowledgeRecord {
	props := node.Properties
	return projectknowledge.KnowledgeRecord{
		ID:                     props["id"],
		ProjectID:              props["project_id"],
		Scope:                  props["scope"],
		OrgRef:                 props["org_ref"],
		KnowledgeRef:           props["knowledge_ref"],
		ClaimID:                props["claim_id"],
		ClaimRef:               props["claim_ref"],
		ConfidenceAssessmentID: props["confidence_assessment_id"],
		ConfidenceScore:        parseInt(props["confidence_score"]),
		ConfidenceBand:         props["confidence_band"],
		State:                  props["state"],
		Summary:                props["summary"],
		ReuseGuidance:          props["reuse_guidance"],
		EvidenceRefs:           splitList(props["evidence_refs"]),
		VerifierRefs:           splitList(props["verifier_refs"]),
		OutcomeRefs:            splitList(props["outcome_refs"]),
		PromotionRefs:          splitList(props["promotion_refs"]),
		SupersedesRef:          props["supersedes_ref"],
		SupersededByRef:        props["superseded_by_ref"],
		CreatedAt:              parseTime(props["created_at"]),
		UpdatedAt:              parseTime(props["updated_at"]),
		PromotedAt:             parseTime(props["promoted_at"]),
	}
}

func nodeToDecision(node ladybug.Node) projectknowledge.PromotionDecision {
	props := node.Properties
	return projectknowledge.PromotionDecision{
		ID:              props["id"],
		ProjectID:       props["project_id"],
		KnowledgeID:     props["knowledge_id"],
		FromState:       props["from_state"],
		ToState:         props["to_state"],
		Scope:           props["scope"],
		DecisionRef:     props["decision_ref"],
		VerifierRef:     props["verifier_ref"],
		ConfidenceScore: parseInt(props["confidence_score"]),
		Rationale:       props["rationale"],
		DecidedBy:       props["decided_by"],
		DecidedAt:       parseTime(props["decided_at"]),
	}
}

func nodeToReuseEvent(node ladybug.Node) projectknowledge.KnowledgeReuseEvent {
	props := node.Properties
	return projectknowledge.KnowledgeReuseEvent{
		ID:              props["id"],
		ProjectID:       props["project_id"],
		KnowledgeID:     props["knowledge_id"],
		AgentRunID:      props["agent_run_id"],
		TraceID:         props["trace_id"],
		ReuseRef:        props["reuse_ref"],
		Revalidated:     parseBool(props["revalidated"]),
		RevalidationRef: props["revalidation_ref"],
		Outcome:         props["outcome"],
		Summary:         props["summary"],
		CreatedAt:       parseTime(props["created_at"]),
	}
}

func relationship(relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string, knowledgeID string) ladybug.Relationship {
	return ladybug.Relationship{Type: relationshipType, From: from, To: to, Properties: map[string]string{"project_id": projectID, "knowledge_id": knowledgeID}}
}

func nodeRef(label string, id string) ladybug.NodeRef {
	return ladybug.NodeRef{Label: label, ID: id}
}

func graphID(projectID string, id string) string {
	return projectID + ":" + id
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func parseBool(value string) bool {
	parsed, _ := strconv.ParseBool(value)
	return parsed
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
