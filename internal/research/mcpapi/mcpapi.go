package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research"
	researchstore "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/store"
)

func ToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "research_sources.create",
			"title":       "Create Research Source Metadata",
			"description": "Store redacted research source metadata without raw content or live provider execution.",
			"inputSchema": objectSchema(map[string]any{
				"research_run_id": map[string]any{"type": "string", "minLength": 1},
				"artifact_ref":    map[string]any{"type": "string", "minLength": 1},
				"source_type":     map[string]any{"type": "string", "minLength": 1},
				"summary":         map[string]any{"type": "string", "minLength": 1},
			}, []string{"research_run_id", "artifact_ref", "source_type", "summary"}),
		},
		{
			"name":        "research_sources.get",
			"title":       "Get Research Source Metadata",
			"description": "Fetch redacted research source metadata by id.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
	}
}

func ResourceTemplates() []map[string]any {
	return []map[string]any{
		{
			"uriTemplate": "mivialabs://research-sources/{id}",
			"name":        "research_source",
			"title":       "Research Source",
			"description": "Redacted research source metadata by id.",
			"mimeType":    "application/json",
		},
	}
}

func CallTool(ctx context.Context, svc *research.Service, name string, arguments json.RawMessage) (map[string]any, error) {
	switch name {
	case "research_sources.create":
		var input research.CreateSourceInput
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid source arguments", research.ErrInvalidInput)
		}
		source, err := svc.CreateSource(ctx, input)
		return toolResult(source), err
	case "research_sources.get":
		var input struct {
			ID string `json:"id"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid source arguments", research.ErrInvalidInput)
		}
		source, err := svc.GetSource(ctx, input.ID)
		return toolResult(source), err
	default:
		return nil, researchstore.ErrNotFound
	}
}

func ReadResource(ctx context.Context, svc *research.Service, uri string) (map[string]any, error) {
	if !strings.HasPrefix(uri, "mivialabs://research-sources/") {
		return nil, researchstore.ErrNotFound
	}
	id := strings.TrimPrefix(uri, "mivialabs://research-sources/")
	source, err := svc.GetSource(ctx, id)
	if err != nil {
		return nil, err
	}
	return resourceResult(uri, source)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func toolResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(encoded)},
		},
		"structuredContent": value,
		"isError":           false,
	}
}

func resourceResult(uri string, value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"contents": []map[string]string{
			{
				"uri":      uri,
				"mimeType": "application/json",
				"text":     string(encoded),
			},
		},
	}, nil
}

func decodeRaw(raw json.RawMessage, dst any) error {
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
