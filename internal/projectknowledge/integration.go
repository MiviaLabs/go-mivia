package projectknowledge

import (
	"context"
	"fmt"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

type EvidenceClaimReader interface {
	GetClaim(ctx context.Context, projectID string, claimID string) (projectevidence.ClaimRecord, error)
}

type ConfidenceAssessmentReader interface {
	GetAssessment(ctx context.Context, projectID string, claimID string) (projectconfidence.ConfidenceAssessment, error)
}

type PromotionInputAdapter struct {
	evidence   EvidenceClaimReader
	confidence ConfidenceAssessmentReader
}

type ProjectGateOptions struct {
	ProjectID string
	ClaimID   string
}

type OrgGateOptions struct {
	ProjectID            string
	ClaimID              string
	ClaimCheckActionable int
	Scope                string
	OrgRef               string
	DecisionRef          string
	VerifierRef          string
	Rationale            string
	DecidedBy            string
}

type ValidateCandidateWithInputsInput struct {
	ProjectID   string
	KnowledgeID string
	DecisionRef string
	VerifierRef string
	Rationale   string
}

type PromoteProjectWithInputsInput struct {
	ProjectID   string
	KnowledgeID string
	DecisionRef string
	VerifierRef string
	Rationale   string
}

type PromoteOrgWithInputsInput struct {
	ProjectID            string
	KnowledgeID          string
	ClaimCheckActionable int
	Scope                string
	OrgRef               string
	DecisionRef          string
	VerifierRef          string
	Rationale            string
	DecidedBy            string
}

func NewPromotionInputAdapter(evidence EvidenceClaimReader, confidence ConfidenceAssessmentReader) *PromotionInputAdapter {
	return &PromotionInputAdapter{evidence: evidence, confidence: confidence}
}

func (adapter *PromotionInputAdapter) BuildProjectGate(ctx context.Context, options ProjectGateOptions) (ProjectGateInput, error) {
	claim, confidence, err := adapter.build(ctx, options.ProjectID, options.ClaimID)
	if err != nil {
		return ProjectGateInput{}, err
	}
	return ProjectGateInput{Claim: claim, Confidence: confidence}, nil
}

func (adapter *PromotionInputAdapter) BuildOrgGate(ctx context.Context, options OrgGateOptions) (OrgGateInput, error) {
	claim, confidence, err := adapter.build(ctx, options.ProjectID, options.ClaimID)
	if err != nil {
		return OrgGateInput{}, err
	}
	return OrgGateInput{
		Claim:                claim,
		Confidence:           confidence,
		ClaimCheckActionable: confidence.Inputs.ClaimCheckActionable,
		Scope:                options.Scope,
		OrgRef:               options.OrgRef,
		DecisionRef:          options.DecisionRef,
		VerifierRef:          options.VerifierRef,
		Rationale:            options.Rationale,
		DecidedBy:            options.DecidedBy,
	}, nil
}

func (svc *Service) ValidateCandidateWithInputs(ctx context.Context, adapter *PromotionInputAdapter, input ValidateCandidateWithInputsInput) (KnowledgeRecord, error) {
	record, gate, err := svc.projectGateFromInputs(ctx, adapter, input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	return svc.ValidateCandidate(ctx, ValidateCandidateInput{
		ProjectID:   record.ProjectID,
		KnowledgeID: record.ID,
		DecisionRef: input.DecisionRef,
		VerifierRef: input.VerifierRef,
		Rationale:   input.Rationale,
		Gate:        gate,
	})
}

func (svc *Service) PromoteProjectWithInputs(ctx context.Context, adapter *PromotionInputAdapter, input PromoteProjectWithInputsInput) (KnowledgeRecord, error) {
	record, gate, err := svc.projectGateFromInputs(ctx, adapter, input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	return svc.PromoteProject(ctx, PromoteProjectInput{
		ProjectID:   record.ProjectID,
		KnowledgeID: record.ID,
		DecisionRef: input.DecisionRef,
		VerifierRef: input.VerifierRef,
		Rationale:   input.Rationale,
		Gate:        gate,
	})
}

func (svc *Service) PromoteOrgWithInputs(ctx context.Context, adapter *PromotionInputAdapter, input PromoteOrgWithInputsInput) (KnowledgeRecord, error) {
	record, err := svc.recordForInputs(ctx, input.ProjectID, input.KnowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	gate, err := adapter.BuildOrgGate(ctx, OrgGateOptions{
		ProjectID:            record.ProjectID,
		ClaimID:              record.ClaimID,
		ClaimCheckActionable: input.ClaimCheckActionable,
		Scope:                input.Scope,
		OrgRef:               input.OrgRef,
		DecisionRef:          input.DecisionRef,
		VerifierRef:          input.VerifierRef,
		Rationale:            input.Rationale,
		DecidedBy:            input.DecidedBy,
	})
	if err != nil {
		return KnowledgeRecord{}, err
	}
	return svc.PromoteOrg(ctx, PromoteOrgInput{ProjectID: record.ProjectID, KnowledgeID: record.ID, Gate: gate})
}

func (svc *Service) projectGateFromInputs(ctx context.Context, adapter *PromotionInputAdapter, projectID string, knowledgeID string) (KnowledgeRecord, ProjectGateInput, error) {
	record, err := svc.recordForInputs(ctx, projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, ProjectGateInput{}, err
	}
	gate, err := adapter.BuildProjectGate(ctx, ProjectGateOptions{ProjectID: record.ProjectID, ClaimID: record.ClaimID})
	if err != nil {
		return KnowledgeRecord{}, ProjectGateInput{}, err
	}
	return record, gate, nil
}

func (svc *Service) recordForInputs(ctx context.Context, projectID string, knowledgeID string) (KnowledgeRecord, error) {
	if svc == nil || svc.store == nil {
		return KnowledgeRecord{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, knowledgeID, err := safeProjectKnowledgeIDs(projectID, knowledgeID)
	if err != nil {
		return KnowledgeRecord{}, err
	}
	return svc.store.GetKnowledge(ctx, projectID, knowledgeID)
}

func (adapter *PromotionInputAdapter) build(ctx context.Context, projectID string, claimID string) (projectevidence.ClaimRecord, projectconfidence.ConfidenceAssessment, error) {
	if adapter == nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: promotion input adapter is required", ErrInvalidInput)
	}
	if adapter.evidence == nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: evidence claim reader is required", ErrInvalidInput)
	}
	if adapter.confidence == nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: confidence assessment reader is required", ErrInvalidInput)
	}
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, err
	}
	claimID, err = safeRefIdentifier(claimID, "claim_id")
	if err != nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, err
	}
	claim, err := adapter.evidence.GetClaim(ctx, projectID, claimID)
	if err != nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, err
	}
	if claim.Claim.ProjectID != "" && claim.Claim.ProjectID != projectID {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: claim project mismatch", ErrInvalidInput)
	}
	if claim.Claim.ID != "" && claim.Claim.ID != claimID {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: claim id mismatch", ErrInvalidInput)
	}
	confidence, err := adapter.confidence.GetAssessment(ctx, projectID, claimID)
	if err != nil {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, err
	}
	if confidence.ProjectID != "" && confidence.ProjectID != projectID {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: confidence project mismatch", ErrInvalidInput)
	}
	if confidence.ClaimID != "" && confidence.ClaimID != claimID {
		return projectevidence.ClaimRecord{}, projectconfidence.ConfidenceAssessment{}, fmt.Errorf("%w: confidence claim mismatch", ErrInvalidInput)
	}
	return claim, confidence, nil
}
