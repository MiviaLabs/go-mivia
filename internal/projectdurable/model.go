package projectdurable

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// ErrUnsafeMetadata marks metadata that failed the local safe-ref or
// safe-summary checks. Error messages name the violated rule, never the
// offending content.
var ErrUnsafeMetadata = errors.New("unsafe durable metadata")

const (
	maxSafeRefLength     = 200
	maxSafeSummaryLength = 512
)

// DurableFailureCategory is a bounded, safe failure label recorded in durable
// history. Values mirror the safe categories the current automation system
// already emits (see internal/projectautomation: RunStatus* statuses used as
// failure categories in service.go and safeCodexFailureCategory in
// codexcli.go). Only the small set needed by the shadow phases is mirrored.
type DurableFailureCategory string

const (
	// FailureCategoryNone is the empty category for successful observations.
	FailureCategoryNone DurableFailureCategory = ""
	// FailureCategoryTimeout mirrors projectautomation.RunStatusTimeout.
	FailureCategoryTimeout DurableFailureCategory = "timeout"
	// FailureCategoryRunnerUnavailable mirrors projectautomation.RunStatusRunnerUnavailable.
	FailureCategoryRunnerUnavailable DurableFailureCategory = "runner_unavailable"
	// FailureCategoryPolicyDenied mirrors projectautomation.RunStatusPolicyDenied.
	FailureCategoryPolicyDenied DurableFailureCategory = "policy_denied"
	// FailureCategoryExternalRunnerInterrupted mirrors the
	// "external_runner_interrupted" category set by the automation service
	// for abandoned external runs.
	FailureCategoryExternalRunnerInterrupted DurableFailureCategory = "external_runner_interrupted"
	// FailureCategoryVerificationRequired mirrors the
	// "verification_required" category set when a run parks in verifying.
	FailureCategoryVerificationRequired DurableFailureCategory = "verification_required"
	// FailureCategoryCodexAuthUnavailable mirrors safeCodexFailureCategory.
	FailureCategoryCodexAuthUnavailable DurableFailureCategory = "codex_auth_unavailable"
	// FailureCategoryCodexOutputSchemaInvalid mirrors safeCodexFailureCategory.
	FailureCategoryCodexOutputSchemaInvalid DurableFailureCategory = "codex_output_schema_invalid"
	// FailureCategoryCodexUsageLimitReached mirrors safeCodexFailureCategory.
	FailureCategoryCodexUsageLimitReached DurableFailureCategory = "codex_usage_limit_reached"
	// FailureCategoryCodexConfigUnreadable mirrors safeCodexFailureCategory.
	FailureCategoryCodexConfigUnreadable DurableFailureCategory = "codex_config_unreadable"
)

// Validate fails closed: only the mirrored known categories (or none) are
// accepted into durable history.
func (c DurableFailureCategory) Validate() error {
	switch c {
	case FailureCategoryNone,
		FailureCategoryTimeout,
		FailureCategoryRunnerUnavailable,
		FailureCategoryPolicyDenied,
		FailureCategoryExternalRunnerInterrupted,
		FailureCategoryVerificationRequired,
		FailureCategoryCodexAuthUnavailable,
		FailureCategoryCodexOutputSchemaInvalid,
		FailureCategoryCodexUsageLimitReached,
		FailureCategoryCodexConfigUnreadable:
		return nil
	default:
		return fmt.Errorf("%w: failure category is not a known safe category", ErrUnsafeMetadata)
	}
}

// SafeAutomationRunRef identifies an automation run by ids only.
type SafeAutomationRunRef struct {
	ProjectID    string `json:"project_id"`
	AutomationID string `json:"automation_id"`
	RunID        string `json:"run_id"`
	TaskID       string `json:"task_id,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
}

// Validate checks every field with the local safe-ref rules. TaskID and
// TraceID are optional and validated only when set.
func (r SafeAutomationRunRef) Validate() error {
	if err := validateRequiredRef(r.ProjectID, "project_id"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.AutomationID, "automation_id"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.RunID, "run_id"); err != nil {
		return err
	}
	if err := validateOptionalRef(r.TaskID, "task_id"); err != nil {
		return err
	}
	return validateOptionalRef(r.TraceID, "trace_id")
}

// SafeWorkTaskRef identifies a work task by ids only.
type SafeWorkTaskRef struct {
	ProjectID string `json:"project_id"`
	PlanID    string `json:"plan_id"`
	TaskID    string `json:"task_id"`
	TaskRef   string `json:"task_ref,omitempty"`
}

// Validate checks every field with the local safe-ref rules. TaskRef is
// optional and validated only when set.
func (r SafeWorkTaskRef) Validate() error {
	if err := validateRequiredRef(r.ProjectID, "project_id"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.PlanID, "plan_id"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.TaskID, "task_id"); err != nil {
		return err
	}
	return validateOptionalRef(r.TaskRef, "task_ref")
}

// SafeWorkflowStageRef identifies one stage of a workflow chain run.
type SafeWorkflowStageRef struct {
	ProjectID   string `json:"project_id"`
	ChainRunID  string `json:"chain_run_id"`
	StageRef    string `json:"stage_ref"`
	WorkflowRef string `json:"workflow_ref"`
	StageIndex  int    `json:"stage_index"`
}

// Validate checks every field with the local safe-ref rules.
func (r SafeWorkflowStageRef) Validate() error {
	if err := validateRequiredRef(r.ProjectID, "project_id"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.ChainRunID, "chain_run_id"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.StageRef, "stage_ref"); err != nil {
		return err
	}
	if err := validateRequiredRef(r.WorkflowRef, "workflow_ref"); err != nil {
		return err
	}
	if r.StageIndex < 0 {
		return fmt.Errorf("%w: stage_index must not be negative", ErrUnsafeMetadata)
	}
	return nil
}

// DurableRunSnapshot is a metadata-only observation of an automation run.
// It carries ids, refs, statuses, counters, a bounded safe summary, and a
// timestamp - never prompts, completions, stderr, source, roots, or secrets.
type DurableRunSnapshot struct {
	Run                    SafeAutomationRunRef   `json:"run"`
	Status                 string                 `json:"status"`
	FailureCategory        DurableFailureCategory `json:"failure_category,omitempty"`
	SafeSummary            string                 `json:"safe_summary,omitempty"`
	AttemptCount           int                    `json:"attempt_count"`
	ClaimID                string                 `json:"claim_id,omitempty"`
	RunnerID               string                 `json:"runner_id,omitempty"`
	VerifierResultRefs     []string               `json:"verifier_result_refs,omitempty"`
	ReviewResultRefs       []string               `json:"review_result_refs,omitempty"`
	EvidenceRefs           []string               `json:"evidence_refs,omitempty"`
	ClaimRefs              []string               `json:"claim_refs,omitempty"`
	KnowledgeCandidateRefs []string               `json:"knowledge_candidate_refs,omitempty"`
	ObservedAt             time.Time              `json:"observed_at"`
}

// Validate applies the local safe-ref and safe-summary rules to every field.
func (s DurableRunSnapshot) Validate() error {
	if err := s.Run.Validate(); err != nil {
		return err
	}
	if err := validateRequiredRef(s.Status, "status"); err != nil {
		return err
	}
	if err := s.FailureCategory.Validate(); err != nil {
		return err
	}
	if err := ValidateSafeSummary(s.SafeSummary); err != nil {
		return fmt.Errorf("%w (field safe_summary)", err)
	}
	if s.AttemptCount < 0 {
		return fmt.Errorf("%w: attempt_count must not be negative", ErrUnsafeMetadata)
	}
	if err := validateOptionalRef(s.ClaimID, "claim_id"); err != nil {
		return err
	}
	if err := validateOptionalRef(s.RunnerID, "runner_id"); err != nil {
		return err
	}
	for name, refs := range map[string][]string{
		"verifier_result_refs":     s.VerifierResultRefs,
		"review_result_refs":       s.ReviewResultRefs,
		"evidence_refs":            s.EvidenceRefs,
		"claim_refs":               s.ClaimRefs,
		"knowledge_candidate_refs": s.KnowledgeCandidateRefs,
	} {
		if err := validateRefSlice(refs, name); err != nil {
			return err
		}
	}
	return nil
}

// Activity result statuses recorded by durable activities.
const (
	ActivityStatusOK      = "ok"
	ActivityStatusBlocked = "blocked"
	ActivityStatusFailed  = "failed"
	ActivityStatusSkipped = "skipped"
)

// DurableActivityResult is the metadata-only outcome of one durable activity.
type DurableActivityResult struct {
	Activity        string                 `json:"activity"`
	Status          string                 `json:"status"`
	FailureCategory DurableFailureCategory `json:"failure_category,omitempty"`
	SafeSummary     string                 `json:"safe_summary,omitempty"`
	Refs            []string               `json:"refs,omitempty"`
	CompletedAt     time.Time              `json:"completed_at"`
}

// Validate applies the local safe-ref and safe-summary rules to every field
// and restricts Status to the small activity status set.
func (r DurableActivityResult) Validate() error {
	if err := validateRequiredRef(r.Activity, "activity"); err != nil {
		return err
	}
	switch r.Status {
	case ActivityStatusOK, ActivityStatusBlocked, ActivityStatusFailed, ActivityStatusSkipped:
	default:
		return fmt.Errorf("%w: status must be ok, blocked, failed, or skipped", ErrUnsafeMetadata)
	}
	if err := r.FailureCategory.Validate(); err != nil {
		return err
	}
	if err := ValidateSafeSummary(r.SafeSummary); err != nil {
		return err
	}
	return validateRefSlice(r.Refs, "refs")
}

var (
	durableEmailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	// durableLongDigitRun rejects 9+ consecutive digits. Applied to
	// summaries only: refs legitimately embed long numeric ids and
	// timestamps, while a 9+ digit run in free-form summary text is more
	// likely a phone number or account number than a safe id.
	durableLongDigitRun = regexp.MustCompile(`[0-9]{9,}`)
)

// unsafeMarkers are sensitive substrings that must never appear in durable
// metadata. Compared case-insensitively. Local, intentionally small list;
// the big production sanitizer is not imported.
var unsafeMarkers = []string{
	"raw_prompt",
	"raw_completion",
	"raw_stderr",
	"provider_payload",
	"api_key",
	"secret",
	"token=",
	"password",
	"begin rsa",
	"begin openssh",
}

// unsafeRoots are absolute filesystem roots that must never leak into
// durable metadata.
var unsafeRoots = []string{"/home/", "/Users/", "C:\\", "/root/"}

// unsafeSchemes are URL schemes that must never leak into durable metadata.
var unsafeSchemes = []string{"http://", "https://", "ssh://"}

// ValidateSafeRef applies small local checks to a single ref: non-empty,
// at most 200 chars, no control characters or whitespace, no absolute
// roots, no URL schemes, no sensitive markers, no email-like content.
// Long digit runs are allowed in refs (ids embed long numbers); see
// ValidateSafeSummary for the digit-run rule.
func ValidateSafeRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: ref must not be empty", ErrUnsafeMetadata)
	}
	if len(ref) > maxSafeRefLength {
		return fmt.Errorf("%w: ref exceeds %d chars", ErrUnsafeMetadata, maxSafeRefLength)
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: ref contains control characters", ErrUnsafeMetadata)
		}
		if unicode.IsSpace(r) {
			return fmt.Errorf("%w: ref contains whitespace", ErrUnsafeMetadata)
		}
	}
	if err := checkUnsafeContent(ref, "ref"); err != nil {
		return err
	}
	if durableEmailPattern.MatchString(ref) {
		return fmt.Errorf("%w: ref looks like an email address", ErrUnsafeMetadata)
	}
	return nil
}

// ValidateSafeSummary applies small local checks to a bounded summary:
// empty is allowed, at most 512 chars, single line (no control characters),
// no absolute roots, no URL schemes, no sensitive markers, no email-like
// content, and no 9+ consecutive digits (phone-/account-number guard;
// documented trade-off: refs keep long ids, summaries do not).
func ValidateSafeSummary(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > maxSafeSummaryLength {
		return fmt.Errorf("%w: summary exceeds %d chars", ErrUnsafeMetadata, maxSafeSummaryLength)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: summary contains control characters", ErrUnsafeMetadata)
		}
	}
	if err := checkUnsafeContent(s, "summary"); err != nil {
		return err
	}
	if durableEmailPattern.MatchString(s) {
		return fmt.Errorf("%w: summary looks like an email address", ErrUnsafeMetadata)
	}
	if durableLongDigitRun.MatchString(s) {
		return fmt.Errorf("%w: summary contains a long digit run", ErrUnsafeMetadata)
	}
	return nil
}

func checkUnsafeContent(value string, kind string) error {
	lower := strings.ToLower(value)
	for _, marker := range unsafeMarkers {
		if strings.Contains(lower, marker) {
			return fmt.Errorf("%w: %s contains a sensitive marker", ErrUnsafeMetadata, kind)
		}
	}
	for _, root := range unsafeRoots {
		if strings.Contains(value, root) {
			return fmt.Errorf("%w: %s contains an absolute filesystem root", ErrUnsafeMetadata, kind)
		}
	}
	for _, scheme := range unsafeSchemes {
		if strings.Contains(lower, scheme) {
			return fmt.Errorf("%w: %s contains a URL scheme", ErrUnsafeMetadata, kind)
		}
	}
	return nil
}

func validateRequiredRef(ref string, name string) error {
	if err := ValidateSafeRef(ref); err != nil {
		return fmt.Errorf("%w (field %s)", err, name)
	}
	return nil
}

func validateOptionalRef(ref string, name string) error {
	if ref == "" {
		return nil
	}
	return validateRequiredRef(ref, name)
}

func validateRefSlice(refs []string, name string) error {
	for _, ref := range refs {
		if err := validateRequiredRef(ref, name); err != nil {
			return err
		}
	}
	return nil
}
