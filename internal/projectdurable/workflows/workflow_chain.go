package workflows

import (
	"errors"
	"strconv"
	"time"

	"github.com/cschleiden/go-workflows/workflow"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
)

type ChainShadowWorkflowInput = activities.ChainShadowWorkflowInput
type ChainShadowTrace = activities.ChainShadowTrace
type StageTraceEntry = activities.StageTraceEntry

var ErrChainShadowOnlyRequired = errors.New("durable chain shadow workflow requires shadow_only=true")

const (
	defaultStageObservationPolls = 3
	stageObservationSleep        = time.Millisecond
)

// MiviaWorkflowChainShadowWorkflow is the Phase 5 multi-workflow chain
// shadow. It drives the current services only through activities/ports and
// records bounded metadata. Stage N+1 cannot compile until stage N reaches
// done. Comparison always runs last.
func MiviaWorkflowChainShadowWorkflow(ctx workflow.Context, input ChainShadowWorkflowInput) (ChainShadowTrace, error) {
	trace := ChainShadowTrace{Input: input, FinalStatus: activities.ChainTraceStatusRunning}
	if !input.ShadowOnly {
		return trace, ErrChainShadowOnlyRequired
	}
	for _, value := range []string{input.ProjectID, input.ChainRef, input.InputRef} {
		if err := projectdurable.ValidateSafeRef(value); err != nil {
			return trace, err
		}
	}
	if input.TraceID != "" {
		if err := projectdurable.ValidateSafeRef(input.TraceID); err != nil {
			return trace, err
		}
	}
	maxPolls := input.MaxStageObservationPolls
	if maxPolls <= 0 {
		maxPolls = defaultStageObservationPolls
	}

	var acts *activities.WorkflowChainActivities
	opts := workflow.DefaultActivityOptions

	appendStep := func(result projectdurable.DurableActivityResult) {
		trace.Steps = append(trace.Steps, result)
	}
	appendSynthetic := func(activity, status, summary string, refs []string) {
		appendStep(projectdurable.DurableActivityResult{
			Activity:    activity,
			Status:      status,
			SafeSummary: summary,
			Refs:        refs,
		})
	}
	block := func(category string) {
		trace.FinalStatus = activities.ChainTraceStatusBlocked
		trace.FailureCategory = category
	}

	stages, err := workflow.ExecuteActivity[[]projectdurable.SafeChainStagePlan](ctx, opts, acts.ResolveChainStages, input.ProjectID, input.ChainRef).Get(ctx)
	if err != nil {
		trace.FinalStatus = activities.ChainTraceStatusFailed
		trace.FailureCategory = activities.ChainFailureActivityFailed
		appendSynthetic(activities.ActivityResolveChainStages, projectdurable.ActivityStatusFailed, "resolve chain stages failed", nil)
		writeChainComparison(ctx, opts, acts, input, trace, appendStep, appendSynthetic)
		return trace, nil
	}
	appendSynthetic(activities.ActivityResolveChainStages, projectdurable.ActivityStatusOK, "", []string{"stage-count:" + strconv.Itoa(len(stages))})

	var carried []string
	var previousPlanID string
	for i, stage := range stages {
		outcome, compileErr := workflow.ExecuteActivity[projectdurable.SafeStageCompileOutcome](ctx, opts, acts.CompileStage, input.ProjectID, input.ChainRef, stage, input.InputRef, carried).Get(ctx)
		if compileErr != nil {
			trace.FinalStatus = activities.ChainTraceStatusFailed
			trace.FailureCategory = activities.ChainFailureActivityFailed
			appendSynthetic(activities.ActivityCompileChainStage, projectdurable.ActivityStatusFailed, "compile stage failed", []string{"stage:" + stage.StageRef})
			break
		}
		entry := activities.StageTraceEntry{
			StageRef:        stage.StageRef,
			WorkflowRef:     stage.WorkflowRef,
			PlanID:          outcome.PlanID,
			TaskCount:       len(outcome.TaskIDs),
			ReviewerCount:   len(outcome.ReviewerTaskIDs),
			AutomationCount: len(outcome.AutomationIDs),
			Status:          "compiled",
			CarriedIDsCount: len(carried),
		}
		trace.Stages = append(trace.Stages, entry)
		appendSynthetic(activities.ActivityCompileChainStage, projectdurable.ActivityStatusOK, "", []string{"stage:" + stage.StageRef, "plan-id:" + outcome.PlanID})

		released, releaseErr := workflow.ExecuteActivity[projectdurable.DurableActivityResult](ctx, opts, acts.ReleaseCompiledTasks, outcome.PlanID).Get(ctx)
		if releaseErr != nil {
			trace.FinalStatus = activities.ChainTraceStatusFailed
			trace.FailureCategory = activities.ChainFailureActivityFailed
			appendSynthetic(activities.ActivityReleaseCompiledTasks, projectdurable.ActivityStatusFailed, "release compiled tasks failed", []string{"plan-id:" + outcome.PlanID})
			break
		}
		appendStep(released)
		activated, activateErr := workflow.ExecuteActivity[projectdurable.DurableActivityResult](ctx, opts, acts.ActivateStagePlan, outcome.PlanID).Get(ctx)
		if activateErr != nil {
			trace.FinalStatus = activities.ChainTraceStatusFailed
			trace.FailureCategory = activities.ChainFailureActivityFailed
			appendSynthetic(activities.ActivityActivateStagePlan, projectdurable.ActivityStatusFailed, "activate stage plan failed", []string{"plan-id:" + outcome.PlanID})
			break
		}
		appendStep(activated)

		var observed activities.StagePlanObservation
		done := false
		for poll := 0; poll < maxPolls; poll++ {
			obs, observeErr := workflow.ExecuteActivity[activities.StagePlanObservation](ctx, opts, acts.ObserveStagePlan, outcome.PlanID).Get(ctx)
			if observeErr != nil {
				trace.FinalStatus = activities.ChainTraceStatusFailed
				trace.FailureCategory = activities.ChainFailureActivityFailed
				appendSynthetic(activities.ActivityObserveStagePlan, projectdurable.ActivityStatusFailed, "observe stage plan failed", []string{"plan-id:" + outcome.PlanID})
				done = true
				break
			}
			observed = obs
			trace.Stages[len(trace.Stages)-1].Status = obs.Status
			trace.Stages[len(trace.Stages)-1].TaskStatuses = obs.TaskStatuses
			appendSynthetic(activities.ActivityObserveStagePlan, projectdurable.ActivityStatusOK, "", []string{"plan-status:" + obs.Status})
			switch obs.Status {
			case "done":
				done = true
			case "blocked":
				block(activities.ChainFailureStageBlocked)
				done = true
			case "failed":
				block(activities.ChainFailureStageFailed)
				done = true
			}
			if done {
				break
			}
			workflow.Sleep(ctx, stageObservationSleep)
		}
		if trace.FinalStatus == activities.ChainTraceStatusFailed || trace.FinalStatus == activities.ChainTraceStatusBlocked {
			break
		}
		if !done || observed.Status != "done" {
			trace.Stages[len(trace.Stages)-1].Status = "blocked"
			block(activities.ChainFailureStageObservationTimeout)
			break
		}

		previousPlanID = outcome.PlanID
		if i < len(stages)-1 {
			next := stages[i+1]
			carriedResult, carryErr := workflow.ExecuteActivity[activities.CarriedTaskIDs](ctx, opts, acts.CarryForwardOutputs, previousPlanID, next.StageRef).Get(ctx)
			if carryErr != nil {
				trace.FinalStatus = activities.ChainTraceStatusFailed
				trace.FailureCategory = activities.ChainFailureActivityFailed
				appendSynthetic(activities.ActivityCarryForwardOutputs, projectdurable.ActivityStatusFailed, "carry forward outputs failed", []string{"stage:" + next.StageRef})
				break
			}
			carried = carriedResult.TaskIDs
			appendSynthetic(activities.ActivityCarryForwardOutputs, projectdurable.ActivityStatusOK, "", []string{"carried-count:" + strconv.Itoa(len(carried))})
		}
	}

	if trace.FinalStatus == activities.ChainTraceStatusRunning {
		gitops, gitopsErr := workflow.ExecuteActivity[activities.ChainGitOpsObservation](ctx, opts, acts.ObserveGitOps, input.ChainRef).Get(ctx)
		if gitopsErr != nil {
			trace.FinalStatus = activities.ChainTraceStatusFailed
			trace.FailureCategory = activities.ChainFailureActivityFailed
			appendSynthetic(activities.ActivityObserveChainGitOps, projectdurable.ActivityStatusFailed, "observe gitops failed", nil)
		} else {
			trace.GitOpsReady = gitops.GitOpsReady
			trace.PullRequestRef = gitops.PullRequestRef
			trace.FinalStatus = activities.ChainTraceStatusCompleted
			appendSynthetic(activities.ActivityObserveChainGitOps, projectdurable.ActivityStatusOK, gitops.BlockedReason, []string{"gitops-ready:" + strconv.FormatBool(gitops.GitOpsReady)})
		}
	}

	writeChainComparison(ctx, opts, acts, input, trace, appendStep, appendSynthetic)
	return trace, nil
}

func writeChainComparison(ctx workflow.Context, opts workflow.ActivityOptions, acts *activities.WorkflowChainActivities, input activities.ChainShadowWorkflowInput, trace activities.ChainShadowTrace, appendStep func(projectdurable.DurableActivityResult), appendSynthetic func(string, string, string, []string)) {
	result, err := workflow.ExecuteActivity[projectdurable.DurableActivityResult](ctx, opts, acts.WriteChainShadowComparison, input, trace).Get(ctx)
	if err != nil {
		appendSynthetic(activities.ActivityWriteChainShadowComparison, projectdurable.ActivityStatusFailed, "chain shadow comparison write failed", nil)
		return
	}
	appendStep(result)
}
