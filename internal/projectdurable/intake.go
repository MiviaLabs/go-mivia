package projectdurable

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidIntake marks rejected durable intake input. Error messages name
// the violated rule only - never the submitted objective text or any
// substring of it.
var ErrInvalidIntake = errors.New("invalid durable intake")

// IntakeKind selects the V2 governed intake route.
type IntakeKind string

const (
	// IntakeKindJiraIssueKey routes intake through a Jira issue key.
	IntakeKindJiraIssueKey IntakeKind = "jira_issue_key"
	// IntakeKindObjectiveText routes intake through free-form objective
	// text that is hashed into a deterministic safe ref and never persisted.
	IntakeKindObjectiveText IntakeKind = "objective_text"
)

// maxObjectiveBytes bounds objective text, matching the Phase 0B governed
// intake acceptance harness (8192-byte metadata bound).
const maxObjectiveBytes = 8192

var durableJiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]+-[0-9]+$`)

// DurableIntakeRequest is the V2 governed intake input. ObjectiveText is
// transient: it is consumed by NormalizeIntake to derive a deterministic
// safe ref and must NEVER be persisted, serialized, logged, or echoed in
// errors - hence the json:"-" tag. Everything else is metadata.
type DurableIntakeRequest struct {
	ProjectID string     `json:"project_id"`
	Kind      IntakeKind `json:"kind"`
	// TicketKey is the Jira issue key for IntakeKindJiraIssueKey, e.g.
	// "PROJ-1044" or "jira:PROJ-1044".
	TicketKey string `json:"ticket_key,omitempty"`
	// ObjectiveText is the raw objective for IntakeKindObjectiveText.
	// Excluded from JSON on purpose: raw prose must never reach durable
	// history, stores, logs, or traces.
	ObjectiveText string `json:"-"`
	// ObjectiveTitleHint is an optional bounded safe summary supplied by
	// the caller. It must not be derived from ObjectiveText content.
	ObjectiveTitleHint string `json:"objective_title_hint,omitempty"`
}

// DurableIntakeResult is the metadata-only outcome of governed intake.
type DurableIntakeResult struct {
	ProjectID   string     `json:"project_id"`
	Kind        IntakeKind `json:"kind"`
	InputRef    string     `json:"input_ref"`
	SafeSummary string     `json:"safe_summary"`
	ContextRefs []string   `json:"context_refs,omitempty"`
}

// NormalizeIntake validates a governed intake request and produces the
// deterministic metadata-only result. Jira mode accepts "PROJ-1044" or
// "jira:PROJ-1044" and rejects fake prefixes such as "ticket:PROJ-1044".
// Objective mode hashes a normalized form of the text into
// "objective:<12 hex>" and never copies any substring of the text into the
// result or into error messages. Objective fields set in jira mode (and the
// ticket key set in objective mode) are rejected to fail closed.
func NormalizeIntake(req DurableIntakeRequest) (DurableIntakeResult, error) {
	if err := ValidateSafeRef(req.ProjectID); err != nil {
		return DurableIntakeResult{}, fmt.Errorf("%w: project_id must be a safe ref", ErrInvalidIntake)
	}
	switch req.Kind {
	case IntakeKindJiraIssueKey:
		return normalizeJiraIntake(req)
	case IntakeKindObjectiveText:
		return normalizeObjectiveIntake(req)
	default:
		return DurableIntakeResult{}, fmt.Errorf("%w: kind must be jira_issue_key or objective_text", ErrInvalidIntake)
	}
}

func normalizeJiraIntake(req DurableIntakeRequest) (DurableIntakeResult, error) {
	// Fail closed: objective fields are not allowed on the jira route.
	if req.ObjectiveText != "" || req.ObjectiveTitleHint != "" {
		return DurableIntakeResult{}, fmt.Errorf("%w: objective fields must be empty in jira_issue_key mode", ErrInvalidIntake)
	}
	key := strings.TrimSpace(req.TicketKey)
	if key == "" {
		return DurableIntakeResult{}, fmt.Errorf("%w: ticket_key is required in jira_issue_key mode", ErrInvalidIntake)
	}
	// Only the canonical "jira:" prefix may be stripped; fake prefixes
	// such as "ticket:" fall through and fail the bare-key pattern.
	if strings.HasPrefix(key, "jira:") {
		key = strings.TrimSpace(strings.TrimPrefix(key, "jira:"))
	}
	if !durableJiraKeyPattern.MatchString(key) {
		return DurableIntakeResult{}, fmt.Errorf("%w: ticket_key must be a Jira issue key", ErrInvalidIntake)
	}
	return DurableIntakeResult{
		ProjectID:   req.ProjectID,
		Kind:        IntakeKindJiraIssueKey,
		InputRef:    "jira:" + key,
		SafeSummary: "jira issue intake",
	}, nil
}

func normalizeObjectiveIntake(req DurableIntakeRequest) (DurableIntakeResult, error) {
	// Fail closed: a ticket key is not allowed on the objective route.
	if strings.TrimSpace(req.TicketKey) != "" {
		return DurableIntakeResult{}, fmt.Errorf("%w: ticket_key must be empty in objective_text mode", ErrInvalidIntake)
	}
	text := req.ObjectiveText
	if strings.TrimSpace(text) == "" {
		return DurableIntakeResult{}, fmt.Errorf("%w: objective_text is required in objective_text mode", ErrInvalidIntake)
	}
	if len(text) > maxObjectiveBytes {
		return DurableIntakeResult{}, fmt.Errorf("%w: objective_text exceeds %d bytes", ErrInvalidIntake, maxObjectiveBytes)
	}
	if objectiveContainsSensitive(text) {
		return DurableIntakeResult{}, fmt.Errorf("%w: objective_text contains a sensitive marker", ErrInvalidIntake)
	}
	summary := "objective intake"
	if req.ObjectiveTitleHint != "" {
		if err := ValidateSafeSummary(req.ObjectiveTitleHint); err != nil {
			return DurableIntakeResult{}, fmt.Errorf("%w: objective_title_hint failed safe summary checks", ErrInvalidIntake)
		}
		summary = req.ObjectiveTitleHint
	}
	return DurableIntakeResult{
		ProjectID: req.ProjectID,
		Kind:      IntakeKindObjectiveText,
		InputRef:  deterministicObjectiveRef(req.ProjectID, text),
		// SafeSummary is the caller-provided hint or a fixed literal -
		// never derived from ObjectiveText content.
		SafeSummary: summary,
		// ContextRefs stay empty at this layer; later phases attach
		// validated refs.
	}, nil
}

// deterministicObjectiveRef derives "objective:<12 hex>" from the project id
// and a normalized form of the objective text (whitespace collapsed,
// lowercased), so equivalent submissions map to the same ref while the raw
// text itself is never stored.
func deterministicObjectiveRef(projectID string, text string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	sum := sha256.Sum256([]byte(projectID + "\x00" + normalized))
	return "objective:" + hex.EncodeToString(sum[:])[:12]
}

// objectiveContainsSensitive applies the same local unsafe-content checks
// used for refs and summaries to free-form objective text: sensitive
// markers, absolute roots, URL schemes, and email-like content. Long digit
// runs are not rejected here; the objective never persists, only its hash.
func objectiveContainsSensitive(text string) bool {
	if checkUnsafeContent(text, "objective_text") != nil {
		return true
	}
	return durableEmailPattern.MatchString(text)
}
