package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func ToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "projects.list",
			"title":       "List Configured Projects",
			"description": "List configured local project metadata without root paths or raw database queries.",
			"inputSchema": objectSchema(map[string]any{}, []string{}),
		},
		{
			"name":        "projects.get",
			"title":       "Get Configured Project",
			"description": "Fetch configured local project metadata by id without exposing local root paths.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
		{
			"name":        "projects.digest",
			"title":       "Run Metadata-Only Project Digest",
			"description": "Run a manual metadata-only digest for an enabled local project.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
	}
}

func ResourceTemplates() []map[string]any {
	return []map[string]any{
		{
			"uriTemplate": "mivialabs://projects/{id}",
			"name":        "project",
			"title":       "Project",
			"description": "Configured local project metadata by id.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "mivialabs://projects/{id}/digest-runs/{run_id}",
			"name":        "project_digest_run",
			"title":       "Project Digest Run",
			"description": "Metadata-only project digest run by id.",
			"mimeType":    "application/json",
		},
	}
}

func CallTool(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, name string, arguments json.RawMessage) (map[string]any, error) {
	switch name {
	case "projects.list", "projects_list":
		var input struct {
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeOptionalRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid project arguments", projectregistry.ErrInvalidInput)
		}
		return toolResult(map[string]any{
			"projects": projectregistry.MetadataForProjects(registry.List()),
		}), nil
	case "projects.get", "projects_get":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid project arguments", projectregistry.ErrInvalidInput)
		}
		project, ok := registry.Get(strings.TrimSpace(input.ID))
		if !ok {
			return nil, projectregistry.ErrProjectNotFound
		}
		return toolResult(projectregistry.MetadataForProject(project)), nil
	case "projects.digest", "projects_digest":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid project arguments", projectregistry.ErrInvalidInput)
		}
		if digest == nil {
			return nil, fmt.Errorf("%w: digest service is not configured", projectregistry.ErrDigestUnsupported)
		}
		run, err := digest.DigestProject(ctx, strings.TrimSpace(input.ID))
		return toolResult(projectregistry.MetadataForDigestRun(run)), err
	default:
		return nil, projectregistry.ErrProjectNotFound
	}
}

func ReadResource(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, uri string) (map[string]any, error) {
	if !strings.HasPrefix(uri, "mivialabs://projects/") {
		return nil, projectregistry.ErrProjectNotFound
	}
	path := strings.TrimPrefix(uri, "mivialabs://projects/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		project, ok := registry.Get(parts[0])
		if !ok {
			return nil, projectregistry.ErrProjectNotFound
		}
		return resourceResult(uri, projectregistry.MetadataForProject(project))
	}
	if len(parts) == 3 && parts[1] == "digest-runs" {
		run, err := digest.GetDigestRun(ctx, parts[0], parts[2])
		if err != nil {
			return nil, err
		}
		return resourceResult(uri, projectregistry.MetadataForDigestRun(run))
	}
	return nil, projectregistry.ErrProjectNotFound
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

func decodeOptionalRaw(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return decodeRaw(raw, dst)
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
