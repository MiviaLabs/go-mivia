package mcpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/mcpapi"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/service"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/agentcontrol/store"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
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

func TestProjectToolsListAndCall_WhenProjectRegistryConfigured(t *testing.T) {
	handler := newHandlerWithProjects(t)
	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if list.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", list.Code, list.Body.String())
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.list"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.digest"`)) {
		t.Fatalf("expected project tools, got %s", list.Body.String())
	}

	call := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects.digest","arguments":{"id":"example-service"}}}`)
	if call.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", call.Code, call.Body.String())
	}
	if bytes.Contains(call.Body.Bytes(), []byte("package main")) || bytes.Contains(call.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("project digest leaked content markers: %s", call.Body.String())
	}
}

func TestProjectIngestionMCPToolsAndResources(t *testing.T) {
	handler, root := newHandlerWithProjectIngestion(t)

	list := postMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.ingest"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.file.chunks"`)) {
		t.Fatalf("expected ingestion tools, got %s", list.Body.String())
	}

	templates := postMCP(t, handler, `{"jsonrpc":"2.0","id":2,"method":"resources/templates/list"}`)
	if !bytes.Contains(templates.Body.Bytes(), []byte(`mivialabs://projects/{id}/files/{file_id}`)) {
		t.Fatalf("expected ingestion resource templates, got %s", templates.Body.String())
	}

	ingest := postMCP(t, handler, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"projects_ingest","arguments":{"id":"example-service","_meta":{"source":"test"}}}}`)
	if bytes.Contains(ingest.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected ingest success, got %s", ingest.Body.String())
	}
	if bytes.Contains(ingest.Body.Bytes(), []byte(root)) || bytes.Contains(ingest.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("ingest response leaked sensitive metadata: %s", ingest.Body.String())
	}

	files := postMCP(t, handler, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects.files.list","arguments":"{\"id\":\"example-service\",\"page_size\":1}"}}`)
	if bytes.Contains(files.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected files success, got %s", files.Body.String())
	}
	var filesRPC rpcResponse
	if err := json.Unmarshal(files.Body.Bytes(), &filesRPC); err != nil {
		t.Fatalf("decode files response: %v", err)
	}
	fileItems := filesRPC.Result.StructuredContent["files"].([]any)
	fileID := fileItems[0].(map[string]any)["id"].(string)

	chunks := postMCP(t, handler, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"projects.file.chunks","arguments":{"id":"example-service","file_id":"`+fileID+`","max_chunk_bytes":10}}}`)
	if bytes.Contains(chunks.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected chunks success, got %s", chunks.Body.String())
	}
	if bytes.Contains(chunks.Body.Bytes(), []byte(root)) || bytes.Contains(chunks.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("chunk response leaked forbidden metadata: %s", chunks.Body.String())
	}

	read := postMCP(t, handler, `{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"mivialabs://projects/example-service/files/`+fileID+`"}}`)
	if read.Code != http.StatusOK || bytes.Contains(read.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected file resource success, got %s", read.Body.String())
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

func newHandlerWithProjects(t *testing.T) http.Handler {
	t.Helper()
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "example-service",
		DisplayName:    "Example Service",
		RootPath:       root,
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     projectregistry.DigestModeMetadataOnly,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
		Include:        []string{"**/*.go"},
	}}, projectregistry.Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	return mcpapi.NewHandlerWithResearchAndProjects(svc, nil, registry, digest, slog.Default())
}

func newHandlerWithProjectIngestion(t *testing.T) (http.Handler, string) {
	t.Helper()
	mem := store.NewMemoryStore()
	svc := service.New(mem, mem)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0o700); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "main.go"), []byte("package main\n\nfunc Run() { println(\"hello\") }\n"), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example-service",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          projectregistry.UpdatePolicyManual,
		Include:               []string{"**/*.go"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: projectregistry.SensitiveMarkerPolicySkipFile,
	}}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(t.Context(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(t.Context(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	ingestion := projectingestion.NewService(registry, projectingestion.NewGraphStore(graph), projectingestion.NewSQLiteStore(db.SQLDB()))
	return mcpapi.NewHandlerWithResearchProjectsAndIngestion(svc, nil, registry, digest, ingestion, slog.Default()), root
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
