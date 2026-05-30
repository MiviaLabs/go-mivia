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
		edit: projectworkspace.EditResult{Applied: true, IngestionRunID: "ingest-path-1"},
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
}

type fakeWorkspaceAPI struct {
	file projectworkspace.WorkspaceFile
	edit projectworkspace.EditResult
}

func (fake *fakeWorkspaceAPI) GitStatus(context.Context, string, projectworkspace.GitStatusOptions) (projectworkspace.GitStatus, error) {
	return projectworkspace.GitStatus{ProjectID: "example-service"}, nil
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
	if astQueries.Code != http.StatusOK || !strings.Contains(astQueries.Body.String(), `"id":"function_declarations"`) || strings.Contains(astQueries.Body.String(), "(function_declaration") {
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
