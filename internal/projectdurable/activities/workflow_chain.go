package activities

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
)

const (
	ActivityResolveChainStages         = "resolve-chain-stages"
	ActivityCompileChainStage          = "compile-chain-stage"
	ActivityReleaseCompiledTasks       = "release-compiled-tasks"
	ActivityActivateStagePlan          = "activate-stage-plan"
	ActivityObserveStagePlan           = "observe-stage-plan"
	ActivityCarryForwardOutputs        = "carry-forward-outputs"
	ActivityObserveChainGitOps         = "observe-chain-gitops"
	ActivityWriteChainShadowComparison = "write-chain-shadow-comparison"
)

const (
	ChainTraceStatusRunning   = "running"
	ChainTraceStatusCompleted = "completed"
	ChainTraceStatusBlocked   = "blocked"
	ChainTraceStatusFailed    = "failed"
)

const (
	ChainFailureStageObservationTimeout = "stage-observation-timeout"
	ChainFailureStageBlocked            = "stage-blocked"
	ChainFailureStageFailed             = "stage-failed"
	ChainFailureActivityFailed          = "activity-failed"
)

type ChainShadowWorkflowInput struct {
	ProjectID                string `json:"project_id"`
	ChainRef                 string `json:"chain_ref"`
	InputRef                 string `json:"input_ref"`
	TraceID                  string `json:"trace_id,omitempty"`
	ShadowOnly               bool   `json:"shadow_only"`
	MaxStageObservationPolls int    `json:"max_stage_observation_polls,omitempty"`
}

type StageTraceEntry struct {
	StageRef        string            `json:"stage_ref"`
	WorkflowRef     string            `json:"workflow_ref"`
	PlanID          string            `json:"plan_id,omitempty"`
	TaskCount       int               `json:"task_count"`
	ReviewerCount   int               `json:"reviewer_count"`
	AutomationCount int               `json:"automation_count"`
	Status          string            `json:"status,omitempty"`
	TaskStatuses    map[string]string `json:"task_statuses,omitempty"`
	CarriedIDsCount int               `json:"carried_ids_count"`
}

type ChainGitOpsObservation struct {
	GitOpsReady    bool   `json:"gitops_ready"`
	PullRequestRef string `json:"pull_request_ref,omitempty"`
	RecoveryStatus string `json:"recovery_status,omitempty"`
	BlockedReason  string `json:"blocked_reason,omitempty"`
}

type ChainShadowTrace struct {
	Input           ChainShadowWorkflowInput               `json:"input"`
	Stages          []StageTraceEntry                      `json:"stages,omitempty"`
	Steps           []projectdurable.DurableActivityResult `json:"steps,omitempty"`
	FinalStatus     string                                 `json:"final_status"`
	GitOpsReady     bool                                   `json:"gitops_ready"`
	PullRequestRef  string                                 `json:"pull_request_ref,omitempty"`
	FailureCategory string                                 `json:"failure_category,omitempty"`
}

type StagePlanObservation struct {
	Status       string            `json:"status"`
	TaskStatuses map[string]string `json:"task_statuses,omitempty"`
}

type CarriedTaskIDs struct {
	TaskIDs []string `json:"task_ids,omitempty"`
}

type WorkflowChainActivities struct {
	Pipeline projectdurable.ChainPipelinePort
	Compare  projectdurable.ChainRunComparator
	Shadow   projectdurable.ShadowComparisonWriter
}

func (a *WorkflowChainActivities) ResolveChainStages(ctx context.Context, projectID, chainRef string) ([]projectdurable.SafeChainStagePlan, error) {
	if err := projectdurable.ValidateSafeRef(projectID); err != nil {
		return nil, fmt.Errorf("%w (field project_id)", err)
	}
	if err := projectdurable.ValidateSafeRef(chainRef); err != nil {
		return nil, fmt.Errorf("%w (field chain_ref)", err)
	}
	stages, err := a.Pipeline.ResolveChainStages(ctx, projectID, chainRef)
	if err != nil {
		return nil, fmt.Errorf("resolve chain stages failed")
	}
	if len(stages) == 0 {
		return nil, fmt.Errorf("resolve chain stages returned no stages")
	}
	for _, stage := range stages {
		if err := stage.Validate(); err != nil {
			return nil, err
		}
	}
	return stages, nil
}

func (a *WorkflowChainActivities) CompileStage(ctx context.Context, projectID, chainRef string, stage projectdurable.SafeChainStagePlan, inputRef string, carriedTaskIDs []string) (projectdurable.SafeStageCompileOutcome, error) {
	if err := projectdurable.ValidateSafeRef(projectID); err != nil {
		return projectdurable.SafeStageCompileOutcome{}, fmt.Errorf("%w (field project_id)", err)
	}
	if err := projectdurable.ValidateSafeRef(chainRef); err != nil {
		return projectdurable.SafeStageCompileOutcome{}, fmt.Errorf("%w (field chain_ref)", err)
	}
	if err := projectdurable.ValidateSafeRef(inputRef); err != nil {
		return projectdurable.SafeStageCompileOutcome{}, fmt.Errorf("%w (field input_ref)", err)
	}
	if err := stage.Validate(); err != nil {
		return projectdurable.SafeStageCompileOutcome{}, err
	}
	for _, taskID := range carriedTaskIDs {
		if err := projectdurable.ValidateSafeRef(taskID); err != nil {
			return projectdurable.SafeStageCompileOutcome{}, fmt.Errorf("%w (field carried_task_ids)", err)
		}
	}
	outcome, err := a.Pipeline.CompileStage(ctx, projectID, chainRef, stage, inputRef, carriedTaskIDs)
	if err != nil {
		return projectdurable.SafeStageCompileOutcome{}, fmt.Errorf("compile stage failed")
	}
	if err := outcome.Validate(); err != nil {
		return projectdurable.SafeStageCompileOutcome{}, err
	}
	return outcome, nil
}

func (a *WorkflowChainActivities) ReleaseCompiledTasks(ctx context.Context, planID string) (projectdurable.DurableActivityResult, error) {
	if err := projectdurable.ValidateSafeRef(planID); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (field plan_id)", err)
	}
	if err := a.Pipeline.ReleaseCompiledTasks(ctx, planID); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("release compiled tasks failed")
	}
	return newResult(ActivityReleaseCompiledTasks, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone, "compiled tasks released", []string{"plan-id:" + planID})
}

func (a *WorkflowChainActivities) ActivateStagePlan(ctx context.Context, planID string) (projectdurable.DurableActivityResult, error) {
	if err := projectdurable.ValidateSafeRef(planID); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (field plan_id)", err)
	}
	if err := a.Pipeline.ActivateWorkPlan(ctx, planID); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("activate stage plan failed")
	}
	return newResult(ActivityActivateStagePlan, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone, "stage plan activated", []string{"plan-id:" + planID})
}

func (a *WorkflowChainActivities) ObserveStagePlan(ctx context.Context, planID string) (StagePlanObservation, error) {
	if err := projectdurable.ValidateSafeRef(planID); err != nil {
		return StagePlanObservation{}, fmt.Errorf("%w (field plan_id)", err)
	}
	status, taskStatuses, err := a.Pipeline.ObserveStagePlanStatus(ctx, planID)
	if err != nil {
		return StagePlanObservation{}, fmt.Errorf("observe stage plan failed")
	}
	if err := projectdurable.ValidateSafeRef(status); err != nil {
		return StagePlanObservation{}, fmt.Errorf("%w (field status)", err)
	}
	keys := make([]string, 0, len(taskStatuses))
	for taskID := range taskStatuses {
		keys = append(keys, taskID)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(taskStatuses))
	for _, taskID := range keys {
		if err := projectdurable.ValidateSafeRef(taskID); err != nil {
			return StagePlanObservation{}, fmt.Errorf("%w (field task_statuses)", err)
		}
		if err := projectdurable.ValidateSafeRef(taskStatuses[taskID]); err != nil {
			return StagePlanObservation{}, fmt.Errorf("%w (field task_statuses)", err)
		}
		ordered[taskID] = taskStatuses[taskID]
	}
	return StagePlanObservation{Status: status, TaskStatuses: ordered}, nil
}

func (a *WorkflowChainActivities) CarryForwardOutputs(ctx context.Context, fromPlanID, toStageRef string) (CarriedTaskIDs, error) {
	if err := projectdurable.ValidateSafeRef(fromPlanID); err != nil {
		return CarriedTaskIDs{}, fmt.Errorf("%w (field from_plan_id)", err)
	}
	if err := projectdurable.ValidateSafeRef(toStageRef); err != nil {
		return CarriedTaskIDs{}, fmt.Errorf("%w (field to_stage_ref)", err)
	}
	taskIDs, err := a.Pipeline.CarryForwardOutputs(ctx, fromPlanID, toStageRef)
	if err != nil {
		return CarriedTaskIDs{}, fmt.Errorf("carry forward outputs failed")
	}
	for _, taskID := range taskIDs {
		if err := projectdurable.ValidateSafeRef(taskID); err != nil {
			return CarriedTaskIDs{}, fmt.Errorf("%w (field task_ids)", err)
		}
	}
	return CarriedTaskIDs{TaskIDs: append([]string(nil), taskIDs...)}, nil
}

func (a *WorkflowChainActivities) ObserveGitOps(ctx context.Context, chainRef string) (ChainGitOpsObservation, error) {
	if err := projectdurable.ValidateSafeRef(chainRef); err != nil {
		return ChainGitOpsObservation{}, fmt.Errorf("%w (field chain_ref)", err)
	}
	ready, prRef, recoveryStatus, blockedReason, err := a.Pipeline.ObserveGitOps(ctx, chainRef)
	if err != nil {
		return ChainGitOpsObservation{}, fmt.Errorf("observe gitops failed")
	}
	if err := projectdurable.ValidateSafeSummary(blockedReason); err != nil {
		return ChainGitOpsObservation{}, fmt.Errorf("%w (field blocked_reason)", err)
	}
	if err := projectdurable.ValidateSafeRef(prRef); prRef != "" && err != nil {
		return ChainGitOpsObservation{}, fmt.Errorf("%w (field pull_request_ref)", err)
	}
	if err := projectdurable.ValidateSafeRef(recoveryStatus); recoveryStatus != "" && err != nil {
		return ChainGitOpsObservation{}, fmt.Errorf("%w (field recovery_status)", err)
	}
	return ChainGitOpsObservation{GitOpsReady: ready, PullRequestRef: prRef, RecoveryStatus: recoveryStatus, BlockedReason: blockedReason}, nil
}

func (a *WorkflowChainActivities) WriteChainShadowComparison(ctx context.Context, input ChainShadowWorkflowInput, trace ChainShadowTrace) (projectdurable.DurableActivityResult, error) {
	runRef := projectdurable.SafeAutomationRunRef{
		ProjectID:    input.ProjectID,
		AutomationID: "workflow-chain-shadow",
		RunID:        input.ChainRef,
		TraceID:      input.TraceID,
	}
	if err := runRef.Validate(); err != nil {
		return projectdurable.DurableActivityResult{}, err
	}
	fields := flattenChainShadowTrace(trace)
	if a.Compare != nil {
		current, found, err := a.Compare.LoadCurrentChainRun(ctx, input.ProjectID, input.ChainRef)
		if err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("load current chain run failed")
		}
		fields["current_found"] = strconv.FormatBool(found)
		if found {
			for key, value := range current {
				fields["current_"+key] = value
			}
			fields["agree_final_status"] = strconv.FormatBool(current["final_status"] == trace.FinalStatus)
			fields["agree_gitops_ready"] = strconv.FormatBool(current["gitops_ready"] == strconv.FormatBool(trace.GitOpsReady))
			fields["agree_pull_request_ref"] = strconv.FormatBool(current["pull_request_ref"] == trace.PullRequestRef)
		}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := projectdurable.ValidateSafeRef(key); err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (chain shadow field key)", err)
		}
		if err := projectdurable.ValidateSafeSummary(fields[key]); err != nil {
			return projectdurable.DurableActivityResult{}, fmt.Errorf("%w (chain shadow field value)", err)
		}
	}
	if err := a.Shadow.WriteShadowComparison(ctx, runRef, fields); err != nil {
		return projectdurable.DurableActivityResult{}, fmt.Errorf("write chain shadow comparison failed")
	}
	return newResult(ActivityWriteChainShadowComparison, projectdurable.ActivityStatusOK, projectdurable.FailureCategoryNone, "chain shadow comparison written", []string{"shadow-fields:" + strconv.Itoa(len(fields))})
}

func flattenChainShadowTrace(trace ChainShadowTrace) map[string]string {
	fields := map[string]string{
		"final_status":     trace.FinalStatus,
		"failure_category": trace.FailureCategory,
		"gitops_ready":     strconv.FormatBool(trace.GitOpsReady),
		"pull_request_ref": trace.PullRequestRef,
		"shadow_only":      strconv.FormatBool(trace.Input.ShadowOnly),
		"stage_count":      strconv.Itoa(len(trace.Stages)),
		"step_count":       strconv.Itoa(len(trace.Steps)),
	}
	for i, stage := range trace.Stages {
		prefix := fmt.Sprintf("stage_%02d_", i)
		fields[prefix+"ref"] = stage.StageRef
		fields[prefix+"status"] = stage.Status
		fields[prefix+"plan_id"] = stage.PlanID
		fields[prefix+"task_count"] = strconv.Itoa(stage.TaskCount)
		fields[prefix+"carried_ids_count"] = strconv.Itoa(stage.CarriedIDsCount)
	}
	return fields
}
