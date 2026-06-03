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
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
)

const (
	labelClaim                = "Claim"
	labelEvidence             = "Evidence"
	labelDecision             = "Decision"
	labelAction               = "Action"
	labelOutcome              = "Outcome"
	labelConfidenceAssessment = "ConfidenceAssessment"
	labelConfidenceFactor     = "ConfidenceFactor"

	relClaimHasEvidence       = "CLAIM_HAS_EVIDENCE"
	relClaimHasDecision       = "CLAIM_HAS_DECISION"
	relDecisionProducedAction = "DECISION_PRODUCED_ACTION"
	relActionProducedOutcome  = "ACTION_PRODUCED_OUTCOME"
	relClaimHasConfidence     = "CLAIM_HAS_CONFIDENCE"
	relConfidenceHasFactor    = "CONFIDENCE_HAS_FACTOR"
	relConfidenceUsedEvidence = "CONFIDENCE_USED_EVIDENCE"
	relConfidenceUsedDecision = "CONFIDENCE_USED_DECISION"
	relConfidenceUsedOutcome  = "CONFIDENCE_USED_OUTCOME"
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

func (store *LadybugStore) CreateAssessment(ctx context.Context, assessment projectconfidence.ConfidenceAssessment) (projectconfidence.ConfidenceAssessment, error) {
	assessment = cloneAssessment(assessment)
	return assessment, store.write(ctx, func(graph ladybug.Graph) error {
		claimRef := nodeRef(labelClaim, graphID(assessment.ProjectID, assessment.ClaimID))
		if _, err := graph.GetNode(ctx, claimRef.Label, claimRef.ID); errors.Is(err, ladybug.ErrNodeNotFound) {
			return ErrNotFound
		} else if err != nil {
			return err
		}

		assessmentRef := nodeRef(labelConfidenceAssessment, graphID(assessment.ProjectID, assessment.ID))
		if err := graph.DeleteNodes(ctx, labelConfidenceAssessment, map[string]string{"project_id": assessment.ProjectID, "claim_id": assessment.ClaimID}); err != nil {
			return err
		}
		if err := graph.DeleteNodes(ctx, labelConfidenceFactor, map[string]string{"project_id": assessment.ProjectID, "claim_id": assessment.ClaimID, "assessment_id": assessment.ID}); err != nil {
			return err
		}
		if err := graph.PutNode(ctx, assessmentNode(assessment)); err != nil {
			return err
		}
		if err := graph.PutRelationship(ctx, confidenceRelationship(relClaimHasConfidence, claimRef, assessmentRef, assessment)); err != nil {
			return err
		}
		for index, factor := range assessment.Factors {
			factorRef := nodeRef(labelConfidenceFactor, factorID(assessment.ProjectID, assessment.ID, index))
			if err := graph.PutNode(ctx, factorNode(assessment, factor, index)); err != nil {
				return err
			}
			if err := graph.PutRelationship(ctx, confidenceRelationship(relConfidenceHasFactor, assessmentRef, factorRef, assessment)); err != nil {
				return err
			}
		}
		return store.linkEvidenceGraphChain(ctx, graph, claimRef, assessmentRef, assessment)
	})
}

func (store *LadybugStore) GetAssessment(ctx context.Context, projectID string, claimID string) (projectconfidence.ConfidenceAssessment, error) {
	nodes, err := store.graph.ListNodes(ctx, labelConfidenceAssessment, map[string]string{"project_id": projectID, "claim_id": claimID})
	if err != nil {
		return projectconfidence.ConfidenceAssessment{}, err
	}
	if len(nodes) == 0 {
		return projectconfidence.ConfidenceAssessment{}, ErrNotFound
	}
	sort.Slice(nodes, func(i, j int) bool {
		return parseTime(nodes[i].Properties["created_at"]).After(parseTime(nodes[j].Properties["created_at"]))
	})
	return store.nodeToAssessment(ctx, nodes[0])
}

func (store *LadybugStore) ListAssessments(ctx context.Context, projectID string, filter projectconfidence.AssessmentFilter) ([]projectconfidence.ConfidenceAssessment, error) {
	nodeFilter := map[string]string{"project_id": projectID}
	if filter.Band != "" {
		nodeFilter["band"] = filter.Band
	}
	if filter.Recommendation != "" {
		nodeFilter["recommendation"] = filter.Recommendation
	}
	if filter.RunID != "" {
		nodeFilter["run_id"] = filter.RunID
	}
	if filter.TraceID != "" {
		nodeFilter["trace_id"] = filter.TraceID
	}
	nodes, err := store.graph.ListNodes(ctx, labelConfidenceAssessment, nodeFilter)
	if err != nil {
		return nil, err
	}
	out := make([]projectconfidence.ConfidenceAssessment, 0, len(nodes))
	for _, node := range nodes {
		assessment := nodeToAssessmentMetadata(node)
		if !matchesFilter(assessment, filter) {
			continue
		}
		withFactors, err := store.nodeToAssessment(ctx, node)
		if err != nil {
			return nil, err
		}
		out = append(out, withFactors)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ClaimID < out[j].ClaimID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	if batch, ok := store.graph.(ladybug.BatchGraph); ok {
		return batch.Batch(ctx, fn)
	}
	return fn(store.graph)
}

func (store *LadybugStore) nodeToAssessment(ctx context.Context, node ladybug.Node) (projectconfidence.ConfidenceAssessment, error) {
	assessment := nodeToAssessmentMetadata(node)
	assessmentRef := nodeRef(labelConfidenceAssessment, node.ID)
	rels, err := store.graph.ListRelationships(ctx, relConfidenceHasFactor, ladybug.RelationshipFilter{From: &assessmentRef, Properties: map[string]string{"project_id": assessment.ProjectID}})
	if err != nil {
		return projectconfidence.ConfidenceAssessment{}, err
	}
	factors := make([]factorWithIndex, 0, len(rels))
	for _, rel := range rels {
		factorNode, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
		if err != nil {
			return projectconfidence.ConfidenceAssessment{}, err
		}
		factors = append(factors, factorWithIndex{index: parseInt(factorNode.Properties["factor_index"]), factor: nodeToFactor(factorNode)})
	}
	sort.Slice(factors, func(i, j int) bool { return factors[i].index < factors[j].index })
	assessment.Factors = make([]projectconfidence.ConfidenceFactor, 0, len(factors))
	for _, item := range factors {
		assessment.Factors = append(assessment.Factors, item.factor)
	}
	return assessment, nil
}

func (store *LadybugStore) linkEvidenceGraphChain(ctx context.Context, graph ladybug.Graph, claimRef ladybug.NodeRef, assessmentRef ladybug.NodeRef, assessment projectconfidence.ConfidenceAssessment) error {
	decisionRefs := make([]ladybug.NodeRef, 0)
	evidenceRels, err := graph.ListRelationships(ctx, relClaimHasEvidence, ladybug.RelationshipFilter{From: &claimRef, Properties: map[string]string{"project_id": assessment.ProjectID}})
	if err != nil {
		return err
	}
	for _, rel := range evidenceRels {
		if err := graph.PutRelationship(ctx, confidenceRelationship(relConfidenceUsedEvidence, assessmentRef, rel.To, assessment)); err != nil {
			return err
		}
	}
	decisionRels, err := graph.ListRelationships(ctx, relClaimHasDecision, ladybug.RelationshipFilter{From: &claimRef, Properties: map[string]string{"project_id": assessment.ProjectID}})
	if err != nil {
		return err
	}
	for _, rel := range decisionRels {
		decisionRefs = append(decisionRefs, rel.To)
		if err := graph.PutRelationship(ctx, confidenceRelationship(relConfidenceUsedDecision, assessmentRef, rel.To, assessment)); err != nil {
			return err
		}
	}
	for _, decisionRef := range decisionRefs {
		actionRels, err := graph.ListRelationships(ctx, relDecisionProducedAction, ladybug.RelationshipFilter{From: &decisionRef, Properties: map[string]string{"project_id": assessment.ProjectID}})
		if err != nil {
			return err
		}
		for _, actionRel := range actionRels {
			outcomeRels, err := graph.ListRelationships(ctx, relActionProducedOutcome, ladybug.RelationshipFilter{From: &actionRel.To, Properties: map[string]string{"project_id": assessment.ProjectID}})
			if err != nil {
				return err
			}
			for _, outcomeRel := range outcomeRels {
				if err := graph.PutRelationship(ctx, confidenceRelationship(relConfidenceUsedOutcome, assessmentRef, outcomeRel.To, assessment)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func assessmentNode(assessment projectconfidence.ConfidenceAssessment) ladybug.Node {
	return ladybug.Node{Label: labelConfidenceAssessment, ID: graphID(assessment.ProjectID, assessment.ID), Properties: map[string]string{
		"id":                               assessment.ID,
		"project_id":                       assessment.ProjectID,
		"claim_id":                         assessment.ClaimID,
		"claim_ref":                        assessment.ClaimRef,
		"run_id":                           assessment.RunID,
		"trace_id":                         assessment.TraceID,
		"score":                            strconv.Itoa(assessment.Score),
		"band":                             assessment.Band,
		"recommendation":                   assessment.Recommendation,
		"input_evidence_count":             strconv.Itoa(assessment.Inputs.EvidenceCount),
		"input_evidence_kinds":             strings.Join(assessment.Inputs.EvidenceKinds, ","),
		"input_decision_count":             strconv.Itoa(assessment.Inputs.DecisionCount),
		"input_action_count":               strconv.Itoa(assessment.Inputs.ActionCount),
		"input_passed_outcome_count":       strconv.Itoa(assessment.Inputs.PassedOutcomeCount),
		"input_failed_outcome_count":       strconv.Itoa(assessment.Inputs.FailedOutcomeCount),
		"input_promotion_state":            assessment.Inputs.PromotionState,
		"input_context_health_status":      assessment.Inputs.ContextHealthStatus,
		"input_context_health_reason":      assessment.Inputs.ContextHealthReason,
		"input_latest_run_age_seconds":     strconv.FormatInt(assessment.Inputs.LatestRunAgeSeconds, 10),
		"input_claim_check_verified":       strconv.Itoa(assessment.Inputs.ClaimCheckVerified),
		"input_claim_check_actionable":     strconv.Itoa(assessment.Inputs.ClaimCheckActionable),
		"input_impact_partial":             strconv.FormatBool(assessment.Inputs.ImpactPartial),
		"input_residual_unknown_count":     strconv.Itoa(assessment.Inputs.ImpactResidualUnknownCount),
		"input_impact_security_flag_count": strconv.Itoa(assessment.Inputs.ImpactSecurityFlagCount),
		"created_at":                       formatTime(assessment.CreatedAt),
		"updated_at":                       formatTime(assessment.UpdatedAt),
	}}
}

func factorNode(assessment projectconfidence.ConfidenceAssessment, factor projectconfidence.ConfidenceFactor, index int) ladybug.Node {
	return ladybug.Node{Label: labelConfidenceFactor, ID: factorID(assessment.ProjectID, assessment.ID, index), Properties: map[string]string{
		"id":            factorID(assessment.ProjectID, assessment.ID, index),
		"project_id":    assessment.ProjectID,
		"claim_id":      assessment.ClaimID,
		"assessment_id": assessment.ID,
		"factor_index":  strconv.Itoa(index),
		"name":          factor.Name,
		"score_delta":   strconv.Itoa(factor.ScoreDelta),
		"weight":        strconv.Itoa(factor.Weight),
		"status":        factor.Status,
		"summary":       factor.Summary,
		"source_ref":    factor.SourceRef,
	}}
}

func nodeToAssessmentMetadata(node ladybug.Node) projectconfidence.ConfidenceAssessment {
	props := node.Properties
	return projectconfidence.ConfidenceAssessment{
		ID:             props["id"],
		ProjectID:      props["project_id"],
		ClaimID:        props["claim_id"],
		ClaimRef:       props["claim_ref"],
		RunID:          props["run_id"],
		TraceID:        props["trace_id"],
		Score:          parseInt(props["score"]),
		Band:           props["band"],
		Recommendation: props["recommendation"],
		Inputs: projectconfidence.ConfidenceInputs{
			EvidenceCount:              parseInt(props["input_evidence_count"]),
			EvidenceKinds:              splitList(props["input_evidence_kinds"]),
			DecisionCount:              parseInt(props["input_decision_count"]),
			ActionCount:                parseInt(props["input_action_count"]),
			PassedOutcomeCount:         parseInt(props["input_passed_outcome_count"]),
			FailedOutcomeCount:         parseInt(props["input_failed_outcome_count"]),
			PromotionState:             props["input_promotion_state"],
			ContextHealthStatus:        props["input_context_health_status"],
			ContextHealthReason:        props["input_context_health_reason"],
			LatestRunAgeSeconds:        parseInt64(props["input_latest_run_age_seconds"]),
			ClaimCheckVerified:         parseInt(props["input_claim_check_verified"]),
			ClaimCheckActionable:       parseInt(props["input_claim_check_actionable"]),
			ImpactPartial:              parseBool(props["input_impact_partial"]),
			ImpactResidualUnknownCount: parseInt(props["input_residual_unknown_count"]),
			ImpactSecurityFlagCount:    parseInt(props["input_impact_security_flag_count"]),
		},
		CreatedAt: parseTime(props["created_at"]),
		UpdatedAt: parseTime(props["updated_at"]),
	}
}

func nodeToFactor(node ladybug.Node) projectconfidence.ConfidenceFactor {
	props := node.Properties
	return projectconfidence.ConfidenceFactor{
		Name:       props["name"],
		ScoreDelta: parseInt(props["score_delta"]),
		Weight:     parseInt(props["weight"]),
		Status:     props["status"],
		Summary:    props["summary"],
		SourceRef:  props["source_ref"],
	}
}

func confidenceRelationship(relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, assessment projectconfidence.ConfidenceAssessment) ladybug.Relationship {
	return ladybug.Relationship{Type: relationshipType, From: from, To: to, Properties: map[string]string{
		"project_id":    assessment.ProjectID,
		"claim_id":      assessment.ClaimID,
		"assessment_id": assessment.ID,
		"run_id":        assessment.RunID,
		"trace_id":      assessment.TraceID,
	}}
}

func nodeRef(label string, id string) ladybug.NodeRef {
	return ladybug.NodeRef{Label: label, ID: id}
}

func graphID(projectID string, id string) string {
	return projectID + ":" + id
}

func factorID(projectID string, assessmentID string, index int) string {
	return graphID(projectID, assessmentID+":factor:"+strconv.Itoa(index))
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

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
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

type factorWithIndex struct {
	index  int
	factor projectconfidence.ConfidenceFactor
}
