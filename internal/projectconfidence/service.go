package projectconfidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	"github.com/MiviaLabs/go-mivia/internal/projectreliability"
)

var ErrInvalidInput = errors.New("invalid input")

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)

type Service struct {
	store Store
	now   func() time.Time
}

func New(store Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (svc *Service) ScoreClaim(ctx context.Context, record projectevidence.ClaimRecord, health projectreliability.ContextHealth, claimCheck *projectreliability.ClaimCheckResult, impact *projectreliability.ImpactAnalysis) (ConfidenceAssessment, error) {
	assessment, err := svc.Score(record, health, claimCheck, impact)
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	if svc.store == nil {
		return assessment, nil
	}
	return svc.store.CreateAssessment(ctx, assessment)
}

func (svc *Service) Score(record projectevidence.ClaimRecord, health projectreliability.ContextHealth, claimCheck *projectreliability.ClaimCheckResult, impact *projectreliability.ImpactAnalysis) (ConfidenceAssessment, error) {
	projectID, err := safeRefIdentifier(record.Claim.ProjectID, "project_id")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	claimID, err := safeRefIdentifier(record.Claim.ID, "claim_id")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	claimRef, err := safeRefIdentifier(record.Claim.ClaimRef, "claim_ref")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	runID, err := safeOptionalRefIdentifier(record.Claim.RunID, "run_id")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	traceID, err := safeOptionalRefIdentifier(record.Claim.TraceID, "trace_id")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := validateClaimRecordMetadata(record); err != nil {
		return ConfidenceAssessment{}, err
	}
	if health.ProjectID != "" && health.ProjectID != projectID {
		return ConfidenceAssessment{}, fmt.Errorf("%w: context health project mismatch", ErrInvalidInput)
	}
	if claimCheck != nil && claimCheck.ProjectID != "" && claimCheck.ProjectID != projectID {
		return ConfidenceAssessment{}, fmt.Errorf("%w: claim check project mismatch", ErrInvalidInput)
	}
	if impact != nil && impact.ProjectID != "" && impact.ProjectID != projectID {
		return ConfidenceAssessment{}, fmt.Errorf("%w: impact project mismatch", ErrInvalidInput)
	}
	inputs, err := buildInputs(record, health, claimCheck, impact, svc.now())
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	score := 50
	factors := make([]ConfidenceFactor, 0, 8)
	add := func(name string, delta int, weight int, status string, summary string, sourceRef string) error {
		factor := ConfidenceFactor{Name: name, ScoreDelta: delta, Weight: weight, Status: status, Summary: summary, SourceRef: sourceRef}
		if err := validateFactor(factor); err != nil {
			return err
		}
		factors = append(factors, factor)
		score += delta
		return nil
	}
	if err := addEvidenceCountFactor(&add, inputs.EvidenceCount); err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := addEvidenceDiversityFactor(&add, len(inputs.EvidenceKinds)); err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := addContextHealthFactor(&add, inputs.ContextHealthStatus); err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := addFreshnessFactor(&add, health.LatestRun, inputs.LatestRunAgeSeconds); err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := addClaimCheckFactor(&add, claimCheck, inputs); err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := addImpactFactor(&add, impact, inputs); err != nil {
		return ConfidenceAssessment{}, err
	}
	if err := addLifecycleFactors(&add, record, inputs); err != nil {
		return ConfidenceAssessment{}, err
	}
	score = clampScore(score)
	band := scoreBand(score)
	if inputs.ContextHealthStatus == "" {
		band = ScoreBandUnknown
	}
	recommendation := recommendationFor(score, band, record, inputs, claimCheck != nil && claimCheck.Summary.Total > 0, impact != nil)
	now := svc.now()
	assessment := ConfidenceAssessment{
		ID:             deterministicID(projectID, claimID),
		ProjectID:      projectID,
		ClaimID:        claimID,
		ClaimRef:       claimRef,
		RunID:          runID,
		TraceID:        traceID,
		Score:          score,
		Band:           band,
		Recommendation: recommendation,
		Factors:        factors,
		Inputs:         inputs,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := validateAssessment(assessment); err != nil {
		return ConfidenceAssessment{}, err
	}
	return assessment, nil
}

func (svc *Service) GetAssessment(ctx context.Context, projectID string, claimID string) (ConfidenceAssessment, error) {
	if svc.store == nil {
		return ConfidenceAssessment{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	claimID, err = safeRefIdentifier(claimID, "claim_id")
	if err != nil {
		return ConfidenceAssessment{}, err
	}
	return svc.store.GetAssessment(ctx, projectID, claimID)
}

func (svc *Service) ListAssessments(ctx context.Context, projectID string, filter AssessmentFilter) ([]ConfidenceAssessment, error) {
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
	return svc.store.ListAssessments(ctx, projectID, filter)
}

func buildInputs(record projectevidence.ClaimRecord, health projectreliability.ContextHealth, claimCheck *projectreliability.ClaimCheckResult, impact *projectreliability.ImpactAnalysis, now time.Time) (ConfidenceInputs, error) {
	kindSet := map[string]struct{}{}
	for _, evidence := range record.Evidence {
		if _, err := safeRefIdentifier(evidence.ID, "evidence_id"); err != nil {
			return ConfidenceInputs{}, err
		}
		if _, err := safeRefIdentifier(evidence.EvidenceRef, "evidence_ref"); err != nil {
			return ConfidenceInputs{}, err
		}
		if evidence.SourceRef != "" {
			if _, err := safeRefIdentifier(evidence.SourceRef, "source_ref"); err != nil {
				return ConfidenceInputs{}, err
			}
		}
		kindSet[evidence.EvidenceKind] = struct{}{}
	}
	kinds := make([]string, 0, len(kindSet))
	for kind := range kindSet {
		if _, err := safeEvidenceKind(kind); err != nil {
			return ConfidenceInputs{}, err
		}
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	inputs := ConfidenceInputs{
		EvidenceCount:       len(record.Evidence),
		EvidenceKinds:       kinds,
		DecisionCount:       len(record.Decisions),
		ActionCount:         len(record.Actions),
		ContextHealthStatus: strings.TrimSpace(string(health.Status)),
		ContextHealthReason: strings.TrimSpace(health.StatusReason),
	}
	if inputs.ContextHealthReason != "" {
		if _, err := safeOptionalText(inputs.ContextHealthReason, "context_health_reason", 200); err != nil {
			return ConfidenceInputs{}, err
		}
	}
	if health.LatestRun != nil && !health.LatestRun.LastProgressAt.IsZero() {
		age := now.Sub(health.LatestRun.LastProgressAt)
		if age < 0 {
			age = 0
		}
		inputs.LatestRunAgeSeconds = int64(age.Seconds())
	}
	for _, outcome := range record.Outcomes {
		if _, err := safeRefIdentifier(outcome.ID, "outcome_id"); err != nil {
			return ConfidenceInputs{}, err
		}
		switch outcome.Status {
		case projectevidence.OutcomeStatusPassed:
			inputs.PassedOutcomeCount++
		case projectevidence.OutcomeStatusFailed:
			inputs.FailedOutcomeCount++
		}
	}
	for _, link := range record.PromotionLinks {
		if _, err := safePromotionState(link.PromotionState); err != nil {
			return ConfidenceInputs{}, err
		}
		if inputs.PromotionState == "" || link.PromotionState == projectevidence.PromotionStatePromoted || link.PromotionState == projectevidence.PromotionStateRejected {
			inputs.PromotionState = link.PromotionState
		}
	}
	if claimCheck != nil {
		inputs.ClaimCheckVerified = claimCheck.Summary.Verified
		inputs.ClaimCheckActionable = claimCheck.Summary.Actionable
		for _, finding := range claimCheck.Claims {
			if finding.SafeMessage != "" {
				if _, err := safeOptionalText(finding.SafeMessage, "claim_check_safe_message", 300); err != nil {
					return ConfidenceInputs{}, err
				}
			}
		}
	}
	if impact != nil {
		inputs.ImpactPartial = impact.Partial
		inputs.ImpactResidualUnknownCount = len(impact.ResidualUnknowns)
		inputs.ImpactSecurityFlagCount = len(impact.SecurityFlags)
		for _, value := range append(append([]string{}, impact.ResidualUnknowns...), impact.SecurityFlags...) {
			if _, err := safeOptionalIdentifier(value, "impact_ref"); err != nil {
				return ConfidenceInputs{}, err
			}
		}
	}
	return inputs, nil
}

func validateClaimRecordMetadata(record projectevidence.ClaimRecord) error {
	if _, err := safeOptionalText(record.Claim.Summary, "claim_summary", 500); err != nil {
		return err
	}
	if record.Claim.Status != "" {
		if _, err := safeClaimStatus(record.Claim.Status); err != nil {
			return err
		}
	}
	for _, evidence := range record.Evidence {
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
		for _, changedFile := range action.ChangedFiles {
			if _, err := safeRefIdentifier(changedFile, "changed_file"); err != nil {
				return err
			}
		}
	}
	for _, outcome := range record.Outcomes {
		if _, err := safeRefIdentifier(outcome.OutcomeRef, "outcome_ref"); err != nil {
			return err
		}
		if _, err := safeRefIdentifier(outcome.ActionID, "outcome_action_id"); err != nil {
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
	for _, link := range record.ArtifactLinks {
		if _, err := safeRefIdentifier(link.ArtifactRef, "artifact_ref"); err != nil {
			return err
		}
		if _, err := safeOptionalIdentifier(link.ArtifactKind, "artifact_kind"); err != nil {
			return err
		}
		if _, err := safeOptionalRefIdentifier(link.RunID, "artifact_run_id"); err != nil {
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

func addEvidenceCountFactor(add *func(string, int, int, string, string, string) error, count int) error {
	switch {
	case count == 0:
		return (*add)("evidence_count", -25, 25, FactorStatusNegative, "no evidence refs are linked", "claim.evidence")
	case count == 1:
		return (*add)("evidence_count", -10, 25, FactorStatusNegative, "one evidence ref is linked", "claim.evidence")
	case count <= 3:
		return (*add)("evidence_count", 10, 25, FactorStatusPositive, "two or three evidence refs are linked", "claim.evidence")
	default:
		return (*add)("evidence_count", 15, 25, FactorStatusPositive, "four or more evidence refs are linked", "claim.evidence")
	}
}

func addEvidenceDiversityFactor(add *func(string, int, int, string, string, string) error, kindCount int) error {
	switch {
	case kindCount <= 1:
		return (*add)("evidence_kind_diversity", -5, 10, FactorStatusNegative, "evidence uses one kind or none", "claim.evidence_kinds")
	case kindCount == 2:
		return (*add)("evidence_kind_diversity", 5, 10, FactorStatusPositive, "evidence uses two kinds", "claim.evidence_kinds")
	default:
		return (*add)("evidence_kind_diversity", 10, 10, FactorStatusPositive, "evidence uses at least three kinds", "claim.evidence_kinds")
	}
}

func addContextHealthFactor(add *func(string, int, int, string, string, string) error, status string) error {
	switch status {
	case string(projectreliability.ContextHealthReady):
		return (*add)("context_health", 10, 20, FactorStatusPositive, "context graph is ready", "context_health.status")
	case string(projectreliability.ContextHealthSyncing), string(projectreliability.ContextHealthWarmingUp):
		return (*add)("context_health", -5, 20, FactorStatusNegative, "context graph is catching up", "context_health.status")
	case string(projectreliability.ContextHealthStale):
		return (*add)("context_health", -10, 20, FactorStatusNegative, "context graph is stale", "context_health.status")
	case string(projectreliability.ContextHealthDegraded), string(projectreliability.ContextHealthEmpty), string(projectreliability.ContextHealthDisabled), string(projectreliability.ContextHealthUnavailable):
		return (*add)("context_health", -20, 20, FactorStatusNegative, "context graph is degraded or unavailable", "context_health.status")
	default:
		return (*add)("context_health", -20, 20, FactorStatusNegative, "context health status is missing", "context_health.status")
	}
}

func addFreshnessFactor(add *func(string, int, int, string, string, string) error, latest *projectreliability.RunSummary, ageSeconds int64) error {
	if latest == nil || latest.LastProgressAt.IsZero() {
		return (*add)("latest_ingestion_freshness", -10, 10, FactorStatusNegative, "latest ingestion progress is unavailable", "context_health.latest_run")
	}
	if ageSeconds <= int64((24 * time.Hour).Seconds()) {
		return (*add)("latest_ingestion_freshness", 10, 10, FactorStatusPositive, "latest ingestion progressed within 24 hours", "context_health.latest_run")
	}
	if ageSeconds <= int64((7 * 24 * time.Hour).Seconds()) {
		return (*add)("latest_ingestion_freshness", 0, 10, FactorStatusNeutral, "latest ingestion progressed within seven days", "context_health.latest_run")
	}
	return (*add)("latest_ingestion_freshness", -10, 10, FactorStatusNegative, "latest ingestion is older than seven days", "context_health.latest_run")
}

func addClaimCheckFactor(add *func(string, int, int, string, string, string) error, result *projectreliability.ClaimCheckResult, inputs ConfidenceInputs) error {
	if result == nil || result.Summary.Total == 0 {
		return (*add)("claim_checks", 0, 15, FactorStatusNeutral, "claim checks are unavailable", "claim_check.summary")
	}
	if inputs.ClaimCheckActionable > 0 {
		return (*add)("claim_checks", -15, 15, FactorStatusNegative, "actionable stale or unverified claim checks exist", "claim_check.summary")
	}
	return (*add)("claim_checks", 10, 15, FactorStatusPositive, "all checked claims are verified", "claim_check.summary")
}

func addImpactFactor(add *func(string, int, int, string, string, string) error, impact *projectreliability.ImpactAnalysis, inputs ConfidenceInputs) error {
	if impact == nil {
		return (*add)("impact_analysis", 0, 15, FactorStatusNeutral, "impact analysis is unavailable", "impact.summary")
	}
	delta := 0
	status := FactorStatusNeutral
	summary := "impact analysis found no residual unknowns"
	if inputs.ImpactPartial || inputs.ImpactResidualUnknownCount > 0 {
		delta -= 10
		status = FactorStatusNegative
		summary = "impact analysis is partial or has residual unknowns"
	} else {
		delta += 5
		status = FactorStatusPositive
	}
	if inputs.ImpactSecurityFlagCount > 0 {
		delta -= 10
		status = FactorStatusNegative
		summary = "impact analysis has security flags"
	}
	return (*add)("impact_analysis", delta, 15, status, summary, "impact.summary")
}

func addLifecycleFactors(add *func(string, int, int, string, string, string) error, record projectevidence.ClaimRecord, inputs ConfidenceInputs) error {
	if hasDecisionState(record, projectevidence.DecisionStateRejected) {
		if err := (*add)("evidence_graph_rejected_decision", -30, 30, FactorStatusNegative, "claim has a rejected decision", "claim.decisions"); err != nil {
			return err
		}
	} else if hasDecisionState(record, projectevidence.DecisionStateValidated) || hasDecisionState(record, projectevidence.DecisionStatePromoted) {
		if err := (*add)("evidence_graph_validated_decision", 10, 10, FactorStatusPositive, "claim has a validated or promoted decision", "claim.decisions"); err != nil {
			return err
		}
	}
	if inputs.PassedOutcomeCount > 0 {
		if err := (*add)("evidence_graph_passed_outcome", 15, 15, FactorStatusPositive, "claim has a passed outcome", "claim.outcomes"); err != nil {
			return err
		}
	}
	if inputs.FailedOutcomeCount > 0 {
		if err := (*add)("evidence_graph_failed_outcome", -20, 20, FactorStatusNegative, "claim has a failed outcome", "claim.outcomes"); err != nil {
			return err
		}
	}
	if inputs.PromotionState == projectevidence.PromotionStatePromoted && inputs.PassedOutcomeCount > 0 {
		if err := (*add)("evidence_graph_promoted_link", 15, 15, FactorStatusPositive, "claim has a promoted link with a passed outcome", "claim.promotions"); err != nil {
			return err
		}
	}
	if record.Claim.Status == projectevidence.ClaimStatusCandidate && inputs.DecisionCount == 0 && inputs.PassedOutcomeCount == 0 && inputs.FailedOutcomeCount == 0 && inputs.PromotionState == "" {
		return (*add)("evidence_graph_candidate_only", -10, 10, FactorStatusNegative, "claim remains candidate-only", "claim.status")
	}
	return nil
}

func hasDecisionState(record projectevidence.ClaimRecord, state string) bool {
	for _, decision := range record.Decisions {
		if decision.State == state {
			return true
		}
	}
	return false
}

func recommendationFor(score int, band string, record projectevidence.ClaimRecord, inputs ConfidenceInputs, claimCheckAvailable bool, impactAvailable bool) string {
	if hasDecisionState(record, projectevidence.DecisionStateRejected) || inputs.FailedOutcomeCount > 0 {
		return RecommendationReject
	}
	if inputs.EvidenceCount == 0 || score == 0 || band == ScoreBandUnknown {
		return RecommendationInsufficientEvidence
	}
	if score >= 85 && inputs.PassedOutcomeCount > 0 && inputs.ClaimCheckActionable == 0 && !isDegradedContext(inputs.ContextHealthStatus) {
		if !claimCheckAvailable || !impactAvailable {
			return RecommendationVerify
		}
		return RecommendationPromote
	}
	if score >= 60 && score <= 84 {
		return RecommendationVerify
	}
	if score >= 40 && score <= 59 || inputs.ImpactPartial || inputs.ContextHealthStatus == string(projectreliability.ContextHealthStale) || isDegradedContext(inputs.ContextHealthStatus) || inputs.ClaimCheckActionable > 0 {
		return RecommendationReview
	}
	return RecommendationVerify
}

func isDegradedContext(status string) bool {
	switch status {
	case string(projectreliability.ContextHealthDegraded), string(projectreliability.ContextHealthEmpty), string(projectreliability.ContextHealthDisabled), string(projectreliability.ContextHealthUnavailable):
		return true
	default:
		return false
	}
}

func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func scoreBand(score int) string {
	switch {
	case score == 0:
		return ScoreBandUnknown
	case score >= 85:
		return ScoreBandHigh
	case score >= 60:
		return ScoreBandMedium
	default:
		return ScoreBandLow
	}
}

func validateAssessment(assessment ConfidenceAssessment) error {
	if _, err := safeRefIdentifier(assessment.ID, "assessment_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(assessment.ProjectID, "project_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(assessment.ClaimID, "claim_id"); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(assessment.ClaimRef, "claim_ref"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(assessment.RunID, "run_id"); err != nil {
		return err
	}
	if _, err := safeOptionalRefIdentifier(assessment.TraceID, "trace_id"); err != nil {
		return err
	}
	if assessment.Score < 0 || assessment.Score > 100 {
		return fmt.Errorf("%w: score is out of bounds", ErrInvalidInput)
	}
	if _, err := safeBand(assessment.Band); err != nil {
		return err
	}
	if _, err := safeRecommendation(assessment.Recommendation); err != nil {
		return err
	}
	if len(assessment.Factors) == 0 {
		return fmt.Errorf("%w: factors are required", ErrInvalidInput)
	}
	for _, factor := range assessment.Factors {
		if err := validateFactor(factor); err != nil {
			return err
		}
	}
	for _, kind := range assessment.Inputs.EvidenceKinds {
		if _, err := safeEvidenceKind(kind); err != nil {
			return err
		}
	}
	if assessment.Inputs.PromotionState != "" {
		if _, err := safePromotionState(assessment.Inputs.PromotionState); err != nil {
			return err
		}
	}
	return nil
}

func validateFactor(factor ConfidenceFactor) error {
	if _, err := safeIdentifier(factor.Name, "factor_name"); err != nil {
		return err
	}
	if _, err := safeRequiredText(factor.Summary, "factor_summary", 300); err != nil {
		return err
	}
	if _, err := safeRefIdentifier(factor.SourceRef, "factor_source_ref"); err != nil {
		return err
	}
	if _, err := safeFactorStatus(factor.Status); err != nil {
		return err
	}
	return nil
}

func validateFilter(filter AssessmentFilter) (AssessmentFilter, error) {
	var err error
	filter.Band, err = safeOptionalBand(filter.Band)
	if err != nil {
		return AssessmentFilter{}, err
	}
	filter.Recommendation, err = safeOptionalRecommendation(filter.Recommendation)
	if err != nil {
		return AssessmentFilter{}, err
	}
	filter.RunID, err = safeOptionalRefIdentifier(filter.RunID, "run_id")
	if err != nil {
		return AssessmentFilter{}, err
	}
	filter.TraceID, err = safeOptionalRefIdentifier(filter.TraceID, "trace_id")
	if err != nil {
		return AssessmentFilter{}, err
	}
	if filter.MinScore != nil && (*filter.MinScore < 0 || *filter.MinScore > 100) {
		return AssessmentFilter{}, fmt.Errorf("%w: min_score is out of bounds", ErrInvalidInput)
	}
	if filter.MaxScore != nil && (*filter.MaxScore < 0 || *filter.MaxScore > 100) {
		return AssessmentFilter{}, fmt.Errorf("%w: max_score is out of bounds", ErrInvalidInput)
	}
	if filter.MinScore != nil && filter.MaxScore != nil && *filter.MinScore > *filter.MaxScore {
		return AssessmentFilter{}, fmt.Errorf("%w: min_score exceeds max_score", ErrInvalidInput)
	}
	return filter, nil
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

func safeOptionalIdentifier(value string, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeIdentifier(value, field)
}

func safeRefIdentifier(value string, field string) (string, error) {
	value, err := safeIdentifier(value, field)
	if err != nil {
		return "", err
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "..") || filepath.IsAbs(normalized) || containsProhibitedMarker(normalized) || containsRootMarker(normalized) || containsURL(normalized) {
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

func safeBand(band string) (string, error) {
	band = strings.TrimSpace(band)
	switch band {
	case ScoreBandHigh, ScoreBandMedium, ScoreBandLow, ScoreBandUnknown:
		return band, nil
	default:
		return "", fmt.Errorf("%w: score band is invalid", ErrInvalidInput)
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
	case RecommendationPromote, RecommendationVerify, RecommendationReview, RecommendationReject, RecommendationInsufficientEvidence:
		return recommendation, nil
	default:
		return "", fmt.Errorf("%w: recommendation is invalid", ErrInvalidInput)
	}
}

func safeOptionalRecommendation(recommendation string) (string, error) {
	recommendation = strings.TrimSpace(recommendation)
	if recommendation == "" {
		return "", nil
	}
	return safeRecommendation(recommendation)
}

func safeFactorStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	switch status {
	case FactorStatusPositive, FactorStatusNeutral, FactorStatusNegative:
		return status, nil
	default:
		return "", fmt.Errorf("%w: factor status is invalid", ErrInvalidInput)
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

func deterministicID(projectID string, claimID string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + claimID))
	return "confidence_" + hex.EncodeToString(sum[:12])
}
