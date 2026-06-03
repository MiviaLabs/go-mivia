package projectworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// CallWorkflowTool adapts MCP workflow tool calls onto service-owned validation and state transitions.
func (svc *Service) CallWorkflowTool(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "projects.workflows.validate_toml":
		var input tomlMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
		}
		return svc.ValidateWorkflowTOML(ctx, ValidateWorkflowTOMLInput{Data: []byte(input.TOML)})
	case "projects.workflows.import_toml":
		var input tomlMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
		}
		return svc.ImportWorkflowTOML(ctx, ImportWorkflowTOMLInput{ProjectID: input.ID, Data: []byte(input.TOML), CreatedByRunID: input.CreatedByRunID, TraceID: input.TraceID})
	case "projects.workflows.get":
		var input workflowIDMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
		}
		return svc.GetWorkflow(ctx, input.ID, input.WorkflowID)
	case "projects.workflows.list":
		var input listWorkflowsMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
		}
		return svc.ListWorkflows(ctx, WorkflowFilter{ProjectID: input.ID, Status: input.Status, WorkflowRef: input.WorkflowRef})
	case "projects.workflows.update_status":
		var input updateWorkflowStatusMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
		}
		return svc.UpdateWorkflowStatus(ctx, UpdateWorkflowStatusInput{ProjectID: input.ID, WorkflowID: input.WorkflowID, Status: input.Status})
	case "projects.workflows.compile_to_work_plan":
		var input compileWorkflowMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow arguments", ErrInvalidInput)
		}
		return svc.CompileWorkflow(ctx, WorkflowCompileInput{ProjectID: input.ID, WorkflowID: input.WorkflowID, UserRequestRef: input.UserRequestRef, CreatedByRunID: input.CreatedByRunID, TraceID: input.TraceID, TitleOverride: input.TitleOverride, DryRun: input.DryRun})
	case "projects.agent_definitions.list":
		var input workflowIDMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent definition arguments", ErrInvalidInput)
		}
		workflow, err := svc.GetWorkflow(ctx, input.ID, input.WorkflowID)
		if err != nil {
			return nil, err
		}
		return workflow.Agents, nil
	case "projects.agent_definitions.get":
		var input agentDefinitionMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent definition arguments", ErrInvalidInput)
		}
		workflow, err := svc.GetWorkflow(ctx, input.ID, input.WorkflowID)
		if err != nil {
			return nil, err
		}
		for _, agent := range workflow.Agents {
			if agent.ID == input.AgentID {
				return agent, nil
			}
		}
		return nil, fmt.Errorf("%w: agent definition not found", ErrInvalidInput)
	case "projects.permission_snapshots.get":
		var input snapshotMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid permission snapshot arguments", ErrInvalidInput)
		}
		projectID, snapshotID, err := safeWorkflowObject(input.ID, input.SnapshotID, "snapshot_id")
		if err != nil {
			return nil, err
		}
		return svc.store.GetPermissionSnapshot(ctx, projectID, snapshotID)
	case "projects.permission_snapshots.list":
		var input listSnapshotsMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid permission snapshot arguments", ErrInvalidInput)
		}
		projectID, err := safeRequiredWorkflowRef(input.ID, "project_id")
		if err != nil {
			return nil, err
		}
		workflowID, err := safeOptionalWorkflowRef(input.WorkflowID, "workflow_id")
		if err != nil {
			return nil, err
		}
		agentID, err := safeOptionalWorkflowRef(input.AgentID, "agent_id")
		if err != nil {
			return nil, err
		}
		return svc.store.ListPermissionSnapshots(ctx, PermissionSnapshotFilter{ProjectID: projectID, WorkflowID: workflowID, AgentID: agentID})
	default:
		return nil, fmt.Errorf("%w: unknown workflow tool", ErrInvalidInput)
	}
}

type tomlMCPInput struct {
	ID             string `json:"id"`
	TOML           string `json:"toml"`
	CreatedByRunID string `json:"created_by_run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
}

type workflowIDMCPInput struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	PageSize   int    `json:"page_size,omitempty"`
	PageToken  string `json:"page_token,omitempty"`
}

type listWorkflowsMCPInput struct {
	ID          string `json:"id"`
	Status      string `json:"status,omitempty"`
	WorkflowRef string `json:"workflow_ref,omitempty"`
	PageSize    int    `json:"page_size,omitempty"`
	PageToken   string `json:"page_token,omitempty"`
}

type updateWorkflowStatusMCPInput struct {
	ID             string `json:"id"`
	WorkflowID     string `json:"workflow_id"`
	Status         string `json:"status"`
	SafeNextAction string `json:"safe_next_action"`
	RunID          string `json:"run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
}

type compileWorkflowMCPInput struct {
	ID             string `json:"id"`
	WorkflowID     string `json:"workflow_id"`
	UserRequestRef string `json:"user_request_ref,omitempty"`
	CreatedByRunID string `json:"created_by_run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	TitleOverride  string `json:"title_override,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

type agentDefinitionMCPInput struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	AgentID    string `json:"agent_id"`
}

type snapshotMCPInput struct {
	ID         string `json:"id"`
	SnapshotID string `json:"snapshot_id"`
}

type listSnapshotsMCPInput struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id,omitempty"`
	AgentID    string `json:"agent_id,omitempty"`
	PageSize   int    `json:"page_size,omitempty"`
	PageToken  string `json:"page_token,omitempty"`
}

func decodeMCP(raw json.RawMessage, target any) error {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		raw = json.RawMessage(encoded)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: trailing json", ErrInvalidInput)
	}
	return nil
}
