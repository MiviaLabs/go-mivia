package agentcontrol

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/httpserver"
)

const mcpProtocolVersion = "2025-06-18"

type MCPHandler struct {
	service *Service
	logger  *slog.Logger
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func NewMCPHandler(service *Service, logger *slog.Logger) http.Handler {
	return &MCPHandler{service: service, logger: logger}
}

func (handler *MCPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validMCPOrigin(r.Header.Get("Origin")) {
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

func (handler *MCPHandler) handlePost(w http.ResponseWriter, r *http.Request) {
	if !accepts(r.Header.Get("Accept"), "application/json") || !accepts(r.Header.Get("Accept"), "text/event-stream") {
		httpserver.WriteError(w, http.StatusNotAcceptable, "invalid_accept", "accept must include application/json and text/event-stream")
		return
	}
	if !httpserver.RequireJSON(r) {
		httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}
	if protocol := r.Header.Get("MCP-Protocol-Version"); protocol != "" && protocol != mcpProtocolVersion {
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

	switch req.Method {
	case "initialize":
		writeJSONRPCResult(w, req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
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
		writeJSONRPCResult(w, req.ID, map[string]any{"tools": []any{}})
	case "resources/list":
		writeJSONRPCResult(w, req.ID, map[string]any{"resources": []any{}})
	default:
		writeJSONRPCError(w, req.ID, -32601, "method not found")
	}
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

func validMCPOrigin(raw string) bool {
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
