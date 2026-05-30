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

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func ToolDefinitions() []map[string]any {
	return ToolDefinitionsWithIngestion(false)
}

func ToolDefinitionsWithIngestion(includeIngestion bool) []map[string]any {
	tools := []map[string]any{
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
	if includeIngestion {
		tools = append(tools, ingestionToolDefinitions()...)
	}
	return tools
}

func ResourceTemplates() []map[string]any {
	return ResourceTemplatesWithIngestion(false)
}

func ResourceTemplatesWithIngestion(includeIngestion bool) []map[string]any {
	templates := []map[string]any{
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
	if includeIngestion {
		templates = append(templates, ingestionResourceTemplates()...)
	}
	return templates
}

func CallTool(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, name string, arguments json.RawMessage) (map[string]any, error) {
	return CallToolWithIngestion(ctx, registry, digest, nil, name, arguments)
}

func CallToolWithIngestion(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion projectingestion.API, name string, arguments json.RawMessage) (map[string]any, error) {
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
	case "projects.ingest", "projects_ingest":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		run, err := ingestion.SubmitIngestProject(ctx, strings.TrimSpace(input.ID), projectingestion.TriggerManual)
		return toolResult(projectingestion.MetadataForRun(run)), err
	case "projects.ingestion_status", "projects_ingestion_status":
		var input struct {
			ID    string          `json:"id"`
			RunID string          `json:"run_id"`
			Meta  json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		run, err := ingestion.RunMetadata(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.RunID))
		return toolResult(run), err
	case "projects.ingestion_status_latest", "projects_ingestion_status_latest", "projects.ingestion_latest", "projects_ingestion_latest":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		run, err := ingestion.LatestRunMetadata(ctx, strings.TrimSpace(input.ID))
		return toolResult(run), err
	case "projects.files.list", "projects_files_list":
		var input struct {
			ID            string          `json:"id"`
			Status        string          `json:"status,omitempty"`
			Extension     string          `json:"extension,omitempty"`
			PathPrefix    string          `json:"path_prefix,omitempty"`
			SkippedReason string          `json:"skipped_reason,omitempty"`
			Present       *bool           `json:"present,omitempty"`
			ModifiedSince string          `json:"modified_since,omitempty"`
			PageSize      int             `json:"page_size,omitempty"`
			PageToken     string          `json:"page_token,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		filter, err := fileFilter(input.Status, input.Extension, input.PathPrefix, input.SkippedReason, input.Present, input.ModifiedSince)
		if err != nil {
			return nil, err
		}
		files, err := ingestion.ListFiles(ctx, strings.TrimSpace(input.ID), filter, projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(files), err
	case "projects.files.get", "projects_files_get":
		var input struct {
			ID     string          `json:"id"`
			FileID string          `json:"file_id"`
			Meta   json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		file, err := ingestion.GetFile(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.FileID))
		return toolResult(file), err
	case "projects.file.chunks", "projects_file_chunks":
		var input struct {
			ID            string          `json:"id"`
			FileID        string          `json:"file_id"`
			PageSize      int             `json:"page_size,omitempty"`
			PageToken     string          `json:"page_token,omitempty"`
			MaxChunkBytes int             `json:"max_chunk_bytes,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		chunks, err := ingestion.ListChunks(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.FileID), projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken}, input.MaxChunkBytes)
		return toolResult(chunks), err
	case "projects.symbols.list", "projects_symbols_list":
		var input struct {
			ID            string          `json:"id"`
			Kind          string          `json:"kind,omitempty"`
			NamePrefix    string          `json:"name_prefix,omitempty"`
			NameContains  string          `json:"name_contains,omitempty"`
			FileID        string          `json:"file_id,omitempty"`
			Extension     string          `json:"extension,omitempty"`
			Package       string          `json:"package,omitempty"`
			Receiver      string          `json:"receiver,omitempty"`
			CaseSensitive bool            `json:"case_sensitive,omitempty"`
			PageSize      int             `json:"page_size,omitempty"`
			PageToken     string          `json:"page_token,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		symbols, err := ingestion.ListSymbols(ctx, strings.TrimSpace(input.ID), projectingestion.SymbolFilter{
			Kind:          projectingestion.SymbolKind(strings.TrimSpace(input.Kind)),
			NamePrefix:    input.NamePrefix,
			NameContains:  input.NameContains,
			FileID:        strings.TrimSpace(input.FileID),
			Extension:     input.Extension,
			Package:       input.Package,
			Receiver:      input.Receiver,
			CaseSensitive: input.CaseSensitive,
		}, projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(symbols), err
	case "projects.search.text", "projects_search_text":
		var input struct {
			ID              string          `json:"id"`
			Query           string          `json:"query"`
			Mode            string          `json:"mode,omitempty"`
			CaseSensitive   bool            `json:"case_sensitive,omitempty"`
			Extension       string          `json:"extension,omitempty"`
			PathPrefix      string          `json:"path_prefix,omitempty"`
			PageSize        int             `json:"page_size,omitempty"`
			PageToken       string          `json:"page_token,omitempty"`
			MaxSnippetBytes int             `json:"max_snippet_bytes,omitempty"`
			MaxMatches      int             `json:"max_matches,omitempty"`
			Meta            json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		results, err := ingestion.SearchText(ctx, strings.TrimSpace(input.ID), projectingestion.TextSearchOptions{
			Query:           input.Query,
			Mode:            input.Mode,
			CaseSensitive:   input.CaseSensitive,
			Extension:       input.Extension,
			PathPrefix:      input.PathPrefix,
			PageSize:        input.PageSize,
			PageToken:       input.PageToken,
			MaxSnippetBytes: input.MaxSnippetBytes,
			MaxMatches:      input.MaxMatches,
		})
		return toolResult(results), err
	case "projects.search.files", "projects_search_files":
		var input struct {
			ID            string          `json:"id"`
			Extension     string          `json:"extension,omitempty"`
			PathPrefix    string          `json:"path_prefix,omitempty"`
			PathContains  string          `json:"path_contains,omitempty"`
			CaseSensitive bool            `json:"case_sensitive,omitempty"`
			PageSize      int             `json:"page_size,omitempty"`
			PageToken     string          `json:"page_token,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		files, err := ingestion.SearchFiles(ctx, strings.TrimSpace(input.ID), projectingestion.FileSearchOptions{
			Extension:     input.Extension,
			PathPrefix:    input.PathPrefix,
			PathContains:  input.PathContains,
			CaseSensitive: input.CaseSensitive,
			PageSize:      input.PageSize,
			PageToken:     input.PageToken,
		})
		return toolResult(files), err
	case "projects.search.symbols", "projects_search_symbols":
		var input struct {
			ID            string          `json:"id"`
			Kind          string          `json:"kind,omitempty"`
			NamePrefix    string          `json:"name_prefix,omitempty"`
			NameContains  string          `json:"name_contains,omitempty"`
			FileID        string          `json:"file_id,omitempty"`
			Extension     string          `json:"extension,omitempty"`
			Package       string          `json:"package,omitempty"`
			Receiver      string          `json:"receiver,omitempty"`
			CaseSensitive bool            `json:"case_sensitive,omitempty"`
			PageSize      int             `json:"page_size,omitempty"`
			PageToken     string          `json:"page_token,omitempty"`
			Meta          json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		symbols, err := ingestion.SearchSymbols(ctx, strings.TrimSpace(input.ID), projectingestion.SymbolFilter{
			Kind:          projectingestion.SymbolKind(strings.TrimSpace(input.Kind)),
			NamePrefix:    input.NamePrefix,
			NameContains:  input.NameContains,
			FileID:        strings.TrimSpace(input.FileID),
			Extension:     input.Extension,
			Package:       input.Package,
			Receiver:      input.Receiver,
			CaseSensitive: input.CaseSensitive,
		}, projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(symbols), err
	case "projects.search.references", "projects_search_references":
		var input struct {
			ID                 string          `json:"id"`
			NameContains       string          `json:"name_contains,omitempty"`
			TargetNameContains string          `json:"target_name_contains,omitempty"`
			EnclosingContains  string          `json:"enclosing_contains,omitempty"`
			Extension          string          `json:"extension,omitempty"`
			PathPrefix         string          `json:"path_prefix,omitempty"`
			ResolutionStatus   string          `json:"resolution_status,omitempty"`
			Confidence         string          `json:"confidence,omitempty"`
			CaseSensitive      bool            `json:"case_sensitive,omitempty"`
			PageSize           int             `json:"page_size,omitempty"`
			PageToken          string          `json:"page_token,omitempty"`
			Meta               json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		refs, err := ingestion.SearchReferences(ctx, strings.TrimSpace(input.ID), projectingestion.ReferenceSearchOptions{
			NameContains:       input.NameContains,
			TargetNameContains: input.TargetNameContains,
			EnclosingContains:  input.EnclosingContains,
			Extension:          input.Extension,
			PathPrefix:         input.PathPrefix,
			ResolutionStatus:   input.ResolutionStatus,
			Confidence:         input.Confidence,
			CaseSensitive:      input.CaseSensitive,
			PageSize:           input.PageSize,
			PageToken:          input.PageToken,
		})
		return toolResult(refs), err
	case "projects.search.calls", "projects_search_calls":
		var input struct {
			ID                 string          `json:"id"`
			NameContains       string          `json:"name_contains,omitempty"`
			CallerNameContains string          `json:"caller_name_contains,omitempty"`
			CalleeNameContains string          `json:"callee_name_contains,omitempty"`
			Extension          string          `json:"extension,omitempty"`
			PathPrefix         string          `json:"path_prefix,omitempty"`
			ResolutionStatus   string          `json:"resolution_status,omitempty"`
			Confidence         string          `json:"confidence,omitempty"`
			CaseSensitive      bool            `json:"case_sensitive,omitempty"`
			PageSize           int             `json:"page_size,omitempty"`
			PageToken          string          `json:"page_token,omitempty"`
			Meta               json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		calls, err := ingestion.SearchCalls(ctx, strings.TrimSpace(input.ID), projectingestion.ReferenceSearchOptions{
			NameContains:       input.NameContains,
			CallerNameContains: input.CallerNameContains,
			CalleeNameContains: input.CalleeNameContains,
			Extension:          input.Extension,
			PathPrefix:         input.PathPrefix,
			ResolutionStatus:   input.ResolutionStatus,
			Confidence:         input.Confidence,
			CaseSensitive:      input.CaseSensitive,
			PageSize:           input.PageSize,
			PageToken:          input.PageToken,
		})
		return toolResult(calls), err
	case "projects.symbol.source", "projects_symbol_source":
		var input struct {
			ID             string          `json:"id"`
			SymbolID       string          `json:"symbol_id"`
			MaxSourceBytes int             `json:"max_source_bytes,omitempty"`
			Meta           json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		source, err := ingestion.GetSymbolSource(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.SymbolID), projectingestion.SymbolSourceOptions{MaxSourceBytes: input.MaxSourceBytes})
		return toolResult(source), err
	case "projects.symbol.references", "projects_symbol_references":
		var input struct {
			ID        string          `json:"id"`
			SymbolID  string          `json:"symbol_id"`
			PageSize  int             `json:"page_size,omitempty"`
			PageToken string          `json:"page_token,omitempty"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		refs, err := ingestion.ListSymbolReferences(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.SymbolID), projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(refs), err
	case "projects.symbol.callers", "projects_symbol_callers":
		var input struct {
			ID        string          `json:"id"`
			SymbolID  string          `json:"symbol_id"`
			PageSize  int             `json:"page_size,omitempty"`
			PageToken string          `json:"page_token,omitempty"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		edges, err := ingestion.ListSymbolCallers(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.SymbolID), projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(edges), err
	case "projects.symbol.callees", "projects_symbol_callees":
		var input struct {
			ID        string          `json:"id"`
			SymbolID  string          `json:"symbol_id"`
			PageSize  int             `json:"page_size,omitempty"`
			PageToken string          `json:"page_token,omitempty"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		edges, err := ingestion.ListSymbolCallees(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.SymbolID), projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(edges), err
	case "projects.symbol.call_graph", "projects_symbol_call_graph":
		var input struct {
			ID        string          `json:"id"`
			SymbolID  string          `json:"symbol_id"`
			Direction string          `json:"direction,omitempty"`
			MaxDepth  int             `json:"max_depth,omitempty"`
			MaxNodes  int             `json:"max_nodes,omitempty"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		graph, err := ingestion.GetSymbolCallGraph(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.SymbolID), projectingestion.CallGraphOptions{
			Direction: input.Direction,
			MaxDepth:  input.MaxDepth,
			MaxNodes:  input.MaxNodes,
		})
		return toolResult(graph), err
	case "projects.headings.list", "projects_headings_list":
		var input struct {
			ID        string          `json:"id"`
			FileID    string          `json:"file_id,omitempty"`
			PageSize  int             `json:"page_size,omitempty"`
			PageToken string          `json:"page_token,omitempty"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		headings, err := ingestion.ListHeadings(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.FileID), projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(headings), err
	case "projects.file.outline", "projects_file_outline":
		var input struct {
			ID               string          `json:"id"`
			FileID           string          `json:"file_id"`
			Kind             string          `json:"kind,omitempty"`
			NamePrefix       string          `json:"name_prefix,omitempty"`
			SymbolPageSize   int             `json:"symbol_page_size,omitempty"`
			SymbolPageToken  string          `json:"symbol_page_token,omitempty"`
			IncludeChunkText bool            `json:"include_chunk_text,omitempty"`
			MaxChunkBytes    int             `json:"max_chunk_bytes,omitempty"`
			Meta             json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid ingestion arguments", projectregistry.ErrInvalidInput)
		}
		if ingestion == nil {
			return nil, fmt.Errorf("%w: ingestion service is not configured", projectingestion.ErrUnsupportedIngest)
		}
		outline, err := ingestion.GetFileOutline(ctx, strings.TrimSpace(input.ID), strings.TrimSpace(input.FileID), projectingestion.FileOutlineOptions{
			SymbolFilter: projectingestion.SymbolFilter{
				Kind:       projectingestion.SymbolKind(strings.TrimSpace(input.Kind)),
				NamePrefix: input.NamePrefix,
			},
			SymbolPagination: projectingestion.Pagination{PageSize: input.SymbolPageSize, PageToken: input.SymbolPageToken},
			IncludeChunkText: input.IncludeChunkText,
			MaxChunkBytes:    input.MaxChunkBytes,
		})
		return toolResult(outline), err
	default:
		return nil, projectregistry.ErrProjectNotFound
	}
}

func ReadResource(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, uri string) (map[string]any, error) {
	return ReadResourceWithIngestion(ctx, registry, digest, nil, uri)
}

func ReadResourceWithIngestion(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion projectingestion.API, uri string) (map[string]any, error) {
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
	if ingestion != nil && len(parts) == 3 && parts[1] == "files" {
		file, err := ingestion.GetFile(ctx, parts[0], parts[2])
		if err != nil {
			return nil, err
		}
		return resourceResult(uri, file)
	}
	if ingestion != nil && len(parts) == 5 && parts[1] == "files" && parts[3] == "chunks" {
		chunk, err := ingestion.GetChunk(ctx, parts[0], parts[2], parts[4], 0)
		if err != nil {
			return nil, err
		}
		return resourceResult(uri, chunk)
	}
	if ingestion != nil && len(parts) == 3 && parts[1] == "symbols" {
		symbol, err := ingestion.GetSymbol(ctx, parts[0], parts[2])
		if err != nil {
			return nil, err
		}
		return resourceResult(uri, symbol)
	}
	if ingestion != nil && len(parts) == 4 && parts[1] == "files" && parts[3] == "outline" {
		outline, err := ingestion.GetFileOutline(ctx, parts[0], parts[2], projectingestion.FileOutlineOptions{})
		if err != nil {
			return nil, err
		}
		return resourceResult(uri, outline)
	}
	return nil, projectregistry.ErrProjectNotFound
}

func ingestionToolDefinitions() []map[string]any {
	pageProperties := map[string]any{
		"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": projectingestion.MaxPageSize},
		"page_token": map[string]any{"type": "string"},
	}
	return []map[string]any{
		{
			"name":        "projects.ingest",
			"title":       "Run Content Graph Ingestion",
			"description": "Run bounded manual content_graph ingestion for an opted-in local project.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
		{
			"name":        "projects.ingestion_status",
			"title":       "Get Project Ingestion Run",
			"description": "Fetch non-sensitive ingestion run metadata by project id and run id.",
			"inputSchema": objectSchema(map[string]any{
				"id":     map[string]any{"type": "string", "minLength": 1},
				"run_id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id", "run_id"}),
		},
		{
			"name":        "projects.ingestion_status_latest",
			"title":       "Get Latest Project Ingestion Run",
			"description": "Fetch the latest non-sensitive ingestion run metadata for a project.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
		{
			"name":        "projects.files.list",
			"title":       "List Project Files",
			"description": "List bounded file ingestion metadata without root paths or skipped sensitive content.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":             map[string]any{"type": "string", "minLength": 1},
				"status":         map[string]any{"type": "string", "enum": []string{"eligible", "skipped", "absent"}},
				"extension":      map[string]any{"type": "string", "description": "File extension filter, with or without a leading dot. Whitespace and path separators are invalid."},
				"path_prefix":    map[string]any{"type": "string", "description": "Safe project-relative path prefix. Absolute paths and parent traversal are invalid."},
				"skipped_reason": map[string]any{"type": "string"},
				"present":        map[string]any{"type": "boolean"},
				"modified_since": map[string]any{"type": "string", "format": "date-time"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.files.get",
			"title":       "Get Project File",
			"description": "Fetch bounded file ingestion metadata by opaque file id without root paths or skipped sensitive content.",
			"inputSchema": objectSchema(map[string]any{
				"id":      map[string]any{"type": "string", "minLength": 1},
				"file_id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id", "file_id"}),
		},
		{
			"name":        "projects.file.chunks",
			"title":       "List Project File Chunks",
			"description": "List bounded chunk text for an opaque file id after ingestion safety gates pass.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":              map[string]any{"type": "string", "minLength": 1},
				"file_id":         map[string]any{"type": "string", "minLength": 1},
				"max_chunk_bytes": map[string]any{"type": "integer", "minimum": 1},
			}), []string{"id", "file_id"}),
		},
		{
			"name":        "projects.symbols.list",
			"title":       "List Project Symbols",
			"description": "List bounded symbol metadata for an opted-in content graph project.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":             map[string]any{"type": "string", "minLength": 1},
				"kind":           map[string]any{"type": "string"},
				"name_prefix":    map[string]any{"type": "string"},
				"name_contains":  map[string]any{"type": "string"},
				"file_id":        map[string]any{"type": "string"},
				"extension":      map[string]any{"type": "string"},
				"package":        map[string]any{"type": "string"},
				"receiver":       map[string]any{"type": "string"},
				"case_sensitive": map[string]any{"type": "boolean"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.search.text",
			"title":       "Search Indexed Project Text",
			"description": "Literal-only bounded search over eligible indexed content chunks. Results may be stale until ingestion catches up; snippets are capped and skipped sensitive files are never returned.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":                map[string]any{"type": "string", "minLength": 1},
				"query":             map[string]any{"type": "string", "minLength": 1, "maxLength": projectingestion.MaxSearchQueryBytes},
				"mode":              map[string]any{"type": "string", "enum": []string{"literal"}},
				"case_sensitive":    map[string]any{"type": "boolean"},
				"extension":         map[string]any{"type": "string"},
				"path_prefix":       map[string]any{"type": "string"},
				"max_snippet_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": projectingestion.MaxSnippetBytes},
				"max_matches":       map[string]any{"type": "integer", "minimum": 1, "maximum": projectingestion.MaxPageSize},
			}), []string{"id", "query"}),
		},
		{
			"name":        "projects.search.files",
			"title":       "Search Indexed Project Files",
			"description": "Search eligible indexed file metadata by safe project-relative path. Skipped, denied, sensitive, absent, and root paths are not returned.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":             map[string]any{"type": "string", "minLength": 1},
				"extension":      map[string]any{"type": "string"},
				"path_prefix":    map[string]any{"type": "string"},
				"path_contains":  map[string]any{"type": "string"},
				"case_sensitive": map[string]any{"type": "boolean"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.search.symbols",
			"title":       "Search Indexed Project Symbols",
			"description": "Search eligible indexed symbol metadata by prefix or substring without source text. Results may be stale until ingestion catches up.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":             map[string]any{"type": "string", "minLength": 1},
				"kind":           map[string]any{"type": "string"},
				"name_prefix":    map[string]any{"type": "string"},
				"name_contains":  map[string]any{"type": "string"},
				"file_id":        map[string]any{"type": "string"},
				"extension":      map[string]any{"type": "string"},
				"package":        map[string]any{"type": "string"},
				"receiver":       map[string]any{"type": "string"},
				"case_sensitive": map[string]any{"type": "boolean"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.search.references",
			"title":       "Search Indexed Project References",
			"description": "Search eligible indexed reference metadata by name, target, and enclosing symbol. No skipped sensitive source text or root paths are returned.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":                   map[string]any{"type": "string", "minLength": 1},
				"name_contains":        map[string]any{"type": "string"},
				"target_name_contains": map[string]any{"type": "string"},
				"enclosing_contains":   map[string]any{"type": "string"},
				"extension":            map[string]any{"type": "string"},
				"path_prefix":          map[string]any{"type": "string"},
				"resolution_status":    map[string]any{"type": "string"},
				"confidence":           map[string]any{"type": "string"},
				"case_sensitive":       map[string]any{"type": "boolean"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.search.calls",
			"title":       "Search Indexed Project Calls",
			"description": "Search eligible indexed call metadata by caller or callee name. No skipped sensitive source text or root paths are returned.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":                   map[string]any{"type": "string", "minLength": 1},
				"name_contains":        map[string]any{"type": "string"},
				"caller_name_contains": map[string]any{"type": "string"},
				"callee_name_contains": map[string]any{"type": "string"},
				"extension":            map[string]any{"type": "string"},
				"path_prefix":          map[string]any{"type": "string"},
				"resolution_status":    map[string]any{"type": "string"},
				"confidence":           map[string]any{"type": "string"},
				"case_sensitive":       map[string]any{"type": "boolean"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.symbol.source",
			"title":       "Get Project Symbol Source",
			"description": "Fetch bounded source text for one eligible indexed symbol without root paths or skipped sensitive content.",
			"inputSchema": objectSchema(map[string]any{
				"id":               map[string]any{"type": "string", "minLength": 1},
				"symbol_id":        map[string]any{"type": "string", "minLength": 1},
				"max_source_bytes": map[string]any{"type": "integer", "minimum": 1},
			}, []string{"id", "symbol_id"}),
		},
		{
			"name":        "projects.symbol.references",
			"title":       "List Project Symbol References",
			"description": "List bounded indexed references that resolve to one symbol.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":        map[string]any{"type": "string", "minLength": 1},
				"symbol_id": map[string]any{"type": "string", "minLength": 1},
			}), []string{"id", "symbol_id"}),
		},
		{
			"name":        "projects.symbol.callers",
			"title":       "List Project Symbol Callers",
			"description": "List bounded direct caller edges for one indexed symbol.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":        map[string]any{"type": "string", "minLength": 1},
				"symbol_id": map[string]any{"type": "string", "minLength": 1},
			}), []string{"id", "symbol_id"}),
		},
		{
			"name":        "projects.symbol.callees",
			"title":       "List Project Symbol Callees",
			"description": "List bounded direct callee edges for one indexed symbol.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":        map[string]any{"type": "string", "minLength": 1},
				"symbol_id": map[string]any{"type": "string", "minLength": 1},
			}), []string{"id", "symbol_id"}),
		},
		{
			"name":        "projects.symbol.call_graph",
			"title":       "Get Project Symbol Call Graph",
			"description": "Traverse bounded direct call edges for one indexed symbol.",
			"inputSchema": objectSchema(map[string]any{
				"id":        map[string]any{"type": "string", "minLength": 1},
				"symbol_id": map[string]any{"type": "string", "minLength": 1},
				"direction": map[string]any{"type": "string", "enum": []string{"callers", "callees", "both"}},
				"max_depth": map[string]any{"type": "integer", "minimum": 1, "maximum": projectingestion.MaxCallGraphDepth},
				"max_nodes": map[string]any{"type": "integer", "minimum": 1, "maximum": projectingestion.MaxCallGraphNodes},
			}, []string{"id", "symbol_id"}),
		},
		{
			"name":        "projects.headings.list",
			"title":       "List Project Document Headings",
			"description": "List bounded Markdown/document heading metadata without chunk text.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":      map[string]any{"type": "string", "minLength": 1},
				"file_id": map[string]any{"type": "string"},
			}), []string{"id"}),
		},
		{
			"name":        "projects.file.outline",
			"title":       "Get Project File Outline",
			"description": "Fetch bounded file metadata, headings, symbols, and chunk ids without full chunk text.",
			"inputSchema": objectSchema(map[string]any{
				"id":                 map[string]any{"type": "string", "minLength": 1},
				"file_id":            map[string]any{"type": "string", "minLength": 1},
				"kind":               map[string]any{"type": "string"},
				"name_prefix":        map[string]any{"type": "string"},
				"symbol_page_size":   map[string]any{"type": "integer", "minimum": 1, "maximum": projectingestion.MaxPageSize},
				"symbol_page_token":  map[string]any{"type": "string"},
				"include_chunk_text": map[string]any{"type": "boolean"},
				"max_chunk_bytes":    map[string]any{"type": "integer", "minimum": 1},
			}, []string{"id", "file_id"}),
		},
	}
}

func ingestionResourceTemplates() []map[string]any {
	return []map[string]any{
		{
			"uriTemplate": "mivialabs://projects/{id}/files/{file_id}",
			"name":        "project_file",
			"title":       "Project File",
			"description": "Project file ingestion metadata by opaque file id.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "mivialabs://projects/{id}/files/{file_id}/chunks/{chunk_id}",
			"name":        "project_file_chunk",
			"title":       "Project File Chunk",
			"description": "Bounded project file chunk by opaque chunk id.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "mivialabs://projects/{id}/symbols/{symbol_id}",
			"name":        "project_symbol",
			"title":       "Project Symbol",
			"description": "Project symbol metadata by opaque symbol id.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "mivialabs://projects/{id}/files/{file_id}/outline",
			"name":        "project_file_outline",
			"title":       "Project File Outline",
			"description": "Bounded project file outline without full chunk text.",
			"mimeType":    "application/json",
		},
	}
}

func fileFilter(rawStatus string, rawExtension string, rawPathPrefix string, rawSkippedReason string, present *bool, rawModifiedSince string) (projectingestion.FileStateFilter, error) {
	filter := projectingestion.FileStateFilter{}
	status := strings.TrimSpace(rawStatus)
	if status != "" {
		switch projectingestion.FileStatus(status) {
		case projectingestion.FileStatusEligible, projectingestion.FileStatusSkipped, projectingestion.FileStatusAbsent:
			filter.Status = projectingestion.FileStatus(status)
		default:
			return projectingestion.FileStateFilter{}, projectregistry.ErrInvalidInput
		}
	}
	extension, err := projectingestion.NormalizeFileExtension(rawExtension)
	if err != nil {
		return projectingestion.FileStateFilter{}, err
	}
	filter.Extension = extension
	pathPrefix, err := projectingestion.NormalizePathPrefix(rawPathPrefix)
	if err != nil {
		return projectingestion.FileStateFilter{}, err
	}
	filter.PathPrefix = pathPrefix
	skippedReason := strings.TrimSpace(rawSkippedReason)
	if skippedReason != "" {
		filter.SkippedReason = projectingestion.SkipReason(skippedReason)
	}
	filter.Present = present
	modifiedSince := strings.TrimSpace(rawModifiedSince)
	if modifiedSince != "" {
		parsed, err := time.Parse(time.RFC3339, modifiedSince)
		if err != nil {
			return projectingestion.FileStateFilter{}, projectregistry.ErrInvalidInput
		}
		filter.ModifiedSince = parsed.UTC()
	}
	return filter, nil
}

func mergeProperties(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
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
