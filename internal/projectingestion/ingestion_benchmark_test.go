package projectingestion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	ladybugschema "github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	sqliteplatform "github.com/MiviaLabs/go-mivia/internal/platform/sqlite"
	sqliteschema "github.com/MiviaLabs/go-mivia/internal/platform/sqlite/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func BenchmarkPrepareExistingFile_UnchangedFastPath(b *testing.B) {
	ctx := context.Background()
	root := b.TempDir()
	path := filepath.Join(root, "cmd", "main.go")
	benchmarkWriteFile(b, path, "package main\n\nfunc Run() {}\n")
	svc, _ := newBenchmarkService(b, root)
	if _, err := svc.IngestProject(ctx, "example-service", TriggerManual); err != nil {
		b.Fatalf("initial ingest: %v", err)
	}
	project, ok := svc.registry.Get("example-service")
	if !ok {
		b.Fatal("missing benchmark project")
	}
	info, err := os.Lstat(path)
	if err != nil {
		b.Fatalf("stat benchmark file: %v", err)
	}
	run := svc.startRun(project, TriggerManual)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := svc.prepareExistingFile(ctx, project, "cmd/main.go", path, info, run)
		if result.err != nil {
			b.Fatalf("prepare unchanged file: %v", result.err)
		}
		if !result.unchanged {
			b.Fatal("expected unchanged fast path")
		}
	}
}

func BenchmarkExecuteProjectRun_BatchedWrites(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		root := b.TempDir()
		for fileIndex := 0; fileIndex < 100; fileIndex++ {
			benchmarkWriteFile(b, filepath.Join(root, "pkg", fmt.Sprintf("file_%03d.go", fileIndex)), fmt.Sprintf("package pkg\n\nfunc F%d() {}\n", fileIndex))
		}
		svc, _ := newBenchmarkService(b, root)
		svc.SetFullScanBatchSize(50)
		b.StartTimer()
		run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
		b.StopTimer()
		if err != nil {
			b.Fatalf("ingest project: %v", err)
		}
		if run.Status != RunStatusCompleted || run.FilesIngested != 100 {
			b.Fatalf("unexpected run: %#v", run)
		}
	}
}

func BenchmarkSearchUpsertBatch(b *testing.B) {
	ctx := context.Background()
	root := b.TempDir()
	svc, state := newBenchmarkService(b, root)
	project, ok := svc.registry.Get("example-service")
	if !ok {
		b.Fatal("missing benchmark project")
	}
	files := make([]PreparedSearchFile, 0, 100)
	now := time.Now().UTC()
	for i := 0; i < 100; i++ {
		relative := fmt.Sprintf("pkg/file_%03d.go", i)
		content := fmt.Sprintf("package pkg\n\nfunc F%d() {}\n", i)
		chunks, safety, err := BuildChunks(relative, []byte(content), DefaultSafetyOptions())
		if err != nil {
			b.Fatalf("build chunks: %v", err)
		}
		fileState := fileStateFromSafety(project, relative, safety, chunks.ContentSHA256, now, now)
		files = append(files, PreparedSearchFile{
			State:  fileState,
			Chunks: chunks.Chunks,
			Symbols: []Symbol{{
				Kind:      SymbolKindFunction,
				Name:      fmt.Sprintf("F%d", i),
				StartLine: 3,
				EndLine:   3,
			}},
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := state.UpsertSearchFilesBatch(ctx, project, files); err != nil {
			b.Fatalf("upsert search batch: %v", err)
		}
	}
}

func newBenchmarkService(b *testing.B, root string) (*Service, *SQLiteStore) {
	b.Helper()
	project := testConfigProject("example-service", "example_ns", root)
	project.MaxFileBytes = 0
	registry, err := projectregistry.NewRegistry([]config.Project{project}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
	})
	if err != nil {
		b.Fatalf("new registry: %v", err)
	}
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(context.Background(), ladybugschema.BootstrapSchema()); err != nil {
		b.Fatalf("bootstrap graph: %v", err)
	}
	db, err := sqliteplatform.Open(":memory:")
	if err != nil {
		b.Fatalf("open sqlite: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if err := sqliteschema.Bootstrap(context.Background(), db.SQLDB()); err != nil {
		b.Fatalf("bootstrap sqlite: %v", err)
	}
	state := NewSQLiteStore(db.SQLDB())
	svc := NewService(registry, NewGraphStore(graph), state)
	svc.now = func() time.Time { return time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) }
	svc.newID = func(projectregistry.Project, time.Time) string { return "ingest_run_1" }
	return svc, state
}

func benchmarkWriteFile(b *testing.B, path string, content string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatalf("mkdir benchmark file parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatalf("write benchmark file: %v", err)
	}
}
