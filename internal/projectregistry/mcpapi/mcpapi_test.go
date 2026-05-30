package mcpapi_test

import (
	"context"
	"encoding/json"
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
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry/mcpapi"
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
		"cmd/main.go": "package main\nfunc main() {}\n",
		"README.md":   "# example\n",
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
