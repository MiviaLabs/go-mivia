package projectconfidence

import (
	"context"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

const (
	ScoreBandHigh    = "high"
	ScoreBandMedium  = "medium"
	ScoreBandLow     = "low"
	ScoreBandUnknown = "unknown"

	RecommendationPromote              = "promote"
	RecommendationVerify               = "verify"
	RecommendationReview               = "review"
	RecommendationReject               = "reject"
	RecommendationInsufficientEvidence = "insufficient_evidence"

	FactorStatusPositive = "positive"
	FactorStatusNeutral  = "neutral"
	FactorStatusNegative = "negative"
)

type ConfidenceAssessment struct {
	ID             string             `json:"id"`
	ProjectID      string             `json:"project_id"`
	ClaimID        string             `json:"claim_id"`
	ClaimRef       string             `json:"claim_ref"`
	RunID          string             `json:"run_id,omitempty"`
	TraceID        string             `json:"trace_id,omitempty"`
	Score          int                `json:"score"`
	Band           string             `json:"band"`
	Recommendation string             `json:"recommendation"`
	Factors        []ConfidenceFactor `json:"factors"`
	Inputs         ConfidenceInputs   `json:"inputs"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type ConfidenceFactor struct {
	Name       string `json:"name"`
	ScoreDelta int    `json:"score_delta"`
	Weight     int    `json:"weight"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	SourceRef  string `json:"source_ref"`
}

type ConfidenceInputs struct {
	EvidenceCount              int      `json:"evidence_count"`
	EvidenceKinds              []string `json:"evidence_kinds,omitempty"`
	DecisionCount              int      `json:"decision_count"`
	ActionCount                int      `json:"action_count"`
	PassedOutcomeCount         int      `json:"passed_outcome_count"`
	FailedOutcomeCount         int      `json:"failed_outcome_count"`
	PromotionState             string   `json:"promotion_state,omitempty"`
	ContextHealthStatus        string   `json:"context_health_status,omitempty"`
	ContextHealthReason        string   `json:"context_health_reason,omitempty"`
	LatestRunAgeSeconds        int64    `json:"latest_run_age_seconds,omitempty"`
	ClaimCheckVerified         int      `json:"claim_check_verified"`
	ClaimCheckActionable       int      `json:"claim_check_actionable"`
	ImpactPartial              bool     `json:"impact_partial"`
	ImpactResidualUnknownCount int      `json:"impact_residual_unknown_count"`
	ImpactSecurityFlagCount    int      `json:"impact_security_flag_count"`
}

type ScoreClaimRequest struct {
	Claim      projectevidence.ClaimRecord `json:"claim"`
	StoreScore bool                        `json:"store_score,omitempty"`
}

type ScoreClaimResponse struct {
	Assessment ConfidenceAssessment `json:"assessment"`
}

type GetAssessmentRequest struct {
	ProjectID string `json:"project_id"`
	ClaimID   string `json:"claim_id"`
}

type ListAssessmentRequest struct {
	ProjectID string           `json:"project_id"`
	Filter    AssessmentFilter `json:"filter,omitempty"`
}

type AssessmentFilter struct {
	Band           string `json:"band,omitempty"`
	MinScore       *int   `json:"min_score,omitempty"`
	MaxScore       *int   `json:"max_score,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
}

type Store interface {
	CreateAssessment(ctx context.Context, assessment ConfidenceAssessment) (ConfidenceAssessment, error)
	GetAssessment(ctx context.Context, projectID string, claimID string) (ConfidenceAssessment, error)
	ListAssessments(ctx context.Context, projectID string, filter AssessmentFilter) ([]ConfidenceAssessment, error)
}
