package mcpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolDefinitionsExposeCodexAndParallelRequirements(t *testing.T) {
	definitions := ToolDefinitions()
	var runDescription string
	var batchDescription string
	var claimDescription string
	var completeDescription string
	for _, definition := range definitions {
		switch definition["name"] {
		case "projects.automations.run":
			runDescription, _ = definition["description"].(string)
		case "projects.automations.run_parallel_batch":
			batchDescription, _ = definition["description"].(string)
		case "projects.automation_runs.claim_next":
			claimDescription, _ = definition["description"].(string)
		case "projects.automation_runs.complete_attempt":
			completeDescription, _ = definition["description"].(string)
		}
	}
	if !strings.Contains(runDescription, "MUST use codex_cli") {
		t.Fatalf("run description does not enforce codex_cli: %q", runDescription)
	}
	if !strings.Contains(runDescription, "MUST NOT fall back to manual") {
		t.Fatalf("run description does not forbid fallback: %q", runDescription)
	}
	if !strings.Contains(batchDescription, "orchestrator-owned") {
		t.Fatalf("batch description does not require orchestrator ownership: %q", batchDescription)
	}
	if !strings.Contains(batchDescription, "independent ready Work Tasks") {
		t.Fatalf("batch description does not require independent ready tasks: %q", batchDescription)
	}
	if !strings.Contains(claimDescription, "logged-in Codex environment") {
		t.Fatalf("claim description does not require local logged-in runner: %q", claimDescription)
	}
	if !strings.Contains(completeDescription, "does not complete the Work Task") {
		t.Fatalf("complete description does not preserve orchestrator verification: %q", completeDescription)
	}
}

func TestIsAutomationToolAcceptsUnderscoreAlias(t *testing.T) {
	if !IsAutomationTool("projects_automations_run_parallel_batch") {
		t.Fatal("expected underscore alias to be accepted")
	}
	if !IsAutomationTool("projects_automation_runs_claim_next") {
		t.Fatal("expected external runner alias to be accepted")
	}
	if !IsAutomationTool("projects_automations_update_status") {
		t.Fatal("expected update status alias to be accepted")
	}
}

func TestCallToolAcceptsProjectIDAlias(t *testing.T) {
	api := &captureAutomationAPI{}
	_, err := CallTool(context.Background(), api, "projects.automations.list", json.RawMessage(`{"project_id":"example-service"}`))
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if api.name != "projects.automations.list" {
		t.Fatalf("unexpected tool name: %q", api.name)
	}
	var arguments map[string]string
	if err := json.Unmarshal(api.arguments, &arguments); err != nil {
		t.Fatalf("unmarshal normalized arguments: %v", err)
	}
	if arguments["id"] != "example-service" {
		t.Fatalf("project_id alias was not normalized to id: %#v", arguments)
	}
	if _, ok := arguments["project_id"]; ok {
		t.Fatalf("project_id should be stripped before strict service decoding: %#v", arguments)
	}
}

func TestToolDefinitionsAllowIDOrProjectID(t *testing.T) {
	for _, definition := range ToolDefinitions() {
		schema, ok := definition["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("missing schema for %v", definition["name"])
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("missing schema properties for %v", definition["name"])
		}
		if _, ok := properties["project_id"]; !ok {
			t.Fatalf("%v schema does not expose project_id", definition["name"])
		}
		if _, ok := schema["anyOf"]; !ok {
			t.Fatalf("%v schema does not allow id/project_id alternatives", definition["name"])
		}
	}
}

func TestToolDefinitionsExposeAutomationStatusUpdate(t *testing.T) {
	for _, definition := range ToolDefinitions() {
		if definition["name"] != "projects.automations.update_status" {
			continue
		}
		schema := definition["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		status := properties["status"].(map[string]any)
		values := status["enum"].([]string)
		for _, value := range values {
			if value == "disabled" {
				return
			}
		}
		t.Fatalf("status enum does not include disabled: %#v", values)
	}
	t.Fatal("missing projects.automations.update_status definition")
}

type captureAutomationAPI struct {
	name      string
	arguments json.RawMessage
}

func (api *captureAutomationAPI) CallAutomationTool(_ context.Context, name string, arguments json.RawMessage) (any, error) {
	api.name = name
	api.arguments = append(json.RawMessage(nil), arguments...)
	return map[string]string{"ok": "true"}, nil
}
