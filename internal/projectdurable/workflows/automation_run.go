// Package workflows hosts the deterministic go-workflows workflow code for
// the durable shadow pilot (Phase 3). Workflow code must stay deterministic:
// no time.Now, no randomness, no I/O, no goroutines, no map-iteration-order
// leaks. All side effects run inside activities.
//
// The shared DTOs (AutomationRunWorkflowInput, ShadowRunTrace) are defined in
// the activities package - the WriteShadowComparison activity consumes the
// trace, so workflows imports activities (the one clean direction, no import
// cycle) and re-exports the names via type aliases.
package workflows

import (
	"errors"

	"github.com/cschleiden/go-workflows/workflow"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
)

// AutomationRunWorkflowInput is the metadata-only workflow input. See the
// activities package for the definition.
type AutomationRunWorkflowInput = activities.AutomationRunWorkflowInput

// ShadowRunTrace is the bounded, safe workflow result. See the activities
// package for the definition.
type ShadowRunTrace = activities.ShadowRunTrace

// ErrShadowOnlyRequired rejects non-shadow execution: Phase 3 may observe
// only, never execute real runs.
var ErrShadowOnlyRequired = errors.New("durable shadow workflow requires shadow_only=true")

// shadowPlanIDPlaceholder fills SafeWorkTaskRef.PlanID for shadow-phase task
// observation. The workflow input carries no plan id; observers resolve the
// task by project and task ids, and this fixed safe literal satisfies the
// safe-ref contract without inventing data.
const shadowPlanIDPlaceholder = "shadow-observation"

// finalStatusUnknown is recorded when the snapshot could not be loaded, so
// FinalStatus never mirrors unverified state.
const finalStatusUnknown = "unknown"

// MiviaAutomationRunShadowWorkflow is the Phase 3 shadow workflow. It is
// observe-only and deterministic. Sequence: load snapshot -> validate
// executable -> observe work task (only when a task id is present) ->
// observe codex attempt -> observe governed closeout -> observe review queue
// -> observe verifier state -> write shadow comparison.
//
// Step semantics (documented decisions):
//   - A step that classifies BLOCKED does not short-circuit: shadow mode
//     keeps observing so the trace captures the full picture (the spec allows
//     but does not require skipping after blocked).
//   - A step that FAILS (activity error or unsafe metadata) short-circuits
//     the remaining observe steps, which are appended as skipped.
//   - WriteShadowComparison ALWAYS runs last, even after a failure.
//   - FinalStatus mirrors the snapshot run status ("unknown" when the load
//     failed); FailureCategory is the first non-none category any step set.
//   - Synthetic (workflow-built) skipped/failed step results carry a zero
//     CompletedAt: workflow code must not read the wall clock.
func MiviaAutomationRunShadowWorkflow(ctx workflow.Context, input AutomationRunWorkflowInput) (ShadowRunTrace, error) {
	trace := ShadowRunTrace{Input: input}

	// Fail closed before any port or activity is touched: this phase may not
	// execute real runs.
	if !input.ShadowOnly {
		return trace, ErrShadowOnlyRequired
	}

	runRef := projectdurable.SafeAutomationRunRef{
		ProjectID:    input.ProjectID,
		AutomationID: input.AutomationID,
		RunID:        input.RunID,
		TaskID:       input.TaskID,
		TraceID:      input.TraceID,
	}
	// Pure validation is deterministic and safe in workflow code.
	if err := runRef.Validate(); err != nil {
		return trace, err
	}

	// Nil-receiver method values resolve activity names without an instance;
	// the worker-registered struct supplies the real receiver (pattern from
	// go-workflows samples/activity-registration).
	var acts *activities.AutomationRunActivities
	opts := workflow.DefaultActivityOptions

	failed := false

	appendResult := func(result projectdurable.DurableActivityResult) {
		trace.Steps = append(trace.Steps, result)
		if trace.FailureCategory == projectdurable.FailureCategoryNone &&
			result.FailureCategory != projectdurable.FailureCategoryNone {
			trace.FailureCategory = result.FailureCategory
		}
	}

	appendSynthetic := func(activity string, status string, summary string) {
		appendResult(projectdurable.DurableActivityResult{
			Activity:    activity,
			Status:      status,
			SafeSummary: summary,
			// Zero CompletedAt by design: deterministic workflow code.
		})
	}

	// Step 1: load the snapshot.
	snap, loadErr := workflow.ExecuteActivity[projectdurable.DurableRunSnapshot](
		ctx, opts, acts.LoadAutomationRunSnapshot, runRef).Get(ctx)
	if loadErr != nil {
		// Generic summary only: activity error text must never reach history
		// through workflow-built metadata.
		appendSynthetic(activities.ActivityLoadRunSnapshot, projectdurable.ActivityStatusFailed, "snapshot load failed")
		trace.FinalStatus = finalStatusUnknown
		failed = true
	} else {
		appendResult(projectdurable.DurableActivityResult{
			Activity: activities.ActivityLoadRunSnapshot,
			Status:   projectdurable.ActivityStatusOK,
			Refs:     []string{"run-status:" + snap.Status},
		})
		trace.FinalStatus = snap.Status
	}

	runStep := func(activity string, call func() (projectdurable.DurableActivityResult, error)) {
		if failed {
			appendSynthetic(activity, projectdurable.ActivityStatusSkipped, "skipped after earlier failed step")
			return
		}
		result, err := call()
		if err != nil {
			appendSynthetic(activity, projectdurable.ActivityStatusFailed, "activity failed")
			failed = true
			return
		}
		appendResult(result)
		if result.Status == projectdurable.ActivityStatusFailed {
			failed = true
		}
	}

	// Step 2: pure classification of executability.
	runStep(activities.ActivityValidateExecutable, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, acts.ValidateAutomationRunExecutable, snap).Get(ctx)
	})

	// Step 3: observe the work task, only when a task id is present.
	if input.TaskID == "" {
		appendSynthetic(activities.ActivityObserveWorkTask, projectdurable.ActivityStatusSkipped, "no task id on workflow input")
	} else {
		taskRef := projectdurable.SafeWorkTaskRef{
			ProjectID: input.ProjectID,
			PlanID:    shadowPlanIDPlaceholder,
			TaskID:    input.TaskID,
		}
		runStep(activities.ActivityObserveWorkTask, func() (projectdurable.DurableActivityResult, error) {
			return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
				ctx, opts, acts.ObserveWorkTask, taskRef).Get(ctx)
		})
	}

	// Steps 4-7: snapshot-based observations.
	runStep(activities.ActivityObserveCodexAttempt, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, acts.ObserveCodexAttempt, snap).Get(ctx)
	})
	runStep(activities.ActivityObserveCloseout, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, acts.ObserveGovernedCloseout, snap).Get(ctx)
	})
	runStep(activities.ActivityObserveReviewQueue, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, acts.ObserveReviewQueue, snap).Get(ctx)
	})
	runStep(activities.ActivityObserveVerifierState, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, acts.ObserveVerifierState, snap).Get(ctx)
	})

	// Step 8: ALWAYS write the shadow comparison, even after a failure.
	shadowResult, shadowErr := workflow.ExecuteActivity[projectdurable.DurableActivityResult](
		ctx, opts, acts.WriteShadowComparison, runRef, trace).Get(ctx)
	if shadowErr != nil {
		appendSynthetic(activities.ActivityWriteShadow, projectdurable.ActivityStatusFailed, "shadow comparison write failed")
	} else {
		appendResult(shadowResult)
	}

	return trace, nil
}
