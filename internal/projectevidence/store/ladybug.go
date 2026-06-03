package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

const (
	labelClaim     = "Claim"
	labelEvidence  = "Evidence"
	labelDecision  = "Decision"
	labelAction    = "Action"
	labelOutcome   = "Outcome"
	labelArtifact  = "Artifact"
	labelPromotion = "Promotion"
	labelAgentRun  = "AgentRun"
	labelProject   = "Project"

	relProjectHasClaim          = "PROJECT_HAS_CLAIM"
	relAgentRunMadeClaim        = "AGENT_RUN_MADE_CLAIM"
	relClaimHasEvidence         = "CLAIM_HAS_EVIDENCE"
	relEvidenceSupportsDecision = "EVIDENCE_SUPPORTS_DECISION"
	relClaimHasDecision         = "CLAIM_HAS_DECISION"
	relDecisionProducedAction   = "DECISION_PRODUCED_ACTION"
	relActionProducedOutcome    = "ACTION_PRODUCED_OUTCOME"
	relActionProducedArtifact   = "ACTION_PRODUCED_ARTIFACT"
	relArtifactHasPromotion     = "ARTIFACT_HAS_PROMOTION"
	relPromotionDecidesClaim    = "PROMOTION_DECIDES_CLAIM"
	relOutcomeSupportsPromotion = "OUTCOME_SUPPORTS_PROMOTION"
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

func (store *LadybugStore) CreateClaim(ctx context.Context, claim projectevidence.Claim) (projectevidence.Claim, error) {
	return claim, store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, claimNode(claim)); err != nil {
			return err
		}
		claimRef := nodeRef(labelClaim, graphID(claim.ProjectID, claim.ID))
		if err := graph.PutRelationship(ctx, relationship(relProjectHasClaim, nodeRef(labelProject, claim.ProjectID), claimRef, claim.ProjectID, claim.ID, claim.RunID)); err != nil {
			return err
		}
		if claim.RunID != "" {
			return graph.PutRelationship(ctx, relationship(relAgentRunMadeClaim, nodeRef(labelAgentRun, claim.RunID), claimRef, claim.ProjectID, claim.ID, claim.RunID))
		}
		return nil
	})
}

func (store *LadybugStore) GetClaim(ctx context.Context, projectID string, claimID string) (projectevidence.ClaimRecord, error) {
	claim, err := store.getClaim(ctx, projectID, claimID)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	record := projectevidence.ClaimRecord{Claim: claim}
	claimRef := nodeRef(labelClaim, graphID(projectID, claim.ID))
	record.Evidence, err = store.evidenceForClaim(ctx, projectID, claimRef)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	record.Decisions, err = store.decisionsForClaim(ctx, projectID, claimRef)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	record.Actions, err = store.actionsForDecisions(ctx, projectID, record.Decisions)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	record.Outcomes, err = store.outcomesForActions(ctx, projectID, record.Actions)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	record.ArtifactLinks, err = store.artifactsForClaim(ctx, projectID, claim.ID)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	record.PromotionLinks, err = store.promotionsForClaim(ctx, projectID, claim.ID)
	if err != nil {
		return projectevidence.ClaimRecord{}, err
	}
	return record, nil
}

func (store *LadybugStore) ListClaims(ctx context.Context, projectID string, filter projectevidence.ClaimFilter) ([]projectevidence.Claim, error) {
	nodeFilter := map[string]string{"project_id": projectID}
	if filter.RunID != "" {
		nodeFilter["run_id"] = filter.RunID
	}
	if filter.TraceID != "" {
		nodeFilter["trace_id"] = filter.TraceID
	}
	nodes, err := store.graph.ListNodes(ctx, labelClaim, nodeFilter)
	if err != nil {
		return nil, err
	}
	claims := make([]projectevidence.Claim, 0, len(nodes))
	for _, node := range nodes {
		claim := nodeToClaim(node)
		if claim.ProjectID != projectID {
			continue
		}
		if !store.claimMatchesFilter(ctx, claim, filter) {
			continue
		}
		claims = append(claims, claim)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].CreatedAt.Before(claims[j].CreatedAt) })
	return claims, nil
}

func (store *LadybugStore) AppendEvidence(ctx context.Context, evidence projectevidence.Evidence) (projectevidence.Evidence, error) {
	claim, err := store.getClaim(ctx, evidence.ProjectID, evidence.ClaimID)
	if err != nil {
		return projectevidence.Evidence{}, err
	}
	evidenceNode := evidenceNode(evidence, claim.RunID)
	return evidence, store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, evidenceNode); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relClaimHasEvidence, nodeRef(labelClaim, graphID(claim.ProjectID, claim.ID)), nodeRef(labelEvidence, graphID(evidence.ProjectID, evidence.ID)), evidence.ProjectID, claim.ID, claim.RunID))
	})
}

func (store *LadybugStore) CreateDecision(ctx context.Context, decision projectevidence.Decision) (projectevidence.Decision, error) {
	claim, err := store.getClaim(ctx, decision.ProjectID, decision.ClaimID)
	if err != nil {
		return projectevidence.Decision{}, err
	}
	return decision, store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, decisionNode(decision, claim.RunID)); err != nil {
			return err
		}
		claimRef := nodeRef(labelClaim, graphID(claim.ProjectID, claim.ID))
		decisionRef := nodeRef(labelDecision, graphID(decision.ProjectID, decision.ID))
		if err := graph.PutRelationship(ctx, relationship(relClaimHasDecision, claimRef, decisionRef, decision.ProjectID, claim.ID, claim.RunID)); err != nil {
			return err
		}
		evidenceRels, err := graph.ListRelationships(ctx, relClaimHasEvidence, ladybug.RelationshipFilter{From: &claimRef, Properties: map[string]string{"project_id": decision.ProjectID}})
		if err != nil {
			return err
		}
		for _, evidenceRel := range evidenceRels {
			if err := graph.PutRelationship(ctx, relationship(relEvidenceSupportsDecision, evidenceRel.To, decisionRef, decision.ProjectID, claim.ID, claim.RunID)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (store *LadybugStore) CreateAction(ctx context.Context, action projectevidence.Action) (projectevidence.Action, error) {
	claim, err := store.getClaim(ctx, action.ProjectID, action.ClaimID)
	if err != nil {
		return projectevidence.Action{}, err
	}
	runID := action.RunID
	if runID == "" {
		runID = claim.RunID
	}
	return cloneAction(action), store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, actionNode(action, runID)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relDecisionProducedAction, nodeRef(labelDecision, graphID(action.ProjectID, action.DecisionID)), nodeRef(labelAction, graphID(action.ProjectID, action.ID)), action.ProjectID, action.ClaimID, runID))
	})
}

func (store *LadybugStore) CreateOutcome(ctx context.Context, outcome projectevidence.Outcome) (projectevidence.Outcome, error) {
	action, err := store.getAction(ctx, outcome.ProjectID, outcome.ActionID)
	if err != nil {
		return projectevidence.Outcome{}, err
	}
	return outcome, store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, outcomeNode(outcome, action.RunID)); err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relActionProducedOutcome, nodeRef(labelAction, graphID(outcome.ProjectID, outcome.ActionID)), nodeRef(labelOutcome, graphID(outcome.ProjectID, outcome.ID)), outcome.ProjectID, outcome.ClaimID, action.RunID))
	})
}

func (store *LadybugStore) LinkArtifact(ctx context.Context, link projectevidence.ArtifactLink) (projectevidence.ArtifactLink, error) {
	claim, err := store.getClaim(ctx, link.ProjectID, link.ClaimID)
	if err != nil {
		return projectevidence.ArtifactLink{}, err
	}
	runID := link.RunID
	if runID == "" {
		runID = claim.RunID
	}
	id := artifactID(link.ProjectID, link.ClaimID, link.ArtifactRef)
	return link, store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, artifactNode(id, link, runID)); err != nil {
			return err
		}
		actionRef, err := store.actionRefForArtifact(ctx, graph, link.ProjectID, link.ClaimID, runID)
		if err != nil {
			return err
		}
		return graph.PutRelationship(ctx, relationship(relActionProducedArtifact, actionRef, nodeRef(labelArtifact, id), link.ProjectID, link.ClaimID, runID))
	})
}

func (store *LadybugStore) LinkPromotion(ctx context.Context, link projectevidence.PromotionLink) (projectevidence.PromotionLink, error) {
	claim, err := store.getClaim(ctx, link.ProjectID, link.ClaimID)
	if err != nil {
		return projectevidence.PromotionLink{}, err
	}
	runID := link.RunID
	if runID == "" {
		runID = claim.RunID
	}
	id := promotionID(link.ProjectID, link.ClaimID, link.ArtifactRef, link.PromotionState, link.SourceRef, link.ActionRef, link.OutcomeRef)
	artifactRef := nodeRef(labelArtifact, artifactID(link.ProjectID, link.ClaimID, link.ArtifactRef))
	if _, err := store.graph.GetNode(ctx, artifactRef.Label, artifactRef.ID); errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectevidence.PromotionLink{}, ErrNotFound
	} else if err != nil {
		return projectevidence.PromotionLink{}, err
	}
	link.RunID = runID
	return link, store.write(ctx, func(graph ladybug.Graph) error {
		if err := graph.PutNode(ctx, promotionNode(id, link, runID)); err != nil {
			return err
		}
		promotionRef := nodeRef(labelPromotion, id)
		if err := graph.PutRelationship(ctx, relationship(relArtifactHasPromotion, artifactRef, promotionRef, link.ProjectID, link.ClaimID, runID)); err != nil {
			return err
		}
		if err := graph.PutRelationship(ctx, relationship(relPromotionDecidesClaim, promotionRef, nodeRef(labelClaim, graphID(claim.ProjectID, claim.ID)), link.ProjectID, link.ClaimID, runID)); err != nil {
			return err
		}
		outcomes, err := store.outcomesForClaim(ctx, graph, link.ProjectID, link.ClaimID)
		if err != nil {
			return err
		}
		for _, outcome := range outcomes {
			if link.OutcomeRef != "" && outcome.OutcomeRef != link.OutcomeRef {
				continue
			}
			if err := graph.PutRelationship(ctx, relationship(relOutcomeSupportsPromotion, nodeRef(labelOutcome, graphID(outcome.ProjectID, outcome.ID)), promotionRef, link.ProjectID, link.ClaimID, runID)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (store *LadybugStore) write(ctx context.Context, fn func(ladybug.Graph) error) error {
	if batch, ok := store.graph.(ladybug.BatchGraph); ok {
		return batch.Batch(ctx, fn)
	}
	return fn(store.graph)
}

func (store *LadybugStore) getClaim(ctx context.Context, projectID string, claimID string) (projectevidence.Claim, error) {
	node, err := store.graph.GetNode(ctx, labelClaim, graphID(projectID, claimID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectevidence.Claim{}, ErrNotFound
	}
	if err != nil {
		return projectevidence.Claim{}, err
	}
	claim := nodeToClaim(node)
	if claim.ProjectID != projectID {
		return projectevidence.Claim{}, ErrNotFound
	}
	return claim, nil
}

func (store *LadybugStore) getAction(ctx context.Context, projectID string, actionID string) (projectevidence.Action, error) {
	node, err := store.graph.GetNode(ctx, labelAction, graphID(projectID, actionID))
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return projectevidence.Action{}, ErrNotFound
	}
	if err != nil {
		return projectevidence.Action{}, err
	}
	action := nodeToAction(node)
	if action.ProjectID != projectID {
		return projectevidence.Action{}, ErrNotFound
	}
	return action, nil
}

func (store *LadybugStore) evidenceForClaim(ctx context.Context, projectID string, claimRef ladybug.NodeRef) ([]projectevidence.Evidence, error) {
	rels, err := store.claimRelationships(ctx, projectID, claimRef, relClaimHasEvidence)
	if err != nil {
		return nil, err
	}
	out := make([]projectevidence.Evidence, 0, len(rels))
	for _, rel := range rels {
		node, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, nodeToEvidence(node))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (store *LadybugStore) decisionsForClaim(ctx context.Context, projectID string, claimRef ladybug.NodeRef) ([]projectevidence.Decision, error) {
	rels, err := store.claimRelationships(ctx, projectID, claimRef, relClaimHasDecision)
	if err != nil {
		return nil, err
	}
	out := make([]projectevidence.Decision, 0, len(rels))
	for _, rel := range rels {
		node, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, nodeToDecision(node))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DecidedAt.Before(out[j].DecidedAt) })
	return out, nil
}

func (store *LadybugStore) actionsForDecisions(ctx context.Context, projectID string, decisions []projectevidence.Decision) ([]projectevidence.Action, error) {
	out := make([]projectevidence.Action, 0)
	for _, decision := range decisions {
		rels, err := store.graph.ListRelationships(ctx, relDecisionProducedAction, ladybug.RelationshipFilter{From: refPtr(nodeRef(labelDecision, graphID(projectID, decision.ID))), Properties: map[string]string{"project_id": projectID}})
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			node, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
			if err != nil {
				return nil, err
			}
			action := nodeToAction(node)
			if action.ProjectID == projectID {
				out = append(out, action)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (store *LadybugStore) outcomesForActions(ctx context.Context, projectID string, actions []projectevidence.Action) ([]projectevidence.Outcome, error) {
	out := make([]projectevidence.Outcome, 0)
	for _, action := range actions {
		rels, err := store.graph.ListRelationships(ctx, relActionProducedOutcome, ladybug.RelationshipFilter{From: refPtr(nodeRef(labelAction, graphID(projectID, action.ID))), Properties: map[string]string{"project_id": projectID}})
		if err != nil {
			return nil, err
		}
		for _, rel := range rels {
			node, err := store.graph.GetNode(ctx, rel.To.Label, rel.To.ID)
			if err != nil {
				return nil, err
			}
			outcome := nodeToOutcome(node)
			if outcome.ProjectID == projectID {
				out = append(out, outcome)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (store *LadybugStore) outcomesForClaim(ctx context.Context, graph ladybug.Graph, projectID string, claimID string) ([]projectevidence.Outcome, error) {
	nodes, err := graph.ListNodes(ctx, labelOutcome, map[string]string{"project_id": projectID, "claim_id": claimID})
	if err != nil {
		return nil, err
	}
	out := make([]projectevidence.Outcome, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, nodeToOutcome(node))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (store *LadybugStore) actionRefForArtifact(ctx context.Context, graph ladybug.Graph, projectID string, claimID string, runID string) (ladybug.NodeRef, error) {
	filter := map[string]string{"project_id": projectID, "claim_id": claimID}
	if runID != "" {
		filter["run_id"] = runID
	}
	nodes, err := graph.ListNodes(ctx, labelAction, filter)
	if err != nil {
		return ladybug.NodeRef{}, err
	}
	if len(nodes) == 0 && runID != "" {
		nodes, err = graph.ListNodes(ctx, labelAction, map[string]string{"project_id": projectID, "claim_id": claimID})
		if err != nil {
			return ladybug.NodeRef{}, err
		}
	}
	if len(nodes) == 0 {
		return ladybug.NodeRef{}, ErrNotFound
	}
	sort.Slice(nodes, func(i, j int) bool {
		return parseTime(nodes[i].Properties["created_at"]).Before(parseTime(nodes[j].Properties["created_at"]))
	})
	return nodeRef(labelAction, nodes[len(nodes)-1].ID), nil
}

func (store *LadybugStore) artifactsForClaim(ctx context.Context, projectID string, claimID string) ([]projectevidence.ArtifactLink, error) {
	nodes, err := store.graph.ListNodes(ctx, labelArtifact, map[string]string{"project_id": projectID, "claim_id": claimID})
	if err != nil {
		return nil, err
	}
	out := make([]projectevidence.ArtifactLink, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, nodeToArtifactLink(node))
	}
	return out, nil
}

func (store *LadybugStore) promotionsForClaim(ctx context.Context, projectID string, claimID string) ([]projectevidence.PromotionLink, error) {
	nodes, err := store.graph.ListNodes(ctx, labelPromotion, map[string]string{"project_id": projectID, "claim_id": claimID})
	if err != nil {
		return nil, err
	}
	out := make([]projectevidence.PromotionLink, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, nodeToPromotionLink(node))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DecidedAt.Before(out[j].DecidedAt) })
	return out, nil
}

func (store *LadybugStore) claimRelationships(ctx context.Context, projectID string, claimRef ladybug.NodeRef, relationshipType string) ([]ladybug.Relationship, error) {
	return store.graph.ListRelationships(ctx, relationshipType, ladybug.RelationshipFilter{From: &claimRef, Properties: map[string]string{"project_id": projectID}})
}

func (store *LadybugStore) claimMatchesFilter(ctx context.Context, claim projectevidence.Claim, filter projectevidence.ClaimFilter) bool {
	if filter.ArtifactRef != "" {
		artifactNodes, err := store.graph.ListNodes(ctx, labelArtifact, map[string]string{"project_id": claim.ProjectID, "claim_id": claim.ID, "artifact_ref": filter.ArtifactRef})
		if err != nil || len(artifactNodes) == 0 {
			promotionNodes, nodeErr := store.graph.ListNodes(ctx, labelPromotion, map[string]string{"project_id": claim.ProjectID, "claim_id": claim.ID, "artifact_ref": filter.ArtifactRef})
			return nodeErr == nil && len(promotionNodes) > 0
		}
	}
	if filter.PromotionState != "" {
		nodes, err := store.graph.ListNodes(ctx, labelPromotion, map[string]string{"project_id": claim.ProjectID, "claim_id": claim.ID, "promotion_state": filter.PromotionState})
		if err != nil || len(nodes) == 0 {
			return false
		}
	}
	if filter.OutcomeStatus != "" {
		nodes, err := store.graph.ListNodes(ctx, labelOutcome, map[string]string{"project_id": claim.ProjectID, "claim_id": claim.ID, "status": filter.OutcomeStatus})
		if err != nil || len(nodes) == 0 {
			return false
		}
	}
	return true
}

func claimNode(claim projectevidence.Claim) ladybug.Node {
	return ladybug.Node{Label: labelClaim, ID: graphID(claim.ProjectID, claim.ID), Properties: map[string]string{
		"id": claim.ID, "project_id": claim.ProjectID, "run_id": claim.RunID, "trace_id": claim.TraceID, "claim_ref": claim.ClaimRef, "summary": claim.Summary, "status": claim.Status, "created_at": formatTime(claim.CreatedAt), "updated_at": formatTime(claim.UpdatedAt),
	}}
}

func evidenceNode(evidence projectevidence.Evidence, runID string) ladybug.Node {
	return ladybug.Node{Label: labelEvidence, ID: graphID(evidence.ProjectID, evidence.ID), Properties: map[string]string{
		"id": evidence.ID, "project_id": evidence.ProjectID, "claim_id": evidence.ClaimID, "run_id": runID, "evidence_ref": evidence.EvidenceRef, "evidence_kind": evidence.EvidenceKind, "source_ref": evidence.SourceRef, "summary": evidence.Summary, "created_at": formatTime(evidence.CreatedAt),
	}}
}

func decisionNode(decision projectevidence.Decision, runID string) ladybug.Node {
	return ladybug.Node{Label: labelDecision, ID: graphID(decision.ProjectID, decision.ID), Properties: map[string]string{
		"id": decision.ID, "project_id": decision.ProjectID, "claim_id": decision.ClaimID, "run_id": runID, "decision_ref": decision.DecisionRef, "state": decision.State, "verifier_ref": decision.VerifierRef, "rationale": decision.Rationale, "decided_at": formatTime(decision.DecidedAt),
	}}
}

func actionNode(action projectevidence.Action, runID string) ladybug.Node {
	return ladybug.Node{Label: labelAction, ID: graphID(action.ProjectID, action.ID), Properties: map[string]string{
		"id": action.ID, "project_id": action.ProjectID, "claim_id": action.ClaimID, "decision_id": action.DecisionID, "run_id": runID, "action_ref": action.ActionRef, "action_kind": action.ActionKind, "summary": action.Summary, "changed_files": strings.Join(action.ChangedFiles, ","), "created_at": formatTime(action.CreatedAt),
	}}
}

func outcomeNode(outcome projectevidence.Outcome, runID string) ladybug.Node {
	return ladybug.Node{Label: labelOutcome, ID: graphID(outcome.ProjectID, outcome.ID), Properties: map[string]string{
		"id": outcome.ID, "project_id": outcome.ProjectID, "claim_id": outcome.ClaimID, "action_id": outcome.ActionID, "run_id": runID, "outcome_ref": outcome.OutcomeRef, "outcome_kind": outcome.OutcomeKind, "status": outcome.Status, "verifier_ref": outcome.VerifierRef, "summary": outcome.Summary, "created_at": formatTime(outcome.CreatedAt),
	}}
}

func artifactNode(id string, link projectevidence.ArtifactLink, runID string) ladybug.Node {
	return ladybug.Node{Label: labelArtifact, ID: id, Properties: map[string]string{
		"id": id, "project_id": link.ProjectID, "claim_id": link.ClaimID, "run_id": runID, "artifact_ref": link.ArtifactRef, "artifact_kind": link.ArtifactKind,
	}}
}

func promotionNode(id string, link projectevidence.PromotionLink, runID string) ladybug.Node {
	return ladybug.Node{Label: labelPromotion, ID: id, Properties: map[string]string{
		"id": id, "project_id": link.ProjectID, "claim_id": link.ClaimID, "run_id": runID, "artifact_ref": link.ArtifactRef, "promotion_state": link.PromotionState, "source_ref": link.SourceRef, "verifier_ref": link.VerifierRef, "decision_ref": link.DecisionRef, "action_ref": link.ActionRef, "outcome_ref": link.OutcomeRef, "decided_at": formatTime(link.DecidedAt),
	}}
}

func relationship(relationshipType string, from ladybug.NodeRef, to ladybug.NodeRef, projectID string, claimID string, runID string) ladybug.Relationship {
	return ladybug.Relationship{Type: relationshipType, From: from, To: to, Properties: map[string]string{"project_id": projectID, "claim_id": claimID, "run_id": runID}}
}

func nodeRef(label string, id string) ladybug.NodeRef {
	return ladybug.NodeRef{Label: label, ID: id}
}

func refPtr(ref ladybug.NodeRef) *ladybug.NodeRef {
	return &ref
}

func nodeToClaim(node ladybug.Node) projectevidence.Claim {
	props := node.Properties
	return projectevidence.Claim{ID: props["id"], ProjectID: props["project_id"], RunID: props["run_id"], TraceID: props["trace_id"], ClaimRef: props["claim_ref"], Summary: props["summary"], Status: props["status"], CreatedAt: parseTime(props["created_at"]), UpdatedAt: parseTime(props["updated_at"])}
}

func nodeToEvidence(node ladybug.Node) projectevidence.Evidence {
	props := node.Properties
	return projectevidence.Evidence{ID: props["id"], ProjectID: props["project_id"], ClaimID: props["claim_id"], EvidenceRef: props["evidence_ref"], EvidenceKind: props["evidence_kind"], SourceRef: props["source_ref"], Summary: props["summary"], CreatedAt: parseTime(props["created_at"])}
}

func nodeToDecision(node ladybug.Node) projectevidence.Decision {
	props := node.Properties
	return projectevidence.Decision{ID: props["id"], ProjectID: props["project_id"], ClaimID: props["claim_id"], DecisionRef: props["decision_ref"], State: props["state"], VerifierRef: props["verifier_ref"], Rationale: props["rationale"], DecidedAt: parseTime(props["decided_at"])}
}

func nodeToAction(node ladybug.Node) projectevidence.Action {
	props := node.Properties
	return projectevidence.Action{ID: props["id"], ProjectID: props["project_id"], ClaimID: props["claim_id"], DecisionID: props["decision_id"], ActionRef: props["action_ref"], ActionKind: props["action_kind"], Summary: props["summary"], ChangedFiles: splitChangedFiles(props["changed_files"]), RunID: props["run_id"], CreatedAt: parseTime(props["created_at"])}
}

func nodeToOutcome(node ladybug.Node) projectevidence.Outcome {
	props := node.Properties
	return projectevidence.Outcome{ID: props["id"], ProjectID: props["project_id"], ClaimID: props["claim_id"], ActionID: props["action_id"], OutcomeRef: props["outcome_ref"], OutcomeKind: props["outcome_kind"], Status: props["status"], VerifierRef: props["verifier_ref"], Summary: props["summary"], CreatedAt: parseTime(props["created_at"])}
}

func nodeToArtifactLink(node ladybug.Node) projectevidence.ArtifactLink {
	props := node.Properties
	return projectevidence.ArtifactLink{ProjectID: props["project_id"], ClaimID: props["claim_id"], ArtifactRef: props["artifact_ref"], ArtifactKind: props["artifact_kind"], RunID: props["run_id"]}
}

func nodeToPromotionLink(node ladybug.Node) projectevidence.PromotionLink {
	props := node.Properties
	return projectevidence.PromotionLink{ProjectID: props["project_id"], ClaimID: props["claim_id"], RunID: props["run_id"], ArtifactRef: props["artifact_ref"], PromotionState: props["promotion_state"], SourceRef: props["source_ref"], VerifierRef: props["verifier_ref"], DecisionRef: props["decision_ref"], ActionRef: props["action_ref"], OutcomeRef: props["outcome_ref"], DecidedAt: parseTime(props["decided_at"])}
}

func artifactID(projectID string, claimID string, artifactRef string) string {
	return strings.Join([]string{projectID, claimID, artifactRef}, ":artifact:")
}

func promotionID(projectID string, claimID string, artifactRef string, state string, sourceRef string, actionRef string, outcomeRef string) string {
	return strings.Join([]string{projectID, claimID, artifactRef, state, sourceRef, actionRef, outcomeRef}, ":promotion:")
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

func splitChangedFiles(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}
