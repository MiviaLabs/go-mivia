package ladybug_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

func TestPersistentGraph_ReloadsNodesAndRelationships(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.lbug")

	graph, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	if err := graph.PutNode(ctx, ladybug.Node{
		Label: "Project",
		ID:    "project-1",
		Properties: map[string]string{
			"id":        "project-1",
			"namespace": "ns",
		},
	}); err != nil {
		t.Fatalf("put project node: %v", err)
	}
	if err := graph.PutNode(ctx, ladybug.Node{
		Label: "RepoFile",
		ID:    "file-1",
		Properties: map[string]string{
			"project_id": "project-1",
			"file_id":    "file-1",
		},
	}); err != nil {
		t.Fatalf("put file node: %v", err)
	}
	if err := graph.PutRelationship(ctx, ladybug.Relationship{
		Type: "PROJECT_HAS_REPO_FILE",
		From: ladybug.NodeRef{Label: "Project", ID: "project-1"},
		To:   ladybug.NodeRef{Label: "RepoFile", ID: "file-1"},
	}); err != nil {
		t.Fatalf("put relationship: %v", err)
	}

	reopened, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	node, err := reopened.GetNode(ctx, "RepoFile", "file-1")
	if err != nil {
		t.Fatalf("get reloaded node: %v", err)
	}
	if node.Properties["project_id"] != "project-1" {
		t.Fatalf("expected project_id to persist, got %#v", node.Properties)
	}
	listed, err := reopened.ListNodes(ctx, "RepoFile", map[string]string{"project_id": "project-1"})
	if err != nil {
		t.Fatalf("list reloaded nodes: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "file-1" {
		t.Fatalf("expected persisted file list, got %#v", listed)
	}
	if _, err := reopened.GetRelationship(ctx, "PROJECT_HAS_REPO_FILE", ladybug.NodeRef{Label: "Project", ID: "project-1"}, ladybug.NodeRef{Label: "RepoFile", ID: "file-1"}); err != nil {
		t.Fatalf("get reloaded relationship: %v", err)
	}
}

func TestPersistentGraph_BatchPersistsOnceAtCommit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.lbug")

	graph, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	if err := graph.Batch(ctx, func(batch ladybug.Graph) error {
		if err := batch.PutNode(ctx, ladybug.Node{
			Label: "Project",
			ID:    "project-1",
			Properties: map[string]string{
				"id": "project-1",
			},
		}); err != nil {
			return err
		}
		reopened, err := ladybug.OpenPersistentGraph(path)
		if err != nil {
			return err
		}
		if _, err := reopened.GetNode(ctx, "Project", "project-1"); !errors.Is(err, ladybug.ErrNodeNotFound) {
			t.Fatalf("expected batch node to remain unpersisted before commit, got %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("batch graph: %v", err)
	}

	reopened, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	if _, err := reopened.GetNode(ctx, "Project", "project-1"); err != nil {
		t.Fatalf("expected batch node to persist after commit: %v", err)
	}
}

func TestPersistentGraph_AppendsJournalInsteadOfRewritingSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.lbug")

	graph, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	if err := graph.Batch(ctx, func(batch ladybug.Graph) error {
		return batch.PutNode(ctx, ladybug.Node{
			Label: "Project",
			ID:    "project-1",
			Properties: map[string]string{
				"id": "project-1",
			},
		})
	}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read graph journal: %v", err)
	}
	if bytes.Contains(first, []byte(`"nodes"`)) {
		t.Fatalf("expected journal operations, got snapshot: %s", first)
	}

	if err := graph.Batch(ctx, func(batch ladybug.Graph) error {
		return batch.PutNode(ctx, ladybug.Node{
			Label: "RepoFile",
			ID:    "file-1",
			Properties: map[string]string{
				"project_id": "project-1",
			},
		})
	}); err != nil {
		t.Fatalf("second batch: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read graph journal: %v", err)
	}
	if !bytes.HasPrefix(second, first) {
		t.Fatalf("expected graph persistence to append operations instead of rewriting")
	}
	if bytes.Count(second, []byte(`"op"`)) != 3 {
		t.Fatalf("expected bootstrap plus two appended operations, got journal: %s", second)
	}
}

func TestPersistentGraph_ReplaysFileScopedDeletesWithoutStaleRelationships(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.lbug")

	graph, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}
	if err := graph.Bootstrap(ctx, schema.BootstrapSchema()); err != nil {
		t.Fatalf("bootstrap graph: %v", err)
	}
	for index := 0; index < 200; index++ {
		fileID := "file-deleted"
		chunkID := fileID + "-chunk-" + strconv.Itoa(index)
		symbolID := fileID + "-symbol-" + strconv.Itoa(index)
		if err := graph.PutNode(ctx, ladybug.Node{
			Label: "ContentChunk",
			ID:    chunkID,
			Properties: map[string]string{
				"project_id":   "project-1",
				"repo_file_id": fileID,
			},
		}); err != nil {
			t.Fatalf("put chunk node: %v", err)
		}
		if err := graph.PutNode(ctx, ladybug.Node{
			Label: "CodeSymbol",
			ID:    symbolID,
			Properties: map[string]string{
				"project_id":   "project-1",
				"repo_file_id": fileID,
			},
		}); err != nil {
			t.Fatalf("put symbol node: %v", err)
		}
		if err := graph.PutRelationship(ctx, ladybug.Relationship{
			Type: "SYMBOL_IN_CHUNK",
			From: ladybug.NodeRef{Label: "CodeSymbol", ID: symbolID},
			To:   ladybug.NodeRef{Label: "ContentChunk", ID: chunkID},
		}); err != nil {
			t.Fatalf("put relationship: %v", err)
		}
		if err := graph.DeleteNodes(ctx, "ContentChunk", map[string]string{
			"project_id":   "project-1",
			"repo_file_id": fileID,
		}); err != nil {
			t.Fatalf("delete chunks: %v", err)
		}
	}

	reopened, err := ladybug.OpenPersistentGraph(path)
	if err != nil {
		t.Fatalf("reopen graph: %v", err)
	}
	chunks, err := reopened.ListNodes(ctx, "ContentChunk", map[string]string{
		"project_id":   "project-1",
		"repo_file_id": "file-deleted",
	})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 0 {
		t.Fatalf("expected deleted chunks to stay deleted after replay, got %#v", chunks)
	}
	if _, err := reopened.GetRelationship(ctx, "SYMBOL_IN_CHUNK", ladybug.NodeRef{Label: "CodeSymbol", ID: "file-deleted-symbol-0"}, ladybug.NodeRef{Label: "ContentChunk", ID: "file-deleted-chunk-0"}); !errors.Is(err, ladybug.ErrRelationshipNotFound) {
		t.Fatalf("expected stale relationship to be removed after replay, got %v", err)
	}
}

func TestPersistentGraph_RejectsInvalidStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.lbug")
	if err := os.WriteFile(path, []byte("{invalid"), 0o600); err != nil {
		t.Fatalf("write corrupt graph: %v", err)
	}

	_, err := ladybug.OpenPersistentGraph(path)
	if err == nil {
		t.Fatal("expected corrupt graph store error")
	}
}

func TestPersistentGraph_MissingNode(t *testing.T) {
	graph, err := ladybug.OpenPersistentGraph(filepath.Join(t.TempDir(), "graph.lbug"))
	if err != nil {
		t.Fatalf("open graph: %v", err)
	}

	_, err = graph.GetNode(context.Background(), "Project", "missing")
	if !errors.Is(err, ladybug.ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got %v", err)
	}
}
