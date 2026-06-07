package projectworkflowchain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func TestValidateConfigRejectsUnsafeAndInvalidChains(t *testing.T) {
	base := testConfig()
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"duplicate stage", func(cfg *Config) { cfg.Stages[1].StageRef = cfg.Stages[0].StageRef }},
		{"cycle", func(cfg *Config) { cfg.Stages[0].DependsOn = []string{"implementation"} }},
		{"unsafe pattern", func(cfg *Config) { cfg.InputPattern = "^MASS-.*$" }},
		{"missing post validation", func(cfg *Config) {
			cfg.Stages = cfg.Stages[:2]
			cfg.GitOpsMode = GitOpsModeDraftPRAfterValidation
		}},
		{"gitops disabled", func(cfg *Config) { cfg.GitOpsEnabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			cfg.Stages = append([]StageConfig(nil), base.Stages...)
			for i := range cfg.Stages {
				cfg.Stages[i].DependsOn = append([]string(nil), base.Stages[i].DependsOn...)
			}
			tc.mutate(&cfg)
			if err := validateConfig(cfg); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid config, got %v", err)
			}
		})
	}
}

func TestStartDryRunRejectsUnsafeInputAndDoesNotCreateRun(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{testConfig()})

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "somebody@example.com", DryRun: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unsafe input rejection, got %v", err)
	}
	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run start: %v", err)
	}
	if !result.DryRun || result.InputRef != "jira:MASS-1044" || len(result.StageRuns) != 3 {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("dry run persisted runs: %#v", runs)
	}
}

func TestStartCreatesFirstStageAndAdvancesAfterPlanDone(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044", CreatedByRunID: "run-1"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result.Status != ChainStatusQueued || len(result.WorkPlanIDs) != 1 || result.StageRuns[0].Status != StageStatusQueued {
		t.Fatalf("unexpected start result: %#v", result)
	}
	if len(workPlans.activations) != 1 || workPlans.activations[0] != "plan-decomposition" {
		t.Fatalf("expected first plan activation, got %#v", workPlans.activations)
	}
	if len(workPlans.released) != 1 || workPlans.released[0] != "task-decomposition" {
		t.Fatalf("expected first stage task release, got %#v", workPlans.released)
	}

	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err != nil {
		t.Fatalf("advance implementation: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.StageRuns[0].Status != StageStatusCompleted || run.StageRuns[1].Status != StageStatusQueued || len(run.WorkPlanIDs) != 2 {
		t.Fatalf("expected implementation queued after decomposition done: %#v", run)
	}
	if workPlans.activations[1] != "plan-implementation" {
		t.Fatalf("expected implementation plan activation, got %#v", workPlans.activations)
	}
}

func TestHandleWorkPlanStatusChangedCreatesDraftPRAfterPostValidationDone(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "pr/MASS-1044"}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation", "plan-post-validation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "pr/MASS-1044" {
		t.Fatalf("expected completed chain with draft PR ref, got %#v", run)
	}
	if run.StageRuns[2].Status != StageStatusCompleted || run.NextAction == "" {
		t.Fatalf("expected completed post-validation stage with next action: %#v", run)
	}
	if len(finalizer.inputs) != 1 || finalizer.inputs[0].WorkPlan.ID != "plan-implementation" {
		t.Fatalf("expected one GitOps finalization with implementation plan, got %#v", finalizer.inputs)
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("idempotent post-validation event: %v", err)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected no duplicate GitOps finalization, got %d", len(finalizer.inputs))
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenDraftPRFinalizationFails(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.SetGitOpsFinalizer(&fakeGitOpsFinalizer{err: errors.New("gitops failed")})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err == nil {
		t.Fatalf("expected GitOps finalization failure")
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed") {
		t.Fatalf("expected blocked chain after GitOps failure, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedRetriesBlockedDraftPRFinalization(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{err: errors.New("git worktree failed: unsafe path")}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err == nil {
		t.Fatalf("expected first GitOps finalization failure")
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get blocked run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed_git_worktree_failed") {
		t.Fatalf("expected blocked run with safe reason, got %#v", run)
	}

	finalizer.err = nil
	finalizer.result = GitOpsFinalizeResult{PullRequestRef: "pr/MASS-1044"}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("retry GitOps finalization: %v", err)
	}
	run, err = svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if run.Status != ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "pr/MASS-1044" {
		t.Fatalf("expected retry to complete chain with PR ref, got %#v", run)
	}
	if len(finalizer.inputs) != 2 || finalizer.inputs[1].WorkPlan.ID != "plan-implementation" {
		t.Fatalf("expected retry to finalize implementation plan, got %#v", finalizer.inputs)
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenNextStageCannotCompile(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows(), failWorkflowID: "workflow-implementation"}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatalf("expected next-stage compile failure")
	}
	run, getErr := svc.Get(ctx, "project-1", result.ChainRunID)
	if getErr != nil {
		t.Fatalf("get run: %v", getErr)
	}
	if run.Status != ChainStatusBlocked || run.StageRuns[1].Status != StageStatusBlocked || run.StageRuns[1].BlockedReason == "" {
		t.Fatalf("expected blocked chain and implementation stage, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedBlocksChainWhenStagePlanBlocks(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusBlocked}); err != nil {
		t.Fatalf("block chain: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || run.StageRuns[0].Status != StageStatusBlocked || run.StageRuns[1].Status == StageStatusQueued {
		t.Fatalf("expected blocked chain without next-stage advancement, got %#v", run)
	}
	if run.StageRuns[0].BlockedReason != "work_plan_blocked" || run.NextAction == "" {
		t.Fatalf("expected safe blocked reason and next action, got %#v", run)
	}
}

func TestHandleWorkPlanStatusChangedFailsChainWhenStagePlanFails(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusFailed}); err != nil {
		t.Fatalf("fail chain: %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusFailed || run.StageRuns[0].Status != StageStatusFailed || run.StageRuns[0].BlockedReason != "work_plan_failed" {
		t.Fatalf("expected failed chain and failed stage, got %#v", run)
	}
}

func TestStartRejectsUnknownWorkflowRef(t *testing.T) {
	ctx := context.Background()
	svc := New(newTestChainStore(), &fakeWorkflowAPI{workflows: enabledWorkflows()[:1]}, &fakeWorkPlans{}, []Config{testConfig()})
	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044", DryRun: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unknown workflow rejection, got %v", err)
	}
}

func testConfig() Config {
	return Config{
		ProjectID:            "project-1",
		ChainRef:             "chain-1",
		Enabled:              true,
		InputKind:            InputKindJiraIssueKey,
		InputPattern:         "^MASS-[0-9]+$",
		ContextProvider:      ContextProviderJira,
		ContextMode:          ContextModeLocalIngested,
		DefaultTitleTemplate: "{{input_ref}} governed delivery",
		GitOpsMode:           GitOpsModeDraftPRAfterValidation,
		GitOpsEnabled:        true,
		Stages: []StageConfig{
			{StageRef: "decomposition", WorkflowRef: "governed-decomposition-planning", Trigger: TriggerOnChainStart, RequiredStatusBeforeNext: StageStatusCompleted},
			{StageRef: "implementation", WorkflowRef: "governed-workplan-implementation", Trigger: TriggerAfterStageReviewPassed, DependsOn: []string{"decomposition"}, RequiredStatusBeforeNext: StageStatusCompleted},
			{StageRef: "post-validation", WorkflowRef: "governed-post-implementation-validation", Trigger: TriggerAfterStageReviewPassed, DependsOn: []string{"implementation"}, RequiredStatusBeforeNext: StageStatusCompleted},
		},
	}
}

func enabledWorkflows() []projectworkflow.WorkflowDefinition {
	return []projectworkflow.WorkflowDefinition{
		{ID: "workflow-decomposition", ProjectID: "project-1", WorkflowRef: "governed-decomposition-planning", Status: projectworkflow.WorkflowStatusEnabled},
		{ID: "workflow-implementation", ProjectID: "project-1", WorkflowRef: "governed-workplan-implementation", Status: projectworkflow.WorkflowStatusEnabled},
		{ID: "workflow-validation", ProjectID: "project-1", WorkflowRef: "governed-post-implementation-validation", Status: projectworkflow.WorkflowStatusEnabled},
	}
}

type fakeWorkflowAPI struct {
	workflows      []projectworkflow.WorkflowDefinition
	failWorkflowID string
}

func (fake *fakeWorkflowAPI) ListWorkflows(_ context.Context, filter projectworkflow.WorkflowFilter) ([]projectworkflow.WorkflowDefinition, error) {
	var out []projectworkflow.WorkflowDefinition
	for _, workflow := range fake.workflows {
		if workflow.ProjectID == filter.ProjectID && workflow.WorkflowRef == filter.WorkflowRef && workflow.Status == filter.Status {
			out = append(out, workflow)
		}
	}
	return out, nil
}

func (fake *fakeWorkflowAPI) CompileWorkflow(_ context.Context, input projectworkflow.WorkflowCompileInput) (projectworkflow.WorkflowCompileResult, error) {
	if input.WorkflowID == fake.failWorkflowID {
		return projectworkflow.WorkflowCompileResult{}, errors.New("compile failed")
	}
	stage := "unknown"
	switch input.WorkflowID {
	case "workflow-decomposition":
		stage = "decomposition"
	case "workflow-implementation":
		stage = "implementation"
	case "workflow-validation":
		stage = "post-validation"
	}
	return projectworkflow.WorkflowCompileResult{
		WorkflowID:    input.WorkflowID,
		WorkPlanID:    "plan-" + stage,
		WorkTaskIDs:   []string{"task-" + stage},
		AutomationIDs: []string{"automation-" + stage},
		DryRun:        input.DryRun,
	}, nil
}

type fakeWorkPlans struct {
	activations []string
	released    []string
}

func (fake *fakeWorkPlans) GetWorkPlan(_ context.Context, projectID string, planID string) (projectworkplan.WorkPlan, error) {
	return projectworkplan.WorkPlan{ID: planID, ProjectID: projectID, Status: projectworkplan.WorkPlanStatusDone}, nil
}

func (fake *fakeWorkPlans) UpdateWorkPlanStatus(_ context.Context, input projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error) {
	fake.activations = append(fake.activations, input.PlanID)
	return projectworkplan.WorkPlan{ID: input.PlanID, ProjectID: input.ProjectID, Status: input.Status}, nil
}

func (fake *fakeWorkPlans) ListOpenWorkTasks(_ context.Context, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
	stage := "unknown"
	switch filter.PlanID {
	case "plan-decomposition":
		stage = "decomposition"
	case "plan-implementation":
		stage = "implementation"
	case "plan-post-validation":
		stage = "post-validation"
	}
	return []projectworkplan.WorkTask{{
		ID:                   "task-" + stage,
		ProjectID:            filter.ProjectID,
		PlanID:               filter.PlanID,
		TaskRef:              "task-" + stage,
		Status:               projectworkplan.WorkTaskStatusPlanned,
		DecompositionQuality: projectworkplan.DecompositionReady,
	}}, nil
}

func (fake *fakeWorkPlans) UpdateWorkTaskStatus(_ context.Context, input projectworkplan.UpdateWorkTaskStatusInput) (projectworkplan.WorkTask, error) {
	fake.released = append(fake.released, input.TaskID)
	return projectworkplan.WorkTask{ID: input.TaskID, ProjectID: input.ProjectID, Status: input.Status}, nil
}

func deterministicIDs(values ...string) func(string) string {
	i := 0
	return func(prefix string) string {
		if i >= len(values) {
			return prefix + "_extra"
		}
		value := values[i]
		i++
		return value
	}
}

type fakeGitOpsFinalizer struct {
	result GitOpsFinalizeResult
	err    error
	inputs []GitOpsFinalizeInput
}

func (fake *fakeGitOpsFinalizer) FinalizeWorkflowChain(_ context.Context, input GitOpsFinalizeInput) (GitOpsFinalizeResult, error) {
	fake.inputs = append(fake.inputs, input)
	if fake.err != nil {
		return GitOpsFinalizeResult{}, fake.err
	}
	return fake.result, nil
}

type testChainStore struct {
	runs map[string]ChainRun
}

func newTestChainStore() *testChainStore {
	return &testChainStore{runs: map[string]ChainRun{}}
}

func (store *testChainStore) CreateChainRun(_ context.Context, run ChainRun) (ChainRun, error) {
	store.runs[run.ID] = run
	return run, nil
}

func (store *testChainStore) GetChainRun(_ context.Context, _ string, chainRunID string) (ChainRun, error) {
	return store.runs[chainRunID], nil
}

func (store *testChainStore) ListChainRuns(_ context.Context, _ ChainFilter) ([]ChainRun, error) {
	out := make([]ChainRun, 0, len(store.runs))
	for _, run := range store.runs {
		out = append(out, run)
	}
	return out, nil
}

func (store *testChainStore) UpdateChainRun(_ context.Context, run ChainRun) (ChainRun, error) {
	store.runs[run.ID] = run
	return run, nil
}

func (store *testChainStore) FindChainRunByWorkPlan(_ context.Context, _ string, workPlanID string) (ChainRun, error) {
	for _, run := range store.runs {
		for _, planID := range run.WorkPlanIDs {
			if planID == workPlanID {
				return run, nil
			}
		}
		for _, stage := range run.StageRuns {
			if stage.WorkPlanID == workPlanID {
				return run, nil
			}
		}
	}
	return ChainRun{}, errors.New("not found")
}
