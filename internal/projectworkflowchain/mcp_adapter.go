package projectworkflowchain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

func (svc *Service) CallWorkflowChainTool(ctx context.Context, name string, arguments json.RawMessage) (any, error) {
	switch name {
	case "projects.workflow_chains.start":
		var input startMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow chain arguments", ErrInvalidInput)
		}
		return svc.Start(ctx, StartInput{ProjectID: input.ID, ChainRef: input.ChainRef, InputText: input.InputText, CreatedByRunID: input.CreatedByRunID, TraceID: input.TraceID, DryRun: input.DryRun})
	case "projects.workflow_chains.get":
		var input getMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow chain arguments", ErrInvalidInput)
		}
		return svc.Get(ctx, input.ID, input.ChainRunID)
	case "projects.workflow_chains.list":
		var input listMCPInput
		if err := decodeMCP(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid workflow chain arguments", ErrInvalidInput)
		}
		return svc.List(ctx, ChainFilter{ProjectID: input.ID, ChainRef: input.ChainRef, Status: input.Status})
	default:
		return nil, fmt.Errorf("%w: unknown workflow chain tool", ErrInvalidInput)
	}
}

type startMCPInput struct {
	ID             string          `json:"id"`
	ChainRef       string          `json:"chain_ref"`
	InputText      string          `json:"input_text"`
	CreatedByRunID string          `json:"created_by_run_id,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`
	DryRun         bool            `json:"dry_run,omitempty"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type getMCPInput struct {
	ID         string          `json:"id"`
	ChainRunID string          `json:"chain_run_id"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type listMCPInput struct {
	ID        string          `json:"id,omitempty"`
	ChainRef  string          `json:"chain_ref,omitempty"`
	Status    string          `json:"status,omitempty"`
	PageSize  int             `json:"page_size,omitempty"`
	PageToken string          `json:"page_token,omitempty"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
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
