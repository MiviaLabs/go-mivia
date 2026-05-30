package mcpapi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/mcpapi"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectworkspace"
)

func TestCallTool_ListProjectsRedactsRootPath(t *testing.T) {
	registry, digest := newServices(t)

	result, err := mcpapi.CallTool(context.Background(), registry, digest, "projects.list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("call list tool: %v", err)
	}
	body := marshalResult(t, result)
	for _, forbidden := range []string{"root_path", "canonical", "package main"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("projects.list leaked %q: %s", forbidden, body)
		}
	}
}

func TestCallTool_DigestAndReadDigestRunResource(t *testing.T) {
	registry, digest := newServices(t)

	result, err := mcpapi.CallTool(context.Background(), registry, digest, "projects.digest", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("call digest tool: %v", err)
	}
	aliasResult, err := mcpapi.CallTool(context.Background(), registry, digest, "projects_digest", json.RawMessage(`{"project_id":"example-service"}`))
	if err != nil {
		t.Fatalf("call digest tool with project_id alias: %v", err)
	}
	if aliasResult["structuredContent"].(projectregistry.DigestRunMetadata).ProjectID != "example-service" {
		t.Fatalf("unexpected digest alias result: %#v", aliasResult["structuredContent"])
	}
	body := marshalResult(t, result)
	for _, forbidden := range []string{"package main", "content_sha256", "root_path"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("projects.digest leaked %q: %s", forbidden, body)
		}
	}
	runID := result["structuredContent"].(projectregistry.DigestRunMetadata).ID

	resource, err := mcpapi.ReadResource(context.Background(), registry, digest, "mivialabs://projects/example-service/digest-runs/"+runID)
	if err != nil {
		t.Fatalf("read digest resource: %v", err)
	}
	resourceBody := marshalResult(t, resource)
	if !strings.Contains(resourceBody, runID) {
		t.Fatalf("expected digest run id in resource: %s", resourceBody)
	}
	if strings.Contains(resourceBody, "package main") || strings.Contains(resourceBody, "content_sha256") {
		t.Fatalf("digest resource leaked content markers: %s", resourceBody)
	}
}

func TestReadResource_Project(t *testing.T) {
	registry, digest := newServices(t)

	resource, err := mcpapi.ReadResource(context.Background(), registry, digest, "mivialabs://projects/example-service")
	if err != nil {
		t.Fatalf("read project resource: %v", err)
	}
	body := marshalResult(t, resource)
	if !strings.Contains(body, "example-service") {
		t.Fatalf("expected project id in resource: %s", body)
	}
	if strings.Contains(body, "root_path") {
		t.Fatalf("project resource leaked root path: %s", body)
	}
}

func TestCallToolWithIngestion_ListFilesFiltersByExtension(t *testing.T) {
	registry, digest, ingestion := newIngestionServices(t)

	if _, err := ingestion.IngestProject(context.Background(), "example-service", projectingestion.TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	result, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.files.list", json.RawMessage(`{"id":"example-service","status":"eligible","extension":"go","page_size":1}`))
	if err != nil {
		t.Fatalf("call files list tool: %v", err)
	}
	fileList := result["structuredContent"].(projectingestion.FileList)
	if len(fileList.Files) != 1 || fileList.Files[0].RelativePath != "cmd/main.go" || fileList.NextPageToken != "" {
		t.Fatalf("unexpected extension filtered file list: %#v", fileList)
	}
	if fileList.Files[0].Extension != ".go" {
		t.Fatalf("expected extension metadata, got %#v", fileList.Files[0])
	}

	getResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.files.get", marshalArgs(t, map[string]string{
		"id":      "example-service",
		"file_id": fileList.Files[0].ID,
	}))
	if err != nil {
		t.Fatalf("call files get tool: %v", err)
	}
	file := getResult["structuredContent"].(projectingestion.FileMetadata)
	if file.ID != fileList.Files[0].ID || file.RelativePath != "cmd/main.go" || file.Extension != ".go" {
		t.Fatalf("unexpected file get result: %#v", file)
	}

	for _, args := range []string{
		`{"id":"example-service","extension":"bad path"}`,
		`{"id":"example-service","extension":"bad/path"}`,
		`{"id":"example-service","extension":"go.md"}`,
		`{"id":"example-service","extension":"g*"}`,
	} {
		_, err = mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.files.list", json.RawMessage(args))
		if err == nil {
			t.Fatalf("expected invalid extension error for %s", args)
		}
	}
}

func TestCallToolWithIngestion_SubmitsAsyncWithoutWaitingForScan(t *testing.T) {
	registry, digest := newServices(t)
	runner := newBlockingAsyncRunner()
	scheduler := projectingestion.NewScheduler(runner, projectingestion.SchedulerOptions{QueueDepth: 4, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := scheduler.Start(ctx); err != nil {
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() {
		runner.releaseExecution()
		_ = scheduler.Stop(context.Background())
	})

	callCtx, callCancel := context.WithTimeout(context.Background(), testShortTimeout)
	defer callCancel()
	result, err := mcpapi.CallToolWithIngestion(callCtx, registry, digest, scheduler, "projects.ingest", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("call ingest tool: %v", err)
	}
	run := result["structuredContent"].(projectingestion.RunMetadata)
	if run.ID != "run-queued" || run.Status != string(projectingestion.RunStatusPending) {
		t.Fatalf("expected queued run metadata, got %#v", run)
	}
	select {
	case <-runner.executeStarted:
	case <-time.After(testShortTimeout):
		t.Fatalf("expected scheduler worker to receive submitted scan")
	}
}

func TestCallToolWithIngestion_LatestStatusToolIsSafe(t *testing.T) {
	registry, digest, ingestion := newIngestionServices(t)
	run, err := ingestion.IngestProject(context.Background(), "example-service", projectingestion.TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}

	result, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.ingestion_status_latest", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("call latest ingestion status: %v", err)
	}
	latest := result["structuredContent"].(projectingestion.RunMetadata)
	if latest.ID != run.ID || latest.Status != string(projectingestion.RunStatusCompleted) {
		t.Fatalf("unexpected latest metadata: %#v", latest)
	}
	body := marshalResult(t, result)
	for _, forbidden := range []string{"cmd/main.go", "package main", "content_sha256", "root_path"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("latest status leaked %q: %s", forbidden, body)
		}
	}
}

func TestCallToolWithWorkspace_ReadAndEditAlias(t *testing.T) {
	root := t.TempDir()
	fullPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(fullPath, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
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
		WorkspaceMode:         projectregistry.WorkspaceModeEdit,
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
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	digest := projectregistry.NewDigestService(registry, graph)
	workspace := projectworkspace.NewService(registry, nil, projectworkspace.Options{Enabled: true})

	readResult, err := mcpapi.CallToolWithWorkspace(context.Background(), registry, digest, nil, workspace, "projects.workspace.file_read", json.RawMessage(`{"id":"example-service","relative_path":"main.go"}`))
	if err != nil {
		t.Fatalf("workspace read: %v", err)
	}
	file := readResult["structuredContent"].(projectworkspace.WorkspaceFile)
	if file.EditToken == "" || strings.Contains(marshalResult(t, readResult), "content_sha256") || strings.Contains(marshalResult(t, readResult), root) {
		t.Fatalf("unsafe workspace read result: %s", marshalResult(t, readResult))
	}

	editResult, err := mcpapi.CallToolWithWorkspace(context.Background(), registry, digest, nil, workspace, "projects_workspace_file_edit", marshalArgs(t, map[string]any{
		"id":            "example-service",
		"relative_path": "main.go",
		"edit_token":    file.EditToken,
		"dry_run":       true,
		"edits": []map[string]any{{
			"start_byte": 0,
			"end_byte":   7,
			"old_text":   "package",
			"new_text":   "module",
		}},
	}))
	if err != nil {
		t.Fatalf("workspace edit alias: %v", err)
	}
	result := editResult["structuredContent"].(projectworkspace.EditResult)
	if result.Applied || result.NewEditToken == "" {
		t.Fatalf("unexpected dry-run edit result: %#v", result)
	}
}

func TestCallToolWithIngestion_P2DiscoveryTools(t *testing.T) {
	registry, digest, ingestion := newIngestionServices(t)

	if _, err := ingestion.IngestProject(context.Background(), "example-service", projectingestion.TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	filesResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.files.list", json.RawMessage(`{"id":"example-service","path_prefix":"docs/","present":true,"page_size":10}`))
	if err != nil {
		t.Fatalf("call files list tool: %v", err)
	}
	files := filesResult["structuredContent"].(projectingestion.FileList)
	if len(files.Files) != 1 || files.Files[0].RelativePath != "docs/guide.md" {
		t.Fatalf("unexpected path-prefix filtered files: %#v", files)
	}

	symbolsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbols.list", json.RawMessage(`{"id":"example-service","kind":"function","name_prefix":"load","extension":"ts","page_size":10}`))
	if err != nil {
		t.Fatalf("call symbols list tool: %v", err)
	}
	symbols := symbolsResult["structuredContent"].(projectingestion.SymbolList)
	if len(symbols.Symbols) != 1 || symbols.Symbols[0].Name != "load" {
		t.Fatalf("unexpected filtered symbols: %#v", symbols)
	}
	pySymbolsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbols.list", json.RawMessage(`{"id":"example-service","kind":"class","name_prefix":"Worker","extension":"py","page_size":10}`))
	if err != nil {
		t.Fatalf("call python symbols list tool: %v", err)
	}
	pySymbols := pySymbolsResult["structuredContent"].(projectingestion.SymbolList)
	if len(pySymbols.Symbols) != 1 || pySymbols.Symbols[0].Name != "Worker" {
		t.Fatalf("unexpected python symbols: %#v", pySymbols)
	}

	headingsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.headings.list", marshalArgs(t, map[string]any{
		"id":      "example-service",
		"file_id": files.Files[0].ID,
	}))
	if err != nil {
		t.Fatalf("call headings list tool: %v", err)
	}
	headings := headingsResult["structuredContent"].(projectingestion.HeadingList)
	if len(headings.Headings) != 2 || headings.Headings[0].Text != "Guide" {
		t.Fatalf("unexpected headings: %#v", headings)
	}

	outlineResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.file.outline", marshalArgs(t, map[string]string{
		"id":      "example-service",
		"file_id": files.Files[0].ID,
	}))
	if err != nil {
		t.Fatalf("call outline tool: %v", err)
	}
	body := marshalResult(t, outlineResult)
	if strings.Contains(body, "## Setup") {
		t.Fatalf("outline leaked chunk text: %s", body)
	}
	outline := outlineResult["structuredContent"].(projectingestion.FileOutline)
	if outline.File.ID != files.Files[0].ID || len(outline.Headings) != 2 || len(outline.Chunks) == 0 {
		t.Fatalf("unexpected outline: %#v", outline)
	}

	goFilesResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.files.list", json.RawMessage(`{"id":"example-service","path_prefix":"cmd/","present":true,"page_size":10}`))
	if err != nil {
		t.Fatalf("call go files list tool: %v", err)
	}
	goFiles := goFilesResult["structuredContent"].(projectingestion.FileList)
	if len(goFiles.Files) != 1 {
		t.Fatalf("unexpected go files: %#v", goFiles)
	}
	filteredOutlineResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.file.outline", marshalArgs(t, map[string]any{
		"id":               "example-service",
		"file_id":          goFiles.Files[0].ID,
		"kind":             "function",
		"name_prefix":      "main",
		"symbol_page_size": 1,
	}))
	if err != nil {
		t.Fatalf("call filtered outline tool: %v", err)
	}
	filteredOutline := filteredOutlineResult["structuredContent"].(projectingestion.FileOutline)
	if len(filteredOutline.Symbols) != 1 || filteredOutline.Symbols[0].Name != "main" {
		t.Fatalf("unexpected filtered outline symbols: %#v", filteredOutline)
	}
	filteredBody := marshalResult(t, filteredOutlineResult)
	if strings.Contains(filteredBody, "func main") || strings.Contains(filteredBody, "package main") {
		t.Fatalf("filtered outline included source without opt-in: %s", filteredBody)
	}
	textOutlineResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.file.outline", marshalArgs(t, map[string]any{
		"id":                 "example-service",
		"file_id":            goFiles.Files[0].ID,
		"include_chunk_text": true,
		"max_chunk_bytes":    24,
	}))
	if err != nil {
		t.Fatalf("call text outline tool: %v", err)
	}
	textBody := marshalResult(t, textOutlineResult)
	if !strings.Contains(textBody, "package main") {
		t.Fatalf("expected opt-in outline source text: %s", textBody)
	}

	allGoSymbolsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbols.list", json.RawMessage(`{"id":"example-service","kind":"function","extension":"go","page_size":10}`))
	if err != nil {
		t.Fatalf("call go symbols list tool: %v", err)
	}
	allGoSymbols := allGoSymbolsResult["structuredContent"].(projectingestion.SymbolList)
	mainSymbol := findSymbol(t, allGoSymbols.Symbols, "main")
	helperSymbol := findSymbol(t, allGoSymbols.Symbols, "helper")
	sourceResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbol.source", marshalArgs(t, map[string]any{
		"id":               "example-service",
		"symbol_id":        mainSymbol.ID,
		"max_source_bytes": 12,
	}))
	if err != nil {
		t.Fatalf("call symbol source tool: %v", err)
	}
	sourceBody := marshalResult(t, sourceResult)
	if !strings.Contains(sourceBody, "func main") || !strings.Contains(sourceBody, `"text_truncated":true`) || strings.Contains(sourceBody, "content_sha256") {
		t.Fatalf("unexpected source response: %s", sourceBody)
	}
	refsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbol.references", marshalArgs(t, map[string]any{
		"id":        "example-service",
		"symbol_id": helperSymbol.ID,
	}))
	if err != nil {
		t.Fatalf("call symbol references tool: %v", err)
	}
	if !strings.Contains(marshalResult(t, refsResult), `"resolution_status":"resolved"`) {
		t.Fatalf("expected resolved references, got %s", marshalResult(t, refsResult))
	}
	calleesResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbol.callees", marshalArgs(t, map[string]any{
		"id":        "example-service",
		"symbol_id": mainSymbol.ID,
	}))
	if err != nil {
		t.Fatalf("call symbol callees tool: %v", err)
	}
	if !strings.Contains(marshalResult(t, calleesResult), helperSymbol.ID) {
		t.Fatalf("expected helper callee, got %s", marshalResult(t, calleesResult))
	}
	graphResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.symbol.call_graph", marshalArgs(t, map[string]any{
		"id":        "example-service",
		"symbol_id": mainSymbol.ID,
		"direction": "callees",
		"max_depth": 1,
		"max_nodes": 10,
	}))
	if err != nil {
		t.Fatalf("call symbol call graph tool: %v", err)
	}
	if !strings.Contains(marshalResult(t, graphResult), helperSymbol.ID) {
		t.Fatalf("expected helper in call graph, got %s", marshalResult(t, graphResult))
	}
}

func TestCallToolWithIngestion_SearchToolsSupportDottedAndUnderscoreNames(t *testing.T) {
	registry, digest, ingestion := newIngestionServices(t)
	if _, err := ingestion.IngestProject(context.Background(), "example-service", projectingestion.TriggerManual); err != nil {
		t.Fatalf("ingest project: %v", err)
	}

	textResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects.search.text", json.RawMessage(`{"id":"example-service","query":"helper","page_size":1,"max_snippet_bytes":12}`))
	if err != nil {
		t.Fatalf("call text search tool: %v", err)
	}
	text := textResult["structuredContent"].(projectingestion.TextSearchResultList)
	if len(text.Results) != 1 || len(text.Results[0].Snippet) > 12 || text.Results[0].Chunk.Text != "" {
		t.Fatalf("unexpected text search result: %#v", text)
	}

	filesResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects_search_files", json.RawMessage(`{"id":"example-service","path_contains":"main"}`))
	if err != nil {
		t.Fatalf("call files search tool: %v", err)
	}
	files := filesResult["structuredContent"].(projectingestion.FileList)
	if len(files.Files) != 1 || files.Files[0].RelativePath != "cmd/main.go" {
		t.Fatalf("unexpected file search result: %#v", files)
	}

	symbolsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects_search_symbols", json.RawMessage(`{"id":"example-service","name_contains":"elp","page_size":10}`))
	if err != nil {
		t.Fatalf("call symbols search tool: %v", err)
	}
	symbols := symbolsResult["structuredContent"].(projectingestion.SymbolList)
	if len(symbols.Symbols) != 1 || symbols.Symbols[0].Name != "helper" {
		t.Fatalf("unexpected symbol search result: %#v", symbols)
	}

	refsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects_search_references", json.RawMessage(`{"id":"example-service","target_name_contains":"helper","page_size":10}`))
	if err != nil {
		t.Fatalf("call references search tool: %v", err)
	}
	refs := refsResult["structuredContent"].(projectingestion.SymbolReferenceList)
	if len(refs.References) != 1 || refs.References[0].TargetName != "helper" {
		t.Fatalf("unexpected reference search result: %#v", refs)
	}

	callsResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects_search_calls", json.RawMessage(`{"id":"example-service","caller_name_contains":"main","callee_name_contains":"helper","page_size":10}`))
	if err != nil {
		t.Fatalf("call calls search tool: %v", err)
	}
	calls := callsResult["structuredContent"].(projectingestion.SymbolCallEdgeList)
	if len(calls.Edges) != 1 || calls.Edges[0].CallerName != "main" || calls.Edges[0].CalleeName != "helper" {
		t.Fatalf("unexpected call search result: %#v", calls)
	}

	astQueriesResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects_search_ast_queries", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("call ast query catalog tool: %v", err)
	}
	astQueries := astQueriesResult["structuredContent"].(projectingestion.ASTQueryCatalog)
	if len(astQueries.Queries) == 0 || len(astQueries.Coverage) == 0 {
		t.Fatalf("unexpected ast query catalog result: %#v", astQueries)
	}
	astQueriesBody := marshalResult(t, astQueriesResult)
	if !strings.Contains(astQueriesBody, `"id":"function_declarations"`) || strings.Contains(astQueriesBody, "(function_declaration") || strings.Contains(astQueriesBody, "content_sha256") {
		t.Fatalf("unexpected ast query catalog body: %s", astQueriesBody)
	}

	astResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, ingestion, "projects_search_ast", json.RawMessage(`{"id":"example-service","language":"go","query":"call_expressions","captures":["callee"],"page_size":10,"max_snippet_bytes":16}`))
	if err != nil {
		t.Fatalf("call ast search tool: %v", err)
	}
	ast := astResult["structuredContent"].(projectingestion.ASTSearchResultList)
	if len(ast.Results) == 0 || ast.Results[0].CaptureName != "callee" || ast.Results[0].CaptureText != "helper" || ast.Results[0].Chunk.Text != "" {
		t.Fatalf("unexpected ast search result: %#v", ast)
	}
	if ast.Coverage == nil || ast.Coverage.Language != "go" || ast.Coverage.EligibleFiles == 0 {
		t.Fatalf("missing ast search coverage: %#v", ast.Coverage)
	}

	body := marshalResult(t, map[string]any{"text": textResult, "files": filesResult, "symbols": symbolsResult, "refs": refsResult, "calls": callsResult, "ast_queries": astQueriesResult, "ast": astResult})
	for _, forbidden := range []string{"root_path", "content_sha256", "access_token", "provider_payload", "raw_prompt"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("search tool response leaked %q: %s", forbidden, body)
		}
	}

	scheduler := projectingestion.NewScheduler(ingestion, projectingestion.SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 1, PerProjectWorkerLimit: 1})
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = scheduler.Stop(context.Background())
	})
	repairResult, err := mcpapi.CallToolWithIngestion(context.Background(), registry, digest, scheduler, "projects.search_index.rebuild", json.RawMessage(`{"id":"example-service"}`))
	if err != nil {
		t.Fatalf("call search index rebuild tool: %v", err)
	}
	repairRun := repairResult["structuredContent"].(projectingestion.RunMetadata)
	if repairRun.ID == "" || repairRun.Status != string(projectingestion.RunStatusPending) {
		t.Fatalf("expected queued repair run metadata, got %#v", repairRun)
	}
	repairBody := marshalResult(t, repairResult)
	for _, forbidden := range []string{"root_path", "content_sha256", "access_token", "provider_payload", "raw_prompt", "project_search"} {
		if strings.Contains(repairBody, forbidden) {
			t.Fatalf("repair tool response leaked %q: %s", forbidden, repairBody)
		}
	}
}

func newServices(t *testing.T) (*projectregistry.Registry, *projectregistry.DigestService) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:             "example-service",
		DisplayName:    "Example Service",
		Description:    "Synthetic local service",
		RootPath:       root,
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     projectregistry.DigestModeMetadataOnly,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
		Include:        []string{"**/*.go"},
		FollowSymlinks: false,
	}}, projectregistry.Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return registry, projectregistry.NewDigestService(registry, graph)
}

func newIngestionServices(t *testing.T) (*projectregistry.Registry, *projectregistry.DigestService, *projectingestion.Service) {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"cmd/main.go":    "package main\nfunc helper() {}\nfunc main() { helper() }\n",
		"docs/guide.md":  "# Guide\n\n## Setup\n",
		"web/app.ts":     "export class Widget {}\nexport const load = () => true\n",
		"scripts/app.py": "import os\nclass Worker:\n    pass\n",
	} {
		fullPath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatalf("create source dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
			t.Fatalf("write source fixture: %v", err)
		}
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
		Include:               []string{"**/*"},
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
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	return registry, projectregistry.NewDigestService(registry, graph), projectingestion.NewService(registry, projectingestion.NewGraphStore(graph), projectingestion.NewSQLiteStore(db.SQLDB()))
}

func marshalResult(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return string(encoded)
}

func marshalArgs(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return encoded
}

func findSymbol(t *testing.T, symbols []projectingestion.SymbolMetadata, name string) projectingestion.SymbolMetadata {
	t.Helper()
	for _, symbol := range symbols {
		if symbol.Name == name {
			return symbol
		}
	}
	t.Fatalf("missing symbol %q in %#v", name, symbols)
	return projectingestion.SymbolMetadata{}
}

const testShortTimeout = 100 * time.Millisecond

type blockingAsyncRunner struct {
	executeStarted chan struct{}
	release        chan struct{}
}

func newBlockingAsyncRunner() *blockingAsyncRunner {
	return &blockingAsyncRunner{
		executeStarted: make(chan struct{}, 1),
		release:        make(chan struct{}),
	}
}

func (runner *blockingAsyncRunner) PrepareProjectRun(_ context.Context, projectID string, trigger projectingestion.Trigger) (projectingestion.Run, error) {
	return projectingestion.Run{
		ID:        "run-queued",
		ProjectID: projectID,
		Trigger:   trigger,
		Mode:      "content_graph",
		Status:    projectingestion.RunStatusPending,
	}, nil
}

func (runner *blockingAsyncRunner) ExecutePreparedProjectRun(ctx context.Context, run projectingestion.Run) (projectingestion.Run, error) {
	select {
	case runner.executeStarted <- struct{}{}:
	default:
	}
	select {
	case <-runner.release:
		run.Status = projectingestion.RunStatusCompleted
		return run, nil
	case <-ctx.Done():
		return run, ctx.Err()
	}
}

func (runner *blockingAsyncRunner) FailPreparedProjectRun(_ context.Context, run projectingestion.Run, category string) (projectingestion.Run, error) {
	run.Status = projectingestion.RunStatusFailed
	run.ErrorCategory = category
	return run, nil
}

func (runner *blockingAsyncRunner) IngestProject(ctx context.Context, projectID string, trigger projectingestion.Trigger) (projectingestion.Run, error) {
	run, err := runner.PrepareProjectRun(ctx, projectID, trigger)
	if err != nil {
		return projectingestion.Run{}, err
	}
	return runner.ExecutePreparedProjectRun(ctx, run)
}

func (runner *blockingAsyncRunner) IngestPath(context.Context, string, string, projectingestion.Trigger) (projectingestion.Run, error) {
	return projectingestion.Run{}, projectingestion.ErrUnsupportedIngest
}

func (runner *blockingAsyncRunner) releaseExecution() {
	select {
	case <-runner.release:
	default:
		close(runner.release)
	}
}
