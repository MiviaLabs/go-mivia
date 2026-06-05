package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
)

var (
	ErrInvalidInput = errors.New("invalid project workflow input")
	ErrNotFound     = errors.New("project workflow resource not found")
)

var workflowTOMLToolTimeout = 15 * time.Second

type API interface {
	CallWorkflowTool(ctx context.Context, name string, arguments json.RawMessage) (any, error)
}

var workflowTools = []string{
	"projects.workflows.validate_toml",
	"projects.workflows.import_toml",
	"projects.workflows.get",
	"projects.workflows.list",
	"projects.workflows.update_status",
	"projects.workflows.compile_to_work_plan",
	"projects.agent_definitions.list",
	"projects.agent_definitions.get",
	"projects.permission_snapshots.get",
	"projects.permission_snapshots.list",
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 500}
	workflowTOML := map[string]any{"type": "string", "minLength": 1}
	pageFields := map[string]any{
		"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"page_token": map[string]any{"type": "string", "maxLength": 20},
	}
	return []map[string]any{
		tool("projects.workflows.validate_toml", "Validate Project Workflow TOML", "MUST validate workflow TOML as metadata/config only before any import or compile. Work Plans and Work Tasks remain mandatory; workflow TOML never executes raw prompts, source, stderr, provider payloads, secrets, roots, or PII. Review gates, verifier refs, evidence/claim refs, confidence, and knowledge decisions remain required downstream. Safety: returns parsed metadata and validation issues only, never raw TOML.", schema(map[string]any{"id": ref, "toml": workflowTOML}, []string{"id", "toml"})),
		tool("projects.workflows.import_toml", "Import Project Workflow TOML", "MUST import workflow TOML only as bounded metadata/config after validation. Work Plans and Work Tasks remain the execution source of truth; import does not run Codex CLI, automation, prompts, source, or shell commands. Review gates are first-class and required unless explicitly exempt in safe metadata. Safety: creates workflow and permission snapshot metadata only; no raw TOML is returned or stored by this MCP surface.", schema(map[string]any{"id": ref, "toml": workflowTOML, "created_by_run_id": ref, "trace_id": ref}, []string{"id", "toml"})),
		tool("projects.workflows.get", "Get Project Workflow", "MUST inspect one workflow metadata record before status changes, compile, or automation setup. Work Plans and Work Tasks remain mandatory before execution. Safety: metadata-only workflow, agent definition, review gate, and permission snapshot refs; no raw prompts/source/logs/provider payloads/secrets/roots/PII.", schema(map[string]any{"id": ref, "workflow_id": ref}, []string{"id", "workflow_id"})),
		tool("projects.workflows.list", "List Project Workflows", "MUST list workflow metadata before creating duplicates or selecting a workflow to compile. Work Plans and Work Tasks remain mandatory for execution. Safety: metadata-only summaries and filters; no raw TOML, prompts, source dumps, raw stderr, provider payloads, secrets, roots, or PII.", schema(merge(map[string]any{"id": ref, "status": statusSchema(), "workflow_ref": ref}, pageFields), []string{"id"})),
		tool("projects.workflows.update_status", "Update Project Workflow Status", "MUST be used only for workflow lifecycle metadata, not execution. Before enabling or compiling, callers must verify review gates, verifier refs, evidence/claim refs, and Work Plan/Work Task governance expectations. Safety: metadata-only status transition; does not run automation or Codex CLI.", schema(map[string]any{"id": ref, "workflow_id": ref, "status": statusSchema(), "safe_next_action": text, "run_id": ref, "trace_id": ref}, []string{"id", "workflow_id", "status", "safe_next_action"})),
		tool("projects.workflows.compile_to_work_plan", "Compile Workflow To Work Plan", "MUST compile an enabled workflow into governed Work Plan, Work Task, review-task, automation, and permission snapshot refs before any runner starts. Codex CLI execution is downstream of ready Work Tasks only; compile must preserve review gates, verifier refs, evidence/claim refs, confidence, and knowledge promotion decisions. Safety: returns ids/refs and validation issues only; no raw prompts/source/logs/provider payloads/secrets/roots/PII.", schema(map[string]any{"id": ref, "workflow_id": ref, "user_request_ref": ref, "created_by_run_id": ref, "trace_id": ref, "title_override": text, "dry_run": map[string]any{"type": "boolean"}}, []string{"id", "workflow_id"})),
		tool("projects.agent_definitions.list", "List Workflow Agent Definitions", "MUST inspect bounded agent definitions before workflow automation or runner configuration. Work Plans and Work Tasks remain mandatory, and permission snapshots must gate downstream automation metadata. Safety: metadata-only skills/tools/command policy; no raw prompts/source/logs/provider payloads/secrets/roots/PII.", schema(merge(map[string]any{"id": ref, "workflow_id": ref}, pageFields), []string{"id", "workflow_id"})),
		tool("projects.agent_definitions.get", "Get Workflow Agent Definition", "MUST inspect one bounded workflow agent definition before assigning Work Tasks or automation. Review gates and independent review remain required where relevant. Safety: metadata-only permission policy; no raw prompts/source/logs/provider payloads/secrets/roots/PII.", schema(map[string]any{"id": ref, "workflow_id": ref, "agent_id": ref}, []string{"id", "workflow_id", "agent_id"})),
		tool("projects.permission_snapshots.get", "Get Workflow Permission Snapshot", "MUST inspect immutable permission snapshot metadata before automation execution. Work Plans, Work Tasks, review gates, verifier refs, evidence refs, and knowledge decisions remain required; this is not an OS sandbox or execution approval. Safety: metadata-only policy and content hash; no secrets, roots, raw commands output, prompts, source, stderr, provider payloads, or PII.", schema(map[string]any{"id": ref, "snapshot_id": ref}, []string{"id", "snapshot_id"})),
		tool("projects.permission_snapshots.list", "List Workflow Permission Snapshots", "MUST inspect immutable permission snapshot metadata for workflow/agent governance before automation. Work Plan/Work Task governance, review gates, verifier refs, evidence/claim refs, confidence, and knowledge promotion remain mandatory. Safety: metadata-only filters and hashes; no secrets, roots, raw prompts/source/stderr/provider payloads/PII.", schema(merge(map[string]any{"id": ref, "workflow_id": ref, "agent_id": ref}, pageFields), []string{"id"})),
	}
}

func IsWorkflowTool(name string) bool {
	return canonicalToolName(name) != ""
}

func CallTool(ctx context.Context, api API, name string, arguments json.RawMessage) (map[string]any, error) {
	if api == nil {
		return nil, ErrNotFound
	}
	canonical := canonicalToolName(name)
	if canonical == "" {
		return nil, ErrNotFound
	}
	if err := validateArguments(canonical, arguments); err != nil {
		return nil, err
	}
	value, err := callWorkflowToolWithGuard(ctx, api, canonical, arguments)
	if err != nil {
		if result, ok := workflowValidationToolResult(canonical, value); ok {
			return toolErrorResult(result), nil
		}
		return nil, err
	}
	return toolResult(value), nil
}

func callWorkflowToolWithGuard(ctx context.Context, api API, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "projects.workflows.validate_toml", "projects.workflows.import_toml":
	default:
		return api.CallWorkflowTool(ctx, name, arguments)
	}
	ctx, cancel := context.WithTimeout(ctx, workflowTOMLToolTimeout)
	defer cancel()
	type result struct {
		value any
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := api.CallWorkflowTool(ctx, name, arguments)
		done <- result{value: value, err: err}
	}()
	select {
	case result := <-done:
		return result.value, result.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: workflow TOML operation timed out", ErrInvalidInput)
	}
}

func workflowValidationToolResult(name string, value any) (any, bool) {
	switch name {
	case "projects.workflows.validate_toml":
		result, ok := value.(projectworkflow.ValidateWorkflowTOMLResult)
		return result, ok && len(result.Issues) > 0
	case "projects.workflows.import_toml":
		result, ok := value.(projectworkflow.ImportWorkflowTOMLResult)
		return result, ok && len(result.ValidationIssues) > 0
	case "projects.workflows.compile_to_work_plan":
		result, ok := value.(projectworkflow.WorkflowCompileResult)
		return result, ok && len(result.ValidationIssues) > 0
	default:
		return nil, false
	}
}

func validateArguments(name string, arguments json.RawMessage) error {
	var value any
	switch name {
	case "projects.workflows.validate_toml", "projects.workflows.import_toml":
		value = &tomlInput{}
	case "projects.workflows.get":
		value = &workflowIDInput{}
	case "projects.workflows.list":
		value = &listWorkflowsInput{}
	case "projects.workflows.update_status":
		value = &updateStatusInput{}
	case "projects.workflows.compile_to_work_plan":
		value = &compileInput{}
	case "projects.agent_definitions.list":
		value = &workflowIDInput{}
	case "projects.agent_definitions.get":
		value = &agentIDInput{}
	case "projects.permission_snapshots.get":
		value = &snapshotIDInput{}
	case "projects.permission_snapshots.list":
		value = &listSnapshotsInput{}
	default:
		return ErrNotFound
	}
	if err := decodeRaw(arguments, value); err != nil {
		return fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
	}
	if hasUnsafeValueForTool(name, value) {
		return fmt.Errorf("%w: unsafe workflow metadata", ErrInvalidInput)
	}
	return nil
}

type tomlInput struct {
	ID             string          `json:"id"`
	TOML           string          `json:"toml"`
	CreatedByRunID string          `json:"created_by_run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type workflowIDInput struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id"`
	PageSize   int             `json:"page_size,omitempty"`
	PageToken  string          `json:"page_token,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type listWorkflowsInput struct {
	ID          string          `json:"id"`
	Status      string          `json:"status,omitempty"`
	WorkflowRef string          `json:"workflow_ref,omitempty"`
	PageSize    int             `json:"page_size,omitempty"`
	PageToken   string          `json:"page_token,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

type updateStatusInput struct {
	ID             string          `json:"id"`
	WorkflowID     string          `json:"workflow_id"`
	Status         string          `json:"status"`
	SafeNextAction string          `json:"safe_next_action"`
	RunID          string          `json:"run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type compileInput struct {
	ID             string          `json:"id"`
	WorkflowID     string          `json:"workflow_id"`
	UserRequestRef string          `json:"user_request_ref,omitempty"`
	CreatedByRunID string          `json:"created_by_run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	TitleOverride  string          `json:"title_override,omitempty"`
	DryRun         bool            `json:"dry_run,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type agentIDInput struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id"`
	AgentID    string          `json:"agent_id"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type snapshotIDInput struct {
	ID         string          `json:"id"`
	SnapshotID string          `json:"snapshot_id"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type listSnapshotsInput struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	PageSize   int             `json:"page_size,omitempty"`
	PageToken  string          `json:"page_token,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

func canonicalToolName(name string) string {
	for _, tool := range workflowTools {
		if name == tool || name == strings.ReplaceAll(tool, ".", "_") {
			return tool
		}
	}
	return ""
}

func toolResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": string(encoded)}},
		"structuredContent": value,
		"isError":           false,
	}
}

func toolErrorResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": string(encoded)}},
		"structuredContent": value,
		"isError":           true,
	}
}

func decodeRaw(raw json.RawMessage, dst any) error {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = json.RawMessage(encoded)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}

func hasUnsafeValueForTool(name string, value any) bool {
	if name == "projects.workflows.validate_toml" || name == "projects.workflows.import_toml" {
		input, ok := value.(*tomlInput)
		if !ok {
			return true
		}
		return hasUnsafeRefs([]string{input.ID, input.CreatedByRunID, input.TraceID})
	}
	encoded, _ := json.Marshal(value)
	lower := strings.ToLower(string(encoded))
	for _, marker := range []string{"raw prompt", "raw completion", "source dump", "raw stderr", "provider payload", "token=", "secret=", "credential", "api_key", "password=", "/home/", "wsl.localhost", "c:\\", "\\\\", ".."} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasUnsafeRefs(values []string) bool {
	for _, value := range values {
		lower := strings.ToLower(strings.TrimSpace(value))
		for _, marker := range []string{"token=", "secret=", "credential", "api_key", "password=", "/home/", "wsl.localhost", "c:\\", "\\\\", ".."} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func statusSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"draft", "enabled", "disabled", "superseded"}}
}

func merge(first map[string]any, second map[string]any) map[string]any {
	out := make(map[string]any, len(first)+len(second))
	for key, value := range first {
		out[key] = value
	}
	for key, value := range second {
		out[key] = value
	}
	return out
}

func tool(name, title, description string, inputSchema map[string]any) map[string]any {
	return map[string]any{"name": name, "title": title, "description": description, "inputSchema": inputSchema}
}

func schema(properties map[string]any, required []string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
