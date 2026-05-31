package projectingestion

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestGraphStore_DeleteDerivedFileNodesIsFileScopedAndRemovesRelationships(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewGraphStore(graph)
	project := testGraphProject()
	run := testGraphRun(project.ID)
	state := testGraphState("cmd/a.go", "hash-a", "sha256:a")

	if err := store.PutEligibleFile(ctx, project, run, state, []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:a"}}, []Symbol{{Kind: SymbolKindFunction, Name: "A", StartLine: 1, EndLine: 1}}, nil, nil, nil); err != nil {
		t.Fatalf("put initial file graph: %v", err)
	}
	otherState := testGraphState("cmd/b.go", "hash-b", "sha256:b")
	if err := store.PutEligibleFile(ctx, project, run, otherState, []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:b"}}, nil, nil, nil, nil); err != nil {
		t.Fatalf("put other file graph: %v", err)
	}

	repoFileIDValue := repoFileID(project.GraphNamespace, state.RelativePathHash)
	if err := store.deleteDerivedFileNodes(ctx, project.ID, repoFileIDValue); err != nil {
		t.Fatalf("delete derived nodes: %v", err)
	}
	if chunks, err := graph.ListNodes(ctx, "ContentChunk", map[string]string{"project_id": project.ID, "repo_file_id": repoFileIDValue}); err != nil || len(chunks) != 0 {
		t.Fatalf("expected deleted file chunks gone, chunks=%#v err=%v", chunks, err)
	}
	if _, err := graph.GetRelationship(ctx, "VERSION_HAS_CHUNK", ladybug.NodeRef{Label: "FileVersion", ID: fileVersionID(repoFileIDValue, state.ContentSHA256)}, ladybug.NodeRef{Label: "ContentChunk", ID: contentChunkID(fileVersionID(repoFileIDValue, state.ContentSHA256), 0)}); !errors.Is(err, ladybug.ErrRelationshipNotFound) {
		t.Fatalf("expected attached relationship removed, got %v", err)
	}
	otherRepoFileID := repoFileID(project.GraphNamespace, otherState.RelativePathHash)
	if chunks, err := graph.ListNodes(ctx, "ContentChunk", map[string]string{"project_id": project.ID, "repo_file_id": otherRepoFileID}); err != nil || len(chunks) != 1 {
		t.Fatalf("expected other file chunk preserved, chunks=%#v err=%v", chunks, err)
	}
}

func TestGraphStore_PutPreparedFilesBatchWritesProjectAndRunOnce(t *testing.T) {
	ctx := context.Background()
	graph := &putCountingGraph{MemoryGraph: ladybug.NewMemoryGraph()}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewGraphStore(graph)
	project := testGraphProject()
	run := testGraphRun(project.ID)

	err := store.PutPreparedFilesBatch(ctx, project, run, []PreparedGraphFile{
		{State: testGraphState("cmd/a.go", "hash-a", "sha256:a"), Chunks: []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:a"}}},
		{State: testGraphState("cmd/b.go", "hash-b", "sha256:b"), Chunks: []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:b"}}},
	})
	if err != nil {
		t.Fatalf("put graph batch: %v", err)
	}
	if graph.projectPuts != 1 || graph.runPuts != 1 {
		t.Fatalf("expected project/run once per batch, got project=%d run=%d", graph.projectPuts, graph.runPuts)
	}

	err = store.PutPreparedFilesBatch(ctx, project, run, []PreparedGraphFile{{State: FileState{Status: FileStatusEligible}}})
	if err == nil || !strings.Contains(err.Error(), "graph batch file 0 failed") {
		t.Fatalf("expected diagnosable batch error, got %v", err)
	}
}

func BenchmarkGraphDeleteDerivedFileNodes(b *testing.B) {
	ctx := context.Background()
	project := testGraphProject()
	run := testGraphRun(project.ID)
	for i := 0; i < b.N; i++ {
		graph := ladybug.NewMemoryGraph()
		if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
			b.Fatalf("bootstrap graph: %v", err)
		}
		store := NewGraphStore(graph)
		for fileIndex := 0; fileIndex < 200; fileIndex++ {
			suffix := strconv.Itoa(fileIndex)
			state := testGraphState("cmd/file-"+suffix+".go", "hash-"+suffix, "sha256:"+suffix)
			if err := store.PutEligibleFile(ctx, project, run, state, []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: state.ContentSHA256}}, nil, nil, nil, nil); err != nil {
				b.Fatalf("put file graph: %v", err)
			}
		}
		target := repoFileID(project.GraphNamespace, "hash-100")
		b.StartTimer()
		if err := store.deleteDerivedFileNodes(ctx, project.ID, target); err != nil {
			b.Fatalf("delete derived nodes: %v", err)
		}
		b.StopTimer()
	}
}

type putCountingGraph struct {
	*ladybug.MemoryGraph
	projectPuts int
	runPuts     int
}

func (graph *putCountingGraph) PutNode(ctx context.Context, node ladybug.Node) error {
	if node.Label == "Project" {
		graph.projectPuts++
	}
	if node.Label == "IngestionRun" {
		graph.runPuts++
	}
	return graph.MemoryGraph.PutNode(ctx, node)
}

func testGraphProject() projectregistry.Project {
	return projectregistry.Project{ID: "example-service", DisplayName: "Example", GraphNamespace: "example_ns"}
}

func testGraphRun(projectID string) Run {
	now := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	return Run{ID: "run-1", ProjectID: projectID, Trigger: TriggerManual, Status: RunStatusRunning, StartedAt: now}
}

func testGraphState(relativePath string, relativePathHash string, contentSHA string) FileState {
	now := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	return FileState{
		ProjectID:        "example-service",
		RelativePath:     relativePath,
		RelativePathHash: relativePathHash,
		RelativePathSafe: true,
		Status:           FileStatusEligible,
		Present:          true,
		ContentSHA256:    contentSHA,
		SizeBytes:        int64(len(contentSHA)),
		ModifiedAt:       now,
	}
}
