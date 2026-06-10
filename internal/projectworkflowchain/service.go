package projectworkflowchain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectgitops"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

const maxChainGitOpsRecoveryAttempts = 3

type Store interface {
	CreateChainRun(context.Context, ChainRun) (ChainRun, error)
	GetChainRun(context.Context, string, string) (ChainRun, error)
	ListChainRuns(context.Context, ChainFilter) ([]ChainRun, error)
	UpdateChainRun(context.Context, ChainRun) (ChainRun, error)
	FindChainRunByWorkPlan(context.Context, string, string) (ChainRun, error)
}

type WorkflowAPI interface {
	ListWorkflows(context.Context, projectworkflow.WorkflowFilter) ([]projectworkflow.WorkflowDefinition, error)
	CompileWorkflow(context.Context, projectworkflow.WorkflowCompileInput) (projectworkflow.WorkflowCompileResult, error)
}

type WorkPlanAPI interface {
	GetWorkPlan(context.Context, string, string) (projectworkplan.WorkPlan, error)
	GetWorkTask(context.Context, string, string) (projectworkplan.WorkTask, error)
	ListWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)
	ListOpenWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)
	CreateWorkTask(context.Context, projectworkplan.CreateWorkTaskInput) (projectworkplan.WorkTask, error)
	UpdateWorkPlanStatus(context.Context, projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error)
	UpdateWorkTaskStatus(context.Context, projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error)
}

type AutomationAPI interface {
	CreateAutomation(context.Context, projectautomation.CreateAutomationInput) (projectautomation.Automation, error)
	ListAutomations(context.Context, projectautomation.AutomationFilter) ([]projectautomation.Automation, error)
}

type GitOpsFinalizer interface {
	FinalizeWorkflowChain(context.Context, GitOpsFinalizeInput) (GitOpsFinalizeResult, error)
}

type LocalContextReader interface {
	ReadLocalContent(context.Context, projectintegrations.LocalReadInput) (projectintegrations.RichContentReadResult, error)
}

type GitOpsFinalizeInput struct {
	ProjectID        string
	ChainRunID       string
	ChainRef         string
	InputRef         string
	WorkPlan         projectworkplan.WorkPlan
	StageRuns        []StageRun
	AutomationIDs    []string
	AllowedPathspecs []string
	ReviewRefs       []string
	VerifierRefs     []string
	TestResults      []string
	CreatedByRunID   string
	TraceID          string
}

type GitOpsFinalizeResult struct {
	CommitRef      string
	PushRef        string
	PullRequestRef string
	EvidenceRefs   []string
	NoChanges      bool
	Skipped        bool
}

type Service struct {
	store           Store
	workflows       WorkflowAPI
	workPlans       WorkPlanAPI
	automations     AutomationAPI
	gitOpsFinalizer GitOpsFinalizer
	localContexts   LocalContextReader
	configs         []Config
	now             func() time.Time
	newID           func(string) string
}

func New(store Store, workflows WorkflowAPI, workPlans WorkPlanAPI, configs []Config) *Service {
	return &Service{store: store, workflows: workflows, workPlans: workPlans, configs: cloneConfigs(configs), now: func() time.Time { return time.Now().UTC() }, newID: newID}
}

func (svc *Service) SetGitOpsFinalizer(finalizer GitOpsFinalizer) {
	svc.gitOpsFinalizer = finalizer
}

func (svc *Service) SetAutomationAPI(automations AutomationAPI) {
	svc.automations = automations
}

func (svc *Service) SetLocalContextReader(reader LocalContextReader) {
	svc.localContexts = reader
}

func (svc *Service) Start(ctx context.Context, input StartInput) (StartResult, error) {
	if svc.store == nil {
		return StartResult{}, fmt.Errorf("%w: store is required", ErrInvalidInput)
	}
	if svc.workflows == nil {
		return StartResult{}, fmt.Errorf("%w: workflow service is required", ErrInvalidInput)
	}
	projectID, err := safeRef(input.ProjectID, "project_id")
	if err != nil {
		return StartResult{}, err
	}
	chainRef, err := safeRef(input.ChainRef, "chain_ref")
	if err != nil {
		return StartResult{}, err
	}
	runID, err := safeOptionalRef(input.CreatedByRunID, "created_by_run_id")
	if err != nil {
		return StartResult{}, err
	}
	traceID, err := safeOptionalRef(input.TraceID, "trace_id")
	if err != nil {
		return StartResult{}, err
	}
	cfg, err := svc.config(projectID, chainRef)
	if err != nil {
		return StartResult{}, err
	}
	inputRef, err := normalizeInputRef(cfg, input.InputText)
	if err != nil {
		return StartResult{}, err
	}
	if err := svc.validateConfiguredWorkflows(ctx, cfg); err != nil {
		return StartResult{}, err
	}
	if !input.DryRun {
		if existing, ok, err := svc.findCorrelatedChainRun(ctx, cfg, inputRef, runID, traceID); err != nil {
			return StartResult{}, err
		} else if ok {
			return startResult(existing, false), nil
		}
	}
	contextRefs, err := svc.resolveContextRefs(ctx, cfg, inputRef)
	if err != nil {
		return StartResult{}, err
	}
	if input.DryRun {
		return svc.dryRunStart(ctx, cfg, inputRef, contextRefs, runID, traceID)
	}
	if svc.workPlans == nil {
		return StartResult{}, fmt.Errorf("%w: work plan service is required", ErrInvalidInput)
	}

	chainRunID := svc.newID("workflow_chain_run")
	now := svc.now()
	run := ChainRun{
		ID:             chainRunID,
		ProjectID:      cfg.ProjectID,
		ChainRef:       cfg.ChainRef,
		InputRef:       inputRef,
		Status:         ChainStatusPlanned,
		ContextRefs:    contextRefs,
		CreatedByRunID: runID,
		TraceID:        traceID,
		CreatedAt:      now,
		UpdatedAt:      now,
		NextAction:     "compile first stage and activate its Work Plan",
	}
	for _, stage := range cfg.Stages {
		run.StageRuns = append(run.StageRuns, StageRun{StageRef: stage.StageRef, WorkflowRef: stage.WorkflowRef, Status: StageStatusPlanned})
	}
	created, err := svc.store.CreateChainRun(ctx, run)
	if err != nil {
		return StartResult{}, err
	}
	run = created
	stage, compiled, err := svc.compileStageMetadata(ctx, cfg, run, cfg.Stages[0], false)
	if err != nil {
		run.Status = ChainStatusBlocked
		run.NextAction = "chain blocked while compiling first stage"
		if len(run.StageRuns) > 0 {
			run.StageRuns[0].Status = StageStatusBlocked
			run.StageRuns[0].BlockedReason = "compile_first_stage_failed"
		}
		run.UpdatedAt = svc.now()
		_, _ = svc.store.UpdateChainRun(ctx, run)
		return StartResult{}, err
	}
	run.StageRuns[0] = stage
	run.WorkPlanIDs = appendUnique(run.WorkPlanIDs, stage.WorkPlanID)
	run.AutomationIDs = appendUniqueMany(run.AutomationIDs, stage.AutomationIDs)
	run.Status = ChainStatusQueued
	run.NextAction = "decomposition automation will run when planned tasks transition to ready"
	run.UpdatedAt = svc.now()
	created, err = svc.store.UpdateChainRun(ctx, run)
	if err != nil {
		return StartResult{}, err
	}
	run = created
	if err := svc.activateCompiledStage(ctx, cfg, run, compiled); err != nil {
		run.Status = ChainStatusBlocked
		run.NextAction = "chain blocked while activating first stage"
		if len(run.StageRuns) > 0 {
			run.StageRuns[0].Status = StageStatusBlocked
			run.StageRuns[0].BlockedReason = "activate_first_stage_failed"
		}
		run.UpdatedAt = svc.now()
		_, _ = svc.store.UpdateChainRun(ctx, run)
		return StartResult{}, err
	}
	return startResult(created, false), nil
}

func (svc *Service) findCorrelatedChainRun(ctx context.Context, cfg Config, inputRef string, runID string, traceID string) (ChainRun, bool, error) {
	if strings.TrimSpace(runID) == "" && strings.TrimSpace(traceID) == "" {
		return ChainRun{}, false, nil
	}
	runs, err := svc.store.ListChainRuns(ctx, ChainFilter{ProjectID: cfg.ProjectID, ChainRef: cfg.ChainRef})
	if err != nil {
		return ChainRun{}, false, err
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	for _, run := range runs {
		if run.ProjectID != cfg.ProjectID || run.ChainRef != cfg.ChainRef || run.InputRef != inputRef {
			continue
		}
		if isRetryableTerminalChainStatus(run.Status) {
			continue
		}
		if strings.TrimSpace(runID) != "" && run.CreatedByRunID == runID {
			return run, true, nil
		}
		if strings.TrimSpace(traceID) != "" && run.TraceID == traceID {
			return run, true, nil
		}
	}
	return ChainRun{}, false, nil
}

func isRetryableTerminalChainStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case ChainStatusBlocked, ChainStatusFailed, ChainStatusCancelled, ChainStatusSuperseded:
		return true
	default:
		return false
	}
}

func (svc *Service) Get(ctx context.Context, projectID, chainRunID string) (ChainRun, error) {
	projectID, chainRunID, err := safeProjectObject(projectID, chainRunID, "chain_run_id")
	if err != nil {
		return ChainRun{}, err
	}
	return svc.store.GetChainRun(ctx, projectID, chainRunID)
}

func (svc *Service) RetryGitOps(ctx context.Context, projectID, chainRunID string) (ChainRun, error) {
	projectID, chainRunID, err := safeProjectObject(projectID, chainRunID, "chain_run_id")
	if err != nil {
		return ChainRun{}, err
	}
	run, err := svc.store.GetChainRun(ctx, projectID, chainRunID)
	if err != nil {
		return ChainRun{}, err
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || !allStagesCompleted(run) {
		return ChainRun{}, fmt.Errorf("%w: chain is not ready for GitOps retry", ErrInvalidInput)
	}
	if chainGitOpsRecoveryTerminal(run) {
		return ChainRun{}, fmt.Errorf("%w: chain GitOps recovery is terminal after %s", ErrInvalidInput, safeAutomationToken(run.GitOpsFailureCategory))
	}
	if err := svc.retryBlockedGitOps(ctx, run); err != nil {
		return ChainRun{}, err
	}
	return svc.store.GetChainRun(ctx, projectID, chainRunID)
}

func (svc *Service) List(ctx context.Context, filter ChainFilter) (ListResult, error) {
	projectID, err := safeOptionalRef(filter.ProjectID, "project_id")
	if err != nil {
		return ListResult{}, err
	}
	chainRef, err := safeOptionalRef(filter.ChainRef, "chain_ref")
	if err != nil {
		return ListResult{}, err
	}
	status, err := safeOptionalRef(filter.Status, "status")
	if err != nil {
		return ListResult{}, err
	}
	filter.ProjectID = projectID
	filter.ChainRef = chainRef
	filter.Status = status
	chains := make([]Config, 0)
	for _, cfg := range svc.configs {
		if projectID != "" && cfg.ProjectID != projectID {
			continue
		}
		if chainRef != "" && cfg.ChainRef != chainRef {
			continue
		}
		chains = append(chains, cfg)
	}
	sort.Slice(chains, func(i, j int) bool {
		if chains[i].ProjectID == chains[j].ProjectID {
			return chains[i].ChainRef < chains[j].ChainRef
		}
		return chains[i].ProjectID < chains[j].ProjectID
	})
	var runs []ChainRun
	if svc.store != nil {
		var err error
		runs, err = svc.store.ListChainRuns(ctx, filter)
		if err != nil {
			return ListResult{}, err
		}
	}
	return ListResult{Chains: chains, Runs: runs}, nil
}

func (svc *Service) HandleWorkPlanStatusChanged(ctx context.Context, change projectworkplan.WorkPlanStatusChange) error {
	if svc.store == nil {
		return nil
	}
	run, err := svc.store.FindChainRunByWorkPlan(ctx, change.ProjectID, change.PlanID)
	if err != nil {
		return nil
	}
	if change.NewStatus == projectworkplan.WorkPlanStatusBlocked ||
		change.NewStatus == projectworkplan.WorkPlanStatusFailed ||
		change.NewStatus == projectworkplan.WorkPlanStatusCancelled ||
		change.NewStatus == projectworkplan.WorkPlanStatusSuperseded {
		return svc.markChainRunTerminalFromWorkPlan(ctx, run, change)
	}
	if change.NewStatus != projectworkplan.WorkPlanStatusDone {
		return nil
	}
	if err := svc.ensureStagePlanCompletedSuccessfully(ctx, run, change); err != nil {
		return err
	}
	cfg, err := svc.config(run.ProjectID, run.ChainRef)
	if err != nil {
		return err
	}
	changed := false
	for i := range run.StageRuns {
		if run.StageRuns[i].WorkPlanID == change.PlanID && run.StageRuns[i].Status != StageStatusCompleted {
			run.StageRuns[i].Status = StageStatusCompleted
			run.StageRuns[i].CompletedAt = svc.now()
			changed = true
		}
	}
	if changed {
		run.UpdatedAt = svc.now()
	}
	if next, ok := nextReadyStage(cfg, run); ok {
		stageRun, compiled, err := svc.compileStageMetadata(ctx, cfg, run, next, false)
		if err != nil {
			run.Status = ChainStatusBlocked
			run.NextAction = "chain blocked while compiling next stage"
			for i := range run.StageRuns {
				if run.StageRuns[i].StageRef == next.StageRef {
					run.StageRuns[i].Status = StageStatusBlocked
					run.StageRuns[i].BlockedReason = "compile_next_stage_failed"
				}
			}
			_, _ = svc.store.UpdateChainRun(ctx, run)
			return err
		}
		for i := range run.StageRuns {
			if run.StageRuns[i].StageRef == next.StageRef {
				run.StageRuns[i] = stageRun
			}
		}
		run.WorkPlanIDs = appendUnique(run.WorkPlanIDs, stageRun.WorkPlanID)
		run.AutomationIDs = appendUniqueMany(run.AutomationIDs, stageRun.AutomationIDs)
		run.Status = ChainStatusQueued
		run.NextAction = "next stage automation will run when lifecycle gates are satisfied"
		run.UpdatedAt = svc.now()
		updated, err := svc.store.UpdateChainRun(ctx, run)
		if err != nil {
			return err
		}
		run = updated
		if err := svc.activateCompiledStage(ctx, cfg, run, compiled); err != nil {
			run.Status = ChainStatusBlocked
			run.NextAction = "chain blocked while activating next stage"
			for i := range run.StageRuns {
				if run.StageRuns[i].StageRef == next.StageRef {
					run.StageRuns[i].Status = StageStatusBlocked
					run.StageRuns[i].BlockedReason = "activate_next_stage_failed"
				}
			}
			run.UpdatedAt = svc.now()
			_, _ = svc.store.UpdateChainRun(ctx, run)
			return err
		}
		if refreshed, err := svc.store.GetChainRun(ctx, run.ProjectID, run.ID); err == nil {
			run = refreshed
		}
	} else if allStagesCompleted(run) && run.Status != ChainStatusBlocked && !run.GitOpsReady && strings.TrimSpace(run.PullRequestRef) == "" {
		if cfg.GitOpsMode == GitOpsModeDraftPRAfterValidation {
			run.Status = ChainStatusPostValidationPassed
			run.GitOpsReady = true
			run.NextAction = "chain ready for draft PR GitOps finalization"
			run.UpdatedAt = svc.now()
			updated, err := svc.store.UpdateChainRun(ctx, run)
			if err != nil {
				return err
			}
			run = updated
			if err := svc.finalizeGitOps(ctx, &run); err != nil {
				if _, updateErr := svc.recordGitOpsFinalizationFailure(ctx, run, err); updateErr != nil {
					return updateErr
				}
				return err
			}
		} else {
			run.Status = ChainStatusCompleted
			run.NextAction = "workflow chain completed"
		}
	}
	run.UpdatedAt = svc.now()
	_, err = svc.store.UpdateChainRun(ctx, run)
	return err
}

func (svc *Service) markChainRunTerminalFromWorkPlan(ctx context.Context, run ChainRun, change projectworkplan.WorkPlanStatusChange) error {
	status := ChainStatusBlocked
	stageStatus := StageStatusBlocked
	reason := "work_plan_blocked"
	nextAction := "workflow chain blocked by terminal Work Plan status"
	switch change.NewStatus {
	case projectworkplan.WorkPlanStatusFailed:
		status = ChainStatusFailed
		stageStatus = StageStatusFailed
		reason = "work_plan_failed"
		nextAction = "workflow chain failed by terminal Work Plan status"
	case projectworkplan.WorkPlanStatusCancelled:
		status = ChainStatusCancelled
		stageStatus = StageStatusCancelled
		reason = "work_plan_cancelled"
		nextAction = "workflow chain cancelled by terminal Work Plan status"
	case projectworkplan.WorkPlanStatusSuperseded:
		status = ChainStatusSuperseded
		stageStatus = StageStatusSuperseded
		reason = "work_plan_superseded"
		nextAction = "workflow chain superseded by terminal Work Plan status"
	}
	changed := run.Status != status || run.NextAction != nextAction
	for i := range run.StageRuns {
		if run.StageRuns[i].WorkPlanID != change.PlanID {
			continue
		}
		if run.StageRuns[i].Status != stageStatus || run.StageRuns[i].BlockedReason != reason {
			run.StageRuns[i].Status = stageStatus
			run.StageRuns[i].BlockedReason = reason
			run.StageRuns[i].CompletedAt = svc.now()
			changed = true
		}
	}
	if !changed {
		return nil
	}
	run.Status = status
	run.NextAction = nextAction
	run.UpdatedAt = svc.now()
	_, err := svc.store.UpdateChainRun(ctx, run)
	return err
}

func (svc *Service) retryBlockedGitOps(ctx context.Context, run ChainRun) error {
	if chainGitOpsRecoveryTerminal(run) {
		return fmt.Errorf("%w: chain GitOps recovery is terminal after %s", ErrInvalidInput, safeAutomationToken(run.GitOpsFailureCategory))
	}
	if err := svc.finalizeGitOps(ctx, &run); err != nil {
		if _, updateErr := svc.recordGitOpsFinalizationFailure(ctx, run, err); updateErr != nil {
			return updateErr
		}
		return err
	}
	run.UpdatedAt = svc.now()
	_, err := svc.store.UpdateChainRun(ctx, run)
	return err
}

func (svc *Service) finalizeGitOps(ctx context.Context, run *ChainRun) error {
	if run == nil {
		return fmt.Errorf("%w: chain run is required", ErrInvalidInput)
	}
	if svc.gitOpsFinalizer == nil {
		return fmt.Errorf("%w: gitops_finalizer_missing", ErrInvalidInput)
	}
	if strings.TrimSpace(run.PullRequestRef) != "" {
		run.Status = ChainStatusCompleted
		run.GitOpsReady = false
		run.GitOpsFailureCategory = ""
		run.GitOpsFailureEvidenceRefs = nil
		run.GitOpsRecoveryStatus = GitOpsRecoveryStatusCompleted
		run.NextAction = "workflow chain completed with draft PR GitOps output"
		return nil
	}
	if svc.workPlans == nil {
		return fmt.Errorf("%w: work plan service is required for GitOps finalization", ErrInvalidInput)
	}
	planID := gitOpsSourceWorkPlanID(*run)
	if planID == "" {
		return fmt.Errorf("%w: final stage Work Plan is required for GitOps finalization", ErrInvalidInput)
	}
	plan, err := svc.workPlans.GetWorkPlan(ctx, run.ProjectID, planID)
	if err != nil {
		return err
	}
	metadata, err := svc.gitOpsFinalizeMetadata(ctx, *run)
	if err != nil {
		return err
	}
	result, err := svc.gitOpsFinalizer.FinalizeWorkflowChain(ctx, GitOpsFinalizeInput{
		ProjectID:        run.ProjectID,
		ChainRunID:       run.ID,
		ChainRef:         run.ChainRef,
		InputRef:         run.InputRef,
		WorkPlan:         plan,
		StageRuns:        append([]StageRun(nil), run.StageRuns...),
		AutomationIDs:    append([]string(nil), run.AutomationIDs...),
		AllowedPathspecs: append([]string(nil), metadata.AllowedPathspecs...),
		ReviewRefs:       append([]string(nil), metadata.ReviewRefs...),
		VerifierRefs:     append([]string(nil), metadata.VerifierRefs...),
		TestResults:      append([]string(nil), metadata.TestResults...),
		CreatedByRunID:   run.CreatedByRunID,
		TraceID:          run.TraceID,
	})
	if err != nil {
		return err
	}
	if result.Skipped || result.NoChanges || strings.TrimSpace(result.PullRequestRef) == "" {
		return fmt.Errorf("%w: GitOps finalization did not create a draft PR", ErrInvalidInput)
	}
	if !chainActionablePullRequestRef(result.PullRequestRef) {
		return fmt.Errorf("%w: GitOps finalization returned non-actionable draft PR ref", ErrInvalidInput)
	}
	run.PullRequestRef = result.PullRequestRef
	run.Status = ChainStatusCompleted
	run.GitOpsReady = false
	run.GitOpsFailureCategory = ""
	run.GitOpsFailureEvidenceRefs = nil
	run.GitOpsRecoveryStatus = GitOpsRecoveryStatusCompleted
	run.NextAction = "workflow chain completed with draft PR GitOps output"
	return nil
}

func chainActionablePullRequestRef(ref string) bool {
	return regexp.MustCompile(`^github-pr-[0-9]+$`).MatchString(strings.TrimSpace(ref))
}

func (svc *Service) recordGitOpsFinalizationFailure(ctx context.Context, run ChainRun, err error) (ChainRun, error) {
	category := chainGitOpsFailureCategory(err)
	run.Status = ChainStatusBlocked
	run.GitOpsReady = true
	run.GitOpsAttemptCount++
	run.GitOpsFailureCategory = category
	run.GitOpsFailureEvidenceRefs = appendUniqueMany(run.GitOpsFailureEvidenceRefs, chainGitOpsFailureEvidenceRefs(err, category, run.GitOpsAttemptCount))
	if chainGitOpsFailureRepairable(category) && run.GitOpsAttemptCount < maxChainGitOpsRecoveryAttempts {
		run.GitOpsRecoveryStatus = GitOpsRecoveryStatusRepairable
		run.NextAction = "chain GitOps recovery is repairable; retry_gitops may resume draft PR finalization"
	} else {
		run.GitOpsRecoveryStatus = GitOpsRecoveryStatusTerminal
		run.NextAction = "chain GitOps recovery is terminal; fix the recorded category before starting a new recovery"
	}
	if len(run.StageRuns) > 0 {
		run.StageRuns[len(run.StageRuns)-1].BlockedReason = chainGitOpsBlockedReason(category, run.GitOpsRecoveryStatus, run.GitOpsAttemptCount)
	}
	run.UpdatedAt = svc.now()
	return svc.store.UpdateChainRun(ctx, run)
}

func (svc *Service) ensureStagePlanCompletedSuccessfully(ctx context.Context, run ChainRun, change projectworkplan.WorkPlanStatusChange) error {
	if svc == nil || svc.workPlans == nil {
		return nil
	}
	var stageIndex = -1
	for i := range run.StageRuns {
		if run.StageRuns[i].WorkPlanID == change.PlanID {
			stageIndex = i
			break
		}
	}
	if stageIndex < 0 {
		return nil
	}
	tasks, err := svc.workPlans.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: change.ProjectID, PlanID: change.PlanID})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		switch task.Status {
		case projectworkplan.WorkTaskStatusDone:
			continue
		case projectworkplan.WorkTaskStatusFailed, projectworkplan.WorkTaskStatusCancelled, projectworkplan.WorkTaskStatusSuperseded:
			reason := "stage_plan_done_with_unsuccessful_task_" + safeAutomationToken(task.Status)
			run.Status = ChainStatusBlocked
			run.NextAction = "chain blocked because completed stage plan contains unsuccessful task"
			run.StageRuns[stageIndex].Status = StageStatusBlocked
			run.StageRuns[stageIndex].BlockedReason = reason
			run.UpdatedAt = svc.now()
			_, _ = svc.store.UpdateChainRun(ctx, run)
			return fmt.Errorf("%w: %s", ErrInvalidInput, reason)
		}
	}
	return nil
}

func gitOpsBlockedReason(err error) string {
	if err == nil {
		return "gitops_finalize_failed"
	}
	reason := strings.TrimSpace(err.Error())
	if reason == "" {
		return "gitops_finalize_failed"
	}
	reason = strings.ToLower(reason)
	var b strings.Builder
	for _, r := range reason {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		case r == ':' || r == '/' || r == '\\' || r == ' ' || r == '\t' || r == '\n':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
		}
		if b.Len() >= 80 {
			break
		}
	}
	safe := strings.Trim(b.String(), "_-")
	if safe == "" {
		return "gitops_finalize_failed"
	}
	return "gitops_finalize_failed_" + safe
}

func chainGitOpsFailureCategory(err error) string {
	if err == nil {
		return "gitops_finalize_failed"
	}
	if errors.Is(err, ErrInvalidInput) {
		return gitOpsBlockedReason(err)
	}
	if projectgitops.FailureCategory(err) == "gitops_post_task_failed" {
		return gitOpsBlockedReason(err)
	}
	category := projectgitops.FailureCategoryWithDetail(err)
	if category == "" || category == "gitops_post_task_failed" {
		return gitOpsBlockedReason(err)
	}
	return safeGitOpsCategory(category)
}

func chainGitOpsBlockedReason(category string, recoveryStatus string, attemptCount int) string {
	category = safeGitOpsCategory(category)
	if category == "" {
		category = "gitops_finalize_failed"
	}
	status := safeAutomationToken(recoveryStatus)
	if status == "" {
		status = GitOpsRecoveryStatusRepairable
	}
	if attemptCount < 1 {
		attemptCount = 1
	}
	return fmt.Sprintf("%s_%s_attempt_%d", category, status, attemptCount)
}

func chainGitOpsFailureEvidenceRefs(err error, category string, attemptCount int) []string {
	category = safeGitOpsCategory(category)
	if category == "" {
		category = "gitops_finalize_failed"
	}
	refs := []string{
		"gitops-failure:" + category,
		fmt.Sprintf("gitops-attempt:%d", attemptCount),
	}
	if dirtyPaths := projectgitops.DirtyWorktreeScopePaths(err); len(dirtyPaths) > 0 {
		refs = append(refs, "gitops-dirty-scope:"+safeShortHash(strings.Join(dirtyPaths, "\n")))
	}
	return refs
}

func chainGitOpsFailureRepairable(category string) bool {
	category = strings.TrimSpace(category)
	switch {
	case category == "gitops_dirty_worktree", category == "gitops_dirty_worktree_scope":
		return true
	case strings.HasPrefix(category, "gitops_verification_failed"):
		return true
	case strings.HasPrefix(category, "gitops_downstream_checks_failed"):
		return true
	case strings.HasPrefix(category, "gitops_finalize_failed_") && !strings.Contains(category, "invalid_project_workflow_chain_input"):
		return true
	default:
		return false
	}
}

func chainGitOpsRecoveryTerminal(run ChainRun) bool {
	return run.GitOpsRecoveryStatus == GitOpsRecoveryStatusTerminal ||
		(run.GitOpsAttemptCount >= maxChainGitOpsRecoveryAttempts && run.GitOpsRecoveryStatus != GitOpsRecoveryStatusCompleted)
}

func safeShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func safeGitOpsCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ':' || r == '/' || r == '\\' || r == ' ' || r == '\t' || r == '\n':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
		}
	}
	return strings.Trim(b.String(), "_-.")
}

type gitOpsFinalizeMetadata struct {
	AllowedPathspecs []string
	ReviewRefs       []string
	VerifierRefs     []string
	TestResults      []string
}

func (svc *Service) gitOpsFinalizeMetadata(ctx context.Context, run ChainRun) (gitOpsFinalizeMetadata, error) {
	if svc == nil || svc.workPlans == nil {
		return gitOpsFinalizeMetadata{}, fmt.Errorf("%w: work plan service is required for GitOps finalization metadata", ErrInvalidInput)
	}
	var metadata gitOpsFinalizeMetadata
	var postValidationReviewRefs []string
	var postValidationVerifierRefs []string
	for _, stage := range run.StageRuns {
		for _, taskID := range stage.WorkTaskIDs {
			task, err := svc.workPlans.GetWorkTask(ctx, run.ProjectID, taskID)
			if err != nil {
				return gitOpsFinalizeMetadata{}, err
			}
			if stage.StageRef == "implementation" {
				metadata.AllowedPathspecs = appendUniqueMany(metadata.AllowedPathspecs, task.FilesToEdit)
			}
			metadata.ReviewRefs = appendUniqueMany(metadata.ReviewRefs, task.ReviewResultRefs)
			metadata.VerifierRefs = appendUniqueMany(metadata.VerifierRefs, task.VerifierResultRefs)
			if stage.StageRef == "post-validation" {
				postValidationReviewRefs = appendUniqueMany(postValidationReviewRefs, task.ReviewResultRefs)
				postValidationVerifierRefs = appendUniqueMany(postValidationVerifierRefs, task.VerifierResultRefs)
			}
			for _, ref := range task.VerifierResultRefs {
				metadata.TestResults = appendUnique(metadata.TestResults, task.TaskRef+" verified by "+ref)
			}
		}
	}
	if len(metadata.AllowedPathspecs) == 0 {
		return gitOpsFinalizeMetadata{}, fmt.Errorf("%w: workflow chain GitOps finalization requires implementation task pathspecs", ErrInvalidInput)
	}
	if len(metadata.ReviewRefs) == 0 {
		return gitOpsFinalizeMetadata{}, fmt.Errorf("%w: workflow chain GitOps finalization requires review result refs", ErrInvalidInput)
	}
	if len(metadata.VerifierRefs) == 0 {
		return gitOpsFinalizeMetadata{}, fmt.Errorf("%w: workflow chain GitOps finalization requires verifier result refs", ErrInvalidInput)
	}
	if len(postValidationReviewRefs) == 0 {
		return gitOpsFinalizeMetadata{}, fmt.Errorf("%w: workflow chain GitOps finalization requires post-validation review result refs", ErrInvalidInput)
	}
	if len(postValidationVerifierRefs) == 0 {
		return gitOpsFinalizeMetadata{}, fmt.Errorf("%w: workflow chain GitOps finalization requires post-validation verifier result refs", ErrInvalidInput)
	}
	return metadata, nil
}

func gitOpsSourceWorkPlanID(run ChainRun) string {
	for i := len(run.StageRuns) - 1; i >= 0; i-- {
		if run.StageRuns[i].StageRef == "implementation" && strings.TrimSpace(run.StageRuns[i].WorkPlanID) != "" {
			return run.StageRuns[i].WorkPlanID
		}
	}
	for i := len(run.StageRuns) - 1; i >= 0; i-- {
		if strings.TrimSpace(run.StageRuns[i].WorkPlanID) != "" {
			return run.StageRuns[i].WorkPlanID
		}
	}
	if len(run.WorkPlanIDs) == 0 {
		return ""
	}
	return run.WorkPlanIDs[len(run.WorkPlanIDs)-1]
}

func (svc *Service) dryRunStart(ctx context.Context, cfg Config, inputRef string, contextRefs []string, runID string, traceID string) (StartResult, error) {
	result := StartResult{ProjectID: cfg.ProjectID, ChainRef: cfg.ChainRef, InputRef: inputRef, Status: ChainStatusPlanned, ContextRefs: append([]string(nil), contextRefs...), DryRun: true, NextAction: "dry run only; no Work Plans or automations created"}
	for _, stage := range cfg.Stages {
		stageRun, err := svc.compileStage(ctx, cfg, ChainRun{InputRef: inputRef, ContextRefs: contextRefs, CreatedByRunID: runID, TraceID: traceID}, stage, true)
		if err != nil {
			return StartResult{}, err
		}
		result.StageRuns = append(result.StageRuns, stageRun)
		result.WorkPlanIDs = appendUnique(result.WorkPlanIDs, stageRun.WorkPlanID)
		result.AutomationIDs = appendUniqueMany(result.AutomationIDs, stageRun.AutomationIDs)
	}
	return result, nil
}

// ResolveStageConfigsForShadow returns the enabled stage configuration for a
// durable shadow adapter. It deliberately exposes metadata only; callers still
// drive compile/activate through the narrow exported methods below.
func (svc *Service) ResolveStageConfigsForShadow(ctx context.Context, projectID, chainRef string) ([]StageConfig, error) {
	projectID, err := safeRef(projectID, "project_id")
	if err != nil {
		return nil, err
	}
	chainRef, err = safeRef(chainRef, "chain_ref")
	if err != nil {
		return nil, err
	}
	cfg, err := svc.config(projectID, chainRef)
	if err != nil {
		return nil, err
	}
	if err := svc.validateConfiguredWorkflows(ctx, cfg); err != nil {
		return nil, err
	}
	return append([]StageConfig(nil), cfg.Stages...), nil
}

// CompileStageMetadataForShadow exposes the current compileStageMetadata
// helper for the durable workflow-chain shadow adapter. It is compile only:
// no task release, plan activation, or GitOps finalization happens here.
func (svc *Service) CompileStageMetadataForShadow(ctx context.Context, run ChainRun, stage StageConfig) (StageRun, projectworkflow.WorkflowCompileResult, error) {
	cfg, err := svc.config(run.ProjectID, run.ChainRef)
	if err != nil {
		return StageRun{}, projectworkflow.WorkflowCompileResult{}, err
	}
	stage, err = configuredShadowStage(cfg, stage)
	if err != nil {
		return StageRun{}, projectworkflow.WorkflowCompileResult{}, err
	}
	return svc.compileStageMetadata(ctx, cfg, run, stage, false)
}

// CarryForwardStageOutputTasksForShadow exposes the current carry-forward
// rules to the durable shadow adapter so generated task handoffs are converted
// through the same concrete Work Task ID path as the current service.
func (svc *Service) CarryForwardStageOutputTasksForShadow(ctx context.Context, projectID string, run ChainRun, compiled projectworkflow.WorkflowCompileResult) error {
	cfg, err := svc.shadowConfigForProjectRun(projectID, run)
	if err != nil {
		return err
	}
	return svc.carryForwardStageOutputTasks(ctx, cfg.ProjectID, run, compiled)
}

// ReleaseCompiledTasksForShadow exposes the current release helper for the
// durable shadow adapter. Callers are responsible for invoking it before
// ActivateCompiledWorkPlanForShadow to preserve the current event order.
func (svc *Service) ReleaseCompiledTasksForShadow(ctx context.Context, projectID string, compiled projectworkflow.WorkflowCompileResult, run ChainRun) error {
	cfg, err := svc.shadowConfigForProjectRun(projectID, run)
	if err != nil {
		return err
	}
	return svc.releaseCompiledTasks(ctx, cfg.ProjectID, compiled, run)
}

// ActivateCompiledWorkPlanForShadow exposes only the Work Plan activation
// part of activateCompiledStage for the durable shadow adapter. Carry-forward
// and task release remain separate exported calls so the durable trace records
// the same step order explicitly.
func (svc *Service) ActivateCompiledWorkPlanForShadow(ctx context.Context, run ChainRun, compiled projectworkflow.WorkflowCompileResult) error {
	cfg, err := svc.config(run.ProjectID, run.ChainRef)
	if err != nil {
		return err
	}
	if compiled.WorkPlanID == "" {
		return nil
	}
	if svc.workPlans == nil {
		return fmt.Errorf("%w: work plan service is required", ErrInvalidInput)
	}
	_, err = svc.workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      cfg.ProjectID,
		PlanID:         compiled.WorkPlanID,
		Status:         projectworkplan.WorkPlanStatusActive,
		SafeNextAction: "workflow chain stage automation may run through lifecycle triggers",
		RunID:          firstNonEmpty(run.CreatedByRunID, run.ID),
		TraceID:        run.TraceID,
	})
	return err
}

// FinalizeGitOpsForShadow exposes the current GitOps finalization helper to
// durable shadow tests. The helper mutates run; callers must persist the run
// through the store after a nil error, mirroring the current service callers.
func (svc *Service) FinalizeGitOpsForShadow(ctx context.Context, run *ChainRun) error {
	if run != nil {
		if _, err := svc.config(run.ProjectID, run.ChainRef); err != nil {
			return err
		}
	}
	return svc.finalizeGitOps(ctx, run)
}

func (svc *Service) shadowConfigForProjectRun(projectID string, run ChainRun) (Config, error) {
	projectID, err := safeRef(projectID, "project_id")
	if err != nil {
		return Config{}, err
	}
	if run.ProjectID != projectID {
		return Config{}, fmt.Errorf("%w: shadow project mismatch", ErrInvalidInput)
	}
	return svc.config(run.ProjectID, run.ChainRef)
}

func configuredShadowStage(cfg Config, requested StageConfig) (StageConfig, error) {
	stageRef, err := safeRef(requested.StageRef, "stage_ref")
	if err != nil {
		return StageConfig{}, err
	}
	workflowRef, err := safeRef(requested.WorkflowRef, "workflow_ref")
	if err != nil {
		return StageConfig{}, err
	}
	for _, stage := range cfg.Stages {
		if stage.StageRef != stageRef {
			continue
		}
		if stage.WorkflowRef != workflowRef {
			return StageConfig{}, fmt.Errorf("%w: shadow stage workflow mismatch", ErrInvalidInput)
		}
		return stage, nil
	}
	return StageConfig{}, fmt.Errorf("%w: shadow stage is not configured", ErrInvalidInput)
}

func (svc *Service) compileStage(ctx context.Context, cfg Config, run ChainRun, stage StageConfig, dryRun bool) (StageRun, error) {
	stageRun, compiled, err := svc.compileStageMetadata(ctx, cfg, run, stage, dryRun)
	if err != nil {
		return StageRun{}, err
	}
	if dryRun {
		return stageRun, nil
	}
	if err := svc.activateCompiledStage(ctx, cfg, run, compiled); err != nil {
		return StageRun{}, err
	}
	return stageRun, nil
}

func (svc *Service) compileStageMetadata(ctx context.Context, cfg Config, run ChainRun, stage StageConfig, dryRun bool) (StageRun, projectworkflow.WorkflowCompileResult, error) {
	workflow, err := svc.resolveWorkflow(ctx, cfg.ProjectID, stage.WorkflowRef)
	if err != nil {
		return StageRun{}, projectworkflow.WorkflowCompileResult{}, err
	}
	title := renderTemplate(firstNonEmpty(cfg.DefaultTitleTemplate, "{{input_ref}} governed delivery"), cfg.ChainRef, run.InputRef)
	if stage.StageRef != "" {
		title = title + " / " + stage.StageRef
	}
	compiled, err := svc.workflows.CompileWorkflow(ctx, projectworkflow.WorkflowCompileInput{
		ProjectID:       cfg.ProjectID,
		WorkflowID:      workflow.ID,
		UserRequestRef:  run.InputRef,
		ContextPackRefs: run.ContextRefs,
		CreatedByRunID:  firstNonEmpty(run.CreatedByRunID, run.ID),
		TraceID:         run.TraceID,
		TitleOverride:   title,
		DryRun:          dryRun,
	})
	if err != nil {
		return StageRun{}, projectworkflow.WorkflowCompileResult{}, err
	}
	stageRun := StageRun{
		StageRef:      stage.StageRef,
		WorkflowRef:   stage.WorkflowRef,
		WorkflowID:    workflow.ID,
		Status:        StageStatusQueued,
		WorkPlanID:    compiled.WorkPlanID,
		WorkTaskIDs:   appendUniqueMany(append([]string(nil), compiled.WorkTaskIDs...), compiled.ReviewerTaskIDs),
		AutomationIDs: append([]string(nil), compiled.AutomationIDs...),
		StartedAt:     svc.now(),
	}
	if dryRun {
		stageRun.Status = StageStatusPlanned
		return stageRun, compiled, nil
	}
	return stageRun, compiled, nil
}

func (svc *Service) activateCompiledStage(ctx context.Context, cfg Config, run ChainRun, compiled projectworkflow.WorkflowCompileResult) error {
	if err := svc.carryForwardStageOutputTasks(ctx, cfg.ProjectID, run, compiled); err != nil {
		return err
	}
	if err := svc.releaseCompiledTasks(ctx, cfg.ProjectID, compiled, run); err != nil {
		return err
	}
	if compiled.WorkPlanID != "" {
		if _, err := svc.workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:      cfg.ProjectID,
			PlanID:         compiled.WorkPlanID,
			Status:         projectworkplan.WorkPlanStatusActive,
			SafeNextAction: "workflow chain stage automation may run through lifecycle triggers",
			RunID:          firstNonEmpty(run.CreatedByRunID, run.ID),
			TraceID:        run.TraceID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) carryForwardStageOutputTasks(ctx context.Context, projectID string, run ChainRun, compiled projectworkflow.WorkflowCompileResult) error {
	if svc.workPlans == nil || compiled.WorkPlanID == "" || len(run.StageRuns) == 0 {
		return nil
	}
	var previous StageRun
	for i := len(run.StageRuns) - 1; i >= 0; i-- {
		if run.StageRuns[i].Status == StageStatusCompleted && run.StageRuns[i].WorkPlanID != "" {
			previous = run.StageRuns[i]
			break
		}
	}
	if previous.WorkPlanID == "" {
		return nil
	}
	sourceTasks, err := svc.workPlans.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: previous.WorkPlanID})
	if err != nil {
		return err
	}
	targetTasks, err := svc.workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: compiled.WorkPlanID})
	if err != nil {
		return err
	}
	existingTasksByRef := map[string]projectworkplan.WorkTask{}
	for _, task := range targetTasks {
		existingTasksByRef[task.TaskRef] = task
	}
	compiledTaskIDs := map[string]struct{}{}
	for _, taskID := range previous.WorkTaskIDs {
		compiledTaskIDs[taskID] = struct{}{}
	}
	sourceRefs := map[string]struct{}{}
	hasDecompositionWorkflowTask := false
	for _, task := range sourceTasks {
		if strings.TrimSpace(task.TaskRef) == "decompose-work-plan" {
			hasDecompositionWorkflowTask = true
		}
		if strings.TrimSpace(task.ID) != "" {
			sourceRefs[task.ID] = struct{}{}
		}
		if strings.TrimSpace(task.TaskRef) != "" {
			sourceRefs[task.TaskRef] = struct{}{}
		}
		if chainStageOutputTaskCandidate(task, compiledTaskIDs) &&
			task.Status != projectworkplan.WorkTaskStatusPlanned &&
			task.Status != projectworkplan.WorkTaskStatusReady &&
			task.Status != projectworkplan.WorkTaskStatusDone {
			return fmt.Errorf("%w: non_ready_carried_implementation_task_%s", ErrInvalidInput, safeAutomationToken(task.Status))
		}
		if chainStageOutputTaskCandidate(task, compiledTaskIDs) && !chainStageOutputTaskHasReviewProof(task) {
			return fmt.Errorf("%w: unreviewed_carried_implementation_task", ErrInvalidInput)
		}
	}
	carriedCount := 0
	carriedBySourceRef := map[string]string{}
	carriedSourceTasks := chainStageOutputTasksInDependencyOrder(sourceTasks, compiledTaskIDs)
	for _, task := range carriedSourceTasks {
		carriedCount++
		if existing, exists := existingTasksByRef[task.TaskRef]; exists {
			carriedBySourceRef[task.ID] = existing.ID
			if ref := strings.TrimSpace(task.TaskRef); ref != "" {
				carriedBySourceRef[ref] = existing.ID
			}
			automationID, err := svc.ensureCarriedImplementationAutomation(ctx, projectID, compiled.WorkPlanID, run, existing)
			if err != nil {
				return err
			}
			if err := svc.persistCarriedStageRefs(ctx, run, compiled.WorkPlanID, existing.ID, automationID); err != nil {
				return err
			}
			continue
		}
		dependencyIDs := carriedTaskDependencyIDs(task, sourceRefs, carriedBySourceRef)
		created, err := svc.workPlans.CreateWorkTask(ctx, carriedTaskCreateInput(projectID, compiled.WorkPlanID, run, task, projectworkplan.WorkTaskStatusPlanned, dependencyIDs))
		if err != nil {
			return err
		}
		automationID, err := svc.ensureCarriedImplementationAutomation(ctx, projectID, compiled.WorkPlanID, run, created)
		if err != nil {
			return err
		}
		if err := svc.persistCarriedStageRefs(ctx, run, compiled.WorkPlanID, created.ID, automationID); err != nil {
			return err
		}
		existingTasksByRef[created.TaskRef] = created
		carriedBySourceRef[task.ID] = created.ID
		if ref := strings.TrimSpace(task.TaskRef); ref != "" {
			carriedBySourceRef[ref] = created.ID
		}
	}
	if carriedCount == 0 && hasDecompositionWorkflowTask && targetPlanHasSelector(targetTasks) {
		return fmt.Errorf("%w: missing_carried_implementation_tasks", ErrInvalidInput)
	}
	return nil
}

func chainStageOutputTasksInDependencyOrder(tasks []projectworkplan.WorkTask, compiledTaskIDs map[string]struct{}) []projectworkplan.WorkTask {
	eligibleByRef := map[string]projectworkplan.WorkTask{}
	for _, task := range tasks {
		if !chainStageOutputTask(task, compiledTaskIDs) {
			continue
		}
		if strings.TrimSpace(task.ID) != "" {
			eligibleByRef[task.ID] = task
		}
		if strings.TrimSpace(task.TaskRef) != "" {
			eligibleByRef[task.TaskRef] = task
		}
	}
	visited := map[string]bool{}
	visiting := map[string]bool{}
	var ordered []projectworkplan.WorkTask
	var visit func(projectworkplan.WorkTask)
	visit = func(task projectworkplan.WorkTask) {
		key := firstNonEmpty(task.ID, task.TaskRef)
		if key == "" || visited[key] || visiting[key] {
			return
		}
		visiting[key] = true
		for _, dep := range task.DependencyTaskIDs {
			if depTask, ok := eligibleByRef[strings.TrimSpace(dep)]; ok {
				visit(depTask)
			}
		}
		visiting[key] = false
		visited[key] = true
		ordered = append(ordered, task)
	}
	for _, task := range tasks {
		if chainStageOutputTask(task, compiledTaskIDs) {
			visit(task)
		}
	}
	return ordered
}

func targetPlanHasSelector(tasks []projectworkplan.WorkTask) bool {
	for _, task := range tasks {
		if strings.TrimSpace(task.TaskRef) == "select-ready-tasks" {
			return true
		}
	}
	return false
}

func (svc *Service) ensureCarriedImplementationAutomation(ctx context.Context, projectID string, planID string, run ChainRun, task projectworkplan.WorkTask) (string, error) {
	if svc == nil || svc.automations == nil || strings.TrimSpace(task.TaskRef) == "" {
		return "", nil
	}
	automations, err := svc.automations.ListAutomations(ctx, projectautomation.AutomationFilter{ProjectID: projectID, Status: projectautomation.AutomationStatusEnabled, AgentID: "implementation-worker"})
	if err != nil {
		return "", err
	}
	for _, automation := range automations {
		if automation.PlanID == planID && containsRefString(automation.AllowedTaskRefs, task.TaskRef) {
			return automation.ID, nil
		}
	}
	automation, err := svc.automations.CreateAutomation(ctx, projectautomation.CreateAutomationInput{
		ProjectID:       projectID,
		AutomationRef:   carriedImplementationAutomationRef(run, task),
		Title:           "Run Carried Implementation Task",
		Purpose:         "Execute a carried implementation Work Task generated by a completed prior workflow stage.",
		Status:          projectautomation.AutomationStatusEnabled,
		AgentID:         "implementation-worker",
		PlanID:          planID,
		AllowedTaskRefs: []string{task.ID, task.TaskRef},
		TriggerKind:     projectautomation.TriggerKindAutomatic,
		SchedulePolicy:  "on-ready-task",
		PermissionRef:   "permission_snapshot:permission-snapshot-workflow-workplan-implementation-implementation-worker",
		SourceKind:      projectautomation.AutomationSourceWorkflow,
		CreatedByRunID:  firstNonEmpty(run.CreatedByRunID, run.ID),
		TraceID:         run.TraceID,
	})
	if err != nil {
		return "", err
	}
	return automation.ID, nil
}

func (svc *Service) persistCarriedStageRefs(ctx context.Context, run ChainRun, planID string, taskID string, automationID string) error {
	if svc == nil || svc.store == nil || strings.TrimSpace(run.ID) == "" || strings.TrimSpace(planID) == "" {
		return nil
	}
	current, err := svc.store.GetChainRun(ctx, run.ProjectID, run.ID)
	if err != nil {
		return err
	}
	run = current
	changed := false
	for i := range run.StageRuns {
		if run.StageRuns[i].WorkPlanID != planID {
			continue
		}
		beforeTasks := len(run.StageRuns[i].WorkTaskIDs)
		run.StageRuns[i].WorkTaskIDs = appendUnique(run.StageRuns[i].WorkTaskIDs, taskID)
		if len(run.StageRuns[i].WorkTaskIDs) != beforeTasks {
			changed = true
		}
		if strings.TrimSpace(automationID) != "" {
			beforeAutomations := len(run.StageRuns[i].AutomationIDs)
			run.StageRuns[i].AutomationIDs = appendUnique(run.StageRuns[i].AutomationIDs, automationID)
			if len(run.StageRuns[i].AutomationIDs) != beforeAutomations {
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	run.WorkPlanIDs = appendUnique(run.WorkPlanIDs, planID)
	if strings.TrimSpace(automationID) != "" {
		run.AutomationIDs = appendUnique(run.AutomationIDs, automationID)
	}
	run.UpdatedAt = svc.now()
	_, err = svc.store.UpdateChainRun(ctx, run)
	return err
}

func carriedImplementationAutomationRef(run ChainRun, task projectworkplan.WorkTask) string {
	return "carried-implementation:" + safeAutomationToken(firstNonEmpty(run.ID, run.CreatedByRunID, "chain-run")) + ":" + safeAutomationToken(task.TaskRef)
}

func containsRefString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func safeAutomationToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.' || r == ':':
			b.WriteRune(r)
		case r == '/' || r == '\\' || r == ' ' || r == '\t' || r == '\n':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		if b.Len() >= 80 {
			break
		}
	}
	out := strings.Trim(b.String(), "-_.:")
	if out == "" {
		return "ref"
	}
	return out
}

func chainStageOutputTask(task projectworkplan.WorkTask, compiledTaskIDs map[string]struct{}) bool {
	if !chainStageOutputTaskCandidate(task, compiledTaskIDs) {
		return false
	}
	return (task.Status == projectworkplan.WorkTaskStatusPlanned ||
		task.Status == projectworkplan.WorkTaskStatusReady ||
		task.Status == projectworkplan.WorkTaskStatusDone) &&
		chainStageOutputTaskHasReviewProof(task)
}

func chainStageOutputTaskCandidate(task projectworkplan.WorkTask, compiledTaskIDs map[string]struct{}) bool {
	if _, compiled := compiledTaskIDs[task.ID]; compiled {
		return false
	}
	if task.Status == projectworkplan.WorkTaskStatusFailed || task.Status == projectworkplan.WorkTaskStatusCancelled || task.Status == projectworkplan.WorkTaskStatusSuperseded {
		return false
	}
	if strings.TrimSpace(task.TaskRef) == "" || strings.HasPrefix(strings.TrimSpace(task.TaskRef), "review-") {
		return false
	}
	return task.DecompositionQuality == projectworkplan.DecompositionReady && len(task.FilesToEdit) > 0
}

func chainStageOutputTaskHasReviewProof(task projectworkplan.WorkTask) bool {
	return len(task.ReviewResultRefs) > 0 || strings.TrimSpace(task.ReviewExemptReason) != ""
}

func carriedTaskCreateInput(projectID string, planID string, run ChainRun, task projectworkplan.WorkTask, status string, dependencyIDs []string) projectworkplan.CreateWorkTaskInput {
	evidenceRefs := append([]string(nil), task.EvidenceRefs...)
	evidenceRefs = append(evidenceRefs, task.ReviewResultRefs...)
	evidenceRefs = append(evidenceRefs, task.VerifierResultRefs...)
	return projectworkplan.CreateWorkTaskInput{
		ProjectID:               projectID,
		PlanID:                  planID,
		TaskRef:                 task.TaskRef,
		Title:                   task.Title,
		Description:             task.Description,
		Status:                  status,
		OwnerAgent:              "implementation-worker",
		RunID:                   firstNonEmpty(run.CreatedByRunID, run.ID),
		TraceID:                 run.TraceID,
		EvidenceNeeded:          append([]string(nil), task.EvidenceNeeded...),
		ContextPackRefs:         append([]string(nil), task.ContextPackRefs...),
		FilesToRead:             append([]string(nil), task.FilesToRead...),
		FilesToEdit:             append([]string(nil), task.FilesToEdit...),
		LikelyFilesAffected:     append([]string(nil), task.LikelyFilesAffected...),
		DependencyTaskIDs:       append([]string(nil), dependencyIDs...),
		VerificationRequirement: task.VerificationRequirement,
		GitOpsVerificationMode:  task.GitOpsVerificationMode,
		ExpectedOutput:          task.ExpectedOutput,
		FailureCriteria:         task.FailureCriteria,
		ReviewGate:              task.ReviewGate,
		ResumeInstructions:      task.ResumeInstructions,
		EvidenceRefs:            evidenceRefs,
		ClaimRefs:               append([]string(nil), task.ClaimRefs...),
		VerifierResultRefs:      nil,
		ReviewResultRefs:        nil,
		ReviewExemptReason:      "",
		ArtifactRefs:            append([]string(nil), task.ArtifactRefs...),
		AgentRunIDs:             append([]string(nil), task.AgentRunIDs...),
		DecompositionQuality:    task.DecompositionQuality,
		AcceptanceCriteria:      append([]string(nil), task.AcceptanceCriteria...),
		StopConditions:          append([]string(nil), task.StopConditions...),
		VerifierLadder:          append([]string(nil), task.VerifierLadder...),
		RegressionApplicability: task.RegressionApplicability,
		DownstreamImpactRefs:    append([]string(nil), task.DownstreamImpactRefs...),
		OutputContract:          task.OutputContract,
	}
}

func carriedTaskDependencyIDs(task projectworkplan.WorkTask, sourceRefs map[string]struct{}, carriedBySourceRef map[string]string) []string {
	if len(task.DependencyTaskIDs) == 0 {
		return nil
	}
	out := make([]string, 0, len(task.DependencyTaskIDs))
	for _, dep := range task.DependencyTaskIDs {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if carriedID := strings.TrimSpace(carriedBySourceRef[dep]); carriedID != "" {
			out = append(out, carriedID)
			continue
		}
		if _, sourceRef := sourceRefs[dep]; sourceRef {
			continue
		}
		out = append(out, dep)
	}
	return out
}

func (svc *Service) releaseCompiledTasks(ctx context.Context, projectID string, compiled projectworkflow.WorkflowCompileResult, run ChainRun) error {
	if svc.workPlans == nil || compiled.WorkPlanID == "" || (len(compiled.WorkTaskIDs) == 0 && len(compiled.ReviewerTaskIDs) == 0) {
		return nil
	}
	tasks, err := svc.workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: compiled.WorkPlanID})
	if err != nil {
		return err
	}
	compiledTasks := map[string]struct{}{}
	for _, taskID := range compiled.WorkTaskIDs {
		compiledTasks[taskID] = struct{}{}
	}
	for _, taskID := range compiled.ReviewerTaskIDs {
		compiledTasks[taskID] = struct{}{}
	}
	for _, task := range tasks {
		if _, ok := compiledTasks[task.ID]; !ok || task.Status != projectworkplan.WorkTaskStatusPlanned {
			continue
		}
		ready, err := svc.compiledTaskDependenciesDone(ctx, projectID, task)
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		if _, err := svc.workPlans.UpdateWorkTaskStatus(ctx, projectworkplan.UpdateWorkTaskStatusInput{
			WorkTaskActionInput: projectworkplan.WorkTaskActionInput{
				ProjectID:      projectID,
				TaskID:         task.ID,
				SafeNextAction: "workflow chain released compiled stage task for automatic lifecycle execution",
				RunID:          firstNonEmpty(run.CreatedByRunID, run.ID),
				TraceID:        run.TraceID,
			},
			Status: projectworkplan.WorkTaskStatusReady,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) compiledTaskDependenciesDone(ctx context.Context, projectID string, task projectworkplan.WorkTask) (bool, error) {
	for _, depID := range task.DependencyTaskIDs {
		dep, err := svc.workPlans.GetWorkTask(ctx, projectID, depID)
		if err != nil {
			return false, err
		}
		if dep.Status != projectworkplan.WorkTaskStatusDone {
			return false, nil
		}
	}
	return true, nil
}

func (svc *Service) validateConfiguredWorkflows(ctx context.Context, cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, stage := range cfg.Stages {
		if _, ok := seen[stage.WorkflowRef]; ok {
			continue
		}
		seen[stage.WorkflowRef] = struct{}{}
		if _, err := svc.resolveWorkflow(ctx, cfg.ProjectID, stage.WorkflowRef); err != nil {
			return err
		}
	}
	return nil
}

func (svc *Service) resolveWorkflow(ctx context.Context, projectID, workflowRef string) (projectworkflow.WorkflowDefinition, error) {
	workflows, err := svc.workflows.ListWorkflows(ctx, projectworkflow.WorkflowFilter{ProjectID: projectID, Status: projectworkflow.WorkflowStatusEnabled, WorkflowRef: workflowRef})
	if err != nil {
		return projectworkflow.WorkflowDefinition{}, err
	}
	if len(workflows) != 1 {
		return projectworkflow.WorkflowDefinition{}, fmt.Errorf("%w: workflow_ref %s must resolve to exactly one enabled workflow", ErrInvalidInput, workflowRef)
	}
	return workflows[0], nil
}

func (svc *Service) config(projectID, chainRef string) (Config, error) {
	for _, cfg := range svc.configs {
		if !cfg.Enabled {
			continue
		}
		if cfg.ProjectID == projectID && cfg.ChainRef == chainRef {
			return cfg, validateConfig(cfg)
		}
	}
	return Config{}, fmt.Errorf("%w: workflow chain config not found", ErrInvalidInput)
}

func nextReadyStage(cfg Config, run ChainRun) (StageConfig, bool) {
	statusByRef := map[string]string{}
	for _, stage := range run.StageRuns {
		statusByRef[stage.StageRef] = stage.Status
	}
	for _, stage := range cfg.Stages {
		if statusByRef[stage.StageRef] != StageStatusPlanned {
			continue
		}
		ready := true
		for _, dep := range stage.DependsOn {
			if statusByRef[dep] != StageStatusCompleted {
				ready = false
				break
			}
		}
		if ready {
			return stage, true
		}
	}
	return StageConfig{}, false
}

func allStagesCompleted(run ChainRun) bool {
	if len(run.StageRuns) == 0 {
		return false
	}
	for _, stage := range run.StageRuns {
		if stage.Status != StageStatusCompleted {
			return false
		}
	}
	return true
}

func startResult(run ChainRun, dryRun bool) StartResult {
	return StartResult{
		ProjectID:      run.ProjectID,
		ChainRef:       run.ChainRef,
		InputRef:       run.InputRef,
		Status:         run.Status,
		ChainRunID:     run.ID,
		ContextRefs:    append([]string(nil), run.ContextRefs...),
		StageRuns:      append([]StageRun(nil), run.StageRuns...),
		WorkPlanIDs:    append([]string(nil), run.WorkPlanIDs...),
		AutomationIDs:  append([]string(nil), run.AutomationIDs...),
		DryRun:         dryRun,
		NextAction:     run.NextAction,
		PullRequestRef: run.PullRequestRef,
	}
}

func (svc *Service) resolveContextRefs(ctx context.Context, cfg Config, inputRef string) ([]string, error) {
	refs := contextRefsForInput(cfg, inputRef)
	if cfg.ContextMode != ContextModeLocalIngested || cfg.ContextProvider != ContextProviderJira {
		return refs, nil
	}
	if svc.localContexts == nil {
		return nil, fmt.Errorf("%w: local_ingested Jira context reader is required for workflow chain", ErrInvalidInput)
	}
	issueKey := strings.TrimPrefix(inputRef, "jira:")
	result, err := svc.localContexts.ReadLocalContent(ctx, projectintegrations.LocalReadInput{
		ProjectID:     cfg.ProjectID,
		Provider:      projectintegrations.ProviderJira,
		ItemIDOrKey:   issueKey,
		MaxChunkBytes: 4096,
		MaxChunks:     12,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: local_ingested Jira context unavailable for %s: %v", ErrInvalidInput, issueKey, err)
	}
	if err := validateJiraTicketContext(issueKey, result); err != nil {
		return nil, err
	}
	return appendUniqueMany(refs, jiraTicketContextRefs(issueKey)), nil
}

func contextRefsForInput(cfg Config, inputRef string) []string {
	switch cfg.ContextProvider {
	case ContextProviderJira:
		return []string{inputRef}
	case ContextProviderConfluence:
		return []string{"confluence:" + strings.TrimPrefix(inputRef, "input:")}
	case ContextProviderIndexedRepo:
		return []string{"repo:" + strings.TrimPrefix(inputRef, "input:")}
	default:
		return nil
	}
}

func validateJiraTicketContext(issueKey string, result projectintegrations.RichContentReadResult) error {
	if strings.TrimSpace(result.Artifact.ItemKey) == "" && strings.TrimSpace(result.Artifact.ItemID) == "" {
		return fmt.Errorf("%w: local_ingested Jira context missing artifact for %s", ErrInvalidInput, issueKey)
	}
	hasSummary := false
	hasScope := false
	hasImplementationEvidence := false
	for _, chunk := range result.Chunks {
		text := strings.ToLower(strings.TrimSpace(chunk.Text))
		if text == "" {
			continue
		}
		field := strings.ToLower(strings.TrimSpace(firstNonEmpty(chunk.FieldName, chunk.Label)))
		switch field {
		case "summary":
			hasSummary = true
		case "description", "acceptance", "acceptance_criteria", "acceptance criteria":
			hasScope = true
		default:
			if strings.Contains(field, "description") || strings.Contains(field, "acceptance") {
				hasScope = true
			}
		}
		if containsAny(text,
			"source anchor",
			"source anchors",
			"source-backed",
			"affected repo",
			"affected file",
			"files_to_edit",
			"regression test",
			"verifier",
			"risk areas",
		) {
			hasImplementationEvidence = true
		}
	}
	var missing []string
	if !hasSummary {
		missing = append(missing, "summary")
	}
	if !hasScope {
		missing = append(missing, "description_or_acceptance_criteria")
	}
	if !hasImplementationEvidence {
		missing = append(missing, "implementation_evidence")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: local_ingested Jira context for %s missing %s", ErrInvalidInput, issueKey, strings.Join(missing, ","))
	}
	return nil
}

func jiraTicketContextRefs(issueKey string) []string {
	issueKey = strings.TrimSpace(issueKey)
	return []string{
		"jira-context:" + issueKey + ":summary",
		"jira-context:" + issueKey + ":scope",
		"jira-context:" + issueKey + ":implementation-evidence",
		"jira-context:" + issueKey + ":source-anchors",
		"jira-context:" + issueKey + ":verifier-scope",
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func renderTemplate(template string, chainRef string, inputRef string) string {
	out := strings.ReplaceAll(template, "{{chain_ref}}", chainRef)
	out = strings.ReplaceAll(out, "{{input_ref}}", inputRef)
	if len(out) > 200 {
		out = strings.TrimSpace(out[:200])
	}
	return out
}

func safeProjectObject(projectID, objectID, field string) (string, string, error) {
	projectID, err := safeRef(projectID, "project_id")
	if err != nil {
		return "", "", err
	}
	objectID, err = safeRef(objectID, field)
	if err != nil {
		return "", "", err
	}
	return projectID, objectID, nil
}

func appendUnique(values []string, next string) []string {
	if strings.TrimSpace(next) == "" {
		return values
	}
	for _, value := range values {
		if value == next {
			return values
		}
	}
	return append(values, next)
}

func appendUniqueMany(values []string, next []string) []string {
	for _, value := range next {
		values = appendUnique(values, value)
	}
	return values
}

func cloneConfigs(configs []Config) []Config {
	out := append([]Config(nil), configs...)
	for i := range out {
		out[i].Stages = append([]StageConfig(nil), out[i].Stages...)
		for j := range out[i].Stages {
			out[i].Stages[j].DependsOn = append([]string(nil), out[i].Stages[j].DependsOn...)
		}
	}
	return out
}

func newID(prefix string) string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return prefix + "_" + hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return prefix + "_" + hex.EncodeToString(bytes[:])
}
