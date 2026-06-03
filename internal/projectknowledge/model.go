package projectknowledge

import (
	"context"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

const (
	ScopeProject = "project"
	ScopeOrg     = "org"

	DefaultOrgRef = "default"

	StateCandidate       = "candidate"
	StateValidated       = "validated"
	StateProjectPromoted = "project_promoted"
	StateOrgReview       = "org_review"
	StateOrgPromoted     = "org_promoted"
	StateRejected        = "rejected"
	StateSuperseded      = "superseded"

	ReuseOutcomeUsed         = "used"
	ReuseOutcomeSkipped      = "skipped"
	ReuseOutcomeStale        = "stale"
	ReuseOutcomeContradicted = "contradicted"
)

type KnowledgeRecord struct {
	ID                     string    `json:"id"`
	ProjectID              string    `json:"project_id"`
	Scope                  string    `json:"scope"`
	OrgRef                 string    `json:"org_ref,omitempty"`
	KnowledgeRef           string    `json:"knowledge_ref"`
	ClaimID                string    `json:"claim_id"`
	ClaimRef               string    `json:"claim_ref"`
	ConfidenceAssessmentID string    `json:"confidence_assessment_id"`
	ConfidenceScore        int       `json:"confidence_score"`
	ConfidenceBand         string    `json:"confidence_band"`
	State                  string    `json:"state"`
	Summary                string    `json:"summary"`
	ReuseGuidance          string    `json:"reuse_guidance"`
	EvidenceRefs           []string  `json:"evidence_refs,omitempty"`
	VerifierRefs           []string  `json:"verifier_refs,omitempty"`
	OutcomeRefs            []string  `json:"outcome_refs,omitempty"`
	PromotionRefs          []string  `json:"promotion_refs,omitempty"`
	SupersedesRef          string    `json:"supersedes_ref,omitempty"`
	SupersededByRef        string    `json:"superseded_by_ref,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	PromotedAt             time.Time `json:"promoted_at,omitempty"`
}

type PromotionDecision struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	FromState       string    `json:"from_state"`
	ToState         string    `json:"to_state"`
	Scope           string    `json:"scope"`
	DecisionRef     string    `json:"decision_ref"`
	VerifierRef     string    `json:"verifier_ref"`
	ConfidenceScore int       `json:"confidence_score"`
	Rationale       string    `json:"rationale"`
	DecidedBy       string    `json:"decided_by,omitempty"`
	DecidedAt       time.Time `json:"decided_at"`
}

type KnowledgeReuseEvent struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	KnowledgeID     string    `json:"knowledge_id"`
	AgentRunID      string    `json:"agent_run_id,omitempty"`
	TraceID         string    `json:"trace_id,omitempty"`
	ReuseRef        string    `json:"reuse_ref"`
	Revalidated     bool      `json:"revalidated"`
	RevalidationRef string    `json:"revalidation_ref,omitempty"`
	Outcome         string    `json:"outcome"`
	Summary         string    `json:"summary,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type KnowledgeFilter struct {
	Scope          string `json:"scope,omitempty"`
	OrgRef         string `json:"org_ref,omitempty"`
	State          string `json:"state,omitempty"`
	ClaimID        string `json:"claim_id,omitempty"`
	KnowledgeRef   string `json:"knowledge_ref,omitempty"`
	ConfidenceBand string `json:"confidence_band,omitempty"`
	MinConfidence  *int   `json:"min_confidence,omitempty"`
	MaxConfidence  *int   `json:"max_confidence,omitempty"`
	PageSize       int    `json:"page_size,omitempty"`
	PageToken      string `json:"page_token,omitempty"`
}

type CreateCandidateInput struct {
	ProjectID              string   `json:"project_id"`
	KnowledgeRef           string   `json:"knowledge_ref"`
	ClaimID                string   `json:"claim_id"`
	ClaimRef               string   `json:"claim_ref"`
	ConfidenceAssessmentID string   `json:"confidence_assessment_id,omitempty"`
	ConfidenceScore        int      `json:"confidence_score,omitempty"`
	ConfidenceBand         string   `json:"confidence_band,omitempty"`
	Summary                string   `json:"summary"`
	ReuseGuidance          string   `json:"reuse_guidance"`
	EvidenceRefs           []string `json:"evidence_refs,omitempty"`
	VerifierRefs           []string `json:"verifier_refs,omitempty"`
	OutcomeRefs            []string `json:"outcome_refs,omitempty"`
	PromotionRefs          []string `json:"promotion_refs,omitempty"`
	SupersedesRef          string   `json:"supersedes_ref,omitempty"`
}

type ProjectGateInput struct {
	Claim      projectevidence.ClaimRecord            `json:"claim"`
	Confidence projectconfidence.ConfidenceAssessment `json:"confidence"`
}

type OrgGateInput struct {
	Claim                projectevidence.ClaimRecord            `json:"claim"`
	Confidence           projectconfidence.ConfidenceAssessment `json:"confidence"`
	ClaimCheckActionable int                                    `json:"claim_check_actionable"`
	Scope                string                                 `json:"scope"`
	OrgRef               string                                 `json:"org_ref,omitempty"`
	DecisionRef          string                                 `json:"decision_ref"`
	VerifierRef          string                                 `json:"verifier_ref"`
	Rationale            string                                 `json:"rationale"`
	DecidedBy            string                                 `json:"decided_by"`
}

type ValidateCandidateInput struct {
	ProjectID   string           `json:"project_id"`
	KnowledgeID string           `json:"knowledge_id"`
	DecisionRef string           `json:"decision_ref"`
	VerifierRef string           `json:"verifier_ref"`
	Rationale   string           `json:"rationale"`
	Gate        ProjectGateInput `json:"gate"`
}

type PromoteProjectInput struct {
	ProjectID   string           `json:"project_id"`
	KnowledgeID string           `json:"knowledge_id"`
	DecisionRef string           `json:"decision_ref"`
	VerifierRef string           `json:"verifier_ref"`
	Rationale   string           `json:"rationale"`
	Gate        ProjectGateInput `json:"gate"`
}

type SubmitOrgReviewInput struct {
	ProjectID   string `json:"project_id"`
	KnowledgeID string `json:"knowledge_id"`
	OrgRef      string `json:"org_ref,omitempty"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by"`
}

type PromoteOrgInput struct {
	ProjectID   string       `json:"project_id"`
	KnowledgeID string       `json:"knowledge_id"`
	Gate        OrgGateInput `json:"gate"`
}

type RejectInput struct {
	ProjectID   string `json:"project_id"`
	KnowledgeID string `json:"knowledge_id"`
	DecisionRef string `json:"decision_ref"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
	DecidedBy   string `json:"decided_by,omitempty"`
}

type SupersedeInput struct {
	ProjectID       string `json:"project_id"`
	KnowledgeID     string `json:"knowledge_id"`
	SupersededByRef string `json:"superseded_by_ref"`
	DecisionRef     string `json:"decision_ref"`
	VerifierRef     string `json:"verifier_ref"`
	Rationale       string `json:"rationale"`
	DecidedBy       string `json:"decided_by,omitempty"`
}

type RecordReuseEventInput struct {
	ProjectID       string `json:"project_id"`
	KnowledgeID     string `json:"knowledge_id"`
	AgentRunID      string `json:"agent_run_id,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	ReuseRef        string `json:"reuse_ref"`
	Revalidated     bool   `json:"revalidated"`
	RevalidationRef string `json:"revalidation_ref,omitempty"`
	Outcome         string `json:"outcome"`
	Summary         string `json:"summary,omitempty"`
}

type Store interface {
	CreateKnowledge(ctx context.Context, record KnowledgeRecord) (KnowledgeRecord, error)
	GetKnowledge(ctx context.Context, projectID string, knowledgeID string) (KnowledgeRecord, error)
	ListKnowledge(ctx context.Context, projectID string, filter KnowledgeFilter) ([]KnowledgeRecord, error)
	ListOrgKnowledge(ctx context.Context, orgRef string, filter KnowledgeFilter) ([]KnowledgeRecord, error)
	UpdateKnowledge(ctx context.Context, record KnowledgeRecord) (KnowledgeRecord, error)
	CreateDecision(ctx context.Context, decision PromotionDecision) (PromotionDecision, error)
	ListDecisions(ctx context.Context, projectID string, knowledgeID string) ([]PromotionDecision, error)
	CreateReuseEvent(ctx context.Context, event KnowledgeReuseEvent) (KnowledgeReuseEvent, error)
	ListReuseEvents(ctx context.Context, projectID string, knowledgeID string) ([]KnowledgeReuseEvent, error)
}
