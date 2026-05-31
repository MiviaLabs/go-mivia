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

	if err := store.PutEligibleFile(ctx, project, run, state, []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:a"}}, []Symbol{{Kind: SymbolKindFunction, Name: "A", StartLine: 1, EndLine: 1}}, nil, nil, nil, nil); err != nil {
		t.Fatalf("put initial file graph: %v", err)
	}
	otherState := testGraphState("cmd/b.go", "hash-b", "sha256:b")
	if err := store.PutEligibleFile(ctx, project, run, otherState, []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:b"}}, nil, nil, nil, nil, nil); err != nil {
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

func TestGraphStore_PutPreparedFilesBatchUsesProjectBatch(t *testing.T) {
	ctx := context.Background()
	graph := &projectBatchCountingGraph{MemoryGraph: ladybug.NewMemoryGraph()}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewGraphStore(graph)
	project := testGraphProject()
	run := testGraphRun(project.ID)

	err := store.PutPreparedFilesBatch(ctx, project, run, []PreparedGraphFile{{
		State:  testGraphState("cmd/a.go", "hash-a", "sha256:a"),
		Chunks: []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: "sha256:a"}},
	}})
	if err != nil {
		t.Fatalf("put graph batch: %v", err)
	}
	if graph.batchProjectCalls != 1 || graph.lastProjectID != project.ID {
		t.Fatalf("expected one project batch for %q, got calls=%d project=%q", project.ID, graph.batchProjectCalls, graph.lastProjectID)
	}
	if graph.genericBatchCalls != 0 {
		t.Fatalf("expected project-specific batch instead of generic batch, got %d generic calls", graph.genericBatchCalls)
	}
}

func TestGraphStore_ResolvesUnambiguousSamePackageCrossFileCallsAndReferences(t *testing.T) {
	ctx := context.Background()
	graph := ladybug.NewMemoryGraph()
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	store := NewGraphStore(graph)
	project := testGraphProject()
	run := testGraphRun(project.ID)

	calleeState := testGraphState("cmd/callee.go", "hash-callee", "sha256:callee")
	if err := store.PutEligibleFile(ctx, project, run, calleeState,
		[]Chunk{{Index: 0, StartLine: 1, EndLine: 5, Text: "package main\nfunc Target() {}\n", ContentSHA256: "sha256:callee"}},
		[]Symbol{{Kind: SymbolKindFunction, Name: "Target", PackageName: "main", StartLine: 2, EndLine: 2}},
		nil,
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatalf("put callee file: %v", err)
	}

	callerState := testGraphState("cmd/caller.go", "hash-caller", "sha256:caller")
	if err := store.PutEligibleFile(ctx, project, run, callerState,
		[]Chunk{{Index: 0, StartLine: 1, EndLine: 8, Text: "package main\nfunc Caller() { Target() }\n", ContentSHA256: "sha256:caller"}},
		[]Symbol{{Kind: SymbolKindFunction, Name: "Caller", PackageName: "main", StartLine: 2, EndLine: 2}},
		[]Reference{{Kind: "identifier", Name: "Target", TargetName: "Target", PackageName: "main", EnclosingSymbolName: "Caller", StartLine: 2, EndLine: 2}},
		[]Call{{CallerName: "Caller", CalleeName: "Target", StartLine: 2, EndLine: 2}},
		nil,
		nil,
	); err != nil {
		t.Fatalf("put caller file: %v", err)
	}

	targetID := codeSymbolID(repoFileID(project.GraphNamespace, calleeState.RelativePathHash), Symbol{Kind: SymbolKindFunction, Name: "Target", PackageName: "main", StartLine: 2, EndLine: 2})
	refs, err := store.ListSymbolReferences(ctx, project, targetID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list references: %v", err)
	}
	if len(refs.References) != 1 || refs.References[0].TargetSymbolID != targetID || refs.References[0].ResolutionStatus != "resolved" || refs.References[0].Confidence != "direct" {
		t.Fatalf("expected direct cross-file reference, got %#v", refs.References)
	}
	callers, err := store.ListSymbolCallers(ctx, project, targetID, Pagination{PageSize: MaxPageSize})
	if err != nil {
		t.Fatalf("list callers: %v", err)
	}
	if len(callers.Edges) != 1 || callers.Edges[0].CalleeSymbolID != targetID || callers.Edges[0].ResolutionStatus != "resolved" || callers.Edges[0].Confidence != "direct" {
		t.Fatalf("expected direct cross-file caller edge, got %#v", callers.Edges)
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
			if err := store.PutEligibleFile(ctx, project, run, state, []Chunk{{Index: 0, StartLine: 1, EndLine: 1, Text: "package main", ContentSHA256: state.ContentSHA256}}, nil, nil, nil, nil, nil); err != nil {
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

type projectBatchCountingGraph struct {
	*ladybug.MemoryGraph
	batchProjectCalls int
	genericBatchCalls int
	lastProjectID     string
}

func (graph *projectBatchCountingGraph) BatchProject(ctx context.Context, projectID string, fn func(ladybug.Graph) error) error {
	graph.batchProjectCalls++
	graph.lastProjectID = projectID
	return fn(graph.MemoryGraph)
}

func (graph *projectBatchCountingGraph) Batch(ctx context.Context, fn func(ladybug.Graph) error) error {
	graph.genericBatchCalls++
	return fn(graph.MemoryGraph)
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
