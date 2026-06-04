package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrInvalidInput = errors.New("invalid project automation input")
	ErrNotFound     = errors.New("project automation resource not found")
)

type API interface {
	CallAutomationTool(ctx context.Context, name string, arguments json.RawMessage) (any, error)
}

var automationTools = []string{
	"projects.automations.create",
	"projects.automations.get",
	"projects.automations.list",
	"projects.automations.update_status",
	"projects.automations.run",
	"projects.automations.run_parallel_batch",
	"projects.automation_runs.get",
	"projects.automation_runs.list",
	"projects.automation_runs.claim_next",
	"projects.automation_runs.complete_attempt",
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
	refArray := map[string]any{"type": "array", "items": ref, "maxItems": 100}
	return []map[string]any{
		tool("projects.automations.create", "Create Project Automation", "MUST create metadata-only automation only after a Work Plan exists and target Work Tasks have been decomposed for isolated low-intelligence workers. It is not a planner, task creator, or knowledge store. Safety: no raw prompts, source, stderr, provider payloads, secrets, roots, external URLs, or PII. Automatic trigger metadata may enqueue runs only for enabled automations with ready allowed tasks and completed required review tasks.", schema(map[string]any{"id": ref, "project_id": ref, "automation_ref": ref, "title": text, "purpose": text, "expected_output": text, "status": ref, "agent_id": ref, "executor": ref, "runner_mode": ref, "plan_id": ref, "work_plan_id": ref, "work_task_id": ref, "allowed_task_refs": refArray, "allowed_work_task_ids": refArray, "required_review_task_ids": refArray, "trigger_kind": map[string]any{"type": "string", "enum": []string{"manual", "automatic"}}, "trigger_mode": map[string]any{"type": "string", "enum": []string{"manual", "automatic"}}, "schedule_policy": ref, "permission_ref": ref, "permission_snapshot_ref": ref, "created_by_run_id": ref, "trace_id": ref}, []string{"id", "automation_ref", "title"})),
		tool("projects.automations.get", "Get Project Automation", "MUST inspect automation metadata before running or changing it. Safety: metadata only. Next tool: projects.automations.run or projects.automations.run_parallel_batch.", schema(map[string]any{"id": ref, "project_id": ref, "automation_id": ref}, []string{"id", "automation_id"})),
		tool("projects.automations.list", "List Project Automations", "MUST be used to find existing automation before creating duplicates. Safety: metadata only.", schema(map[string]any{"id": ref, "project_id": ref, "status": ref, "agent_id": ref}, []string{"id"})),
		tool("projects.automations.update_status", "Update Project Automation Status", "MUST be used to disable, pause, supersede, or re-enable existing automation metadata without deleting history. Safety: metadata only; never stores raw prompts, source, stderr, provider payloads, secrets, roots, external URLs, or PII.", schema(map[string]any{"id": ref, "project_id": ref, "automation_id": ref, "status": map[string]any{"type": "string", "enum": []string{"draft", "enabled", "disabled", "running", "paused", "failed", "cancelled", "superseded"}}, "run_id": ref, "trace_id": ref}, []string{"id", "automation_id", "status"})),
		tool("projects.automations.run", "Run Project Automation", "MUST execute or queue one orchestrator-owned run over a ready Work Task. Required prior state: Work Plan exists, Work Task is ready/claimed as applicable, and needed context/evidence/claim refs are attached. If runner is enabled, executable automation MUST use codex_cli and MUST NOT fall back to manual. A successful exit still requires Evidence Graph outcome refs, confidence refs when knowledge may be reused, and orchestrator verifier refs before task completion or Knowledge Promotion. Safety: refs only.", schema(map[string]any{"id": ref, "project_id": ref, "automation_id": ref, "plan_id": ref, "task_id": ref, "owner_agent": ref, "runner_kind": map[string]any{"type": "string", "enum": []string{"codex_cli", "manual"}}, "orchestrator_run_id": ref, "parent_run_id": ref, "evidence_refs": refArray, "verifier_result_refs": refArray, "safe_next_action": text}, []string{"id", "automation_id"})),
		tool("projects.automations.run_parallel_batch", "Run Project Automation Parallel Batch", "MUST be orchestrator-owned and may only batch independent ready Work Tasks with satisfied dependencies and disjoint write/verifier/artifact scope. Worker prompts must use task metadata and refs only. Safety: no raw prompts/source/logs/provider payloads.", schema(map[string]any{"id": ref, "project_id": ref, "automation_run_id": ref, "orchestrator_run_id": ref, "plan_id": ref, "task_ids": refArray, "max_tasks": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}, []string{"id", "orchestrator_run_id"})),
		tool("projects.automation_runs.get", "Get Automation Run", "MUST inspect one automation run by safe ref. Safety: metadata only.", schema(map[string]any{"id": ref, "project_id": ref, "run_id": ref}, []string{"id", "run_id"})),
		tool("projects.automation_runs.list", "List Automation Runs", "MUST inspect project automation run history. Safety: metadata only.", schema(map[string]any{"id": ref, "project_id": ref, "automation_id": ref, "status": ref}, []string{"id"})),
		tool("projects.automation_runs.claim_next", "Claim Next External Automation Run", "MUST be used only by a local external runner running in the user's logged-in Codex environment. Claims one queued codex_cli run and returns metadata-only Codex input. Safety: no raw source, prompts, stderr, provider payloads, secrets, roots, or PII.", schema(map[string]any{"id": ref, "project_id": ref, "agent_id": ref, "runner_kind": map[string]any{"type": "string", "enum": []string{"codex_cli"}}}, []string{"id"})),
		tool("projects.automation_runs.complete_attempt", "Complete External Automation Attempt", "MUST be called by the local external runner after Codex exits. Stores only attempt metadata and safe refs. A successful exit moves the run to verifier-required state; it does not complete the Work Task, validate a claim, score confidence, or promote knowledge. The orchestrator must attach verifier refs and use Evidence Graph, Confidence Engine, and Knowledge Promotion tools for any reusable conclusion.", schema(map[string]any{"id": ref, "project_id": ref, "run_id": ref, "status": map[string]any{"type": "string", "enum": []string{"completed", "failed", "timeout", "blocked", "cancelled"}}, "failure_category": ref, "duration_ms": map[string]any{"type": "integer", "minimum": 0}, "verifier_result_refs": refArray, "evidence_refs": refArray, "claim_refs": refArray, "review_result_refs": refArray, "knowledge_candidate_refs": refArray}, []string{"id", "run_id", "status"})),
	}
}

func IsAutomationTool(name string) bool {
	canonical := canonicalToolName(name)
	return canonical != ""
}

func CallTool(ctx context.Context, api API, name string, arguments json.RawMessage) (map[string]any, error) {
	if api == nil {
		return nil, ErrNotFound
	}
	canonical := canonicalToolName(name)
	if canonical == "" {
		return nil, ErrNotFound
	}
	normalized, err := normalizeProjectIDAlias(arguments)
	if err != nil {
		return nil, err
	}
	if err := validateArguments(canonical, normalized); err != nil {
		return nil, err
	}
	value, err := api.CallAutomationTool(ctx, canonical, normalized)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": []map[string]any{{"type": "json", "json": value}}}, nil
}

func validateArguments(_ string, arguments json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: invalid json", ErrInvalidInput)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: trailing json", ErrInvalidInput)
	}
	return nil
}

func canonicalToolName(name string) string {
	for _, tool := range automationTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return tool
		}
	}
	return ""
}

func tool(name, title, description string, inputSchema map[string]any) map[string]any {
	return map[string]any{"name": name, "title": title, "description": description, "inputSchema": inputSchema}
}

func schema(properties map[string]any, required []string) map[string]any {
	out := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if _, hasID := properties["id"]; hasID {
		if _, hasProjectID := properties["project_id"]; hasProjectID {
			filtered := make([]string, 0, len(required))
			for _, name := range required {
				if name != "id" {
					filtered = append(filtered, name)
				}
			}
			if len(filtered) > 0 {
				out["required"] = filtered
			}
			return out
		}
	}
	out["required"] = required
	return out
}

func normalizeProjectIDAlias(arguments json.RawMessage) (json.RawMessage, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("%w: invalid json", ErrInvalidInput)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing json", ErrInvalidInput)
	}
	if projectID, ok := object["project_id"]; ok {
		if _, hasID := object["id"]; !hasID {
			object["id"] = projectID
		}
		delete(object, "project_id")
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid json", ErrInvalidInput)
	}
	return normalized, nil
}
