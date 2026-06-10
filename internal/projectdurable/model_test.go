package projectdurable

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validRunRef() SafeAutomationRunRef {
	return SafeAutomationRunRef{
		ProjectID:    "project-1",
		AutomationID: "automation-1",
		RunID:        "run-0001",
		TaskID:       "task-0001",
		TraceID:      "trace-0001",
	}
}

func TestValidateSafeRefAccepted(t *testing.T) {
	for _, ref := range []string{
		"evidence:pilot-0001",
		"jira-context:PROJ-1044:summary",
		"objective:abcdef012345",
		"run:1234567890123", // long digit runs are allowed in refs (ids)
		"permission_snapshot:agent-1",
	} {
		if err := ValidateSafeRef(ref); err != nil {
			t.Fatalf("expected ref %q accepted, got %v", ref, err)
		}
	}
}

func TestValidateSafeRefRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
	}{
		{"empty", ""},
		{"oversized", strings.Repeat("a", 201)},
		{"control char", "evidence:\x01bad"},
		{"newline", "evidence:line1\nline2"},
		{"space", "context with spaces"},
		{"unicode nbsp", "evidence: hidden"},
		{"unicode en-space", "evidence: hidden"},
		{"root home", "/home/mac/repo"},
		{"root users", "/Users/pc/repo"},
		{"root windows", "C:\\repo"},
		{"root root", "/root/repo"},
		{"url http", "http://example.test"},
		{"url https", "https://example.test"},
		{"url ssh", "ssh://example.test"},
		{"raw prompt marker", "evidence:raw_prompt:1"},
		{"raw completion marker", "evidence:raw_completion:1"},
		{"raw stderr marker", "evidence:raw_stderr:1"},
		{"provider payload marker", "evidence:provider_payload:1"},
		{"api key marker", "evidence:api_key:1"},
		{"secret marker", "evidence:secret:1"},
		{"token marker", "evidence:token=abc"},
		{"password marker", "evidence:password:1"},
		{"email", "user@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSafeRef(tc.ref)
			if !errors.Is(err, ErrUnsafeMetadata) {
				t.Fatalf("expected ErrUnsafeMetadata for ref %q, got %v", tc.ref, err)
			}
		})
	}
}

func TestValidateSafeSummaryAccepted(t *testing.T) {
	for _, s := range []string{
		"",
		"jira issue intake",
		"verified task done after 3 attempts",
		"retry 12345678 scheduled", // 8 digits allowed
	} {
		if err := ValidateSafeSummary(s); err != nil {
			t.Fatalf("expected summary %q accepted, got %v", s, err)
		}
	}
}

func TestValidateSafeSummaryRejected(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary string
	}{
		{"oversized", strings.Repeat("a", 513)},
		{"control char", "line1\nline2"},
		{"root path", "wrote /home/mac/file"},
		{"url", "see https://example.test"},
		{"raw prompt marker", "stored raw_prompt for run"},
		{"api key marker", "api_key was rotated"},
		{"key block marker", "found BEGIN RSA private key"},
		{"openssh block marker", "found BEGIN OPENSSH private key"},
		{"email", "contact user@example.com"},
		{"long digit run", "call 123456789 now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSafeSummary(tc.summary)
			if !errors.Is(err, ErrUnsafeMetadata) {
				t.Fatalf("expected ErrUnsafeMetadata for summary %q, got %v", tc.summary, err)
			}
		})
	}
}

func TestDurableFailureCategoryValidate(t *testing.T) {
	for _, c := range []DurableFailureCategory{
		FailureCategoryNone,
		FailureCategoryTimeout,
		FailureCategoryRunnerUnavailable,
		FailureCategoryPolicyDenied,
		FailureCategoryExternalRunnerInterrupted,
		FailureCategoryVerificationRequired,
		FailureCategoryCodexAuthUnavailable,
		FailureCategoryCodexOutputSchemaInvalid,
		FailureCategoryCodexUsageLimitReached,
		FailureCategoryCodexConfigUnreadable,
	} {
		if err := c.Validate(); err != nil {
			t.Fatalf("expected category %q accepted, got %v", c, err)
		}
	}
	if err := DurableFailureCategory("made_up_category").Validate(); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("expected unknown category rejected, got %v", err)
	}
}

func TestSafeAutomationRunRefValidate(t *testing.T) {
	if err := validRunRef().Validate(); err != nil {
		t.Fatalf("expected valid run ref, got %v", err)
	}
	optional := validRunRef()
	optional.TaskID = ""
	optional.TraceID = ""
	if err := optional.Validate(); err != nil {
		t.Fatalf("expected optional ids allowed empty, got %v", err)
	}
	for _, mutate := range []func(*SafeAutomationRunRef){
		func(r *SafeAutomationRunRef) { r.ProjectID = "" },
		func(r *SafeAutomationRunRef) { r.AutomationID = "/home/mac/x" },
		func(r *SafeAutomationRunRef) { r.RunID = "run with spaces" },
		func(r *SafeAutomationRunRef) { r.TaskID = "https://example.test" },
		func(r *SafeAutomationRunRef) { r.TraceID = "trace:raw_prompt" },
	} {
		ref := validRunRef()
		mutate(&ref)
		if err := ref.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
			t.Fatalf("expected ErrUnsafeMetadata for %#v, got %v", ref, err)
		}
	}
}

func TestSafeWorkTaskRefValidate(t *testing.T) {
	ref := SafeWorkTaskRef{ProjectID: "project-1", PlanID: "plan-1", TaskID: "task-1", TaskRef: "task:fixture-1"}
	if err := ref.Validate(); err != nil {
		t.Fatalf("expected valid task ref, got %v", err)
	}
	ref.TaskRef = "ref with spaces"
	if err := ref.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("expected unsafe task_ref rejected, got %v", err)
	}
	ref = SafeWorkTaskRef{ProjectID: "project-1", PlanID: "", TaskID: "task-1"}
	if err := ref.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("expected empty plan_id rejected, got %v", err)
	}
}

func TestSafeWorkflowStageRefValidate(t *testing.T) {
	ref := SafeWorkflowStageRef{
		ProjectID:   "project-1",
		ChainRunID:  "chain-run-1",
		StageRef:    "decomposition",
		WorkflowRef: "workflow:pilot",
		StageIndex:  0,
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("expected valid stage ref, got %v", err)
	}
	ref.StageIndex = -1
	if err := ref.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("expected negative stage index rejected, got %v", err)
	}
	ref.StageIndex = 0
	ref.WorkflowRef = "api_key:leak"
	if err := ref.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
		t.Fatalf("expected unsafe workflow_ref rejected, got %v", err)
	}
}

func validSnapshot() DurableRunSnapshot {
	return DurableRunSnapshot{
		Run:                    validRunRef(),
		Status:                 "completed",
		FailureCategory:        FailureCategoryNone,
		SafeSummary:            "external runner verified task done",
		AttemptCount:           1,
		ClaimID:                "claim-1",
		RunnerID:               "runner-1",
		VerifierResultRefs:     []string{"verifier:pilot-0001"},
		ReviewResultRefs:       []string{"review:pilot-0001"},
		EvidenceRefs:           []string{"evidence:pilot-0001"},
		ClaimRefs:              []string{"claim:pilot-0001"},
		KnowledgeCandidateRefs: []string{"knowledge:pilot-0001"},
		ObservedAt:             time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
	}
}

func TestDurableRunSnapshotValidate(t *testing.T) {
	if err := validSnapshot().Validate(); err != nil {
		t.Fatalf("expected valid snapshot, got %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*DurableRunSnapshot)
	}{
		{"empty status", func(s *DurableRunSnapshot) { s.Status = "" }},
		{"unknown failure category", func(s *DurableRunSnapshot) { s.FailureCategory = "weird" }},
		{"unsafe summary", func(s *DurableRunSnapshot) { s.SafeSummary = "see https://example.test" }},
		{"negative attempts", func(s *DurableRunSnapshot) { s.AttemptCount = -1 }},
		{"unsafe claim id", func(s *DurableRunSnapshot) { s.ClaimID = "claim with spaces" }},
		{"unsafe runner id", func(s *DurableRunSnapshot) { s.RunnerID = "/home/mac/runner" }},
		{"unsafe evidence ref", func(s *DurableRunSnapshot) { s.EvidenceRefs = []string{"raw_prompt:1"} }},
		{"unsafe verifier ref", func(s *DurableRunSnapshot) { s.VerifierResultRefs = []string{"user@example.com"} }},
		{"unsafe run ref", func(s *DurableRunSnapshot) { s.Run.RunID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := validSnapshot()
			tc.mutate(&snapshot)
			if err := snapshot.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
				t.Fatalf("expected ErrUnsafeMetadata, got %v", err)
			}
		})
	}
}

func TestDurableActivityResultValidate(t *testing.T) {
	valid := DurableActivityResult{
		Activity:        "observe-run",
		Status:          ActivityStatusOK,
		FailureCategory: FailureCategoryNone,
		SafeSummary:     "run observed",
		Refs:            []string{"evidence:pilot-0001"},
		CompletedAt:     time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid activity result, got %v", err)
	}
	blocked := valid
	blocked.Status = ActivityStatusBlocked
	blocked.FailureCategory = FailureCategoryPolicyDenied
	if err := blocked.Validate(); err != nil {
		t.Fatalf("expected blocked activity result valid, got %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*DurableActivityResult)
	}{
		{"empty activity", func(r *DurableActivityResult) { r.Activity = "" }},
		{"unknown status", func(r *DurableActivityResult) { r.Status = "running" }},
		{"unknown failure category", func(r *DurableActivityResult) { r.FailureCategory = "weird" }},
		{"unsafe summary", func(r *DurableActivityResult) { r.SafeSummary = "token=abc leaked" }},
		{"unsafe ref", func(r *DurableActivityResult) { r.Refs = []string{"http://example.test"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := valid
			tc.mutate(&result)
			if err := result.Validate(); !errors.Is(err, ErrUnsafeMetadata) {
				t.Fatalf("expected ErrUnsafeMetadata, got %v", err)
			}
		})
	}
}
