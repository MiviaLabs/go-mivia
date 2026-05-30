package mcpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/mcpapi"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/service"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
)

func TestToolsList_ReturnsTaskAndResearchTools(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"tasks.create"`)) || !bytes.Contains(res.Body.Bytes(), []byte(`"research_runs.create"`)) {
		t.Fatalf("expected tool discovery response, got %s", res.Body.String())
	}
}

func TestToolsCall_CreateAndGetTask(t *testing.T) {
	handler := newHandler()
	create := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP task"}}}`)
	if create.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", create.Code, create.Body.String())
	}
	var created rpcResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	taskID := created.Result.StructuredContent["id"].(string)

	get := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tasks.get","arguments":{"id":"`+taskID+`"}}}`)
	if get.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", get.Code, get.Body.String())
	}
	if !bytes.Contains(get.Body.Bytes(), []byte(taskID)) {
		t.Fatalf("expected fetched task id, got %s", get.Body.String())
	}
}

func TestToolsCall_AllowsMCPMeta(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP task"},"_meta":{"progressToken":"token-1"}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_AllowsMetaInsideArguments(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"MCP task","_meta":{"source":"codex"}}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_AllowsJSONStringArguments(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":"{\"title\":\"MCP task\"}"}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_AllowsUnderscoreToolAlias(t *testing.T) {
	handler := newHandler()
	res := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks_create","arguments":{"title":"MCP task"}}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if bytes.Contains(res.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected tool call success, got %s", res.Body.String())
	}
}

func TestToolsCall_RejectsRawQueryArgument(t *testing.T) {
	res := postMCP(t, newHandler(), `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"Task","query":"MATCH (n)"}}}`)
	if !bytes.Contains(res.Body.Bytes(), []byte(`"code":-32602`)) {
		t.Fatalf("expected invalid argument error, got %s", res.Body.String())
	}
}

func TestResourcesRead_ReturnsTaskJSON(t *testing.T) {
	handler := newHandler()
	create := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tasks.create","arguments":{"title":"Resource task"}}}`)
	var created rpcResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	taskID := created.Result.StructuredContent["id"].(string)

	read := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"mivialabs://tasks/`+taskID+`"}}`)
	if read.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", read.Code, read.Body.String())
	}
	if !bytes.Contains(read.Body.Bytes(), []byte(`"mimeType":"application/json"`)) {
		t.Fatalf("expected json resource, got %s", read.Body.String())
	}
}

type rpcResponse struct {
	Result struct {
		StructuredContent map[string]any `json:"structuredContent"`
	} `json:"result"`
}

func newHandler() http.Handler {
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	return mcpapi.NewHandler(svc, slog.Default())
}

func postMCP(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpapi.ProtocolVersion)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}
