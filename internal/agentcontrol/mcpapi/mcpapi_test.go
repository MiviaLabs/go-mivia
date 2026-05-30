package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.ingest"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search_index.rebuild"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.file.chunks"`)) {
		t.Fatalf("expected ingestion tools, got %s", list.Body.String())
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search.text"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search.calls"`)) || !bytes.Contains(list.Body.Bytes(), []byte(`"projects.search.ast.queries"`)) {
		t.Fatalf("expected project search tools, got %s", list.Body.String())
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
	var ingestRPC rpcResponse
	if err := json.Unmarshal(ingest.Body.Bytes(), &ingestRPC); err != nil {
		t.Fatalf("decode ingest response: %v", err)
	}
	waitMCPIngestionRun(t, handler, ingestRPC.Result.StructuredContent["id"].(string))

	latest := postMCP(t, handler, `{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"projects.ingestion_status_latest","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(latest.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected latest ingestion status success, got %s", latest.Body.String())
	}
	if bytes.Contains(latest.Body.Bytes(), []byte(root)) || bytes.Contains(latest.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("latest ingestion status leaked sensitive metadata: %s", latest.Body.String())
	}

	digest := postMCP(t, handler, `{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"projects.digest","arguments":{"id":"example-service"}}}`)
	if !bytes.Contains(digest.Body.Bytes(), []byte(`"code":-32004`)) || !bytes.Contains(digest.Body.Bytes(), []byte(`project digest unsupported`)) || bytes.Contains(digest.Body.Bytes(), []byte(`invalid tool arguments`)) {
		t.Fatalf("expected content_graph digest unsupported error, got %s", digest.Body.String())
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

	fileGet := postMCP(t, handler, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"projects.files.get","arguments":{"id":"example-service","file_id":"`+fileID+`"}}}`)
	if bytes.Contains(fileGet.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected file get success, got %s", fileGet.Body.String())
	}
	if !bytes.Contains(fileGet.Body.Bytes(), []byte(`"extension":".go"`)) {
		t.Fatalf("expected file get extension metadata, got %s", fileGet.Body.String())
	}

	chunks := postMCP(t, handler, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"projects.file.chunks","arguments":{"id":"example-service","file_id":"`+fileID+`","max_chunk_bytes":10}}}`)
	if bytes.Contains(chunks.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected chunks success, got %s", chunks.Body.String())
	}
	if bytes.Contains(chunks.Body.Bytes(), []byte(root)) || bytes.Contains(chunks.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("chunk response leaked forbidden metadata: %s", chunks.Body.String())
	}

	symbols := postMCP(t, handler, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"projects.symbols.list","arguments":{"id":"example-service","kind":"function","page_size":10}}}`)
	if bytes.Contains(symbols.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected symbols success, got %s", symbols.Body.String())
	}
	var symbolsRPC rpcResponse
	if err := json.Unmarshal(symbols.Body.Bytes(), &symbolsRPC); err != nil {
		t.Fatalf("decode symbols response: %v", err)
	}
	symbolItems := symbolsRPC.Result.StructuredContent["symbols"].([]any)
	runID := symbolIDByName(t, symbolItems, "Run")
	helperID := symbolIDByName(t, symbolItems, "helper")

	source := postMCP(t, handler, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"projects.symbol.source","arguments":{"id":"example-service","symbol_id":"`+runID+`","max_source_bytes":12}}}`)
	if bytes.Contains(source.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected symbol source success, got %s", source.Body.String())
	}
	if !bytes.Contains(source.Body.Bytes(), []byte("func Run")) || !bytes.Contains(source.Body.Bytes(), []byte(`"text_truncated":true`)) || bytes.Contains(source.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("unexpected source response: %s", source.Body.String())
	}

	refs := postMCP(t, handler, `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"projects.symbol.references","arguments":{"id":"example-service","symbol_id":"`+helperID+`"}}}`)
	if bytes.Contains(refs.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(refs.Body.Bytes(), []byte(`"resolution_status":"resolved"`)) {
		t.Fatalf("expected resolved references success, got %s", refs.Body.String())
	}
	callees := postMCP(t, handler, `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"projects.symbol.callees","arguments":{"id":"example-service","symbol_id":"`+runID+`"}}}`)
	if bytes.Contains(callees.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(callees.Body.Bytes(), []byte(helperID)) {
		t.Fatalf("expected helper callee success, got %s", callees.Body.String())
	}
	graph := postMCP(t, handler, `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"projects.symbol.call_graph","arguments":{"id":"example-service","symbol_id":"`+runID+`","direction":"callees","max_depth":1,"max_nodes":10}}}`)
	if bytes.Contains(graph.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(graph.Body.Bytes(), []byte(helperID)) {
		t.Fatalf("expected call graph success, got %s", graph.Body.String())
	}

	searchText := postMCP(t, handler, `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"projects_search_text","arguments":{"id":"example-service","query":"helper","max_snippet_bytes":12}}}`)
	if bytes.Contains(searchText.Body.Bytes(), []byte(`"error"`)) || bytes.Contains(searchText.Body.Bytes(), []byte(root)) || bytes.Contains(searchText.Body.Bytes(), []byte("content_sha256")) {
		t.Fatalf("expected safe text search success, got %s", searchText.Body.String())
	}
	searchFiles := postMCP(t, handler, `{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"projects_search_files","arguments":{"id":"example-service","path_contains":"main"}}}`)
	if bytes.Contains(searchFiles.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchFiles.Body.Bytes(), []byte(`"relative_path":"cmd/main.go"`)) {
		t.Fatalf("expected file search success, got %s", searchFiles.Body.String())
	}
	searchSymbols := postMCP(t, handler, `{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"projects_search_symbols","arguments":{"id":"example-service","name_contains":"Run"}}}`)
	if bytes.Contains(searchSymbols.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchSymbols.Body.Bytes(), []byte(`"name":"Run"`)) {
		t.Fatalf("expected symbol search success, got %s", searchSymbols.Body.String())
	}
	searchRefs := postMCP(t, handler, `{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"projects_search_references","arguments":{"id":"example-service","target_name_contains":"helper"}}}`)
	if bytes.Contains(searchRefs.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchRefs.Body.Bytes(), []byte(`"target_name":"helper"`)) {
		t.Fatalf("expected reference search success, got %s", searchRefs.Body.String())
	}
	searchCalls := postMCP(t, handler, `{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"projects_search_calls","arguments":{"id":"example-service","caller_name_contains":"Run","callee_name_contains":"helper"}}}`)
	if bytes.Contains(searchCalls.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(searchCalls.Body.Bytes(), []byte(`"callee_name":"helper"`)) {
		t.Fatalf("expected call search success, got %s", searchCalls.Body.String())
	}
	astQueries := postMCP(t, handler, `{"jsonrpc":"2.0","id":171,"method":"tools/call","params":{"name":"projects_search_ast_queries","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(astQueries.Body.Bytes(), []byte(`"error":{`)) || bytes.Contains(astQueries.Body.Bytes(), []byte(`"isError":true`)) || !bytes.Contains(astQueries.Body.Bytes(), []byte(`"id":"function_declarations"`)) || strings.Contains(astQueries.Body.String(), "(function_declaration") {
		t.Fatalf("expected AST query catalog success, got %s", astQueries.Body.String())
	}

	repair := postMCP(t, handler, `{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"projects_search_index_rebuild","arguments":{"id":"example-service"}}}`)
	if bytes.Contains(repair.Body.Bytes(), []byte(`"error"`)) || !bytes.Contains(repair.Body.Bytes(), []byte(`"status":"pending"`)) {
		t.Fatalf("expected search index repair success, got %s", repair.Body.String())
	}
	if bytes.Contains(repair.Body.Bytes(), []byte(root)) || bytes.Contains(repair.Body.Bytes(), []byte("content_sha256")) || bytes.Contains(repair.Body.Bytes(), []byte("project_search")) {
		t.Fatalf("repair response leaked forbidden metadata: %s", repair.Body.String())
	}

	read := postMCP(t, handler, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"mivialabs://projects/example-service/files/`+fileID+`"}}`)
	if read.Code != http.StatusOK || bytes.Contains(read.Body.Bytes(), []byte(`"error"`)) {
		t.Fatalf("expected file resource success, got %s", read.Body.String())
	}
}

type rpcResponse struct {
	Result struct {
		StructuredContent map[string]any `json:"structuredContent"`
	} `json:"result"`
}

func symbolIDByName(t *testing.T, symbols []any, name string) string {
	t.Helper()
	for _, item := range symbols {
		symbol := item.(map[string]any)
		if symbol["name"] == name {
			return symbol["id"].(string)
		}
	}
	t.Fatalf("missing symbol %q in %#v", name, symbols)
	return ""
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
	if err := os.WriteFile(filepath.Join(root, "cmd", "main.go"), []byte("package main\n\nfunc helper() {}\n\nfunc Run() { helper() }\n"), 0o600); err != nil {
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
	scheduler := projectingestion.NewScheduler(ingestion, projectingestion.SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1})
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() { _ = scheduler.Stop(context.Background()) })
	return mcpapi.NewHandlerWithResearchProjectsAndIngestion(svc, nil, registry, digest, scheduler, slog.Default()), root
}

func waitMCPIngestionRun(t *testing.T, handler http.Handler, runID string) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		status := postMCP(t, handler, `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"projects.ingestion_status","arguments":{"id":"example-service","run_id":"`+runID+`"}}}`)
		if bytes.Contains(status.Body.Bytes(), []byte(`"error"`)) {
			t.Fatalf("expected status success, got %s", status.Body.String())
		}
		var statusRPC rpcResponse
		if err := json.Unmarshal(status.Body.Bytes(), &statusRPC); err != nil {
			t.Fatalf("decode status response: %v", err)
		}
		statusValue := statusRPC.Result.StructuredContent["status"].(string)
		if statusValue == string(projectingestion.RunStatusCompleted) || statusValue == string(projectingestion.RunStatusFailed) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for ingestion run %s", runID)
		case <-ticker.C:
		}
	}
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
