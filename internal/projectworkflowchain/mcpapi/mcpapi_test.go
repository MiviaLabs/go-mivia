package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
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
		"input_text": "MASS-1044",
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
		"input_text": "MASS-1044",
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
		"input_text": "MASS-1044",
	}))
	if err != nil {
		t.Fatalf("start alias: %v", err)
	}
	if result["isError"] != false {
		t.Fatalf("expected non-error tool result marker, got %#v", result["isError"])
	}
	structured := result["structuredContent"].(projectworkflowchain.StartResult)
	if structured.InputRef != "jira:MASS-1044" {
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
	if structured.PullRequestRef != "pr/MASS-1044" {
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
			if !strings.Contains(body, `"pull_request_ref":"pr/MASS-1044"`) || !strings.Contains(body, `"next_action":"workflow chain completed with draft PR GitOps output"`) {
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
		"input_text": "MASS-1044",
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
	if structured.InputRef != "jira:MASS-1044" {
		t.Fatalf("unexpected structured result: %#v", structured)
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
	return projectworkflowchain.StartResult{ProjectID: "project-1", ChainRef: "chain-1", InputRef: "jira:MASS-1044", Status: projectworkflowchain.ChainStatusPlanned, DryRun: true}, nil
}

func completedChainRun() projectworkflowchain.ChainRun {
	return projectworkflowchain.ChainRun{
		ID:             "workflow_chain_run_1",
		ProjectID:      "project-1",
		ChainRef:       "chain-1",
		InputRef:       "jira:MASS-1044",
		Status:         projectworkflowchain.ChainStatusCompleted,
		AutomationIDs:  []string{"automation-decomposition", "automation-implementation", "automation-validation"},
		GitOpsReady:    false,
		PullRequestRef: "pr/MASS-1044",
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
	if run.Status != projectworkflowchain.ChainStatusCompleted || run.GitOpsReady || run.PullRequestRef != "pr/MASS-1044" || run.NextAction != "workflow chain completed with draft PR GitOps output" {
		t.Fatalf("chain handoff status/actions were not preserved: %#v", run)
	}
	if len(run.AutomationIDs) != 3 || len(run.StageRuns) != 3 {
		t.Fatalf("chain handoff refs were not preserved: %#v", run)
	}
	if run.StageRuns[2].StageRef != "post-validation" || run.StageRuns[2].WorkflowID != "workflow-validation" || len(run.StageRuns[2].AutomationIDs) != 1 {
		t.Fatalf("post-validation stage handoff metadata was not preserved: %#v", run.StageRuns[2])
	}
}

func mustArgs(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}
