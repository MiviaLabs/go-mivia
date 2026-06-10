package workflows

// This file hosts the Phase 4 TEST-ONLY durable execution workflow. No
// production path constructs this workflow: there is no configuration that
// enables durable execution, the only construction sites are tests, and the
// execution ports it depends on are implemented exclusively by test adapters
// over the current projectautomation service until cutover approval. It
// exists to prove the durable engine can drive the CURRENT service contract
// (claim -> complete -> review/verifier gates) deterministically.

import (
	"github.com/cschleiden/go-workflows/workflow"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
)

// TestExecutionWorkflowInput is the metadata-only workflow input. See the
// activities package for the definition.
type TestExecutionWorkflowInput = activities.TestExecutionWorkflowInput

// ExecutionTrace is the bounded, safe workflow result. See the activities
// package for the definition.
type ExecutionTrace = activities.ExecutionTrace

// MiviaAutomationRunTestExecutionWorkflow is the Phase 4 test-only execution
// workflow. It is deterministic and follows the same rules as the Phase 3
// shadow workflow: no wall clock, no randomness, no I/O in workflow code;
// all side effects live in activities.
//
// Sequence: load snapshot -> claim run (activity) -> complete attempt
// (activity) -> observe review queue -> observe verifier state -> write
// execution comparison.
//
// Step semantics (mirroring Phase 3):
//   - A step that classifies BLOCKED does not short-circuit: the trace keeps
//     observing so the full picture is captured.
//   - A step that FAILS (activity error or unsafe metadata) short-circuits
//     the remaining steps, which are appended as skipped.
//   - WriteExecutionComparison ALWAYS runs last, even after a failure.
//   - FinalStatus mirrors the most recent snapshot run status ("unknown"
//     when the load failed); FailureCategory is the first non-none category
//     any step produced.
//   - Synthetic (workflow-built) step results carry a zero CompletedAt:
//     workflow code must not read the wall clock.
//   - The attempt outcome is NOT pre-validated in workflow code beyond pure
//     ref checks on the run ref: the fail-closed unsafe-ref gate lives in
//     the CompleteRunAttempt activity, BEFORE the completion port is
//     invoked, so the guard is exercised end-to-end.
func MiviaAutomationRunTestExecutionWorkflow(ctx workflow.Context, input TestExecutionWorkflowInput) (ExecutionTrace, error) {
	trace := ExecutionTrace{Input: input}

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
	if err := projectdurable.ValidateSafeRef(input.RunnerID); err != nil {
		return trace, err
	}

	// Nil-receiver method values resolve activity names without an instance;
	// the worker-registered structs supply the real receivers (pattern from
	// go-workflows samples/activity-registration, same as Phase 3). The
	// observe methods are reused from the Phase 3 activities struct.
	var obs *activities.AutomationRunActivities
	var exec *activities.AutomationRunExecutionActivities
	opts := workflow.DefaultActivityOptions

	failed := false

	appendResult := func(result projectdurable.DurableActivityResult) {
		trace.Steps = append(trace.Steps, result)
		if trace.FailureCategory == projectdurable.FailureCategoryNone &&
			result.FailureCategory != projectdurable.FailureCategoryNone {
			trace.FailureCategory = result.FailureCategory
		}
	}

	appendSynthetic := func(activity string, status string, summary string, refs []string) {
		appendResult(projectdurable.DurableActivityResult{
			Activity:    activity,
			Status:      status,
			SafeSummary: summary,
			Refs:        refs,
			// Zero CompletedAt by design: deterministic workflow code.
		})
	}

	// Step 1: load the pre-execution snapshot.
	snap, loadErr := workflow.ExecuteActivity[projectdurable.DurableRunSnapshot](
		ctx, opts, obs.LoadAutomationRunSnapshot, runRef).Get(ctx)
	if loadErr != nil {
		// Generic summary only: activity error text must never reach history
		// through workflow-built metadata.
		appendSynthetic(activities.ActivityLoadRunSnapshot, projectdurable.ActivityStatusFailed, "snapshot load failed", nil)
		trace.FinalStatus = finalStatusUnknown
		failed = true
	} else {
		appendSynthetic(activities.ActivityLoadRunSnapshot, projectdurable.ActivityStatusOK, "",
			[]string{"run-status:" + snap.Status})
		trace.FinalStatus = snap.Status
	}

	// Step 2: claim the run for execution.
	if failed {
		appendSynthetic(activities.ActivityClaimRunForExecution, projectdurable.ActivityStatusSkipped, "skipped after earlier failed step", nil)
	} else {
		claimed, claimErr := workflow.ExecuteActivity[activities.ClaimedRunResult](
			ctx, opts, exec.ClaimRunForExecution, runRef, input.RunnerID).Get(ctx)
		if claimErr != nil {
			appendSynthetic(activities.ActivityClaimRunForExecution, projectdurable.ActivityStatusFailed, "claim failed", nil)
			failed = true
		} else {
			snap = claimed.Snapshot
			trace.FinalStatus = snap.Status
			appendSynthetic(activities.ActivityClaimRunForExecution, projectdurable.ActivityStatusOK, "",
				[]string{"run-status:" + snap.Status, "claim-id:" + claimed.ClaimID})
			// The completion report must echo the claim token and runner id
			// the claim step established.
			input.Outcome.ClaimID = claimed.ClaimID
			input.Outcome.RunnerID = input.RunnerID
		}
	}

	// Step 3: complete the attempt. The activity validates the outcome
	// fail-closed before the completion port is invoked.
	if failed {
		appendSynthetic(activities.ActivityCompleteRunAttempt, projectdurable.ActivityStatusSkipped, "skipped after earlier failed step", nil)
	} else {
		completedSnap, completeErr := workflow.ExecuteActivity[projectdurable.DurableRunSnapshot](
			ctx, opts, exec.CompleteRunAttempt, runRef, input.Outcome).Get(ctx)
		if completeErr != nil {
			appendSynthetic(activities.ActivityCompleteRunAttempt, projectdurable.ActivityStatusFailed, "complete attempt failed", nil)
			failed = true
		} else {
			snap = completedSnap
			trace.FinalStatus = snap.Status
			appendSynthetic(activities.ActivityCompleteRunAttempt, projectdurable.ActivityStatusOK, "",
				[]string{"run-status:" + snap.Status})
		}
	}

	runStep := func(activity string, call func() (projectdurable.DurableActivityResult, error)) {
		if failed {
			appendSynthetic(activity, projectdurable.ActivityStatusSkipped, "skipped after earlier failed step", nil)
			return
		}
		result, err := call()
		if err != nil {
			appendSynthetic(activity, projectdurable.ActivityStatusFailed, "activity failed", nil)
			failed = true
			return
		}
		appendResult(result)
		if result.Status == projectdurable.ActivityStatusFailed {
			failed = true
		}
	}

	// Steps 4-5: observe the post-completion review/verifier gates (reused
	// Phase 3 observation activities; blocked continues, failed skips).
	runStep(activities.ActivityObserveReviewQueue, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, obs.ObserveReviewQueue, snap).Get(ctx)
	})
	runStep(activities.ActivityObserveVerifierState, func() (projectdurable.DurableActivityResult, error) {
		return workflow.ExecuteActivity[projectdurable.DurableActivityResult](
			ctx, opts, obs.ObserveVerifierState, snap).Get(ctx)
	})

	// Step 6: ALWAYS write the execution comparison, even after a failure.
	comparisonResult, comparisonErr := workflow.ExecuteActivity[projectdurable.DurableActivityResult](
		ctx, opts, exec.WriteExecutionComparison, runRef, trace).Get(ctx)
	if comparisonErr != nil {
		appendSynthetic(activities.ActivityWriteExecutionComparison, projectdurable.ActivityStatusFailed, "execution comparison write failed", nil)
	} else {
		appendResult(comparisonResult)
	}

	return trace, nil
}
