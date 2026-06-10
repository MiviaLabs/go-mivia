package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
	chainstore "github.com/MiviaLabs/go-mivia/internal/projectworkflowchain/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

func TestToolDefinitionsExposeWorkflowChainTools(t *testing.T) {
	definitions := ToolDefinitions()
	seen := map[string]string{}
	for _, definition := range definitions {
		name, _ := definition["name"].(string)
		description, _ := definition["description"].(string)
		seen[name] = description
	}
	for _, name := range []string{"projects.workflow_chains.start", "projects.workflow_chains.get", "projects.workflow_chains.retry_gitops", "projects.workflow_chains.list"} {
		if seen[name] == "" {
			t.Fatalf("missing tool %s", name)
		}
		if !IsWorkflowChainTool(name) || !IsWorkflowChainTool(strings.ReplaceAll(name, ".", "_")) {
			t.Fatalf("aliases not accepted for %s", name)
		}
	}
	if !strings.Contains(seen["projects.workflow_chains.start"], "never stores raw prompts") {
		t.Fatalf("start description does not state metadata-only safety: %q", seen["projects.workflow_chains.start"])
	}
}

func TestCallToolStartRejectsUnknownFields(t *testing.T) {
	_, err := CallTool(context.Background(), fakeChainAPI{}, "projects.workflow_chains.start", mustArgs(t, map[string]any{
		"id":         "project-1",
		"chain_ref":  "chain-1",
		"input_text": "GENERIC-1044",
		"raw_prompt": "do unsafe work",
	}))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestCallToolStartRejectsUnsafeMetadata(t *testing.T) {
	_, err := CallTool(context.Background(), fakeChainAPI{}, "projects.workflow_chains.start", mustArgs(t, map[string]any{
		"id":         "project-1",
		"chain_ref":  "chain-1",
		"input_text": "GENERIC-1044",
		"trace_id":   "secret-token",
	}))
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected unsafe metadata rejection, got %v", err)
	}
}

func TestCallToolAcceptsUnderscoreAlias(t *testing.T) {
	result, err := CallTool(context.Background(), fakeChainAPI{}, "projects_workflow_chains_start", mustArgs(t, map[string]any{
		"id":         "project-1",
		"chain_ref":  "chain-1",
		"input_text": "GENERIC-1044",
	}))
	if err != nil {
		t.Fatalf("start alias: %v", err)
	}
	if result["isError"] != false {
		t.Fatalf("expected non-error tool result marker, got %#v", result["isError"])
	}
	structured := result["structuredContent"].(projectworkflowchain.StartResult)
	if structured.InputRef != "jira:GENERIC-1044" {
		t.Fatalf("unexpected structured result: %#v", structured)
	}
}

func TestCallToolRetryGitOpsUsesSafeRefsOnly(t *testing.T) {
	result, err := CallTool(context.Background(), fakeChainAPI{}, "projects.workflow_chains.retry_gitops", mustArgs(t, map[string]any{
		"id":           "project-1",
		"chain_run_id": "workflow_chain_run_1",
	}))
	if err != nil {
		t.Fatalf("retry gitops: %v", err)
	}
	structured := result["structuredContent"].(projectworkflowchain.ChainRun)
	if structured.PullRequestRef != "github-pr-1044" {
		t.Fatalf("unexpected structured result: %#v", structured)
	}
}

func TestCallToolGetAndListPreserveWorkflowChainHandoffMetadata(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		tool      string
		args      map[string]any
		assertRun func(*testing.T, projectworkflowchain.ChainRun)
	}{
		{
			name: "get",
			tool: "projects.workflow_chains.get",
			args: map[string]any{"id": "project-1", "chain_run_id": "workflow_chain_run_1"},
			assertRun: func(t *testing.T, run projectworkflowchain.ChainRun) {
				t.Helper()
				assertCompletedChainHandoff(t, run)
			},
		},
		{
			name: "list",
			tool: "projects.workflow_chains.list",
			args: map[string]any{"id": "project-1", "status": projectworkflowchain.ChainStatusCompleted},
			assertRun: func(t *testing.T, run projectworkflowchain.ChainRun) {
				t.Helper()
				assertCompletedChainHandoff(t, run)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := CallTool(ctx, fakeChainAPI{}, tc.tool, mustArgs(t, tc.args))
			if err != nil {
				t.Fatalf("%s: %v", tc.tool, err)
			}
			body := result["content"].([]map[string]string)[0]["text"]
			if !strings.Contains(body, `"pull_request_ref":"github-pr-1044"`) || !strings.Contains(body, `"next_action":"workflow chain completed with draft PR GitOps output"`) {
				t.Fatalf("tool content lost handoff refs/actions: %s", body)
			}
			switch structured := result["structuredContent"].(type) {
			case projectworkflowchain.ChainRun:
				tc.assertRun(t, structured)
			case projectworkflowchain.ListResult:
				if len(structured.Runs) != 1 {
					t.Fatalf("expected one listed run, got %#v", structured)
				}
				tc.assertRun(t, structured.Runs[0])
			default:
				t.Fatalf("unexpected structuredContent type %T: %#v", structured, structured)
			}
		})
	}
}

func TestCallToolStartReturnsSafeRefsOnly(t *testing.T) {
	result, err := CallTool(context.Background(), fakeChainAPI{}, "projects.workflow_chains.start", mustArgs(t, map[string]any{
		"id":         "project-1",
		"chain_ref":  "chain-1",
		"input_text": "GENERIC-1044",
		"dry_run":    true,
	}))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if result["isError"] != false {
		t.Fatalf("expected non-error tool result marker, got %#v", result["isError"])
	}
	body := result["content"].([]map[string]string)[0]["text"]
	if strings.Contains(body, `"input_text"`) || strings.Contains(body, "raw prompt") {
		t.Fatalf("result leaked raw input field: %s", body)
	}
	structured := result["structuredContent"].(projectworkflowchain.StartResult)
	if structured.InputRef != "jira:GENERIC-1044" {
		t.Fatalf("unexpected structured result: %#v", structured)
	}
}

func TestCallToolGenericChainUsesRealCompilerAndPreservesGeneratedHandoffs(t *testing.T) {
	ctx := context.Background()
	workPlanStore := workplanstore.NewMemoryStore()
	workPlans := projectworkplan.New(workPlanStore)
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  projectautomation.RunnerExecutionExternal,
		MaxParallelTasks: 2,
		PermissionResolver: mcpRealPermissionResolver{
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
		if _, err := workflows.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: "project-1", Data: data, CreatedByRunID: "mcp-import", TraceID: "trace-mcp-real"}); err != nil {
			t.Fatalf("import workflow %s: %v", path, err)
		}
	}
	chainSvc := projectworkflowchain.New(chainstore.NewMemoryStore(), workflows, workPlans, []projectworkflowchain.Config{mcpGenericChainConfig()})
	finalizer := &mcpGitOpsFinalizer{result: projectworkflowchain.GitOpsFinalizeResult{
		CommitRef:      "commit/generic-mcp-2044",
		PushRef:        "push/generic-mcp-2044",
		PullRequestRef: "github-pr-2044",
		EvidenceRefs:   []string{"gitops-evidence:generic-mcp-2044"},
	}}
	chainSvc.SetGitOpsFinalizer(finalizer)
	chainSvc.SetAutomationAPI(automations)
	workPlans.SetStatusChangeHandler(mcpStatusFanout{handlers: []projectworkplan.WorkPlanStatusChangeHandler{automations, chainSvc}})

	started, err := CallTool(ctx, chainSvc, "projects.workflow_chains.start", mustArgs(t, map[string]any{
		"id":                "project-1",
		"chain_ref":         "generic-chain",
		"input_text":        "ticket/GENERIC-2044",
		"created_by_run_id": "codex-mcp-run-2044",
		"trace_id":          "trace-generic-mcp-2044",
	}))
	if err != nil {
		t.Fatalf("real MCP start: %v", err)
	}
	startResult := started["structuredContent"].(projectworkflowchain.StartResult)
	if startResult.Status != projectworkflowchain.ChainStatusQueued || startResult.InputRef != "input:ticket/GENERIC-2044" || len(startResult.WorkPlanIDs) != 1 || len(startResult.AutomationIDs) == 0 {
		t.Fatalf("MCP start lost generated handoff refs: %#v", startResult)
	}
	if strings.Contains(started["content"].([]map[string]string)[0]["text"], `"input_text"`) {
		t.Fatalf("MCP start leaked raw input field: %s", started["content"].([]map[string]string)[0]["text"])
	}
	for _, stageRef := range []string{"decomposition", "implementation", "post-validation"} {
		run, err := chainSvc.Get(ctx, "project-1", startResult.ChainRunID)
		if err != nil {
			t.Fatalf("get chain before %s: %v", stageRef, err)
		}
		stage := mcpStageRunByRef(t, run, stageRef)
		mcpAssertQueuedRuns(t, ctx, automations, "project-1", stage)
		if stageRef == "decomposition" {
			if _, err := workPlans.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
				ProjectID:               "project-1",
				PlanID:                  stage.WorkPlanID,
				TaskRef:                 "generic-2044-mcp-implementation-slice",
				Title:                   "Implement GENERIC-2044 MCP Slice",
				Status:                  projectworkplan.WorkTaskStatusPlanned,
				OwnerAgent:              "developer",
				FilesToEdit:             []string{"internal/projectworkflowchain/service.go"},
				VerificationRequirement: "focused workflow-chain MCP tests",
				ReviewResultRefs:        []string{"review:mcp-planning-readiness-approved"},
				VerifierResultRefs:      []string{"verifier:mcp-planning-readiness"},
				DecompositionQuality:    projectworkplan.DecompositionReady,
				AcceptanceCriteria:      []string{"MCP implementation slice is executable from task metadata."},
				StopConditions:          []string{"Stop if MCP workflow-chain scope changes."},
				VerifierLadder:          []string{"focused workflow-chain MCP tests"},
				RegressionApplicability: "required for MCP workflow-chain behavior",
				DownstreamImpactRefs:    []string{"workflow-chain-mcp-impact-ref"},
				OutputContract:          "bounded diff refs and verifier refs",
			}); err != nil {
				t.Fatalf("create MCP implementation child: %v", err)
			}
			mcpCompleteGeneratedPlanWithOpenChildren(t, ctx, workPlans, workPlanStore, chainSvc, "project-1", stage.WorkPlanID, stageRef)
			continue
		}
		mcpCompleteGeneratedPlan(t, ctx, workPlans, "project-1", stage.WorkPlanID, stageRef)
	}
	got, err := CallTool(ctx, chainSvc, "projects.workflow_chains.get", mustArgs(t, map[string]any{
		"id":           "project-1",
		"chain_run_id": startResult.ChainRunID,
	}))
	if err != nil {
		t.Fatalf("real MCP get: %v", err)
	}
	run := got["structuredContent"].(projectworkflowchain.ChainRun)
	if run.Status != projectworkflowchain.ChainStatusCompleted || run.PullRequestRef != "github-pr-2044" || run.NextAction != "workflow chain completed with draft PR GitOps output" {
		t.Fatalf("MCP get lost completed status/action/PR refs: %#v", run)
	}
	if len(finalizer.inputs) != 1 || finalizer.inputs[0].InputRef != "input:ticket/GENERIC-2044" || finalizer.inputs[0].CreatedByRunID != "codex-mcp-run-2044" || finalizer.inputs[0].TraceID != "trace-generic-mcp-2044" {
		t.Fatalf("GitOps finalizer lost MCP chain refs: %#v", finalizer.inputs)
	}
	if !containsString(finalizer.inputs[0].AllowedPathspecs, "internal/projectworkflowchain/service.go") || !containsString(finalizer.inputs[0].ReviewRefs, "review:mcp-generated-implementation") || !containsString(finalizer.inputs[0].VerifierRefs, "verifier:mcp-generated-post-validation") {
		t.Fatalf("GitOps finalizer lost generated task outputs: %#v", finalizer.inputs[0])
	}
	listed, err := CallTool(ctx, chainSvc, "projects.workflow_chains.list", mustArgs(t, map[string]any{"id": "project-1", "status": projectworkflowchain.ChainStatusCompleted}))
	if err != nil {
		t.Fatalf("real MCP list: %v", err)
	}
	listResult := listed["structuredContent"].(projectworkflowchain.ListResult)
	if len(listResult.Runs) != 1 || listResult.Runs[0].PullRequestRef != "github-pr-2044" {
		t.Fatalf("MCP list lost completed chain refs: %#v", listResult)
	}
}

func TestBaselineChainMcpReadModelMatchesStoreAfterBlockedActivation(t *testing.T) {
	ctx := context.Background()
	workPlanStore := workplanstore.NewMemoryStore()
	workPlans := projectworkplan.New(workPlanStore)
	automations := projectautomation.New(automationstore.NewMemoryStore(), workPlans, projectautomation.Options{
		Enabled:          true,
		RunnerEnabled:    true,
		RunnerExecution:  projectautomation.RunnerExecutionExternal,
		MaxParallelTasks: 2,
		PermissionResolver: mcpRealPermissionResolver{
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
		if _, err := workflows.ImportWorkflowTOML(ctx, projectworkflow.ImportWorkflowTOMLInput{ProjectID: "project-1", Data: data, CreatedByRunID: "mcp-import", TraceID: "trace-mcp-blocked"}); err != nil {
			t.Fatalf("import workflow %s: %v", path, err)
		}
	}
	chainStore := chainstore.NewMemoryStore()
	chainSvc := projectworkflowchain.New(chainStore, workflows, workPlans, []projectworkflowchain.Config{mcpGenericChainConfig()})
	finalizer := &mcpGitOpsFinalizer{err: errors.New("git push failed")}
	chainSvc.SetGitOpsFinalizer(finalizer)
	chainSvc.SetAutomationAPI(automations)
	workPlans.SetStatusChangeHandler(mcpStatusFanout{handlers: []projectworkplan.WorkPlanStatusChangeHandler{automations, chainSvc}})

	started, err := CallTool(ctx, chainSvc, "projects.workflow_chains.start", mustArgs(t, map[string]any{
		"id":                "project-1",
		"chain_ref":         "generic-chain",
		"input_text":        "ticket/GENERIC-2044",
		"created_by_run_id": "codex-mcp-run-blocked-2044",
		"trace_id":          "trace-generic-mcp-blocked-2044",
	}))
	if err != nil {
		t.Fatalf("real MCP start: %v", err)
	}
	startResult := started["structuredContent"].(projectworkflowchain.StartResult)

	run, err := chainSvc.Get(ctx, "project-1", startResult.ChainRunID)
	if err != nil {
		t.Fatalf("get chain before decomposition: %v", err)
	}
	decomposition := mcpStageRunByRef(t, run, "decomposition")
	if _, err := workPlans.CreateWorkTask(ctx, projectworkplan.CreateWorkTaskInput{
		ProjectID:               "project-1",
		PlanID:                  decomposition.WorkPlanID,
		TaskRef:                 "generic-2044-mcp-blocked-implementation-slice",
		Title:                   "Implement GENERIC-2044 Blocked Slice",
		Status:                  projectworkplan.WorkTaskStatusPlanned,
		OwnerAgent:              "developer",
		FilesToEdit:             []string{"internal/projectworkflowchain/service.go"},
		VerificationRequirement: "focused workflow-chain MCP tests",
		ReviewResultRefs:        []string{"review:mcp-planning-readiness-approved"},
		VerifierResultRefs:      []string{"verifier:mcp-planning-readiness"},
		DecompositionQuality:    projectworkplan.DecompositionReady,
		AcceptanceCriteria:      []string{"MCP implementation slice is executable from task metadata."},
		StopConditions:          []string{"Stop if MCP workflow-chain scope changes."},
		VerifierLadder:          []string{"focused workflow-chain MCP tests"},
		RegressionApplicability: "required for MCP workflow-chain behavior",
		DownstreamImpactRefs:    []string{"workflow-chain-mcp-impact-ref"},
		OutputContract:          "bounded diff refs and verifier refs",
	}); err != nil {
		t.Fatalf("create MCP implementation child: %v", err)
	}
	mcpCompleteGeneratedPlanWithOpenChildren(t, ctx, workPlans, workPlanStore, chainSvc, "project-1", decomposition.WorkPlanID, "decomposition")

	run, err = chainSvc.Get(ctx, "project-1", startResult.ChainRunID)
	if err != nil {
		t.Fatalf("get chain before implementation: %v", err)
	}
	implementation := mcpStageRunByRef(t, run, "implementation")
	mcpCompleteGeneratedPlan(t, ctx, workPlans, "project-1", implementation.WorkPlanID, "implementation")

	run, err = chainSvc.Get(ctx, "project-1", startResult.ChainRunID)
	if err != nil {
		t.Fatalf("get chain before post-validation: %v", err)
	}
	postValidation := mcpStageRunByRef(t, run, "post-validation")
	tasks, err := workPlans.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: "project-1", PlanID: postValidation.WorkPlanID})
	if err != nil {
		t.Fatalf("list post-validation tasks: %v", err)
	}
	for _, task := range tasks {
		task.Status = projectworkplan.WorkTaskStatusDone
		task.Outcome = "MCP generated post-validation output accepted"
		task.EvidenceRefs = appendUniqueString(task.EvidenceRefs, "evidence:mcp-generated-post-validation")
		task.ReviewResultRefs = appendUniqueString(task.ReviewResultRefs, "review:mcp-generated-post-validation")
		task.VerifierResultRefs = appendUniqueString(task.VerifierResultRefs, "verifier:mcp-generated-post-validation")
		if _, err := workPlans.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("update post-validation task %s: %v", task.TaskRef, err)
		}
	}
	plan, err := workPlanStore.GetWorkPlan(ctx, "project-1", postValidation.WorkPlanID)
	if err != nil {
		t.Fatalf("get post-validation plan: %v", err)
	}
	oldStatus := plan.Status
	plan.Status = projectworkplan.WorkPlanStatusDone
	plan.Outcome = "MCP generated post-validation stage completed"
	plan.ResumeSummary = "finalize MCP generic chain GitOps"
	updated, err := workPlanStore.UpdateWorkPlan(ctx, plan)
	if err != nil {
		t.Fatalf("mark post-validation plan done in store: %v", err)
	}
	if err := chainSvc.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID:  "project-1",
		PlanID:     postValidation.WorkPlanID,
		PlanRef:    updated.PlanRef,
		OldStatus:  oldStatus,
		NewStatus:  updated.Status,
		OwnerAgent: updated.OwnerAgent,
		ChangedAt:  updated.UpdatedAt,
	}); err == nil {
		t.Fatalf("expected GitOps finalization failure to block chain")
	}

	storeRun, err := chainStore.GetChainRun(ctx, "project-1", startResult.ChainRunID)
	if err != nil {
		t.Fatalf("store read of blocked chain: %v", err)
	}
	if storeRun.Status != projectworkflowchain.ChainStatusBlocked || !storeRun.GitOpsReady || storeRun.GitOpsAttemptCount != 1 || storeRun.GitOpsFailureCategory == "" || storeRun.GitOpsRecoveryStatus == "" {
		t.Fatalf("store must hold blocked GitOps-ready chain before MCP read comparison: %#v", storeRun)
	}
	got, err := CallTool(ctx, chainSvc, "projects.workflow_chains.get", mustArgs(t, map[string]any{
		"id":           "project-1",
		"chain_run_id": startResult.ChainRunID,
	}))
	if err != nil {
		t.Fatalf("MCP get of blocked chain: %v", err)
	}
	mcpRun := got["structuredContent"].(projectworkflowchain.ChainRun)
	if mcpRun.Status != storeRun.Status || mcpRun.NextAction != storeRun.NextAction || mcpRun.PullRequestRef != storeRun.PullRequestRef {
		t.Fatalf("MCP read model diverged from store status/next_action/pr:\n mcp=%#v\nstore=%#v", mcpRun, storeRun)
	}
	if mcpRun.GitOpsReady != storeRun.GitOpsReady || mcpRun.GitOpsAttemptCount != storeRun.GitOpsAttemptCount || mcpRun.GitOpsFailureCategory != storeRun.GitOpsFailureCategory || mcpRun.GitOpsRecoveryStatus != storeRun.GitOpsRecoveryStatus {
		t.Fatalf("MCP read model diverged from store GitOps recovery fields:\n mcp=%#v\nstore=%#v", mcpRun, storeRun)
	}
	if !reflect.DeepEqual(mcpRun.GitOpsFailureEvidenceRefs, storeRun.GitOpsFailureEvidenceRefs) {
		t.Fatalf("MCP read model diverged from store GitOps evidence refs: mcp=%#v store=%#v", mcpRun.GitOpsFailureEvidenceRefs, storeRun.GitOpsFailureEvidenceRefs)
	}
	if len(mcpRun.StageRuns) != len(storeRun.StageRuns) {
		t.Fatalf("MCP read model diverged on stage count: mcp=%#v store=%#v", mcpRun.StageRuns, storeRun.StageRuns)
	}
	for i := range storeRun.StageRuns {
		if mcpRun.StageRuns[i].StageRef != storeRun.StageRuns[i].StageRef || mcpRun.StageRuns[i].Status != storeRun.StageRuns[i].Status || mcpRun.StageRuns[i].BlockedReason != storeRun.StageRuns[i].BlockedReason {
			t.Fatalf("MCP read model diverged on stage %d:\n mcp=%#v\nstore=%#v", i, mcpRun.StageRuns[i], storeRun.StageRuns[i])
		}
	}
	listed, err := CallTool(ctx, chainSvc, "projects.workflow_chains.list", mustArgs(t, map[string]any{"id": "project-1", "status": projectworkflowchain.ChainStatusBlocked}))
	if err != nil {
		t.Fatalf("MCP list of blocked chain: %v", err)
	}
	listResult := listed["structuredContent"].(projectworkflowchain.ListResult)
	if len(listResult.Runs) != 1 {
		t.Fatalf("expected one blocked run from MCP list, got %#v", listResult)
	}
	listedRun := listResult.Runs[0]
	if listedRun.Status != storeRun.Status || listedRun.GitOpsReady != storeRun.GitOpsReady || listedRun.GitOpsAttemptCount != storeRun.GitOpsAttemptCount || listedRun.GitOpsFailureCategory != storeRun.GitOpsFailureCategory || listedRun.GitOpsRecoveryStatus != storeRun.GitOpsRecoveryStatus || listedRun.NextAction != storeRun.NextAction {
		t.Fatalf("MCP list read model diverged from store:\n mcp=%#v\nstore=%#v", listedRun, storeRun)
	}
}

type fakeChainAPI struct{}

func (fakeChainAPI) CallWorkflowChainTool(_ context.Context, name string, _ json.RawMessage) (any, error) {
	if name == "projects.workflow_chains.retry_gitops" {
		return completedChainRun(), nil
	}
	if name == "projects.workflow_chains.get" {
		return completedChainRun(), nil
	}
	if name == "projects.workflow_chains.list" {
		return projectworkflowchain.ListResult{Runs: []projectworkflowchain.ChainRun{completedChainRun()}}, nil
	}
	if name != "projects.workflow_chains.start" {
		return nil, projectworkflowchain.ErrInvalidInput
	}
	return projectworkflowchain.StartResult{ProjectID: "project-1", ChainRef: "chain-1", InputRef: "jira:GENERIC-1044", Status: projectworkflowchain.ChainStatusPlanned, DryRun: true}, nil
}

func completedChainRun() projectworkflowchain.ChainRun {
	return projectworkflowchain.ChainRun{
		ID:             "workflow_chain_run_1",
		ProjectID:      "project-1",
		ChainRef:       "chain-1",
		InputRef:       "jira:GENERIC-1044",
		Status:         projectworkflowchain.ChainStatusCompleted,
		AutomationIDs:  []string{"automation-decomposition", "automation-implementation", "automation-validation"},
		GitOpsReady:    false,
		PullRequestRef: "github-pr-1044",
		NextAction:     "workflow chain completed with draft PR GitOps output",
		StageRuns: []projectworkflowchain.StageRun{
			{StageRef: "decomposition", WorkflowID: "workflow-decomposition", Status: projectworkflowchain.StageStatusCompleted, AutomationIDs: []string{"automation-decomposition"}},
			{StageRef: "implementation", WorkflowID: "workflow-implementation", Status: projectworkflowchain.StageStatusCompleted, WorkPlanID: "plan-implementation", WorkTaskIDs: []string{"task-implementation"}, AutomationIDs: []string{"automation-implementation"}},
			{StageRef: "post-validation", WorkflowID: "workflow-validation", Status: projectworkflowchain.StageStatusCompleted, WorkPlanID: "plan-post-validation", WorkTaskIDs: []string{"task-post-validation"}, AutomationIDs: []string{"automation-validation"}},
		},
	}
}

func assertCompletedChainHandoff(t *testing.T, run projectworkflowchain.ChainRun) {
	t.Helper()
	if run.Status != projectworkflowchain.ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "github-pr-1044" || run.NextAction != "workflow chain completed with draft PR GitOps output" {
		t.Fatalf("chain handoff status/actions were not preserved: %#v", run)
	}
	if len(run.AutomationIDs) != 3 || len(run.StageRuns) != 3 {
		t.Fatalf("chain handoff refs were not preserved: %#v", run)
	}
	if run.StageRuns[2].StageRef != "post-validation" || run.StageRuns[2].WorkflowID != "workflow-validation" || len(run.StageRuns[2].AutomationIDs) != 1 {
		t.Fatalf("post-validation stage handoff metadata was not preserved: %#v", run.StageRuns[2])
	}
}

type mcpStatusFanout struct {
	handlers []projectworkplan.WorkPlanStatusChangeHandler
}

func (fanout mcpStatusFanout) HandleWorkPlanStatusChanged(ctx context.Context, change projectworkplan.WorkPlanStatusChange) error {
	for _, handler := range fanout.handlers {
		if handler == nil {
			continue
		}
		if err := handler.HandleWorkPlanStatusChanged(ctx, change); err != nil {
			return err
		}
	}
	return nil
}

type mcpRealPermissionResolver struct {
	allowedRunnerKinds []string
}

func (resolver mcpRealPermissionResolver) CheckAutomationPermission(_ context.Context, input projectautomation.PermissionCheckInput) (projectautomation.PermissionSnapshotMetadata, error) {
	return projectautomation.PermissionSnapshotMetadata{
		PermissionRef:      input.PermissionRef,
		AgentID:            input.AgentID,
		AllowedRunnerKinds: append([]string(nil), resolver.allowedRunnerKinds...),
	}, nil
}

type mcpGitOpsFinalizer struct {
	result projectworkflowchain.GitOpsFinalizeResult
	inputs []projectworkflowchain.GitOpsFinalizeInput
	err    error
}

func (finalizer *mcpGitOpsFinalizer) FinalizeWorkflowChain(_ context.Context, input projectworkflowchain.GitOpsFinalizeInput) (projectworkflowchain.GitOpsFinalizeResult, error) {
	finalizer.inputs = append(finalizer.inputs, input)
	if finalizer.err != nil {
		return projectworkflowchain.GitOpsFinalizeResult{}, finalizer.err
	}
	return finalizer.result, nil
}

func mcpGenericChainConfig() projectworkflowchain.Config {
	return projectworkflowchain.Config{
		ProjectID:            "project-1",
		ChainRef:             "generic-chain",
		Enabled:              true,
		InputKind:            projectworkflowchain.InputKindSafeRef,
		InputPattern:         "^ticket/GENERIC-[0-9]+$",
		ContextProvider:      projectworkflowchain.ContextProviderIndexedRepo,
		DefaultTitleTemplate: "{{input_ref}} generic MCP delivery",
		GitOpsMode:           projectworkflowchain.GitOpsModeDraftPRAfterValidation,
		GitOpsEnabled:        true,
		Stages: []projectworkflowchain.StageConfig{
			{StageRef: "decomposition", WorkflowRef: "governed-decomposition-planning", Trigger: projectworkflowchain.TriggerOnChainStart, RequiredStatusBeforeNext: projectworkflowchain.StageStatusCompleted},
			{StageRef: "implementation", WorkflowRef: "governed-workplan-implementation", Trigger: projectworkflowchain.TriggerAfterStageReviewPassed, DependsOn: []string{"decomposition"}, RequiredStatusBeforeNext: projectworkflowchain.StageStatusCompleted},
			{StageRef: "post-validation", WorkflowRef: "governed-post-implementation-validation", Trigger: projectworkflowchain.TriggerAfterStageReviewPassed, DependsOn: []string{"implementation"}, RequiredStatusBeforeNext: projectworkflowchain.StageStatusCompleted},
		},
	}
}

func mcpStageRunByRef(t *testing.T, run projectworkflowchain.ChainRun, stageRef string) projectworkflowchain.StageRun {
	t.Helper()
	for _, stage := range run.StageRuns {
		if stage.StageRef == stageRef {
			if stage.WorkPlanID == "" || len(stage.WorkTaskIDs) == 0 || len(stage.AutomationIDs) == 0 {
				t.Fatalf("stage %s lost generated refs: %#v", stageRef, stage)
			}
			return stage
		}
	}
	t.Fatalf("stage %s not found: %#v", stageRef, run.StageRuns)
	return projectworkflowchain.StageRun{}
}

func mcpAssertQueuedRuns(t *testing.T, ctx context.Context, svc *projectautomation.Service, projectID string, stage projectworkflowchain.StageRun) {
	t.Helper()
	runs, err := svc.ListRuns(ctx, projectautomation.RunFilter{ProjectID: projectID, PlanID: stage.WorkPlanID, Status: projectautomation.RunStatusQueued})
	if err != nil {
		t.Fatalf("list queued runs for %s: %v", stage.StageRef, err)
	}
	if len(runs) == 0 {
		t.Fatalf("stage %s did not queue automation through status fanout", stage.StageRef)
	}
	taskIDs := map[string]struct{}{}
	for _, taskID := range stage.WorkTaskIDs {
		taskIDs[taskID] = struct{}{}
	}
	for _, run := range runs {
		if run.OrchestratorRunID == "" || run.TaskID == "" || run.WorkTaskStatus != projectworkplan.WorkTaskStatusReady {
			t.Fatalf("queued run lost live refs/status for %s: %#v", stage.StageRef, run)
		}
		if _, ok := taskIDs[run.TaskID]; !ok {
			t.Fatalf("queued run references task outside generated stage %s: %#v", stage.StageRef, run)
		}
	}
}

func mcpCompleteGeneratedPlan(t *testing.T, ctx context.Context, svc *projectworkplan.Service, projectID string, planID string, stageRef string) {
	t.Helper()
	tasks, err := svc.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		t.Fatalf("list tasks for %s: %v", stageRef, err)
	}
	for _, task := range tasks {
		if stageRef == "implementation" && len(task.FilesToEdit) == 0 {
			task.FilesToEdit = []string{"internal/projectworkflowchain/service.go", "cmd/mivia-automation-runner/main_test.go"}
		}
		task.Status = projectworkplan.WorkTaskStatusDone
		task.Outcome = "MCP generated " + stageRef + " output accepted"
		task.EvidenceRefs = appendUniqueString(task.EvidenceRefs, "evidence:mcp-generated-"+stageRef)
		task.ReviewResultRefs = appendUniqueString(task.ReviewResultRefs, "review:mcp-generated-"+stageRef)
		task.VerifierResultRefs = appendUniqueString(task.VerifierResultRefs, "verifier:mcp-generated-"+stageRef)
		if _, err := svc.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("update generated task %s/%s: %v", stageRef, task.TaskRef, err)
		}
	}
	if _, err := svc.UpdateWorkPlanStatus(ctx, projectworkplan.UpdateWorkPlanStatusInput{
		ProjectID:      projectID,
		PlanID:         planID,
		Status:         projectworkplan.WorkPlanStatusDone,
		Outcome:        "MCP generated " + stageRef + " stage completed",
		SafeNextAction: "advance MCP generic chain",
		RunID:          "mcp-complete-" + stageRef,
		TraceID:        "trace-generic-mcp-2044",
	}); err != nil {
		t.Fatalf("mark generated plan %s done: %v", stageRef, err)
	}
}

func mcpCompleteGeneratedPlanWithOpenChildren(t *testing.T, ctx context.Context, svc *projectworkplan.Service, store *workplanstore.MemoryStore, chain *projectworkflowchain.Service, projectID string, planID string, stageRef string) {
	t.Helper()
	tasks, err := svc.ListOpenWorkTasks(ctx, projectworkplan.WorkTaskFilter{ProjectID: projectID, PlanID: planID})
	if err != nil {
		t.Fatalf("list tasks for %s: %v", stageRef, err)
	}
	for _, task := range tasks {
		if !mcpWorkflowWrapperTaskRef(task.TaskRef) && !strings.HasPrefix(task.TaskRef, "review-") {
			continue
		}
		task.Status = projectworkplan.WorkTaskStatusDone
		task.Outcome = "MCP generated " + stageRef + " wrapper output accepted"
		task.EvidenceRefs = appendUniqueString(task.EvidenceRefs, "evidence:mcp-generated-"+stageRef)
		task.ReviewResultRefs = appendUniqueString(task.ReviewResultRefs, "review:mcp-generated-"+stageRef)
		task.VerifierResultRefs = appendUniqueString(task.VerifierResultRefs, "verifier:mcp-generated-"+stageRef)
		if _, err := svc.UpdateWorkTask(ctx, task); err != nil {
			t.Fatalf("update generated wrapper task %s/%s: %v", stageRef, task.TaskRef, err)
		}
	}
	plan, err := store.GetWorkPlan(ctx, projectID, planID)
	if err != nil {
		t.Fatalf("get generated plan %s: %v", stageRef, err)
	}
	oldStatus := plan.Status
	plan.Status = projectworkplan.WorkPlanStatusDone
	plan.Outcome = "MCP generated " + stageRef + " stage completed"
	plan.ResumeSummary = "advance MCP generic chain"
	updated, err := store.UpdateWorkPlan(ctx, plan)
	if err != nil {
		t.Fatalf("mark generated plan %s done in store: %v", stageRef, err)
	}
	if err := chain.HandleWorkPlanStatusChanged(ctx, projectworkplan.WorkPlanStatusChange{
		ProjectID:  projectID,
		PlanID:     planID,
		PlanRef:    updated.PlanRef,
		OldStatus:  oldStatus,
		NewStatus:  updated.Status,
		OwnerAgent: updated.OwnerAgent,
		ChangedAt:  updated.UpdatedAt,
	}); err != nil {
		t.Fatalf("advance MCP chain after %s: %v", stageRef, err)
	}
}

func mcpWorkflowWrapperTaskRef(ref string) bool {
	switch strings.TrimSpace(ref) {
	case "discover-planning-context", "decompose-work-plan", "mark-ready-after-review", "select-ready-tasks", "run-implementation-batch", "review-implementation-batch", "orchestrator-verification", "pr-gitops-readiness", "post-implementation-validation":
		return true
	default:
		return false
	}
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func mustArgs(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}
