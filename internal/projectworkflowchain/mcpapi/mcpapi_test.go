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
	for _, name := range []string{"projects.workflow_chains.start", "projects.workflow_chains.get", "projects.workflow_chains.list"} {
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
	structured := result["structuredContent"].(projectworkflowchain.StartResult)
	if structured.InputRef != "jira:MASS-1044" {
		t.Fatalf("unexpected structured result: %#v", structured)
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
	if name != "projects.workflow_chains.start" {
		return nil, projectworkflowchain.ErrInvalidInput
	}
	return projectworkflowchain.StartResult{ProjectID: "project-1", ChainRef: "chain-1", InputRef: "jira:MASS-1044", Status: projectworkflowchain.ChainStatusPlanned, DryRun: true}, nil
}

func mustArgs(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return raw
}
