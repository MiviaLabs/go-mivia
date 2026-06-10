package projectdurable

import (
	"context"
	"sync"
	"time"
)

// Runner shadow boundaries are runner-side observations of the current
// authoritative claim/heartbeat/execute/report loop. They are metadata-only
// and must never drive current automation state.
const (
	RunnerShadowBoundaryClaimStarted     = "runner_claim_started"
	RunnerShadowBoundaryClaimed          = "runner_claimed"
	RunnerShadowBoundaryHeartbeatStarted = "runner_heartbeat_started"
	RunnerShadowBoundaryExecuteStarted   = "runner_execute_started"
	RunnerShadowBoundaryExecuteFinished  = "runner_execute_finished"
	RunnerShadowBoundaryCloseoutFinished = "runner_closeout_finished"
	RunnerShadowBoundaryReported         = "runner_reported"
)

// RunnerShadowEvent is the safe runner-side metadata recorded in shadow mode.
// It intentionally excludes prompts, completions, stderr, source, filesystem
// roots, provider payloads, secrets, external URLs, and PII.
type RunnerShadowEvent struct {
	Boundary        string               `json:"boundary"`
	ProjectID       string               `json:"project_id,omitempty"`
	Run             SafeAutomationRunRef `json:"run"`
	Status          string               `json:"status,omitempty"`
	FailureCategory string               `json:"failure_category,omitempty"`
	SafeSummary     string               `json:"safe_summary,omitempty"`
	AttemptCount    int                  `json:"attempt_count"`
	ClaimID         string               `json:"claim_id,omitempty"`
	RunnerID        string               `json:"runner_id,omitempty"`
	EvidenceRefs    []string             `json:"evidence_refs,omitempty"`
	ObservedAt      time.Time            `json:"observed_at"`
}

// Validate fails closed for all runner shadow metadata.
func (e RunnerShadowEvent) Validate() error {
	if err := ValidateSafeRef(e.Boundary); err != nil {
		return err
	}
	switch e.Boundary {
	case RunnerShadowBoundaryClaimStarted,
		RunnerShadowBoundaryClaimed,
		RunnerShadowBoundaryHeartbeatStarted,
		RunnerShadowBoundaryExecuteStarted,
		RunnerShadowBoundaryExecuteFinished,
		RunnerShadowBoundaryCloseoutFinished,
		RunnerShadowBoundaryReported:
	default:
		return ErrUnsafeMetadata
	}
	if e.ProjectID != "" {
		if err := ValidateSafeRef(e.ProjectID); err != nil {
			return err
		}
	}
	if e.Boundary == RunnerShadowBoundaryClaimStarted {
		if e.ProjectID == "" {
			return ErrUnsafeMetadata
		}
	} else {
		if err := e.Run.Validate(); err != nil {
			return err
		}
	}
	if e.Status != "" {
		if err := ValidateSafeRef(e.Status); err != nil {
			return err
		}
	}
	if e.FailureCategory != "" {
		if err := ValidateSafeRef(e.FailureCategory); err != nil {
			return err
		}
	}
	if err := ValidateSafeSummary(e.SafeSummary); err != nil {
		return err
	}
	if e.AttemptCount < 0 {
		return ErrUnsafeMetadata
	}
	if e.ClaimID != "" {
		if err := ValidateSafeRef(e.ClaimID); err != nil {
			return err
		}
	}
	if e.RunnerID != "" {
		if err := ValidateSafeRef(e.RunnerID); err != nil {
			return err
		}
	}
	for _, ref := range e.EvidenceRefs {
		if err := ValidateSafeRef(ref); err != nil {
			return err
		}
	}
	return nil
}

// RunnerShadowRecorder records runner shadow events. Implementations are
// optional while shadow mode is true; their failures must not affect the
// authoritative runner path.
type RunnerShadowRecorder interface {
	RecordRunnerShadowEvent(ctx context.Context, event RunnerShadowEvent) error
}

// InMemoryRunnerShadowRecorder is a local test/pilot recorder. It validates
// and stores copies of metadata-only events.
type InMemoryRunnerShadowRecorder struct {
	mu     sync.Mutex
	events []RunnerShadowEvent
}

func NewInMemoryRunnerShadowRecorder() *InMemoryRunnerShadowRecorder {
	return &InMemoryRunnerShadowRecorder{}
}

func (r *InMemoryRunnerShadowRecorder) RecordRunnerShadowEvent(ctx context.Context, event RunnerShadowEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := event.Validate(); err != nil {
		return err
	}
	event.EvidenceRefs = append([]string(nil), event.EvidenceRefs...)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *InMemoryRunnerShadowRecorder) Events() []RunnerShadowEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RunnerShadowEvent, len(r.events))
	for i, event := range r.events {
		event.EvidenceRefs = append([]string(nil), event.EvidenceRefs...)
		out[i] = event
	}
	return out
}
