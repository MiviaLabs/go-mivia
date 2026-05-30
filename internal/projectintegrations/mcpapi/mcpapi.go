package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectintegrations"
)

func ToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "projects.integrations.list",
			"title":       "List Project Integration Providers",
			"description": "List configured project integration providers using redacted local metadata only.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
		{
			"name":        "projects.integrations.status",
			"title":       "Get Project Integration Status",
			"description": "Fetch redacted project integration status from local config and sync metadata only.",
			"inputSchema": objectSchema(map[string]any{
				"id":       map[string]any{"type": "string", "minLength": 1},
				"provider": map[string]any{"type": "string", "enum": []string{string(projectintegrations.ProviderJira), string(projectintegrations.ProviderConfluence)}},
			}, []string{"id", "provider"}),
		},
	}
}

func CallTool(ctx context.Context, service *projectintegrations.Service, name string, arguments json.RawMessage) (map[string]any, error) {
	if service == nil {
		return nil, fmt.Errorf("%w: integration service is not configured", projectintegrations.ErrNotFound)
	}
	switch name {
	case "projects.integrations.list", "projects_integrations_list":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		providers, err := service.ListProviders(strings.TrimSpace(input.ID))
		if err != nil {
			return nil, err
		}
		return toolResult(map[string]any{"providers": providers}), nil
	case "projects.integrations.status", "projects_integrations_status":
		var input struct {
			ID       string          `json:"id"`
			Provider string          `json:"provider"`
			Meta     json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		status, err := service.Status(ctx, strings.TrimSpace(input.ID), projectintegrations.Provider(strings.TrimSpace(input.Provider)))
		if err != nil {
			return nil, err
		}
		return toolResult(status), nil
	default:
		return nil, projectintegrations.ErrNotFound
	}
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
