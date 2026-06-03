package projectevidence

import (
	"context"
	"time"
)

const (
	ClaimStatusCandidate = "candidate"
	ClaimStatusValidated = "validated"
	ClaimStatusPromoted  = "promoted"
	ClaimStatusRejected  = "rejected"

	EvidenceKindContextPack = "context_pack"
	EvidenceKindFile        = "file"
	EvidenceKindChunk       = "chunk"
	EvidenceKindSymbol      = "symbol"
	EvidenceKindVerifier    = "verifier"
	EvidenceKindClaimCheck  = "claim_check"
	EvidenceKindArtifact    = "artifact"
	EvidenceKindOther       = "other"

	DecisionStateValidated = "validated"
	DecisionStatePromoted  = "promoted"
	DecisionStateRejected  = "rejected"

	ActionKindCodeChange    = "code_change"
	ActionKindDocChange     = "doc_change"
	ActionKindVerifierRun   = "verifier_run"
	ActionKindConfigChange  = "config_change"
	ActionKindReviewComment = "review_comment"
	ActionKindOther         = "other"

	OutcomeKindTest         = "test"
	OutcomeKindBuild        = "build"
	OutcomeKindClaimCheck   = "claim_check"
	OutcomeKindManualReview = "manual_review"
	OutcomeKindPromotion    = "promotion"
	OutcomeKindFailure      = "failure"
	OutcomeKindOther        = "other"

	OutcomeStatusPassed  = "passed"
	OutcomeStatusFailed  = "failed"
	OutcomeStatusBlocked = "blocked"
	OutcomeStatusUnknown = "unknown"

	PromotionStateCandidate = "candidate"
	PromotionStateValidated = "validated"
	PromotionStatePromoted  = "promoted"
	PromotionStateRejected  = "rejected"
)

type Claim struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	RunID     string    `json:"run_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
	ClaimRef  string    `json:"claim_ref"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Evidence struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	ClaimID      string    `json:"claim_id"`
	EvidenceRef  string    `json:"evidence_ref"`
	EvidenceKind string    `json:"evidence_kind"`
	SourceRef    string    `json:"source_ref,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Decision struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ClaimID     string    `json:"claim_id"`
	DecisionRef string    `json:"decision_ref"`
	State       string    `json:"state"`
	VerifierRef string    `json:"verifier_ref"`
	Rationale   string    `json:"rationale"`
	DecidedAt   time.Time `json:"decided_at"`
}

type Action struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	ClaimID      string    `json:"claim_id"`
	DecisionID   string    `json:"decision_id"`
	ActionRef    string    `json:"action_ref"`
	ActionKind   string    `json:"action_kind"`
	Summary      string    `json:"summary,omitempty"`
	ChangedFiles []string  `json:"changed_files,omitempty"`
	RunID        string    `json:"run_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type Outcome struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ClaimID     string    `json:"claim_id"`
	ActionID    string    `json:"action_id"`
	OutcomeRef  string    `json:"outcome_ref"`
	OutcomeKind string    `json:"outcome_kind"`
	Status      string    `json:"status"`
	VerifierRef string    `json:"verifier_ref,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type ArtifactLink struct {
	ProjectID    string `json:"project_id"`
	ClaimID      string `json:"claim_id"`
	ArtifactRef  string `json:"artifact_ref"`
	ArtifactKind string `json:"artifact_kind,omitempty"`
	RunID        string `json:"run_id,omitempty"`
}

type PromotionLink struct {
	ProjectID      string    `json:"project_id"`
	ClaimID        string    `json:"claim_id"`
	RunID          string    `json:"run_id,omitempty"`
	ArtifactRef    string    `json:"artifact_ref"`
	PromotionState string    `json:"promotion_state"`
	SourceRef      string    `json:"source_ref"`
	VerifierRef    string    `json:"verifier_ref,omitempty"`
	DecisionRef    string    `json:"decision_ref,omitempty"`
	ActionRef      string    `json:"action_ref,omitempty"`
	OutcomeRef     string    `json:"outcome_ref,omitempty"`
	DecidedAt      time.Time `json:"decided_at"`
}

type ClaimRecord struct {
	Claim          Claim           `json:"claim"`
	Evidence       []Evidence      `json:"evidence,omitempty"`
	Decisions      []Decision      `json:"decisions,omitempty"`
	Actions        []Action        `json:"actions,omitempty"`
	Outcomes       []Outcome       `json:"outcomes,omitempty"`
	ArtifactLinks  []ArtifactLink  `json:"artifact_links,omitempty"`
	PromotionLinks []PromotionLink `json:"promotion_links,omitempty"`
}

type ClaimFilter struct {
	ArtifactRef    string
	PromotionState string
	OutcomeStatus  string
	RunID          string
	TraceID        string
}

type CreateClaimInput struct {
	ProjectID string `json:"project_id"`
	RunID     string `json:"run_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
	ClaimRef  string `json:"claim_ref"`
	Summary   string `json:"summary"`
	Status    string `json:"status,omitempty"`
}

type AppendEvidenceInput struct {
	ProjectID    string `json:"project_id"`
	ClaimID      string `json:"claim_id"`
	EvidenceRef  string `json:"evidence_ref"`
	EvidenceKind string `json:"evidence_kind"`
	SourceRef    string `json:"source_ref,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type CreateDecisionInput struct {
	ProjectID   string `json:"project_id"`
	ClaimID     string `json:"claim_id"`
	DecisionRef string `json:"decision_ref"`
	State       string `json:"state"`
	VerifierRef string `json:"verifier_ref"`
	Rationale   string `json:"rationale"`
}

type CreateActionInput struct {
	ProjectID    string   `json:"project_id"`
	ClaimID      string   `json:"claim_id"`
	DecisionID   string   `json:"decision_id"`
	ActionRef    string   `json:"action_ref"`
	ActionKind   string   `json:"action_kind"`
	Summary      string   `json:"summary,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	RunID        string   `json:"run_id,omitempty"`
}

type CreateOutcomeInput struct {
	ProjectID   string `json:"project_id"`
	ClaimID     string `json:"claim_id"`
	ActionID    string `json:"action_id"`
	OutcomeRef  string `json:"outcome_ref"`
	OutcomeKind string `json:"outcome_kind"`
	Status      string `json:"status"`
	VerifierRef string `json:"verifier_ref,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type LinkArtifactInput struct {
	ProjectID    string `json:"project_id"`
	ClaimID      string `json:"claim_id"`
	ArtifactRef  string `json:"artifact_ref"`
	ArtifactKind string `json:"artifact_kind,omitempty"`
	RunID        string `json:"run_id,omitempty"`
}

type LinkPromotionInput struct {
	ProjectID      string `json:"project_id"`
	ClaimID        string `json:"claim_id"`
	RunID          string `json:"run_id,omitempty"`
	ArtifactRef    string `json:"artifact_ref"`
	PromotionState string `json:"promotion_state"`
	SourceRef      string `json:"source_ref"`
	VerifierRef    string `json:"verifier_ref,omitempty"`
	DecisionRef    string `json:"decision_ref,omitempty"`
	ActionRef      string `json:"action_ref,omitempty"`
	OutcomeRef     string `json:"outcome_ref,omitempty"`
}

type Store interface {
	CreateClaim(ctx context.Context, claim Claim) (Claim, error)
	GetClaim(ctx context.Context, projectID string, claimID string) (ClaimRecord, error)
	ListClaims(ctx context.Context, projectID string, filter ClaimFilter) ([]Claim, error)
	AppendEvidence(ctx context.Context, evidence Evidence) (Evidence, error)
	CreateDecision(ctx context.Context, decision Decision) (Decision, error)
	CreateAction(ctx context.Context, action Action) (Action, error)
	CreateOutcome(ctx context.Context, outcome Outcome) (Outcome, error)
	LinkArtifact(ctx context.Context, link ArtifactLink) (ArtifactLink, error)
	LinkPromotion(ctx context.Context, link PromotionLink) (PromotionLink, error)
}
