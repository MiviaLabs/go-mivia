package workflows

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cschleiden/go-workflows/client"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable"
	"github.com/MiviaLabs/go-mivia/internal/projectdurable/activities"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
	chainstore "github.com/MiviaLabs/go-mivia/internal/projectworkflowchain/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

type chainPipelineHarness struct {
	pipeline *fakeChainPipeline
	shadow   *fakeShadowWriter
}

type fakeChainPipeline struct {
	mu sync.Mutex

	stages []projectdurable.SafeChainStagePlan
	config []projectworkflowchain.StageConfig

	chainStore *chainstore.MemoryStore
	chainSvc   *projectworkflowchain.Service
	workPlans  *projectworkplan.Service
	finalizer  *chainShadowGitOpsFinalizer
	runID      string
	inputRef   string
	traceID    string

	compiled        map[string]projectdurable.SafeStageCompileOutcome
	compiledNative  map[string]projectworkflow.WorkflowCompileResult
	compileOrder    []string
	compileCalls    map[string]int
	compileFailures map[string]error
	releaseActivate []string

	planStatusScripts map[string][]string
	observeCalls      map[string]int
	taskStatuses      map[string]map[string]string
	carriedByPlan     map[string][]string

	gitOpsReady    bool
	pullRequestRef string
	recoveryStatus string
	blockedReason  string
	gitOpsCalls    int
	currentFound   bool
	currentFields  map[string]string
}

func newChainPipelineHarness(t *testing.T, stages []projectdurable.SafeChainStagePlan) *chainPipelineHarness {
	t.Helper()
	cfg := chainShadowConfig(stages)
	chainStore := chainstore.NewMemoryStore()
	workPlanStore := workplanstore.NewMemoryStore()
	workPlans := projectworkplan.New(workPlanStore)
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:         true,
		RunnerEnabled:   true,
		RunnerExecution: projectautomation.RunnerExecutionExternal,
		PermissionResolver: chainShadowPermissionResolver{
			allowedRunnerKinds: []string{projectautomation.RunnerKindCodexCLI},
		},
		WorkPlanStatusTrigger: projectautomation.WorkPlanStatusTriggerOptions{
			Enabled:  true,
			Statuses: []string{projectworkplan.WorkPlanStatusActive},
		},
	})
	workflows := projectworkflow.New(workflowstore.NewMemoryStore())
	workflows.SetCompilerDependencies(workPlans, automations)
	for _, path := range []string{
		"configs/workflows/governed-decomposition-planning.toml",
		"configs/workflows/governed-workplan-implementation.toml",
		"configs/workflows/governed-post-implementation-validation.toml",
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", path))
		if err != nil {
			t.Fatalf("read workflow %s: %v", path, err)
		}
		if _, err := workflows.ImportWorkflowTOML(context.Background(), projectworkflow.ImportWorkflowTOMLInput{
			ProjectID:      "project-1",
			Data:           data,
			CreatedByRunID: "import-chain-shadow",
			TraceID:        "trace-chain-shadow-import",
		}); err != nil {
			t.Fatalf("import workflow %s: %v", path, err)
		}
	}
	chainSvc := projectworkflowchain.New(chainStore, workflows, workPlans, []projectworkflowchain.Config{cfg})
	chainSvc.SetAutomationAPI(automations)
	finalizer := &chainShadowGitOpsFinalizer{result: projectworkflowchain.GitOpsFinalizeResult{
		CommitRef:      "commit/chain-1044",
		PushRef:        "push/chain-1044",
		PullRequestRef: "github-pr-1044",
		EvidenceRefs:   []string{"gitops-evidence:chain-1044"},
	}}
	chainSvc.SetGitOpsFinalizer(finalizer)
	pipeline := &fakeChainPipeline{
		stages:            stages,
		config:            cfg.Stages,
		chainStore:        chainStore,
		chainSvc:          chainSvc,
		workPlans:         workPlans,
		finalizer:         finalizer,
		runID:             "workflow_chain_run_shadow",
		compiled:          map[string]projectdurable.SafeStageCompileOutcome{},
		compiledNative:    map[string]projectworkflow.WorkflowCompileResult{},
		compileCalls:      map[string]int{},
		compileFailures:   map[string]error{},
		planStatusScripts: map[string][]string{},
		observeCalls:      map[string]int{},
		taskStatuses:      map[string]map[string]string{},
		carriedByPlan:     map[string][]string{},
		gitOpsReady:       true,
		pullRequestRef:    "github-pr-1044",
		currentFound:      true,
		currentFields: map[string]string{
			"final_status":     activities.ChainTraceStatusCompleted,
			"gitops_ready":     "true",
			"pull_request_ref": "github-pr-1044",
		},
	}
	return &chainPipelineHarness{pipeline: pipeline, shadow: &fakeShadowWriter{}}
}

func defaultChainHarness(t *testing.T) *chainPipelineHarness {
	t.Helper()
	return newChainPipelineHarness(t, []projectdurable.SafeChainStagePlan{
		{StageRef: "decomposition", WorkflowRef: "governed-decomposition-planning"},
		{StageRef: "implementation", WorkflowRef: "governed-workplan-implementation"},
	})
}

func governedChainHarness(t *testing.T) *chainPipelineHarness {
	t.Helper()
	return newChainPipelineHarness(t, []projectdurable.SafeChainStagePlan{
		{StageRef: "decomposition", WorkflowRef: "governed-decomposition-planning"},
		{StageRef: "implementation", WorkflowRef: "governed-workplan-implementation"},
		{StageRef: "post-validation", WorkflowRef: "governed-post-implementation-validation"},
	})
}

func chainShadowConfig(stages []projectdurable.SafeChainStagePlan) projectworkflowchain.Config {
	cfg := projectworkflowchain.Config{
		ProjectID:            "project-1",
		ChainRef:             "chain-1044",
		Enabled:              true,
		InputKind:            projectworkflowchain.InputKindSafeRef,
		ContextProvider:      projectworkflowchain.ContextProviderIndexedRepo,
		ContextMode:          projectworkflowchain.ContextModeIndexed,
		DefaultTitleTemplate: "{{input_ref}} governed delivery",
	}
	if len(stages) > 2 {
		cfg.GitOpsMode = projectworkflowchain.GitOpsModeDraftPRAfterValidation
		cfg.GitOpsEnabled = true
	}
	for i, stage := range stages {
		cfgStage := projectworkflowchain.StageConfig{
			StageRef:    stage.StageRef,
			WorkflowRef: stage.WorkflowRef,
			Trigger:     projectworkflowchain.TriggerAfterStageReviewPassed,
		}
		if i == 0 {
			cfgStage.Trigger = projectworkflowchain.TriggerOnChainStart
		} else {
			cfgStage.DependsOn = []string{stages[i-1].StageRef}
		}
		cfg.Stages = append(cfg.Stages, cfgStage)
	}
	return cfg
}

type chainShadowPermissionResolver struct {
	allowedRunnerKinds []string
}

func (r chainShadowPermissionResolver) CheckAutomationPermission(_ context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      input.PermissionRef,
		AgentID:            input.AgentID,
		AllowedRunnerKinds: append([]string(nil), r.allowedRunnerKinds...),
	}, nil
}

type chainShadowGitOpsFinalizer struct {
	result projectworkflowchain.GitOpsFinalizeResult
	inputs []projectworkflowchain.GitOpsFinalizeInput
}

func (f *chainShadowGitOpsFinalizer) FinalizeWorkflowChain(_ context.Context, input projectworkflowchain.GitOpsFinalizeInput) (projectworkflowchain.GitOpsFinalizeResult, error) {
	f.inputs = append(f.inputs, input)
	return f.result, nil
}

func (p *fakeChainPipeline) ResolveChainStages(ctx context.Context, projectID, chainRef string) ([]projectdurable.SafeChainStagePlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	configs, err := p.chainSvc.ResolveStageConfigsForShadow(ctx, projectID, chainRef)
	if err != nil {
		return nil, err
	}
	p.config = configs
	p.stages = nil
	for _, cfg := range configs {
		p.stages = append(p.stages, projectdurable.SafeChainStagePlan{StageRef: cfg.StageRef, WorkflowRef: cfg.WorkflowRef})
	}
	return append([]projectdurable.SafeChainStagePlan(nil), p.stages...), nil
}

func (p *fakeChainPipeline) CompileStage(ctx context.Context, projectID, chainRef string, stage projectdurable.SafeChainStagePlan, inputRef string, carriedTaskIDs []string) (projectdurable.SafeStageCompileOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.compiled[stage.StageRef]; ok {
		return existing, nil
	}
	run, err := p.ensureRunLocked(ctx, projectID, chainRef, inputRef)
	if err != nil {
		return projectdurable.SafeStageCompileOutcome{}, err
	}
	stageCfg, ok := p.stageConfigLocked(stage.StageRef)
	if !ok {
		return projectdurable.SafeStageCompileOutcome{}, fmt.Errorf("stage config not found")
	}
	p.compileCalls[stage.StageRef]++
	p.compileOrder = append(p.compileOrder, stage.StageRef)
	if err := p.compileFailures[stage.StageRef]; err != nil {
		return projectdurable.SafeStageCompileOutcome{}, err
	}
	stageRun, compiled, err := p.chainSvc.CompileStageMetadataForShadow(ctx, run, stageCfg)
	if err != nil {
		return projectdurable.SafeStageCompileOutcome{}, err
	}
	outcome := projectdurable.SafeStageCompileOutcome{
		PlanID:                compiled.WorkPlanID,
		TaskIDs:               append([]string(nil), compiled.WorkTaskIDs...),
		ReviewerTaskIDs:       append([]string(nil), compiled.ReviewerTaskIDs...),
		AutomationIDs:         append([]string(nil), compiled.AutomationIDs...),
		PermissionSnapshotIDs: append([]string(nil), compiled.PermissionSnapshotIDs...),
		ContextRefs:           append([]string(nil), run.ContextRefs...),
	}
	p.persistCompiledStageLocked(ctx, run, stageRun)
	p.compiled[stage.StageRef] = outcome
	p.compiledNative[stage.StageRef] = compiled
	if script, ok := p.planStatusScripts[stage.StageRef]; ok {
		p.planStatusScripts[compiled.WorkPlanID] = append([]string(nil), script...)
	}
	if _, ok := p.planStatusScripts[compiled.WorkPlanID]; !ok {
		p.planStatusScripts[compiled.WorkPlanID] = []string{"done"}
	}
	return outcome, nil
}

func (p *fakeChainPipeline) ReleaseCompiledTasks(ctx context.Context, planID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	stageRef, compiled, run, err := p.compiledByPlanLocked(ctx, planID)
	if err != nil {
		return err
	}
	if plan, err := p.workPlans.GetWorkPlan(ctx, run.ProjectID, planID); err == nil && plan.Status == projectworkplan.WorkPlanStatusDone {
		p.releaseActivate = append(p.releaseActivate, "release:"+planID)
		return nil
	}
	if err := p.chainSvc.CarryForwardStageOutputTasksForShadow(ctx, run.ProjectID, run, compiled); err != nil {
		return err
	}
	refreshed, err := p.chainStore.GetChainRun(ctx, run.ProjectID, run.ID)
	if err == nil {
		run = refreshed
	}
	if err := p.chainSvc.ReleaseCompiledTasksForShadow(ctx, run.ProjectID, compiled, run); err != nil {
		return err
	}
	p.releaseActivate = append(p.releaseActivate, "release:"+planID)
	_ = stageRef
	return nil
}

func (p *fakeChainPipeline) ActivateWorkPlan(ctx context.Context, planID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, compiled, run, err := p.compiledByPlanLocked(ctx, planID)
	if err != nil {
		return err
	}
	if plan, err := p.workPlans.GetWorkPlan(ctx, run.ProjectID, planID); err == nil && plan.Status == projectworkplan.WorkPlanStatusDone {
		p.releaseActivate = append(p.releaseActivate, "activate:"+planID)
		return nil
	}
	if err := p.chainSvc.ActivateCompiledWorkPlanForShadow(ctx, run, compiled); err != nil {
		return err
	}
	p.releaseActivate = append(p.releaseActivate, "activate:"+planID)
	return nil
}

func (p *fakeChainPipeline) ObserveStagePlanStatus(_ context.Context, planID string) (string, map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	calls := p.observeCalls[planID]
	p.observeCalls[planID] = calls + 1
	script := p.planStatusScripts[planID]
	status := "done"
	if len(script) > 0 {
		if calls >= len(script) {
			status = script[len(script)-1]
		} else {
			status = script[calls]
		}
	}
	if status == "done" {
		if err := p.completePlanLocked(context.Background(), planID); err != nil {
			return "", nil, err
		}
	} else if status == "failed" {
		if _, err := p.workPlans.UpdateWorkPlanStatus(context.Background(), projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:      "project-1",
			PlanID:         planID,
			Status:         projectworkplan.WorkPlanStatusFailed,
			SafeNextAction: "stage failed in durable shadow test",
		}); err != nil {
			return "", nil, err
		}
	} else if status == "blocked" {
		if _, err := p.workPlans.UpdateWorkPlanStatus(context.Background(), projectworkplan.UpdateWorkPlanStatusInput{
			ProjectID:      "project-1",
			PlanID:         planID,
			Status:         projectworkplan.WorkPlanStatusBlocked,
			SafeNextAction: "stage blocked in durable shadow test",
		}); err != nil {
			return "", nil, err
		}
	}
	tasks, err := p.workPlans.ListWorkTasks(context.Background(), projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: planID})
	if err != nil {
		return "", nil, err
	}
	statuses := map[string]string{}
	for _, task := range tasks {
		statuses[task.ID] = task.Status
	}
	return status, statuses, nil
}

func (p *fakeChainPipeline) CarryForwardOutputs(_ context.Context, fromPlanID, toStageRef string) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if carried, ok := p.carriedByPlan[fromPlanID]; ok {
		return append([]string(nil), carried...), nil
	}
	carried, err := p.sourceOutputTaskIDsLocked(context.Background(), fromPlanID)
	if err != nil {
		return nil, err
	}
	p.carriedByPlan[fromPlanID] = carried
	_ = toStageRef
	return append([]string(nil), carried...), nil
}

func (p *fakeChainPipeline) ObserveGitOps(_ context.Context, chainRef string) (bool, string, string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gitOpsCalls++
	run, err := p.chainStore.GetChainRun(context.Background(), "project-1", p.runID)
	if err != nil {
		return false, "", "", "", err
	}
	if len(p.config) <= 2 {
		run.Status = projectworkflowchain.ChainStatusCompleted
		run.GitOpsReady = false
		run.NextAction = "workflow chain completed"
		if _, err := p.chainStore.UpdateChainRun(context.Background(), run); err != nil {
			return false, "", "", "", err
		}
		p.gitOpsReady = false
		p.pullRequestRef = ""
		p.recoveryStatus = run.GitOpsRecoveryStatus
		return false, "", run.GitOpsRecoveryStatus, p.blockedReason, nil
	}
	run.Status = projectworkflowchain.ChainStatusPostValidationPassed
	run.GitOpsReady = true
	run.NextAction = "chain ready for draft PR GitOps finalization"
	if _, err := p.chainStore.UpdateChainRun(context.Background(), run); err != nil {
		return false, "", "", "", err
	}
	if err := p.chainSvc.FinalizeGitOpsForShadow(context.Background(), &run); err != nil {
		return false, "", run.GitOpsRecoveryStatus, "gitops-finalization-failed", err
	}
	if _, err := p.chainStore.UpdateChainRun(context.Background(), run); err != nil {
		return false, "", "", "", err
	}
	p.gitOpsReady = run.GitOpsReady
	p.pullRequestRef = run.PullRequestRef
	p.recoveryStatus = run.GitOpsRecoveryStatus
	return run.GitOpsReady, run.PullRequestRef, run.GitOpsRecoveryStatus, p.blockedReason, nil
}

func (p *fakeChainPipeline) LoadCurrentChainRun(_ context.Context, projectID, chainRef string) (map[string]string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, err := p.chainStore.GetChainRun(context.Background(), projectID, p.runID)
	if err != nil {
		return nil, false, nil
	}
	return map[string]string{
		"final_status":     run.Status,
		"gitops_ready":     fmt.Sprintf("%t", run.GitOpsReady),
		"pull_request_ref": run.PullRequestRef,
	}, p.currentFound, nil
}

func (p *fakeChainPipeline) compileCount(stageRef string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.compileCalls[stageRef]
}

func (p *fakeChainPipeline) counts() (plans, tasks, automations int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, outcome := range p.compiled {
		plans++
		tasks += len(outcome.TaskIDs) + len(outcome.ReviewerTaskIDs)
		automations += len(outcome.AutomationIDs)
	}
	return plans, tasks, automations
}

func (p *fakeChainPipeline) gitOpsCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.gitOpsCalls
}

func (p *fakeChainPipeline) ensureRunLocked(ctx context.Context, projectID, chainRef, inputRef string) (projectworkflowchain.ChainRun, error) {
	if p.inputRef == "" {
		p.inputRef = inputRef
	}
	run, err := p.chainStore.GetChainRun(ctx, projectID, p.runID)
	if err == nil {
		return run, nil
	}
	run = projectworkflowchain.ChainRun{
		ID:             p.runID,
		ProjectID:      projectID,
		ChainRef:       chainRef,
		InputRef:       inputRef,
		Status:         projectworkflowchain.ChainStatusPlanned,
		ContextRefs:    []string{inputRef},
		CreatedByRunID: "codex-orchestrator-run-1044",
		TraceID:        firstNonEmptyTest(p.traceID, "trace-chain-shadow-current-service"),
		NextAction:     "compile first stage and activate its Work Plan",
	}
	for _, stage := range p.config {
		run.StageRuns = append(run.StageRuns, projectworkflowchain.StageRun{
			StageRef:    stage.StageRef,
			WorkflowRef: stage.WorkflowRef,
			Status:      projectworkflowchain.StageStatusPlanned,
		})
	}
	return p.chainStore.CreateChainRun(ctx, run)
}

func (p *fakeChainPipeline) stageConfigLocked(stageRef string) (projectworkflowchain.StageConfig, bool) {
	for _, stage := range p.config {
		if stage.StageRef == stageRef {
			return stage, true
		}
	}
	return projectworkflowchain.StageConfig{}, false
}

func (p *fakeChainPipeline) persistCompiledStageLocked(ctx context.Context, run projectworkflowchain.ChainRun, stageRun projectworkflowchain.StageRun) {
	for i := range run.StageRuns {
		if run.StageRuns[i].StageRef == stageRun.StageRef {
			run.StageRuns[i] = stageRun
			break
		}
	}
	run.WorkPlanIDs = appendUniqueTest(run.WorkPlanIDs, stageRun.WorkPlanID)
	run.AutomationIDs = appendUniqueManyTest(run.AutomationIDs, stageRun.AutomationIDs)
	run.Status = projectworkflowchain.ChainStatusQueued
	run.NextAction = "stage automation will run when lifecycle gates are satisfied"
	_, _ = p.chainStore.UpdateChainRun(ctx, run)
}

func (p *fakeChainPipeline) compiledByPlanLocked(ctx context.Context, planID string) (string, projectworkflow.WorkflowCompileResult, projectworkflowchain.ChainRun, error) {
	run, err := p.chainStore.GetChainRun(ctx, "project-1", p.runID)
	if err != nil {
		return "", projectworkflow.WorkflowCompileResult{}, projectworkflowchain.ChainRun{}, err
	}
	for stageRef, outcome := range p.compiled {
		if outcome.PlanID == planID {
			compiled, ok := p.compiledNative[stageRef]
			if !ok {
				return "", projectworkflow.WorkflowCompileResult{}, projectworkflowchain.ChainRun{}, fmt.Errorf("compiled stage not found")
			}
			return stageRef, compiled, run, nil
		}
	}
	return "", projectworkflow.WorkflowCompileResult{}, projectworkflowchain.ChainRun{}, fmt.Errorf("compiled plan not found")
}

func (p *fakeChainPipeline) completePlanLocked(ctx context.Context, planID string) error {
	run, err := p.chainStore.GetChainRun(ctx, "project-1", p.runID)
	if err != nil {
		return err
	}
	compiledTaskIDs := map[string]struct{}{}
	for _, stage := range run.StageRuns {
		if stage.WorkPlanID != planID {
			continue
		}
		for _, taskID := range stage.WorkTaskIDs {
			compiledTaskIDs[taskID] = struct{}{}
		}
		break
	}
	tasks, err := p.workPlans.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: planID})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if _, compiled := compiledTaskIDs[task.ID]; !compiled {
			continue
		}
		task.Status = projectworkplan.WorkTaskStatusDone
		task.EvidenceRefs = appendUniqueTest(task.EvidenceRefs, "evidence:"+task.ID)
		task.VerifierResultRefs = appendUniqueTest(task.VerifierResultRefs, "verifier:"+task.ID)
		task.ReviewResultRefs = appendUniqueTest(task.ReviewResultRefs, "review:"+task.ID)
		task.Outcome = "durable shadow completed stage task"
		if _, err := p.workPlans.UpdateWorkTask(ctx, task); err != nil {
			return err
		}
	}
	if _, err := p.ensureSourceOutputTasksLocked(ctx, planID); err != nil {
		return err
	}
	if _, err := p.workPlans.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      "project-1",
		PlanID:         planID,
		Status:         projectworkplan.WorkPlanStatusDone,
		SafeNextAction: "durable shadow completed stage plan",
	}); err != nil {
		return err
	}
	run, err = p.chainStore.GetChainRun(ctx, "project-1", p.runID)
	if err != nil {
		return err
	}
	for i := range run.StageRuns {
		if run.StageRuns[i].WorkPlanID == planID {
			run.StageRuns[i].Status = projectworkflowchain.StageStatusCompleted
		}
	}
	_, err = p.chainStore.UpdateChainRun(ctx, run)
	return err
}

func (p *fakeChainPipeline) ensureSourceOutputTasksLocked(ctx context.Context, planID string) ([]string, error) {
	existing, err := p.sourceOutputTaskIDsLocked(ctx, planID)
	if err != nil {
		return nil, err
	}
	if len(existing) == 2 {
		return existing, nil
	}
	createdIDs := append([]string(nil), existing...)
	for i := len(existing); i < 2; i++ {
		taskRef := fmt.Sprintf("implementation-output-%d", i+1)
		created, err := p.workPlans.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
			ProjectID:               "project-1",
			PlanID:                  planID,
			TaskRef:                 taskRef,
			Title:                   fmt.Sprintf("Implementation output %d", i+1),
			Description:             "durable shadow source output task",
			Status:                  projectworkplan.WorkTaskStatusReady,
			OwnerAgent:              "implementation-worker",
			RunID:                   p.runID,
			TraceID:                 p.traceID,
			FilesToEdit:             []string{fmt.Sprintf("internal/example/output_%d.go", i+1)},
			VerificationRequirement: "durable shadow verifier",
			ReviewResultRefs:        []string{"review:" + taskRef},
			VerifierResultRefs:      []string{"verifier:" + taskRef},
			EvidenceRefs:            []string{"evidence:" + taskRef},
			DecompositionQuality:    projectworkplan.DecompositionReady,
		})
		if err != nil {
			return nil, err
		}
		created.Status = projectworkplan.WorkTaskStatusDone
		created.Outcome = "durable shadow source output ready for carry-forward"
		updated, err := p.workPlans.UpdateWorkTask(ctx, created)
		if err != nil {
			return nil, err
		}
		createdIDs = append(createdIDs, updated.ID)
	}
	p.carriedByPlan[planID] = createdIDs
	return append([]string(nil), createdIDs...), nil
}

func (p *fakeChainPipeline) sourceOutputTaskIDsLocked(ctx context.Context, planID string) ([]string, error) {
	tasks, err := p.workPlans.ListWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: planID})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, task := range tasks {
		if strings.HasPrefix(task.TaskRef, "implementation-output-") {
			ids = append(ids, task.ID)
		}
	}
	return ids, nil
}

func appendUniqueTest(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueManyTest(values []string, incoming []string) []string {
	for _, value := range incoming {
		values = appendUniqueTest(values, value)
	}
	return values
}

func firstNonEmptyTest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func runChainShadowWorkflow(t *testing.T, h *chainPipelineHarness, input ChainShadowWorkflowInput) (ChainShadowTrace, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := projectdurable.NewMemoryEngine()
	defer func() {
		if err := engine.Close(); err != nil {
			t.Fatalf("close engine: %v", err)
		}
	}()
	if err := engine.Orchestrator.RegisterWorkflow(MiviaWorkflowChainShadowWorkflow); err != nil {
		t.Fatalf("register workflow: %v", err)
	}
	if err := engine.Orchestrator.RegisterActivity(&activities.WorkflowChainActivities{
		Pipeline: h.pipeline,
		Compare:  h.pipeline,
		Shadow:   h.shadow,
	}); err != nil {
		t.Fatalf("register activities: %v", err)
	}
	if err := engine.Orchestrator.Start(ctx); err != nil {
		t.Fatalf("start orchestrator: %v", err)
	}
	instance, err := engine.Orchestrator.CreateWorkflowInstance(ctx, client.WorkflowInstanceOptions{
		InstanceID: "chain-shadow-" + input.TraceID + "-" + strings.ReplaceAll(t.Name(), "/", "-"),
	}, MiviaWorkflowChainShadowWorkflow, input)
	if err != nil {
		t.Fatalf("create workflow instance: %v", err)
	}
	return client.GetWorkflowResult[ChainShadowTrace](ctx, engine.Orchestrator.Client, instance, 10*time.Second)
}

func chainInput() ChainShadowWorkflowInput {
	return ChainShadowWorkflowInput{
		ProjectID:                "project-1",
		ChainRef:                 "chain-1044",
		InputRef:                 "safe_ref:PROJ-1044",
		TraceID:                  "trace-chain-1044",
		ShadowOnly:               true,
		MaxStageObservationPolls: 2,
	}
}

func TestChainShadowCompilesTwoStagesInOrder(t *testing.T) {
	h := defaultChainHarness(t)
	trace, err := runChainShadowWorkflow(t, h, chainInput())
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if got := fmt.Sprint(h.pipeline.compileOrder); got != "[decomposition implementation]" {
		t.Fatalf("compile order = %s", got)
	}
	decompositionPlanID := h.pipeline.compiled["decomposition"].PlanID
	implementationPlanID := h.pipeline.compiled["implementation"].PlanID
	wantReleaseActivate := fmt.Sprintf("[release:%s activate:%s release:%s activate:%s]", decompositionPlanID, decompositionPlanID, implementationPlanID, implementationPlanID)
	if got := fmt.Sprint(h.pipeline.releaseActivate); got != wantReleaseActivate {
		t.Fatalf("release/activate order = %s", got)
	}
	if trace.FinalStatus != activities.ChainTraceStatusCompleted || len(trace.Stages) != 2 {
		t.Fatalf("unexpected trace: %#v", trace)
	}
	if h.pipeline.gitOpsCallCount() != 1 {
		t.Fatalf("completed chain should observe GitOps once, got %d", h.pipeline.gitOpsCallCount())
	}
}

func TestChainShadowStageTwoDoesNotStartBeforeStageOneStatus(t *testing.T) {
	h := defaultChainHarness(t)
	h.pipeline.planStatusScripts["decomposition"] = []string{"active", "active"}
	input := chainInput()
	input.MaxStageObservationPolls = 2
	trace, err := runChainShadowWorkflow(t, h, input)
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if h.pipeline.compileCount("implementation") != 0 {
		t.Fatalf("stage 2 compiled before stage 1 done")
	}
	if trace.FinalStatus != activities.ChainTraceStatusBlocked || trace.FailureCategory != activities.ChainFailureStageObservationTimeout {
		t.Fatalf("expected observation-timeout block, got %#v", trace)
	}
	if h.pipeline.gitOpsCallCount() != 0 {
		t.Fatalf("blocked stage must not observe GitOps, got %d calls", h.pipeline.gitOpsCallCount())
	}
	if got := trace.Steps[len(trace.Steps)-1].Activity; got != activities.ActivityWriteChainShadowComparison {
		t.Fatalf("comparison must run last after blocked stage, got %q", got)
	}
	if h.shadow.callCount() != 1 {
		t.Fatalf("blocked stage should still write one comparison, got %d", h.shadow.callCount())
	}
}

func TestChainShadowCarriesConcreteTaskIDsNotSourceRefs(t *testing.T) {
	h := defaultChainHarness(t)
	trace, err := runChainShadowWorkflow(t, h, chainInput())
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if len(trace.Stages) != 2 || trace.Stages[1].CarriedIDsCount != 2 {
		t.Fatalf("stage 2 did not receive concrete carried ids: %#v", trace.Stages)
	}
	if len(trace.Stages) == 0 {
		t.Fatalf("expected stages")
	}
	for _, carried := range h.pipeline.carriedByPlan[trace.Stages[0].PlanID] {
		if !strings.HasPrefix(carried, "work_task_") {
			t.Fatalf("carried source ref instead of concrete task id: %q", carried)
		}
	}
}

func TestChainShadowFailedStageBlocksWithSafeReason(t *testing.T) {
	h := defaultChainHarness(t)
	h.pipeline.planStatusScripts["decomposition"] = []string{"failed"}
	trace, err := runChainShadowWorkflow(t, h, chainInput())
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if trace.FinalStatus != activities.ChainTraceStatusBlocked || trace.FailureCategory != activities.ChainFailureStageFailed {
		t.Fatalf("expected safe failed-stage block, got %#v", trace)
	}
	if h.pipeline.compileCount("implementation") != 0 {
		t.Fatalf("stage 2 compiled after failed stage")
	}
	if h.pipeline.gitOpsCallCount() != 0 {
		t.Fatalf("failed stage must not observe GitOps, got %d calls", h.pipeline.gitOpsCallCount())
	}
	if got := trace.Steps[len(trace.Steps)-1].Activity; got != activities.ActivityWriteChainShadowComparison {
		t.Fatalf("comparison must run last after failed stage, got %q", got)
	}
}

func TestChainShadowActivityFailureStillComparesLast(t *testing.T) {
	h := defaultChainHarness(t)
	h.pipeline.compileFailures["implementation"] = fmt.Errorf("compile unavailable")
	trace, err := runChainShadowWorkflow(t, h, chainInput())
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if trace.FinalStatus != activities.ChainTraceStatusFailed || trace.FailureCategory != activities.ChainFailureActivityFailed {
		t.Fatalf("expected safe activity failure, got %#v", trace)
	}
	if h.pipeline.compileCount("implementation") == 0 {
		t.Fatalf("expected implementation compile to be attempted")
	}
	if h.pipeline.gitOpsCallCount() != 0 {
		t.Fatalf("failed activity must not observe GitOps, got %d calls", h.pipeline.gitOpsCallCount())
	}
	if got := trace.Steps[len(trace.Steps)-1].Activity; got != activities.ActivityWriteChainShadowComparison {
		t.Fatalf("comparison must run last after failed activity, got %q", got)
	}
	fields := h.shadow.capturedFields()
	if fields["final_status"] != activities.ChainTraceStatusFailed || fields["failure_category"] != activities.ChainFailureActivityFailed {
		t.Fatalf("shadow comparison missed failed terminal fields: %#v", fields)
	}
}

func TestChainShadowPostValidationMatchesCurrentChainService(t *testing.T) {
	h := governedChainHarness(t)
	trace, err := runChainShadowWorkflow(t, h, chainInput())
	if err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if trace.FinalStatus != activities.ChainTraceStatusCompleted || trace.GitOpsReady || trace.PullRequestRef != "github-pr-1044" {
		t.Fatalf("durable trace diverged from current chain fields: %#v", trace)
	}
	fields := h.shadow.capturedFields()
	for _, key := range []string{"agree_final_status", "agree_gitops_ready", "agree_pull_request_ref"} {
		if fields[key] != "true" {
			t.Fatalf("shadow comparison %s = %q, fields=%#v", key, fields[key], fields)
		}
	}
}

func TestChainShadowRepeatedResumeDoesNotDuplicate(t *testing.T) {
	h := governedChainHarness(t)
	first, err := runChainShadowWorkflow(t, h, chainInput())
	if err != nil {
		t.Fatalf("first workflow failed: %v", err)
	}
	plans, tasks, automations := h.pipeline.counts()
	secondInput := chainInput()
	secondInput.TraceID = "trace-chain-1044-replay"
	second, err := runChainShadowWorkflow(t, h, secondInput)
	if err != nil {
		t.Fatalf("second workflow failed: %v", err)
	}
	plans2, tasks2, automations2 := h.pipeline.counts()
	if plans2 != plans || tasks2 != tasks || automations2 != automations {
		t.Fatalf("resume duplicated state: before=%d/%d/%d after=%d/%d/%d", plans, tasks, automations, plans2, tasks2, automations2)
	}
	if first.FinalStatus != second.FinalStatus || first.PullRequestRef != second.PullRequestRef {
		t.Fatalf("resume trace changed final outcome:\nfirst=%#v\nsecond=%#v", first, second)
	}
	for _, stage := range []string{"decomposition", "implementation", "post-validation"} {
		if h.pipeline.compileCount(stage) != 1 {
			t.Fatalf("stage %s compile-or-get called compile path %d times", stage, h.pipeline.compileCount(stage))
		}
	}
}
