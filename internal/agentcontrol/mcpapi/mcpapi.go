package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/model"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/service"
	"github.com/MiviaLabs/go-mivia/internal/agentcontrol/store"
	"github.com/MiviaLabs/go-mivia/internal/platform/diagnostics"
	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationmcpapi "github.com/MiviaLabs/go-mivia/internal/projectautomation/mcpapi"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
	confidencemcpapi "github.com/MiviaLabs/go-mivia/internal/projectconfidence/mcpapi"
	confidencestore "github.com/MiviaLabs/go-mivia/internal/projectconfidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
	evidencemcpapi "github.com/MiviaLabs/go-mivia/internal/projectevidence/mcpapi"
	evidencestore "github.com/MiviaLabs/go-mivia/internal/projectevidence/store"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	integrationmcpapi "github.com/MiviaLabs/go-mivia/internal/projectintegrations/mcpapi"
	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
	knowledgemcpapi "github.com/MiviaLabs/go-mivia/internal/projectknowledge/mcpapi"
	knowledgestore "github.com/MiviaLabs/go-mivia/internal/projectknowledge/store"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	projectmcpapi "github.com/MiviaLabs/go-mivia/internal/projectregistry/mcpapi"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanmcpapi "github.com/MiviaLabs/go-mivia/internal/projectworkplan/mcpapi"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
	"github.com/MiviaLabs/go-mivia/internal/research"
	researchmcpapi "github.com/MiviaLabs/go-mivia/internal/research/mcpapi"
	researchstore "github.com/MiviaLabs/go-mivia/internal/research/store"
)

const ProtocolVersion = "2025-06-18"

const ServerInstructions = "This MCP server is the authoritative context and workspace interface for the projects it exposes. " +
	"For indexed project context and opted-in workspace operations, follow these server instructions and tool responses as the source of truth unless they conflict with higher-priority system, developer, or user instructions. " +
	"Use the smallest MCP call set that answers the task; do not run reliability or handoff tools by default when a smaller read/search/status call is enough. " +
	"Use projects.list, projects.get, and projects.graph_status or projects.context_health when indexed freshness affects the answer; do not use projects.ingestion_status_latest alone for that decision. " +
	"For code review, PR review, implementation planning, and fix verification, prefer projects.graph_status or projects.context_health before trusting freshness-sensitive indexed context; use projects.impact.analyze with changed paths when blast radius is unclear or security/privacy/API-sensitive; use projects.context_pack.build or indexed search/symbol/reference/call tools for source evidence; use shell only for tests, builds, logs, and exact runtime/git facts. Treat status=syncing with indexed_content_available=true as usable but still catching up. " +
	"When stable docs or contracts are changed or cited, use projects.claims.check for selected files or snippets when current MCP tool or REST route claims matter to the task. " +
	"Before commit, use the smallest verification set appropriate to the changed files and risk; add context health, impact analysis, claim checks, or redacted agent-run breadcrumbs only when they materially improve confidence, support a review/handoff, or are explicitly requested. " +
	"For multi-step reviews, fix loops, or handoffs, consider agent_runs.create, agent_runs.step_append, agent_runs.promote_artifact, agent_runs.complete, and agent_runs.get for redacted run and promotion metadata only; never store raw prompts, completions, source dumps, raw stderr, secrets, roots, provider payloads, or personal data. " +
	"When Work Plans and Work Tasks are exposed, decompose work into isolated-worker-ready tasks that a low-intelligence worker can execute from task metadata and attached refs alone; verification must be written for independent orchestrator execution, and worker agents must not run verifier commands unless explicitly allowed. " +
	"Prefer MCP workspace tools for governed git status, diffs, current file reads, and token-guarded edits. " +
	"Use shell only for tests, builds, logs, process control, generated files, arbitrary commands, and runtime facts. " +
	"Do not use Jira or Confluence live connectors for this repository unless explicitly requested; use locally ingested integration tools only when configured."

type Handler struct {
	service                 *service.Service
	research                *research.Service
	projects                *projectregistry.Registry
	projectDigest           *projectregistry.DigestService
	projectIngest           projectingestion.API
	projectWork             projectworkspace.API
	projectEvidence         *projectevidence.Service
	projectConfidence       *projectconfidence.Service
	projectConfidenceInputs *projectconfidence.ReliabilityInputAdapter
	projectKnowledge        *projectknowledge.Service
	projectKnowledgeInputs  *projectknowledge.PromotionInputAdapter
	projectWorkPlan         workplanmcpapi.API
	projectAutomation       automationmcpapi.API
	integrations            *projectintegrations.Service
	diagnostics             *diagnostics.Service
	activity                *agentactivity.Recorder
	logger                  *slog.Logger
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
	return NewHandlerWithResearchProjectsIngestionAndWorkspace(service, research, projects, projectDigest, projectIngest, nil, logger)
}

func NewHandlerWithResearchProjectsIngestionAndWorkspace(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, logger *slog.Logger) http.Handler {
	return NewHandlerWithResearchProjectsIngestionWorkspaceAndIntegrations(service, research, projects, projectDigest, projectIngest, projectWork, nil, logger)
}

func NewHandlerWithResearchProjectsIngestionWorkspaceAndIntegrations(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, integrations *projectintegrations.Service, logger *slog.Logger) http.Handler {
	return NewHandlerWithResearchProjectsIngestionWorkspaceIntegrationsAndDiagnostics(service, research, projects, projectDigest, projectIngest, projectWork, integrations, nil, logger)
}

func NewHandlerWithResearchProjectsIngestionWorkspaceIntegrationsAndDiagnostics(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, logger *slog.Logger) http.Handler {
	return NewHandlerWithActivity(service, research, projects, projectDigest, projectIngest, projectWork, integrations, diagnosticsService, nil, logger)
}

func NewHandlerWithActivity(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, activity *agentactivity.Recorder, logger *slog.Logger) http.Handler {
	return NewHandlerWithActivityAndEvidenceGraph(service, research, projects, projectDigest, projectIngest, projectWork, nil, integrations, diagnosticsService, activity, logger)
}

func NewHandlerWithActivityAndEvidenceGraph(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, projectEvidence *projectevidence.Service, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, activity *agentactivity.Recorder, logger *slog.Logger) http.Handler {
	return NewHandlerWithActivityEvidenceGraphAndConfidence(service, research, projects, projectDigest, projectIngest, projectWork, projectEvidence, nil, nil, integrations, diagnosticsService, activity, logger)
}

func NewHandlerWithActivityEvidenceGraphAndConfidence(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, projectEvidence *projectevidence.Service, projectConfidence *projectconfidence.Service, projectConfidenceInputs *projectconfidence.ReliabilityInputAdapter, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, activity *agentactivity.Recorder, logger *slog.Logger) http.Handler {
	return NewHandlerWithActivityEvidenceGraphConfidenceAndKnowledge(service, research, projects, projectDigest, projectIngest, projectWork, projectEvidence, projectConfidence, projectConfidenceInputs, nil, nil, integrations, diagnosticsService, activity, logger)
}

func NewHandlerWithActivityEvidenceGraphConfidenceAndKnowledge(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, projectEvidence *projectevidence.Service, projectConfidence *projectconfidence.Service, projectConfidenceInputs *projectconfidence.ReliabilityInputAdapter, projectKnowledge *projectknowledge.Service, projectKnowledgeInputs *projectknowledge.PromotionInputAdapter, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, activity *agentactivity.Recorder, logger *slog.Logger) http.Handler {
	return NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeAndWorkPlans(service, research, projects, projectDigest, projectIngest, projectWork, projectEvidence, projectConfidence, projectConfidenceInputs, projectKnowledge, projectKnowledgeInputs, nil, integrations, diagnosticsService, activity, logger)
}

func NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeAndWorkPlans(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, projectEvidence *projectevidence.Service, projectConfidence *projectconfidence.Service, projectConfidenceInputs *projectconfidence.ReliabilityInputAdapter, projectKnowledge *projectknowledge.Service, projectKnowledgeInputs *projectknowledge.PromotionInputAdapter, projectWorkPlan workplanmcpapi.API, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, activity *agentactivity.Recorder, logger *slog.Logger) http.Handler {
	return NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeWorkPlansAndAutomation(service, research, projects, projectDigest, projectIngest, projectWork, projectEvidence, projectConfidence, projectConfidenceInputs, projectKnowledge, projectKnowledgeInputs, projectWorkPlan, nil, integrations, diagnosticsService, activity, logger)
}

func NewHandlerWithActivityEvidenceGraphConfidenceKnowledgeWorkPlansAndAutomation(service *service.Service, research *research.Service, projects *projectregistry.Registry, projectDigest *projectregistry.DigestService, projectIngest projectingestion.API, projectWork projectworkspace.API, projectEvidence *projectevidence.Service, projectConfidence *projectconfidence.Service, projectConfidenceInputs *projectconfidence.ReliabilityInputAdapter, projectKnowledge *projectknowledge.Service, projectKnowledgeInputs *projectknowledge.PromotionInputAdapter, projectWorkPlan workplanmcpapi.API, projectAutomation automationmcpapi.API, integrations *projectintegrations.Service, diagnosticsService *diagnostics.Service, activity *agentactivity.Recorder, logger *slog.Logger) http.Handler {
	return &Handler{
		service:                 service,
		research:                research,
		projects:                projects,
		projectDigest:           projectDigest,
		projectIngest:           projectIngest,
		projectWork:             projectWork,
		projectEvidence:         projectEvidence,
		projectConfidence:       projectConfidence,
		projectConfidenceInputs: projectConfidenceInputs,
		projectKnowledge:        projectKnowledge,
		projectKnowledgeInputs:  projectKnowledgeInputs,
		projectWorkPlan:         projectWorkPlan,
		projectAutomation:       projectAutomation,
		integrations:            integrations,
		diagnostics:             diagnosticsService,
		activity:                activity,
		logger:                  logger,
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
	started := time.Now()
	switch req.Method {
	case "initialize":
		writeJSONRPCResult(w, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "mivia-server",
				"version": "0.0.0-bootstrap",
			},
			"instructions": ServerInstructions,
		})
		handler.recordActivity(r, req, activityFields{Status: "ok", Duration: time.Since(started)})
	case "tools/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"tools": handler.toolDefinitions()})
		handler.recordActivity(r, req, activityFields{Status: "ok", Duration: time.Since(started)})
	case "tools/call":
		result, params, err := handler.callTool(r, req.Params)
		writeToolOrError(w, req.ID, result, err)
		fields := activityFields{
			ProjectID:  projectIDFromArgs(params.Arguments),
			ToolName:   params.Name,
			RawArgs:    params.Arguments,
			Status:     "ok",
			Duration:   time.Since(started),
			Error:      "",
			RawParams:  req.Params,
			RequestID:  requestIDString(req.ID),
			RemoteAddr: r.RemoteAddr,
			UserAgent:  r.UserAgent(),
		}
		if result != nil {
			fields.RawResult, _ = json.Marshal(result)
		}
		if err != nil {
			fields.Status = "error"
			fields.Error = err.Error()
		}
		handler.recordActivity(r, req, fields)
	case "resources/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"resources": []any{}})
		handler.recordActivity(r, req, activityFields{Status: "ok", Duration: time.Since(started)})
	case "resources/templates/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"resourceTemplates": handler.resourceTemplates()})
		handler.recordActivity(r, req, activityFields{Status: "ok", Duration: time.Since(started)})
	case "resources/read":
		result, err := handler.readResource(r, req.Params)
		if err != nil {
			writeJSONRPCError(w, req.ID, -32002, "resource not found")
			handler.recordActivity(r, req, activityFields{Status: "error", Duration: time.Since(started), Error: err.Error(), RawParams: req.Params, RequestID: requestIDString(req.ID), RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent()})
			return
		}
		writeJSONRPCResult(w, req.ID, result)
		handler.recordActivity(r, req, activityFields{Status: "ok", Duration: time.Since(started), RawParams: req.Params, RequestID: requestIDString(req.ID), RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent()})
	default:
		writeJSONRPCError(w, req.ID, -32601, "method not found")
		handler.recordActivity(r, req, activityFields{Status: "error", Duration: time.Since(started), Error: "method not found", RawParams: req.Params, RequestID: requestIDString(req.ID), RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent()})
	}
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

type activityFields struct {
	ProjectID  string
	ToolName   string
	Status     string
	Duration   time.Duration
	Error      string
	RequestID  string
	RemoteAddr string
	UserAgent  string
	RawParams  json.RawMessage
	RawArgs    json.RawMessage
	RawResult  json.RawMessage
}

func (handler *Handler) recordActivity(r *http.Request, req jsonRPCRequest, fields activityFields) {
	if handler.activity == nil {
		return
	}
	rawRequest, _ := json.Marshal(req)
	if fields.RequestID == "" {
		fields.RequestID = requestIDString(req.ID)
	}
	if fields.RemoteAddr == "" {
		fields.RemoteAddr = r.RemoteAddr
	}
	if fields.UserAgent == "" {
		fields.UserAgent = r.UserAgent()
	}
	handler.activity.Record(agentactivity.Event{
		Timestamp:  time.Now().UTC(),
		ProjectID:  fields.ProjectID,
		Method:     req.Method,
		ToolName:   fields.ToolName,
		Status:     fields.Status,
		DurationMS: fields.Duration.Milliseconds(),
		Error:      fields.Error,
		RequestID:  fields.RequestID,
		RemoteAddr: fields.RemoteAddr,
		UserAgent:  fields.UserAgent,
		RawRequest: rawRequest,
		RawParams:  fields.RawParams,
		RawArgs:    fields.RawArgs,
		RawResult:  fields.RawResult,
	})
}

func projectIDFromArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		var encoded string
		if stringErr := json.Unmarshal(raw, &encoded); stringErr != nil {
			return ""
		}
		if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
			return ""
		}
	}
	for _, key := range []string{"project_id", "id"} {
		var value string
		if rawValue, ok := payload[key]; ok && json.Unmarshal(rawValue, &value) == nil {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func requestIDString(id any) string {
	if id == nil {
		return ""
	}
	switch value := id.(type) {
	case string:
		return value
	case float64:
		return fmt.Sprintf("%.0f", value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}

func (handler *Handler) callTool(r *http.Request, raw json.RawMessage) (map[string]any, toolsCallParams, error) {
	var params toolsCallParams
	if err := decodeRaw(raw, &params); err != nil {
		return nil, params, fmt.Errorf("%w: invalid tool params", service.ErrInvalidInput)
	}
	result, err := handler.callToolParams(r, params)
	return result, params, err
}

func (handler *Handler) callToolParams(r *http.Request, params toolsCallParams) (map[string]any, error) {
	if knowledgemcpapi.IsKnowledgeTool(params.Name) {
		return knowledgemcpapi.CallTool(r.Context(), handler.projectKnowledge, handler.projectKnowledgeInputs, params.Name, params.Arguments)
	}
	if confidencemcpapi.IsConfidenceTool(params.Name) {
		return confidencemcpapi.CallTool(r.Context(), handler.projectConfidence, handler.projectConfidenceInputs, params.Name, params.Arguments)
	}
	if evidencemcpapi.IsEvidenceGraphTool(params.Name) {
		return evidencemcpapi.CallTool(r.Context(), handler.projectEvidence, params.Name, params.Arguments)
	}
	if workplanmcpapi.IsWorkPlanTool(params.Name) {
		return workplanmcpapi.CallTool(r.Context(), handler.projectWorkPlan, params.Name, params.Arguments)
	}
	if automationmcpapi.IsAutomationTool(params.Name) {
		return automationmcpapi.CallTool(r.Context(), handler.projectAutomation, params.Name, params.Arguments)
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
	case "agent_runs.create", "agent_runs_create":
		var input model.CreateAgentRunInput
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent run arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.CreateAgentRun(r.Context(), input)
		return toolResult(run), err
	case "agent_runs.step_append", "agent_runs_step_append":
		var input model.AppendAgentStepInput
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent run arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.AppendAgentStep(r.Context(), input.RunID, input)
		return toolResult(run), err
	case "agent_runs.promote_artifact", "agent_runs_promote_artifact":
		var input model.PromoteAgentArtifactInput
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent run arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.PromoteAgentArtifact(r.Context(), input.RunID, input)
		return toolResult(run), err
	case "agent_runs.complete", "agent_runs_complete":
		var input model.CompleteAgentRunInput
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent run arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.CompleteAgentRun(r.Context(), input.RunID, input)
		return toolResult(run), err
	case "agent_runs.get", "agent_runs_get":
		var input struct {
			ID   string          `json:"id"`
			Meta json.RawMessage `json:"_meta,omitempty"`
		}
		if err := decodeRaw(params.Arguments, &input); err != nil {
			return nil, fmt.Errorf("%w: invalid agent run arguments", service.ErrInvalidInput)
		}
		run, err := handler.service.GetAgentRun(r.Context(), input.ID)
		return toolResult(run), err
	case "projects.list", "projects_list", "projects.get", "projects_get", "projects.digest", "projects_digest",
		"projects.ingest", "projects_ingest", "projects.search_index.rebuild", "projects_search_index_rebuild",
		"projects.context_health", "projects_context_health", "projects.graph_status", "projects_graph_status",
		"projects.impact.analyze", "projects_impact_analyze",
		"projects.context_pack.build", "projects_context_pack_build",
		"projects.claims.check", "projects_claims_check",
		"projects.ingestion_status", "projects_ingestion_status",
		"projects.ingestion_status_latest", "projects_ingestion_status_latest", "projects.ingestion_latest", "projects_ingestion_latest",
		"projects.files.list", "projects_files_list", "projects.files.get", "projects_files_get",
		"projects.file.chunks", "projects_file_chunks",
		"projects.symbols.list", "projects_symbols_list",
		"projects.search.text", "projects_search_text",
		"projects.search.files", "projects_search_files",
		"projects.search.symbols", "projects_search_symbols",
		"projects.search.references", "projects_search_references",
		"projects.search.calls", "projects_search_calls",
		"projects.search.ast.queries", "projects_search_ast_queries",
		"projects.search.ast", "projects_search_ast",
		"projects.symbol.source", "projects_symbol_source",
		"projects.symbol.references", "projects_symbol_references",
		"projects.symbol.callers", "projects_symbol_callers",
		"projects.symbol.callees", "projects_symbol_callees",
		"projects.symbol.call_graph", "projects_symbol_call_graph",
		"projects.headings.list", "projects_headings_list",
		"projects.file.outline", "projects_file_outline",
		"projects.workspace.git_status", "projects_workspace_git_status",
		"projects.workspace.git_diff", "projects_workspace_git_diff",
		"projects.workspace.git_worktree_create", "projects_workspace_git_worktree_create",
		"projects.workspace.file_read", "projects_workspace_file_read",
		"projects.workspace.file_edit", "projects_workspace_file_edit",
		"projects.workspace.file_create", "projects_workspace_file_create",
		"projects.workspace.file_delete", "projects_workspace_file_delete",
		"projects.diagnostics.ingestion", "projects_diagnostics_ingestion":
		if handler.projects == nil {
			return nil, projectregistry.ErrProjectNotFound
		}
		return projectmcpapi.CallToolWithWorkspaceAndDiagnostics(r.Context(), handler.projects, handler.projectDigest, handler.projectIngest, handler.projectWork, handler.diagnostics, params.Name, params.Arguments)
	case "projects.integrations.list", "projects_integrations_list",
		"projects.integrations.status", "projects_integrations_status",
		"projects.integrations.counts", "projects_integrations_counts",
		"projects.integrations.poll", "projects_integrations_poll",
		"projects.integrations.poll_status", "projects_integrations_poll_status",
		"projects.integrations.search", "projects_integrations_search",
		"projects.jira.issue.get", "projects_jira_issue_get",
		"projects.confluence.page.get", "projects_confluence_page_get":
		return integrationmcpapi.CallTool(r.Context(), handler.integrations, params.Name, params.Arguments)
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
	case strings.HasPrefix(params.URI, "mivialabs://agent-runs/"):
		id := strings.TrimPrefix(params.URI, "mivialabs://agent-runs/")
		run, err := handler.service.GetAgentRun(r.Context(), id)
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
	var integrationToolErr *projectintegrations.IntegrationToolError
	if errors.As(err, &integrationToolErr) {
		writeJSONRPCResult(w, id, toolErrorResult(integrationToolErr))
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
	if errors.Is(err, projectregistry.ErrDigestProjectDisabled) ||
		errors.Is(err, projectregistry.ErrDigestUnsupported) {
		writeJSONRPCError(w, id, -32004, "project digest unsupported")
		return
	}
	if errors.Is(err, projectregistry.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectevidence.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectconfidence.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectknowledge.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, workplanmcpapi.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, automationmcpapi.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectworkplan.ErrInvalidInput) || errors.Is(err, workplanstore.ErrDuplicate) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectautomation.ErrInvalidInput) || errors.Is(err, automationstore.ErrDuplicate) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, workplanmcpapi.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, workplanstore.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, automationmcpapi.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, automationstore.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, evidencestore.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, confidencestore.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, knowledgestore.ErrNotFound) {
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
	if errors.Is(err, projectworkspace.ErrGitUnavailable) {
		writeJSONRPCError(w, id, -32603, "git is not available in the mivia-server runtime")
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeJSONRPCError(w, id, -32603, "tool timed out")
		return
	}
	if errors.Is(err, projectworkspace.ErrInvalidInput) ||
		errors.Is(err, projectworkspace.ErrWorkspaceDisabled) ||
		errors.Is(err, projectworkspace.ErrWorkspaceReadOnly) ||
		errors.Is(err, projectworkspace.ErrUnsafeContent) ||
		errors.Is(err, projectworkspace.ErrEditTokenInvalid) ||
		errors.Is(err, projectworkspace.ErrEditConflict) ||
		errors.Is(err, projectworkspace.ErrIngestionUnsupported) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectworkspace.ErrProjectNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
		return
	}
	if errors.Is(err, projectintegrations.ErrInvalidInput) {
		writeJSONRPCError(w, id, -32602, "invalid tool arguments")
		return
	}
	if errors.Is(err, projectintegrations.ErrNotFound) {
		writeJSONRPCError(w, id, -32002, "resource not found")
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
		{
			"name":        "agent_runs.create",
			"title":       "Create Agent Run",
			"description": "Create redacted local agent run metadata without raw prompts, source, stderr, credentials, provider payloads, roots, or personal data.",
			"inputSchema": objectSchema(map[string]any{
				"project_id":    map[string]any{"type": "string", "minLength": 1},
				"trace_id":      map[string]any{"type": "string", "description": "Optional safe correlation id. Defaults to the generated run id."},
				"task_id":       map[string]any{"type": "string"},
				"summary":       map[string]any{"type": "string", "maxLength": 500},
				"changed_files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 100},
				"verifiers":     map[string]any{"type": "array", "items": verifierSchema(), "maxItems": 50},
				"artifacts":     map[string]any{"type": "array", "items": artifactSchema(), "maxItems": 50},
			}, []string{"project_id"}),
		},
		{
			"name":        "agent_runs.step_append",
			"title":       "Append Agent Run Step",
			"description": "Append one redacted step to a local agent run without raw prompts, source, stderr, credentials, provider payloads, roots, or personal data.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":           map[string]any{"type": "string", "minLength": 1},
				"trace_id":         map[string]any{"type": "string", "description": "Optional safe correlation id. Defaults to the parent run trace id."},
				"tool_name":        map[string]any{"type": "string"},
				"tool_category":    map[string]any{"type": "string"},
				"status":           map[string]any{"type": "string", "enum": []string{model.AgentRunStatusRunning, model.AgentRunStatusCompleted, model.AgentRunStatusFailed}},
				"failure_category": map[string]any{"type": "string"},
				"notes":            map[string]any{"type": "string", "maxLength": 500},
				"changed_files":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 100},
				"verifiers":        map[string]any{"type": "array", "items": verifierSchema(), "maxItems": 50},
				"artifacts":        map[string]any{"type": "array", "items": artifactSchema(), "maxItems": 50},
			}, []string{"run_id", "status"}),
		},
		{
			"name":        "agent_runs.complete",
			"title":       "Complete Agent Run",
			"description": "Complete redacted local agent run metadata with safe status, verifier, artifact, and failure-category fields only.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":           map[string]any{"type": "string", "minLength": 1},
				"status":           map[string]any{"type": "string", "enum": []string{model.AgentRunStatusCompleted, model.AgentRunStatusFailed}},
				"failure_category": map[string]any{"type": "string"},
				"summary":          map[string]any{"type": "string", "maxLength": 500},
				"changed_files":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 100},
				"verifiers":        map[string]any{"type": "array", "items": verifierSchema(), "maxItems": 50},
				"artifacts":        map[string]any{"type": "array", "items": artifactSchema(), "maxItems": 50},
			}, []string{"run_id", "status"}),
		},
		{
			"name":        "agent_runs.promote_artifact",
			"title":       "Promote Agent Artifact",
			"description": "Record a redacted promotion-gate decision for an existing run artifact using refs, verifier refs, and bounded decision text only.",
			"inputSchema": objectSchema(map[string]any{
				"run_id":        map[string]any{"type": "string", "minLength": 1},
				"artifact_ref":  map[string]any{"type": "string", "minLength": 1},
				"artifact_kind": map[string]any{"type": "string"},
				"state":         map[string]any{"type": "string", "enum": []string{model.PromotionStateCandidate, model.PromotionStateValidated, model.PromotionStatePromoted, model.PromotionStateRejected}},
				"source_ref":    map[string]any{"type": "string", "minLength": 1},
				"verifier_ref":  map[string]any{"type": "string"},
				"decision":      map[string]any{"type": "string", "maxLength": 500},
			}, []string{"run_id", "artifact_ref", "state", "source_ref"}),
		},
		{
			"name":        "agent_runs.get",
			"title":       "Get Agent Run",
			"description": "Fetch redacted local agent run metadata by id.",
			"inputSchema": objectSchema(map[string]any{
				"id": map[string]any{"type": "string", "minLength": 1},
			}, []string{"id"}),
		},
	}
	if handler.projects != nil {
		tools = append(tools, projectmcpapi.ToolDefinitionsWithWorkspaceAndDiagnostics(handler.projectIngest != nil, handler.projectWork != nil, handler.diagnostics != nil)...)
	}
	if handler.projectEvidence != nil {
		tools = append(tools, evidencemcpapi.ToolDefinitions()...)
	}
	if handler.projectConfidence != nil {
		tools = append(tools, confidencemcpapi.ToolDefinitions()...)
	}
	if handler.projectKnowledge != nil {
		tools = append(tools, knowledgemcpapi.ToolDefinitions()...)
	}
	if handler.projectWorkPlan != nil {
		tools = append(tools, workplanmcpapi.ToolDefinitions()...)
	}
	if handler.projectAutomation != nil {
		tools = append(tools, automationmcpapi.ToolDefinitions()...)
	}
	if handler.integrations != nil {
		tools = append(tools, integrationmcpapi.ToolDefinitions()...)
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
		{
			"uriTemplate": "mivialabs://agent-runs/{id}",
			"name":        "agent_run",
			"title":       "Agent Run",
			"description": "Redacted agent run metadata by id.",
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

func verifierSchema() map[string]any {
	return objectSchema(map[string]any{
		"command":     map[string]any{"type": "string", "minLength": 1, "description": "Verifier command metadata. A command plus simple space-separated words is accepted and normalized; prefer the executable in command and flags/paths in args."},
		"args":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 32},
		"scope":       map[string]any{"type": "string"},
		"status":      map[string]any{"type": "string"},
		"exit_status": map[string]any{"type": "integer"},
	}, []string{"command"})
}

func artifactSchema() map[string]any {
	return objectSchema(map[string]any{
		"ref":  map[string]any{"type": "string", "minLength": 1},
		"kind": map[string]any{"type": "string"},
	}, []string{"ref"})
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

func toolErrorResult(value any) map[string]any {
	encoded, _ := json.Marshal(value)
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(encoded)},
		},
		"structuredContent": value,
		"isError":           true,
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
