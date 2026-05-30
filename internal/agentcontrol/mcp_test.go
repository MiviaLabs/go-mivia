package agentcontrol

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPInitialize_ReturnsServerInfo(t *testing.T) {
	handler := NewMCPHandler(NewService(NewMemoryStore()), slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"protocolVersion":"2025-06-18"`)) {
		t.Fatalf("expected protocol version in response: %s", res.Body.String())
	}
}

func TestMCPRejectsNonLocalOrigin(t *testing.T) {
	handler := NewMCPHandler(NewService(NewMemoryStore()), slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "https://example.com")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden origin, got %d", res.Code)
	}
}

func TestMCPRequiresExplicitAcceptTypes(t *testing.T) {
	handler := NewMCPHandler(NewService(NewMemoryStore()), slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	res := httptest.NewRecorder()

	handler.ServeHTTP(res, req)

	if res.Code != http.StatusNotAcceptable {
		t.Fatalf("expected invalid accept to be rejected, got %d", res.Code)
	}
}
