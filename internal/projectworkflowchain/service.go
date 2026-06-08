package projectworkflowchain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

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
	ListOpenWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)
	UpdateWorkPlanStatus(context.Context, projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error)
	UpdateWorkTaskStatus(context.Context, projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error)
}

type GitOpsFinalizer interface {
	FinalizeWorkflowChain(context.Context, GitOpsFinalizeInput) (GitOpsFinalizeResult, error)
}

type LocalContextReader interface {
	ReadLocalContent(context.Context, projectintegrations.LocalReadInput) (projectintegrations.RichContentReadResult, error)
}

type GitOpsFinalizeInput struct {
	ProjectID      string
	ChainRunID     string
	ChainRef       string
	InputRef       string
	WorkPlan       projectworkplan.WorkPlan
	StageRuns      []StageRun
	AutomationIDs  []string
	CreatedByRunID string
	TraceID        string
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
	if run.Status == ChainStatusBlocked && run.GitOpsReady && allStagesCompleted(run) {
		return svc.retryBlockedGitOps(ctx, run)
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
	} else if allStagesCompleted(run) {
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
				run.Status = ChainStatusBlocked
				run.GitOpsReady = true
				run.NextAction = "chain blocked while creating draft PR GitOps output"
				if len(run.StageRuns) > 0 {
					run.StageRuns[len(run.StageRuns)-1].BlockedReason = gitOpsBlockedReason(err)
				}
				_, _ = svc.store.UpdateChainRun(ctx, run)
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
	if err := svc.finalizeGitOps(ctx, &run); err != nil {
		run.Status = ChainStatusBlocked
		run.GitOpsReady = true
		run.NextAction = "chain blocked while creating draft PR GitOps output"
		if len(run.StageRuns) > 0 {
			run.StageRuns[len(run.StageRuns)-1].BlockedReason = gitOpsBlockedReason(err)
		}
		run.UpdatedAt = svc.now()
		_, _ = svc.store.UpdateChainRun(ctx, run)
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
	result, err := svc.gitOpsFinalizer.FinalizeWorkflowChain(ctx, GitOpsFinalizeInput{
		ProjectID:      run.ProjectID,
		ChainRunID:     run.ID,
		ChainRef:       run.ChainRef,
		InputRef:       run.InputRef,
		WorkPlan:       plan,
		StageRuns:      append([]StageRun(nil), run.StageRuns...),
		AutomationIDs:  append([]string(nil), run.AutomationIDs...),
		CreatedByRunID: run.CreatedByRunID,
		TraceID:        run.TraceID,
	})
	if err != nil {
		return err
	}
	if result.Skipped || result.NoChanges || strings.TrimSpace(result.PullRequestRef) == "" {
		return fmt.Errorf("%w: GitOps finalization did not create a draft PR", ErrInvalidInput)
	}
	run.PullRequestRef = result.PullRequestRef
	run.Status = ChainStatusCompleted
	run.GitOpsReady = false
	run.NextAction = "workflow chain completed with draft PR GitOps output"
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
	for _, chunk := range result.Chunks {
		if strings.TrimSpace(chunk.Text) == "" {
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
	}
	var missing []string
	if !hasSummary {
		missing = append(missing, "summary")
	}
	if !hasScope {
		missing = append(missing, "description_or_acceptance_criteria")
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
	}
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
