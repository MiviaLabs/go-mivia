package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectingestion"
	"github.com/MiviaLabs/go-mivia/internal/projectintegrations"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry/httpapi"
	"github.com/MiviaLabs/go-mivia/internal/projectworkspace"
)

func TestProjectRoutes_ListAndGetRedactRootPath(t *testing.T) {
	mux, projectID := newMux(t)

	listRes := httptest.NewRecorder()
	mux.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listRes.Code, listRes.Body.String())
	}
	assertProjectResponseSafe(t, listRes.Body.String())

	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID, nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getRes.Code, getRes.Body.String())
	}
	assertProjectResponseSafe(t, getRes.Body.String())
}

func TestProjectRoutes_CreateDigestRunMetadataOnly(t *testing.T) {
	mux, projectID := newMux(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/digest-runs", bytes.NewReader(nil))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, forbidden := range []string{"package main", "root_path", "content_sha256"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("digest response leaked %q: %s", forbidden, body)
		}
	}
	var run projectregistry.DigestRunMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode digest run: %v", err)
	}
	if run.Status != projectregistry.DigestStatusCompleted || run.FilesStored != 1 {
		t.Fatalf("unexpected digest run response: %#v", run)
	}
}

func TestProjectRoutes_UnknownProjectReturnsNotFound(t *testing.T) {
	mux, _ := newMux(t)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/projects/missing", nil))

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
}

func TestProjectIngestionRoutes_ControlAndQueriesAreBounded(t *testing.T) {
	mux, projectID, root := newIngestionMux(t, "package main\n\nfunc helper() {}\n\nfunc Run() { helper() }\n")

	created := httptest.NewRecorder()
	mux.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/ingestion-runs", nil))
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}
	assertDoesNotLeak(t, created.Body.String(), root, "root_path", "content_sha256")
	var run projectingestion.RunMetadata
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.ID == "" || run.Status != string(projectingestion.RunStatusPending) {
		t.Fatalf("unexpected ingestion run: %#v", run)
	}
	run = waitIngestionRun(t, mux, projectID, run.ID)
	if run.Status != string(projectingestion.RunStatusCompleted) || run.FilesIngested != 1 {
		t.Fatalf("unexpected completed ingestion run: %#v", run)
	}

	status := httptest.NewRecorder()
	mux.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/ingestion-runs/"+run.ID, nil))
	if status.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", status.Code, status.Body.String())
	}
	latest := httptest.NewRecorder()
	mux.ServeHTTP(latest, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/ingestion-runs/latest", nil))
	if latest.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", latest.Code, latest.Body.String())
	}
	assertDoesNotLeak(t, latest.Body.String(), root, "cmd/main.go", "package main", "content_sha256", "root_path")

	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/context-health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", health.Code, health.Body.String())
	}
	assertDoesNotLeak(t, health.Body.String(), root, "cmd/main.go", "package main", "content_sha256", "root_path")
	if !strings.Contains(health.Body.String(), `"status":"ready"`) || !strings.Contains(health.Body.String(), `"project_id":"`+projectID+`"`) {
		t.Fatalf("expected ready context health, got %s", health.Body.String())
	}

	dashboard := httptest.NewRecorder()
	mux.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/dashboard-summary", nil))
	if dashboard.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", dashboard.Code, dashboard.Body.String())
	}
	assertDoesNotLeak(t, dashboard.Body.String(), root, "package main", "content_sha256", "root_path")
	for _, expected := range []string{`"project"`, `"context_health"`, `"graph"`, `"by_extension"`, `"by_kind"`, `"ast_coverage"`, `"limits"`} {
		if !strings.Contains(dashboard.Body.String(), expected) {
			t.Fatalf("expected dashboard summary to contain %s, got %s", expected, dashboard.Body.String())
		}
	}
	if strings.Contains(dashboard.Body.String(), `"text":`) || strings.Contains(dashboard.Body.String(), `"diff":`) {
		t.Fatalf("dashboard summary must not include source text or diffs: %s", dashboard.Body.String())
	}

	impactReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/impact/analyze", bytes.NewBufferString(`{"changed_paths":["internal/projectregistry/httpapi/httpapi.go","internal/agentcontrol/model/model.go"]}`))
	impactReq.Header.Set("Content-Type", "application/json")
	impact := httptest.NewRecorder()
	mux.ServeHTTP(impact, impactReq)
	if impact.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", impact.Code, impact.Body.String())
	}
	assertDoesNotLeak(t, impact.Body.String(), root, "package main", "content_sha256", "root_path")
	if !strings.Contains(impact.Body.String(), `"rest_project_api"`) || !strings.Contains(impact.Body.String(), `"agent_control"`) {
		t.Fatalf("expected impact domains, got %s", impact.Body.String())
	}

	contextPackReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/context-pack", bytes.NewBufferString(`{"query":"helper","changed_paths":["cmd/main.go"],"max_items":2,"max_snippet_bytes":80}`))
	contextPackReq.Header.Set("Content-Type", "application/json")
	contextPack := httptest.NewRecorder()
	mux.ServeHTTP(contextPack, contextPackReq)
	if contextPack.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", contextPack.Code, contextPack.Body.String())
	}
	assertDoesNotLeak(t, contextPack.Body.String(), root, "content_sha256", "root_path")
	for _, expected := range []string{`"project_id":"` + projectID + `"`, `"text_hits"`, `"files"`, `"symbols"`, `"impact"`} {
		if !strings.Contains(contextPack.Body.String(), expected) {
			t.Fatalf("expected context pack to contain %s, got %s", expected, contextPack.Body.String())
		}
	}

	claimsReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/claims/check", bytes.NewBufferString(`{"documents":[{"path":"README.md","text":"Use projects.context_health and not projects.verifiers.recommend. Do not link .ai/tasks/active/local.md"}]}`))
	claimsReq.Header.Set("Content-Type", "application/json")
	claims := httptest.NewRecorder()
	mux.ServeHTTP(claims, claimsReq)
	if claims.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", claims.Code, claims.Body.String())
	}
	assertDoesNotLeak(t, claims.Body.String(), root, "package main", "content_sha256", "root_path", "local.md")
	if strings.Contains(claims.Body.String(), `"claim":"projects.context_health"`) || strings.Contains(claims.Body.String(), `"status":"verified"`) {
		t.Fatalf("expected default claims response to omit verified claims, got %s", claims.Body.String())
	}
	if !strings.Contains(claims.Body.String(), `"claim":"projects.verifiers.recommend"`) || !strings.Contains(claims.Body.String(), `"status":"stale"`) || !strings.Contains(claims.Body.String(), `"verified_omitted":1`) {
		t.Fatalf("expected actionable claim statuses and omitted verified count, got %s", claims.Body.String())
	}

	files := httptest.NewRecorder()
	mux.ServeHTTP(files, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files?page_size=1", nil))
	if files.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", files.Code, files.Body.String())
	}
	assertDoesNotLeak(t, files.Body.String(), root, "root_path", "content_sha256")
	var fileList projectingestion.FileList
	if err := json.Unmarshal(files.Body.Bytes(), &fileList); err != nil {
		t.Fatalf("decode files: %v", err)
	}
	if len(fileList.Files) != 1 || fileList.Files[0].ID == "" || fileList.Files[0].RelativePath != "cmd/main.go" {
		t.Fatalf("unexpected file list: %#v", fileList)
	}

	chunks := httptest.NewRecorder()
	mux.ServeHTTP(chunks, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files/"+fileList.Files[0].ID+"/chunks?max_chunk_bytes=12", nil))
	if chunks.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", chunks.Code, chunks.Body.String())
	}
	assertDoesNotLeak(t, chunks.Body.String(), root, "root_path", "content_sha256")
	var chunkList projectingestion.ChunkList
	if err := json.Unmarshal(chunks.Body.Bytes(), &chunkList); err != nil {
		t.Fatalf("decode chunks: %v", err)
	}
	if len(chunkList.Chunks) != 1 || len(chunkList.Chunks[0].Text) > 12 || !chunkList.Chunks[0].TextTruncated {
		t.Fatalf("expected bounded truncated chunk text: %#v", chunkList)
	}

	symbols := httptest.NewRecorder()
	mux.ServeHTTP(symbols, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/symbols", nil))
	if symbols.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", symbols.Code, symbols.Body.String())
	}
	if !strings.Contains(symbols.Body.String(), `"name":"Run"`) || strings.Contains(symbols.Body.String(), root) {
		t.Fatalf("unexpected symbols response: %s", symbols.Body.String())
	}
	var symbolList projectingestion.SymbolList
	if err := json.Unmarshal(symbols.Body.Bytes(), &symbolList); err != nil {
		t.Fatalf("decode symbols: %v", err)
	}
	runSymbol := findSymbol(t, symbolList.Symbols, "Run")
	helperSymbol := findSymbol(t, symbolList.Symbols, "helper")

	source := httptest.NewRecorder()
	mux.ServeHTTP(source, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/symbols/"+runSymbol.ID+"/source?max_source_bytes=12", nil))
	if source.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", source.Code, source.Body.String())
	}
	assertDoesNotLeak(t, source.Body.String(), root, "content_sha256")
	if !strings.Contains(source.Body.String(), "func Run") || !strings.Contains(source.Body.String(), `"text_truncated":true`) {
		t.Fatalf("expected bounded source response, got %s", source.Body.String())
	}

	refs := httptest.NewRecorder()
	mux.ServeHTTP(refs, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/symbols/"+helperSymbol.ID+"/references", nil))
	if refs.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", refs.Code, refs.Body.String())
	}
	assertDoesNotLeak(t, refs.Body.String(), root, "content_sha256", "package main")
	if !strings.Contains(refs.Body.String(), `"resolution_status":"resolved"`) {
		t.Fatalf("expected resolved reference response, got %s", refs.Body.String())
	}

	callees := httptest.NewRecorder()
	mux.ServeHTTP(callees, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/symbols/"+runSymbol.ID+"/callees", nil))
	if callees.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", callees.Code, callees.Body.String())
	}
	if !strings.Contains(callees.Body.String(), helperSymbol.ID) {
		t.Fatalf("expected helper callee response, got %s", callees.Body.String())
	}

	callGraph := httptest.NewRecorder()
	mux.ServeHTTP(callGraph, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/symbols/"+runSymbol.ID+"/call-graph?direction=callees&max_depth=1&max_nodes=10", nil))
	if callGraph.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", callGraph.Code, callGraph.Body.String())
	}
	if !strings.Contains(callGraph.Body.String(), helperSymbol.ID) || strings.Contains(callGraph.Body.String(), root) {
		t.Fatalf("unexpected call graph response: %s", callGraph.Body.String())
	}

	outline := httptest.NewRecorder()
	mux.ServeHTTP(outline, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files/"+fileList.Files[0].ID+"/outline?kind=function&name_prefix=Run&symbol_page_size=1", nil))
	if outline.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", outline.Code, outline.Body.String())
	}
	assertDoesNotLeak(t, outline.Body.String(), root, "package main", "println", "content_sha256")
	var fileOutline projectingestion.FileOutline
	if err := json.Unmarshal(outline.Body.Bytes(), &fileOutline); err != nil {
		t.Fatalf("decode outline: %v", err)
	}
	if len(fileOutline.Symbols) != 1 || fileOutline.Symbols[0].Name != "Run" || len(fileOutline.Chunks) != 1 || fileOutline.Chunks[0].Text != "" {
		t.Fatalf("unexpected outline response: %#v", fileOutline)
	}

	textOutline := httptest.NewRecorder()
	mux.ServeHTTP(textOutline, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files/"+fileList.Files[0].ID+"/outline?include_chunk_text=true&max_chunk_bytes=18", nil))
	if textOutline.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", textOutline.Code, textOutline.Body.String())
	}
	assertDoesNotLeak(t, textOutline.Body.String(), root, "content_sha256")
	var textFileOutline projectingestion.FileOutline
	if err := json.Unmarshal(textOutline.Body.Bytes(), &textFileOutline); err != nil {
		t.Fatalf("decode text outline: %v", err)
	}
	if len(textFileOutline.Chunks) != 1 || !strings.Contains(textFileOutline.Chunks[0].Text, "package main") || !textFileOutline.Chunks[0].TextTruncated {
		t.Fatalf("expected bounded outline text: %#v", textFileOutline.Chunks)
	}
}

func TestProjectDashboardSummary_SymbolConcentrationUsesExplicitCodeAreaFallback(t *testing.T) {
	mux, projectID, root := newIngestionMuxWithFiles(t, map[string]string{
		"cmd/main.go":                  "package main\n\nfunc Run() {}\n",
		"src/app.py":                   "def run():\n    return 1\n",
		"web/app.js":                   "function run() { return 1 }\n",
		"web/app.jsx":                  "function App() { return <div /> }\n",
		"web/src/app.ts":               "function run(): number { return 1 }\n",
		"web/app.tsx":                  "function App(): JSX.Element { return <div /> }\n",
		"services/api/App.cs":          "namespace Demo { class App { void Run() {} } }\n",
		"Assets/Scripts/Controller.cs": "namespace Game.Runtime { class Controller { void Tick() {} } }\n",
		"Assets/Editor/Tool.cs":        "namespace Game.Editor { class Tool { void Run() {} } }\n",
		"mobile/app.dart":              "class Home { Widget build(BuildContext context) { return Container(); } }\n",
		"Assets/Scripts/Game.asmdef":   `{"name":"Game.Runtime"}`,
		"config/app.yaml":              "service:\n  enabled: true\n",
		"docker/Dockerfile":            "FROM alpine AS runtime\n",
	}, []string{"**/*.go", "**/*.py", "**/*.js", "**/*.jsx", "**/*.ts", "**/*.tsx", "**/*.cs", "**/*.dart", "**/*.asmdef", "**/*.yaml", "**/Dockerfile"})

	created := httptest.NewRecorder()
	mux.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/ingestion-runs", nil))
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var run projectingestion.RunMetadata
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	waitIngestionRun(t, mux, projectID, run.ID)

	dashboard := httptest.NewRecorder()
	mux.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/dashboard-summary", nil))
	if dashboard.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", dashboard.Code, dashboard.Body.String())
	}
	body := dashboard.Body.String()
	assertDoesNotLeak(t, body, root, "def run", "function run", "namespace Demo", "content_sha256", "root_path")
	if !strings.Contains(body, `"by_package"`) || !strings.Contains(body, `"by_code_area"`) || !strings.Contains(body, `"by_path_bucket"`) {
		t.Fatalf("expected package and code-area symbol concentration, got %s", body)
	}
	if strings.Contains(body, `"primary_field":"by_module"`) && !strings.Contains(body, `"source":"relative_file_module"`) {
		t.Fatalf("by_module must be backed by semantic file-module metadata, got %s", body)
	}
	if !strings.Contains(body, `"by_language"`) {
		t.Fatalf("expected symbol language distribution, got %s", body)
	}
	if strings.Contains(body, "unmapped file") {
		t.Fatalf("dashboard summary should not fall back to unmapped file when symbol file metadata is available: %s", body)
	}

	var summary struct {
		Graph struct {
			Symbols struct {
				TotalCount         int `json:"total_count"`
				ConcentrationBasis struct {
					PrimaryField     string `json:"primary_field"`
					Source           string `json:"source"`
					Denominator      string `json:"denominator"`
					DenominatorCount int    `json:"denominator_count"`
				} `json:"concentration_basis"`
				ByPackage []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_package"`
				ByModule []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_module"`
				ByNamespace []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_namespace"`
				ByAssembly []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_assembly"`
				ByCodeArea []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_code_area"`
				ByPathBucket []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_path_bucket"`
				ByLanguage []struct {
					Key   string `json:"key"`
					Count int    `json:"count"`
				} `json:"by_language"`
			} `json:"symbols"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(dashboard.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode dashboard summary: %v", err)
	}
	if summary.Graph.Symbols.ConcentrationBasis.PrimaryField != "by_namespace" || summary.Graph.Symbols.ConcentrationBasis.Source != "csharp_package_metadata" || summary.Graph.Symbols.ConcentrationBasis.Denominator != "indexed_csharp_namespace_symbols" {
		t.Fatalf("expected highest-coverage semantic namespace concentration basis: %#v", summary.Graph.Symbols.ConcentrationBasis)
	}
	if summary.Graph.Symbols.ConcentrationBasis.DenominatorCount <= 0 {
		t.Fatalf("expected semantic denominator count in mixed fixture: %#v total=%d", summary.Graph.Symbols.ConcentrationBasis, summary.Graph.Symbols.TotalCount)
	}
	hasGoPackage := false
	for _, item := range summary.Graph.Symbols.ByPackage {
		if item.Key == "main" && item.Count > 0 {
			hasGoPackage = true
			break
		}
	}
	if !hasGoPackage {
		t.Fatalf("expected real parsed Go package metadata in by_package: %#v", summary.Graph.Symbols.ByPackage)
	}
	assertDashboardCount(t, summary.Graph.Symbols.ByModule, "web/src/app", "expected TS source file module in by_module")
	assertDashboardCountAtLeast(t, summary.Graph.Symbols.ByNamespace, "Demo", 2, "expected parsed C# namespace metadata in by_namespace")
	assertDashboardCountAtLeast(t, summary.Graph.Symbols.ByAssembly, "Game.Runtime", 2, "expected Unity asmdef to group symbols under its directory")
	assertDashboardCountAtLeast(t, summary.Graph.Symbols.ByAssembly, "Assembly-CSharp-Editor", 1, "expected Unity predefined editor assembly fallback")
	if summary.Graph.Symbols.TotalCount <= 12 {
		t.Fatalf("expected dashboard concentration denominator to cover all indexed symbols, got %d", summary.Graph.Symbols.TotalCount)
	}
	hasPathFallback := false
	for _, item := range summary.Graph.Symbols.ByCodeArea {
		if (item.Key == "src" || item.Key == "web" || item.Key == "services" || item.Key == "mobile" || item.Key == "config" || item.Key == "docker") && item.Count > 0 {
			hasPathFallback = true
			break
		}
	}
	if !hasPathFallback {
		t.Fatalf("expected safe code-area path fallback for symbols without package metadata: %#v", summary.Graph.Symbols.ByCodeArea)
	}
	hasPathBucket := false
	for _, item := range summary.Graph.Symbols.ByPathBucket {
		if (item.Key == "web/src" || item.Key == "services/api") && item.Count > 0 {
			hasPathBucket = true
			break
		}
	}
	if !hasPathBucket {
		t.Fatalf("expected explicit path buckets for non-Go symbols without package metadata: %#v", summary.Graph.Symbols.ByPathBucket)
	}
	for _, item := range summary.Graph.Symbols.ByCodeArea {
		if strings.HasPrefix(item.Key, "file:") {
			t.Fatalf("code-area concentration should not expose opaque file IDs when safe path metadata is available: %#v", summary.Graph.Symbols.ByCodeArea)
		}
	}
	for _, language := range []string{"Go", "Python", "JavaScript", "JSX", "TypeScript", "TSX", "C#", "Dart", "YAML", "Dockerfile"} {
		found := false
		for _, item := range summary.Graph.Symbols.ByLanguage {
			if item.Key == language && item.Count > 0 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected language %q in symbol language distribution: %#v", language, summary.Graph.Symbols.ByLanguage)
		}
	}
}

func TestProjectDashboardSummary_IncludesLocalIntegrationStatusAndCounts(t *testing.T) {
	mux, projectID, root := newIntegrationDashboardMux(t)

	dashboard := httptest.NewRecorder()
	mux.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/dashboard-summary", nil))
	if dashboard.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", dashboard.Code, dashboard.Body.String())
	}
	body := dashboard.Body.String()
	assertDoesNotLeak(t, body, root, "https://tenant.atlassian.net", "ACME", "ENG", "MIVIA_ATLASSIAN", "/home/mac/secret", "raw-provider-cursor", "sha256:")
	for _, expected := range []string{`"integrations"`, `"provider":"jira"`, `"provider":"confluence"`, `"allowlist_kind":"project_keys"`, `"allowlist_kind":"space_keys"`, `"allowlist_count":2`, `"count":3`, `"count":2`, `"active_run"`, `"last_run_status":"completed"`, `"last_run_items_seen":7`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected dashboard integration summary to contain %s, got %s", expected, body)
		}
	}
}

func assertDashboardCount(t *testing.T, items []struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}, key string, message string) {
	t.Helper()
	for _, item := range items {
		if item.Key == key && item.Count > 0 {
			return
		}
	}
	t.Fatalf("%s: %#v", message, items)
}

func assertDashboardCountAtLeast(t *testing.T, items []struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}, key string, minimum int, message string) {
	t.Helper()
	for _, item := range items {
		if item.Key == key && item.Count >= minimum {
			return
		}
	}
	t.Fatalf("%s: %#v", message, items)
}

func TestProjectIngestionRoutes_SubmitsAsyncWithoutWaitingForScan(t *testing.T) {
	registry, digest := newRegistryDigest(t)
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

	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithIngestion(mux, registry, digest, scheduler)
	res := httptest.NewRecorder()
	req, reqCancel := context.WithTimeout(context.Background(), testShortTimeout)
	defer reqCancel()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/ingestion-runs", nil).WithContext(req))

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	var run projectingestion.RunMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.ID != "run-queued" || run.Status != string(projectingestion.RunStatusPending) {
		t.Fatalf("expected queued run metadata, got %#v", run)
	}
	select {
	case <-runner.executeStarted:
	case <-time.After(testShortTimeout):
		t.Fatalf("expected scheduler worker to receive submitted scan")
	}
	for _, path := range []string{
		"/api/v1/projects/example-service/ingestion-runs/latest",
		"/api/v1/projects/example-service/context-health",
		"/api/v1/projects/example-service/dashboard-summary",
	} {
		active := httptest.NewRecorder()
		mux.ServeHTTP(active, httptest.NewRequest(http.MethodGet, path, nil))
		if active.Code == http.StatusServiceUnavailable {
			t.Fatalf("expected active ingestion endpoint %s not to return 503: %s", path, active.Body.String())
		}
	}
}

func TestProjectWorkspaceRoutes_ReadAndEdit(t *testing.T) {
	registry, digest := newRegistryDigest(t)
	workspace := &fakeWorkspaceAPI{
		file: projectworkspace.WorkspaceFile{
			ProjectID:    "example-service",
			RelativePath: "main.go",
			Text:         "package main\n",
			EditToken:    "opaque-token",
		},
		edit:   projectworkspace.EditResult{Applied: true, IngestionRunID: "ingest-path-1"},
		create: projectworkspace.CreateFileResult{Applied: true, IngestionRunID: "create-path-1"},
		delete: projectworkspace.DeleteFileResult{Deleted: true, ProjectID: "example-service", RelativePath: "main.go", IngestionRunID: "delete-path-1"},
	}
	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithWorkspace(mux, registry, digest, nil, workspace)

	read := httptest.NewRecorder()
	mux.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/workspace/files/read?relative_path=main.go", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), "opaque-token") {
		t.Fatalf("unexpected workspace read response %d: %s", read.Code, read.Body.String())
	}
	assertDoesNotLeak(t, read.Body.String(), "content_sha256", "root_path")

	editBody := `{"relative_path":"main.go","edit_token":"opaque-token","edits":[{"start_byte":0,"end_byte":7,"old_text":"package","new_text":"module"}]}`
	edit := httptest.NewRecorder()
	mux.ServeHTTP(edit, httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/workspace/files/edit", strings.NewReader(editBody)))
	if edit.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected content-type guard, got %d: %s", edit.Code, edit.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/workspace/files/edit", strings.NewReader(editBody))
	req.Header.Set("Content-Type", "application/json")
	edit = httptest.NewRecorder()
	mux.ServeHTTP(edit, req)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "ingest-path-1") {
		t.Fatalf("unexpected workspace edit response %d: %s", edit.Code, edit.Body.String())
	}

	createBody := `{"relative_path":"new.go","text":"package main\n","create_parent_dirs":true,"dry_run":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/workspace/files/create", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	create := httptest.NewRecorder()
	mux.ServeHTTP(create, req)
	if create.Code != http.StatusOK || !strings.Contains(create.Body.String(), "create-path-1") {
		t.Fatalf("unexpected workspace create response %d: %s", create.Code, create.Body.String())
	}

	deleteBody := `{"relative_path":"main.go","edit_token":"opaque-token","dry_run":true}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects/example-service/workspace/files/delete", strings.NewReader(deleteBody))
	req.Header.Set("Content-Type", "application/json")
	deleteRes := httptest.NewRecorder()
	mux.ServeHTTP(deleteRes, req)
	if deleteRes.Code != http.StatusOK || !strings.Contains(deleteRes.Body.String(), "delete-path-1") {
		t.Fatalf("unexpected workspace delete response %d: %s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestProjectWorkspaceRoutes_GitUnavailableIsExplicit(t *testing.T) {
	registry, digest := newRegistryDigest(t)
	workspace := &fakeWorkspaceAPI{err: projectworkspace.ErrGitUnavailable}
	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithWorkspace(mux, registry, digest, nil, workspace)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/workspace/git/status", nil))

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "git_unavailable") || !strings.Contains(res.Body.String(), "git is not available") {
		t.Fatalf("expected explicit git unavailable error, got %s", res.Body.String())
	}
}

func TestProjectIntegrationRoutesExposeLocalDataOnly(t *testing.T) {
	ctx := context.Background()
	registry, digest, project := newIntegrationRegistryDigest(t)
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(ctx, db.SQLDB()); err != nil {
		t.Fatalf("bootstrap sqlite: %v", err)
	}
	store := projectintegrations.NewSQLiteStore(db.SQLDB())
	if _, err := store.UpsertItem(ctx, projectintegrations.ItemMetadataInput{ProjectID: "example-service", Provider: projectintegrations.ProviderJira, ItemID: "10001", ItemKey: "LOCAL-1", ItemType: "issue", ItemStatus: "open", ItemUpdatedAt: testIntegrationTime().Add(-time.Hour), FirstSeenAt: testIntegrationTime(), LastSeenAt: testIntegrationTime()}); err != nil {
		t.Fatalf("upsert old jira item: %v", err)
	}
	if _, err := store.UpsertItem(ctx, projectintegrations.ItemMetadataInput{ProjectID: "example-service", Provider: projectintegrations.ProviderJira, ItemID: "10002", ItemKey: "LOCAL-2", ItemType: "issue", ItemStatus: "done", ItemUpdatedAt: testIntegrationTime(), FirstSeenAt: testIntegrationTime(), LastSeenAt: testIntegrationTime()}); err != nil {
		t.Fatalf("upsert recent jira item: %v", err)
	}
	if _, err := store.UpsertItem(ctx, projectintegrations.ItemMetadataInput{ProjectID: "example-service", Provider: projectintegrations.ProviderConfluence, ItemID: "20001", ItemType: "page", ItemStatus: "current", ItemUpdatedAt: testIntegrationTime(), FirstSeenAt: testIntegrationTime(), LastSeenAt: testIntegrationTime()}); err != nil {
		t.Fatalf("upsert confluence page: %v", err)
	}
	service, err := projectintegrations.NewServiceWithOptions([]config.Project{project}, store, projectintegrations.ServiceOptions{RichContent: fakeIntegrationRichContent{}})
	if err != nil {
		t.Fatalf("new integration service: %v", err)
	}
	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithWorkspaceAndIntegrations(mux, registry, digest, nil, nil, service)

	counts := httptest.NewRecorder()
	mux.ServeHTTP(counts, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/integrations/counts", nil))
	if counts.Code != http.StatusOK || !strings.Contains(counts.Body.String(), `"project_id":"example-service"`) || !strings.Contains(counts.Body.String(), `"provider":"jira"`) || !strings.Contains(counts.Body.String(), `"count":2`) || !strings.Contains(counts.Body.String(), `"provider":"confluence"`) || !strings.Contains(counts.Body.String(), `"count":1`) {
		t.Fatalf("unexpected counts response %d: %s", counts.Code, counts.Body.String())
	}
	assertDoesNotLeak(t, counts.Body.String(), "tenant.atlassian.net", "MIVIA_ATLASSIAN", "/home/mac/secret", "content_sha256")

	jira := httptest.NewRecorder()
	mux.ServeHTTP(jira, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/integrations/jira/issues?page_size=2", nil))
	if jira.Code != http.StatusOK {
		t.Fatalf("expected jira list 200, got %d: %s", jira.Code, jira.Body.String())
	}
	if !strings.Contains(jira.Body.String(), `"sort":"updated_desc"`) || strings.Index(jira.Body.String(), "LOCAL-2") > strings.Index(jira.Body.String(), "LOCAL-1") {
		t.Fatalf("expected recent jira issues sorted by updated desc, got %s", jira.Body.String())
	}
	assertDoesNotLeak(t, jira.Body.String(), "tenant.atlassian.net", "MIVIA_ATLASSIAN", "/home/mac/secret")

	confluence := httptest.NewRecorder()
	mux.ServeHTTP(confluence, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/integrations/confluence/pages", nil))
	if confluence.Code != http.StatusOK || !strings.Contains(confluence.Body.String(), `"provider":"confluence"`) || !strings.Contains(confluence.Body.String(), `"item_type":"page"`) {
		t.Fatalf("unexpected confluence pages response %d: %s", confluence.Code, confluence.Body.String())
	}

	search := httptest.NewRecorder()
	mux.ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/integrations/search?provider=confluence&query=policy", nil))
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "bounded local confluence result") {
		t.Fatalf("unexpected local search response %d: %s", search.Code, search.Body.String())
	}

	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/integrations/confluence/pages/20001", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "bounded local page text") {
		t.Fatalf("unexpected confluence read response %d: %s", page.Code, page.Body.String())
	}

	invalidSort := httptest.NewRecorder()
	mux.ServeHTTP(invalidSort, httptest.NewRequest(http.MethodGet, "/api/v1/projects/example-service/integrations/jira/issues?sort=provider_url_desc", nil))
	if invalidSort.Code != http.StatusBadRequest || strings.Contains(invalidSort.Body.String(), "tenant.atlassian.net") {
		t.Fatalf("expected redacted 400 for invalid sort, got %d: %s", invalidSort.Code, invalidSort.Body.String())
	}
}

type fakeWorkspaceAPI struct {
	file   projectworkspace.WorkspaceFile
	edit   projectworkspace.EditResult
	create projectworkspace.CreateFileResult
	delete projectworkspace.DeleteFileResult
	err    error
}

func (fake *fakeWorkspaceAPI) GitStatus(context.Context, string, projectworkspace.GitStatusOptions) (projectworkspace.GitStatus, error) {
	if fake.err != nil {
		return projectworkspace.GitStatus{}, fake.err
	}
	return projectworkspace.GitStatus{ProjectID: "example-service"}, nil
}

func (fake *fakeWorkspaceAPI) GitAvailable(context.Context, string) (bool, error) {
	if fake.err != nil {
		return false, fake.err
	}
	return true, nil
}

func (fake *fakeWorkspaceAPI) GitDiff(context.Context, string, projectworkspace.GitDiffOptions) (projectworkspace.GitDiff, error) {
	return projectworkspace.GitDiff{ProjectID: "example-service"}, nil
}

func (fake *fakeWorkspaceAPI) ReadFile(context.Context, string, projectworkspace.ReadFileOptions) (projectworkspace.WorkspaceFile, error) {
	return fake.file, nil
}

func (fake *fakeWorkspaceAPI) EditFile(context.Context, string, projectworkspace.EditFileOptions) (projectworkspace.EditResult, error) {
	return fake.edit, nil
}

func (fake *fakeWorkspaceAPI) CreateFile(context.Context, string, projectworkspace.CreateFileOptions) (projectworkspace.CreateFileResult, error) {
	return fake.create, nil
}

func (fake *fakeWorkspaceAPI) DeleteFile(context.Context, string, projectworkspace.DeleteFileOptions) (projectworkspace.DeleteFileResult, error) {
	return fake.delete, nil
}

func TestProjectIngestionRoutes_SkippedSensitiveContentDoesNotLeak(t *testing.T) {
	mux, projectID, root := newIngestionMux(t, "package main\nvar access_token = placeholder\n")

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/ingestion-runs", nil))
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	var run projectingestion.RunMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	waitIngestionRun(t, mux, projectID, run.ID)

	files := httptest.NewRecorder()
	mux.ServeHTTP(files, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files?status=skipped", nil))
	if files.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", files.Code, files.Body.String())
	}
	body := files.Body.String()
	assertDoesNotLeak(t, body, root, "cmd/main.go", "access_token", "placeholder", "content_sha256")
	if !strings.Contains(body, `"skipped_reason":"sensitive_content"`) {
		t.Fatalf("expected non-sensitive reason code, got %s", body)
	}
}

func TestProjectIngestionRoutes_ListFilesFiltersByExtension(t *testing.T) {
	mux, projectID, _ := newIngestionMuxWithFiles(t, map[string]string{
		"cmd/main.go": "package main\nfunc main() {}\n",
		"README.md":   "# example\n",
	}, []string{"**/*"})

	created := httptest.NewRecorder()
	mux.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/ingestion-runs", nil))
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var run projectingestion.RunMetadata
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	waitIngestionRun(t, mux, projectID, run.ID)

	files := httptest.NewRecorder()
	mux.ServeHTTP(files, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files?status=eligible&extension=GO&page_size=1", nil))
	if files.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", files.Code, files.Body.String())
	}
	var fileList projectingestion.FileList
	if err := json.Unmarshal(files.Body.Bytes(), &fileList); err != nil {
		t.Fatalf("decode files: %v", err)
	}
	if len(fileList.Files) != 1 || fileList.Files[0].RelativePath != "cmd/main.go" || fileList.NextPageToken != "" {
		t.Fatalf("unexpected filtered file list: %#v", fileList)
	}
	if fileList.Files[0].Extension != ".go" {
		t.Fatalf("expected extension metadata, got %#v", fileList.Files[0])
	}

	file := httptest.NewRecorder()
	mux.ServeHTTP(file, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files/"+fileList.Files[0].ID, nil))
	if file.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", file.Code, file.Body.String())
	}
	var fileMetadata projectingestion.FileMetadata
	if err := json.Unmarshal(file.Body.Bytes(), &fileMetadata); err != nil {
		t.Fatalf("decode file: %v", err)
	}
	if fileMetadata.ID != fileList.Files[0].ID || fileMetadata.RelativePath != "cmd/main.go" || fileMetadata.Extension != ".go" {
		t.Fatalf("unexpected file metadata: %#v", fileMetadata)
	}

	for _, query := range []string{"extension=bad/path", "extension=bad%20path", "extension=go.md", "extension=g*"} {
		invalid := httptest.NewRecorder()
		mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files?"+query, nil))
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid extension query %q, got %d: %s", query, invalid.Code, invalid.Body.String())
		}
	}
}

func TestProjectIngestionRoutes_SearchEndpointsAreBoundedAndSafe(t *testing.T) {
	mux, projectID, root := newIngestionMuxWithFiles(t, map[string]string{
		"cmd/main.go": `package main

func helperAlpha() {}

func Run() {
	helperAlpha()
}
`,
		"secrets/token.go": "package main\nvar access_token = placeholder\n",
	}, []string{"**/*"})

	created := httptest.NewRecorder()
	mux.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/ingestion-runs", nil))
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var run projectingestion.RunMetadata
	if err := json.Unmarshal(created.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	waitIngestionRun(t, mux, projectID, run.ID)

	cases := []string{
		"/api/v1/projects/" + projectID + "/search/text?query=helper&page_size=1&max_snippet_bytes=20",
		"/api/v1/projects/" + projectID + "/search/files?path_contains=main",
		"/api/v1/projects/" + projectID + "/search/symbols?name_contains=Alpha",
		"/api/v1/projects/" + projectID + "/search/references?target_name_contains=Alpha",
		"/api/v1/projects/" + projectID + "/search/calls?caller_name_contains=Run&callee_name_contains=Alpha",
		"/api/v1/projects/" + projectID + "/search/ast/queries",
		"/api/v1/projects/" + projectID + "/search/ast?language=go&query=call_expressions&captures=callee&max_snippet_bytes=20",
		"/api/v1/projects/" + projectID + "/search/ast?language=dart&query=flutter_widgets&captures=name&max_snippet_bytes=20",
	}
	for _, path := range cases {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d: %s", path, res.Code, res.Body.String())
		}
		assertDoesNotLeak(t, res.Body.String(), root, "access_token", "placeholder", "content_sha256", "secrets/token.go")
	}
	astQueries := httptest.NewRecorder()
	mux.ServeHTTP(astQueries, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/search/ast/queries", nil))
	if astQueries.Code != http.StatusOK || !strings.Contains(astQueries.Body.String(), `"language":"dart"`) || !strings.Contains(astQueries.Body.String(), `"id":"function_declarations"`) || strings.Contains(astQueries.Body.String(), "(function_declaration") {
		t.Fatalf("unexpected AST query catalog response: %d %s", astQueries.Code, astQueries.Body.String())
	}

	secret := httptest.NewRecorder()
	mux.ServeHTTP(secret, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/search/text?query=access_token", nil))
	if secret.Code != http.StatusOK || !strings.Contains(secret.Body.String(), `"results":[]`) {
		t.Fatalf("expected empty safe secret search, got %d: %s", secret.Code, secret.Body.String())
	}
	assertDoesNotLeak(t, secret.Body.String(), root, "access_token", "placeholder", "content_sha256", "secrets/token.go")

	repair := httptest.NewRecorder()
	mux.ServeHTTP(repair, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/search-index/rebuild", nil))
	if repair.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for search-index rebuild, got %d: %s", repair.Code, repair.Body.String())
	}
	assertDoesNotLeak(t, repair.Body.String(), root, "root_path", "content_sha256", "access_token", "placeholder", "project_search")
	var repairRun projectingestion.RunMetadata
	if err := json.Unmarshal(repair.Body.Bytes(), &repairRun); err != nil {
		t.Fatalf("decode repair run: %v", err)
	}
	if repairRun.ID == "" || repairRun.Status != string(projectingestion.RunStatusPending) {
		t.Fatalf("expected queued repair run metadata, got %#v", repairRun)
	}
}

func newMux(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	registry, digest := newRegistryDigest(t)
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, registry, digest)
	return mux, "example-service"
}

func newRegistryDigest(t *testing.T) (*projectregistry.Registry, *projectregistry.DigestService) {
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
	digest := projectregistry.NewDigestService(registry, graph)
	return registry, digest
}

func newIngestionMux(t *testing.T, content string) (*http.ServeMux, string, string) {
	return newIngestionMuxWithFiles(t, map[string]string{"cmd/main.go": content}, []string{"**/*.go"})
}

func newIngestionMuxWithFiles(t *testing.T, files map[string]string, include []string) (*http.ServeMux, string, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
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
		Include:               include,
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
	digest := projectregistry.NewDigestService(registry, graph)
	ingestion := projectingestion.NewService(registry, projectingestion.NewGraphStore(graph), projectingestion.NewSQLiteStore(db.SQLDB()))
	scheduler := projectingestion.NewScheduler(ingestion, projectingestion.SchedulerOptions{QueueDepth: 8, GlobalWorkerCount: 2, PerProjectWorkerLimit: 1})
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start scheduler: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = scheduler.Stop(context.Background())
	})
	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithIngestion(mux, registry, digest, scheduler)
	return mux, "example-service", root
}

func newIntegrationDashboardMux(t *testing.T) (*http.ServeMux, string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	project := config.Project{
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
		Integrations: config.IntegrationConfig{
			Jira: &config.JiraIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "tenant-cloud-id",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailEnv:    "MIVIA_ATLASSIAN_EMAIL",
					APITokenEnv: "MIVIA_ATLASSIAN_TOKEN",
				},
				Polling:     config.IntegrationPolling{IngestionEnabled: true, InitialFullSync: "manual", IncrementalInterval: time.Minute},
				ProjectKeys: []string{"ACME", "OPS"},
			},
			Confluence: &config.ConfluenceIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "tenant-cloud-id",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailFile:    "/home/mac/secret-email",
					APITokenFile: "/home/mac/secret-token",
				},
				Polling:   config.IntegrationPolling{IngestionEnabled: true, InitialFullSync: "manual", IncrementalInterval: time.Minute},
				SpaceKeys: []string{"ENG", "TEAM"},
			},
		},
	}
	registry, err := projectregistry.NewRegistry([]config.Project{project}, projectregistry.Options{
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
	digest := projectregistry.NewDigestService(registry, graph)
	ingestion := projectingestion.NewService(registry, projectingestion.NewGraphStore(graph), projectingestion.NewSQLiteStore(db.SQLDB()))
	integrations, err := projectintegrations.NewService([]config.Project{project}, &dashboardIntegrationStore{})
	if err != nil {
		t.Fatalf("new integration service: %v", err)
	}
	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithWorkspaceAndIntegrations(mux, registry, digest, ingestion, nil, integrations)
	return mux, "example-service", root
}

type dashboardIntegrationStore struct{}

func (store *dashboardIntegrationStore) UpsertSource(context.Context, projectintegrations.SourceMetadataInput) (projectintegrations.SourceMetadata, error) {
	return projectintegrations.SourceMetadata{}, projectintegrations.ErrNotFound
}

func (store *dashboardIntegrationStore) ListSources(context.Context, string) ([]projectintegrations.SourceMetadata, error) {
	return []projectintegrations.SourceMetadata{
		{ProjectID: "example-service", Provider: projectintegrations.ProviderJira, AllowlistCount: 2},
		{ProjectID: "example-service", Provider: projectintegrations.ProviderConfluence, AllowlistCount: 2},
	}, nil
}

func (store *dashboardIntegrationStore) GetSyncState(_ context.Context, _ string, provider projectintegrations.Provider) (projectintegrations.SyncState, error) {
	if provider != projectintegrations.ProviderJira {
		return projectintegrations.SyncState{}, projectintegrations.ErrNotFound
	}
	return projectintegrations.SyncState{ProjectID: "example-service", Provider: projectintegrations.ProviderJira, LastRunID: "jira-run-1", LastSuccessfulRunID: "jira-run-1", UpdatedAt: time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)}, nil
}

func (store *dashboardIntegrationStore) GetSyncRun(_ context.Context, _ string, provider projectintegrations.Provider, runID string) (projectintegrations.SyncRun, error) {
	if provider != projectintegrations.ProviderJira || runID != "jira-run-1" {
		return projectintegrations.SyncRun{}, projectintegrations.ErrNotFound
	}
	return projectintegrations.SyncRun{ID: runID, ProjectID: "example-service", Provider: provider, Kind: projectintegrations.SyncKindIncremental, Status: projectintegrations.SyncRunStatusCompleted, ItemsSeen: 7, ItemsUpserted: 4}, nil
}

func (store *dashboardIntegrationStore) GetActiveSyncRun(_ context.Context, _ string, provider projectintegrations.Provider) (projectintegrations.SyncRun, error) {
	if provider != projectintegrations.ProviderJira {
		return projectintegrations.SyncRun{}, projectintegrations.ErrNotFound
	}
	return projectintegrations.SyncRun{ID: "jira-run-active", ProjectID: "example-service", Provider: projectintegrations.ProviderJira, Kind: projectintegrations.SyncKindInitialFull, Status: projectintegrations.SyncRunStatusRunning, ItemsSeen: 2, ItemsUpserted: 1}, nil
}

func (store *dashboardIntegrationStore) CountItems(_ context.Context, _ string, provider projectintegrations.Provider) (int, error) {
	switch provider {
	case projectintegrations.ProviderJira:
		return 3, nil
	case projectintegrations.ProviderConfluence:
		return 2, nil
	default:
		return 0, projectintegrations.ErrNotFound
	}
}

func waitIngestionRun(t *testing.T, mux *http.ServeMux, projectID string, runID string) projectingestion.RunMetadata {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		status := httptest.NewRecorder()
		mux.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/ingestion-runs/"+runID, nil))
		if status.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", status.Code, status.Body.String())
		}
		var run projectingestion.RunMetadata
		if err := json.Unmarshal(status.Body.Bytes(), &run); err != nil {
			t.Fatalf("decode status run: %v", err)
		}
		if run.Status == string(projectingestion.RunStatusCompleted) || run.Status == string(projectingestion.RunStatusFailed) {
			return run
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for ingestion run %#v", run)
		case <-ticker.C:
		}
	}
}

func assertProjectResponseSafe(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"root_path", "canonical", "/tmp/", `\home\`, "include", "exclude"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("project response leaked %q: %s", forbidden, body)
		}
	}
}

func assertDoesNotLeak(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if value != "" && strings.Contains(body, value) {
			t.Fatalf("response leaked %q: %s", value, body)
		}
	}
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

func newIntegrationRegistryDigest(t *testing.T) (*projectregistry.Registry, *projectregistry.DigestService, config.Project) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	project := config.Project{
		ID:             "example-service",
		DisplayName:    "Example Service",
		RootPath:       root,
		Enabled:        true,
		Classification: projectregistry.ClassificationInternal,
		GraphNamespace: "example-service",
		DigestMode:     projectregistry.DigestModeMetadataOnly,
		UpdatePolicy:   projectregistry.UpdatePolicyManual,
		Include:        []string{"**/*.go"},
		FollowSymlinks: false,
		Integrations: config.IntegrationConfig{
			Jira: &config.JiraIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "cloud-id-1",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailEnv:    "MIVIA_ATLASSIAN_EMAIL_PROJECT_1",
					APITokenEnv: "MIVIA_ATLASSIAN_TOKEN_PROJECT_1",
				},
				ProjectKeys: []string{"LOCAL"},
			},
			Confluence: &config.ConfluenceIntegration{
				Enabled:    true,
				SiteURL:    "https://tenant.atlassian.net",
				CloudID:    "cloud-id-1",
				AuthMode:   "api_token_basic",
				MaxResults: 100,
				CredentialRefs: config.AtlassianCredentialRefs{
					EmailFile:    "/home/mac/secret-email",
					APITokenFile: "/home/mac/secret-token",
				},
				SpaceKeys: []string{"ENG"},
			},
		},
	}
	registry, err := projectregistry.NewRegistry([]config.Project{project}, projectregistry.Options{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	return registry, projectregistry.NewDigestService(registry, graph), project
}

type fakeIntegrationRichContent struct{}

func (fakeIntegrationRichContent) SearchRichContent(_ context.Context, projectID string, options projectintegrations.RichContentSearchOptions) ([]projectintegrations.RichContentSearchResult, error) {
	return []projectintegrations.RichContentSearchResult{{
		Artifact: projectintegrations.RichContentArtifact{
			ID:        "artifact-1",
			ProjectID: projectID,
			Provider:  options.Provider,
			ItemID:    "20001",
			ItemType:  "page",
		},
		Snippet: "bounded local confluence result",
	}}, nil
}

func (fakeIntegrationRichContent) GetRichContentItem(_ context.Context, projectID string, provider projectintegrations.Provider, itemIDOrKey string, _ projectintegrations.RichContentReadOptions) (projectintegrations.RichContentReadResult, error) {
	return projectintegrations.RichContentReadResult{
		Artifact: projectintegrations.RichContentArtifact{
			ID:        "artifact-1",
			ProjectID: projectID,
			Provider:  provider,
			ItemID:    itemIDOrKey,
			ItemType:  "page",
		},
		Chunks: []projectintegrations.RichContentChunkView{{
			ID:        "chunk-1",
			ProjectID: projectID,
			Provider:  provider,
			ItemID:    itemIDOrKey,
			ItemType:  "page",
			Text:      "bounded local page text",
		}},
	}, nil
}

func testIntegrationTime() time.Time {
	return time.Date(2026, 6, 2, 5, 0, 0, 0, time.UTC)
}
