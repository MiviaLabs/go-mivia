package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

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

func CallToolWithIngestion(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion *projectingestion.Service, name string, arguments json.RawMessage) (map[string]any, error) {
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
		run, err := ingestion.IngestProject(ctx, strings.TrimSpace(input.ID), projectingestion.TriggerManual)
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
	case "projects.files.list", "projects_files_list":
		var input struct {
			ID        string          `json:"id"`
			Status    string          `json:"status,omitempty"`
			Extension string          `json:"extension,omitempty"`
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
		filter, err := fileFilter(input.Status, input.Extension)
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
			ID        string          `json:"id"`
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
		symbols, err := ingestion.ListSymbols(ctx, strings.TrimSpace(input.ID), projectingestion.Pagination{PageSize: input.PageSize, PageToken: input.PageToken})
		return toolResult(symbols), err
	default:
		return nil, projectregistry.ErrProjectNotFound
	}
}

func ReadResource(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, uri string) (map[string]any, error) {
	return ReadResourceWithIngestion(ctx, registry, digest, nil, uri)
}

func ReadResourceWithIngestion(ctx context.Context, registry *projectregistry.Registry, digest *projectregistry.DigestService, ingestion *projectingestion.Service, uri string) (map[string]any, error) {
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
			"name":        "projects.files.list",
			"title":       "List Project Files",
			"description": "List bounded file ingestion metadata without root paths or skipped sensitive content.",
			"inputSchema": objectSchema(mergeProperties(pageProperties, map[string]any{
				"id":        map[string]any{"type": "string", "minLength": 1},
				"status":    map[string]any{"type": "string", "enum": []string{"eligible", "skipped", "absent"}},
				"extension": map[string]any{"type": "string", "description": "File extension filter, with or without a leading dot. Whitespace and path separators are invalid."},
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
				"id": map[string]any{"type": "string", "minLength": 1},
			}), []string{"id"}),
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
	}
}

func fileFilter(rawStatus string, rawExtension string) (projectingestion.FileStateFilter, error) {
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
