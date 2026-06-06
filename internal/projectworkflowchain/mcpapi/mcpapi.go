package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectworkflowchain"
	chainstore "github.com/MiviaLabs/go-mivia/internal/projectworkflowchain/store"
)

var (
	ErrInvalidInput = errors.New("invalid project workflow chain input")
	ErrNotFound     = errors.New("project workflow chain resource not found")
)

type API interface {
	CallWorkflowChainTool(ctx context.Context, name string, arguments json.RawMessage) (any, error)
}

var chainTools = []string{
	"projects.workflow_chains.start",
	"projects.workflow_chains.get",
	"projects.workflow_chains.list",
}

func ToolDefinitions() []map[string]any {
	ref := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	text := map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
	pageFields := map[string]any{
		"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"page_token": map[string]any{"type": "string", "maxLength": 20},
	}
	return []map[string]any{
		tool("projects.workflow_chains.start", "Start Project Workflow Chain", "MUST start only a configured metadata-only workflow chain from one bounded input string. Creates safe chain state, Work Plan refs, Work Task refs, automation refs, and context refs only; never stores raw prompts, source, stderr, provider payloads, secrets, roots, external URLs, or PII, and never runs shell or live Jira/Confluence connectors.", schema(map[string]any{"id": ref, "chain_ref": ref, "input_text": text, "created_by_run_id": ref, "trace_id": ref, "dry_run": map[string]any{"type": "boolean"}}, []string{"id", "chain_ref", "input_text"})),
		tool("projects.workflow_chains.get", "Get Project Workflow Chain Run", "Fetch safe workflow-chain run metadata by id. Returns refs and lifecycle state only; no raw input, prompts, source, stderr, provider payloads, secrets, roots, URLs, or PII.", schema(map[string]any{"id": ref, "chain_run_id": ref}, []string{"id", "chain_run_id"})),
		tool("projects.workflow_chains.list", "List Project Workflow Chains", "List configured workflow chains and safe run metadata. Use before creating duplicates or selecting a chain. Returns metadata only.", schema(merge(map[string]any{"id": ref, "chain_ref": ref, "status": ref}, pageFields), []string{})),
	}
}

func IsWorkflowChainTool(name string) bool {
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
	value, err := api.CallWorkflowChainTool(ctx, canonical, arguments)
	if err != nil {
		return nil, chainValidationError(err)
	}
	return toolResult(value), nil
}

func validateArguments(name string, arguments json.RawMessage) error {
	var value any
	switch name {
	case "projects.workflow_chains.start":
		value = &startInput{}
	case "projects.workflow_chains.get":
		value = &getInput{}
	case "projects.workflow_chains.list":
		value = &listInput{}
	default:
		return ErrNotFound
	}
	if err := decodeRaw(arguments, value); err != nil {
		return fmt.Errorf("%w: invalid workflow chain arguments", ErrInvalidInput)
	}
	if hasUnsafeValue(value) {
		return fmt.Errorf("%w: unsafe workflow chain metadata", ErrInvalidInput)
	}
	return nil
}

type startInput struct {
	ID             string          `json:"id"`
	ChainRef       string          `json:"chain_ref"`
	InputText      string          `json:"input_text"`
	CreatedByRunID string          `json:"created_by_run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	DryRun         bool            `json:"dry_run,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type getInput struct {
	ID         string          `json:"id"`
	ChainRunID string          `json:"chain_run_id"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type listInput struct {
	ID        string          `json:"id,omitempty"`
	ChainRef  string          `json:"chain_ref,omitempty"`
	Status    string          `json:"status,omitempty"`
	PageSize  int             `json:"page_size,omitempty"`
	PageToken string          `json:"page_token,omitempty"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

func canonicalToolName(name string) string {
	for _, tool := range chainTools {
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
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func merge(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for key, value := range a {
		out[key] = value
	}
	for key, value := range b {
		out[key] = value
	}
	return out
}

func toolResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": string(encoded)}},
		"structuredContent": value,
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
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: trailing json", ErrInvalidInput)
	}
	return nil
}

func hasUnsafeValue(value any) bool {
	encoded, _ := json.Marshal(value)
	lower := strings.ToLower(string(encoded))
	for _, unsafe := range []string{"token=", "password", "secret", "raw prompt", "provider payload", "raw stderr"} {
		if strings.Contains(lower, unsafe) {
			return true
		}
	}
	return false
}

func chainValidationError(err error) error {
	if errors.Is(err, projectworkflowchain.ErrInvalidInput) {
		return ErrInvalidInput
	}
	if errors.Is(err, chainstore.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
