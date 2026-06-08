package projectworkflowchain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
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

func TestStartDryRunPreflightsLocalJiraContextBeforePersistence(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("MASS-1044", true)})

	result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run start: %v", err)
	}
	if !containsString(result.ContextRefs, "jira-context:MASS-1044:summary") || !containsString(result.ContextRefs, "jira-context:MASS-1044:scope") {
		t.Fatalf("dry run missing verified context refs: %#v", result.ContextRefs)
	}
	if !containsString(result.ContextRefs, "jira-context:MASS-1044:implementation-evidence") || !containsString(result.ContextRefs, "jira-context:MASS-1044:source-anchors") || !containsString(result.ContextRefs, "jira-context:MASS-1044:verifier-scope") {
		t.Fatalf("dry run missing implementation context refs: %#v", result.ContextRefs)
	}
	if len(workflows.compileInputs) != 3 {
		t.Fatalf("expected dry run to compile all stages, got %d", len(workflows.compileInputs))
	}
	if !containsString(workflows.compileInputs[0].ContextPackRefs, "jira-context:MASS-1044:scope") || !containsString(workflows.compileInputs[0].ContextPackRefs, "jira-context:MASS-1044:implementation-evidence") {
		t.Fatalf("compile input missing context refs: %#v", workflows.compileInputs[0])
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("dry run persisted runs: %#v", runs)
	}
}

func TestStartRejectsLocalJiraContextMissingScopeBeforeRunCreation(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContext("MASS-1044", false)})

	_, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "description_or_acceptance_criteria") {
		t.Fatalf("expected missing scope rejection, got %v", err)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("invalid context must not create chain runs: %#v", runs)
	}
	if len(workflows.compileInputs) != 0 {
		t.Fatalf("invalid context must not compile workflows: %#v", workflows.compileInputs)
	}
}

func TestStartRejectsLocalJiraContextMissingImplementationEvidenceBeforeRunCreation(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	svc := New(store, workflows, &fakeWorkPlans{}, []Config{localIngestedTestConfig()})
	svc.SetLocalContextReader(fakeLocalContextReader{result: localJiraContextWithoutImplementationEvidence("MASS-1044")})

	_, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "implementation_evidence") {
		t.Fatalf("expected missing implementation evidence rejection, got %v", err)
	}
	runs, err := store.ListChainRuns(ctx, ChainFilter{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("invalid context must not create chain runs: %#v", runs)
	}
	if len(workflows.compileInputs) != 0 {
		t.Fatalf("invalid context must not compile workflows: %#v", workflows.compileInputs)
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
	if got, want := strings.Join(workPlans.events[:2], ","), "release:task-decomposition,activate:plan-decomposition"; got != want {
		t.Fatalf("stage activation must release tasks before plan active event, got %s", got)
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
	if got, want := strings.Join(workPlans.events[2:4], ","), "release:task-implementation,activate:plan-implementation"; got != want {
		t.Fatalf("next stage activation must release tasks before plan active event, got %s", got)
	}
}

func TestStartDoesNotActivateFirstStageBeforeChainRunPersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	store.createErr = errors.New("chain store unavailable")
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"}); err == nil {
		t.Fatalf("expected start to fail when chain run persistence fails")
	}
	if len(workPlans.activations) != 0 || len(workPlans.released) != 0 {
		t.Fatalf("must not activate or release stage work before chain persistence, activations=%#v released=%#v", workPlans.activations, workPlans.released)
	}
	if len(store.runs) != 0 {
		t.Fatalf("failed create must not leave persisted chain runs: %#v", store.runs)
	}
}

func TestStartDoesNotActivateFirstStageBeforeStageMetadataUpdatePersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	svc.newID = deterministicIDs("workflow_chain_run_1")
	store.updateErr = errors.New("chain update unavailable")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"}); err == nil {
		t.Fatalf("expected start to fail when first-stage metadata update fails")
	}
	if len(workPlans.activations) != 0 || len(workPlans.released) != 0 {
		t.Fatalf("must not activate or release first-stage work before chain metadata update, activations=%#v released=%#v", workPlans.activations, workPlans.released)
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
	input := finalizer.inputs[0]
	if !containsString(input.AllowedPathspecs, "internal/projectworkflowchain/service.go") || !containsString(input.AllowedPathspecs, "cmd/mivia-server") {
		t.Fatalf("expected implementation pathspecs in GitOps finalization, got %#v", input.AllowedPathspecs)
	}
	if !containsString(input.ReviewRefs, "review:task-post-validation") || !containsString(input.VerifierRefs, "verifier:task-post-validation") {
		t.Fatalf("expected stage review and verifier refs in GitOps finalization, got reviews=%#v verifiers=%#v", input.ReviewRefs, input.VerifierRefs)
	}
	if !containsString(input.TestResults, "task-post-validation verified by verifier:task-post-validation") {
		t.Fatalf("expected verifier-derived test result in GitOps finalization, got %#v", input.TestResults)
	}

	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
		t.Fatalf("idempotent post-validation event: %v", err)
	}
	if len(finalizer.inputs) != 1 {
		t.Fatalf("expected no duplicate GitOps finalization, got %d", len(finalizer.inputs))
	}
}

func TestHandleWorkPlanStatusChangedDoesNotActivateNextStageBeforeChainUpdatePersists(t *testing.T) {
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
	store.updateErr = errors.New("chain update unavailable")
	err = svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatalf("expected next-stage chain update failure")
	}
	if len(workPlans.activations) != 1 || workPlans.activations[0] != "plan-decomposition" {
		t.Fatalf("must not activate implementation before chain update, start=%#v activations=%#v", result, workPlans.activations)
	}
	if len(workPlans.released) != 1 || workPlans.released[0] != "task-decomposition" {
		t.Fatalf("must not release implementation before chain update, released=%#v", workPlans.released)
	}
}

func TestHandleWorkPlanStatusChangedDoesNotFinalizeGitOpsBeforeCheckpointPersists(t *testing.T) {
	ctx := context.Background()
	store := newTestChainStore()
	workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
	workPlans := &fakeWorkPlans{}
	svc := New(store, workflows, workPlans, []Config{testConfig()})
	finalizer := &fakeGitOpsFinalizer{result: GitOpsFinalizeResult{PullRequestRef: "pr/MASS-1044"}}
	svc.SetGitOpsFinalizer(finalizer)
	svc.newID = deterministicIDs("workflow_chain_run_1")

	if _, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	store.updateErr = errors.New("chain update unavailable")
	err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone})
	if err == nil {
		t.Fatalf("expected GitOps checkpoint update failure")
	}
	if len(finalizer.inputs) != 0 {
		t.Fatalf("must not create draft PR GitOps output before checkpoint persists, inputs=%#v", finalizer.inputs)
	}
}

func TestHandleWorkPlanStatusChangedBlocksWhenDraftPRFinalizerMissing(t *testing.T) {
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
	for _, planID := range []string{"plan-decomposition", "plan-implementation"} {
		if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: planID, NewStatus: projectworkplan.WorkPlanStatusDone}); err != nil {
			t.Fatalf("advance after %s done: %v", planID, err)
		}
	}
	if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-post-validation", NewStatus: projectworkplan.WorkPlanStatusDone}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected missing finalizer to block with invalid input, got %v", err)
	}
	run, err := svc.Get(ctx, "project-1", result.ChainRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != ChainStatusBlocked || !run.GitOpsReady || run.StageRuns[2].BlockedReason == "" {
		t.Fatalf("expected blocked GitOps-ready chain with explicit reason, got %#v", run)
	}
	if !strings.HasPrefix(run.StageRuns[2].BlockedReason, "gitops_finalize_failed_invalid_project_workflow_chain_input_gitops_finalizer_missing") {
		t.Fatalf("expected explicit missing-finalizer blocked reason, got %#v", run.StageRuns[2].BlockedReason)
	}
}

func TestHandleWorkPlanStatusChangedStopsChainWhenStagePlanCancelledOrSuperseded(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		planStatus  string
		chainStatus string
		stageStatus string
		reason      string
	}{
		{name: "cancelled", planStatus: projectworkplan.WorkPlanStatusCancelled, chainStatus: ChainStatusCancelled, stageStatus: StageStatusCancelled, reason: "work_plan_cancelled"},
		{name: "superseded", planStatus: projectworkplan.WorkPlanStatusSuperseded, chainStatus: ChainStatusSuperseded, stageStatus: StageStatusSuperseded, reason: "work_plan_superseded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestChainStore()
			workflows := &fakeWorkflowAPI{workflows: enabledWorkflows()}
			workPlans := &fakeWorkPlans{}
			svc := New(store, workflows, workPlans, []Config{testConfig()})
			svc.newID = deterministicIDs("workflow_chain_run_1")

			result, err := svc.Start(ctx, StartInput{ProjectID: "project-1", ChainRef: "chain-1", InputText: "MASS-1044"})
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			if err := svc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{ProjectID: "project-1", PlanID: "plan-decomposition", NewStatus: tc.planStatus}); err != nil {
				t.Fatalf("terminal change: %v", err)
			}
			run, err := svc.Get(ctx, "project-1", result.ChainRunID)
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			if run.Status != tc.chainStatus || run.StageRuns[0].Status != tc.stageStatus || run.StageRuns[0].BlockedReason != tc.reason {
				t.Fatalf("expected chain/stage terminal %s/%s, got %#v", tc.chainStatus, tc.stageStatus, run)
			}
		})
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

func localIngestedTestConfig() Config {
	cfg := testConfig()
	cfg.ContextMode = ContextModeLocalIngested
	return cfg
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
	compileInputs  []projectworkflow.WorkflowCompileInput
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
	fake.compileInputs = append(fake.compileInputs, input)
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

type fakeLocalContextReader struct {
	result projectintegrations.RichContentReadResult
	err    error
}

func (fake fakeLocalContextReader) ReadLocalContent(_ context.Context, _ projectintegrations.LocalReadInput) (projectintegrations.RichContentReadResult, error) {
	if fake.err != nil {
		return projectintegrations.RichContentReadResult{}, fake.err
	}
	return fake.result, nil
}

func localJiraContext(issueKey string, includeScope bool) projectintegrations.RichContentReadResult {
	chunks := []projectintegrations.RichContentChunkView{{
		ItemKey:   issueKey,
		FieldName: "summary",
		Text:      "Implement bounded automation ticket delivery",
	}}
	if includeScope {
		chunks = append(chunks, projectintegrations.RichContentChunkView{
			ItemKey:   issueKey,
			FieldName: "description",
			Text:      "Acceptance criteria: decompose, implement, verify, and open a draft PR. Source anchors and verifier scope identify the implementation evidence.",
		})
	}
	return projectintegrations.RichContentReadResult{
		Artifact: projectintegrations.RichContentArtifact{
			ID:      "integration-artifact-1",
			ItemID:  "10001",
			ItemKey: issueKey,
		},
		Chunks: chunks,
	}
}

func localJiraContextWithoutImplementationEvidence(issueKey string) projectintegrations.RichContentReadResult {
	result := localJiraContext(issueKey, false)
	result.Chunks = append(result.Chunks, projectintegrations.RichContentChunkView{
		ItemKey:   issueKey,
		FieldName: "description",
		Text:      "Acceptance criteria: deliver the requested behavior.",
	})
	return result
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

type fakeWorkPlans struct {
	activations []string
	released    []string
	events      []string
}

func (fake *fakeWorkPlans) GetWorkPlan(_ context.Context, projectID string, planID string) (projectworkplan.WorkPlan, error) {
	return projectworkplan.WorkPlan{ID: planID, ProjectID: projectID, Status: projectworkplan.WorkPlanStatusDone}, nil
}

func (fake *fakeWorkPlans) UpdateWorkPlanStatus(_ context.Context, input projectworkplan.UpdateWorkPlanStatusInput) (projectworkplan.WorkPlan, error) {
	fake.activations = append(fake.activations, input.PlanID)
	fake.events = append(fake.events, "activate:"+input.PlanID)
	return projectworkplan.WorkPlan{ID: input.PlanID, ProjectID: input.ProjectID, Status: input.Status}, nil
}

func (fake *fakeWorkPlans) GetWorkTask(_ context.Context, projectID string, taskID string) (projectworkplan.WorkTask, error) {
	stage := strings.TrimPrefix(taskID, "task-")
	task := projectworkplan.WorkTask{
		ID:                   taskID,
		ProjectID:            projectID,
		PlanID:               "plan-" + stage,
		TaskRef:              taskID,
		Status:               projectworkplan.WorkTaskStatusDone,
		DecompositionQuality: projectworkplan.DecompositionReady,
		ReviewResultRefs:     []string{"review:" + taskID},
		VerifierResultRefs:   []string{"verifier:" + taskID},
	}
	if stage == "implementation" {
		task.FilesToEdit = []string{"internal/projectworkflowchain/service.go"}
		task.LikelyFilesAffected = []string{"cmd/mivia-server"}
	}
	return task, nil
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
	fake.events = append(fake.events, "release:"+input.TaskID)
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
	runs      map[string]ChainRun
	createErr error
	updateErr error
}

func newTestChainStore() *testChainStore {
	return &testChainStore{runs: map[string]ChainRun{}}
}

func (store *testChainStore) CreateChainRun(_ context.Context, run ChainRun) (ChainRun, error) {
	if store.createErr != nil {
		return ChainRun{}, store.createErr
	}
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
	if store.updateErr != nil {
		return ChainRun{}, store.updateErr
	}
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
