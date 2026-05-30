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

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectingestion"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/httpapi"
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
	mux, projectID, root := newIngestionMux(t, "package main\n\nfunc Run() { println(\"hello world\") }\n")

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
	if run.Status != string(projectingestion.RunStatusCompleted) || run.FilesIngested != 1 {
		t.Fatalf("unexpected ingestion run: %#v", run)
	}

	status := httptest.NewRecorder()
	mux.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/ingestion-runs/"+run.ID, nil))
	if status.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", status.Code, status.Body.String())
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
}

func TestProjectIngestionRoutes_SkippedSensitiveContentDoesNotLeak(t *testing.T) {
	mux, projectID, root := newIngestionMux(t, "package main\nvar access_token = placeholder\n")

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/ingestion-runs", nil))
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}

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

	for _, query := range []string{"extension=bad/path", "extension=bad%20path", "extension=go.md", "extension=g*"} {
		invalid := httptest.NewRecorder()
		mux.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/files?"+query, nil))
		if invalid.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid extension query %q, got %d: %s", query, invalid.Code, invalid.Body.String())
		}
	}
}

func newMux(t *testing.T) (*http.ServeMux, string) {
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
	mux := http.NewServeMux()
	httpapi.RegisterRoutes(mux, registry, digest)
	return mux, "example-service"
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
	mux := http.NewServeMux()
	httpapi.RegisterRoutesWithIngestion(mux, registry, digest, ingestion)
	return mux, "example-service", root
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
