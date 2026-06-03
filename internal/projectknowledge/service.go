package projectknowledge

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

var ErrInvalidInput = errors.New("invalid input")

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)

type Service struct {
	store Store
	now   func() time.Time
	newID func(string) string
}

func New(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }, newID: newID}
}

func (svc *Service) CreateCandidate(ctx context.Context, input CreateCandidateInput) (KnowledgeRecord, error) {
	if svc.store == nil {
		return KnowledgeRecord{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRefIdentifier(input.ProjectID, "project_id")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	knowledgeRef, err := safeRefIdentifier(input.KnowledgeRef, "knowledge_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	claimID, err := safeRefIdentifier(input.ClaimID, "claim_id")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	claimRef, err := safeRefIdentifier(input.ClaimRef, "claim_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	assessmentID, err := safeOptionalRefIdentifier(input.ConfidenceAssessmentID, "confidence_assessment_id")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	band, err := safeOptionalBand(input.ConfidenceBand)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if input.ConfidenceScore < 0 || input.ConfidenceScore > 100 {
		return KnowledgeRecord{}, fmt.Errorf("%w: confidence_score is out of bounds", ErrInvalidInput)
	}
	summary, err := safeRequiredText(input.Summary, "summary", 500)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	guidance, err := safeRequiredText(input.ReuseGuidance, "reuse_guidance", 500)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	evidenceRefs, err := safeRefList(input.EvidenceRefs, "evidence_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	verifierRefs, err := safeRefList(input.VerifierRefs, "verifier_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	outcomeRefs, err := safeRefList(input.OutcomeRefs, "outcome_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	promotionRefs, err := safeRefList(input.PromotionRefs, "promotion_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	supersedesRef, err := safeOptionalRefIdentifier(input.SupersedesRef, "supersedes_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	now := svc.now()
	record := KnowledgeRecord{
		ID:                     svc.newID("knowledge"),
		ProjectID:              projectID,
		Scope:                  ScopeProject,
		KnowledgeRef:           knowledgeRef,
		ClaimID:                claimID,
		ClaimRef:               claimRef,
		ConfidenceAssessmentID: assessmentID,
		ConfidenceScore:        input.ConfidenceScore,
		ConfidenceBand:         band,
		State:                  StateCandidate,
		Summary:                summary,
		ReuseGuidance:          guidance,
		EvidenceRefs:           evidenceRefs,
		VerifierRefs:           verifierRefs,
		OutcomeRefs:            outcomeRefs,
		PromotionRefs:          promotionRefs,
		SupersedesRef:          supersedesRef,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if err := validateKnowledgeRecord(record); err != nil {
		return KnowledgeRecord{}, err
	}
	return svc.store.CreateKnowledge(ctx, record)
}

func (svc *Service) GetKnowledge(ctx context.Context, projectID string, knowledgeID string) (KnowledgeRecord, error) {
	if svc.store == nil {
		return KnowledgeRecord{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	return svc.store.GetKnowledge(ctx, projectID, knowledgeID)
}

func (svc *Service) ListKnowledge(ctx context.Context, projectID string, filter KnowledgeFilter) ([]KnowledgeRecord, error) {
	if svc.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter, err = validateFilter(filter)
	if err != nil {
		return nil, err
	}
	return svc.store.ListKnowledge(ctx, projectID, filter)
}

func (svc *Service) ListOrgKnowledge(ctx context.Context, orgRef string, filter KnowledgeFilter) ([]KnowledgeRecord, error) {
	if svc.store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	orgRef, err := safeDefaultOrgRef(orgRef, false)
	if err != nil {
		return nil, err
	}
	if filter.Scope != "" && filter.Scope != ScopeOrg {
		return nil, fmt.Errorf("%w: org knowledge scope must be org", ErrInvalidInput)
	}
	if filter.State != "" && filter.State != StateOrgPromoted {
		return nil, fmt.Errorf("%w: org knowledge state must be org_promoted", ErrInvalidInput)
	}
	filter.Scope = ScopeOrg
	filter.OrgRef = orgRef
	filter.State = StateOrgPromoted
	filter, err = validateFilter(filter)
	if err != nil {
		return nil, err
	}
	return svc.store.ListOrgKnowledge(ctx, orgRef, filter)
}

func (svc *Service) ValidateCandidate(ctx context.Context, input ValidateCandidateInput) (KnowledgeRecord, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateTransition(record.State, StateValidated); err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateProjectGate(record, input.Gate); err != nil {
		return KnowledgeRecord{}, err
	}
	decision, err := svc.newDecision(record, StateValidated, ScopeProject, input.DecisionRef, input.VerifierRef, input.Rationale, "", input.Gate.Confidence.Score)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record = applyProjectGate(record, input.Gate)
	record.State = StateValidated
	record.UpdatedAt = svc.now()
	if err := validateKnowledgeRecord(record); err != nil {
		return KnowledgeRecord{}, err
	}
	updated, err := svc.store.UpdateKnowledge(ctx, record)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := svc.store.CreateDecision(ctx, decision); err != nil {
		return KnowledgeRecord{}, err
	}
	return updated, nil
}

func (svc *Service) PromoteProject(ctx context.Context, input PromoteProjectInput) (KnowledgeRecord, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateTransition(record.State, StateProjectPromoted); err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateProjectGate(record, input.Gate); err != nil {
		return KnowledgeRecord{}, err
	}
	decision, err := svc.newDecision(record, StateProjectPromoted, ScopeProject, input.DecisionRef, input.VerifierRef, input.Rationale, "", input.Gate.Confidence.Score)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	now := svc.now()
	record = applyProjectGate(record, input.Gate)
	record.Scope = ScopeProject
	record.State = StateProjectPromoted
	record.PromotionRefs = uniqueRefs(append(record.PromotionRefs, decision.DecisionRef))
	record.UpdatedAt = now
	record.PromotedAt = now
	if err := validateKnowledgeRecord(record); err != nil {
		return KnowledgeRecord{}, err
	}
	updated, err := svc.store.UpdateKnowledge(ctx, record)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := svc.store.CreateDecision(ctx, decision); err != nil {
		return KnowledgeRecord{}, err
	}
	return updated, nil
}

func (svc *Service) SubmitOrgReview(ctx context.Context, input SubmitOrgReviewInput) (KnowledgeRecord, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateTransition(record.State, StateOrgReview); err != nil {
		return KnowledgeRecord{}, err
	}
	orgRef, err := safeDefaultOrgRef(input.OrgRef, false)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := safeActorRef(input.DecidedBy, "decided_by"); err != nil {
		return KnowledgeRecord{}, err
	}
	decision, err := svc.newDecision(record, StateOrgReview, ScopeOrg, input.DecisionRef, input.VerifierRef, input.Rationale, input.DecidedBy, record.ConfidenceScore)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record.Scope = ScopeOrg
	record.OrgRef = orgRef
	record.State = StateOrgReview
	record.UpdatedAt = svc.now()
	if err := validateKnowledgeRecord(record); err != nil {
		return KnowledgeRecord{}, err
	}
	updated, err := svc.store.UpdateKnowledge(ctx, record)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := svc.store.CreateDecision(ctx, decision); err != nil {
		return KnowledgeRecord{}, err
	}
	return updated, nil
}

func (svc *Service) PromoteOrg(ctx context.Context, input PromoteOrgInput) (KnowledgeRecord, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	decisions, err := svc.store.ListDecisions(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateOrgGate(record, decisions, input.Gate); err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateTransition(record.State, StateOrgPromoted); err != nil {
		return KnowledgeRecord{}, err
	}
	orgRef, err := safeDefaultOrgRef(record.OrgRef, true)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	decision, err := svc.newDecision(record, StateOrgPromoted, ScopeOrg, input.Gate.DecisionRef, input.Gate.VerifierRef, input.Gate.Rationale, input.Gate.DecidedBy, input.Gate.Confidence.Score)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	now := svc.now()
	record = applyProjectGate(record, ProjectGateInput{Claim: input.Gate.Claim, Confidence: input.Gate.Confidence})
	record.Scope = ScopeOrg
	record.OrgRef = orgRef
	record.State = StateOrgPromoted
	record.PromotionRefs = uniqueRefs(append(record.PromotionRefs, decision.DecisionRef))
	record.UpdatedAt = now
	record.PromotedAt = now
	if err := validateKnowledgeRecord(record); err != nil {
		return KnowledgeRecord{}, err
	}
	updated, err := svc.store.UpdateKnowledge(ctx, record)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := svc.store.CreateDecision(ctx, decision); err != nil {
		return KnowledgeRecord{}, err
	}
	return updated, nil
}

func (svc *Service) Reject(ctx context.Context, input RejectInput) (KnowledgeRecord, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateTransition(record.State, StateRejected); err != nil {
		return KnowledgeRecord{}, err
	}
	decision, err := svc.newDecision(record, StateRejected, record.Scope, input.DecisionRef, input.VerifierRef, input.Rationale, input.DecidedBy, record.ConfidenceScore)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record.State = StateRejected
	record.UpdatedAt = svc.now()
	updated, err := svc.store.UpdateKnowledge(ctx, record)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := svc.store.CreateDecision(ctx, decision); err != nil {
		return KnowledgeRecord{}, err
	}
	return updated, nil
}

func (svc *Service) Supersede(ctx context.Context, input SupersedeInput) (KnowledgeRecord, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if err := validateTransition(record.State, StateSuperseded); err != nil {
		return KnowledgeRecord{}, err
	}
	supersededByRef, err := safeRefIdentifier(input.SupersededByRef, "superseded_by_ref")
	if err != nil {
		return KnowledgeRecord{}, err
	}
	decision, err := svc.newDecision(record, StateSuperseded, record.Scope, input.DecisionRef, input.VerifierRef, input.Rationale, input.DecidedBy, record.ConfidenceScore)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	record.State = StateSuperseded
	record.SupersededByRef = supersededByRef
	record.UpdatedAt = svc.now()
	if err := validateKnowledgeRecord(record); err != nil {
		return KnowledgeRecord{}, err
	}
	updated, err := svc.store.UpdateKnowledge(ctx, record)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	if _, err := svc.store.CreateDecision(ctx, decision); err != nil {
		return KnowledgeRecord{}, err
	}
	return updated, nil
}

func (svc *Service) RecordReuseEvent(ctx context.Context, input RecordReuseEventInput) (KnowledgeReuseEvent, error) {
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	if _, err := svc.store.GetKnowledge(ctx, projectID, knowledgeID); err != nil {
		return KnowledgeReuseEvent{}, err
	}
	agentRunID, err := safeOptionalRefIdentifier(input.AgentRunID, "agent_run_id")
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	traceID, err := safeOptionalRefIdentifier(input.TraceID, "trace_id")
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	reuseRef, err := safeRefIdentifier(input.ReuseRef, "reuse_ref")
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	revalidationRef, err := safeOptionalRefIdentifier(input.RevalidationRef, "revalidation_ref")
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	if input.Revalidated && revalidationRef == "" {
		return KnowledgeReuseEvent{}, fmt.Errorf("%w: revalidation_ref is required when revalidated", ErrInvalidInput)
	}
	outcome, err := safeReuseOutcome(input.Outcome)
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	summary, err := safeOptionalText(input.Summary, "summary", 500)
	if err != nil {
		return KnowledgeReuseEvent{}, err
	}
	event := KnowledgeReuseEvent{
		ID:              svc.newID("reuse"),
		ProjectID:       projectID,
		KnowledgeID:     knowledgeID,
		AgentRunID:      agentRunID,
		TraceID:         traceID,
		ReuseRef:        reuseRef,
		Revalidated:     input.Revalidated,
		RevalidationRef: revalidationRef,
		Outcome:         outcome,
		Summary:         summary,
		CreatedAt:       svc.now(),
	}
	return svc.store.CreateReuseEvent(ctx, event)
}

func (svc *Service) newDecision(record KnowledgeRecord, toState string, scope string, decisionRef string, verifierRef string, rationale string, decidedBy string, score int) (PromotionDecision, error) {
	if _, err := safeState(toState); err != nil {
		return PromotionDecision{}, err
	}
	scope, err := safeScope(scope)
	if err != nil {
		return PromotionDecision{}, err
	}
	decisionRef, err = safeRefIdentifier(decisionRef, "decision_ref")
	if err != nil {
		return PromotionDecision{}, err
	}
	verifierRef, err = safeRefIdentifier(verifierRef, "verifier_ref")
	if err != nil {
		return PromotionDecision{}, err
	}
	rationale, err = safeRequiredText(rationale, "rationale", 500)
	if err != nil {
		return PromotionDecision{}, err
	}
	decidedBy, err = safeOptionalActorRef(decidedBy, "decided_by")
	if err != nil {
		return PromotionDecision{}, err
	}
	if score < 0 || score > 100 {
		return PromotionDecision{}, fmt.Errorf("%w: confidence score is out of bounds", ErrInvalidInput)
	}
	return PromotionDecision{
		ID:              svc.newID("knowledge_decision"),
		ProjectID:       record.ProjectID,
		KnowledgeID:     record.ID,
		FromState:       record.State,
		ToState:         toState,
		Scope:           scope,
		DecisionRef:     decisionRef,
		VerifierRef:     verifierRef,
		ConfidenceScore: score,
		Rationale:       rationale,
		DecidedBy:       decidedBy,
		DecidedAt:       svc.now(),
	}, nil
}

func validateProjectGate(record KnowledgeRecord, input ProjectGateInput) error {
	if err := validateClaimRecordMetadata(input.Claim); err != nil {
		return err
	}
	if err := validateAssessmentMetadata(input.Confidence); err != nil {
		return err
	}
	claim := input.Claim.Claim
	confidence := input.Confidence
	if claim.ProjectID != record.ProjectID || confidence.ProjectID != record.ProjectID {
		return fmt.Errorf("%w: project mismatch", ErrInvalidInput)
	}
	if claim.ID != record.ClaimID || confidence.ClaimID != record.ClaimID {
		return fmt.Errorf("%w: claim mismatch", ErrInvalidInput)
	}
	if confidence.ClaimRef != "" && confidence.ClaimRef != claim.ClaimRef {
		return fmt.Errorf("%w: claim_ref mismatch", ErrInvalidInput)
	}
	if len(input.Claim.Evidence) == 0 {
		return fmt.Errorf("%w: evidence is required", ErrInvalidInput)
	}
	if !hasDecisionState(input.Claim, projectevidence.DecisionStateValidated) && !hasDecisionState(input.Claim, projectevidence.DecisionStatePromoted) {
		return fmt.Errorf("%w: validated or promoted decision is required", ErrInvalidInput)
	}
	if hasDecisionState(input.Claim, projectevidence.DecisionStateRejected) {
		return fmt.Errorf("%w: rejected decisions block project promotion", ErrInvalidInput)
	}
	if len(input.Claim.Actions) == 0 {
		return fmt.Errorf("%w: action is required", ErrInvalidInput)
	}
	if !hasPassedOutcome(input.Claim) {
		return fmt.Errorf("%w: passed outcome is required", ErrInvalidInput)
	}
	if hasUnresolvedFailedOutcome(input.Claim) {
		return fmt.Errorf("%w: latest failed outcome is not superseded by a later pass", ErrInvalidInput)
	}
	if confidence.Score < 85 {
		return fmt.Errorf("%w: confidence score is below project gate", ErrInvalidInput)
	}
	if confidence.Recommendation != projectconfidence.RecommendationPromote {
		return fmt.Errorf("%w: confidence recommendation must be promote", ErrInvalidInput)
	}
	if !hasVerifierRef(input.Claim) {
		return fmt.Errorf("%w: verifier_ref is required", ErrInvalidInput)
	}
	if !hasPromotedLinkToPassedOutcome(input.Claim) {
		return fmt.Errorf("%w: promoted link to passed outcome is required", ErrInvalidInput)
	}
	return nil
}

func validateOrgGate(record KnowledgeRecord, decisions []PromotionDecision, input OrgGateInput) error {
	if record.State != StateOrgReview {
		return fmt.Errorf("%w: org promotion requires org_review state", ErrInvalidInput)
	}
	if !hasTransition(decisions, StateValidated, StateProjectPromoted) && !hasTransition(decisions, StateCandidate, StateProjectPromoted) {
		return fmt.Errorf("%w: org promotion requires prior project promotion", ErrInvalidInput)
	}
	if !hasTransitionTo(decisions, StateOrgReview) {
		return fmt.Errorf("%w: explicit org review decision is required", ErrInvalidInput)
	}
	scope, err := safeScope(input.Scope)
	if err != nil {
		return err
	}
	if scope != ScopeOrg {
		return fmt.Errorf("%w: org scope must be explicit", ErrInvalidInput)
	}
	inputOrgRef, err := safeDefaultOrgRef(input.OrgRef, true)
	if err != nil {
		return err
	}
	recordOrgRef, err := safeDefaultOrgRef(record.OrgRef, true)
	if err != nil {
		return err
	}
	if inputOrgRef != recordOrgRef {
		return fmt.Errorf("%w: org_ref must match org review", ErrInvalidInput)
	}
	if record.Scope != ScopeOrg {
		return fmt.Errorf("%w: org promotion requires org scope", ErrInvalidInput)
	}
	if recordOrgRef != DefaultOrgRef {
		return fmt.Errorf("%w: org_ref is not supported", ErrInvalidInput)
	}
	if _, err := safeRefIdentifier(input.DecisionRef, "decision_ref"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(input.VerifierRef, "verifier_ref"); err != nil {
		return err
	}
	if _, err := safeRequiredText(input.Rationale, "rationale", 500); err != nil {
		return err
	}
	if _, err := safeActorRef(input.DecidedBy, "decided_by"); err != nil {
		return err
	}
	if err := validateProjectGate(record, ProjectGateInput{Claim: input.Claim, Confidence: input.Confidence}); err != nil {
		return err
	}
	if input.Confidence.Score < 90 {
		return fmt.Errorf("%w: confidence score is below org gate", ErrInvalidInput)
	}
	if input.Confidence.Band != projectconfidence.ScoreBandHigh {
		return fmt.Errorf("%w: confidence band must be high", ErrInvalidInput)
	}
	if len(evidenceKinds(input.Claim)) < 2 {
		return fmt.Errorf("%w: at least two evidence kinds are required", ErrInvalidInput)
	}
	if hasDecisionState(input.Claim, projectevidence.DecisionStateRejected) {
		return fmt.Errorf("%w: rejected decisions block org promotion", ErrInvalidInput)
	}
	if input.ClaimCheckActionable != 0 {
		return fmt.Errorf("%w: actionable claim-check findings block org promotion", ErrInvalidInput)
	}
	return nil
}

func applyProjectGate(record KnowledgeRecord, input ProjectGateInput) KnowledgeRecord {
	record.ClaimRef = input.Claim.Claim.ClaimRef
	record.ConfidenceAssessmentID = input.Confidence.ID
	record.ConfidenceScore = input.Confidence.Score
	record.ConfidenceBand = input.Confidence.Band
	record.EvidenceRefs = uniqueRefs(append(record.EvidenceRefs, evidenceRefs(input.Claim)...))
	record.VerifierRefs = uniqueRefs(append(record.VerifierRefs, verifierRefs(input.Claim)...))
	record.OutcomeRefs = uniqueRefs(append(record.OutcomeRefs, passedOutcomeRefs(input.Claim)...))
	return record
}

func validateKnowledgeRecord(record KnowledgeRecord) error {
	if _, err := safeRefIdentifier(record.ID, "knowledge_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(record.ProjectID, "project_id"); err != nil {
		return err
	}
	if _, err := safeScope(record.Scope); err != nil {
		return err
	}
	if record.Scope == ScopeOrg {
		if _, err := safeOrgRef(record.OrgRef); err != nil {
			return err
		}
	}
	if _, err := safeRefIdentifier(record.KnowledgeRef, "knowledge_ref"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(record.ClaimID, "claim_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(record.ClaimRef, "claim_ref"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(record.ConfidenceAssessmentID, "confidence_assessment_id"); err != nil {
		return err
	}
	if record.ConfidenceScore < 0 || record.ConfidenceScore > 100 {
		return fmt.Errorf("%w: confidence_score is out of bounds", ErrInvalidInput)
	}
	if _, err := safeOptionalBand(record.ConfidenceBand); err != nil {
		return err
	}
	if _, err := safeState(record.State); err != nil {
		return err
	}
	if _, err := safeRequiredText(record.Summary, "summary", 500); err != nil {
		return err
	}
	if _, err := safeRequiredText(record.ReuseGuidance, "reuse_guidance", 500); err != nil {
		return err
	}
	if _, err := safeRefList(record.EvidenceRefs, "evidence_ref"); err != nil {
		return err
	}
	if _, err := safeRefList(record.VerifierRefs, "verifier_ref"); err != nil {
		return err
	}
	if _, err := safeRefList(record.OutcomeRefs, "outcome_ref"); err != nil {
		return err
	}
	if _, err := safeRefList(record.PromotionRefs, "promotion_ref"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(record.SupersedesRef, "supersedes_ref"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(record.SupersededByRef, "superseded_by_ref"); err != nil {
		return err
	}
	return nil
}

func validateClaimRecordMetadata(record projectevidence.ClaimRecord) error {
	if _, err := safeRefIdentifier(record.Claim.ID, "claim_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(record.Claim.ProjectID, "project_id"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(record.Claim.RunID, "run_id"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(record.Claim.TraceID, "trace_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(record.Claim.ClaimRef, "claim_ref"); err != nil {
		return err
	}
	if _, err := safeOptionalText(record.Claim.Summary, "claim_summary", 500); err != nil {
		return err
	}
	if record.Claim.Status != "" {
		if _, err := safeClaimStatus(record.Claim.Status); err != nil {
			return err
		}
	}
	for _, evidence := range record.Evidence {
		if evidence.ProjectID != "" && evidence.ProjectID != record.Claim.ProjectID {
			return fmt.Errorf("%w: evidence project mismatch", ErrInvalidInput)
		}
		if evidence.ClaimID != "" && evidence.ClaimID != record.Claim.ID {
			return fmt.Errorf("%w: evidence claim mismatch", ErrInvalidInput)
		}
		if _, err := safeRefIdentifier(evidence.ID, "evidence_id"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(evidence.EvidenceRef, "evidence_ref"); err != nil {
			return err
		}
		if _, err := safeEvidenceKind(evidence.EvidenceKind); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(evidence.SourceRef, "source_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalText(evidence.Summary, "evidence_summary", 500); err != nil {
			return err
		}
	}
	for _, decision := range record.Decisions {
		if _, err := safeRefIdentifier(decision.ID, "decision_id"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(decision.DecisionRef, "decision_ref"); err != nil {
			return err
		}
		if _, err := safeDecisionState(decision.State); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(decision.VerifierRef, "verifier_ref"); err != nil {
			return err
		}
		if _, err := safeRequiredText(decision.Rationale, "decision_rationale", 500); err != nil {
			return err
		}
	}
	for _, action := range record.Actions {
		if _, err := safeRefIdentifier(action.ID, "action_id"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(action.DecisionID, "action_decision_id"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(action.ActionRef, "action_ref"); err != nil {
			return err
		}
		if _, err := safeActionKind(action.ActionKind); err != nil {
			return err
		}
		if _, err := safeOptionalText(action.Summary, "action_summary", 500); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(action.RunID, "action_run_id"); err != nil {
			return err
		}
		if _, err := safeRefList(action.ChangedFiles, "changed_file"); err != nil {
			return err
		}
	}
	for _, outcome := range record.Outcomes {
		if _, err := safeRefIdentifier(outcome.ID, "outcome_id"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(outcome.ActionID, "outcome_action_id"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(outcome.OutcomeRef, "outcome_ref"); err != nil {
			return err
		}
		if _, err := safeOutcomeKind(outcome.OutcomeKind); err != nil {
			return err
		}
		if _, err := safeOutcomeStatus(outcome.Status); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(outcome.VerifierRef, "outcome_verifier_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalText(outcome.Summary, "outcome_summary", 500); err != nil {
			return err
		}
	}
	for _, link := range record.PromotionLinks {
		if _, err := safeRefIdentifier(link.ArtifactRef, "promotion_artifact_ref"); err != nil {
			return err
		}
		if _, err := safePromotionState(link.PromotionState); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(link.SourceRef, "promotion_source_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(link.VerifierRef, "promotion_verifier_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(link.DecisionRef, "promotion_decision_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(link.ActionRef, "promotion_action_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(link.OutcomeRef, "promotion_outcome_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(link.RunID, "promotion_run_id"); err != nil {
			return err
		}
	}
	return nil
}

func validateAssessmentMetadata(assessment projectconfidence.ConfidenceAssessment) error {
	if _, err := safeRefIdentifier(assessment.ID, "confidence_assessment_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(assessment.ProjectID, "confidence_project_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(assessment.ClaimID, "confidence_claim_id"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(assessment.ClaimRef, "confidence_claim_ref"); err != nil {
		return err
	}
	if assessment.Score < 0 || assessment.Score > 100 {
		return fmt.Errorf("%w: confidence score is out of bounds", ErrInvalidInput)
	}
	if _, err := safeBand(assessment.Band); err != nil {
		return err
	}
	if _, err := safeRecommendation(assessment.Recommendation); err != nil {
		return err
	}
	for _, kind := range assessment.Inputs.EvidenceKinds {
		if _, err := safeEvidenceKind(kind); err != nil {
			return err
		}
	}
	return nil
}

func validateTransition(from string, to string) error {
	if _, err := safeState(from); err != nil {
		return err
	}
	if _, err := safeState(to); err != nil {
		return err
	}
	allowed := false
	switch to {
	case StateValidated:
		allowed = from == StateCandidate
	case StateProjectPromoted:
		allowed = from == StateValidated
	case StateOrgReview:
		allowed = from == StateProjectPromoted
	case StateOrgPromoted:
		allowed = from == StateOrgReview
	case StateRejected:
		allowed = from == StateCandidate || from == StateValidated || from == StateProjectPromoted || from == StateOrgReview
	case StateSuperseded:
		allowed = from == StateProjectPromoted || from == StateOrgPromoted
	}
	if !allowed {
		return fmt.Errorf("%w: invalid transition %s -> %s", ErrInvalidInput, from, to)
	}
	return nil
}

func validateFilter(filter KnowledgeFilter) (KnowledgeFilter, error) {
	var err error
	filter.Scope, err = safeOptionalScope(filter.Scope)
	if err != nil {
		return KnowledgeFilter{}, err
	}
	filter.OrgRef, err = safeOptionalRefIdentifier(filter.OrgRef, "org_ref")
	if err != nil {
		return KnowledgeFilter{}, err
	}
	filter.State, err = safeOptionalState(filter.State)
	if err != nil {
		return KnowledgeFilter{}, err
	}
	filter.ClaimID, err = safeOptionalRefIdentifier(filter.ClaimID, "claim_id")
	if err != nil {
		return KnowledgeFilter{}, err
	}
	filter.KnowledgeRef, err = safeOptionalRefIdentifier(filter.KnowledgeRef, "knowledge_ref")
	if err != nil {
		return KnowledgeFilter{}, err
	}
	filter.ConfidenceBand, err = safeOptionalBand(filter.ConfidenceBand)
	if err != nil {
		return KnowledgeFilter{}, err
	}
	filter.PageToken, err = safeOptionalRefIdentifier(filter.PageToken, "page_token")
	if err != nil {
		return KnowledgeFilter{}, err
	}
	if filter.MinConfidence != nil && (*filter.MinConfidence < 0 || *filter.MinConfidence > 100) {
		return KnowledgeFilter{}, fmt.Errorf("%w: min_confidence is out of bounds", ErrInvalidInput)
	}
	if filter.MaxConfidence != nil && (*filter.MaxConfidence < 0 || *filter.MaxConfidence > 100) {
		return KnowledgeFilter{}, fmt.Errorf("%w: max_confidence is out of bounds", ErrInvalidInput)
	}
	if filter.MinConfidence != nil && filter.MaxConfidence != nil && *filter.MinConfidence > *filter.MaxConfidence {
		return KnowledgeFilter{}, fmt.Errorf("%w: min_confidence exceeds max_confidence", ErrInvalidInput)
	}
	if filter.PageSize < 0 || filter.PageSize > 100 {
		return KnowledgeFilter{}, fmt.Errorf("%w: page_size is out of bounds", ErrInvalidInput)
	}
	return filter, nil
}

func hasTransition(decisions []PromotionDecision, from string, to string) bool {
	for _, decision := range decisions {
		if decision.FromState == from && decision.ToState == to {
			return true
		}
	}
	return false
}

func hasTransitionTo(decisions []PromotionDecision, to string) bool {
	for _, decision := range decisions {
		if decision.ToState == to && decision.Scope == ScopeOrg && decision.DecidedBy != "" {
			return true
		}
	}
	return false
}

func hasDecisionState(record projectevidence.ClaimRecord, state string) bool {
	for _, decision := range record.Decisions {
		if decision.State == state {
			return true
		}
	}
	return false
}

func hasPassedOutcome(record projectevidence.ClaimRecord) bool {
	for _, outcome := range record.Outcomes {
		if outcome.Status == projectevidence.OutcomeStatusPassed {
			return true
		}
	}
	return false
}

func hasUnresolvedFailedOutcome(record projectevidence.ClaimRecord) bool {
	latestIndex := -1
	latestTime := time.Time{}
	latestStatus := ""
	for i, outcome := range record.Outcomes {
		if outcome.Status != projectevidence.OutcomeStatusPassed && outcome.Status != projectevidence.OutcomeStatusFailed {
			continue
		}
		if latestIndex == -1 || outcome.CreatedAt.After(latestTime) || outcome.CreatedAt.Equal(latestTime) && i > latestIndex {
			latestIndex = i
			latestTime = outcome.CreatedAt
			latestStatus = outcome.Status
		}
	}
	return latestStatus == projectevidence.OutcomeStatusFailed
}

func hasVerifierRef(record projectevidence.ClaimRecord) bool {
	for _, decision := range record.Decisions {
		if decision.VerifierRef != "" {
			return true
		}
	}
	for _, outcome := range record.Outcomes {
		if outcome.VerifierRef != "" {
			return true
		}
	}
	for _, link := range record.PromotionLinks {
		if link.VerifierRef != "" {
			return true
		}
	}
	return false
}

func hasPromotedLinkToPassedOutcome(record projectevidence.ClaimRecord) bool {
	passed := map[string]struct{}{}
	for _, outcome := range record.Outcomes {
		if outcome.Status == projectevidence.OutcomeStatusPassed {
			passed[outcome.OutcomeRef] = struct{}{}
		}
	}
	for _, link := range record.PromotionLinks {
		if link.ProjectID != "" && link.ProjectID != record.Claim.ProjectID {
			continue
		}
		if link.ClaimID != "" && link.ClaimID != record.Claim.ID {
			continue
		}
		if link.PromotionState != projectevidence.PromotionStatePromoted || link.OutcomeRef == "" || link.DecisionRef == "" || link.VerifierRef == "" {
			continue
		}
		if _, ok := passed[link.OutcomeRef]; ok {
			return true
		}
	}
	return false
}

func evidenceKinds(record projectevidence.ClaimRecord) []string {
	seen := map[string]struct{}{}
	for _, evidence := range record.Evidence {
		seen[evidence.EvidenceKind] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for kind := range seen {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func evidenceRefs(record projectevidence.ClaimRecord) []string {
	refs := make([]string, 0, len(record.Evidence))
	for _, evidence := range record.Evidence {
		refs = append(refs, evidence.EvidenceRef)
	}
	return refs
}

func verifierRefs(record projectevidence.ClaimRecord) []string {
	refs := make([]string, 0)
	for _, decision := range record.Decisions {
		refs = append(refs, decision.VerifierRef)
	}
	for _, outcome := range record.Outcomes {
		refs = append(refs, outcome.VerifierRef)
	}
	for _, link := range record.PromotionLinks {
		refs = append(refs, link.VerifierRef)
	}
	return refs
}

func passedOutcomeRefs(record projectevidence.ClaimRecord) []string {
	refs := make([]string, 0)
	for _, outcome := range record.Outcomes {
		if outcome.Status == projectevidence.OutcomeStatusPassed {
			refs = append(refs, outcome.OutcomeRef)
		}
	}
	return refs
}

func uniqueRefs(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func safeProjectKnowledgeIDs(projectID string, knowledgeID string) (string, string, error) {
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return "", "", err
	}
	knowledgeID, err = safeRefIdentifier(knowledgeID, "knowledge_id")
	if err != nil {
		return "", "", err
	}
	return projectID, knowledgeID, nil
}

func safeRequiredText(value string, field string, maxLength int) (string, error) {
	value, err := safeOptionalText(value, field, maxLength)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	return value, nil
}

func safeOptionalText(value string, field string, maxLength int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > maxLength || containsProhibitedData(value) || looksLikeSourceDump(value) || containsURL(value) || containsRootMarker(value) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return value, nil
}

func safeIdentifier(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	if len(value) > 200 || containsProhibitedMarker(value) || emailPattern.MatchString(value) || phonePattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return value, nil
}

func safeRefIdentifier(value string, field string) (string, error) {
	value, err := safeIdentifier(value, field)
	if err != nil {
		return "", err
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "..") || filepath.IsAbs(normalized) || containsProhibitedMarker(normalized) || containsRootMarker(normalized) || containsURL(normalized) || strings.Contains(normalized, "//") {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return normalized, nil
}

func safeOptionalRefIdentifier(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeRefIdentifier(value, field)
}

func safeRefList(values []string, field string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value, err := safeOptionalRefIdentifier(value, field)
		if err != nil {
			return nil, err
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return uniqueRefs(out), nil
}

func safeActorRef(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidInput, field)
	}
	if len(value) > 120 {
		return "", fmt.Errorf("%w: %s is unsafe", ErrInvalidInput, field)
	}
	return safeRefIdentifier(value, field)
}

func safeOptionalActorRef(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeActorRef(value, field)
}

func safeScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	switch scope {
	case ScopeProject, ScopeOrg:
		return scope, nil
	default:
		return "", fmt.Errorf("%w: scope is invalid", ErrInvalidInput)
	}
}

func safeOptionalScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "", nil
	}
	return safeScope(scope)
}

func safeOrgRef(orgRef string) (string, error) {
	orgRef = strings.TrimSpace(orgRef)
	if orgRef == "" {
		orgRef = DefaultOrgRef
	}
	return safeRefIdentifier(orgRef, "org_ref")
}

func safeDefaultOrgRef(orgRef string, required bool) (string, error) {
	if required && strings.TrimSpace(orgRef) == "" {
		return "", fmt.Errorf("%w: org_ref is required", ErrInvalidInput)
	}
	orgRef, err := safeOrgRef(orgRef)
	if err != nil {
		return "", err
	}
	if orgRef != DefaultOrgRef {
		return "", fmt.Errorf("%w: org_ref is not supported", ErrInvalidInput)
	}
	return orgRef, nil
}

func safeState(state string) (string, error) {
	state = strings.TrimSpace(state)
	switch state {
	case StateCandidate, StateValidated, StateProjectPromoted, StateOrgReview, StateOrgPromoted, StateRejected, StateSuperseded:
		return state, nil
	default:
		return "", fmt.Errorf("%w: state is invalid", ErrInvalidInput)
	}
}

func safeOptionalState(state string) (string, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", nil
	}
	return safeState(state)
}

func safeReuseOutcome(outcome string) (string, error) {
	outcome = strings.TrimSpace(outcome)
	switch outcome {
	case ReuseOutcomeUsed, ReuseOutcomeSkipped, ReuseOutcomeStale, ReuseOutcomeContradicted:
		return outcome, nil
	default:
		return "", fmt.Errorf("%w: reuse outcome is invalid", ErrInvalidInput)
	}
}

func safeBand(band string) (string, error) {
	band = strings.TrimSpace(band)
	switch band {
	case projectconfidence.ScoreBandHigh, projectconfidence.ScoreBandMedium, projectconfidence.ScoreBandLow, projectconfidence.ScoreBandUnknown:
		return band, nil
	default:
		return "", fmt.Errorf("%w: confidence band is invalid", ErrInvalidInput)
	}
}

func safeOptionalBand(band string) (string, error) {
	band = strings.TrimSpace(band)
	if band == "" {
		return "", nil
	}
	return safeBand(band)
}

func safeRecommendation(recommendation string) (string, error) {
	recommendation = strings.TrimSpace(recommendation)
	switch recommendation {
	case projectconfidence.RecommendationPromote, projectconfidence.RecommendationVerify, projectconfidence.RecommendationReview, projectconfidence.RecommendationReject, projectconfidence.RecommendationInsufficientEvidence:
		return recommendation, nil
	default:
		return "", fmt.Errorf("%w: recommendation is invalid", ErrInvalidInput)
	}
}

func safeEvidenceKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case projectevidence.EvidenceKindContextPack, projectevidence.EvidenceKindFile, projectevidence.EvidenceKindChunk, projectevidence.EvidenceKindSymbol, projectevidence.EvidenceKindVerifier, projectevidence.EvidenceKindClaimCheck, projectevidence.EvidenceKindArtifact, projectevidence.EvidenceKindOther:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: evidence_kind is invalid", ErrInvalidInput)
	}
}

func safeClaimStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	switch status {
	case projectevidence.ClaimStatusCandidate, projectevidence.ClaimStatusValidated, projectevidence.ClaimStatusPromoted, projectevidence.ClaimStatusRejected:
		return status, nil
	default:
		return "", fmt.Errorf("%w: claim status is invalid", ErrInvalidInput)
	}
}

func safeDecisionState(state string) (string, error) {
	state = strings.TrimSpace(state)
	switch state {
	case projectevidence.DecisionStateValidated, projectevidence.DecisionStatePromoted, projectevidence.DecisionStateRejected:
		return state, nil
	default:
		return "", fmt.Errorf("%w: decision state is invalid", ErrInvalidInput)
	}
}

func safeActionKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case projectevidence.ActionKindCodeChange, projectevidence.ActionKindDocChange, projectevidence.ActionKindVerifierRun, projectevidence.ActionKindConfigChange, projectevidence.ActionKindReviewComment, projectevidence.ActionKindOther:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: action_kind is invalid", ErrInvalidInput)
	}
}

func safeOutcomeKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case projectevidence.OutcomeKindTest, projectevidence.OutcomeKindBuild, projectevidence.OutcomeKindClaimCheck, projectevidence.OutcomeKindManualReview, projectevidence.OutcomeKindPromotion, projectevidence.OutcomeKindFailure, projectevidence.OutcomeKindOther:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: outcome_kind is invalid", ErrInvalidInput)
	}
}

func safeOutcomeStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	switch status {
	case projectevidence.OutcomeStatusPassed, projectevidence.OutcomeStatusFailed, projectevidence.OutcomeStatusBlocked, projectevidence.OutcomeStatusUnknown:
		return status, nil
	default:
		return "", fmt.Errorf("%w: outcome status is invalid", ErrInvalidInput)
	}
}

func safePromotionState(state string) (string, error) {
	state = strings.TrimSpace(state)
	switch state {
	case projectevidence.PromotionStateCandidate, projectevidence.PromotionStateValidated, projectevidence.PromotionStatePromoted, projectevidence.PromotionStateRejected:
		return state, nil
	default:
		return "", fmt.Errorf("%w: promotion state is invalid", ErrInvalidInput)
	}
}

func containsProhibitedData(value string) bool {
	return containsProhibitedMarker(value) || emailPattern.MatchString(value) || phonePattern.MatchString(value)
}

func containsProhibitedMarker(value string) bool {
	normalized := strings.ToLower(value)
	disallowed := []string{
		"match (",
		"select ",
		"insert into",
		"update ",
		"delete from",
		"pragma ",
		"drop ",
		"create (",
		"return n",
		"raw prompt",
		"raw completion",
		"raw source",
		"source dump",
		"raw stderr",
		"stderr:",
		"provider payload",
		"authorization:",
		"bearer ",
		"begin private key",
		"api_key=",
		"token=",
		"secret=",
		"password=",
		"credential=",
	}
	for _, marker := range disallowed {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func looksLikeSourceDump(value string) bool {
	normalized := strings.ToLower(value)
	return strings.Count(value, "\n") > 4 || strings.Contains(normalized, "package main") || strings.Contains(normalized, "func ") || strings.Contains(normalized, "-----begin") || strings.Contains(normalized, "traceback (most recent call last)") || strings.Contains(normalized, "```")
}

func containsRootMarker(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	return strings.Contains(normalized, "/home/") || strings.Contains(normalized, "/users/") || strings.Contains(normalized, "wsl.localhost/") || strings.Contains(normalized, "c:/")
}

func containsURL(value string) bool {
	if strings.Contains(strings.ToLower(value), "www.") {
		return true
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return true
	}
	return strings.Contains(value, "://")
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate id: %w", err))
	}
	const alphabet = "abcdefghijklmnop"
	var encoded [32]byte
	for i, value := range b {
		encoded[i*2] = alphabet[value>>4]
		encoded[i*2+1] = alphabet[value&0x0f]
	}
	return prefix + "_" + string(encoded[:])
}
