package projectingestion

import (
	"context"
	"errors"
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
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/projectregistry"
)

func TestIngestProject_StoresEligibleContentGraphState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n\nfunc Run() {}\n")
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "# Guide\n\n## Setup\n")

	svc, graph, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted {
		t.Fatalf("expected completed run, got %#v", run)
	}
	if run.FilesIngested != 2 || run.ChunksStored != 2 || run.SymbolsStored == 0 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusEligible})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected two eligible states, got %#v", states)
	}
	for _, fileState := range states {
		if fileState.ContentSHA256 == "" {
			t.Fatalf("expected content hash for eligible file: %#v", fileState)
		}
		if strings.Contains(fileState.RelativePath, root) {
			t.Fatalf("relative path leaked root: %#v", fileState)
		}
	}

	goState := findState(t, states, "cmd/main.go")
	repoFileID := repoFileID("example_ns", goState.RelativePathHash)
	versionID := fileVersionID(repoFileID, goState.ContentSHA256)
	chunkNode, err := graph.GetNode(ctx, "ContentChunk", contentChunkID(versionID, 0))
	if err != nil {
		t.Fatalf("get content chunk: %v", err)
	}
	if !strings.Contains(chunkNode.Properties["text"], "func Run") {
		t.Fatalf("expected stored eligible chunk text, got %#v", chunkNode.Properties)
	}
	assertNoPropertyContains(t, chunkNode.Properties, root)

	symbolID := codeSymbolID(repoFileID, Symbol{
		Kind:      SymbolKindFunction,
		Name:      "Run",
		StartLine: 3,
	})
	symbolNode, err := graph.GetNode(ctx, "CodeSymbol", symbolID)
	if err != nil {
		t.Fatalf("get function symbol: %v", err)
	}
	if symbolNode.Properties["name"] != "Run" {
		t.Fatalf("expected Run symbol, got %#v", symbolNode.Properties)
	}
}

func TestIngestProject_SensitiveContentSkipIsHashOnly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "main.go"), "package main\nvar access_token = placeholder\n")

	svc, graph, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.FilesSkipped != 1 || run.FilesIngested != 0 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected one skipped state, got %#v", states)
	}
	skipped := states[0]
	if skipped.SkippedReason != SkipReasonSensitiveContent {
		t.Fatalf("expected sensitive-content skip, got %#v", skipped)
	}
	if skipped.RelativePath != "" || skipped.RelativePathSafe || skipped.ContentSHA256 != "" {
		t.Fatalf("sensitive skip must be hash-only without content hash: %#v", skipped)
	}

	repoFileNode, err := graph.GetNode(ctx, "RepoFile", repoFileID("example_ns", hashValue("cmd/main.go")))
	if err != nil {
		t.Fatalf("get skipped repo file: %v", err)
	}
	if _, ok := repoFileNode.Properties["relative_path"]; ok {
		t.Fatalf("skipped sensitive file leaked path: %#v", repoFileNode.Properties)
	}
	if _, ok := repoFileNode.Properties["content_sha256"]; ok {
		t.Fatalf("skipped sensitive file stored content hash: %#v", repoFileNode.Properties)
	}
}

func TestIngestProject_SkipsBinaryInvalidUTF8AndSymlinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeBytes(t, filepath.Join(root, "binary.txt"), []byte{0, 1, 2, 3})
	writeBytes(t, filepath.Join(root, "invalid.txt"), []byte{0xff, 0xfe, 0xfd})
	writeFile(t, filepath.Join(root, "target.txt"), "safe text\n")
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "linked.txt")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.FilesIngested != 1 || run.FilesSkipped != 3 {
		t.Fatalf("unexpected run counts: %#v", run)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	reasons := map[SkipReason]bool{}
	for _, fileState := range states {
		reasons[fileState.SkippedReason] = true
		if fileState.ContentSHA256 != "" {
			t.Fatalf("skipped file stored content hash: %#v", fileState)
		}
	}
	for _, reason := range []SkipReason{SkipReasonNULByte, SkipReasonInvalidUTF8, SkipReasonUnsafePath} {
		if !reasons[reason] {
			t.Fatalf("expected skip reason %q in %#v", reason, states)
		}
	}
}

func TestIngestPath_DeletedFileCreatesTombstone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	filePath := filepath.Join(root, "docs", "guide.md")
	writeFile(t, filePath, "# Guide\n")

	svc, _, state := newTestService(t, root)
	if _, err := svc.IngestPath(ctx, "example-service", "docs/guide.md", TriggerManual); err != nil {
		t.Fatalf("ingest path: %v", err)
	}
	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if _, err := svc.IngestPath(ctx, "example-service", "docs/guide.md", TriggerManual); err != nil {
		t.Fatalf("ingest deleted path: %v", err)
	}

	states, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusAbsent})
	if err != nil {
		t.Fatalf("list absent states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected absent state, got %#v", states)
	}
	if states[0].Present || states[0].ContentSHA256 != "" {
		t.Fatalf("deleted file must be tombstoned without content hash: %#v", states[0])
	}
}

func TestIngestPath_RejectsPathEscape(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc, _, _ := newTestService(t, root)
	if _, err := svc.IngestPath(ctx, "example-service", "../outside.go", TriggerManual); !errors.Is(err, ErrPathNotProjectLocal) {
		t.Fatalf("expected path-local error, got %v", err)
	}
}

func newTestService(t *testing.T, root string) (*Service, *ladybug.MemoryGraph, *SQLiteStore) {
	t.Helper()
	registry, err := projectregistry.NewRegistry([]config.Project{{
		ID:                    "example-service",
		DisplayName:           "Example Service",
		RootPath:              root,
		Enabled:               true,
		Classification:        projectregistry.ClassificationInternal,
		GraphNamespace:        "example_ns",
		DigestMode:            projectregistry.DigestModeContentGraph,
		UpdatePolicy:          projectregistry.UpdatePolicyManual,
		Include:               []string{"**/*.go", "**/*.md", "**/*.txt"},
		FollowSymlinks:        false,
		MaxFileBytes:          4096,
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
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
	state := NewSQLiteStore(db.SQLDB())
	svc := NewService(registry, NewGraphStore(graph), state)
	svc.now = func() time.Time { return time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) }
	svc.newID = func(projectregistry.Project, time.Time) string { return "ingest_run_1" }
	return svc, graph, state
}

func findState(t *testing.T, states []FileState, relativePath string) FileState {
	t.Helper()
	for _, state := range states {
		if state.RelativePath == relativePath {
			return state
		}
	}
	t.Fatalf("state for %q not found in %#v", relativePath, states)
	return FileState{}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	writeBytes(t, path, []byte(content))
}

func writeBytes(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func assertNoPropertyContains(t *testing.T, properties map[string]string, forbidden string) {
	t.Helper()
	for key, value := range properties {
		if strings.Contains(value, forbidden) {
			t.Fatalf("property %q leaked forbidden value %q in %#v", key, forbidden, properties)
		}
	}
}
