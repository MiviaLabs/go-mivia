package mcpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/model"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/service"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	projectmcpapi "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/mcpapi"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research"
	researchmcpapi "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/mcpapi"
	researchstore "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/research/store"
)

const ProtocolVersion = "2025-06-18"

type Handler struct {
	service       *service.Service
	research      *research.Service
	projects      *projectregistry.Registry
	projectDigest *projectregistry.DigestService
	projectIngest projectingestion.API
	logger        *slog.Logger
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func NewHandler(service *service.Service, logger *slog.Logger) http.Handler {
	return &Handler{service: service, logger: logger}
}

func NewHandlerWithResearch(service *service.Service, research *research.Service, logger *slog.Logger) http.Handler {
	return &Handler{service: service, research: research, logger: logger}
}

func NewHandlerWithResearchAndProjects(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, logger *slog.Logger) http.Handler {
	return NewHandlerWithResearchProjectsAndIngestion(service, research, projects, projectDigest, nil, logger)
}

func NewHandlerWithResearchProjectsAndIngestion(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, logger *slog.Logger) http.Handler {
	return &Handler{
		service:       service,
		research:      research,
		projects:      projects,
		projectDigest: projectDigest,
		projectIngest: projectIngest,
		logger:        logger,
	}
}

func (handler *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validOrigin(r.Header.Get("Origin")) {
		httpserver.WriteError(w, http.StatusForbidden, "origin_forbidden", "origin is not allowed")
		return
	}
	switch r.Method {
	case http.MethodPost:
		handler.handlePost(w, r)
	case http.MethodGet:
		w.Header().Set("Allow", http.MethodPost)
		httpserver.WriteError(w, http.StatusMethodNotAllowed, "sse_unsupported", "server-sent event streams are not implemented")
	default:
		w.Header().Set("Allow", http.MethodPost)
		httpserver.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
	}
}

func (handler *Handler) handlePost(w http.ResponseWriter, r *http.Request) {
	if !accepts(r.Header.Get("Accept"), "application/json") || !accepts(r.Header.Get("Accept"), "text/event-stream") {
		httpserver.WriteError(w, http.StatusNotAcceptable, "invalid_accept", "accept must include application/json and text/event-stream")
		return
	}
	if !httpserver.RequireJSON(r) {
		httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	if protocol := r.Header.Get("MCP-Protocol-Version"); protocol != "" && protocol != ProtocolVersion {
		httpserver.WriteError(w, http.StatusBadRequest, "unsupported_mcp_protocol_version", "unsupported MCP protocol version")
		return
	}

	var req jsonRPCRequest
	if err := httpserver.DecodeJSON(r, &req); err != nil {
		writeJSONRPCError(w, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, -32600, "invalid request")
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	handler.dispatch(w, r, req)
}

func (handler *Handler) dispatch(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		writeJSONRPCResult(w, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "mivialabs-agent-server",
				"version": "0.0.0-bootstrap",
			},
		})
	case "tools/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"tools": handler.toolDefinitions()})
	case "tools/call":
		result, err := handler.callTool(r, req.Params)
		writeToolOrError(w, req.ID, result, err)
	case "resources/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"resources": []any{}})
	case "resources/templates/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"resourceTemplates": handler.resourceTemplates()})
	case "resources/read":
		result, err := handler.readResource(r, req.Params)
		if err != nil {
			writeJSONRPCError(w, req.ID, -32002, "resource not found")
			return
		}
		writeJSONRPCResult(w, req.ID, result)
	default:
		writeJSONRPCError(w, req.ID, -32601, "method not found")
	}
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

func (handler *Handler) callTool(r *http.Request, raw json.RawMessage) (map[string]any, error) {
	var params toolsCallParams
	if err := decodeRaw(raw, &params); err != nil {
		return nil, fmt.Errorf("%w: invalid tool params", service.ErrInvalidInput)
	}
	switch params.Name {
	case "tasks.create", "tasks_create":
		var input struct {
			Title string          `json:"title"`
			Meta  json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid task arguments", service.ErrInvalidInput)
		}
		task, err := handler.service.CreateTask(r.Context(), model.CreateTaskInput{Title: input.Title})
		return toolResult(task), err
	case "tasks.get", "tasks_get":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid task arguments", service.ErrInvalidInput)
		}
		task, err := handler.service.GetTask(r.Context(), input.ID)
		return toolResult(task), err
	case "research_runs.create", "research_runs_create":
		var input struct {
			TaskID      string          `json:"task_id"`
			GoalSummary string          `json:"goal_summary"`
			Meta        json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid research arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.CreateResearchRun(r.Context(), model.CreateResearchRunInput{
			TaskID:      input.TaskID,
			GoalSummary: input.GoalSummary,
		})
		return toolResult(run), err
	case "research_runs.get", "research_runs_get":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid research arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.GetResearchRun(r.Context(), input.ID)
		return toolResult(run), err
	case "projects.list", "projects_list", "projects.get", "projects_get", "projects.digest", "projects_digest",
		"projects.ingest", "projects_ingest", "projects.ingestion_status", "projects_ingestion_status",
		"projects.ingestion_status_latest", "projects_ingestion_status_latest", "projects.ingestion_latest", "projects_ingestion_latest",
		"projects.files.list", "projects_files_list", "projects.files.get", "projects_files_get",
		"projects.file.chunks", "projects_file_chunks",
		"projects.symbols.list", "projects_symbols_list",
		"projects.symbol.source", "projects_symbol_source",
		"projects.symbol.references", "projects_symbol_references",
		"projects.symbol.callers", "projects_symbol_callers",
		"projects.symbol.callees", "projects_symbol_callees",
		"projects.symbol.call_graph", "projects_symbol_call_graph",
		"projects.headings.list", "projects_headings_list",
		"projects.file.outline", "projects_file_outline":
		if handler.projects == nil {
			return nil, projectregistry.ErrProjectNotFound
		}
		return projectmcpapi.CallToolWithIngestion(r.Context(), handler.projects, handler.projectDigest, handler.projectIngest, params.Name, params.Arguments)
	default:
		if handler.research != nil {
			return researchmcpapi.CallTool(r.Context(), handler.research, params.Name, params.Arguments)
		}
		return nil, fmt.Errorf("%w: unknown tool", service.ErrInvalidInput)
	}
}

type resourceReadParams struct {
	URI  string          `json:"uri"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

func (handler *Handler) readResource(r *http.Request, raw json.RawMessage) (map[string]any, error) {
	var params resourceReadParams
	if err := decodeRaw(raw, &params); err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(params.URI, "mivialabs://tasks/"):
		id := strings.TrimPrefix(params.URI, "mivialabs://tasks/")
		task, err := handler.service.GetTask(r.Context(), id)
		if err != nil {
			return nil, err
		}
		return resourceResult(params.URI, task)
	case strings.HasPrefix(params.URI, "mivialabs://research-runs/"):
		id := strings.TrimPrefix(params.URI, "mivialabs://research-runs/")
		run, err := handler.service.GetResearchRun(r.Context(), id)
		if err != nil {
			return nil, err
		}
		return resourceResult(params.URI, run)
	case strings.HasPrefix(params.URI, "mivialabs://projects/"):
		if handler.projects == nil {
			return nil, projectregistry.ErrProjectNotFound
		}
		return projectmcpapi.ReadResourceWithIngestion(r.Context(), handler.projects, handler.projectDigest, handler.projectIngest, params.URI)
	default:
		if handler.research != nil {
			return researchmcpapi.ReadResource(r.Context(), handler.research, params.URI)
		}
		return nil, store.ErrNotFound
	}
}

func writeToolOrError(w http.ResponseWriter, id any, result map[string]any, err error) {
	if err == nil {
		writeJSONRPCResult(w, id, result)
		return
	}
	if errors.Is(err, service.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, research.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectregistry.ErrInvalidInput) ||
		errors.Is(err, projectregistry.ErrDigestProjectDisabled) ||
		errors.Is(err, projectregistry.ErrDigestUnsupported) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, researchstore.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, projectregistry.ErrProjectNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, projectingestion.ErrIngestionNotFound) ||
		errors.Is(err, projectingestion.ErrRunNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, projectingestion.ErrInvalidInput) ||
		errors.Is(err, projectingestion.ErrProjectDisabled) ||
		errors.Is(err, projectingestion.ErrUnsupportedIngest) ||
		errors.Is(err, projectingestion.ErrPathEscapesRoot) ||
		errors.Is(err, projectingestion.ErrPathNotProjectLocal) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	writeJSONRPCError(w, id, -32603, "internal error")
}

func (handler *Handler) toolDefinitions() []map[string]any {
	tools := []map[string]any{
		{
			"name":        "tasks.create",
			"title":       "Create Task",
			"description": "Create a local agent task without raw database query execution.",
			"inputSchema": objectSchema(map[string]any{
				"title": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			}, []string{"title"}),
		},
		{
			"name":        "tasks.get",
			"title":       "Get Task",
			"description": "Fetch a local agent task by id.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
		{
			"name":        "research_runs.create",
			"title":       "Create Research Run",
			"description": "Create fixture-only research run metadata without live provider execution.",
			"inputSchema": objectSchema(map[string]any{
				"task_id":      map[string]any{"type": "string", "minLength": 1},
				"goal_summary": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			}, []string{"task_id", "goal_summary"}),
		},
		{
			"name":        "research_runs.get",
			"title":       "Get Research Run",
			"description": "Fetch research run metadata by id.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
	}
	if handler.projects != nil {
		tools = append(tools, projectmcpapi.ToolDefinitionsWithIngestion(handler.projectIngest != nil)...)
	}
	return append(tools, researchmcpapi.ToolDefinitions()...)
}

func (handler *Handler) resourceTemplates() []map[string]any {
	templates := []map[string]any{
		{
			"uriTemplate": "mivialabs://tasks/{id}",
			"name":        "task",
			"title":       "Task",
			"description": "Task metadata by id.",
			"mimeType":    "application/json",
		},
		{
			"uriTemplate": "mivialabs://research-runs/{id}",
			"name":        "research_run",
			"title":       "Research Run",
			"description": "Research run metadata by id.",
			"mimeType":    "application/json",
		},
	}
	if handler.projects != nil {
		templates = append(templates, projectmcpapi.ResourceTemplatesWithIngestion(handler.projectIngest != nil)...)
	}
	return append(templates, researchmcpapi.ResourceTemplates()...)
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

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func accepts(header string, mediaType string) bool {
	for _, part := range strings.Split(header, ",") {
		value := strings.TrimSpace(strings.Split(part, ";")[0])
		if value == mediaType {
			return true
		}
	}
	return false
}

func validOrigin(raw string) bool {
	if raw == "" {
		return true
	}
	origin, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := origin.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
