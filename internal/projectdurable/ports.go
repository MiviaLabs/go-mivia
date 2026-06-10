package projectdurable

import "context"

// Ports for the durable shadow phases (Phases 3-5). Durable activities call
// the CURRENT services only through these narrow observe-only interfaces.
// This file defines no implementations and imports no other internal
// packages: ports are expressed purely in terms of the metadata-only DTOs in
// this package, context, and plain strings, so the durable pilot stays
// decoupled. Adapters live beside the current services and arrive in later
// phases. Activities may only pass and receive metadata - never prompts,
// completions, stderr, source, roots, secrets, or PII.

// AutomationRunObserver loads a metadata-only snapshot of an automation run.
// Implementations live beside the projectautomation service; they must
// return only ids, refs, statuses, counters, and bounded safe summaries.
type AutomationRunObserver interface {
	LoadRunSnapshot(ctx context.Context, ref SafeAutomationRunRef) (DurableRunSnapshot, error)
}

// SafeTaskSnapshot is the metadata-only view of a work task that durable
// activities are allowed to observe. Refs groups ref slices by kind (for
// example "evidence_refs", "verifier_result_refs"); every key and every
// entry must pass ValidateSafeRef.
type SafeTaskSnapshot struct {
	Ref    SafeWorkTaskRef     `json:"ref"`
	Status string              `json:"status"`
	Refs   map[string][]string `json:"refs,omitempty"`
}

// WorkTaskObserver loads a metadata-only snapshot of a work task.
// Implementations live beside the projectworkplan service; they must return
// only ids, refs, and statuses.
type WorkTaskObserver interface {
	LoadTaskStatus(ctx context.Context, ref SafeWorkTaskRef) (SafeTaskSnapshot, error)
}

// SafeStageSnapshot is the metadata-only view of a workflow chain stage that
// durable activities are allowed to observe. BlockedReason and NextAction
// must pass ValidateSafeSummary.
type SafeStageSnapshot struct {
	Ref           SafeWorkflowStageRef `json:"ref"`
	Status        string               `json:"status"`
	BlockedReason string               `json:"blocked_reason,omitempty"`
	NextAction    string               `json:"next_action,omitempty"`
}

// ChainStageObserver loads a metadata-only snapshot of a workflow chain
// stage. Implementations live beside the projectworkflowchain service; they
// must return only ids, refs, statuses, and bounded safe summaries.
type ChainStageObserver interface {
	LoadStageStatus(ctx context.Context, ref SafeWorkflowStageRef) (SafeStageSnapshot, error)
}

// ShadowComparisonWriter records shadow-mode comparison fields for one
// automation run. Every value in fields must pass ValidateSafeSummary (and
// keys must pass ValidateSafeRef); implementations live beside the current
// services and must reject anything else fail-closed.
type ShadowComparisonWriter interface {
	WriteShadowComparison(ctx context.Context, runRef SafeAutomationRunRef, fields map[string]string) error
}
