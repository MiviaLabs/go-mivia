package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
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
		{
			"name":        "projects.integrations.poll",
			"title":       "Run Project Integration Poll",
			"description": "Queue one manual provider poll and return redacted pending run metadata only.",
			"inputSchema": objectSchema(map[string]any{
				"id":       map[string]any{"type": "string", "minLength": 1},
				"provider": map[string]any{"type": "string", "enum": []string{string(projectintegrations.ProviderJira), string(projectintegrations.ProviderConfluence)}},
				"kind":     map[string]any{"type": "string", "enum": []string{string(projectintegrations.SyncKindInitialFull), string(projectintegrations.SyncKindIncremental)}},
			}, []string{"id", "provider"}),
		},
		{
			"name":        "projects.integrations.poll_status",
			"title":       "Get Project Integration Poll Run",
			"description": "Fetch redacted local integration poll run metadata by project, provider, and run ID.",
			"inputSchema": objectSchema(map[string]any{
				"id":       map[string]any{"type": "string", "minLength": 1},
				"provider": map[string]any{"type": "string", "enum": []string{string(projectintegrations.ProviderJira), string(projectintegrations.ProviderConfluence)}},
				"run_id":   map[string]any{"type": "string", "minLength": 1},
			}, []string{"id", "provider", "run_id"}),
		},
		{
			"name":        "projects.integrations.search",
			"title":       "Search Local Project Integration Content",
			"description": "Search locally ingested integration rich content only. Does not call remote providers or resolve credentials.",
			"inputSchema": objectSchema(map[string]any{
				"id":                map[string]any{"type": "string", "minLength": 1},
				"provider":          map[string]any{"type": "string", "enum": []string{string(projectintegrations.ProviderJira), string(projectintegrations.ProviderConfluence)}},
				"query":             map[string]any{"type": "string", "minLength": 1},
				"max_results":       map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
				"max_snippet_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 4096},
				"case_sensitive":    map[string]any{"type": "boolean"},
			}, []string{"id", "query"}),
		},
		{
			"name":        "projects.jira.issue.get",
			"title":       "Read Local Jira Issue Content",
			"description": "Read one locally ingested Jira issue by key or ID. Does not call Jira or resolve credentials.",
			"inputSchema": objectSchema(map[string]any{
				"id":              map[string]any{"type": "string", "minLength": 1},
				"key":             map[string]any{"type": "string", "minLength": 1},
				"max_chunk_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 16384},
			}, []string{"id", "key"}),
		},
		{
			"name":        "projects.confluence.page.get",
			"title":       "Read Local Confluence Page Content",
			"description": "Read one locally ingested Confluence page by page ID. Does not call Confluence or resolve credentials.",
			"inputSchema": objectSchema(map[string]any{
				"id":              map[string]any{"type": "string", "minLength": 1},
				"page_id":         map[string]any{"type": "string", "minLength": 1},
				"max_chunk_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": 16384},
			}, []string{"id", "page_id"}),
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
	case "projects.integrations.poll", "projects_integrations_poll":
		var input struct {
			ID       string          `json:"id"`
			Provider string          `json:"provider"`
			Kind     string          `json:"kind,omitempty"`
			Meta     json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		status, err := service.SubmitProviderPoll(ctx, strings.TrimSpace(input.ID), projectintegrations.Provider(strings.TrimSpace(input.Provider)), projectintegrations.SyncKind(strings.TrimSpace(input.Kind)))
		if err != nil {
			return nil, err
		}
		return toolResult(status), nil
	case "projects.integrations.poll_status", "projects_integrations_poll_status":
		var input struct {
			ID       string          `json:"id"`
			Provider string          `json:"provider"`
			RunID    string          `json:"run_id"`
			Meta     json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		status, err := service.PollRunStatus(ctx, strings.TrimSpace(input.ID), projectintegrations.Provider(strings.TrimSpace(input.Provider)), strings.TrimSpace(input.RunID))
		if err != nil {
			return nil, err
		}
		return toolResult(status), nil
	case "projects.integrations.search", "projects_integrations_search":
		var input struct {
			ID              string          `json:"id"`
			Provider        string          `json:"provider,omitempty"`
			Query           string          `json:"query"`
			MaxResults      int             `json:"max_results,omitempty"`
			MaxSnippetBytes int             `json:"max_snippet_bytes,omitempty"`
			CaseSensitive   bool            `json:"case_sensitive,omitempty"`
			Meta            json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		results, err := service.SearchLocalContent(ctx, projectintegrations.LocalSearchInput{
			ProjectID:       strings.TrimSpace(input.ID),
			Provider:        projectintegrations.Provider(strings.TrimSpace(input.Provider)),
			Query:           strings.TrimSpace(input.Query),
			MaxResults:      input.MaxResults,
			MaxSnippetBytes: input.MaxSnippetBytes,
			CaseSensitive:   input.CaseSensitive,
		})
		if err != nil {
			return nil, err
		}
		return toolResult(map[string]any{"results": results}), nil
	case "projects.jira.issue.get", "projects_jira_issue_get":
		var input struct {
			ID            string          `json:"id"`
			Key           string          `json:"key"`
			MaxChunkBytes int             `json:"max_chunk_bytes,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		result, err := service.ReadLocalContent(ctx, projectintegrations.LocalReadInput{
			ProjectID:     strings.TrimSpace(input.ID),
			Provider:      projectintegrations.ProviderJira,
			ItemIDOrKey:   strings.TrimSpace(input.Key),
			MaxChunkBytes: input.MaxChunkBytes,
		})
		if err != nil {
			return nil, err
		}
		return toolResult(result), nil
	case "projects.confluence.page.get", "projects_confluence_page_get":
		var input struct {
			ID            string          `json:"id"`
			PageID        string          `json:"page_id"`
			MaxChunkBytes int             `json:"max_chunk_bytes,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid integration arguments", projectintegrations.ErrInvalidInput)
		}
		result, err := service.ReadLocalContent(ctx, projectintegrations.LocalReadInput{
			ProjectID:     strings.TrimSpace(input.ID),
			Provider:      projectintegrations.ProviderConfluence,
			ItemIDOrKey:   strings.TrimSpace(input.PageID),
			MaxChunkBytes: input.MaxChunkBytes,
		})
		if err != nil {
			return nil, err
		}
		return toolResult(result), nil
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
