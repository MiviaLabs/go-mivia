package projectevidence

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
	return &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
		newID: newID,
	}
}

func (svc *Service) CreateClaim(ctx context.Context, input CreateClaimInput) (Claim, error) {
	projectID, err := safeRefIdentifier(input.ProjectID, "project_id")
	if err != nil {
		return Claim{}, err
	}
	runID, err := safeOptionalRefIdentifier(input.RunID, "run_id")
	if err != nil {
		return Claim{}, err
	}
	traceID, err := safeOptionalRefIdentifier(input.TraceID, "trace_id")
	if err != nil {
		return Claim{}, err
	}
	claimRef, err := safeRefIdentifier(input.ClaimRef, "claim_ref")
	if err != nil {
		return Claim{}, err
	}
	summary, err := safeRequiredText(input.Summary, "summary", 500)
	if err != nil {
		return Claim{}, err
	}
	status, err := safeClaimStatus(input.Status)
	if err != nil {
		return Claim{}, err
	}
	now := svc.now()
	claim := Claim{
		ID:        svc.newID("claim"),
		ProjectID: projectID,
		RunID:     runID,
		TraceID:   traceID,
		ClaimRef:  claimRef,
		Summary:   summary,
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
	return svc.store.CreateClaim(ctx, claim)
}

func (svc *Service) GetClaim(ctx context.Context, projectID string, claimID string) (ClaimRecord, error) {
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return ClaimRecord{}, err
	}
	claimID, err = safeRefIdentifier(claimID, "claim_id")
	if err != nil {
		return ClaimRecord{}, err
	}
	return svc.store.GetClaim(ctx, projectID, claimID)
}

func (svc *Service) ListClaims(ctx context.Context, projectID string, filter ClaimFilter) ([]Claim, error) {
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return nil, err
	}
	filter.ArtifactRef, err = safeOptionalRefIdentifier(filter.ArtifactRef, "artifact_ref")
	if err != nil {
		return nil, err
	}
	filter.PromotionState, err = safeOptionalPromotionState(filter.PromotionState)
	if err != nil {
		return nil, err
	}
	filter.OutcomeStatus, err = safeOptionalOutcomeStatus(filter.OutcomeStatus)
	if err != nil {
		return nil, err
	}
	filter.RunID, err = safeOptionalRefIdentifier(filter.RunID, "run_id")
	if err != nil {
		return nil, err
	}
	filter.TraceID, err = safeOptionalRefIdentifier(filter.TraceID, "trace_id")
	if err != nil {
		return nil, err
	}
	return svc.store.ListClaims(ctx, projectID, filter)
}

func (svc *Service) AppendEvidence(ctx context.Context, input AppendEvidenceInput) (Evidence, error) {
	projectID, claimID, record, err := svc.validateClaim(ctx, input.ProjectID, input.ClaimID)
	if err != nil {
		return Evidence{}, err
	}
	_ = record
	evidenceRef, err := safeRefIdentifier(input.EvidenceRef, "evidence_ref")
	if err != nil {
		return Evidence{}, err
	}
	evidenceKind, err := safeEvidenceKind(input.EvidenceKind)
	if err != nil {
		return Evidence{}, err
	}
	sourceRef, err := safeOptionalRefIdentifier(input.SourceRef, "source_ref")
	if err != nil {
		return Evidence{}, err
	}
	summary, err := safeOptionalText(input.Summary, "summary", 500)
	if err != nil {
		return Evidence{}, err
	}
	evidence := Evidence{
		ID:           svc.newID("evidence"),
		ProjectID:    projectID,
		ClaimID:      claimID,
		EvidenceRef:  evidenceRef,
		EvidenceKind: evidenceKind,
		SourceRef:    sourceRef,
		Summary:      summary,
		CreatedAt:    svc.now(),
	}
	return svc.store.AppendEvidence(ctx, evidence)
}

func (svc *Service) CreateDecision(ctx context.Context, input CreateDecisionInput) (Decision, error) {
	projectID, claimID, record, err := svc.validateClaim(ctx, input.ProjectID, input.ClaimID)
	if err != nil {
		return Decision{}, err
	}
	if len(record.Evidence) == 0 {
		return Decision{}, fmt.Errorf("%w: evidence is required before decisions", ErrInvalidInput)
	}
	decisionRef, err := safeRefIdentifier(input.DecisionRef, "decision_ref")
	if err != nil {
		return Decision{}, err
	}
	state, err := safeDecisionState(input.State)
	if err != nil {
		return Decision{}, err
	}
	verifierRef, err := safeRefIdentifier(input.VerifierRef, "verifier_ref")
	if err != nil {
		return Decision{}, err
	}
	rationale, err := safeRequiredText(input.Rationale, "rationale", 500)
	if err != nil {
		return Decision{}, err
	}
	decision := Decision{
		ID:          svc.newID("decision"),
		ProjectID:   projectID,
		ClaimID:     claimID,
		DecisionRef: decisionRef,
		State:       state,
		VerifierRef: verifierRef,
		Rationale:   rationale,
		DecidedAt:   svc.now(),
	}
	return svc.store.CreateDecision(ctx, decision)
}

func (svc *Service) CreateAction(ctx context.Context, input CreateActionInput) (Action, error) {
	projectID, claimID, record, err := svc.validateClaim(ctx, input.ProjectID, input.ClaimID)
	if err != nil {
		return Action{}, err
	}
	decisionID, err := safeRefIdentifier(input.DecisionID, "decision_id")
	if err != nil {
		return Action{}, err
	}
	if !hasDecision(record, decisionID) {
		return Action{}, fmt.Errorf("%w: decision_id must match an existing decision", ErrInvalidInput)
	}
	actionRef, err := safeRefIdentifier(input.ActionRef, "action_ref")
	if err != nil {
		return Action{}, err
	}
	actionKind, err := safeActionKind(input.ActionKind)
	if err != nil {
		return Action{}, err
	}
	summary, err := safeOptionalText(input.Summary, "summary", 500)
	if err != nil {
		return Action{}, err
	}
	changedFiles, err := safeRelativePaths(input.ChangedFiles)
	if err != nil {
		return Action{}, err
	}
	runID, err := safeOptionalRefIdentifier(input.RunID, "run_id")
	if err != nil {
		return Action{}, err
	}
	action := Action{
		ID:           svc.newID("action"),
		ProjectID:    projectID,
		ClaimID:      claimID,
		DecisionID:   decisionID,
		ActionRef:    actionRef,
		ActionKind:   actionKind,
		Summary:      summary,
		ChangedFiles: changedFiles,
		RunID:        runID,
		CreatedAt:    svc.now(),
	}
	return svc.store.CreateAction(ctx, action)
}

func (svc *Service) CreateOutcome(ctx context.Context, input CreateOutcomeInput) (Outcome, error) {
	projectID, claimID, record, err := svc.validateClaim(ctx, input.ProjectID, input.ClaimID)
	if err != nil {
		return Outcome{}, err
	}
	actionID, err := safeRefIdentifier(input.ActionID, "action_id")
	if err != nil {
		return Outcome{}, err
	}
	if !hasAction(record, actionID) {
		return Outcome{}, fmt.Errorf("%w: action_id must match an existing action", ErrInvalidInput)
	}
	outcomeRef, err := safeRefIdentifier(input.OutcomeRef, "outcome_ref")
	if err != nil {
		return Outcome{}, err
	}
	outcomeKind, err := safeOutcomeKind(input.OutcomeKind)
	if err != nil {
		return Outcome{}, err
	}
	status, err := safeOutcomeStatus(input.Status)
	if err != nil {
		return Outcome{}, err
	}
	verifierRef, err := safeOptionalRefIdentifier(input.VerifierRef, "verifier_ref")
	if err != nil {
		return Outcome{}, err
	}
	summary, err := safeOptionalText(input.Summary, "summary", 500)
	if err != nil {
		return Outcome{}, err
	}
	outcome := Outcome{
		ID:          svc.newID("outcome"),
		ProjectID:   projectID,
		ClaimID:     claimID,
		ActionID:    actionID,
		OutcomeRef:  outcomeRef,
		OutcomeKind: outcomeKind,
		Status:      status,
		VerifierRef: verifierRef,
		Summary:     summary,
		CreatedAt:   svc.now(),
	}
	return svc.store.CreateOutcome(ctx, outcome)
}

func (svc *Service) LinkArtifact(ctx context.Context, input LinkArtifactInput) (ArtifactLink, error) {
	projectID, claimID, _, err := svc.validateClaim(ctx, input.ProjectID, input.ClaimID)
	if err != nil {
		return ArtifactLink{}, err
	}
	artifactRef, err := safeRefIdentifier(input.ArtifactRef, "artifact_ref")
	if err != nil {
		return ArtifactLink{}, err
	}
	artifactKind, err := safeOptionalIdentifier(input.ArtifactKind, "artifact_kind")
	if err != nil {
		return ArtifactLink{}, err
	}
	runID, err := safeOptionalRefIdentifier(input.RunID, "run_id")
	if err != nil {
		return ArtifactLink{}, err
	}
	link := ArtifactLink{ProjectID: projectID, ClaimID: claimID, ArtifactRef: artifactRef, ArtifactKind: artifactKind, RunID: runID}
	return svc.store.LinkArtifact(ctx, link)
}

func (svc *Service) LinkPromotion(ctx context.Context, input LinkPromotionInput) (PromotionLink, error) {
	projectID, claimID, record, err := svc.validateClaim(ctx, input.ProjectID, input.ClaimID)
	if err != nil {
		return PromotionLink{}, err
	}
	artifactRef, err := safeRefIdentifier(input.ArtifactRef, "artifact_ref")
	if err != nil {
		return PromotionLink{}, err
	}
	if !hasArtifactLink(record, artifactRef) {
		return PromotionLink{}, fmt.Errorf("%w: artifact_ref must match an existing artifact link", ErrInvalidInput)
	}
	state, err := safePromotionState(input.PromotionState)
	if err != nil {
		return PromotionLink{}, err
	}
	sourceRef, err := safeRefIdentifier(input.SourceRef, "source_ref")
	if err != nil {
		return PromotionLink{}, err
	}
	verifierRef, err := safeOptionalRefIdentifier(input.VerifierRef, "verifier_ref")
	if err != nil {
		return PromotionLink{}, err
	}
	decisionRef, err := safeOptionalRefIdentifier(input.DecisionRef, "decision_ref")
	if err != nil {
		return PromotionLink{}, err
	}
	runID, err := safeOptionalRefIdentifier(input.RunID, "run_id")
	if err != nil {
		return PromotionLink{}, err
	}
	actionRef, err := safeOptionalRefIdentifier(input.ActionRef, "action_ref")
	if err != nil {
		return PromotionLink{}, err
	}
	outcomeRef, err := safeOptionalRefIdentifier(input.OutcomeRef, "outcome_ref")
	if err != nil {
		return PromotionLink{}, err
	}
	if state != PromotionStateCandidate && verifierRef == "" {
		return PromotionLink{}, fmt.Errorf("%w: verifier_ref is required for promotion decisions", ErrInvalidInput)
	}
	if state != PromotionStateCandidate && decisionRef == "" {
		return PromotionLink{}, fmt.Errorf("%w: decision_ref is required for promotion decisions", ErrInvalidInput)
	}
	decision, hasDecisionRefMatch := findDecisionRef(record, decisionRef)
	if decisionRef != "" && !hasDecisionRefMatch {
		return PromotionLink{}, fmt.Errorf("%w: decision_ref must match an existing decision", ErrInvalidInput)
	}
	action, hasActionRefMatch := findActionRef(record, actionRef)
	if actionRef != "" && !hasActionRefMatch {
		return PromotionLink{}, fmt.Errorf("%w: action_ref must match an existing action", ErrInvalidInput)
	}
	if actionRef != "" && decisionRef != "" && action.DecisionID != decision.ID {
		return PromotionLink{}, fmt.Errorf("%w: action_ref must belong to decision_ref", ErrInvalidInput)
	}
	outcome, hasOutcomeRefMatch := findOutcomeRef(record, outcomeRef)
	if outcomeRef != "" && !hasOutcomeRefMatch {
		return PromotionLink{}, fmt.Errorf("%w: outcome_ref must match an existing outcome", ErrInvalidInput)
	}
	if outcomeRef != "" && actionRef != "" && outcome.ActionID != action.ID {
		return PromotionLink{}, fmt.Errorf("%w: outcome_ref must belong to action_ref", ErrInvalidInput)
	}
	if outcomeRef != "" && decisionRef != "" && actionRef == "" && !outcomeBelongsToDecision(record, outcome, decision.ID) {
		return PromotionLink{}, fmt.Errorf("%w: outcome_ref must belong to decision_ref", ErrInvalidInput)
	}
	if state == PromotionStatePromoted {
		if outcomeRef == "" {
			return PromotionLink{}, fmt.Errorf("%w: outcome_ref is required for promoted links", ErrInvalidInput)
		}
		if !hasPassedOutcomeRef(record, outcomeRef) {
			return PromotionLink{}, fmt.Errorf("%w: passed outcome_ref is required before promoted links", ErrInvalidInput)
		}
	}
	runID = promotionRunID(record, runID, actionRef, outcomeRef, artifactRef)
	link := PromotionLink{
		ProjectID:      projectID,
		ClaimID:        claimID,
		RunID:          runID,
		ArtifactRef:    artifactRef,
		PromotionState: state,
		SourceRef:      sourceRef,
		VerifierRef:    verifierRef,
		DecisionRef:    decisionRef,
		ActionRef:      actionRef,
		OutcomeRef:     outcomeRef,
		DecidedAt:      svc.now(),
	}
	return svc.store.LinkPromotion(ctx, link)
}

func (svc *Service) validateClaim(ctx context.Context, projectID string, claimID string) (string, string, ClaimRecord, error) {
	projectID, err := safeRefIdentifier(projectID, "project_id")
	if err != nil {
		return "", "", ClaimRecord{}, err
	}
	claimID, err = safeRefIdentifier(claimID, "claim_id")
	if err != nil {
		return "", "", ClaimRecord{}, err
	}
	record, err := svc.store.GetClaim(ctx, projectID, claimID)
	if err != nil {
		return "", "", ClaimRecord{}, err
	}
	return projectID, claimID, record, nil
}

func hasDecision(record ClaimRecord, decisionID string) bool {
	for _, decision := range record.Decisions {
		if decision.ID == decisionID {
			return true
		}
	}
	return false
}

func hasAction(record ClaimRecord, actionID string) bool {
	for _, action := range record.Actions {
		if action.ID == actionID {
			return true
		}
	}
	return false
}

func hasActionRef(record ClaimRecord, actionRef string) bool {
	_, ok := findActionRef(record, actionRef)
	return ok
}

func hasOutcomeRef(record ClaimRecord, outcomeRef string) bool {
	_, ok := findOutcomeRef(record, outcomeRef)
	return ok
}

func hasPassedOutcomeRef(record ClaimRecord, outcomeRef string) bool {
	for _, outcome := range record.Outcomes {
		if outcome.OutcomeRef == outcomeRef && outcome.Status == OutcomeStatusPassed {
			return true
		}
	}
	return false
}

func promotionRunID(record ClaimRecord, runID string, actionRef string, outcomeRef string, artifactRef string) string {
	if runID != "" {
		return runID
	}
	for _, action := range record.Actions {
		if actionRef != "" && action.ActionRef == actionRef && action.RunID != "" {
			return action.RunID
		}
	}
	for _, outcome := range record.Outcomes {
		if outcomeRef == "" || outcome.OutcomeRef != outcomeRef {
			continue
		}
		for _, action := range record.Actions {
			if action.ID == outcome.ActionID && action.RunID != "" {
				return action.RunID
			}
		}
	}
	for _, link := range record.ArtifactLinks {
		if link.ArtifactRef == artifactRef && link.RunID != "" {
			return link.RunID
		}
	}
	return record.Claim.RunID
}

func hasArtifactLink(record ClaimRecord, artifactRef string) bool {
	for _, link := range record.ArtifactLinks {
		if link.ArtifactRef == artifactRef {
			return true
		}
	}
	return false
}

func findDecisionRef(record ClaimRecord, decisionRef string) (Decision, bool) {
	if decisionRef == "" {
		return Decision{}, false
	}
	for _, decision := range record.Decisions {
		if decision.DecisionRef == decisionRef {
			return decision, true
		}
	}
	return Decision{}, false
}

func findActionRef(record ClaimRecord, actionRef string) (Action, bool) {
	if actionRef == "" {
		return Action{}, false
	}
	for _, action := range record.Actions {
		if action.ActionRef == actionRef {
			return action, true
		}
	}
	return Action{}, false
}

func findOutcomeRef(record ClaimRecord, outcomeRef string) (Outcome, bool) {
	if outcomeRef == "" {
		return Outcome{}, false
	}
	for _, outcome := range record.Outcomes {
		if outcome.OutcomeRef == outcomeRef {
			return outcome, true
		}
	}
	return Outcome{}, false
}

func outcomeBelongsToDecision(record ClaimRecord, outcome Outcome, decisionID string) bool {
	for _, action := range record.Actions {
		if action.ID == outcome.ActionID && action.DecisionID == decisionID {
			return true
		}
	}
	return false
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
	if len(value) > 200 || containsProhibitedMarker(value) || emailPattern.MatchString(value) {
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

func safeRelativePaths(paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if path == "" {
			continue
		}
		if len(path) > 300 ||
			strings.HasPrefix(path, "/") ||
			strings.Contains(path, "..") ||
			filepath.IsAbs(path) ||
			containsProhibitedData(path) ||
			containsURL(path) ||
			containsRootMarker(path) ||
			looksLikeSourceDump(path) ||
			!isSafeRelativePathToken(path) {
			return nil, fmt.Errorf("%w: changed file path is unsafe", ErrInvalidInput)
		}
		out = append(out, path)
	}
	return out, nil
}

func isSafeRelativePathToken(path string) bool {
	if strings.Contains(path, "//") {
		return false
	}
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}
		return false
	}
	return true
}

func safeClaimStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		return ClaimStatusCandidate, nil
	}
	switch status {
	case ClaimStatusCandidate, ClaimStatusValidated, ClaimStatusPromoted, ClaimStatusRejected:
		return status, nil
	default:
		return "", fmt.Errorf("%w: claim status is invalid", ErrInvalidInput)
	}
}

func safeEvidenceKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case EvidenceKindContextPack, EvidenceKindFile, EvidenceKindChunk, EvidenceKindSymbol, EvidenceKindVerifier, EvidenceKindClaimCheck, EvidenceKindArtifact, EvidenceKindOther:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: evidence_kind is invalid", ErrInvalidInput)
	}
}

func safeDecisionState(state string) (string, error) {
	state = strings.TrimSpace(state)
	switch state {
	case DecisionStateValidated, DecisionStatePromoted, DecisionStateRejected:
		return state, nil
	default:
		return "", fmt.Errorf("%w: decision state is invalid", ErrInvalidInput)
	}
}

func safeActionKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case ActionKindCodeChange, ActionKindDocChange, ActionKindVerifierRun, ActionKindConfigChange, ActionKindReviewComment, ActionKindOther:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: action_kind is invalid", ErrInvalidInput)
	}
}

func safeOutcomeKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	switch kind {
	case OutcomeKindTest, OutcomeKindBuild, OutcomeKindClaimCheck, OutcomeKindManualReview, OutcomeKindPromotion, OutcomeKindFailure, OutcomeKindOther:
		return kind, nil
	default:
		return "", fmt.Errorf("%w: outcome_kind is invalid", ErrInvalidInput)
	}
}

func safeOutcomeStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	switch status {
	case OutcomeStatusPassed, OutcomeStatusFailed, OutcomeStatusBlocked, OutcomeStatusUnknown:
		return status, nil
	default:
		return "", fmt.Errorf("%w: outcome status is invalid", ErrInvalidInput)
	}
}

func safeOptionalOutcomeStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		return "", nil
	}
	return safeOutcomeStatus(status)
}

func safePromotionState(state string) (string, error) {
	state = strings.TrimSpace(state)
	switch state {
	case PromotionStateCandidate, PromotionStateValidated, PromotionStatePromoted, PromotionStateRejected:
		return state, nil
	default:
		return "", fmt.Errorf("%w: promotion state is invalid", ErrInvalidInput)
	}
}

func safeOptionalPromotionState(state string) (string, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", nil
	}
	return safePromotionState(state)
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
	return strings.Count(value, "\n") > 4 ||
		strings.Contains(normalized, "package main") ||
		strings.Contains(normalized, "func ") ||
		strings.Contains(normalized, "-----begin") ||
		strings.Contains(normalized, "traceback (most recent call last)") ||
		strings.Contains(normalized, "```")
}

func containsRootMarker(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	return strings.Contains(normalized, "/home/") ||
		strings.Contains(normalized, "/users/") ||
		strings.Contains(normalized, "wsl.localhost/") ||
		strings.Contains(normalized, "c:/")
}

func containsURL(value string) bool {
	if !strings.Contains(value, "://") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return true
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate id: %w", err))
	}
	return prefix + "_" + letterEncode(b[:])
}

func letterEncode(values []byte) string {
	const alphabet = "abcdefghijklmnop"
	out := make([]byte, len(values)*2)
	for i, value := range values {
		out[i*2] = alphabet[value>>4]
		out[i*2+1] = alphabet[value&0x0f]
	}
	return string(out)
}
