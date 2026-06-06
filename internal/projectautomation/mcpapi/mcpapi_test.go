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

func TestCallToolReturnsMCPTextContentAndStructuredContent(t *testing.T) {
	api := &captureAutomationAPI{value: []map[string]string{{"id": "automation_1"}}}
	result, err := CallTool(context.Background(), api, "projects.automations.list", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	content, ok := result["content"].([]map[string]string)
	if !ok {
		t.Fatalf("content should use text content shape, got %T: %#v", result["content"], result["content"])
	}
	if len(content) != 1 || content[0]["type"] != "text" {
		t.Fatalf("unexpected content shape: %#v", content)
	}
	if strings.Contains(content[0]["text"], `"type":"json"`) {
		t.Fatalf("content text should be encoded value only, got wrapper text: %s", content[0]["text"])
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent should be an object envelope, got %T: %#v", result["structuredContent"], result["structuredContent"])
	}
	if _, ok := structured["automations"]; !ok {
		t.Fatalf("structuredContent missing automations envelope: %#v", structured)
	}
	var text map[string][]map[string]string
	if err := json.Unmarshal([]byte(content[0]["text"]), &text); err != nil {
		t.Fatalf("content text should be JSON object envelope: %v", err)
	}
	if len(text["automations"]) != 1 || text["automations"][0]["id"] != "automation_1" {
		t.Fatalf("content text missing automation list envelope: %#v", text)
	}
	if result["isError"] != false {
		t.Fatalf("expected non-error tool result, got %#v", result["isError"])
	}
}

func TestCallToolCreateUsesMCPTextContentShape(t *testing.T) {
	api := &captureAutomationAPI{value: map[string]string{"id": "automation_1"}}
	result, err := CallTool(context.Background(), api, "projects.automations.create", json.RawMessage(`{
		"id":"example-service",
		"automation_ref":"automation/ref",
		"title":"Create automation"
	}`))
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	content, ok := result["content"].([]map[string]string)
	if !ok {
		t.Fatalf("content should use text content shape, got %T: %#v", result["content"], result["content"])
	}
	if len(content) != 1 || content[0]["type"] != "text" {
		t.Fatalf("unexpected content shape: %#v", content)
	}
	structured, ok := result["structuredContent"].(map[string]string)
	if !ok {
		t.Fatalf("structuredContent should preserve create object, got %T: %#v", result["structuredContent"], result["structuredContent"])
	}
	if structured["id"] != "automation_1" {
		t.Fatalf("unexpected create structuredContent: %#v", structured)
	}
	var text map[string]string
	if err := json.Unmarshal([]byte(content[0]["text"]), &text); err != nil {
		t.Fatalf("content text should be JSON object: %v", err)
	}
	if text["id"] != "automation_1" {
		t.Fatalf("unexpected create content text: %#v", text)
	}
}

func TestCallToolRunListReturnsObjectEnvelope(t *testing.T) {
	api := &captureAutomationAPI{value: []map[string]string{{"id": "run_1"}}}
	result, err := CallTool(context.Background(), api, "projects.automation_runs.list", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent should be an object envelope, got %T: %#v", result["structuredContent"], result["structuredContent"])
	}
	if _, ok := structured["automation_runs"]; !ok {
		t.Fatalf("structuredContent missing automation_runs envelope: %#v", structured)
	}
	content := result["content"].([]map[string]string)
	var text map[string][]map[string]string
	if err := json.Unmarshal([]byte(content[0]["text"]), &text); err != nil {
		t.Fatalf("content text should be JSON object envelope: %v", err)
	}
	if len(text["automation_runs"]) != 1 || text["automation_runs"][0]["id"] != "run_1" {
		t.Fatalf("content text missing run list envelope: %#v", text)
	}
}

func TestToolDefinitionsExposeProjectIDWithoutTopLevelCombinators(t *testing.T) {
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
		for _, forbidden := range []string{"anyOf", "oneOf", "allOf", "not"} {
			if _, ok := schema[forbidden]; ok {
				t.Fatalf("%v schema exposes top-level %s", definition["name"], forbidden)
			}
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

func TestCompleteAttemptSchemaExposesReviewRefs(t *testing.T) {
	for _, definition := range ToolDefinitions() {
		if definition["name"] != "projects.automation_runs.complete_attempt" {
			continue
		}
		schema := definition["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		if _, ok := properties["review_result_refs"]; !ok {
			t.Fatalf("complete_attempt schema does not expose review_result_refs: %#v", properties)
		}
		return
	}
	t.Fatal("missing projects.automation_runs.complete_attempt definition")
}

func TestCreateAutomationSchemaExposesCompatibilityAliases(t *testing.T) {
	for _, definition := range ToolDefinitions() {
		if definition["name"] != "projects.automations.create" {
			continue
		}
		schema := definition["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, name := range []string{"work_plan_id", "work_task_id", "allowed_work_task_ids", "trigger_mode", "permission_snapshot_ref", "expected_output", "executor", "runner_mode"} {
			if _, ok := properties[name]; !ok {
				t.Fatalf("create schema does not expose alias %s: %#v", name, properties)
			}
		}
		return
	}
	t.Fatal("missing projects.automations.create definition")
}

type captureAutomationAPI struct {
	name      string
	arguments json.RawMessage
	value     any
}

func (api *captureAutomationAPI) CallAutomationTool(_ context.Context, name string, arguments json.RawMessage) (any, error) {
	api.name = name
	api.arguments = append(json.RawMessage(nil), arguments...)
	if api.value != nil {
		return api.value, nil
	}
	return map[string]string{"ok": "true"}, nil
}
