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
		t.Fatalf("filtered outline leaked raw source: %s", filteredBody)
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
		"cmd/main.go":   "package main\nfunc main() {}\n",
		"docs/guide.md": "# Guide\n\n## Setup\n",
		"web/app.ts":    "export class Widget {}\nexport const load = () => true\n",
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
